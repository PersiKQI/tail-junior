package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"teyljunior/internal/core"
	"teyljunior/internal/httpx"
	"teyljunior/internal/money"
)

func (s *server) getPlan(w http.ResponseWriter, r *http.Request, userID string) {
	v, err := s.money.GetPlan(r.Context(), s.db.Pool, userID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, v)
}

type categoriesRequest struct {
	Food      *int64 `json:"foodMinor"`
	Transport *int64 `json:"transportMinor"`
	Housing   *int64 `json:"housingMinor"`
	Other     *int64 `json:"otherMinor"`
}

type planRequest struct {
	ExpectedRevision         *int               `json:"expectedRevision"`
	IncomeMinor              *int64             `json:"incomeMinor"`
	ReserveMinor             *int64             `json:"reserveMinor"`
	MonthlyContributionMinor *int64             `json:"monthlyContributionMinor"`
	Categories               *categoriesRequest `json:"categories"`
	Goal                     *money.GoalInput   `json:"goal"`
}

// putPlan сохраняет бюджет и цель одним снимком. Все поля обязательны: частичная форма не
// должна незаметно обнулить часть плана.
func (s *server) putPlan(w http.ResponseWriter, r *http.Request, userID string) {
	var req planRequest
	s.mutation(w, r, userID, "PUT /money/plan", &req, func(ctx context.Context, tx pgx.Tx, now time.Time) (int, any, error) {
		c := req.Categories
		switch {
		case req.ExpectedRevision == nil:
			return 0, nil, core.Validation("expectedRevision", "обязательное поле")
		case req.IncomeMinor == nil || req.ReserveMinor == nil || req.MonthlyContributionMinor == nil:
			return 0, nil, core.Validation("incomeMinor", "нужны incomeMinor, reserveMinor и monthlyContributionMinor")
		case c == nil || c.Food == nil || c.Transport == nil || c.Housing == nil || c.Other == nil:
			return 0, nil, core.Validation("categories", "нужны все четыре категории")
		}
		v, err := s.money.PutPlan(ctx, tx, userID, money.PlanInput{
			ExpectedRevision: *req.ExpectedRevision, Goal: req.Goal,
			Budget: money.Budget{IncomeMinor: *req.IncomeMinor, ReserveMinor: *req.ReserveMinor, MonthlyContributionMinor: *req.MonthlyContributionMinor,
				FoodMinor: *c.Food, TransportMinor: *c.Transport, HousingMinor: *c.Housing, OtherMinor: *c.Other},
		}, now)
		return http.StatusOK, v, err
	})
}

// pageParams разбирает limit и cursor.
func pageParams(r *http.Request) (string, int, error) {
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return "", 0, core.Validation("limit", "целое число")
		}
		limit = n
	}
	n, e := money.PageSize(limit)
	if e != nil {
		return "", 0, e
	}
	return r.URL.Query().Get("cursor"), n, nil
}

func (s *server) getExpenses(w http.ResponseWriter, r *http.Request, userID string) {
	cursor, limit, err := pageParams(r)
	if err == nil {
		var page money.ExpensePage
		page, err = s.money.ListExpenses(r.Context(), s.db.Pool, userID, r.URL.Query().Get("month"), cursor, limit)
		if err == nil {
			httpx.WriteJSON(w, r, http.StatusOK, page)
			return
		}
	}
	s.fail(w, r, err)
}

type expenseRequest struct {
	AmountMinor int64  `json:"amountMinor"`
	CategoryID  string `json:"categoryId"`
	OccurredOn  string `json:"occurredOn"`
	Note        string `json:"note"`
}

func (s *server) postExpense(w http.ResponseWriter, r *http.Request, userID string) {
	var req expenseRequest
	s.mutation(w, r, userID, "POST /money/expenses", &req, func(ctx context.Context, tx pgx.Tx, now time.Time) (int, any, error) {
		v, err := s.money.AddExpense(ctx, tx, userID, money.ExpenseInput{AmountMinor: req.AmountMinor, CategoryID: req.CategoryID,
			OccurredOn: req.OccurredOn, Note: req.Note}, now)
		return http.StatusCreated, v, err
	})
}

