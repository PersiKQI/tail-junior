// Пакет assessment — диагностические и отложенные наборы (спецификация §9): создание, сроки
// по якорям, допуск к попыткам, финализация и проекции. До finalizedAt правильность,
// объяснения и производные не попадают ни в один ответ API.
package assessment

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"teyljunior/internal/content"
	"teyljunior/internal/core"
	"teyljunior/internal/progression"
	"teyljunior/internal/store"
)

// Виды наборов.
const (
	KindDiagnostic = "diagnostic"
	KindReview     = "review"
)

// Статусы набора (§9). available для review не хранится, а вычисляется по due_at.
const (
	StatusWaiting     = "waiting"
	StatusAvailable   = "available"
	StatusInProgress  = "in_progress"
	StatusCompleted   = "completed"
	StatusPartial     = "partial"
	StatusSkipped     = "skipped"
	StatusInvalidated = "invalidated"
)

const diagnosticLabel = "Входная проверка"

// Service — сценарии наборов. Методы, меняющие состояние, вызываются внутри транзакции после
// захвата строки пользователя; строка набора блокируется следующей (§3).
type Service struct {
	Catalog *content.Catalog
	IDs     core.IDs
	Prog    *progression.Service
}

// anchorSnapshot — якоря и задержка review-карточки на момент создания набора: новая поставка
// не сдвигает уже назначенный срок (§9).
type anchorSnapshot struct {
	AnchorFamilyIDs []string `json:"anchorFamilyIds"`
	DelayDays       int      `json:"delayDays"`
}

