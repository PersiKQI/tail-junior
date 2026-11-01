package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"teyljunior/internal/progression"
	"teyljunior/internal/store"
)

// Message — сообщение провайдеру. Только текст шаблона и маршрут: без личных данных.
type Message struct {
	NotificationID string
	Provider       string
	Token          string
	Title          string
	Body           string
	Route          string
	TargetID       string
}

// Provider — внешний провайдер push. Ответ «принято» не означает доставку и прочтение.
type Provider interface {
	Send(ctx context.Context, m Message) (providerMessageID string, err error)
}

// ErrRejected — известный отказ провайдера: повтор с тем же notificationId без новой квоты.
// Любая другая ошибка считается неоднозначной (таймаут): статус unknown, автоматической
// повторной отправки нет (§18).
var ErrRejected = errors.New("provider rejected the message")

// DryRun — провайдер для разработки и пилота без реальной отправки людям (§18).
type DryRun struct {
	mu   sync.Mutex
	sent []Message
	// Respond позволяет тестам задать ответ; nil — принято.
	Respond func(Message) error
	// Log — журнал «отправленных» сообщений без токена; nil — без журнала.
	Log *slog.Logger
}

// dryRunKeep — сколько последних сообщений хранится в памяти.
const dryRunKeep = 1000

func (d *DryRun) Send(_ context.Context, m Message) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.Respond != nil {
		if err := d.Respond(m); err != nil {
			return "", err
		}
	}
	if d.Log != nil {
		d.Log.Info("dry-run notification", "notificationId", m.NotificationID, "route", m.Route, "title", m.Title)
	}
	d.sent = append(d.sent, m)
	if len(d.sent) > dryRunKeep {
		d.sent = slices.Clone(d.sent[len(d.sent)-dryRunKeep:])
	}
	return "dryrun-" + m.NotificationID, nil
}

// Sent — принятые dry-run сообщения.
func (d *DryRun) Sent() []Message {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.sent)
}

type trigger struct {
	key, kind, category, targetType, targetID string
	expiresAt                                 time.Time
}

func categoryOf(kind string) string {
	switch kind {
	case "new_episode":
		return "event"
	case "streak":
		return "streak"
	}
	return "learning"
}

// Tick — один проход доставки: восстановление зависших отправок, затем пользователи, у
// которых наступило выбранное время. Возвращает число отправленных сообщений.
func (s *Service) Tick(ctx context.Context, db *store.DB) (int, error) {
	now := s.Clock.Now()
	// Неоднозначный исход отправки после падения обработчика — unknown, не повторяем.
	if _, err := db.Pool.Exec(ctx, `UPDATE notification_jobs SET state = 'unknown', lease_until = NULL
		WHERE state = 'submitting' AND lease_until < $1`, now); err != nil {
		return 0, err
	}
	rows, err := db.Pool.Query(ctx, `
		SELECT p.user_id FROM reminder_preferences p JOIN devices d ON d.user_id = p.user_id
		WHERE p.mode <> 'off' AND d.permission = 'granted' AND d.push_token_ciphertext IS NOT NULL
		ORDER BY p.user_id`)
	if err != nil {
		return 0, err
	}
	users, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, u := range users {
		ok, err := s.deliver(ctx, db, u, now)
		if err != nil {
			return sent, fmt.Errorf("user %s: %w", u, err)
		}
		if ok {
			sent++
		}
	}
	return sent, nil
}

type pending struct {
	jobID, provider, token, triggerType string
	tpl                                 Template
	targetID                            string
}

