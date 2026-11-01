package learning

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"teyljunior/internal/assessment"
	"teyljunior/internal/content"
	"teyljunior/internal/core"
	"teyljunior/internal/store"
)

// WithdrawalReport — что изменил отзыв редакции.
type WithdrawalReport struct {
	Users               int
	AttemptsWithdrawn   int
	AssignmentsReplaced int
	AssignmentsClosed   int
	SetsInvalidated     int
}

// WithdrawRevision отзывает редакцию при существенной ошибке (§6). Незавершённые попытки
// получают withdrawn без оценки и без штрафа. Открытые learning/practice назначения
// закрываются; если в опубликованной поставке есть действующая редакция того же семейства,
// создаётся замена со ссылкой на прежнее назначение. Набор, где есть отозванная карточка,
// целиком становится invalidated, его записи сохраняются. Принятые ответы не переписываются.
func (s *Service) WithdrawRevision(ctx context.Context, db *store.DB, k content.RevisionKey, reason string, now time.Time) (WithdrawalReport, error) {
	if reason == "" {
		return WithdrawalReport{}, errors.New("нужна причина отзыва")
	}
	var rep WithdrawalReport
	err := db.InTx(ctx, func(tx pgx.Tx) error {
		rep = WithdrawalReport{}
		tag, err := tx.Exec(ctx, `
			UPDATE content_revisions SET status = 'withdrawn', withdrawn_at = $5, withdrawal_reason = $6
			WHERE item_id = $1 AND revision = $2 AND grading_revision = $3 AND locale = $4 AND status = 'active'`,
			k.ItemID, k.Revision, k.GradingRevision, k.Locale, now, reason)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("редакция %s r%d g%d %s не найдена или уже отозвана", k.ItemID, k.Revision, k.GradingRevision, k.Locale)
		}

		// Пользователи блокируются в одном порядке, как и в запросах API: сначала строка
		// пользователя (§3).
		rows, err := tx.Query(ctx, `
			SELECT id FROM users WHERE id IN (
				SELECT user_id FROM attempts
				WHERE item_id = $1 AND revision = $2 AND grading_revision = $3 AND locale = $4 AND status = 'started'
				UNION
				SELECT a.user_id FROM assignments a
				JOIN release_items ri ON ri.program_version = a.program_version AND ri.family_id = a.family_id
				WHERE ri.item_id = $1 AND ri.revision = $2 AND ri.grading_revision = $3 AND ri.locale = $4
				  AND (a.status = 'open' OR a.assessment_set_id IS NOT NULL))
			ORDER BY id FOR UPDATE`, k.ItemID, k.Revision, k.GradingRevision, k.Locale)
		if err != nil {
			return err
		}
		users, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		rep.Users = len(users)
		for _, userID := range users {
			if err := s.withdrawForUser(ctx, tx, userID, k, reason, now, &rep); err != nil {
				return fmt.Errorf("user %s: %w", userID, err)
			}
		}
		return nil
	})
	return rep, err
}

func (s *Service) withdrawForUser(ctx context.Context, tx pgx.Tx, userID string, k content.RevisionKey, reason string, now time.Time, rep *WithdrawalReport) error {
	// Наборы с отозванной карточкой — целиком invalidated.
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT a.assessment_set_id FROM assignments a
		JOIN release_items ri ON ri.program_version = a.program_version AND ri.family_id = a.family_id
		JOIN assessment_sets st ON st.id = a.assessment_set_id
		WHERE a.user_id = $1 AND st.status <> 'invalidated'
		  AND ri.item_id = $2 AND ri.revision = $3 AND ri.grading_revision = $4 AND ri.locale = $5`,
		userID, k.ItemID, k.Revision, k.GradingRevision, k.Locale)
	if err != nil {
		return err
	}
	sets, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, setID := range sets {
		if err := assessment.Invalidate(ctx, tx, setID, reason, now); err != nil {
			return err
		}
		rep.SetsInvalidated++
	}

	tag, err := tx.Exec(ctx, `
		UPDATE attempts SET status = 'withdrawn', closed_at = $6
		WHERE user_id = $1 AND status = 'started'
		  AND item_id = $2 AND revision = $3 AND grading_revision = $4 AND locale = $5`,
		userID, k.ItemID, k.Revision, k.GradingRevision, k.Locale, now)
	if err != nil {
		return err
	}
	rep.AttemptsWithdrawn += int(tag.RowsAffected())

	type open struct{ id, role, family string }
	rows, err = tx.Query(ctx, `
		SELECT a.id, a.role, a.family_id FROM assignments a
		JOIN release_items ri ON ri.program_version = a.program_version AND ri.family_id = a.family_id
		WHERE a.user_id = $1 AND a.status = 'open' AND a.role IN ('learning', 'practice')
		  AND ri.item_id = $2 AND ri.revision = $3 AND ri.grading_revision = $4 AND ri.locale = $5
		FOR UPDATE OF a`, userID, k.ItemID, k.Revision, k.GradingRevision, k.Locale)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (open, error) {
		var o open
		return o, r.Scan(&o.id, &o.role, &o.family)
	})
	if err != nil {
		return err
	}
	for _, o := range list {
		if _, err := tx.Exec(ctx, `UPDATE assignments SET status = 'withdrawn' WHERE id = $1`, o.id); err != nil {
			return err
		}
		version, err := s.replacementVersion(ctx, tx, o.family)
		if err != nil {
			return err
		}
		if version == "" {
			rep.AssignmentsClosed++
			continue
		}
		k2, err := s.Catalog.ItemForFamily(ctx, tx, version, o.family)
		if err != nil {
			return err
		}
		var origin *string
		if o.role == RolePractice {
			origin = new("manual")
		}
		id := s.IDs.New(core.PrefixAssignment)
		if _, err := tx.Exec(ctx, `
			INSERT INTO assignments (id, user_id, assignment_key, family_id, item_id, program_version, role, practice_origin,
			                         status, created_at, replaces_assignment_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'open', $9, $10)`,
			id, userID, "replacement:"+o.id, o.family, k2.ItemID, version, o.role, origin, now, o.id); err != nil {
			return err
		}
		rep.AssignmentsReplaced++
	}
	return nil
}

// replacementVersion — последняя опубликованная поставка, где у семейства действующая
// редакция. Пусто — замены пока нет.
func (s *Service) replacementVersion(ctx context.Context, q store.Querier, familyID string) (string, error) {
	var v string
	err := q.QueryRow(ctx, `
		SELECT p.version FROM program_releases p
		JOIN release_items ri ON ri.program_version = p.version
		JOIN content_revisions r USING (item_id, revision, grading_revision, locale)
		WHERE p.status = 'published' AND ri.family_id = $1 AND r.status = 'active'
		ORDER BY p.published_at DESC LIMIT 1`, familyID).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return v, err
}
