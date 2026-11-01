package progression

import (
	"context"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// RoleView — роль участия.
type RoleView struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// Requirement — недостающее условие следующей роли.
type Requirement struct {
	Kind string `json:"kind"` // xp | learningFamilies | modulesWithCompletion | completeLabCase
	Have int    `json:"have"`
	Need int    `json:"need"`
}

// NextRole — ближайшая роль и что для неё нужно.
type NextRole struct {
	RoleView
	Missing []Requirement `json:"missing"`
}

// Participation — XP и роль: показывают выполненную работу, а не уровень знаний (§13).
type Participation struct {
	XP                    int       `json:"xp"`
	MaxXP                 int       `json:"maxXp"`
	Role                  RoleView  `json:"role"`
	NextRole              *NextRole `json:"nextRole"`
	LearningFamilies      int       `json:"learningFamilies"`
	ModulesWithCompletion int       `json:"modulesWithCompletion"`
	LabCasesCompleted     int       `json:"labCasesCompleted"`
}

// Activity — регулярность: серия косметическая и ничего не закрывает (§15).
type Activity struct {
	Timezone             string `json:"timezone"`
	TimezoneLocked       bool   `json:"timezoneLocked"`
	Today                string `json:"today"`
	TodayStatus          string `json:"todayStatus"` // active | pending
	CurrentActiveDays    int    `json:"currentActiveDays"`
	CurrentProtectedDays int    `json:"currentProtectedDays"`
	BestActiveDays       int    `json:"bestActiveDays"`
	TotalActiveDays      int    `json:"totalActiveDays"`
	FreezeCharges        int    `json:"freezeCharges"`
}

// EvidenceView — конкретный результат в новой задаче (§12).
type EvidenceView struct {
	OutcomeID          string    `json:"outcomeId"`
	Text               string    `json:"text"`
	AcceptedAt         time.Time `json:"acceptedAt"`
	LocalDate          string    `json:"localDate"`
	ActualDelaySeconds *int64    `json:"actualDelaySeconds"`
	Status             string    `json:"status"`
}

// Coverage — охват отложенных проверок. Correct — только по финализированным наборам;
// процент среди ответивших не подменяет охват (§12).
type Coverage struct {
	Assigned int `json:"assigned"`
	Answered int `json:"answered"`
	Correct  int `json:"correct"`
	Skipped  int `json:"skipped"`
	Pending  int `json:"pending"`
	Excluded int `json:"excluded"`
}

// Results — учебные результаты.
type Results struct {
	Evidence []EvidenceView `json:"evidence"`
	Review   Coverage       `json:"review"`
}

// BadgeView — выданный бейдж.
type BadgeView struct {
	BadgeID            string     `json:"badgeId"`
	Title              string     `json:"title"`
	GrantedAt          time.Time  `json:"grantedAt"`
	Confirmation       string     `json:"confirmation"`
	ConfirmedLaterAt   *time.Time `json:"confirmedLaterAt"`
	ConfirmationStatus string     `json:"confirmationStatus"`
}

// Progress — GET /progress: участие, активность, результаты и бейджи отдельно (§12).
type Progress struct {
	Participation Participation `json:"participation"`
	Activity      Activity      `json:"activity"`
	Results       Results       `json:"results"`
	Badges        []BadgeView   `json:"badges"`
}

// Progress собирает прогресс. Перед чтением серии завершённые дни обрабатываются (§15),
// поэтому вызывается в транзакции с захваченным пользователем.
func (s *Service) Progress(ctx context.Context, tx pgx.Tx, userID string, now time.Time) (Progress, error) {
	var p Progress
	loc, name, err := learningLocation(ctx, tx, userID, false, now)
	if err != nil {
		return p, err
	}
	today := civil(now, loc)
	st, err := s.settle(ctx, tx, userID, today, now)
	if err != nil {
		return p, err
	}
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT timezone_locked_at IS NOT NULL FROM users WHERE id = $1`, userID).Scan(&locked); err != nil {
		return p, err
	}
	p.Activity = Activity{
		Timezone: name, TimezoneLocked: locked, Today: today.Format(time.DateOnly), TodayStatus: "pending",
		BestActiveDays: st.best, TotalActiveDays: st.totalActive, FreezeCharges: st.charges,
	}
	if st.todayActive {
		p.Activity.TodayStatus = "active"
	}
	if st.chain {
		p.Activity.CurrentActiveDays, p.Activity.CurrentProtectedDays = st.curActive, st.curProtected
	}

	pa := &p.Participation
	pa.MaxXP = s.Rules.XP.MaxFullBank
	if err := tx.QueryRow(ctx, `
		SELECT coalesce((SELECT sum(amount) FROM xp_ledger WHERE user_id = $1), 0),
		       (SELECT count(*) FROM learning_completions WHERE user_id = $1),
		       (SELECT count(DISTINCT f.module_id) FROM learning_completions c JOIN content_families f ON f.id = c.family_id WHERE c.user_id = $1),
		       (SELECT count(DISTINCT case_family_id) FROM lab_experiments WHERE user_id = $1 AND status = 'completed')`, userID).
		Scan(&pa.XP, &pa.LearningFamilies, &pa.ModulesWithCompletion, &pa.LabCasesCompleted); err != nil {
		return p, err
	}
	s.roles(pa)

	if p.Results, err = s.results(ctx, tx, userID, loc); err != nil {
		return p, err
	}
	rows, err := tx.Query(ctx, `
		SELECT badge_id, granted_at, confirmation, confirmed_later_at, confirmation_status
		FROM badge_grants WHERE user_id = $1 ORDER BY granted_at, badge_id`, userID)
	if err != nil {
		return p, err
	}
	p.Badges, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (BadgeView, error) {
		var b BadgeView
		err := r.Scan(&b.BadgeID, &b.GrantedAt, &b.Confirmation, &b.ConfirmedLaterAt, &b.ConfirmationStatus)
		b.Title = s.Rules.badge(b.BadgeID).Title
		b.GrantedAt = b.GrantedAt.UTC()
		if b.ConfirmedLaterAt != nil {
			b.ConfirmedLaterAt = new(b.ConfirmedLaterAt.UTC())
		}
		return b, err
	})
	if p.Badges == nil {
		p.Badges = []BadgeView{}
	}
	return p, err
}

// roles — текущая роль требует выполнения условий всех предыдущих ролей (§13).
func (s *Service) roles(pa *Participation) {
	missing := func(r Role) []Requirement {
		var m []Requirement
		if pa.XP < r.MinXP {
			m = append(m, Requirement{Kind: "xp", Have: pa.XP, Need: r.MinXP})
		}
		if pa.LearningFamilies < r.MinLearningFamilies {
			m = append(m, Requirement{Kind: "learningFamilies", Have: pa.LearningFamilies, Need: r.MinLearningFamilies})
		}
		if pa.ModulesWithCompletion < r.MinModulesWithCompletion {
			m = append(m, Requirement{Kind: "modulesWithCompletion", Have: pa.ModulesWithCompletion, Need: r.MinModulesWithCompletion})
		}
		if r.RequiresCompleteLabCase && pa.LabCasesCompleted == 0 {
			m = append(m, Requirement{Kind: "completeLabCase", Have: 0, Need: 1})
		}
		return m
	}
	for _, r := range s.Rules.Roles {
		if m := missing(r); len(m) > 0 {
			pa.NextRole = &NextRole{RoleView: RoleView{ID: r.ID, Title: r.Title}, Missing: m}
			return
		}
		pa.Role = RoleView{ID: r.ID, Title: r.Title}
	}
}

func (s *Service) results(ctx context.Context, tx pgx.Tx, userID string, loc *time.Location) (Results, error) {
	res := Results{Evidence: []EvidenceView{}}
	var version string
	if err := tx.QueryRow(ctx, `SELECT program_version FROM enrollments WHERE user_id = $1`, userID).Scan(&version); err != nil {
		return res, err
	}
	m, err := s.Catalog.Manifest(ctx, tx, version)
	if err != nil {
		return res, err
	}
	titles := map[string]string{}
	for _, o := range m.Outcomes {
		titles[o.ID] = o.Title
	}
	rows, err := tx.Query(ctx, `
		SELECT outcome_id, accepted_at, actual_delay_seconds, status FROM learning_evidence
		WHERE user_id = $1 ORDER BY accepted_at`, userID)
	if err != nil {
		return res, err
	}
	res.Evidence, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (EvidenceView, error) {
		var e EvidenceView
		err := r.Scan(&e.OutcomeID, &e.AcceptedAt, &e.ActualDelaySeconds, &e.Status)
		e.AcceptedAt = e.AcceptedAt.UTC()
		e.LocalDate = e.AcceptedAt.In(loc).Format(time.DateOnly)
		// «Получилось в новой задаче: учесть расходы» — без обобщения до «освоено» (§12).
		e.Text = "Получилось в новой задаче: " + lowerFirst(titles[e.OutcomeID])
		return e, err
	})
	if err != nil {
		return res, err
	}
	if res.Evidence == nil {
		res.Evidence = []EvidenceView{}
	}
	c := &res.Review
	err = tx.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE a.status = 'completed'),
		       count(*) FILTER (WHERE a.status = 'completed' AND st.finalized_at IS NOT NULL AND e.passed),
		       count(*) FILTER (WHERE a.status = 'skipped'),
		       count(*) FILTER (WHERE a.status = 'open'),
		       count(*) FILTER (WHERE a.status = 'withdrawn')
		FROM assignments a
		JOIN assessment_sets st ON st.id = a.assessment_set_id
		LEFT JOIN attempts t ON t.assignment_id = a.id AND t.status = 'submitted'
		LEFT JOIN evaluations e ON e.attempt_id = t.id AND e.replaces_evaluation_id IS NULL
		WHERE a.user_id = $1 AND a.role = 'review'`, userID).
		Scan(&c.Assigned, &c.Answered, &c.Correct, &c.Skipped, &c.Pending, &c.Excluded)
	return res, err
}

func lowerFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToLower(r)) + s[n:]
}
