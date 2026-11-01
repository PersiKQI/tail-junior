package lab

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"teyljunior/internal/core"
	"teyljunior/internal/progression"
	"teyljunior/internal/store"
)

// Пределы, которые защищают историю от бесконечного роста.
const (
	noteMaxRunes    = 500
	maxAlternatives = 10
)

// Service — сценарии лаборатории. Мутации вызываются в транзакции после захвата
// пользователя; затем блокируется эксперимент (§3).
type Service struct {
	IDs   core.IDs
	Prog  *progression.Service
	packs sync.Map // caseKey → *Pack, редакция кейса неизменяема
}

type caseKey struct {
	family   string
	revision int
}

func (s *Service) pack(ctx context.Context, q store.Querier, family string, revision int) (*Pack, error) {
	k := caseKey{family, revision}
	if p, ok := s.packs.Load(k); ok {
		return p.(*Pack), nil
	}
	var c, m []byte
	err := q.QueryRow(ctx, `
		SELECT c.payload, m.payload FROM lab_cases c JOIN lab_models m ON m.version = c.model_version
		WHERE c.family_id = $1 AND c.revision = $2`, family, revision).Scan(&c, &m)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, core.NotFound()
	}
	if err != nil {
		return nil, err
	}
	var p Pack
	if err := json.Unmarshal(c, &p.Case); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(m, &p.Model); err != nil {
		return nil, err
	}
	s.packs.Store(k, &p)
	return &p, nil
}

// ScenarioView — вид возможных условий без числовых исходов (§10).
type ScenarioView struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Months int    `json:"months"`
}

// CaseView — условия кейса. До фиксации решения ставки сценариев, проверочные пары и
// статусы причин не отдаются.
type CaseView struct {
	CaseFamilyID    string         `json:"caseFamilyId"`
	Revision        int            `json:"revision"`
	BundleRevision  int            `json:"bundleRevision"`
	ModelVersion    string         `json:"modelVersion"`
	Title           string         `json:"title"`
	Conditions      string         `json:"conditions"`
	InitialMinor    int64          `json:"initialMinor"`
	ReserveMinMinor int64          `json:"reserveMinMinor"`
	UnitMinor       int64          `json:"unitMinor"`
	Units           int            `json:"units"`
	Assets          []Asset        `json:"assets"`
	Scenarios       []ScenarioView `json:"scenarios"`
	DecisionReasons []Option       `json:"decisionReasons"`
	NotIncluded     []string       `json:"notIncluded"`
	Notice          string         `json:"notice"`
}

func caseView(p *Pack) CaseView {
	v := CaseView{
		CaseFamilyID: p.Case.CaseFamilyID, Revision: p.Case.Revision, BundleRevision: p.Case.BundleRevision,
		ModelVersion: p.Model.Version, Title: p.Case.Title, Conditions: p.Case.Conditions,
		InitialMinor: p.Case.InitialMinor, ReserveMinMinor: p.Case.ReserveMinMinor, UnitMinor: p.Model.UnitMinor,
		Units: p.Model.Units, Assets: p.Model.Assets, NotIncluded: p.Model.NotExcludedFromReality, Notice: p.Model.Notice,
	}
	for _, sc := range p.Model.Scenarios {
		v.Scenarios = append(v.Scenarios, ScenarioView{ID: sc.ID, Title: sc.Title, Months: sc.Months})
	}
	for _, r := range p.Case.DecisionReasons {
		v.DecisionReasons = append(v.DecisionReasons, Option{ID: r.ID, Text: r.Text})
	}
	return v
}

// ListCases — последние опубликованные редакции кейсов.
func (s *Service) ListCases(ctx context.Context, q store.Querier) ([]CaseView, error) {
	rows, err := q.Query(ctx, `
		SELECT DISTINCT ON (family_id) family_id, revision FROM lab_cases
		WHERE status = 'published' ORDER BY family_id, revision DESC`)
	if err != nil {
		return nil, err
	}
	keys, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (caseKey, error) {
		var k caseKey
		return k, r.Scan(&k.family, &k.revision)
	})
	if err != nil {
		return nil, err
	}
	out := []CaseView{}
	for _, k := range keys {
		p, err := s.pack(ctx, q, k.family, k.revision)
		if err != nil {
			return nil, err
		}
		out = append(out, caseView(p))
	}
	return out, nil
}

