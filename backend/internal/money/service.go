package money

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"teyljunior/internal/core"
	"teyljunior/internal/progression"
	"teyljunior/internal/store"
)

// Service — денежные сценарии. Мутации вызываются в транзакции после захвата пользователя.
type Service struct{ IDs core.IDs }

// GoalInput — цель в снимке плана. GoalID совпадает с текущей — правка; пустой — новая цель
// вместо текущей.
type GoalInput struct {
	GoalID      *string `json:"goalId"`
	Title       string  `json:"title"`
	TargetMinor int64   `json:"targetMinor"`
	SavedMinor  int64   `json:"savedMinor"`
	Months      int     `json:"months"`
}

// GoalView — текущая цель с производными.
type GoalView struct {
	GoalID      string    `json:"goalId"`
	Title       string    `json:"title"`
	TargetMinor int64     `json:"targetMinor"`
	SavedMinor  int64     `json:"savedMinor"`
	Months      int       `json:"months"`
	UpdatedAt   time.Time `json:"updatedAt"`
	GoalFigures
}

// PlanView — снимок плана. Revision 0 — план ещё не сохранялся.
type PlanView struct {
	Revision int `json:"revision"`
	Budget
	BudgetFigures
	Goal      *GoalView  `json:"goal"`
	UpdatedAt *time.Time `json:"updatedAt"`
}

// GetPlan читает снимок.
func (s *Service) GetPlan(ctx context.Context, q store.Querier, userID string) (PlanView, error) {
	var v PlanView
	b := &v.Budget
	err := q.QueryRow(ctx, `
		SELECT revision, income_minor, reserve_minor, monthly_contribution_minor, food_minor, transport_minor,
		       housing_minor, other_minor, updated_at
		FROM money_plans WHERE user_id = $1`, userID).
		Scan(&v.Revision, &b.IncomeMinor, &b.ReserveMinor, &b.MonthlyContributionMinor, &b.FoodMinor, &b.TransportMinor,
			&b.HousingMinor, &b.OtherMinor, &v.UpdatedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return v, err
	}
	if v.UpdatedAt != nil {
		v.UpdatedAt = new(v.UpdatedAt.UTC())
	}
	v.BudgetFigures = v.Budget.Figures()
	g, err := activeGoal(ctx, q, userID)
	if err != nil || g == nil {
		return v, err
	}
	g.GoalFigures = GoalFiguresFor(v.Budget, g.TargetMinor, g.SavedMinor, g.Months)
	v.Goal = g
	return v, nil
}