// deliver: в первой транзакции перед отправкой заново проверяются согласие, настройки,
// существование и свежесть повода, квота и отсутствие выполненного сегодня действия, и квота
// атомарно резервируется. Отправка — вне транзакции. Результат фиксируется второй транзакцией.
func (s *Service) deliver(ctx context.Context, db *store.DB, userID string, now time.Time) (bool, error) {
	var job *pending
	err := db.InTx(ctx, func(tx pgx.Tx) error {
		job = nil
		if err := store.LockUser(ctx, tx, userID); err != nil {
			return err
		}
		var err error
		job, err = s.reserve(ctx, tx, userID, now)
		return err
	})
	if err != nil || job == nil {
		return false, err
	}
	msgID, sendErr := s.Provider.Send(ctx, Message{NotificationID: job.jobID, Provider: job.provider, Token: job.token,
		Title: job.tpl.Title, Body: job.tpl.Body, Route: job.tpl.TargetRoute, TargetID: job.targetID})
	err = db.InTx(ctx, func(tx pgx.Tx) error {
		switch {
		case sendErr == nil:
			if _, err := tx.Exec(ctx, `UPDATE notification_jobs SET state = 'provider_accepted', provider_message_id = $2,
				provider_accepted_at = $3, lease_until = NULL WHERE id = $1`, job.jobID, msgID, now); err != nil {
				return err
			}
			if err := s.Prog.NotificationAccepted(ctx, tx, userID, job.jobID, job.triggerType, now); err != nil {
				return err
			}
			return s.autoPause(ctx, tx, userID, now)
		case errors.Is(sendErr, ErrRejected):
			_, err := tx.Exec(ctx, `UPDATE notification_jobs SET state = 'failed', last_error_code = 'rejected', lease_until = NULL WHERE id = $1`, job.jobID)
			return err
		default:
			_, err := tx.Exec(ctx, `UPDATE notification_jobs SET state = 'unknown', last_error_code = 'ambiguous', lease_until = NULL WHERE id = $1`, job.jobID)
			return err
		}
	})
	return sendErr == nil && err == nil, err
}