// DecisionView — неизменяемое решение.
type DecisionView struct {
	DecisionID       string     `json:"decisionId"`
	ExperimentID     string     `json:"experimentId"`
	Kind             string     `json:"kind"` // original | alternative
	SourceDecisionID *string    `json:"sourceDecisionId"`
	Allocation       Allocation `json:"allocation"`
	ReasonID         string     `json:"reasonId"`
	ReasonStatus     string     `json:"reasonStatus"`
	ConstraintStatus string     `json:"constraintStatus"`
	Note             *string    `json:"note"`
	CommittedAt      time.Time  `json:"committedAt"`
}

// RunSummary — расчёт в истории эксперимента.
type RunSummary struct {
	RunID       string `json:"runId"`
	DecisionID  string `json:"decisionId"`
	ScenarioID  string `json:"scenarioId"`
	EndingMinor int64  `json:"endingMinor"`
	ProfitMinor int64  `json:"profitMinor"`
	Reflected   bool   `json:"reflected"`
}

// Completion — что осталось для полного кейса (§10).
type Completion struct {
	OriginalDecision    bool `json:"originalDecision"`
	OriginalRuns        int  `json:"originalRuns"`
	OriginalReflections int  `json:"originalReflections"`
	Required            int  `json:"required"`
	Comparison          bool `json:"comparison"`
	Complete            bool `json:"complete"`
}

// ExperimentView — эксперимент с закреплёнными условиями и историей решений.
type ExperimentView struct {
	ExperimentID string          `json:"experimentId"`
	Status       string          `json:"status"`
	CreatedAt    time.Time       `json:"createdAt"`
	CompletedAt  *time.Time      `json:"completedAt"`
	Case         CaseView        `json:"case"`
	Decisions    []DecisionView  `json:"decisions"`
	Runs         []RunSummary    `json:"runs"`
	Completion   Completion      `json:"completion"`
	Comparison   *ComparisonView `json:"comparison"`
}

type experimentRow struct {
	id, userID, family, status string
	revision                   int
	createdAt                  time.Time
	completedAt                *time.Time
	outcome, reasonID          *string
	reasonStatus, altID        *string
	comparedAt                 *time.Time
}

const experimentColumns = `id, user_id, case_family_id, status, case_revision, created_at, completed_at,
	comparison_outcome, comparison_reason_id, comparison_reason_status, comparison_alternative_id, compared_at`

func scanExperiment(row pgx.Row) (experimentRow, error) {
	var e experimentRow
	err := row.Scan(&e.id, &e.userID, &e.family, &e.status, &e.revision, &e.createdAt, &e.completedAt,
		&e.outcome, &e.reasonID, &e.reasonStatus, &e.altID, &e.comparedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, core.NotFound()
	}
	return e, err
}

func (s *Service) loadExperiment(ctx context.Context, q store.Querier, userID, id string, lock bool) (experimentRow, error) {
	sql := `SELECT ` + experimentColumns + ` FROM lab_experiments WHERE user_id = $1 AND id = $2`
	if lock {
		sql += ` FOR UPDATE`
	}
	return scanExperiment(q.QueryRow(ctx, sql, userID, id))
}

func errCompleted() *core.Error {
	return core.Conflict("EXPERIMENT_COMPLETED", "кейс уже завершён; другой ответ — в новом эксперименте")
}

// CreateExperiment закрепляет последнюю опубликованную редакцию кейса. Незавершённый
// эксперимент того же кейса возвращается повторно.
func (s *Service) CreateExperiment(ctx context.Context, tx pgx.Tx, userID, family string, now time.Time) (ExperimentView, bool, error) {
	var open string
	err := tx.QueryRow(ctx, `SELECT id FROM lab_experiments WHERE user_id = $1 AND case_family_id = $2 AND status = 'open'
		ORDER BY created_at LIMIT 1`, userID, family).Scan(&open)
	if err == nil {
		v, err := s.GetExperiment(ctx, tx, userID, open)
		return v, false, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ExperimentView{}, false, err
	}
	var revision int
	err = tx.QueryRow(ctx, `SELECT revision FROM lab_cases WHERE family_id = $1 AND status = 'published' ORDER BY revision DESC LIMIT 1`,
		family).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExperimentView{}, false, core.NotFound()
	}
	if err != nil {
		return ExperimentView{}, false, err
	}
	p, err := s.pack(ctx, tx, family, revision)
	if err != nil {
		return ExperimentView{}, false, err
	}
	id := s.IDs.New("lex")
	if _, err := tx.Exec(ctx, `
		INSERT INTO lab_experiments (id, user_id, case_family_id, case_revision, model_version, bundle_revision, initial_minor, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'open', $8)`,
		id, userID, family, revision, p.Model.Version, p.Case.BundleRevision, p.Case.InitialMinor, now); err != nil {
		return ExperimentView{}, false, err
	}
	v, err := s.GetExperiment(ctx, tx, userID, id)
	return v, true, err
}

