package api

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"teyljunior/internal/core"
	"teyljunior/internal/httpx"
	"teyljunior/internal/lab"
)

type itemsResponse[T any] struct {
	Items []T `json:"items"`
}

// getLabCases — условия кейсов без числовых исходов сценариев (§10).
func (s *server) getLabCases(w http.ResponseWriter, r *http.Request, _ string) {
	items, err := s.lab.ListCases(r.Context(), s.db.Pool)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, itemsResponse[lab.CaseView]{Items: items})
}

func (s *server) getExperiments(w http.ResponseWriter, r *http.Request, userID string) {
	items, err := s.lab.ListExperiments(r.Context(), s.db.Pool, userID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, itemsResponse[lab.ExperimentView]{Items: items})
}

type experimentRequest struct {
	CaseFamilyID string `json:"caseFamilyId"`
}

func (s *server) postExperiment(w http.ResponseWriter, r *http.Request, userID string) {
	var req experimentRequest
	s.mutation(w, r, userID, "POST /lab/experiments", &req, func(ctx context.Context, tx pgx.Tx, now time.Time) (int, any, error) {
		if req.CaseFamilyID == "" {
			return 0, nil, core.Validation("caseFamilyId", "обязательное поле")
		}
		v, ok, err := s.lab.CreateExperiment(ctx, tx, userID, req.CaseFamilyID, now)
		return created(ok), v, err
	})
}

func (s *server) getExperiment(w http.ResponseWriter, r *http.Request, userID string) {
	v, err := s.lab.GetExperiment(r.Context(), s.db.Pool, userID, r.PathValue("experimentId"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, v)
}

type decisionRequest struct {
	Allocation *lab.Allocation `json:"allocation"`
	ReasonID   string          `json:"reasonId"`
	Note       *string         `json:"note"`
}

func (s *server) postDecision(w http.ResponseWriter, r *http.Request, userID string) {
	var req decisionRequest
	s.mutation(w, r, userID, "POST /lab/experiments/{experimentId}/decisions", &req, func(ctx context.Context, tx pgx.Tx, now time.Time) (int, any, error) {
		if req.Allocation == nil {
			return 0, nil, core.Validation("allocation", "обязательное поле")
		}
		v, err := s.lab.CommitDecision(ctx, tx, userID, r.PathValue("experimentId"),
			lab.DecisionInput{Allocation: *req.Allocation, ReasonID: req.ReasonID, Note: req.Note}, now)
		return http.StatusCreated, v, err
	})
}

func (s *server) getDecision(w http.ResponseWriter, r *http.Request, userID string) {
	v, err := s.lab.GetDecision(r.Context(), s.db.Pool, userID, r.PathValue("decisionId"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, v)
}

type runRequest struct {
	DecisionID string `json:"decisionId"`
	ScenarioID string `json:"scenarioId"`
}

func (s *server) postRun(w http.ResponseWriter, r *http.Request, userID string) {
	var req runRequest
	s.mutation(w, r, userID, "POST /lab/experiments/{experimentId}/runs", &req, func(ctx context.Context, tx pgx.Tx, now time.Time) (int, any, error) {
		v, ok, err := s.lab.CreateRun(ctx, tx, userID, r.PathValue("experimentId"), req.DecisionID, req.ScenarioID, now)
		return created(ok), v, err
	})
}

func (s *server) getRun(w http.ResponseWriter, r *http.Request, userID string) {
	v, err := s.lab.GetRun(r.Context(), s.db.Pool, userID, r.PathValue("runId"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, v)
}

type reflectionRequest struct {
	ChoiceID string `json:"choiceId"`
	ReasonID string `json:"reasonId"`
}

func (s *server) postReflection(w http.ResponseWriter, r *http.Request, userID string) {
	var req reflectionRequest
	s.mutation(w, r, userID, "POST /lab/runs/{runId}/reflection", &req, func(ctx context.Context, tx pgx.Tx, now time.Time) (int, any, error) {
		v, ok, err := s.lab.Reflect(ctx, tx, userID, r.PathValue("runId"), req.ChoiceID, req.ReasonID, now)
		return created(ok), v, err
	})
}

type comparisonRequest struct {
	Outcome               string  `json:"outcome"`
	ReasonID              string  `json:"reasonId"`
	AlternativeDecisionID *string `json:"alternativeDecisionId"`
}

func (s *server) postComparison(w http.ResponseWriter, r *http.Request, userID string) {
	var req comparisonRequest
	s.mutation(w, r, userID, "POST /lab/experiments/{experimentId}/comparison", &req, func(ctx context.Context, tx pgx.Tx, now time.Time) (int, any, error) {
		v, ok, err := s.lab.Compare(ctx, tx, userID, r.PathValue("experimentId"),
			lab.ComparisonInput{Outcome: req.Outcome, ReasonID: req.ReasonID, AlternativeDecisionID: req.AlternativeDecisionID}, now)
		return created(ok), v, err
	})
}

func (s *server) getComparison(w http.ResponseWriter, r *http.Request, userID string) {
	v, err := s.lab.GetComparison(r.Context(), s.db.Pool, userID, r.PathValue("experimentId"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, v)
}
