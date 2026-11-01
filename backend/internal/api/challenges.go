package api

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"teyljunior/internal/challenge"
	"teyljunior/internal/core"
	"teyljunior/internal/httpx"
	"teyljunior/internal/progression"
)

// getChallenges — активные, предстоящие и архивные события; завершение досчитывается по
// фактам лаборатории, поэтому чтение идёт в транзакции.
func (s *server) getChallenges(w http.ResponseWriter, r *http.Request, userID string) {
	var items []challenge.View
	err := s.locked(r.Context(), userID, func(ctx context.Context, tx pgx.Tx) (err error) {
		items, err = s.challenge.List(ctx, tx, userID, s.clock.Now())
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, itemsResponse[challenge.View]{Items: items})
}

func (s *server) postParticipation(w http.ResponseWriter, r *http.Request, userID string) {
	var req emptyRequest
	s.mutation(w, r, userID, "POST /challenges/{familyId}/participation", &req, func(ctx context.Context, tx pgx.Tx, now time.Time) (int, any, error) {
		v, ok, err := s.challenge.Join(ctx, tx, userID, r.PathValue("familyId"), now)
		return created(ok), v, err
	})
}

func (s *server) getParticipation(w http.ResponseWriter, r *http.Request, userID string) {
	var v challenge.View
	err := s.locked(r.Context(), userID, func(ctx context.Context, tx pgx.Tx) (err error) {
		v, err = s.challenge.Get(ctx, tx, userID, r.PathValue("familyId"), s.clock.Now())
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, v)
}

func (s *server) getInventory(w http.ResponseWriter, r *http.Request, userID string) {
	var v progression.Inventory
	err := s.locked(r.Context(), userID, func(ctx context.Context, tx pgx.Tx) (err error) {
		v, err = s.prog.Inventory(ctx, tx, userID, s.clock.Now())
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, v)
}

type equipmentRequest struct {
	ExpectedRevision *int               `json:"expectedRevision"`
	Equipment        map[string]*string `json:"equipment"`
}

func (s *server) putEquipment(w http.ResponseWriter, r *http.Request, userID string) {
	var req equipmentRequest
	s.mutation(w, r, userID, "PUT /inventory/equipment", &req, func(ctx context.Context, tx pgx.Tx, now time.Time) (int, any, error) {
		if req.ExpectedRevision == nil || req.Equipment == nil {
			return 0, nil, core.Validation("equipment", "нужны expectedRevision и equipment")
		}
		v, err := s.prog.Equip(ctx, tx, userID, *req.ExpectedRevision, req.Equipment, now)
		return http.StatusOK, v, err
	})
}
