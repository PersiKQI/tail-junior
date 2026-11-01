package api

import (
	"context"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"teyljunior/internal/core"
	"teyljunior/internal/httpx"
	"teyljunior/internal/notify"
)

func (s *server) getReminders(w http.ResponseWriter, r *http.Request, userID string) {
	v, err := s.notify.GetPreferences(r.Context(), s.db.Pool, userID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, v)
}

type remindersRequest struct {
	ExpectedRevision *int     `json:"expectedRevision"`
	Mode             string   `json:"mode"`
	DaysIso          []int    `json:"daysIso"`
	LocalTime        string   `json:"localTime"`
	Categories       []string `json:"categories"`
	PauseUntil       *string  `json:"pauseUntil"`
}

func (s *server) putReminders(w http.ResponseWriter, r *http.Request, userID string) {
	var req remindersRequest
	s.mutation(w, r, userID, "PUT /reminders", &req, func(ctx context.Context, tx pgx.Tx, now time.Time) (int, any, error) {
		if req.ExpectedRevision == nil {
			return 0, nil, core.Validation("expectedRevision", "обязательное поле")
		}
		v, err := s.notify.PutPreferences(ctx, tx, userID, notify.PreferencesInput{ExpectedRevision: *req.ExpectedRevision, Mode: req.Mode,
			DaysIso: req.DaysIso, LocalTime: req.LocalTime, Categories: req.Categories, PauseUntil: req.PauseUntil}, now)
		return http.StatusOK, v, err
	})
}

type deviceRequest struct {
	InstallationID string  `json:"installationId"`
	Platform       string  `json:"platform"`
	Provider       string  `json:"provider"`
	PushToken      *string `json:"pushToken"`
	Permission     string  `json:"permission"`
}

// putDevice — состояние установки берётся из ОС, поэтому expectedRevision не нужен: новое
// состояние всегда заменяет прежнее.
func (s *server) putDevice(w http.ResponseWriter, r *http.Request, userID string) {
	var req deviceRequest
	s.mutation(w, r, userID, "PUT /device", &req, func(ctx context.Context, tx pgx.Tx, now time.Time) (int, any, error) {
		v, err := s.notify.PutDevice(ctx, tx, userID, notify.DeviceInput{InstallationID: req.InstallationID, Platform: req.Platform,
			Provider: req.Provider, PushToken: req.PushToken, Permission: req.Permission}, now)
		return http.StatusOK, v, err
	})
}

// clientEventNames — allowlist клиентских наблюдений (§22). Суммы, цели, заметки, свободный
// текст, распределения и токены сюда не попадают: лишнее поле отклоняется разбором.
var clientEventNames = []string{"app_opened", "assignment_opened", "explanation_opened", "lab_result_viewed", "challenge_viewed", "notification_opened"}

var clientEventID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

// maxTelemetryEvents — не больше 20 событий в пакете (§20).
const maxTelemetryEvents = 20

type telemetryEvent struct {
	ClientEventID    string     `json:"clientEventId"`
	Name             string     `json:"name"`
	ClientOccurredAt *time.Time `json:"clientOccurredAt"`
	TargetID         *string    `json:"targetId"`
}

type telemetryRequest struct {
	Events []telemetryEvent `json:"events"`
}

type telemetryResponse struct {
	Accepted   int  `json:"accepted"`
	Duplicates int  `json:"duplicates"`
	Stored     bool `json:"stored"`
}

// postTelemetry принимает наблюдения клиента. Они не начисляют награды. Хранятся и
// экспортируются только при включённой аналитике; notification_opened отмечает открытие
// уведомления всегда — это операционный факт для частоты приглашений.
func (s *server) postTelemetry(w http.ResponseWriter, r *http.Request, userID string) {
	var req telemetryRequest
	s.mutation(w, r, userID, "POST /telemetry", &req, func(ctx context.Context, tx pgx.Tx, now time.Time) (int, any, error) {
		if len(req.Events) == 0 || len(req.Events) > maxTelemetryEvents {
			return 0, nil, core.Validation("events", "от 1 до 20 событий")
		}
		for i, e := range req.Events {
			field := "events[" + strconv.Itoa(i) + "]."
			switch {
			case !clientEventID.MatchString(e.ClientEventID):
				return 0, nil, core.Validation(field+"clientEventId", "1–80 символов A-Z, a-z, 0-9, _ и -")
			case !slices.Contains(clientEventNames, e.Name):
				return 0, nil, core.Validation(field+"name", "событие не входит в allowlist")
			case e.ClientOccurredAt == nil:
				return 0, nil, core.Validation(field+"clientOccurredAt", "обязательное поле")
			case e.TargetID != nil && (len(*e.TargetID) == 0 || len(*e.TargetID) > 128):
				return 0, nil, core.Validation(field+"targetId", "от 1 до 128 символов")
			}
		}
		var enabled bool
		if err := tx.QueryRow(ctx, `SELECT analytics_enabled FROM users WHERE id = $1`, userID).Scan(&enabled); err != nil {
			return 0, nil, err
		}
		resp := telemetryResponse{Stored: enabled}
		for _, e := range req.Events {
			if e.Name == "notification_opened" && e.TargetID != nil {
				if err := notify.MarkOpened(ctx, tx, userID, *e.TargetID, now); err != nil {
					return 0, nil, err
				}
			}
			if !enabled {
				resp.Accepted++
				continue
			}
			tag, err := tx.Exec(ctx, `
				INSERT INTO client_events (user_id, client_event_id, name, client_occurred_at, received_at, target_id)
				VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT DO NOTHING`,
				userID, e.ClientEventID, e.Name, e.ClientOccurredAt.UTC(), now, e.TargetID)
			if err != nil {
				return 0, nil, err
			}
			if tag.RowsAffected() == 0 {
				resp.Duplicates++
				continue
			}
			resp.Accepted++
			if _, err := tx.Exec(ctx, `
				INSERT INTO outbox (id, user_id, source_kind, source_id, destination, state, available_at, created_at)
				VALUES ($1, $2, 'client_event', $3, 'analytics', 'pending', $4, $4) ON CONFLICT DO NOTHING`,
				s.ids.New("obx"), userID, e.ClientEventID, now); err != nil {
				return 0, nil, err
			}
		}
		return http.StatusOK, resp, nil
	})
}