func activeGoal(ctx context.Context, q store.Querier, userID string) (*GoalView, error) {
	var g GoalView
	err := q.QueryRow(ctx, `SELECT id, title, target_minor, saved_minor, months, updated_at FROM goals WHERE user_id = $1 AND active`, userID).
		Scan(&g.GoalID, &g.Title, &g.TargetMinor, &g.SavedMinor, &g.Months, &g.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	g.UpdatedAt = g.UpdatedAt.UTC()
	return &g, err
}

// PlanInput — новый снимок бюджета и цели.
type PlanInput struct {
	ExpectedRevision int
	Budget           Budget
	Goal             *GoalInput
}

func amount(field string, v, minV int64) *core.Error {
	if v < minV || v > AmountMax {
		return core.Validation(field, "целое число копеек от "+strconv.FormatInt(minV, 10)+" до 100000000")
	}
	return nil
}

func cleanText(field, s string, minRunes, maxRunes int) (string, *core.Error) {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	if n < minRunes || n > maxRunes || strings.ContainsFunc(s, unicode.IsControl) {
		return "", core.Validation(field, "от "+strconv.Itoa(minRunes)+" до "+strconv.Itoa(maxRunes)+" символов без управляющих")
	}
	return s, nil
}

// PutPlan атомарно сохраняет бюджет и текущую цель (§19). Старая форма с прежней revision
// получает 409 и не затирает новое изменение.
func (s *Service) PutPlan(ctx context.Context, tx pgx.Tx, userID string, in PlanInput, now time.Time) (PlanView, error) {
	b := in.Budget
	for field, v := range map[string]int64{
		"incomeMinor": b.IncomeMinor, "reserveMinor": b.ReserveMinor, "monthlyContributionMinor": b.MonthlyContributionMinor,
		"categories.food": b.FoodMinor, "categories.transport": b.TransportMinor, "categories.housing": b.HousingMinor, "categories.other": b.OtherMinor,
	} {
		if e := amount(field, v, 0); e != nil {
			return PlanView{}, e
		}
	}
	cur, err := s.GetPlan(ctx, tx, userID)
	if err != nil {
		return PlanView{}, err
	}
	if cur.Revision != in.ExpectedRevision {
		return PlanView{}, core.RevisionConflict("план уже изменился; открой свежую версию и проверь свои изменения")
	}
	if in.Goal != nil {
		g := in.Goal
		title, e := cleanText("goal.title", g.Title, 1, TitleMaxRunes)
		if e != nil {
			return PlanView{}, e
		}
		if e := amount("goal.targetMinor", g.TargetMinor, 1); e != nil {
			return PlanView{}, e
		}
		if e := amount("goal.savedMinor", g.SavedMinor, 0); e != nil {
			return PlanView{}, e
		}
		if g.Months < MonthsMin || g.Months > MonthsMax {
			return PlanView{}, core.Validation("goal.months", "от 1 до 120 месяцев")
		}
		switch {
		case g.GoalID != nil && (cur.Goal == nil || *g.GoalID != cur.Goal.GoalID):
			return PlanView{}, core.Conflict("GOAL_CHANGED", "текущая цель уже другая; открой свежую версию плана")
		case g.GoalID != nil:
			// Ручная правка saved не переписывает прошлые отметки (§19).
			if _, err := tx.Exec(ctx, `UPDATE goals SET title = $2, target_minor = $3, saved_minor = $4, months = $5, updated_at = $6 WHERE id = $1`,
				*g.GoalID, title, g.TargetMinor, g.SavedMinor, g.Months, now); err != nil {
				return PlanView{}, err
			}
		default:
			if _, err := tx.Exec(ctx, `UPDATE goals SET active = false, updated_at = $2 WHERE user_id = $1 AND active`, userID, now); err != nil {
				return PlanView{}, err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO goals (id, user_id, title, target_minor, saved_minor, months, active, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, true, $7, $7)`,
				s.IDs.New("gol"), userID, title, g.TargetMinor, g.SavedMinor, g.Months, now); err != nil {
				return PlanView{}, err
			}
		}
	} else if _, err := tx.Exec(ctx, `UPDATE goals SET active = false, updated_at = $2 WHERE user_id = $1 AND active`, userID, now); err != nil {
		return PlanView{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO money_plans (user_id, revision, income_minor, reserve_minor, monthly_contribution_minor,
		                         food_minor, transport_minor, housing_minor, other_minor, updated_at)
		VALUES ($1, 1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (user_id) DO UPDATE SET revision = money_plans.revision + 1, income_minor = $2, reserve_minor = $3,
		    monthly_contribution_minor = $4, food_minor = $5, transport_minor = $6, housing_minor = $7, other_minor = $8, updated_at = $9`,
		userID, b.IncomeMinor, b.ReserveMinor, b.MonthlyContributionMinor, b.FoodMinor, b.TransportMinor, b.HousingMinor, b.OtherMinor, now); err != nil {
		return PlanView{}, err
	}
	return s.GetPlan(ctx, tx, userID)
}

// bumpPlan увеличивает revision снимка при изменении saved отметкой или действием: старая
// форма плана после этого получит 409, а не перезапишет накопленную сумму.
func bumpPlan(ctx context.Context, tx pgx.Tx, userID string, now time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO money_plans (user_id, revision, income_minor, reserve_minor, monthly_contribution_minor,
		                         food_minor, transport_minor, housing_minor, other_minor, updated_at)
		VALUES ($1, 1, 0, 0, 0, 0, 0, 0, 0, $2)
		ON CONFLICT (user_id) DO UPDATE SET revision = money_plans.revision + 1, updated_at = $2`, userID, now)
	return err
}

// localToday — сегодняшняя дата в учебном поясе пользователя (§15, §19).
func localToday(ctx context.Context, q store.Querier, userID string, now time.Time) (time.Time, error) {
	var tz *string
	if err := q.QueryRow(ctx, `SELECT learning_timezone FROM users WHERE id = $1`, userID).Scan(&tz); err != nil {
		return time.Time{}, err
	}
	name := progression.DefaultTimezone
	if tz != nil {
		name = *tz
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.Time{}, err
	}
	y, m, d := now.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC), nil
}

func parseDate(field, s string) (time.Time, *core.Error) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil || t.Format(time.DateOnly) != s || t.Year() < 1900 {
		return t, core.Validation(field, "существующая дата ГГГГ-ММ-ДД не раньше 1900 года")
	}
	return t, nil
}

// ExpenseView — запись журнала.
type ExpenseView struct {
	ExpenseID   string     `json:"expenseId"`
	Revision    int        `json:"revision"`
	AmountMinor int64      `json:"amountMinor"`
	CategoryID  string     `json:"categoryId"`
	OccurredOn  string     `json:"occurredOn"`
	Note        string     `json:"note"`
	CreatedAt   time.Time  `json:"createdAt"`
	VoidedAt    *time.Time `json:"voidedAt"`
}

// ExpenseInput — новая запись расхода.
type ExpenseInput struct {
	AmountMinor int64
	CategoryID  string
	OccurredOn  string
	Note        string
}

// AddExpense добавляет самоотчёт. Он не уменьшает план, накопленную сумму цели и виртуальный
// капитал. Будущая дата запрещена по учебному поясу; лимит учитывает и отменённые записи.
func (s *Service) AddExpense(ctx context.Context, tx pgx.Tx, userID string, in ExpenseInput, now time.Time) (ExpenseView, error) {
	if e := amount("amountMinor", in.AmountMinor, 1); e != nil {
		return ExpenseView{}, e
	}
	if !slices.Contains(Categories, in.CategoryID) {
		return ExpenseView{}, core.Validation("categoryId", "food, transport, housing или other")
	}
	on, e := parseDate("occurredOn", in.OccurredOn)
	if e != nil {
		return ExpenseView{}, e
	}
	today, err := localToday(ctx, tx, userID, now)
	if err != nil {
		return ExpenseView{}, err
	}
	if on.After(today) {
		return ExpenseView{}, core.Validation("occurredOn", "расход можно записать за сегодня или прошедший день")
	}
	note, e := cleanText("note", in.Note, 0, NoteMaxRunes)
	if e != nil {
		return ExpenseView{}, e
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM expenses WHERE user_id = $1`, userID).Scan(&n); err != nil {
		return ExpenseView{}, err
	}
	if n >= ExpensesMax {
		return ExpenseView{}, core.Conflict("EXPENSE_LIMIT", "в журнале уже 500 записей; отмена записи не освобождает место")
	}
	id := s.IDs.New("exn")
	if _, err := tx.Exec(ctx, `
		INSERT INTO expenses (id, user_id, revision, amount_minor, category_id, occurred_on, note, created_at)
		VALUES ($1, $2, 1, $3, $4, $5, $6, $7)`, id, userID, in.AmountMinor, in.CategoryID, on, note, now); err != nil {
		return ExpenseView{}, err
	}
	return s.expense(ctx, tx, userID, id)
}

const expenseColumns = `id, revision, amount_minor, category_id, occurred_on, note, created_at, voided_at`

func scanExpense(row pgx.Row) (ExpenseView, error) {
	var v ExpenseView
	var on time.Time
	err := row.Scan(&v.ExpenseID, &v.Revision, &v.AmountMinor, &v.CategoryID, &on, &v.Note, &v.CreatedAt, &v.VoidedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, core.NotFound()
	}
	v.OccurredOn = on.Format(time.DateOnly)
	v.CreatedAt = v.CreatedAt.UTC()
	if v.VoidedAt != nil {
		v.VoidedAt = new(v.VoidedAt.UTC())
	}
	return v, err
}

func (s *Service) expense(ctx context.Context, q store.Querier, userID, id string) (ExpenseView, error) {
	return scanExpense(q.QueryRow(ctx, `SELECT `+expenseColumns+` FROM expenses WHERE user_id = $1 AND id = $2`, userID, id))
}

// SetVoided отменяет или восстанавливает запись: меняется voidedAt, строка не удаляется.
func (s *Service) SetVoided(ctx context.Context, tx pgx.Tx, userID, id string, expectedRevision int, voided bool, now time.Time) (ExpenseView, error) {
	v, err := scanExpense(tx.QueryRow(ctx, `SELECT `+expenseColumns+` FROM expenses WHERE user_id = $1 AND id = $2 FOR UPDATE`, userID, id))
	if err != nil {
		return v, err
	}
	if v.Revision != expectedRevision {
		return v, core.RevisionConflict("запись уже изменилась; открой свежую версию")
	}
	if (v.VoidedAt != nil) == voided {
		return v, nil
	}
	var at *time.Time
	if voided {
		at = &now
	}
	if _, err := tx.Exec(ctx, `UPDATE expenses SET voided_at = $2, revision = revision + 1 WHERE id = $1`, id, at); err != nil {
		return v, err
	}
	return s.expense(ctx, tx, userID, id)
}

// Page — страница списка с курсором.
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"nextCursor"`
}

// ExpensePage — страница журнала; итоги периода — только по неотменённым записям.
type ExpensePage struct {
	Page[ExpenseView]
	Month  *string          `json:"month"`
	Totals map[string]int64 `json:"totalsMinor"`
}

type expenseCursor struct {
	On string    `json:"o"`
	At time.Time `json:"a"`
	ID string    `json:"i"`
}

func encodeCursor(v any) *string {
	raw, _ := json.Marshal(v)
	s := base64.RawURLEncoding.EncodeToString(raw)
	return &s
}

func decodeCursor(s string, v any) *core.Error {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || json.Unmarshal(raw, v) != nil {
		return core.Validation("cursor", "некорректный курсор")
	}
	return nil
}

// PageSize проверяет limit (§19): по умолчанию 50, максимум 100.
func PageSize(limit int) (int, *core.Error) {
	switch {
	case limit == 0:
		return PageDefault, nil
	case limit < 0 || limit > PageMax:
		return 0, core.Validation("limit", "от 1 до 100")
	}
	return limit, nil
}

// ListExpenses — журнал, включая отменённые, от новых к старым. month (ГГГГ-ММ) ограничивает
// период и включает итоги по категориям.
func (s *Service) ListExpenses(ctx context.Context, q store.Querier, userID, month, cursor string, limit int) (ExpensePage, error) {
	page := ExpensePage{Page: Page[ExpenseView]{Items: []ExpenseView{}}}
	var from, to *time.Time
	if month != "" {
		m, err := time.Parse("2006-01", month)
		if err != nil || m.Format("2006-01") != month {
			return page, core.Validation("month", "ГГГГ-ММ")
		}
		from, to = &m, new(m.AddDate(0, 1, 0))
		page.Month = &month
		page.Totals = map[string]int64{}
		rows, err := q.Query(ctx, `SELECT category_id, sum(amount_minor) FROM expenses
			WHERE user_id = $1 AND voided_at IS NULL AND occurred_on >= $2 AND occurred_on < $3 GROUP BY category_id`, userID, from, to)
		if err != nil {
			return page, err
		}
		for rows.Next() {
			var c string
			var sum int64
			if err := rows.Scan(&c, &sum); err != nil {
				rows.Close()
				return page, err
			}
			page.Totals[c] = sum
		}
		if err := rows.Err(); err != nil {
			return page, err
		}
	}
	cur := expenseCursor{On: "9999-12-31", At: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), ID: "~"}
	if cursor != "" {
		if e := decodeCursor(cursor, &cur); e != nil {
			return page, e
		}
	}
	rows, err := q.Query(ctx, `SELECT `+expenseColumns+` FROM expenses
		WHERE user_id = $1 AND (occurred_on, created_at, id) < ($2::date, $3, $4)
		  AND ($5::date IS NULL OR occurred_on >= $5) AND ($6::date IS NULL OR occurred_on < $6)
		ORDER BY occurred_on DESC, created_at DESC, id DESC LIMIT $7`, userID, cur.On, cur.At, cur.ID, from, to, limit+1)
	if err != nil {
		return page, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ExpenseView, error) { return scanExpense(r) })
	if err != nil {
		return page, err
	}
	if len(items) > limit {
		last := items[limit-1]
		items = items[:limit]
		page.NextCursor = encodeCursor(expenseCursor{On: last.OccurredOn, At: last.CreatedAt, ID: last.ExpenseID})
	}
	page.Items = items
	return page, nil
}

// CheckinView — отметка накопления или пересмотра.
type CheckinView struct {
	CheckinID      string    `json:"checkinId"`
	GoalID         string    `json:"goalId"`
	Kind           string    `json:"kind"`
	AmountMinor    *int64    `json:"amountMinor"`
	ReviewedFields []string  `json:"reviewedFields"`
	Note           string    `json:"note"`
	CreatedAt      time.Time `json:"createdAt"`
}

// CheckinInput — новая отметка.
type CheckinInput struct {
	Kind           string
	AmountMinor    *int64
	ReviewedFields []string
	Note           string
}

var reviewable = []string{"amount", "date", "budget"}

func validateCheckin(kind string, amountMinor *int64, fields []string) *core.Error {
	switch kind {
	case "saved", "save":
		if amountMinor == nil {
			return core.Validation("amountMinor", "нужна сумма накопления")
		}
		if e := amount("amountMinor", *amountMinor, 1); e != nil {
			return e
		}
		if len(fields) > 0 {
			return core.Validation("reviewedFields", "только для пересмотра")
		}
	case "reviewed", "review":
		if amountMinor != nil {
			return core.Validation("amountMinor", "у пересмотра нет суммы")
		}
		if len(fields) == 0 {
			return core.Validation("reviewedFields", "выбери, что проверено: amount, date или budget")
		}
		for _, f := range fields {
			if !slices.Contains(reviewable, f) {
				return core.Validation("reviewedFields", "допустимо: amount, date, budget")
			}
		}
	default:
		return core.Validation("kind", "saved или reviewed")
	}
	return nil
}

// AddCheckin создаёт отметку по текущей цели. Отметка saved прибавляет сумму к накопленному
// в той же транзакции — это не банковский перевод (§19).
func (s *Service) AddCheckin(ctx context.Context, tx pgx.Tx, userID string, in CheckinInput, now time.Time) (CheckinView, error) {
	if e := validateCheckin(in.Kind, in.AmountMinor, in.ReviewedFields); e != nil {
		return CheckinView{}, e
	}
	note, e := cleanText("note", in.Note, 0, NoteMaxRunes)
	if e != nil {
		return CheckinView{}, e
	}
	g, err := activeGoal(ctx, tx, userID)
	if err != nil {
		return CheckinView{}, err
	}
	if g == nil {
		return CheckinView{}, core.Conflict("NO_ACTIVE_GOAL", "сначала задай цель в плане")
	}
	id, err := s.insertCheckin(ctx, tx, userID, g, in.Kind, in.AmountMinor, in.ReviewedFields, note, now)
	if err != nil {
		return CheckinView{}, err
	}
	return s.checkin(ctx, tx, userID, id)
}

func (s *Service) insertCheckin(ctx context.Context, tx pgx.Tx, userID string, g *GoalView, kind string, amountMinor *int64,
	fields []string, note string, now time.Time) (string, error) {
	if amountMinor != nil && g.SavedMinor+*amountMinor > AmountMax {
		return "", core.Validation("amountMinor", "накопленная сумма превысит 1 000 000 ₽")
	}
	if fields == nil {
		fields = []string{}
	}
	id := s.IDs.New("chk")
	if _, err := tx.Exec(ctx, `
		INSERT INTO checkins (id, user_id, goal_id, kind, amount_minor, reviewed_fields, note, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, id, userID, g.GoalID, kind, amountMinor, fields, note, now); err != nil {
		return "", err
	}
	if amountMinor != nil {
		if _, err := tx.Exec(ctx, `UPDATE goals SET saved_minor = saved_minor + $2, updated_at = $3 WHERE id = $1`, g.GoalID, *amountMinor, now); err != nil {
			return "", err
		}
		if err := bumpPlan(ctx, tx, userID, now); err != nil {
			return "", err
		}
	}
	return id, nil
}

const checkinColumns = `id, goal_id, kind, amount_minor, reviewed_fields, note, created_at`

func scanCheckin(row pgx.Row) (CheckinView, error) {
	var v CheckinView
	err := row.Scan(&v.CheckinID, &v.GoalID, &v.Kind, &v.AmountMinor, &v.ReviewedFields, &v.Note, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, core.NotFound()
	}
	v.CreatedAt = v.CreatedAt.UTC()
	return v, err
}

func (s *Service) checkin(ctx context.Context, q store.Querier, userID, id string) (CheckinView, error) {
	return scanCheckin(q.QueryRow(ctx, `SELECT `+checkinColumns+` FROM checkins WHERE user_id = $1 AND id = $2`, userID, id))
}

type timeCursor struct {
	At time.Time `json:"a"`
	ID string    `json:"i"`
}

// ListCheckins — отметки от новых к старым.
func (s *Service) ListCheckins(ctx context.Context, q store.Querier, userID, cursor string, limit int) (Page[CheckinView], error) {
	page := Page[CheckinView]{Items: []CheckinView{}}
	cur := timeCursor{At: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), ID: "~"}
	if cursor != "" {
		if e := decodeCursor(cursor, &cur); e != nil {
			return page, e
		}
	}
	rows, err := q.Query(ctx, `SELECT `+checkinColumns+` FROM checkins WHERE user_id = $1 AND (created_at, id) < ($2, $3)
		ORDER BY created_at DESC, id DESC LIMIT $4`, userID, cur.At, cur.ID, limit+1)
	if err != nil {
		return page, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (CheckinView, error) { return scanCheckin(r) })
	if err != nil {
		return page, err
	}
	if len(items) > limit {
		last := items[limit-1]
		items = items[:limit]
		page.NextCursor = encodeCursor(timeCursor{At: last.CreatedAt, ID: last.CheckinID})
	}
	page.Items = items
	return page, nil
}

