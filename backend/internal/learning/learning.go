// Пакет learning — назначения и попытки (спецификация §7, §8, §9): зачисление, выдача попытки
// с зафиксированной редакцией и порядком вариантов, помощь, приём ответа, серверная оценка и
// первая принятая попытка семейства. Попытки diagnostic/review проходят через наборы пакета
// assessment: их оценка скрыта до финализации набора.
package learning

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"teyljunior/internal/assessment"
	"teyljunior/internal/content"
	"teyljunior/internal/core"
	"teyljunior/internal/progression"
	"teyljunior/internal/store"
)

// Роли назначений.
const (
	RoleLearning   = "learning"
	RolePractice   = "practice"
	RoleDiagnostic = "diagnostic"
	RoleReview     = "review"
)

// Service — сценарии обучения. Методы, меняющие состояние, вызываются внутри транзакции после
// захвата строки пользователя.
type Service struct {
	Catalog *content.Catalog
	IDs     core.IDs
	Shuffle core.Shuffler
	Assess  *assessment.Service
	Prog    *progression.Service
}

// Enroll зачисляет пользователя в поставку и материализует все learning назначения (§7):
// любую тему можно выбрать через список, маршрут задаёт только рекомендацию.
func (s *Service) Enroll(ctx context.Context, tx pgx.Tx, userID, version string, now time.Time) error {
	if _, err := tx.Exec(ctx, `INSERT INTO enrollments (user_id, program_version, enrolled_at) VALUES ($1, $2, $3)`,
		userID, version, now); err != nil {
		return err
	}
	keys, families, err := s.Catalog.ReleaseItems(ctx, tx, version)
	if err != nil {
		return err
	}
	for i, k := range keys {
		rev, err := s.Catalog.Revision(ctx, tx, k)
		if err != nil {
			return err
		}
		if rev.Purpose != content.PurposeLearning {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO assignments (id, user_id, assignment_key, family_id, item_id, program_version, role, status, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, 'learning', 'open', $7)`,
			s.IDs.New(core.PrefixAssignment), userID, "learning:"+families[i], families[i], k.ItemID, version, now); err != nil {
			return err
		}
	}
	return s.Assess.CreateReviewSets(ctx, tx, userID, version, now)
}

// AssignmentView — назначение в списке.
type AssignmentView struct {
	AssignmentID    string     `json:"assignmentId"`
	Role            string     `json:"role"`
	PracticeOrigin  *string    `json:"practiceOrigin"`
	FamilyID        string     `json:"familyId"`
	ItemID          string     `json:"itemId"`
	ModuleID        string     `json:"moduleId"`
	Title           string     `json:"title"`
	Subtitle        string     `json:"subtitle"`
	Status          string     `json:"status"`
	InRoute         bool       `json:"inRoute"`
	ActiveAttemptID *string    `json:"activeAttemptId"`
	CreatedAt       time.Time  `json:"createdAt"`
	CompletedAt     *time.Time `json:"completedAt"`
}

const assignmentColumns = `
	a.id, a.role, a.practice_origin, a.family_id, a.item_id, a.program_version, a.status, a.created_at, a.completed_at,
	(SELECT t.id FROM attempts t WHERE t.assignment_id = a.id AND t.status = 'started')`

type assignmentRow struct {
	AssignmentView
	version string
}

func scanAssignment(row pgx.Row) (assignmentRow, error) {
	var r assignmentRow
	err := row.Scan(&r.AssignmentID, &r.Role, &r.PracticeOrigin, &r.FamilyID, &r.ItemID, &r.version,
		&r.Status, &r.CreatedAt, &r.CompletedAt, &r.ActiveAttemptID)
	return r, err
}

// ListAssignments — learning и practice назначения пользователя. Тексты будущих проверок сюда
// не попадают: только название и подзаголовок выданной поставки.
func (s *Service) ListAssignments(ctx context.Context, q store.Querier, userID string) ([]AssignmentView, error) {
	route, err := s.routeFamilies(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `SELECT `+assignmentColumns+` FROM assignments a
		WHERE a.user_id = $1 AND a.role IN ('learning', 'practice')
		ORDER BY a.role, a.item_id, a.created_at`, userID)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (assignmentRow, error) { return scanAssignment(r) })
	if err != nil {
		return nil, err
	}
	out := make([]AssignmentView, 0, len(list))
	for _, r := range list {
		v, err := s.decorate(ctx, q, r, route)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Service) decorate(ctx context.Context, q store.Querier, r assignmentRow, route []string) (AssignmentView, error) {
	k, err := s.Catalog.ItemForFamily(ctx, q, r.version, r.FamilyID)
	if err != nil {
		return r.AssignmentView, err
	}
	rev, err := s.Catalog.Revision(ctx, q, k)
	if err != nil {
		return r.AssignmentView, err
	}
	v := r.AssignmentView
	v.ModuleID, v.Title, v.Subtitle = rev.ModuleID, rev.Public.Title, rev.Public.Subtitle
	v.InRoute = slices.Contains(route, v.FamilyID)
	v.CreatedAt = v.CreatedAt.UTC()
	v.CompletedAt = utcPtr(v.CompletedAt)
	return v, nil
}

// routeFamilies — семейства выбранного маршрута в порядке рекомендации.
func (s *Service) routeFamilies(ctx context.Context, q store.Querier, userID string) ([]string, error) {
	var route, version string
	err := q.QueryRow(ctx, `SELECT u.route, e.program_version FROM users u JOIN enrollments e ON e.user_id = u.id WHERE u.id = $1`, userID).
		Scan(&route, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, core.Unauthorized()
	}
	if err != nil {
		return nil, err
	}
	m, err := s.Catalog.Manifest(ctx, q, version)
	if err != nil {
		return nil, err
	}
	r, _ := m.Route(route)
	return r.FamilyIDs, nil
}

// CreatePractice назначает ручную практику семейства. До первого принятого ответа практика
// недоступна: первый ответ — это learning (§7). Открытая практика возвращается повторно.
func (s *Service) CreatePractice(ctx context.Context, tx pgx.Tx, userID, familyID string, now time.Time) (AssignmentView, bool, error) {
	var version string
	if err := tx.QueryRow(ctx, `SELECT program_version FROM enrollments WHERE user_id = $1`, userID).Scan(&version); err != nil {
		return AssignmentView{}, false, err
	}
	k, err := s.Catalog.ItemForFamily(ctx, tx, version, familyID)
	if err != nil {
		return AssignmentView{}, false, err
	}
	rev, err := s.Catalog.Revision(ctx, tx, k)
	if err != nil {
		return AssignmentView{}, false, err
	}
	if rev.Purpose != content.PurposeLearning {
		return AssignmentView{}, false, core.NotFound()
	}
	var done bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM learning_completions WHERE user_id = $1 AND family_id = $2)`,
		userID, familyID).Scan(&done); err != nil {
		return AssignmentView{}, false, err
	}
	if !done {
		return AssignmentView{}, false, core.Conflict("PRACTICE_NOT_AVAILABLE", "сначала ответь на само задание; повтор появится после него")
	}
	route, err := s.routeFamilies(ctx, tx, userID)
	if err != nil {
		return AssignmentView{}, false, err
	}
	r, err := scanAssignment(tx.QueryRow(ctx, `SELECT `+assignmentColumns+` FROM assignments a
		WHERE a.user_id = $1 AND a.family_id = $2 AND a.role = 'practice' AND a.status = 'open'`, userID, familyID))
	if err == nil {
		v, err := s.decorate(ctx, tx, r, route)
		return v, false, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return AssignmentView{}, false, err
	}
	id := s.IDs.New(core.PrefixAssignment)
	if _, err := tx.Exec(ctx, `
		INSERT INTO assignments (id, user_id, assignment_key, family_id, item_id, program_version, role, practice_origin, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'practice', 'manual', 'open', $7)`,
		id, userID, "practice:"+id, familyID, k.ItemID, version, now); err != nil {
		return AssignmentView{}, false, err
	}
	r, err = scanAssignment(tx.QueryRow(ctx, `SELECT `+assignmentColumns+` FROM assignments a WHERE a.id = $1`, id))
	if err != nil {
		return AssignmentView{}, false, err
	}
	v, err := s.decorate(ctx, tx, r, route)
	return v, true, err
}

// optionOrder — порядок вариантов, зафиксированный при выдаче попытки (§6).
type optionOrder struct {
	Choices []string `json:"choices"`
	Reasons []string `json:"reasons"`
}

func (s *Service) shuffled(opts []content.Option) []string {
	perm := s.Shuffle.Perm(len(opts))
	ids := make([]string, len(opts))
	for i, p := range perm {
		ids[i] = opts[p].ID
	}
	return ids
}

// StartAttempt начинает попытку по назначению или возвращает уже начатую (§7).
// Второй результат — создана ли новая попытка.
func (s *Service) StartAttempt(ctx context.Context, tx pgx.Tx, userID, assignmentID string, now time.Time) (AttemptView, bool, error) {
	var role, familyID, version, status string
	var setID *string
	err := tx.QueryRow(ctx, `SELECT role, family_id, program_version, status, assessment_set_id FROM assignments WHERE user_id = $1 AND id = $2`,
		userID, assignmentID).Scan(&role, &familyID, &version, &status, &setID)
	if errors.Is(err, pgx.ErrNoRows) {
		return AttemptView{}, false, core.NotFound()
	}
	if err != nil {
		return AttemptView{}, false, err
	}
	// Порядок блокировок: пользователь (уже захвачен), набор, назначение (§3).
	if setID != nil {
		if err := s.Assess.BeginAttempt(ctx, tx, userID, *setID, now); err != nil {
			return AttemptView{}, false, err
		}
	}
	if err := tx.QueryRow(ctx, `SELECT status FROM assignments WHERE id = $1 FOR UPDATE`, assignmentID).Scan(&status); err != nil {
		return AttemptView{}, false, err
	}
	var activeID string
	err = tx.QueryRow(ctx, `SELECT id FROM attempts WHERE assignment_id = $1 AND status = 'started'`, assignmentID).Scan(&activeID)
	if err == nil {
		v, err := s.GetAttempt(ctx, tx, userID, activeID)
		return v, false, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return AttemptView{}, false, err
	}
	switch {
	case status == "withdrawn":
		return AttemptView{}, false, core.Gone("CONTENT_WITHDRAWN", "в этом примере нашли неточность; история сохранена, готовится замена")
	case status == "completed":
		return AttemptView{}, false, core.Conflict("ASSIGNMENT_COMPLETED", "задание уже выполнено; повтор создаётся как практика")
	}
	k, err := s.Catalog.ItemForFamily(ctx, tx, version, familyID)
	if err != nil {
		return AttemptView{}, false, err
	}
	rev, err := s.Catalog.Revision(ctx, tx, k)
	if err != nil {
		return AttemptView{}, false, err
	}
	if rev.Status == "withdrawn" {
		return AttemptView{}, false, core.Gone("CONTENT_WITHDRAWN", "в этом примере нашли неточность; история сохранена, готовится замена")
	}
	order, err := json.Marshal(optionOrder{Choices: s.shuffled(rev.Public.Choices), Reasons: s.shuffled(rev.Public.Reasons)})
	if err != nil {
		return AttemptView{}, false, err
	}
	// Ранее показанный разбор семейства делает попытку несамостоятельной: известный ответ не
	// является новой проверкой (§9, §12).
	var seen bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM content_exposures WHERE user_id = $1 AND family_id = $2 AND kind = 'explanation')`,
		userID, familyID).Scan(&seen); err != nil {
		return AttemptView{}, false, err
	}
	id := s.IDs.New(core.PrefixAttempt)
	if _, err := tx.Exec(ctx, `
		INSERT INTO attempts (id, user_id, assignment_id, family_id, item_id, revision, grading_revision, locale,
		                      program_version, role, status, option_order, issued_at, independence_eligible)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'started', $11, $12, $13)`,
		id, userID, assignmentID, familyID, k.ItemID, k.Revision, k.GradingRevision, k.Locale, version, role, order, now, !seen); err != nil {
		return AttemptView{}, false, err
	}
	if err := s.expose(ctx, tx, userID, familyID, k.ItemID, id, "card", now); err != nil {
		return AttemptView{}, false, err
	}
	v, err := s.GetAttempt(ctx, tx, userID, id)
	return v, true, err
}

func (s *Service) expose(ctx context.Context, tx pgx.Tx, userID, familyID, itemID, attemptID, kind string, now time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO content_exposures (id, user_id, family_id, item_id, attempt_id, kind, provided_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		s.IDs.New(core.PrefixExposure), userID, familyID, itemID, attemptID, kind, now)
	return err
}

// CardView — публичная проекция карточки: явный allowlist полей и сохранённый порядок вариантов.
type CardView struct {
	Purpose  string           `json:"purpose"`
	ModuleID string           `json:"moduleId"`
	Title    string           `json:"title"`
	Subtitle string           `json:"subtitle"`
	Concept  string           `json:"concept"`
	Example  string           `json:"example"`
	Question string           `json:"question"`
	Choices  []content.Option `json:"choices"`
	Reasons  []content.Option `json:"reasons"`
	TermIDs  []string         `json:"termIds"`
	Sources  []content.Source `json:"sources"`
}

// EvaluationView — серверная оценка learning/practice ответа (§8).
type EvaluationView struct {
	ChoiceCorrect  bool   `json:"choiceCorrect"`
	ReasonCorrect  bool   `json:"reasonCorrect"`
	Passed         bool   `json:"passed"`
	Explanation    string `json:"explanation"`
	ChoiceFeedback string `json:"choiceFeedback,omitempty"`
	ReasonFeedback string `json:"reasonFeedback,omitempty"`
}

// SubmissionView — принятый ответ.
type SubmissionView struct {
	SubmissionID string    `json:"submissionId"`
	ChoiceID     string    `json:"choiceId"`
	ReasonID     string    `json:"reasonId"`
	Confidence   *string   `json:"confidence"`
	AcceptedAt   time.Time `json:"acceptedAt"`
}

// AttemptView — попытка с допустимой публичной проекцией.
type AttemptView struct {
	AttemptID       string          `json:"attemptId"`
	AssignmentID    string          `json:"assignmentId"`
	Role            string          `json:"role"`
	Status          string          `json:"status"`
	FamilyID        string          `json:"familyId"`
	ItemID          string          `json:"itemId"`
	Revision        int             `json:"revision"`
	GradingRevision int             `json:"gradingRevision"`
	Locale          string          `json:"locale"`
	ProgramVersion  string          `json:"programVersion"`
	IssuedAt        time.Time       `json:"issuedAt"`
	ClosedAt        *time.Time      `json:"closedAt"`
	AssistanceUsed  bool            `json:"assistanceUsed"`
	Card            CardView        `json:"card"`
	Submission      *SubmissionView `json:"submission"`
	Evaluation      *EvaluationView `json:"evaluation"`
}

type attemptRow struct {
	view           AttemptView
	key            content.RevisionKey
	order          optionOrder
	setID          *string
	independent    bool // нет ранее показанного разбора и выданной помощи
	streakEligible bool // назначенный сервером повтор
}

func (s *Service) loadAttempt(ctx context.Context, q store.Querier, userID, attemptID string, forUpdate bool) (attemptRow, error) {
	var r attemptRow
	var order []byte
	sql := `SELECT t.id, t.assignment_id, t.role, t.status, t.family_id, t.item_id, t.revision, t.grading_revision, t.locale,
		t.program_version, t.option_order, t.issued_at, t.closed_at,
		EXISTS (SELECT 1 FROM assistance_events h WHERE h.attempt_id = t.id), a.assessment_set_id,
		t.independence_eligible, a.streak_eligible
		FROM attempts t JOIN assignments a ON a.user_id = t.user_id AND a.id = t.assignment_id
		WHERE t.user_id = $1 AND t.id = $2`
	if forUpdate {
		sql += ` FOR UPDATE OF t`
	}
	v := &r.view
	err := q.QueryRow(ctx, sql, userID, attemptID).Scan(&v.AttemptID, &v.AssignmentID, &v.Role, &v.Status, &v.FamilyID,
		&v.ItemID, &v.Revision, &v.GradingRevision, &v.Locale, &v.ProgramVersion, &order, &v.IssuedAt, &v.ClosedAt,
		&v.AssistanceUsed, &r.setID, &r.independent, &r.streakEligible)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, core.NotFound() // чужая попытка неотличима от отсутствующей (§5)
	}
	if err != nil {
		return r, err
	}
	v.IssuedAt = v.IssuedAt.UTC()
	v.ClosedAt = utcPtr(v.ClosedAt)
	r.key = content.RevisionKey{ItemID: v.ItemID, Revision: v.Revision, GradingRevision: v.GradingRevision, Locale: v.Locale}
	return r, json.Unmarshal(order, &r.order)
}

// GetAttempt собирает попытку: карточку в сохранённом порядке, ответ и оценку.
func (s *Service) GetAttempt(ctx context.Context, q store.Querier, userID, attemptID string) (AttemptView, error) {
	r, err := s.loadAttempt(ctx, q, userID, attemptID, false)
	if err != nil {
		return AttemptView{}, err
	}
	rev, err := s.Catalog.Revision(ctx, q, r.key)
	if err != nil {
		return AttemptView{}, err
	}
	v := r.view
	pc := rev.Public
	v.Card = CardView{
		Purpose: rev.Purpose, ModuleID: rev.ModuleID,
		Title: pc.Title, Subtitle: pc.Subtitle, Concept: pc.Concept, Example: pc.Example, Question: pc.Question,
		Choices: ordered(pc.Choices, r.order.Choices), Reasons: ordered(pc.Reasons, r.order.Reasons),
		TermIDs: nonNil(pc.TermIDs), Sources: nonNil(pc.Sources),
	}
	if v.Status == "submitted" {
		sub, ev, err := s.loadResult(ctx, q, userID, attemptID)
		if err != nil {
			return AttemptView{}, err
		}
		v.Submission = &sub
		revealed, err := s.revealed(ctx, q, userID, r.setID)
		if err != nil {
			return AttemptView{}, err
		}
		if revealed {
			v.Evaluation = &ev
		}
	}
	return v, nil
}

// revealed — можно ли показывать оценку: в learning/practice сразу, в наборе — после
// финализации (§9).
func (s *Service) revealed(ctx context.Context, q store.Querier, userID string, setID *string) (bool, error) {
	if setID == nil {
		return true, nil
	}
	return assessment.Revealed(ctx, q, userID, *setID)
}

func ordered(opts []content.Option, ids []string) []content.Option {
	out := make([]content.Option, 0, len(ids))
	for _, id := range ids {
		for _, o := range opts {
			if o.ID == id {
				out = append(out, o)
			}
		}
	}
	return out
}

func (s *Service) loadResult(ctx context.Context, q store.Querier, userID, attemptID string) (SubmissionView, EvaluationView, error) {
	var sub SubmissionView
	var ev EvaluationView
	var fb []byte
	err := q.QueryRow(ctx, `
		SELECT s.id, s.choice_id, s.reason_id, s.confidence, s.accepted_at,
		       e.choice_correct, e.reason_correct, e.passed, e.feedback
		FROM submissions s
		JOIN evaluations e ON e.submission_id = s.id AND e.replaces_evaluation_id IS NULL
		WHERE s.user_id = $1 AND s.attempt_id = $2`, userID, attemptID).
		Scan(&sub.SubmissionID, &sub.ChoiceID, &sub.ReasonID, &sub.Confidence, &sub.AcceptedAt,
			&ev.ChoiceCorrect, &ev.ReasonCorrect, &ev.Passed, &fb)
	if err != nil {
		return sub, ev, err
	}
	sub.AcceptedAt = sub.AcceptedAt.UTC()
	var f storedFeedback
	if err := json.Unmarshal(fb, &f); err != nil {
		return sub, ev, err
	}
	ev.Explanation, ev.ChoiceFeedback, ev.ReasonFeedback = f.Explanation, f.Choice, f.Reason
	return sub, ev, nil
}

// storedFeedback — тексты разбора, зафиксированные при оценке: исправление редакции не меняет
// уже показанный пользователю разбор.
type storedFeedback struct {
	Explanation string `json:"explanation"`
	Choice      string `json:"choice,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// SupportView — выданная помощь.
