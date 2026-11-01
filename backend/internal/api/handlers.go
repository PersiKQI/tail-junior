package api

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"teyljunior/internal/assessment"
	"teyljunior/internal/core"
	"teyljunior/internal/httpx"
	"teyljunior/internal/identity"
	"teyljunior/internal/learning"
	"teyljunior/internal/store"
)

type sessionRequest struct {
	InstallationID string `json:"installationId"`
}

type sessionResponse struct {
	UserID  string           `json:"userId"`
	Token   string           `json:"token"`
	Profile identity.Profile `json:"profile"`
}

// postSession создаёт анонимный профиль и зачисляет его в опубликованную поставку. Idempotency
// здесь не используется: ответ с открытым токеном не должен лежать в журнале повторов (§20).
// Пустое тело допустимо.
func (s *server) postSession(w http.ResponseWriter, r *http.Request) {
	if s.limited(w, r, s.ipLimit, clientIP(r)) {
		return
	}
	var req sessionRequest
	if r.ContentLength != 0 {
		if _, err := readJSON(r, &req); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	var resp sessionResponse
	err := s.db.InTx(r.Context(), func(tx pgx.Tx) error {
		ctx, now := r.Context(), s.clock.Now()
		version, err := s.catalog.LatestPublished(ctx, tx)
		if err != nil {
			return err
		}
		userID, token, err := identity.CreateUser(ctx, tx, s.ids, now, req.InstallationID)
		if err != nil {
			return err
		}
		if err := s.learning.Enroll(ctx, tx, userID, version, now); err != nil {
			return err
		}
		resp.UserID, resp.Token = userID, token
		resp.Profile, err = identity.GetProfile(ctx, tx, userID)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusCreated, resp)
}

type bootstrapResponse struct {
	Profile  identity.Profile  `json:"profile"`
	Program  programView       `json:"program"`
	Progress learning.Progress `json:"progress"`
	// AssessmentSets — только статусы и охват, без правильности (§20).
	AssessmentSets []assessment.SetSummary `json:"assessmentSets"`
	// Legacy — перенесённая история прежнего приложения: можно показать «Вы уже знакомились
	// с темой» без новых наград (§23).
	Legacy legacyView `json:"legacy"`
}

type legacyView struct {
	Imported              bool             `json:"imported"`
	InvestmentLessonsSeen int              `json:"investmentLessonsSeen"`
	D7                    *legacyD7Summary `json:"d7"`
}

// legacyD7Summary — перенесённое окно старого D7 без правильности и текста задания.
type legacyD7Summary struct {
	Status      string    `json:"status"`
	AvailableAt time.Time `json:"availableAt"`
	ClosesAt    time.Time `json:"closesAt"`
}

type programView struct {
	Version string `json:"version"`
	Route   string `json:"route"`
}

// getBootstrap — профиль, поставка и краткий прогресс без банка, ключей и истории (§20).
func (s *server) getBootstrap(w http.ResponseWriter, r *http.Request, userID string) {
	ctx := r.Context()
	var resp bootstrapResponse
	var err error
	if resp.Profile, err = identity.GetProfile(ctx, s.db.Pool, userID); err != nil {
		s.fail(w, r, err)
		return
	}
	if err = s.db.Pool.QueryRow(ctx, `SELECT program_version FROM enrollments WHERE user_id = $1`, userID).Scan(&resp.Program.Version); err != nil {
		s.fail(w, r, err)
		return
	}
	resp.Program.Route = resp.Profile.Route
	if resp.Progress, err = s.learning.Summary(ctx, s.db.Pool, userID); err != nil {
		s.fail(w, r, err)
		return
	}
	if resp.AssessmentSets, err = s.assess.Summaries(ctx, s.db.Pool, userID, s.clock.Now()); err != nil {
		s.fail(w, r, err)
		return
	}
	if err = s.db.Pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM legacy_records WHERE user_id = $1),
		       (SELECT count(*) FROM legacy_records WHERE user_id = $1 AND source_kind = 'investment_answer')`, userID).
		Scan(&resp.Legacy.Imported, &resp.Legacy.InvestmentLessonsSeen); err != nil {
		s.fail(w, r, err)
		return
	}
	switch d7, err := s.d7.Get(ctx, s.db.Pool, userID, s.clock.Now()); {
	case err == nil:
		resp.Legacy.D7 = &legacyD7Summary{Status: d7.Status, AvailableAt: d7.AvailableAt, ClosesAt: d7.ClosesAt}
	case core.AsError(err).Code != "NOT_FOUND":
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, resp)
}

type profileRequest struct {
	ExpectedRevision *int                 `json:"expectedRevision"`
	Nickname         identity.Opt[string] `json:"nickname"`
	AgeGroup         identity.Opt[string] `json:"ageGroup"`
	CharacterID      identity.Opt[string] `json:"characterId"`
	ReducedMotion    identity.Opt[bool]   `json:"reducedMotion"`
	Route            identity.Opt[string] `json:"route"`
	LearningTimezone identity.Opt[string] `json:"learningTimezone"`
	AnalyticsEnabled identity.Opt[bool]   `json:"analyticsEnabled"`
}

func (s *server) patchProfile(w http.ResponseWriter, r *http.Request, userID string) {
	var req profileRequest
	s.mutation(w, r, userID, "PATCH /profile", &req, func(ctx context.Context, tx pgx.Tx, _ time.Time) (int, any, error) {
		if req.ExpectedRevision == nil {
			return 0, nil, core.Validation("expectedRevision", "обязательное поле")
		}
		p, err := identity.UpdateProfile(ctx, tx, userID, *req.ExpectedRevision, identity.Patch{
			Nickname: req.Nickname, AgeGroup: req.AgeGroup, CharacterID: req.CharacterID,
			ReducedMotion: req.ReducedMotion, Route: req.Route, LearningTimezone: req.LearningTimezone,
			AnalyticsEnabled: req.AnalyticsEnabled,
		})
		return http.StatusOK, p, err
	})
}

// deleteAccount удаляет профиль; Idempotency-Key для удаления не требуется (§20). Повтор после
// 204 получит 401: credential уже удалён (§4).
func (s *server) deleteAccount(w http.ResponseWriter, r *http.Request, userID string) {
	err := s.db.InTx(r.Context(), func(tx pgx.Tx) error {
		if err := store.LockUser(r.Context(), tx, userID); err != nil {
			return err
		}
		return identity.DeleteAccount(r.Context(), tx, userID)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type assignmentsResponse struct {
	Items []learning.AssignmentView `json:"items"`
}

func (s *server) getAssignments(w http.ResponseWriter, r *http.Request, userID string) {
	items, err := s.learning.ListAssignments(r.Context(), s.db.Pool, userID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, assignmentsResponse{Items: items})
}

type practiceRequest struct {
	FamilyID string `json:"familyId"`
}

func (s *server) postPractice(w http.ResponseWriter, r *http.Request, userID string) {
	var req practiceRequest
	s.mutation(w, r, userID, "POST /learning/practice-assignments", &req, func(ctx context.Context, tx pgx.Tx, now time.Time) (int, any, error) {
		if req.FamilyID == "" {
			return 0, nil, core.Validation("familyId", "обязательное поле")
		}
		v, ok, err := s.learning.CreatePractice(ctx, tx, userID, req.FamilyID, now)
		return created(ok), v, err
	})
}

type attemptRequest struct {
	AssignmentID string `json:"assignmentId"`
}

func (s *server) postAttempt(w http.ResponseWriter, r *http.Request, userID string) {
	var req attemptRequest
	s.mutation(w, r, userID, "POST /learning/attempts", &req, func(ctx context.Context, tx pgx.Tx, now time.Time) (int, any, error) {
		if req.AssignmentID == "" {
			return 0, nil, core.Validation("assignmentId", "обязательное поле")
		}
		v, ok, err := s.learning.StartAttempt(ctx, tx, userID, req.AssignmentID, now)
		return created(ok), v, err
	})
}

func (s *server) getAttempt(w http.ResponseWriter, r *http.Request, userID string) {
	v, err := s.learning.GetAttempt(r.Context(), s.db.Pool, userID, r.PathValue("attemptId"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, v)
}

type supportRequest struct {
	Kind   string `json:"kind"`
	TermID string `json:"termId"`
}

func (s *server) postSupport(w http.ResponseWriter, r *http.Request, userID string) {
	var req supportRequest
	s.mutation(w, r, userID, "POST /learning/attempts/{attemptId}/support", &req, func(ctx context.Context, tx pgx.Tx, now time.Time) (int, any, error) {
		v, err := s.learning.Support(ctx, tx, userID, r.PathValue("attemptId"), req.Kind, req.TermID, now)
		return http.StatusOK, v, err
	})
}

type submissionRequest struct {
	ChoiceID          string     `json:"choiceId"`
	ReasonID          string     `json:"reasonId"`
	Confidence        *string    `json:"confidence"`
	ClientCompletedAt *time.Time `json:"clientCompletedAt"`
}

func (s *server) postSubmission(w http.ResponseWriter, r *http.Request, userID string) {
	var req submissionRequest
	s.mutation(w, r, userID, "POST /learning/attempts/{attemptId}/submission", &req, func(ctx context.Context, tx pgx.Tx, now time.Time) (int, any, error) {
		if req.ChoiceID == "" || req.ReasonID == "" {
			return 0, nil, core.Validation("choiceId", "нужны и выбор, и объяснение").WithField("reasonId", "нужны и выбор, и объяснение")
		}
		if req.ClientCompletedAt != nil {
			t := req.ClientCompletedAt.UTC()
			req.ClientCompletedAt = &t
		}
		rc, ok, err := s.learning.Submit(ctx, tx, userID, r.PathValue("attemptId"), learning.SubmissionInput{
			ChoiceID: req.ChoiceID, ReasonID: req.ReasonID, Confidence: req.Confidence, ClientCompletedAt: req.ClientCompletedAt,
		}, now)
		return created(ok), rc, err
	})
}