// ActionView — запланированное действие.
type ActionView struct {
	ActionID    string     `json:"actionId"`
	GoalID      string     `json:"goalId"`
	Kind        string     `json:"kind"`
	AmountMinor *int64     `json:"amountMinor"`
	Note        string     `json:"note"`
	DueOn       string     `json:"dueOn"`
	Status      string     `json:"status"`
	CheckinID   *string    `json:"checkinId"`
	CreatedAt   time.Time  `json:"createdAt"`
	CompletedAt *time.Time `json:"completedAt"`
	CancelledAt *time.Time `json:"cancelledAt"`
}

// ActionInput — новое действие.
type ActionInput struct {
	Kind        string
	AmountMinor *int64
	Note        string
	DueOn       string
}

// AddAction планирует save или review по текущей цели на дату от сегодня до 30 дней (§19).
func (s *Service) AddAction(ctx context.Context, tx pgx.Tx, userID string, in ActionInput, now time.Time) (ActionView, error) {
	if in.Kind != "save" && in.Kind != "review" {
		return ActionView{}, core.Validation("kind", "save или review")
	}
	if in.Kind == "save" {
		if e := validateCheckin("save", in.AmountMinor, nil); e != nil {
			return ActionView{}, e
		}
	} else if in.AmountMinor != nil {
		return ActionView{}, core.Validation("amountMinor", "у пересмотра нет суммы")
	}
	note, e := cleanText("note", in.Note, 0, NoteMaxRunes)
	if e != nil {
		return ActionView{}, e
	}
	due, e := parseDate("dueOn", in.DueOn)
	if e != nil {
		return ActionView{}, e
	}
	today, err := localToday(ctx, tx, userID, now)
	if err != nil {
		return ActionView{}, err
	}
	if due.Before(today) || due.After(today.AddDate(0, 0, ActionHorizon)) {
		return ActionView{}, core.Validation("dueOn", "от сегодня до 30 дней вперёд")
	}
	g, err := activeGoal(ctx, tx, userID)
	if err != nil {
		return ActionView{}, err
	}
	if g == nil {
		return ActionView{}, core.Conflict("NO_ACTIVE_GOAL", "сначала задай цель в плане")
	}
	var pending int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM planned_actions WHERE user_id = $1 AND status = 'pending'`, userID).Scan(&pending); err != nil {
		return ActionView{}, err
	}
	if pending >= PendingMax {
		return ActionView{}, core.Conflict("ACTION_LIMIT", "запланировано уже 20 действий; выполни или отмени одно из них")
	}
	id := s.IDs.New("act")
	if _, err := tx.Exec(ctx, `
		INSERT INTO planned_actions (id, user_id, goal_id, kind, amount_minor, note, due_on, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', $8)`, id, userID, g.GoalID, in.Kind, in.AmountMinor, note, due, now); err != nil {
		return ActionView{}, err
	}
	return s.action(ctx, tx, userID, id, false)
}