func (s *Service) reserve(ctx context.Context, tx pgx.Tx, userID string, now time.Time) (*pending, error) {
	var mode, localTime string
	var days []int
	var cats []string
	var pauseUntil, autoPauseUntil, autoPausedAt *time.Time
	var tz, character *string
	var provider string
	var sealed []byte
	err := tx.QueryRow(ctx, `
		SELECT p.mode, p.days_iso, p.local_time, p.categories, p.pause_until, p.auto_pause_until, p.auto_paused_at,
		       u.learning_timezone, u.character_id, d.provider, d.push_token_ciphertext
		FROM reminder_preferences p JOIN users u ON u.id = p.user_id JOIN devices d ON d.user_id = p.user_id
		WHERE p.user_id = $1 AND p.mode <> 'off' AND d.permission = 'granted' AND d.push_token_ciphertext IS NOT NULL`, userID).
		Scan(&mode, &days, &localTime, &cats, &pauseUntil, &autoPauseUntil, &autoPausedAt, &tz, &character, &provider, &sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil // согласие отозвано между выборкой и захватом
	}
	if err != nil {
		return nil, err
	}
	name := progression.DefaultTimezone
	if tz != nil {
		name = *tz
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, err
	}
	local := now.In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	if pauseUntil != nil && !pauseUntil.Before(today) {
		return nil, nil
	}
	if autoPauseUntil != nil && now.Before(*autoPauseUntil) {
		// Автопаузу снимает только учебное действие после неё (§18).
		var returned bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM study_days WHERE user_id = $1 AND kind = 'active' AND created_at > $2)`,
			userID, *autoPausedAt).Scan(&returned); err != nil {
			return nil, err
		}
		if !returned {
			return nil, nil
		}
		if _, err := tx.Exec(ctx, `UPDATE reminder_preferences SET auto_pause_until = NULL, auto_paused_at = NULL WHERE user_id = $1`, userID); err != nil {
			return nil, err
		}
	}
	weekday := int(local.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	if mode == "three_per_week" && !slices.Contains(days, weekday) {
		return nil, nil
	}
	minutes, _ := minutesOf(localTime)
	slot := time.Date(local.Year(), local.Month(), local.Day(), minutes/60, minutes%60, 0, 0, loc)
	quiet, _ := minutesOf(s.Rules.QuietHours.From)
	morning, _ := minutesOf(s.Rules.QuietHours.To)
	nowMin := local.Hour()*60 + local.Minute()
	if now.Before(slot) || !now.Before(slot.Add(time.Duration(s.Rules.DeliveryWindowMinutes)*time.Minute)) || nowMin >= quiet || nowMin < morning {
		return nil, nil
	}
	// После зачётного действия сегодня необязательное приглашение не нужно.
	var activeToday bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM study_days WHERE user_id = $1 AND local_date = $2 AND kind = 'active')`,
		userID, today).Scan(&activeToday); err != nil || activeToday {
		return nil, err
	}
	modeMax := s.Rules.ThreePerWeekMaxPer168h
	if mode == "daily" {
		modeMax = s.Rules.DailyMaxPer168h
	}
	// Квота считается из резервирования, включая unknown и известные отказы.
	var day, week int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE reserved_at > $2), count(*) FILTER (WHERE reserved_at > $3)
		FROM notification_jobs WHERE user_id = $1 AND reserved_at IS NOT NULL AND state <> 'cancelled'`,
		userID, now.Add(-24*time.Hour), now.Add(-168*time.Hour)).Scan(&day, &week); err != nil {
		return nil, err
	}
	triggers, err := s.triggers(ctx, tx, userID, today, now)
	if err != nil {
		return nil, err
	}
	charID := "lumi"
	if character != nil {
		charID = *character
	}
	token, err := s.open(sealed)
	if err != nil {
		return nil, err
	}

	// Повтор известного отказа: тот же notificationId, новая квота не резервируется.
	var retryID, retryKind, retryTarget, retryKey string
	err = tx.QueryRow(ctx, `SELECT id, trigger_type, target_id, trigger_key FROM notification_jobs
		WHERE user_id = $1 AND state = 'failed' AND attempts < $2 AND expires_at > $3 ORDER BY reserved_at LIMIT 1`,
		userID, s.Rules.MaxProviderAttempts, now).Scan(&retryID, &retryKind, &retryTarget, &retryKey)
	if err == nil && slices.ContainsFunc(triggers, func(t trigger) bool { return t.key == retryKey }) {
		if _, err := tx.Exec(ctx, `UPDATE notification_jobs SET state = 'submitting', attempts = attempts + 1, lease_until = $2 WHERE id = $1`,
			retryID, now.Add(s.Lease)); err != nil {
			return nil, err
		}
		return &pending{jobID: retryID, provider: provider, token: token, triggerType: retryKind,
			tpl: s.Templates[retryKind+"."+charID], targetID: retryTarget}, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	if day >= s.Rules.GlobalMaxPer24h || week >= modeMax {
		return nil, nil
	}
	for _, t := range triggers {
		if !slices.Contains(cats, t.category) {
			continue
		}
		tpl := s.Templates[t.kind+"."+charID]
		id := s.IDs.New("ntf")
		tag, err := tx.Exec(ctx, `
			INSERT INTO notification_jobs (id, user_id, trigger_key, trigger_type, category, template_id, template_revision,
			                               target_type, target_id, character_id, state, scheduled_at, expires_at, reserved_at,
			                               lease_until, attempts, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'submitting', $11, $12, $13, $14, 1, $13)
			ON CONFLICT (user_id, trigger_key) DO NOTHING`,
			id, userID, t.key, t.kind, t.category, tpl.TemplateID, tpl.Revision, t.targetType, t.targetID, charID,
			slot, t.expiresAt, now, now.Add(s.Lease))
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 0 {
			continue // этот повод уже использован
		}
		return &pending{jobID: id, provider: provider, token: token, triggerType: t.kind, tpl: tpl, targetID: t.targetID}, nil
	}
	return nil, nil
}

// triggers — действующие поводы в порядке приоритета §18: готовый review, незаконченная
// попытка, новый эпизод, мягкое возвращение, серия.
func (s *Service) triggers(ctx context.Context, tx pgx.Tx, userID string, today, now time.Time) ([]trigger, error) {
	day := today.Format(time.DateOnly)
	expires := now.Add(24 * time.Hour)
	var out []trigger
	add := func(key, kind, targetType, targetID string) {
		out = append(out, trigger{key: key, kind: kind, category: categoryOf(kind), targetType: targetType, targetID: targetID, expiresAt: expires})
	}
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM assessment_sets WHERE user_id = $1 AND kind = 'review' AND finalized_at IS NULL
		AND (status = 'in_progress' OR (status = 'waiting' AND due_at <= $2)) ORDER BY due_at LIMIT 1`, userID, now).Scan(&id)
	if err == nil {
		add("review:"+id, "review_ready", "assessment_set", id)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	err = tx.QueryRow(ctx, `SELECT id FROM attempts WHERE user_id = $1 AND status = 'started' ORDER BY issued_at DESC LIMIT 1`, userID).Scan(&id)
	if err == nil {
		add("attempt:"+id, "unfinished_attempt", "attempt", id)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	var family string
	var revision int
	err = tx.QueryRow(ctx, `
		SELECT c.family_id, c.revision FROM challenge_releases c
		WHERE c.status = 'published' AND c.starts_at <= $2 AND c.ends_at > $2
		  AND NOT EXISTS (SELECT 1 FROM challenge_participations p WHERE p.user_id = $1 AND p.family_id = c.family_id AND p.completed_at IS NOT NULL)
		ORDER BY c.starts_at DESC LIMIT 1`, userID, now).Scan(&family, &revision)
	if err == nil {
		add(fmt.Sprintf("event:%s:%d", family, revision), "new_episode", "challenge", family)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	var last *time.Time
	var yesterday bool
	if err := tx.QueryRow(ctx, `
		SELECT (SELECT max(local_date) FROM study_days WHERE user_id = $1 AND kind = 'active'),
		       EXISTS (SELECT 1 FROM study_days WHERE user_id = $1 AND local_date = $2)`, userID, today.AddDate(0, 0, -1)).Scan(&last, &yesterday); err != nil {
		return nil, err
	}
	if last != nil && !last.After(today.AddDate(0, 0, -s.Rules.GentleReturnAfterDays)) {
		add("return:"+day, "gentle_return", "next_step", day)
	}
	if yesterday {
		add("streak:"+day, "streak", "next_step", day)
	}
	return out, nil
}

// autoPause: после двух принятых провайдером приглашений без учебного возвращения за семь
// дней новые приглашения ставятся на паузу на семь дней. Это снижение частоты, а не вывод
// «прочитал и проигнорировал» (§18).
func (s *Service) autoPause(ctx context.Context, tx pgx.Tx, userID string, now time.Time) error {
	r := s.Rules.AutoPause
	var n int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM notification_jobs j
		WHERE j.user_id = $1 AND j.state = 'provider_accepted' AND j.provider_accepted_at > $2
		  AND NOT EXISTS (SELECT 1 FROM study_days d WHERE d.user_id = j.user_id AND d.kind = 'active' AND d.created_at > j.provider_accepted_at)`,
		userID, now.AddDate(0, 0, -r.WindowDays)).Scan(&n); err != nil {
		return err
	}
	if n < r.AcceptedWithoutReturn {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE reminder_preferences SET auto_pause_until = $2, auto_paused_at = $3
		WHERE user_id = $1 AND (auto_pause_until IS NULL OR auto_pause_until < $3)`, userID, now.AddDate(0, 0, r.PauseDays), now)
	return err
}

// MarkOpened отмечает открытие по событию клиента. Содержательное возвращение — учебное
// действие после открытия — считается в аналитике и не доказывает эффект сообщения.
func MarkOpened(ctx context.Context, tx pgx.Tx, userID, notificationID string, at time.Time) error {
	if !strings.HasPrefix(notificationID, "ntf_") {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE notification_jobs SET opened_at = coalesce(opened_at, $3)
		WHERE user_id = $1 AND id = $2 AND state IN ('provider_accepted', 'unknown')`, userID, notificationID, at)
	return err
}