// CreateReviewSets создаёт пары RS02/RS07/RS30 при зачислении. Сроки появятся по мере
// первого принятия якорей.
func (s *Service) CreateReviewSets(ctx context.Context, tx pgx.Tx, userID, version string, now time.Time) error {
	m, err := s.Catalog.Manifest(ctx, tx, version)
	if err != nil {
		return err
	}
	for _, rs := range m.ReviewSets {
		setID := s.IDs.New(core.PrefixSet)
		if _, err := tx.Exec(ctx, `
			INSERT INTO assessment_sets (id, user_id, kind, slot_family_id, program_lineage, program_version, delay_days, status, created_at)
			VALUES ($1, $2, 'review', $3, $4, $5, $6, 'waiting', $7)`,
			setID, userID, rs.SlotFamilyID, m.LineageID, version, rs.DelayDays, now); err != nil {
			return err
		}
		for _, itemID := range rs.ItemIDs {
			snap := anchorSnapshot{AnchorFamilyIDs: m.AnchorFamilies[itemID], DelayDays: rs.DelayDays}
			if err := s.insertItem(ctx, tx, userID, setID, version, itemID, KindReview, &snap, now); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) insertItem(ctx context.Context, tx pgx.Tx, userID, setID, version, itemID, role string, snap *anchorSnapshot, now time.Time) error {
	var family string
	if err := tx.QueryRow(ctx, `SELECT family_id FROM release_items WHERE program_version = $1 AND item_id = $2`, version, itemID).Scan(&family); err != nil {
		return err
	}
	var snapJSON []byte
	if snap != nil {
		var err error
		if snapJSON, err = json.Marshal(snap); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO assignments (id, user_id, assignment_key, family_id, item_id, program_version, role, status,
		                         created_at, assessment_set_id, anchor_snapshot)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'open', $8, $9, $10)`,
		s.IDs.New(core.PrefixAssignment), userID, "set:"+setID+":"+itemID, family, itemID, version, role, now, setID, snapJSON)
	return err
}

// CreateDiagnostic начинает добровольную диагностику. Первичный набор один: повторный запрос
// с другим ключом возвращает существующий, даже после partial или skipped (§9).
func (s *Service) CreateDiagnostic(ctx context.Context, tx pgx.Tx, userID string, now time.Time) (SetView, bool, error) {
	var version string
	if err := tx.QueryRow(ctx, `SELECT program_version FROM enrollments WHERE user_id = $1`, userID).Scan(&version); err != nil {
		return SetView{}, false, err
	}
	m, err := s.Catalog.Manifest(ctx, tx, version)
	if err != nil {
		return SetView{}, false, err
	}
	var existing string
	err = tx.QueryRow(ctx, `
		SELECT id FROM assessment_sets WHERE user_id = $1 AND program_lineage = $2 AND slot_family_id = $3
		ORDER BY generation DESC LIMIT 1`, userID, m.LineageID, m.Diagnostic.SlotFamilyID).Scan(&existing)
	if err == nil {
		v, err := s.Get(ctx, tx, userID, existing, now)
		return v, false, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return SetView{}, false, err
	}
	setID := s.IDs.New(core.PrefixSet)
	if _, err := tx.Exec(ctx, `
		INSERT INTO assessment_sets (id, user_id, kind, slot_family_id, program_lineage, program_version, status, created_at)
		VALUES ($1, $2, 'diagnostic', $3, $4, $5, 'available', $6)`,
		setID, userID, m.Diagnostic.SlotFamilyID, m.LineageID, version, now); err != nil {
		return SetView{}, false, err
	}
	for _, itemID := range m.Diagnostic.ItemIDs {
		if err := s.insertItem(ctx, tx, userID, setID, version, itemID, KindDiagnostic, nil, now); err != nil {
			return SetView{}, false, err
		}
	}
	v, err := s.Get(ctx, tx, userID, setID, now)
	return v, true, err
}

// RefreshDue пересчитывает сроки review-карточек после первого принятия learning семейства.
// dueAt карточки = позднейшее первое принятие всех якорей + delayDays × 24 ч; без любого якоря
// срок остаётся null. Набор открывается по максимуму сроков своих карточек (§9).
func (s *Service) RefreshDue(ctx context.Context, tx pgx.Tx, userID string) error {
	type pending struct {
		id, setID string
		snap      anchorSnapshot
	}
	rows, err := tx.Query(ctx, `
		SELECT a.id, a.assessment_set_id, a.anchor_snapshot FROM assignments a
		JOIN assessment_sets s ON s.user_id = a.user_id AND s.id = a.assessment_set_id
		WHERE a.user_id = $1 AND a.role = 'review' AND a.due_at IS NULL
		  AND s.finalized_at IS NULL AND s.status <> 'invalidated'`, userID)
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (pending, error) {
		var p pending
		var raw []byte
		if err := r.Scan(&p.id, &p.setID, &raw); err != nil {
			return p, err
		}
		return p, json.Unmarshal(raw, &p.snap)
	})
	if err != nil {
		return err
	}
	touched := map[string]bool{}
	for _, p := range items {
		var n int
		var latest *time.Time
		if err := tx.QueryRow(ctx, `
			SELECT count(*), max(accepted_at) FROM learning_completions
			WHERE user_id = $1 AND family_id = ANY($2)`, userID, p.snap.AnchorFamilyIDs).Scan(&n, &latest); err != nil {
			return err
		}
		if n < len(p.snap.AnchorFamilyIDs) || latest == nil {
			continue
		}
		due := latest.Add(time.Duration(p.snap.DelayDays) * 24 * time.Hour)
		if _, err := tx.Exec(ctx, `UPDATE assignments SET anchor_accepted_at = $2, due_at = $3 WHERE id = $1`, p.id, *latest, due); err != nil {
			return err
		}
		touched[p.setID] = true
	}
	for setID := range touched {
		if _, err := tx.Exec(ctx, `
			UPDATE assessment_sets s SET due_at = (SELECT max(due_at) FROM assignments WHERE assessment_set_id = s.id)
			WHERE s.id = $1 AND NOT EXISTS (SELECT 1 FROM assignments WHERE assessment_set_id = s.id AND due_at IS NULL)`, setID); err != nil {
			return err
		}
	}
	return nil
}

// setRow — строка набора.
type setRow struct {
	ID, Kind, SlotFamilyID, Status, Version string
	DueAt, FinalizedAt                      *time.Time
	FinalizedStatus                         *string
	BaselineEligible                        *bool
	CreatedAt                               time.Time
	InvalidationReason                      *string
}

// effective — статус для клиента: waiting review после срока показывается available.
func (r setRow) effective(now time.Time) string {
	if r.Status == StatusWaiting && r.DueAt != nil && !now.Before(*r.DueAt) {
		return StatusAvailable
	}
	return r.Status
}

func (r setRow) finalized() bool { return r.FinalizedAt != nil }

const setColumns = `id, kind, slot_family_id, status, program_version, due_at, finalized_at, finalized_status,
	baseline_eligible, created_at, invalidation_reason`

func scanSet(row pgx.Row) (setRow, error) {
	var r setRow
	err := row.Scan(&r.ID, &r.Kind, &r.SlotFamilyID, &r.Status, &r.Version, &r.DueAt, &r.FinalizedAt,
		&r.FinalizedStatus, &r.BaselineEligible, &r.CreatedAt, &r.InvalidationReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, core.NotFound()
	}
	return r, err
}

func lockSet(ctx context.Context, tx pgx.Tx, userID, setID string) (setRow, error) {
	return scanSet(tx.QueryRow(ctx, `SELECT `+setColumns+` FROM assessment_sets WHERE user_id = $1 AND id = $2 FOR UPDATE`, userID, setID))
}

func errFinalized() *core.Error {
	return core.Conflict("SET_FINALIZED", "проверка уже завершена; ответы дальше относятся к практике")
}

func errInvalidated() *core.Error {
	return core.Conflict("SET_INVALIDATED", "в проверке нашли неточность; записи сохранены, замену назначит редакция")
}

// BeginAttempt допускает начало попытки внутри набора: только когда набор открыт и не
// финализирован. Первая выдача переводит набор в in_progress.
func (s *Service) BeginAttempt(ctx context.Context, tx pgx.Tx, userID, setID string, now time.Time) error {
	r, err := lockSet(ctx, tx, userID, setID)
	if err != nil {
		return err
	}
	switch r.effective(now) {
	case StatusInvalidated:
		return errInvalidated()
	case StatusCompleted, StatusPartial, StatusSkipped:
		return errFinalized()
	case StatusWaiting:
		return core.Conflict("SET_NOT_AVAILABLE", "проверка ещё не открылась; срок есть в списке проверок")
	case StatusAvailable:
		_, err = tx.Exec(ctx, `UPDATE assessment_sets SET status = 'in_progress' WHERE id = $1`, setID)
	}
	return err
}

// CheckOpen блокирует набор перед приёмом ответа. Если первой стала финализация, поздний ответ
// не добавляется в первичный набор (§21).
func (s *Service) CheckOpen(ctx context.Context, tx pgx.Tx, userID, setID string) error {
	r, err := lockSet(ctx, tx, userID, setID)
	if err != nil {
		return err
	}
	switch {
	case r.Status == StatusInvalidated:
		return errInvalidated()
	case r.finalized():
		return errFinalized()
	}
	return nil
}

// Answered вызывается после принятия ответа: последний необходимый ответ финализирует набор
// как completed.
func (s *Service) Answered(ctx context.Context, tx pgx.Tx, userID, setID string, now time.Time) error {
	r, err := lockSet(ctx, tx, userID, setID)
	if err != nil {
		return err
	}
	var open int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM assignments WHERE assessment_set_id = $1 AND status = 'open'`, setID).Scan(&open); err != nil {
		return err
	}
	if open > 0 {
		return nil
	}
	return s.finalize(ctx, tx, userID, r, StatusCompleted, now)
}

// Finalize — явное завершение: с частью ответов partial, без ответов skipped. Первая
// финализация неизменна, повтор возвращает её результат (§9).
func (s *Service) Finalize(ctx context.Context, tx pgx.Tx, userID, setID string, now time.Time) (SetView, error) {
	r, err := lockSet(ctx, tx, userID, setID)
	if err != nil {
		return SetView{}, err
	}
	if r.Status == StatusInvalidated {
		return SetView{}, errInvalidated()
	}
	if !r.finalized() {
		if r.effective(now) == StatusWaiting {
			return SetView{}, core.Conflict("SET_NOT_AVAILABLE", "проверка ещё не открылась")
		}
		var answered int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM assignments WHERE assessment_set_id = $1 AND status = 'completed'`, setID).Scan(&answered); err != nil {
			return SetView{}, err
		}
		outcome := StatusSkipped
		if answered > 0 {
			outcome = StatusPartial
		}
		if err := s.finalize(ctx, tx, userID, r, outcome, now); err != nil {
			return SetView{}, err
		}
	}
	return s.Get(ctx, tx, userID, setID, now)
}

func (s *Service) finalize(ctx context.Context, tx pgx.Tx, userID string, r setRow, outcome string, now time.Time) error {
	if _, err := tx.Exec(ctx, `
		UPDATE attempts SET status = 'abandoned', closed_at = $3
		WHERE user_id = $1 AND status = 'started'
		  AND assignment_id IN (SELECT id FROM assignments WHERE assessment_set_id = $2)`, userID, r.ID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE assignments SET status = 'skipped' WHERE assessment_set_id = $1 AND status = 'open'`, r.ID); err != nil {
		return err
	}
	// Разбор раскрывается в момент финализации: это и есть экспозиция объяснения.
	rows, err := tx.Query(ctx, `
		SELECT t.id, t.family_id, t.item_id FROM attempts t JOIN assignments a ON a.id = t.assignment_id
		WHERE a.assessment_set_id = $1 AND t.status = 'submitted'`, r.ID)
	if err != nil {
		return err
	}
	type answered struct{ attempt, family, item string }
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (answered, error) {
		var a answered
		return a, row.Scan(&a.attempt, &a.family, &a.item)
	})
	if err != nil {
		return err
	}
	for _, a := range list {
		if _, err := tx.Exec(ctx, `
			INSERT INTO content_exposures (id, user_id, family_id, item_id, attempt_id, kind, provided_at)
			VALUES ($1, $2, $3, $4, $5, 'explanation', $6)`,
			s.IDs.New(core.PrefixExposure), userID, a.family, a.item, a.attempt, now); err != nil {
			return err
		}
	}
	// Чистый входной замер — диагностика, завершённая до первого учебного разбора (§9).
	var baseline *bool
	if r.Kind == KindDiagnostic {
		var learned bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM content_exposures e JOIN content_families f ON f.id = e.family_id
			               WHERE e.user_id = $1 AND e.kind = 'explanation' AND f.purpose = 'learning')`, userID).Scan(&learned); err != nil {
			return err
		}
		baseline = new(!learned)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE assessment_sets SET status = $2, finalized_status = $2, finalized_at = $3, baseline_eligible = $4
		WHERE id = $1`, r.ID, outcome, now, baseline); err != nil {
		return err
	}
	// XP за review, свидетельства и зависящие от оценки бейджи — только с финализацией.
	return s.Prog.SetFinalized(ctx, tx, userID, r.ID, now)
}

// Revealed — можно ли показывать оценку ответов набора.
func Revealed(ctx context.Context, q store.Querier, userID, setID string) (bool, error) {
	var finalized bool
	err := q.QueryRow(ctx, `SELECT finalized_at IS NOT NULL FROM assessment_sets WHERE user_id = $1 AND id = $2`, userID, setID).Scan(&finalized)
	return finalized, err
}

// Invalidate помечает набор invalidated при отзыве карточки (§6). Записи и первая финализация
// сохраняются; незавершённые попытки отзываются, неотвеченные назначения закрываются.
func Invalidate(ctx context.Context, tx pgx.Tx, setID, reason string, now time.Time) error {
	if _, err := tx.Exec(ctx, `
		UPDATE attempts SET status = 'withdrawn', closed_at = $2
		WHERE status = 'started' AND assignment_id IN (SELECT id FROM assignments WHERE assessment_set_id = $1)`, setID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE assignments SET status = 'withdrawn' WHERE assessment_set_id = $1 AND status = 'open'`, setID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		UPDATE assessment_sets SET status = 'invalidated', invalidated_at = $2, invalidation_reason = $3
		WHERE id = $1 AND status <> 'invalidated'`, setID, now, reason)
	return err
}