const actionColumns = `id, goal_id, kind, amount_minor, note, due_on, status, checkin_id, created_at, completed_at, cancelled_at`

func scanAction(row pgx.Row) (ActionView, error) {
	var v ActionView
	var due time.Time
	err := row.Scan(&v.ActionID, &v.GoalID, &v.Kind, &v.AmountMinor, &v.Note, &due, &v.Status, &v.CheckinID,
		&v.CreatedAt, &v.CompletedAt, &v.CancelledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, core.NotFound()
	}
	v.DueOn = due.Format(time.DateOnly)
	v.CreatedAt = v.CreatedAt.UTC()
	for _, t := range []**time.Time{&v.CompletedAt, &v.CancelledAt} {
		if *t != nil {
			*t = new((*t).UTC())
		}
	}
	return v, err
}

func (s *Service) action(ctx context.Context, q store.Querier, userID, id string, lock bool) (ActionView, error) {
	sql := `SELECT ` + actionColumns + ` FROM planned_actions WHERE user_id = $1 AND id = $2`
	if lock {
		sql += ` FOR UPDATE`
	}
	return scanAction(q.QueryRow(ctx, sql, userID, id))
}

// CompleteAction выполняет действие: save создаёт ровно одну отметку и увеличивает
// накопленное, review — только отметку пересмотра. Если текущая цель уже другая —
// GOAL_CHANGED, новую цель незаметно не пополняем (§19).
func (s *Service) CompleteAction(ctx context.Context, tx pgx.Tx, userID, id string, now time.Time) (ActionView, error) {
	a, err := s.action(ctx, tx, userID, id, true)
	if err != nil {
		return a, err
	}
	switch a.Status {
	case "completed":
		return a, nil
	case "cancelled":
		return a, core.Conflict("ACTION_CANCELLED", "действие отменено; запланируй новое")
	}
	g, err := activeGoal(ctx, tx, userID)
	if err != nil {
		return a, err
	}
	if g == nil || g.GoalID != a.GoalID {
		return a, core.Conflict("GOAL_CHANGED", "цель изменилась; перенеси действие на текущую цель")
	}
	kind, fields := "saved", []string(nil)
	if a.Kind == "review" {
		kind, fields = "reviewed", []string{"amount", "date", "budget"}
	}
	checkinID, err := s.insertCheckin(ctx, tx, userID, g, kind, a.AmountMinor, fields, a.Note, now)
	if err != nil {
		return a, err
	}
	if _, err := tx.Exec(ctx, `UPDATE planned_actions SET status = 'completed', checkin_id = $2, completed_at = $3 WHERE id = $1`,
		id, checkinID, now); err != nil {
		return a, err
	}
	return s.action(ctx, tx, userID, id, false)
}