// ListExperiments — история: устаревшая витрина не лишает пользователя истории (§10).
func (s *Service) ListExperiments(ctx context.Context, q store.Querier, userID string) ([]ExperimentView, error) {
	rows, err := q.Query(ctx, `SELECT id FROM lab_experiments WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := []ExperimentView{}
	for _, id := range ids {
		v, err := s.GetExperiment(ctx, q, userID, id)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// GetExperiment собирает эксперимент.
func (s *Service) GetExperiment(ctx context.Context, q store.Querier, userID, id string) (ExperimentView, error) {
	e, err := s.loadExperiment(ctx, q, userID, id, false)
	if err != nil {
		return ExperimentView{}, err
	}
	p, err := s.pack(ctx, q, e.family, e.revision)
	if err != nil {
		return ExperimentView{}, err
	}
	v := ExperimentView{ExperimentID: e.id, Status: e.status, CreatedAt: e.createdAt.UTC(), CompletedAt: utc(e.completedAt),
		Case: caseView(p), Decisions: []DecisionView{}, Runs: []RunSummary{}}
	if v.Decisions, err = s.decisions(ctx, q, userID, `experiment_id = $2`, id); err != nil {
		return ExperimentView{}, err
	}
	rows, err := q.Query(ctx, `
		SELECT r.id, r.decision_id, r.scenario_id, r.ending_minor, r.profit_minor,
		       EXISTS (SELECT 1 FROM lab_reflections f WHERE f.run_id = r.id)
		FROM lab_runs r JOIN lab_decisions d ON d.id = r.decision_id
		WHERE r.user_id = $1 AND r.experiment_id = $2 ORDER BY d.committed_at, r.created_at`, userID, id)
	if err != nil {
		return ExperimentView{}, err
	}
	if v.Runs, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (RunSummary, error) {
		var x RunSummary
		return x, r.Scan(&x.RunID, &x.DecisionID, &x.ScenarioID, &x.EndingMinor, &x.ProfitMinor, &x.Reflected)
	}); err != nil {
		return ExperimentView{}, err
	}
	c := &v.Completion
	c.Required = len(p.Model.Scenarios)
	for _, d := range v.Decisions {
		if d.Kind != "original" {
			continue
		}
		c.OriginalDecision = true
		for _, r := range v.Runs {
			if r.DecisionID == d.DecisionID {
				c.OriginalRuns++
				if r.Reflected {
					c.OriginalReflections++
				}
			}
		}
	}
	if e.comparedAt != nil {
		c.Comparison, c.Complete = true, true
		cv, err := s.comparisonView(ctx, q, userID, e, p)
		if err != nil {
			return ExperimentView{}, err
		}
		v.Comparison = &cv
	}
	return v, nil
}

func (s *Service) decisions(ctx context.Context, q store.Querier, userID, where string, arg string) ([]DecisionView, error) {
	rows, err := q.Query(ctx, `
		SELECT id, experiment_id, source_decision_id, cash_units, bond_units, stock_units, fund_units,
		       reason_id, reason_status, constraint_status, note, committed_at
		FROM lab_decisions WHERE user_id = $1 AND `+where+` ORDER BY committed_at, id`, userID, arg)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (DecisionView, error) {
		var d DecisionView
		a := &d.Allocation
		err := r.Scan(&d.DecisionID, &d.ExperimentID, &d.SourceDecisionID, &a.Cash, &a.Bond, &a.Stock, &a.Fund,
			&d.ReasonID, &d.ReasonStatus, &d.ConstraintStatus, &d.Note, &d.CommittedAt)
		d.Kind = "original"
		if d.SourceDecisionID != nil {
			d.Kind = "alternative"
		}
		d.CommittedAt = d.CommittedAt.UTC()
		return d, err
	})
	if list == nil {
		list = []DecisionView{}
	}
	return list, err
}

// GetDecision — сохранённое решение.
func (s *Service) GetDecision(ctx context.Context, q store.Querier, userID, id string) (DecisionView, error) {
	list, err := s.decisions(ctx, q, userID, `id = $2`, id)
	if err != nil {
		return DecisionView{}, err
	}
	if len(list) == 0 {
		return DecisionView{}, core.NotFound()
	}
	return list[0], nil
}

// DecisionInput — состав и причина до последствий.
type DecisionInput struct {
	Allocation Allocation
	ReasonID   string
	Note       *string
}

// CommitDecision фиксирует решение до исходов. Первое — original; следующие — альтернативы со
// ссылкой на original. Нарушение резерва — статус условия, а не ошибка формата (§10).
func (s *Service) CommitDecision(ctx context.Context, tx pgx.Tx, userID, expID string, in DecisionInput, now time.Time) (DecisionView, error) {
	e, err := s.loadExperiment(ctx, tx, userID, expID, true)
	if err != nil {
		return DecisionView{}, err
	}
	if e.status != "open" {
		return DecisionView{}, errCompleted()
	}
	p, err := s.pack(ctx, tx, e.family, e.revision)
	if err != nil {
		return DecisionView{}, err
	}
	a := in.Allocation
	if !a.Valid(p.Model.Units) {
		return DecisionView{}, core.Unprocessable("INVALID_ALLOCATION", "распредели все 10 долей целыми числами").
			WithField("allocation", "нужно ровно 10 долей, каждая от 0 до 10")
	}
	reason, ok := findReason(p.Case.DecisionReasons, in.ReasonID)
	if !ok {
		return DecisionView{}, core.Unprocessable("UNKNOWN_OPTION", "такой причины нет в этом кейсе").WithField("reasonId", "неизвестная причина")
	}
	var note *string
	if in.Note != nil {
		if n := strings.TrimSpace(*in.Note); n != "" {
			if utf8.RuneCountInString(n) > noteMaxRunes {
				return DecisionView{}, core.Validation("note", "не больше 500 символов")
			}
			note = &n
		}
	}
	constraint := "met"
	if int64(a.Cash)*p.Model.UnitMinor < p.Case.ReserveMinMinor {
		constraint = "reserve_violated"
	}
	// Первое решение — original; последующие ссылаются на него.
	var source *string
	var alternatives int
	if err := tx.QueryRow(ctx, `
		SELECT (SELECT id FROM lab_decisions WHERE experiment_id = $1 AND source_decision_id IS NULL),
		       (SELECT count(*) FROM lab_decisions WHERE experiment_id = $1 AND source_decision_id IS NOT NULL)`, expID).
		Scan(&source, &alternatives); err != nil {
		return DecisionView{}, err
	}
	if source != nil && alternatives >= maxAlternatives {
		return DecisionView{}, core.Conflict("DECISION_LIMIT", "в эксперименте уже много альтернатив; начни новый эксперимент")
	}
	id := s.IDs.New("ldc")
	if _, err := tx.Exec(ctx, `
		INSERT INTO lab_decisions (id, user_id, experiment_id, source_decision_id, cash_units, bond_units, stock_units, fund_units,
		                           reason_id, note, constraint_status, reason_status, committed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		id, userID, expID, source, a.Cash, a.Bond, a.Stock, a.Fund, in.ReasonID, note, constraint, reason.Status, now); err != nil {
		return DecisionView{}, err
	}
	if err := s.Prog.LabStep(ctx, tx, userID, "lab_decision_committed", "lab_decision", id, now); err != nil {
		return DecisionView{}, err
	}
	return s.GetDecision(ctx, tx, userID, id)
}

// ProbeView — проверочный вопрос без ключа.
type ProbeView struct {
	Question string   `json:"question"`
	Choices  []Option `json:"choices"`
	Reasons  []Option `json:"reasons"`
}

// ReflectionView — принятый структурированный вывод и разбор.
type ReflectionView struct {
	ChoiceID       string    `json:"choiceId"`
	ReasonID       string    `json:"reasonId"`
	ChoiceCorrect  bool      `json:"choiceCorrect"`
	ReasonCorrect  bool      `json:"reasonCorrect"`
	Passed         bool      `json:"passed"`
	Explanation    string    `json:"explanation"`
	ChoiceFeedback string    `json:"choiceFeedback,omitempty"`
	ReasonFeedback string    `json:"reasonFeedback,omitempty"`
	AcceptedAt     time.Time `json:"acceptedAt"`
}

// RunView — расчёт сценария и вопрос.
type RunView struct {
	RunID         string          `json:"runId"`
	ExperimentID  string          `json:"experimentId"`
	DecisionID    string          `json:"decisionId"`
	DecisionKind  string          `json:"decisionKind"`
	ScenarioID    string          `json:"scenarioId"`
	ScenarioTitle string          `json:"scenarioTitle"`
	Months        int             `json:"months"`
	ModelVersion  string          `json:"modelVersion"`
	Rates         map[string]Rate `json:"rates"`
	Result        Result          `json:"result"`
	Probe         ProbeView       `json:"probe"`
	Reflection    *ReflectionView `json:"reflection"`
	CreatedAt     time.Time       `json:"createdAt"`
	Notice        string          `json:"notice"`
}

// runSnapshot — вход расчёта для воспроизведения (§11).
type runSnapshot struct {
	ModelVersion   string     `json:"modelVersion"`
	CaseFamilyID   string     `json:"caseFamilyId"`
	CaseRevision   int        `json:"caseRevision"`
	BundleRevision int        `json:"bundleRevision"`
	Allocation     Allocation `json:"allocation"`
	InitialMinor   int64      `json:"initialMinor"`
	Scenario       Scenario   `json:"scenario"`
}

// CreateRun считает сценарий для решения. Один run на decision и сценарий: повтор возвращает
// его (created=false), сохранённый результат не пересчитывается новым кодом (§11).
func (s *Service) CreateRun(ctx context.Context, tx pgx.Tx, userID, expID, decisionID, scenarioID string, now time.Time) (RunView, bool, error) {
	e, err := s.loadExperiment(ctx, tx, userID, expID, true)
	if err != nil {
		return RunView{}, false, err
	}
	d, err := s.GetDecision(ctx, tx, userID, decisionID)
	if err != nil {
		return RunView{}, false, err
	}
	if d.ExperimentID != expID {
		return RunView{}, false, core.NotFound()
	}
	var existing string
	err = tx.QueryRow(ctx, `SELECT id FROM lab_runs WHERE decision_id = $1 AND scenario_id = $2`, decisionID, scenarioID).Scan(&existing)
	if err == nil {
		v, err := s.GetRun(ctx, tx, userID, existing)
		return v, false, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RunView{}, false, err
	}
	if e.status != "open" {
		return RunView{}, false, errCompleted()
	}
	p, err := s.pack(ctx, tx, e.family, e.revision)
	if err != nil {
		return RunView{}, false, err
	}
	sc, ok := p.Model.Scenario(scenarioID)
	if !ok {
		return RunView{}, false, core.Unprocessable("UNKNOWN_OPTION", "такого сценария нет в кейсе").WithField("scenarioId", "неизвестный сценарий")
	}
	res, err := Evaluate(&p.Model, d.Allocation, scenarioID)
	if err != nil {
		return RunView{}, false, err
	}
	snap, err := json.Marshal(runSnapshot{ModelVersion: p.Model.Version, CaseFamilyID: e.family, CaseRevision: e.revision,
		BundleRevision: p.Case.BundleRevision, Allocation: d.Allocation, InitialMinor: p.Case.InitialMinor, Scenario: sc})
	if err != nil {
		return RunView{}, false, err
	}
	result, err := json.Marshal(res)
	if err != nil {
		return RunView{}, false, err
	}
	id := s.IDs.New("lrn")
	if _, err := tx.Exec(ctx, `
		INSERT INTO lab_runs (id, user_id, experiment_id, decision_id, scenario_id, model_version, input_snapshot, input_sha256,
		                      result, ending_minor, profit_minor, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		id, userID, expID, decisionID, scenarioID, p.Model.Version, snap, sha(snap), result, res.Ending, res.Profit, now); err != nil {
		return RunView{}, false, err
	}
	if d.Kind == "original" {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM lab_runs WHERE decision_id = $1`, decisionID).Scan(&n); err != nil {
			return RunView{}, false, err
		}
		if n == len(p.Model.Scenarios) {
			if err := s.Prog.ThreeScenarios(ctx, tx, userID, decisionID, now); err != nil {
				return RunView{}, false, err
			}
		}
	}
	v, err := s.GetRun(ctx, tx, userID, id)
	return v, true, err
}

