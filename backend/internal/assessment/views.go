package assessment

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"teyljunior/internal/store"
)

// AnchorView — learning задание, которого не хватает для срока review-карточки.
type AnchorView struct {
	FamilyID     string `json:"familyId"`
	AssignmentID string `json:"assignmentId"`
	Title        string `json:"title"`
}

// ItemResult — оценка карточки; есть только после финализации набора.
type ItemResult struct {
	ChoiceCorrect      bool   `json:"choiceCorrect"`
	ReasonCorrect      bool   `json:"reasonCorrect"`
	Passed             bool   `json:"passed"`
	ActualDelaySeconds *int64 `json:"actualDelaySeconds"`
}

// ItemView — карточка набора. Название и itemId открываются вместе с набором; до этого — только
// статус, срок и недостающие якоря (§9).
type ItemView struct {
	AssignmentID     string       `json:"assignmentId"`
	Status           string       `json:"status"`
	ItemID           *string      `json:"itemId"`
	Title            *string      `json:"title"`
	DueAt            *time.Time   `json:"dueAt"`
	AnchorAcceptedAt *time.Time   `json:"anchorAcceptedAt"`
	SubmittedAt      *time.Time   `json:"submittedAt"`
	MissingAnchors   []AnchorView `json:"missingAnchors"`
	Result           *ItemResult  `json:"result"`
}

// Summary — охват набора. Correct появляется только после финализации; процент среди
// ответивших не подменяет охват (§12).
type Summary struct {
	Assigned int  `json:"assigned"`
	Answered int  `json:"answered"`
	Pending  int  `json:"pending"`
	Skipped  int  `json:"skipped"`
	Excluded int  `json:"excluded"`
	Correct  *int `json:"correct"`
}

// SetView — набор целиком.
type SetView struct {
	SetID              string     `json:"setId"`
	Kind               string     `json:"kind"`
	SlotFamilyID       string     `json:"slotFamilyId"`
	Label              string     `json:"label"`
	Status             string     `json:"status"`
	DueAt              *time.Time `json:"dueAt"`
	CreatedAt          time.Time  `json:"createdAt"`
	FinalizedAt        *time.Time `json:"finalizedAt"`
	FinalizedStatus    *string    `json:"finalizedStatus"`
	BaselineEligible   *bool      `json:"baselineEligible"`
	InvalidationReason *string    `json:"invalidationReason"`
	Summary            Summary    `json:"summary"`
	Items              []ItemView `json:"items"`
}

// SetSummary — краткое состояние для bootstrap: без карточек и без результатов.
type SetSummary struct {
	SetID    string     `json:"setId"`
	Kind     string     `json:"kind"`
	Label    string     `json:"label"`
	Status   string     `json:"status"`
	DueAt    *time.Time `json:"dueAt"`
	Answered int        `json:"answered"`
	Assigned int        `json:"assigned"`
}

// Get собирает набор пользователя.
func (s *Service) Get(ctx context.Context, q store.Querier, userID, setID string, now time.Time) (SetView, error) {
	r, err := scanSet(q.QueryRow(ctx, `SELECT `+setColumns+` FROM assessment_sets WHERE user_id = $1 AND id = $2`, userID, setID))
	if err != nil {
		return SetView{}, err
	}
	return s.view(ctx, q, userID, r, now)
}

