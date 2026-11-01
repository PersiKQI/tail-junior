package api

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"teyljunior/internal/httpx"
)

// getLegacyAssessment — перенесённое окно старого D7 (§23); без окна — 404.
func (s *server) getLegacyAssessment(w http.ResponseWriter, r *http.Request, userID string) {
	v, err := s.d7.Get(r.Context(), s.db.Pool, userID, s.clock.Now())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, v)
}

type legacyAnswerRequest struct {
	ChoiceID string `json:"choiceId"`
	ReasonID string `json:"reasonId"`
}

func (s *server) postLegacyAnswer(w http.ResponseWriter, r *http.Request, userID string) {
	var req legacyAnswerRequest
	s.mutation(w, r, userID, "POST /legacy/assessment/answer", &req, func(ctx context.Context, tx pgx.Tx, now time.Time) (int, any, error) {
		v, err := s.d7.Answer(ctx, tx, userID, req.ChoiceID, req.ReasonID, now)
		return http.StatusOK, v, err
	})
}
