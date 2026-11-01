// Пакет analytics — экспорт состоявшихся фактов и клиентских наблюдений получателю аналитики
// через outbox (спецификация §21, §22). Гарантии: один доменный факт со стабильным eventId,
// повторяемая обработка и дедупликация на стороне получателя; exactly once не обещаем.
package analytics

import (
	"cmp"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"teyljunior/internal/core"
	"teyljunior/internal/store"
)

// Event — экспортируемое событие. Subject — псевдоним пользователя, а не userId; в properties
// нет сумм, заметок, распределений, свободного текста и токенов.
type Event struct {
	EventID         string         `json:"eventId"`
	EventName       string         `json:"eventName"`
	SchemaVersion   int            `json:"schemaVersion"`
	Origin          string         `json:"origin"` // server | client
	OccurredAt      time.Time      `json:"occurredAt"`
	ReceivedAt      *time.Time     `json:"receivedAt,omitempty"`
	Subject         string         `json:"subject"`
	SourceType      string         `json:"sourceType"`
	SourceID        string         `json:"sourceId"`
	ProgramVersion  *string        `json:"programVersion"`
	ContentFamilyID *string        `json:"contentFamilyId"`
	CohortID        *string        `json:"cohortId"`
	Properties      map[string]any `json:"properties"`
}

// Sink — получатель. Должен быть идемпотентным по EventID.
type Sink interface {
	Export(ctx context.Context, events []Event) error
}

// LogSink пишет события в журнал — dry-run получатель для разработки.
type LogSink struct{ Log *slog.Logger }

func (s LogSink) Export(_ context.Context, events []Event) error {
	for _, e := range events {
		s.Log.Info("analytics event", "eventId", e.EventID, "eventName", e.EventName, "subject", e.Subject)
	}
	return nil
}

// MemorySink — получатель в памяти с дедупликацией по EventID (тесты, локальный стенд).
type MemorySink struct {
	mu     sync.Mutex
	events map[string]Event
	Fail   error // если задана, Export возвращает её
}

func (s *MemorySink) Export(_ context.Context, events []Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Fail != nil {
		return s.Fail
	}
	if s.events == nil {
		s.events = map[string]Event{}
	}
	for _, e := range events {
		s.events[e.EventID] = e
	}
	return nil
}

// Events — полученные события по порядку eventId.
func (s *MemorySink) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.SortedFunc(maps.Values(s.events), func(a, b Event) int { return cmp.Compare(a.EventID, b.EventID) })
}

// safeKeys — ключи safe_payload фактов, которые можно передать получателю.
var safeKeys = []string{"role", "familyId", "programVersion", "kind", "status", "caseFamilyId", "basisKey", "xp", "badgeId", "triggerType"}

// Exporter забирает готовые строки outbox, собирает события и передаёт их получателю.
type Exporter struct {
	DB         *store.DB
	Sink       Sink
	Clock      core.Clock
	SubjectKey []byte        // ключ HMAC для псевдонима пользователя
	Lease      time.Duration // сколько строка закреплена за обработчиком
	Batch      int
}