// GetRun — расчёт с вопросом без ключа и с принятым разбором, если он есть.
func (s *Service) GetRun(ctx context.Context, q store.Querier, userID, runID string) (RunView, error) {
	var v RunView
	var snapRaw, resRaw []byte
	var family string
	var revision int
	var source *string
	err := q.QueryRow(ctx, `
		SELECT r.id, r.experiment_id, r.decision_id, r.scenario_id, r.model_version, r.input_snapshot, r.result, r.created_at,
		       e.case_family_id, e.case_revision, d.source_decision_id
		FROM lab_runs r JOIN lab_experiments e ON e.id = r.experiment_id JOIN lab_decisions d ON d.id = r.decision_id
		WHERE r.user_id = $1 AND r.id = $2`, userID, runID).
		Scan(&v.RunID, &v.ExperimentID, &v.DecisionID, &v.ScenarioID, &v.ModelVersion, &snapRaw, &resRaw, &v.CreatedAt,
			&family, &revision, &source)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, core.NotFound()
	}
	if err != nil {
		return v, err
	}
	var snap runSnapshot
	if err := json.Unmarshal(snapRaw, &snap); err != nil {
		return v, err
	}
	if err := json.Unmarshal(resRaw, &v.Result); err != nil {
		return v, err
	}
	p, err := s.pack(ctx, q, family, revision)
	if err != nil {
		return v, err
	}
	v.CreatedAt = v.CreatedAt.UTC()
	v.DecisionKind = "original"
	if source != nil {
		v.DecisionKind = "alternative"
	}
	v.ScenarioTitle, v.Months, v.Rates, v.Notice = snap.Scenario.Title, snap.Scenario.Months, snap.Scenario.Rates, p.Model.Notice
	probe := p.Case.Probes[v.ScenarioID]
	v.Probe = ProbeView{Question: probe.Question, Choices: probe.Choices, Reasons: probe.Reasons}

	var rv ReflectionView
	var fb []byte
	err = q.QueryRow(ctx, `
		SELECT choice_id, reason_id, choice_correct, reason_correct, passed, feedback, accepted_at
		FROM lab_reflections WHERE user_id = $1 AND run_id = $2`, userID, runID).
		Scan(&rv.ChoiceID, &rv.ReasonID, &rv.ChoiceCorrect, &rv.ReasonCorrect, &rv.Passed, &fb, &rv.AcceptedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	var f struct{ Explanation, Choice, Reason string }
	if err := json.Unmarshal(fb, &f); err != nil {
		return v, err
	}
	rv.Explanation, rv.ChoiceFeedback, rv.ReasonFeedback = f.Explanation, f.Choice, f.Reason
	rv.AcceptedAt = rv.AcceptedAt.UTC()
	v.Reflection = &rv
	return v, nil
}

