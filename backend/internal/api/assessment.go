package api

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"teyljunior/internal/assessment"
	"teyljunior/internal/httpx"
	"teyljunior/internal/learning"
	"teyljunior/internal/progression"
	"teyljunior/internal/store"
)

type setsResponse struct {
	Items []assessment.SetView `json:"items"`
}

// getSets — состояния наборов; результаты только у финализированных (§9).
func (s *server) getSets(w http.ResponseWriter, r *http.Request, userID string) {
	items, err := s.assess.List(r.Context(), s.db.Pool, userID, s.clock.Now())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, setsResponse{Items: items})
}

// emptyRequest — команда без параметров; тело {} нужно для единого пути мутации.
type emptyRequest struct{}

func (s *server) postDiagnostic(w http.ResponseWriter, r *http.Request, userID string) {
	var req emptyRequest
	s.mutation(w, r, userID, "POST /assessment-sets/diagnostic", &req, func(ctx context.Context, tx pgx.Tx, now time.Time) (int, any, error) {
		v, ok, err := s.assess.CreateDiagnostic(ctx, tx, userID, now)
		return created(ok), v, err
	})
}

func (s *server) postFinalization(w http.ResponseWriter, r *http.Request, userID string) {
	var req emptyRequest
	s.mutation(w, r, userID, "POST /assessment-sets/{setId}/finalization", &req, func(ctx context.Context, tx pgx.Tx, now time.Time) (int, any, error) {
		v, err := s.assess.Finalize(ctx, tx, userID, r.PathValue("setId"), now)
		return http.StatusOK, v, err
	})
}

// getNextStep может создать назначенный повтор, поэтому идёт в транзакции с захватом
// пользователя.
func (s *server) getNextStep(w http.ResponseWriter, r *http.Request, userID string) {
	var v learning.NextStep
	err := s.locked(r.Context(), userID, func(ctx context.Context, tx pgx.Tx) (err error) {
		v, err = s.learning.Next(ctx, tx, userID, s.clock.Now())
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, v)
}

// getProgress обрабатывает завершённые учебные дни перед чтением серии (§15).
func (s *server) getProgress(w http.ResponseWriter, r *http.Request, userID string) {
	var v progression.Progress
	err := s.locked(r.Context(), userID, func(ctx context.Context, tx pgx.Tx) (err error) {
		v, err = s.prog.Progress(ctx, tx, userID, s.clock.Now())
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, v)
}

// locked — транзакция с захваченной строкой пользователя для чтений с побочными эффектами.
func (s *server) locked(ctx context.Context, userID string, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		if err := store.LockUser(ctx, tx, userID); err != nil {
			return err
		}
		return fn(ctx, tx)
	})
}