type expensePatchRequest struct {
	ExpectedRevision *int  `json:"expectedRevision"`
	Voided           *bool `json:"voided"`
}

func (s *server) patchExpense(w http.ResponseWriter, r *http.Request, userID string) {
	var req expensePatchRequest
	s.mutation(w, r, userID, "PATCH /money/expenses/{expenseId}", &req, func(ctx context.Context, tx pgx.Tx, now time.Time) (int, any, error) {
		if req.ExpectedRevision == nil || req.Voided == nil {
			return 0, nil, core.Validation("voided", "нужны expectedRevision и voided")
		}
		v, err := s.money.SetVoided(ctx, tx, userID, r.PathValue("expenseId"), *req.ExpectedRevision, *req.Voided, now)
		return http.StatusOK, v, err
	})
}

func (s *server) getCheckins(w http.ResponseWriter, r *http.Request, userID string) {
	cursor, limit, err := pageParams(r)
	if err == nil {
		var page money.Page[money.CheckinView]
		if page, err = s.money.ListCheckins(r.Context(), s.db.Pool, userID, cursor, limit); err == nil {
			httpx.WriteJSON(w, r, http.StatusOK, page)
			return
		}
	}
	s.fail(w, r, err)
}

type checkinRequest struct {
	Kind           string   `json:"kind"`
	AmountMinor    *int64   `json:"amountMinor"`
	ReviewedFields []string `json:"reviewedFields"`
	Note           string   `json:"note"`
}

func (s *server) postCheckin(w http.ResponseWriter, r *http.Request, userID string) {
	var req checkinRequest
	s.mutation(w, r, userID, "POST /money/checkins", &req, func(ctx context.Context, tx pgx.Tx, now time.Time) (int, any, error) {
		v, err := s.money.AddCheckin(ctx, tx, userID, money.CheckinInput{Kind: req.Kind, AmountMinor: req.AmountMinor,
			ReviewedFields: req.ReviewedFields, Note: req.Note}, now)
		return http.StatusCreated, v, err
	})
}

func (s *server) getActions(w http.ResponseWriter, r *http.Request, userID string) {
	cursor, limit, err := pageParams(r)
	if err == nil {
		var page money.Page[money.ActionView]
		if page, err = s.money.ListActions(r.Context(), s.db.Pool, userID, cursor, limit); err == nil {
			httpx.WriteJSON(w, r, http.StatusOK, page)
			return
		}
	}
	s.fail(w, r, err)
}

type actionRequest struct {
	Kind        string `json:"kind"`
	AmountMinor *int64 `json:"amountMinor"`
	Note        string `json:"note"`
	DueOn       string `json:"dueOn"`
}

func (s *server) postAction(w http.ResponseWriter, r *http.Request, userID string) {
	var req actionRequest
	s.mutation(w, r, userID, "POST /money/actions", &req, func(ctx context.Context, tx pgx.Tx, now time.Time) (int, any, error) {
		v, err := s.money.AddAction(ctx, tx, userID, money.ActionInput{Kind: req.Kind, AmountMinor: req.AmountMinor, Note: req.Note, DueOn: req.DueOn}, now)
		return http.StatusCreated, v, err
	})
}

func (s *server) postActionCompletion(w http.ResponseWriter, r *http.Request, userID string) {
	var req emptyRequest
	s.mutation(w, r, userID, "POST /money/actions/{actionId}/completion", &req, func(ctx context.Context, tx pgx.Tx, now time.Time) (int, any, error) {
		v, err := s.money.CompleteAction(ctx, tx, userID, r.PathValue("actionId"), now)
		return http.StatusOK, v, err
	})
}

func (s *server) postActionCancellation(w http.ResponseWriter, r *http.Request, userID string) {
	var req emptyRequest
	s.mutation(w, r, userID, "POST /money/actions/{actionId}/cancellation", &req, func(ctx context.Context, tx pgx.Tx, now time.Time) (int, any, error) {
		v, err := s.money.CancelAction(ctx, tx, userID, r.PathValue("actionId"), now)
		return http.StatusOK, v, err
	})
}