// Subject — псевдоним пользователя для внешней аналитики.
func (x *Exporter) Subject(userID string) string {
	m := hmac.New(sha256.New, x.SubjectKey)
	m.Write([]byte(userID))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

const maxAttempts = 10

type row struct {
	id, userID, kind, sourceID string
	attempts                   int
}

// Tick обрабатывает одну партию. Строки забираются с блокировкой SKIP LOCKED и lease:
// истёкший lease возвращает работу в очередь, повтор безопасен благодаря стабильному eventId.
// Возвращает число экспортированных строк.
func (x *Exporter) Tick(ctx context.Context) (int, error) {
	now := x.Clock.Now()
	var rows []row
	err := x.DB.InTx(ctx, func(tx pgx.Tx) error {
		r, err := tx.Query(ctx, `
			SELECT id, user_id, source_kind, source_id, attempts FROM outbox
			WHERE destination = 'analytics' AND available_at <= $1
			  AND (state = 'pending' OR (state = 'processing' AND lease_until < $1))
			ORDER BY available_at, id LIMIT $2 FOR UPDATE SKIP LOCKED`, now, x.Batch)
		if err != nil {
			return err
		}
		if rows, err = pgx.CollectRows(r, func(c pgx.CollectableRow) (row, error) {
			var o row
			return o, c.Scan(&o.id, &o.userID, &o.kind, &o.sourceID, &o.attempts)
		}); err != nil {
			return err
		}
		ids := make([]string, len(rows))
		for i, o := range rows {
			ids[i] = o.id
		}
		_, err = tx.Exec(ctx, `UPDATE outbox SET state = 'processing', lease_until = $2 WHERE id = ANY($1)`, ids, now.Add(x.Lease))
		return err
	})
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	events := make([]Event, 0, len(rows))
	for _, o := range rows {
		e, err := x.build(ctx, o)
		if err != nil {
			return 0, err
		}
		events = append(events, e)
	}
	ids := make([]string, len(rows))
	for i, o := range rows {
		ids[i] = o.id
	}
	if exportErr := x.Sink.Export(ctx, events); exportErr != nil {
		// Временная ошибка получателя: повтор с нарастающей задержкой, после maxAttempts — dead.
		_, err := x.DB.Pool.Exec(ctx, `
			UPDATE outbox SET attempts = attempts + 1, last_error = $2, lease_until = NULL,
			       state = CASE WHEN attempts + 1 >= $3 THEN 'dead' ELSE 'pending' END,
			       available_at = $4::timestamptz + make_interval(secs => least(3600, power(2, attempts + 1)))
			WHERE id = ANY($1)`, ids, exportErr.Error(), maxAttempts, now)
		return 0, err
	}
	_, err = x.DB.Pool.Exec(ctx, `UPDATE outbox SET state = 'done', done_at = $2, lease_until = NULL WHERE id = ANY($1)`, ids, now)
	return len(rows), err
}

func (x *Exporter) build(ctx context.Context, o row) (Event, error) {
	e := Event{EventID: o.sourceID, Subject: x.Subject(o.userID), Properties: map[string]any{}}
	if err := x.DB.Pool.QueryRow(ctx, `SELECT program_version, cohort_id FROM enrollments WHERE user_id = $1`, o.userID).
		Scan(&e.ProgramVersion, &e.CohortID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return e, err
	}
	if o.kind == "client_event" {
		var target *string
		e.Origin, e.SchemaVersion, e.SourceType = "client", 1, "client_event"
		err := x.DB.Pool.QueryRow(ctx, `SELECT client_event_id, name, client_occurred_at, received_at, target_id FROM client_events
			WHERE user_id = $1 AND client_event_id = $2`, o.userID, o.sourceID).Scan(&e.SourceID, &e.EventName, &e.OccurredAt, &e.ReceivedAt, &target)
		if target != nil {
			e.Properties["targetId"] = *target
		}
		e.EventID = "client:" + x.Subject(o.userID) + ":" + o.sourceID
		return e, err
	}
	var payload []byte
	e.Origin = "server"
	if err := x.DB.Pool.QueryRow(ctx, `SELECT name, schema_version, occurred_at, source_type, source_id, safe_payload FROM domain_facts WHERE id = $1`,
		o.sourceID).Scan(&e.EventName, &e.SchemaVersion, &e.OccurredAt, &e.SourceType, &e.SourceID, &payload); err != nil {
		return e, err
	}
	var p map[string]any
	if err := json.Unmarshal(payload, &p); err != nil {
		return e, err
	}
	for _, k := range safeKeys {
		if v, ok := p[k]; ok {
			e.Properties[k] = v
		}
	}
	if f, ok := p["familyId"].(string); ok {
		e.ContentFamilyID = &f
	}
	e.OccurredAt = e.OccurredAt.UTC()
	return e, nil
}