// List — все наборы пользователя: диагностика, затем отложенные по сроку задержки.
func (s *Service) List(ctx context.Context, q store.Querier, userID string, now time.Time) ([]SetView, error) {
	rows, err := q.Query(ctx, `SELECT `+setColumns+` FROM assessment_sets WHERE user_id = $1
		ORDER BY kind, delay_days NULLS FIRST, generation`, userID)
	if err != nil {
		return nil, err
	}
	sets, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (setRow, error) { return scanSet(r) })
	if err != nil {
		return nil, err
	}
	out := make([]SetView, 0, len(sets))
	for _, r := range sets {
		v, err := s.view(ctx, q, userID, r, now)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Summaries — краткие состояния для bootstrap.
func (s *Service) Summaries(ctx context.Context, q store.Querier, userID string, now time.Time) ([]SetSummary, error) {
	sets, err := s.List(ctx, q, userID, now)
	if err != nil {
		return nil, err
	}
	out := make([]SetSummary, 0, len(sets))
	for _, v := range sets {
		out = append(out, SetSummary{SetID: v.SetID, Kind: v.Kind, Label: v.Label, Status: v.Status, DueAt: v.DueAt,
			Answered: v.Summary.Answered, Assigned: v.Summary.Assigned})
	}
	return out, nil
}

type itemRow struct {
	assignmentID, itemID, familyID, status string
	dueAt, anchorAcceptedAt, submittedAt   *time.Time
	snapshot                               []byte
	choiceOK, reasonOK, passed             *bool
}

func (s *Service) view(ctx context.Context, q store.Querier, userID string, r setRow, now time.Time) (SetView, error) {
	m, err := s.Catalog.Manifest(ctx, q, r.Version)
	if err != nil {
		return SetView{}, err
	}
	v := SetView{
		SetID: r.ID, Kind: r.Kind, SlotFamilyID: r.SlotFamilyID, Status: r.effective(now),
		DueAt: utc(r.DueAt), CreatedAt: r.CreatedAt.UTC(), FinalizedAt: utc(r.FinalizedAt),
		FinalizedStatus: r.FinalizedStatus, BaselineEligible: r.BaselineEligible, InvalidationReason: r.InvalidationReason,
		Label: diagnosticLabel, Items: []ItemView{},
	}
	if rs, ok := m.ReviewSet(r.SlotFamilyID); ok {
		v.Label = rs.Label
	}
	opened := v.Status != StatusWaiting

	rows, err := q.Query(ctx, `
		SELECT a.id, a.item_id, a.family_id, a.status, a.due_at, a.anchor_accepted_at, a.anchor_snapshot,
		       sub.accepted_at, e.choice_correct, e.reason_correct, e.passed
		FROM assignments a
		LEFT JOIN attempts t ON t.assignment_id = a.id AND t.status = 'submitted'
		LEFT JOIN submissions sub ON sub.attempt_id = t.id
		LEFT JOIN evaluations e ON e.attempt_id = t.id AND e.replaces_evaluation_id IS NULL
		WHERE a.user_id = $1 AND a.assessment_set_id = $2 ORDER BY a.item_id`, userID, r.ID)
	if err != nil {
		return SetView{}, err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (itemRow, error) {
		var it itemRow
		err := row.Scan(&it.assignmentID, &it.itemID, &it.familyID, &it.status, &it.dueAt, &it.anchorAcceptedAt,
			&it.snapshot, &it.submittedAt, &it.choiceOK, &it.reasonOK, &it.passed)
		return it, err
	})
	if err != nil {
		return SetView{}, err
	}

	correct := 0
	for _, it := range items {
		iv := ItemView{
			AssignmentID: it.assignmentID, Status: it.status, DueAt: utc(it.dueAt),
			AnchorAcceptedAt: utc(it.anchorAcceptedAt), SubmittedAt: utc(it.submittedAt), MissingAnchors: []AnchorView{},
		}
		if opened {
			iv.ItemID = new(it.itemID)
			k, err := s.Catalog.ItemForFamily(ctx, q, r.Version, it.familyID)
			if err != nil {
				return SetView{}, err
			}
			rev, err := s.Catalog.Revision(ctx, q, k)
			if err != nil {
				return SetView{}, err
			}
			iv.Title = new(rev.Public.Title)
		}
		if it.dueAt == nil && it.snapshot != nil {
			if iv.MissingAnchors, err = s.missingAnchors(ctx, q, userID, r.Version, it.snapshot); err != nil {
				return SetView{}, err
			}
		}
		v.Summary.Assigned++
		switch it.status {
		case "completed":
			v.Summary.Answered++
		case "open":
			v.Summary.Pending++
		case "skipped":
			v.Summary.Skipped++
		case "withdrawn":
			v.Summary.Excluded++
		}
		if r.finalized() && it.passed != nil {
			res := &ItemResult{ChoiceCorrect: *it.choiceOK, ReasonCorrect: *it.reasonOK, Passed: *it.passed}
			if it.anchorAcceptedAt != nil && it.submittedAt != nil {
				res.ActualDelaySeconds = new(int64(it.submittedAt.Sub(*it.anchorAcceptedAt) / time.Second))
			}
			iv.Result = res
			if *it.passed {
				correct++
			}
		}
		v.Items = append(v.Items, iv)
	}
	if r.finalized() {
		v.Summary.Correct = &correct
	}
	return v, nil
}

func (s *Service) missingAnchors(ctx context.Context, q store.Querier, userID, version string, raw []byte) ([]AnchorView, error) {
	var snap anchorSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, err
	}
	out := []AnchorView{}
	for _, fam := range snap.AnchorFamilyIDs {
		var done bool
		var assignmentID string
		err := q.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM learning_completions WHERE user_id = $1 AND family_id = $2),
			       coalesce((SELECT id FROM assignments WHERE user_id = $1 AND family_id = $2 AND role = 'learning'
			                 ORDER BY (status = 'open') DESC, created_at DESC LIMIT 1), '')`, userID, fam).Scan(&done, &assignmentID)
		if err != nil {
			return nil, err
		}
		if done {
			continue
		}
		k, err := s.Catalog.ItemForFamily(ctx, q, version, fam)
		if err != nil {
			return nil, err
		}
		rev, err := s.Catalog.Revision(ctx, q, k)
		if err != nil {
			return nil, err
		}
		out = append(out, AnchorView{FamilyID: fam, AssignmentID: assignmentID, Title: rev.Public.Title})
	}
	return out, nil
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	return new(t.UTC())
}