// Reflect принимает вывод по расчёту. Первый вывод неизменен: тот же ответ возвращает его,
// другой — 409 REFLECTION_EXISTS (§10).
func (s *Service) Reflect(ctx context.Context, tx pgx.Tx, userID, runID, choiceID, reasonID string, now time.Time) (RunView, bool, error) {
	v, err := s.GetRun(ctx, tx, userID, runID)
	if err != nil {
		return RunView{}, false, err
	}
	e, err := s.loadExperiment(ctx, tx, userID, v.ExperimentID, true)
	if err != nil {
		return RunView{}, false, err
	}
	if v.Reflection != nil {
		if v.Reflection.ChoiceID != choiceID || v.Reflection.ReasonID != reasonID {
			return RunView{}, false, core.Conflict("REFLECTION_EXISTS", "вывод по этому расчёту уже сохранён")
		}
		return v, false, nil
	}
	if e.status != "open" {
		return RunView{}, false, errCompleted()
	}
	p, err := s.pack(ctx, tx, e.family, e.revision)
	if err != nil {
		return RunView{}, false, err
	}
	probe := p.Case.Probes[v.ScenarioID]
	if !hasOption(probe.Choices, choiceID) || !hasOption(probe.Reasons, reasonID) {
		return RunView{}, false, core.Unprocessable("UNKNOWN_OPTION", "такого варианта нет в этом вопросе")
	}
	choiceOK, reasonOK, passed := probe.Grade(choiceID, reasonID)
	fb := map[string]string{"explanation": probe.Explanation}
	if !choiceOK {
		fb["choice"] = probe.Feedback.Choice
	}
	if !reasonOK {
		fb["reason"] = probe.Feedback.Reason
	}
	fbJSON, err := json.Marshal(fb)
	if err != nil {
		return RunView{}, false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO lab_reflections (id, user_id, run_id, choice_id, reason_id, choice_correct, reason_correct, passed, feedback, accepted_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		s.IDs.New("lrf"), userID, runID, choiceID, reasonID, choiceOK, reasonOK, passed, fbJSON, now); err != nil {
		return RunView{}, false, err
	}
	if err := s.Prog.LabStep(ctx, tx, userID, "lab_reflection_accepted", "lab_run", runID, now); err != nil {
		return RunView{}, false, err
	}
	v, err = s.GetRun(ctx, tx, userID, runID)
	return v, true, err
}

// ComparisonInput — итог «сохраняю/меняю».
type ComparisonInput struct {
	Outcome               string
	ReasonID              string
	AlternativeDecisionID *string
}

// Outcomes — итоги сценария для одного решения.
type Outcomes struct {
	DecisionID  string `json:"decisionId"`
	EndingMinor int64  `json:"endingMinor"`
	ProfitMinor int64  `json:"profitMinor"`
}

// ComparisonRow — сопоставление при том же пакете, модели и капитале.
type ComparisonRow struct {
	ScenarioID  string    `json:"scenarioId"`
	Months      int       `json:"months"`
	Original    Outcomes  `json:"original"`
	Alternative *Outcomes `json:"alternative"`
}

// ComparisonView — сохранённое итоговое сравнение.
type ComparisonView struct {
	Outcome               string          `json:"outcome"`
	ReasonID              string          `json:"reasonId"`
	ReasonStatus          string          `json:"reasonStatus"`
	AlternativeDecisionID *string         `json:"alternativeDecisionId"`
	ComparedAt            time.Time       `json:"comparedAt"`
	Rows                  []ComparisonRow `json:"rows"`
}

// Compare сохраняет итог и завершает кейс. Нужны: исходное решение, все его расчёты и выводы
// по ним; при revise — альтернатива того же эксперимента со всеми расчётами (§10).
func (s *Service) Compare(ctx context.Context, tx pgx.Tx, userID, expID string, in ComparisonInput, now time.Time) (ComparisonView, bool, error) {
	e, err := s.loadExperiment(ctx, tx, userID, expID, true)
	if err != nil {
		return ComparisonView{}, false, err
	}
	p, err := s.pack(ctx, tx, e.family, e.revision)
	if err != nil {
		return ComparisonView{}, false, err
	}
	if e.comparedAt != nil {
		if *e.outcome != in.Outcome || *e.reasonID != in.ReasonID || !sameID(e.altID, in.AlternativeDecisionID) {
			return ComparisonView{}, false, core.Conflict("COMPARISON_EXISTS", "итог кейса уже сохранён; другой — в новом эксперименте")
		}
		v, err := s.comparisonView(ctx, tx, userID, e, p)
		return v, false, err
	}
	var reasons []Reason
	switch in.Outcome {
	case "keep":
		reasons = p.Case.Comparison.KeepReasons
		if in.AlternativeDecisionID != nil {
			return ComparisonView{}, false, core.Validation("alternativeDecisionId", "при сохранении плана альтернатива не указывается")
		}
	case "revise":
		reasons = p.Case.Comparison.ReviseReasons
		if in.AlternativeDecisionID == nil {
			return ComparisonView{}, false, core.Validation("alternativeDecisionId", "при пересмотре нужна альтернатива")
		}
	default:
		return ComparisonView{}, false, core.Validation("outcome", "ожидалось keep или revise")
	}
	reason, ok := findReason(reasons, in.ReasonID)
	if !ok {
		return ComparisonView{}, false, core.Unprocessable("UNKNOWN_OPTION", "такой причины нет для этого итога").WithField("reasonId", "неизвестная причина")
	}
	ev, err := s.GetExperiment(ctx, tx, userID, expID)
	if err != nil {
		return ComparisonView{}, false, err
	}
	c := ev.Completion
	if !c.OriginalDecision || c.OriginalRuns < c.Required || c.OriginalReflections < c.Required {
		return ComparisonView{}, false, core.Conflict("COMPARISON_NOT_READY",
			"сначала сохрани решение, посмотри все сценарии и ответь на вопросы по каждому")
	}
	if in.Outcome == "revise" {
		i := slices.IndexFunc(ev.Decisions, func(d DecisionView) bool { return d.DecisionID == *in.AlternativeDecisionID })
		if i < 0 || ev.Decisions[i].Kind != "alternative" {
			return ComparisonView{}, false, core.Validation("alternativeDecisionId", "нужна альтернатива из этого эксперимента")
		}
		runs := 0
		for _, r := range ev.Runs {
			if r.DecisionID == *in.AlternativeDecisionID {
				runs++
			}
		}
		if runs < c.Required {
			return ComparisonView{}, false, core.Conflict("COMPARISON_NOT_READY", "посмотри все сценарии для альтернативы при тех же условиях")
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE lab_experiments SET status = 'completed', completed_at = $2, compared_at = $2, comparison_outcome = $3,
		       comparison_reason_id = $4, comparison_reason_status = $5, comparison_alternative_id = $6
		WHERE id = $1`, expID, now, in.Outcome, in.ReasonID, reason.Status, in.AlternativeDecisionID); err != nil {
		return ComparisonView{}, false, err
	}
	if err := s.Prog.LabCaseCompleted(ctx, tx, userID, expID, e.family, now); err != nil {
		return ComparisonView{}, false, err
	}
	if e, err = s.loadExperiment(ctx, tx, userID, expID, false); err != nil {
		return ComparisonView{}, false, err
	}
	v, err := s.comparisonView(ctx, tx, userID, e, p)
	return v, true, err
}

// GetComparison — сохранённое сравнение; до него — 404.
func (s *Service) GetComparison(ctx context.Context, q store.Querier, userID, expID string) (ComparisonView, error) {
	e, err := s.loadExperiment(ctx, q, userID, expID, false)
	if err != nil {
		return ComparisonView{}, err
	}
	if e.comparedAt == nil {
		return ComparisonView{}, core.NotFound()
	}
	p, err := s.pack(ctx, q, e.family, e.revision)
	if err != nil {
		return ComparisonView{}, err
	}
	return s.comparisonView(ctx, q, userID, e, p)
}

func (s *Service) comparisonView(ctx context.Context, q store.Querier, userID string, e experimentRow, p *Pack) (ComparisonView, error) {
	v := ComparisonView{Outcome: *e.outcome, ReasonID: *e.reasonID, ReasonStatus: *e.reasonStatus,
		AlternativeDecisionID: e.altID, ComparedAt: e.comparedAt.UTC()}
	var original string
	if err := q.QueryRow(ctx, `SELECT id FROM lab_decisions WHERE experiment_id = $1 AND source_decision_id IS NULL`, e.id).Scan(&original); err != nil {
		return v, err
	}
	outcomes := func(decisionID, scenarioID string) (Outcomes, error) {
		o := Outcomes{DecisionID: decisionID}
		err := q.QueryRow(ctx, `SELECT ending_minor, profit_minor FROM lab_runs WHERE user_id = $1 AND decision_id = $2 AND scenario_id = $3`,
			userID, decisionID, scenarioID).Scan(&o.EndingMinor, &o.ProfitMinor)
		return o, err
	}
	for _, sc := range p.Model.Scenarios {
		row := ComparisonRow{ScenarioID: sc.ID, Months: sc.Months}
		var err error
		if row.Original, err = outcomes(original, sc.ID); err != nil {
			return v, err
		}
		if e.altID != nil {
			alt, err := outcomes(*e.altID, sc.ID)
			if err != nil {
				return v, err
			}
			row.Alternative = &alt
		}
		v.Rows = append(v.Rows, row)
	}
	return v, nil
}

func sameID(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	return new(t.UTC())
}
