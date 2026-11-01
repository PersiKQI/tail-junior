package learning

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"teyljunior/internal/core"
)

// repeatInterval — не чаще одного зачётного назначения семейства за 24 часа (§7, §15).
const repeatInterval = 24 * time.Hour

// ScheduleRepeat возвращает открытый назначенный повтор или создаёт новый. Планировщик
// использует только раскрытые оценки: сначала тема с известной ошибкой без последующего
// верного ответа, затем дольше всего не повторявшаяся, при равенстве — стабильный порядок
// семейства. Назначенный повтор закрывает день, но не даёт XP и нового свидетельства.
// Пустая строка — подходящей темы сейчас нет.
func (s *Service) ScheduleRepeat(ctx context.Context, tx pgx.Tx, userID string, now time.Time) (string, error) {
	var open string
	err := tx.QueryRow(ctx, `SELECT id FROM assignments WHERE user_id = $1 AND role = 'practice'
		AND practice_origin = 'scheduled' AND status = 'open' LIMIT 1`, userID).Scan(&open)
	if err == nil {
		return open, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	var family string
	err = tx.QueryRow(ctx, `
		WITH answers AS (
			SELECT t.family_id, sub.accepted_at, e.passed
			FROM attempts t
			JOIN assignments a ON a.id = t.assignment_id
			JOIN submissions sub ON sub.attempt_id = t.id
			JOIN evaluations e ON e.attempt_id = t.id AND e.replaces_evaluation_id IS NULL
			WHERE t.user_id = $1 AND (a.role = 'learning' OR (a.role = 'practice' AND a.streak_eligible))
		)
		SELECT c.family_id FROM learning_completions c
		JOIN evaluations first ON first.attempt_id = c.first_attempt_id AND first.replaces_evaluation_id IS NULL
		WHERE c.user_id = $1
		  AND NOT EXISTS (SELECT 1 FROM assignments p WHERE p.user_id = c.user_id AND p.family_id = c.family_id
		                  AND p.role = 'practice' AND p.status = 'open')
		  AND (SELECT max(accepted_at) FROM answers x WHERE x.family_id = c.family_id) <= $2
		ORDER BY (NOT first.passed AND NOT EXISTS (
		              SELECT 1 FROM answers x WHERE x.family_id = c.family_id AND x.passed)) DESC,
		         (SELECT max(accepted_at) FROM answers x WHERE x.family_id = c.family_id),
		         c.family_id
		LIMIT 1`, userID, now.Add(-repeatInterval)).Scan(&family)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var version string
	if err := tx.QueryRow(ctx, `SELECT program_version FROM enrollments WHERE user_id = $1`, userID).Scan(&version); err != nil {
		return "", err
	}
	k, err := s.Catalog.ItemForFamily(ctx, tx, version, family)
	if err != nil {
		return "", err
	}
	id := s.IDs.New(core.PrefixAssignment)
	_, err = tx.Exec(ctx, `
		INSERT INTO assignments (id, user_id, assignment_key, family_id, item_id, program_version, role, practice_origin,
		                         due_at, streak_eligible, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'practice', 'scheduled', $7, true, 'open', $7)`,
		id, userID, "practice:"+id, family, k.ItemID, version, now)
	return id, err
}