type SupportView struct {
	Kind string        `json:"kind"`
	Hint string        `json:"hint,omitempty"`
	Term *content.Term `json:"term,omitempty"`
}

// Support выдаёт подсказку или термин. Факт помощи записывается до возврата, и попытка теряет
// самостоятельность; клиентскому usedHint=false не доверяем (§8).
func (s *Service) Support(ctx context.Context, tx pgx.Tx, userID, attemptID, kind, termID string, now time.Time) (SupportView, error) {
	r, err := s.loadAttempt(ctx, tx, userID, attemptID, true)
	if err != nil {
		return SupportView{}, err
	}
	if r.view.Role != RoleLearning && r.view.Role != RolePractice {
		return SupportView{}, core.Conflict("SUPPORT_UNAVAILABLE", "в проверке помощь откроется после её завершения")
	}
	if r.view.Status != "started" {
		return SupportView{}, core.Conflict("ATTEMPT_CLOSED", "попытка уже завершена")
	}
	out := SupportView{Kind: kind}
	var term *string
	switch kind {
	case "hint":
		rev, err := s.Catalog.Revision(ctx, tx, r.key)
		if err != nil {
			return SupportView{}, err
		}
		if rev.Grading.Hint == "" {
			return SupportView{}, core.Conflict("SUPPORT_UNAVAILABLE", "для этого задания нет подсказки")
		}
		out.Hint = rev.Grading.Hint
	case "term":
		m, err := s.Catalog.Manifest(ctx, tx, r.view.ProgramVersion)
		if err != nil {
			return SupportView{}, err
		}
		t, ok := m.Term(termID)
		if !ok {
			return SupportView{}, core.Unprocessable("UNKNOWN_TERM", "такого термина нет в словаре").WithField("termId", "неизвестный термин")
		}
		out.Term, term = &t, &termID
	default:
		return SupportView{}, core.Validation("kind", "ожидалось hint или term")
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO assistance_events (id, user_id, attempt_id, kind, term_id, provided_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		s.IDs.New(core.PrefixAssistance), userID, attemptID, kind, term, now); err != nil {
		return SupportView{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE attempts SET independence_eligible = false WHERE id = $1`, attemptID)
	return out, err
}

// SubmissionInput — выбор и объяснение.
type SubmissionInput struct {
	ChoiceID          string
	ReasonID          string
	Confidence        *string
	ClientCompletedAt *time.Time
}

// SetProgress — состояние набора в квитанции: «сохранено 1 из 2» без правильности.
type SetProgress struct {
	SetID     string `json:"setId"`
	Status    string `json:"status"`
	Answered  int    `json:"answered"`
	Assigned  int    `json:"assigned"`
	Finalized bool   `json:"finalized"`
}

// Receipt — квитанция принятого ответа. В наборе Evaluation равна null до финализации, и
// правильность не кодируется косвенно через другие поля (приложение А).
type Receipt struct {
	AttemptID      string          `json:"attemptId"`
	Completed      bool            `json:"completed"`
	FirstForFamily bool            `json:"firstForFamily"`
	Submission     SubmissionView  `json:"submission"`
	Evaluation     *EvaluationView `json:"evaluation"`
	Assessment     *SetProgress    `json:"assessment"`
}

// Submit принимает ответ. Повтор того же выбора возвращает сохранённую квитанцию (created=false);
// другой выбор в закрытой попытке — 409 ATTEMPT_CLOSED; неизвестный вариант — 422 без учебного
// завершения (§8). Ответ в финализированный набор — 409 SET_FINALIZED (§21).
func (s *Service) Submit(ctx context.Context, tx pgx.Tx, userID, attemptID string, in SubmissionInput, now time.Time) (Receipt, bool, error) {
	pre, err := s.loadAttempt(ctx, tx, userID, attemptID, false)
	if err != nil {
		return Receipt{}, false, err
	}
	// Порядок блокировок: набор раньше попытки (§3). Повтор уже принятого ответа после
	// финализации возвращает квитанцию, а не ошибку.
	if pre.setID != nil && pre.view.Status != "submitted" {
		if err := s.Assess.CheckOpen(ctx, tx, userID, *pre.setID); err != nil {
			return Receipt{}, false, err
		}
	}
	r, err := s.loadAttempt(ctx, tx, userID, attemptID, true)
	if err != nil {
		return Receipt{}, false, err
	}
	switch r.view.Status {
	case "submitted":
		sub, _, err := s.loadResult(ctx, tx, userID, attemptID)
		if err != nil {
			return Receipt{}, false, err
		}
		if sub.ChoiceID != in.ChoiceID || sub.ReasonID != in.ReasonID {
			return Receipt{}, false, core.Conflict("ATTEMPT_CLOSED", "ответ уже принят; другой вариант можно попробовать в практике")
		}
		rc, err := s.receipt(ctx, tx, userID, attemptID, r.setID)
		return rc, false, err
	case "started":
	case "withdrawn":
		return Receipt{}, false, core.Gone("CONTENT_WITHDRAWN", "в этом примере нашли неточность; история сохранена, готовится замена")
	default:
		return Receipt{}, false, core.Conflict("ATTEMPT_CLOSED", "попытка закрыта")
	}
	rev, err := s.Catalog.Revision(ctx, tx, r.key)
	if err != nil {
		return Receipt{}, false, err
	}
	if rev.Status == "withdrawn" {
		return Receipt{}, false, core.Gone("CONTENT_WITHDRAWN", "в этом примере нашли неточность; история сохранена, готовится замена")
	}
	if e := validateAnswer(rev.Public, in); e != nil {
		return Receipt{}, false, e
	}

	subID := s.IDs.New(core.PrefixSubmission)
	if _, err := tx.Exec(ctx, `
		INSERT INTO submissions (id, user_id, attempt_id, choice_id, reason_id, confidence, accepted_at, client_completed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		subID, userID, attemptID, in.ChoiceID, in.ReasonID, in.Confidence, now, in.ClientCompletedAt); err != nil {
		return Receipt{}, false, err
	}
	choiceOK, reasonOK, passed := rev.Grading.Grade(in.ChoiceID, in.ReasonID)
	fb := storedFeedback{Explanation: rev.Grading.Explanation}
	if !choiceOK {
		fb.Choice = rev.Grading.Feedback.Choice
	}
	if !reasonOK {
		fb.Reason = rev.Grading.Feedback.Reason
	}
	fbJSON, err := json.Marshal(fb)
	if err != nil {
		return Receipt{}, false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO evaluations (id, user_id, attempt_id, submission_id, grading_revision, evaluated_at,
		                         choice_correct, reason_correct, passed, feedback)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		s.IDs.New(core.PrefixEvaluation), userID, attemptID, subID, r.key.GradingRevision, now, choiceOK, reasonOK, passed, fbJSON); err != nil {
		return Receipt{}, false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE attempts SET status = 'submitted', closed_at = $2 WHERE id = $1`, attemptID, now); err != nil {
		return Receipt{}, false, err
	}
	// Ошибка тоже завершает учебный шаг и не закрывает следующий материал (§8).
	if _, err := tx.Exec(ctx, `UPDATE assignments SET status = 'completed', completed_at = $3 WHERE user_id = $1 AND id = $2`,
		userID, r.view.AssignmentID, now); err != nil {
		return Receipt{}, false, err
	}
	switch r.view.Role {
	case RoleLearning, RolePractice:
		first := false
		if r.view.Role == RoleLearning {
			tag, err := tx.Exec(ctx, `
				INSERT INTO learning_completions (user_id, family_id, first_attempt_id, accepted_at)
				VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`, userID, r.view.FamilyID, attemptID, now)
			if err != nil {
				return Receipt{}, false, err
			}
			first = tag.RowsAffected() == 1
			// Первое принятие якоря может назначить срок отложенной проверке.
			if first {
				if err := s.Assess.RefreshDue(ctx, tx, userID); err != nil {
					return Receipt{}, false, err
				}
			}
		}
		if err := s.Prog.LearningAccepted(ctx, tx, progression.LearningAnswer{
			UserID: userID, AttemptID: attemptID, FamilyID: r.view.FamilyID, ItemID: r.view.ItemID,
			ProgramVersion: r.view.ProgramVersion, Role: r.view.Role, FirstForFamily: first,
			Independent: r.independent && !r.view.AssistanceUsed, Passed: passed, StreakEligible: r.streakEligible,
		}, now); err != nil {
			return Receipt{}, false, err
		}
	default:
		// Ответ в доступном review закрывает день сразу, оценка остаётся скрытой (§9, §15).
		if err := s.Prog.ReviewAccepted(ctx, tx, userID, attemptID, r.view.Role, now); err != nil {
			return Receipt{}, false, err
		}
	}
	if r.setID != nil {
		// Разбор в наборе раскрывается при финализации; последний ответ финализирует completed.
		if err := s.Assess.Answered(ctx, tx, userID, *r.setID, now); err != nil {
			return Receipt{}, false, err
		}
	} else if err := s.expose(ctx, tx, userID, r.view.FamilyID, r.view.ItemID, attemptID, "explanation", now); err != nil {
		return Receipt{}, false, err
	}
	rc, err := s.receipt(ctx, tx, userID, attemptID, r.setID)
	return rc, true, err
}

func validateAnswer(pc content.PublicCard, in SubmissionInput) *core.Error {
	var e *core.Error
	add := func(field, msg string) {
		if e == nil {
			e = core.Unprocessable("UNKNOWN_OPTION", "такого варианта нет в этом задании")
		}
		e = e.WithField(field, msg)
	}
	if !hasOption(pc.Choices, in.ChoiceID) {
		add("choiceId", "неизвестный вариант действия")
	}
	if !hasOption(pc.Reasons, in.ReasonID) {
		add("reasonId", "неизвестный вариант объяснения")
	}
	if e != nil {
		return e
	}
	if in.Confidence != nil && !slices.Contains([]string{"sure", "unsure", "guess"}, *in.Confidence) {
		return core.Validation("confidence", "ожидалось sure, unsure или guess")
	}
	return nil
}

func hasOption(opts []content.Option, id string) bool {
	return slices.ContainsFunc(opts, func(o content.Option) bool { return o.ID == id })
}

func (s *Service) receipt(ctx context.Context, q store.Querier, userID, attemptID string, setID *string) (Receipt, error) {
	sub, ev, err := s.loadResult(ctx, q, userID, attemptID)
	if err != nil {
		return Receipt{}, err
	}
	rc := Receipt{AttemptID: attemptID, Completed: true, Submission: sub}
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM learning_completions WHERE user_id = $1 AND first_attempt_id = $2)`,
		userID, attemptID).Scan(&rc.FirstForFamily); err != nil {
		return Receipt{}, err
	}
	if setID == nil {
		rc.Evaluation = &ev
		return rc, nil
	}
	p := SetProgress{SetID: *setID}
	if err := q.QueryRow(ctx, `
		SELECT s.status, s.finalized_at IS NOT NULL,
		       count(a.id) FILTER (WHERE a.status = 'completed'), count(a.id)
		FROM assessment_sets s JOIN assignments a ON a.assessment_set_id = s.id
		WHERE s.user_id = $1 AND s.id = $2 GROUP BY s.id`, userID, *setID).
		Scan(&p.Status, &p.Finalized, &p.Answered, &p.Assigned); err != nil {
		return Receipt{}, err
	}
	rc.Assessment = &p
	if p.Finalized {
		rc.Evaluation = &ev
	}
	return rc, nil
}

// Progress — краткий прогресс для bootstrap. Только счётчики участия; правильность здесь не
// смешивается с охватом (§12).
type Progress struct {
	LearningCompleted int     `json:"learningCompleted"`
	LearningTotal     int     `json:"learningTotal"`
	RouteCompleted    int     `json:"routeCompleted"`
	RouteTotal        int     `json:"routeTotal"`
	NextAssignmentID  *string `json:"nextAssignmentId"`
	ActiveAttemptID   *string `json:"activeAttemptId"`
}

// Summary считает прогресс и рекомендуемое следующее назначение маршрута.
func (s *Service) Summary(ctx context.Context, q store.Querier, userID string) (Progress, error) {
	route, err := s.routeFamilies(ctx, q, userID)
	if err != nil {
		return Progress{}, err
	}
	var p Progress
	rows, err := q.Query(ctx, `
		SELECT a.id, a.family_id, (c.family_id IS NOT NULL)
		FROM assignments a LEFT JOIN learning_completions c ON c.user_id = a.user_id AND c.family_id = a.family_id
		WHERE a.user_id = $1 AND a.role = 'learning' AND (a.status <> 'withdrawn' OR c.family_id IS NOT NULL)
		ORDER BY (a.status = 'open'), a.created_at`, userID)
	if err != nil {
		return p, err
	}
	// После отзыва у семейства есть отозванное назначение и замена: считаем семейства,
	// рекомендуем открытое (оно идёт последним).
	byFamily := map[string]string{}
	completed := map[string]bool{}
	for rows.Next() {
		var id, fam string
		var done bool
		if err := rows.Scan(&id, &fam, &done); err != nil {
			return p, err
		}
		byFamily[fam], completed[fam] = id, done
	}
	if err := rows.Err(); err != nil {
		return p, err
	}
	p.LearningTotal = len(byFamily)
	for _, done := range completed {
		if done {
			p.LearningCompleted++
		}
	}
	p.RouteTotal = len(route)
	for _, f := range route {
		if completed[f] {
			p.RouteCompleted++
		} else if p.NextAssignmentID == nil {
			id := byFamily[f]
			p.NextAssignmentID = &id
		}
	}
	var active string
	err = q.QueryRow(ctx, `SELECT id FROM attempts WHERE user_id = $1 AND status = 'started' ORDER BY issued_at DESC LIMIT 1`, userID).Scan(&active)
	if err == nil {
		p.ActiveAttemptID = &active
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return p, err
	}
	return p, nil
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