// CancelAction отменяет только pending; выполненное остаётся в истории.
func (s *Service) CancelAction(ctx context.Context, tx pgx.Tx, userID, id string, now time.Time) (ActionView, error) {
	a, err := s.action(ctx, tx, userID, id, true)
	if err != nil {
		return a, err
	}
	switch a.Status {
	case "cancelled":
		return a, nil
	case "completed":
		return a, core.Conflict("ACTION_COMPLETED", "выполненное действие уже в истории; накопленную сумму можно поправить в цели")
	}
	if _, err := tx.Exec(ctx, `UPDATE planned_actions SET status = 'cancelled', cancelled_at = $2 WHERE id = $1`, id, now); err != nil {
		return a, err
	}
	return s.action(ctx, tx, userID, id, false)
}

type actionCursor struct {
	Rank int    `json:"r"`
	Due  string `json:"d"`
	ID   string `json:"i"`
}

// ListActions — сначала pending по сроку, затем история.
func (s *Service) ListActions(ctx context.Context, q store.Querier, userID, cursor string, limit int) (Page[ActionView], error) {
	page := Page[ActionView]{Items: []ActionView{}}
	cur := actionCursor{Rank: -1, Due: "0001-01-01", ID: ""}
	if cursor != "" {
		if e := decodeCursor(cursor, &cur); e != nil {
			return page, e
		}
	}
	rows, err := q.Query(ctx, `SELECT `+actionColumns+` FROM planned_actions
		WHERE user_id = $1 AND ((status <> 'pending')::int, due_on, id) > ($2, $3::date, $4)
		ORDER BY (status <> 'pending')::int, due_on, id LIMIT $5`, userID, cur.Rank, cur.Due, cur.ID, limit+1)
	if err != nil {
		return page, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ActionView, error) { return scanAction(r) })
	if err != nil {
		return page, err
	}
	if len(items) > limit {
		last := items[limit-1]
		items = items[:limit]
		rank := 0
		if last.Status != "pending" {
			rank = 1
		}
		page.NextCursor = encodeCursor(actionCursor{Rank: rank, Due: last.DueOn, ID: last.ActionID})
	}
	page.Items = items
	return page, nil
}
