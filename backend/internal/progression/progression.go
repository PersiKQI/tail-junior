package progression

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"teyljunior/internal/content"
	"teyljunior/internal/core"
)

// Идентификаторы бейджей участия.
const (
	BadgeFirstReview     = "first_review"
	BadgeOwnPace         = "own_pace"
	BadgeThreeScenarios  = "three_scenarios"
	BadgeLaterConfirmed  = "later_confirmed"
	factSchemaVersion    = 1
	confirmParticipation = "participation"
	confirmPractice      = "practice"
	confirmEvidence      = "evidence"
)

// Service — начисления по фактам. Все методы вызываются в транзакции после захвата строки
// пользователя.
type Service struct {
	Catalog *content.Catalog
	IDs     core.IDs
	Rules   *Rules
}

// fact — состоявшийся доменный факт. Payload содержит только идентификаторы: без сумм
// личного бюджета, заметок и токенов (§21, §22).
type fact struct {
	userID, name, sourceType, sourceID, dedupe string
	payload                                    map[string]any
	at                                         time.Time
}

// record сохраняет факт один раз по dedupe; повтор возвращает существующий.
func (s *Service) record(ctx context.Context, tx pgx.Tx, f fact) (string, bool, error) {
	payload, err := json.Marshal(f.payload)
	if err != nil {
		return "", false, err
	}
	if f.payload == nil {
		payload = []byte("{}")
	}
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO domain_facts (id, user_id, name, schema_version, source_type, source_id, dedupe_key, safe_payload, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (user_id, dedupe_key) DO NOTHING RETURNING id`,
		s.IDs.New("fct"), f.userID, f.name, factSchemaVersion, f.sourceType, f.sourceID, f.dedupe, payload, f.at).Scan(&id)
	if err == nil {
		// Transactional outbox: экспорт в аналитику фиксируется вместе с фактом и только при
		// включённой настройке; операционный факт записан в любом случае (§21, §22).
		_, err = tx.Exec(ctx, `
			INSERT INTO outbox (id, user_id, source_kind, source_id, destination, state, available_at, created_at)
			SELECT $1, id, 'fact', $2, 'analytics', 'pending', $3, $3 FROM users WHERE id = $4 AND analytics_enabled`,
			s.IDs.New("obx"), id, f.at, f.userID)
		return id, true, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, err
	}
	err = tx.QueryRow(ctx, `SELECT id FROM domain_facts WHERE user_id = $1 AND dedupe_key = $2`, f.userID, f.dedupe).Scan(&id)
	return id, false, err
}

// grantXP начисляет XP по стабильному основанию. Revision, ruleVersion и попытка в ключ не
// входят: тот же материал не даёт повторной базовой награды (§13).
func (s *Service) grantXP(ctx context.Context, tx pgx.Tx, userID, basis string, amount int, sourceFactID string, now time.Time) error {
	tag, err := tx.Exec(ctx, `
		INSERT INTO xp_ledger (id, user_id, basis_key, amount, source_fact_id, rule_version, granted_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (user_id, basis_key) DO NOTHING`,
		s.IDs.New("xp"), userID, basis, amount, sourceFactID, s.Rules.RuleVersion, now)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	_, _, err = s.record(ctx, tx, fact{userID: userID, name: "xp_granted", sourceType: "xp_basis", sourceID: basis,
		dedupe: "xp:" + basis, payload: map[string]any{"basisKey": basis, "xp": amount}, at: now})
	return err
}

// grantBadge выдаёт бейдж один раз: UNIQUE(user_id, badge_id).
func (s *Service) grantBadge(ctx context.Context, tx pgx.Tx, userID, badgeID, confirmation string, refs map[string]any, now time.Time) error {
	raw, err := json.Marshal(refs)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO badge_grants (user_id, badge_id, rule_version, granted_at, confirmation, source_refs)
		VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (user_id, badge_id) DO NOTHING`,
		userID, badgeID, s.Rules.RuleVersion, now, confirmation, raw)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	if _, _, err = s.record(ctx, tx, fact{userID: userID, name: "badge_granted", sourceType: "badge", sourceID: badgeID,
		dedupe: "badge:" + badgeID, payload: map[string]any{"badgeId": badgeID}, at: now}); err != nil {
		return err
	}
	return s.unlockBy(ctx, tx, userID, "badge:"+badgeID, now)
}

// LearningAnswer — принятый ответ learning/practice.
type LearningAnswer struct {
	UserID         string
	AttemptID      string
	FamilyID       string
	ItemID         string
	ProgramVersion string
	Role           string
	FirstForFamily bool
	Independent    bool
	Passed         bool
	StreakEligible bool
}

// LearningAccepted обрабатывает принятый ответ. Первое принятие семейства даёт XP, день и
// бейджи участия; самостоятельный верный первый ответ из mapping — практическую отметку
// исхода (§14). Ручная практика день не закрывает и XP не даёт; назначенная закрывает день (§15).
func (s *Service) LearningAccepted(ctx context.Context, tx pgx.Tx, a LearningAnswer, now time.Time) error {
	factID, _, err := s.record(ctx, tx, fact{userID: a.UserID, name: "answer_accepted", sourceType: "attempt", sourceID: a.AttemptID,
		dedupe: "answer:" + a.AttemptID, payload: map[string]any{"role": a.Role, "familyId": a.FamilyID, "programVersion": a.ProgramVersion}, at: now})
	if err != nil {
		return err
	}
	if a.Role == "practice" {
		if a.StreakEligible {
			return s.creditDay(ctx, tx, a.UserID, factID, now)
		}
		return nil
	}
	if !a.FirstForFamily {
		return nil
	}
	if err := s.grantXP(ctx, tx, a.UserID, "learning:"+a.FamilyID, s.Rules.XP.Learning, factID, now); err != nil {
		return err
	}
	if err := s.creditDay(ctx, tx, a.UserID, factID, now); err != nil {
		return err
	}
	if err := s.grantBadge(ctx, tx, a.UserID, BadgeFirstReview, confirmParticipation, map[string]any{"attemptId": a.AttemptID}, now); err != nil {
		return err
	}
	if a.Independent && a.Passed {
		m, err := s.Catalog.Manifest(ctx, tx, a.ProgramVersion)
		if err != nil {
			return err
		}
		for _, o := range m.Outcomes {
			if o.BadgeID != nil && slices.Contains(o.PracticeMarkItemIDs, a.ItemID) {
				if err := s.grantBadge(ctx, tx, a.UserID, *o.BadgeID, confirmPractice, map[string]any{"attemptId": a.AttemptID, "outcomeId": o.ID}, now); err != nil {
					return err
				}
			}
		}
	}
	return s.checkRoute(ctx, tx, a.UserID, now)
}

// checkRoute выдаёт «В своём темпе» за завершение выбранного маршрута; ежедневность не нужна.
func (s *Service) checkRoute(ctx context.Context, tx pgx.Tx, userID string, now time.Time) error {
	var route, version string
	if err := tx.QueryRow(ctx, `SELECT u.route, e.program_version FROM users u JOIN enrollments e ON e.user_id = u.id WHERE u.id = $1`,
		userID).Scan(&route, &version); err != nil {
		return err
	}
	m, err := s.Catalog.Manifest(ctx, tx, version)
	if err != nil {
		return err
	}
	r, ok := m.Route(route)
	if !ok || len(r.FamilyIDs) == 0 {
		return nil
	}
	var done int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM learning_completions WHERE user_id = $1 AND family_id = ANY($2)`,
		userID, r.FamilyIDs).Scan(&done); err != nil {
		return err
	}
	if done < len(r.FamilyIDs) {
		return nil
	}
	return s.grantBadge(ctx, tx, userID, BadgeOwnPace, confirmParticipation, map[string]any{"route": route}, now)
}

// ReviewAccepted — принятый ответ в доступном review закрывает день сразу, не дожидаясь
// финализации пары до полуночи (§15). Диагностика день не закрывает.
func (s *Service) ReviewAccepted(ctx context.Context, tx pgx.Tx, userID, attemptID, role string, now time.Time) error {
	factID, _, err := s.record(ctx, tx, fact{userID: userID, name: "answer_accepted", sourceType: "attempt", sourceID: attemptID,
		dedupe: "answer:" + attemptID, payload: map[string]any{"role": role}, at: now})
	if err != nil || role != "review" {
		return err
	}
	return s.creditDay(ctx, tx, userID, factID, now)
}

// SetFinalized — XP за отвеченные review карточки, учебные свидетельства и зависящие от
// оценки бейджи. Всё это выдаётся только при финализации (§9, §13).
func (s *Service) SetFinalized(ctx context.Context, tx pgx.Tx, userID, setID string, now time.Time) error {
	var kind, lineage, slot, version, status string
	if err := tx.QueryRow(ctx, `SELECT kind, program_lineage, slot_family_id, program_version, finalized_status FROM assessment_sets WHERE id = $1`,
		setID).Scan(&kind, &lineage, &slot, &version, &status); err != nil {
		return err
	}
	factID, created, err := s.record(ctx, tx, fact{userID: userID, name: "assessment_finalized", sourceType: "assessment_set", sourceID: setID,
		dedupe: "set:" + setID, payload: map[string]any{"kind": kind, "status": status}, at: now})
	if err != nil || !created || kind != "review" {
		return err
	}

	type answered struct {
		familyID, attemptID, evaluationID, outcomeID string
		passed, independent                          bool
		acceptedAt                                   time.Time
		anchorAt                                     *time.Time
	}
	rows, err := tx.Query(ctx, `
		SELECT a.family_id, t.id, e.id, coalesce(f.assessed_outcome_id, ''), e.passed,
		       t.independence_eligible AND NOT EXISTS (SELECT 1 FROM assistance_events h WHERE h.attempt_id = t.id),
		       sub.accepted_at, a.anchor_accepted_at
		FROM assignments a
		JOIN attempts t ON t.assignment_id = a.id AND t.status = 'submitted'
		JOIN submissions sub ON sub.attempt_id = t.id
		JOIN evaluations e ON e.attempt_id = t.id AND e.replaces_evaluation_id IS NULL
		JOIN content_families f ON f.id = a.family_id
		WHERE a.assessment_set_id = $1 ORDER BY sub.accepted_at`, setID)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (answered, error) {
		var x answered
		return x, r.Scan(&x.familyID, &x.attemptID, &x.evaluationID, &x.outcomeID, &x.passed, &x.independent, &x.acceptedAt, &x.anchorAt)
	})
	if err != nil {
		return err
	}
	m, err := s.Catalog.Manifest(ctx, tx, version)
	if err != nil {
		return err
	}
	for _, x := range list {
		if err := s.grantXP(ctx, tx, userID, "review:"+lineage+":"+slot+":"+x.familyID, s.Rules.XP.Review, factID, now); err != nil {
			return err
		}
		if !x.passed || !x.independent || x.outcomeID == "" {
			continue
		}
		var delay *int64
		if x.anchorAt != nil {
			delay = new(int64(x.acceptedAt.Sub(*x.anchorAt) / time.Second))
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO learning_evidence (id, user_id, outcome_id, attempt_id, evaluation_id, assessment_set_id,
			                               actual_delay_seconds, accepted_at, rule_version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) ON CONFLICT (attempt_id) DO NOTHING`,
			s.IDs.New("evd"), userID, x.outcomeID, x.attemptID, x.evaluationID, setID, delay, x.acceptedAt, s.Rules.RuleVersion); err != nil {
			return err
		}
		refs := map[string]any{"attemptId": x.attemptID, "outcomeId": x.outcomeID}
		if err := s.grantBadge(ctx, tx, userID, BadgeLaterConfirmed, confirmEvidence, refs, now); err != nil {
			return err
		}
		for _, o := range m.Outcomes {
			if o.ID != x.outcomeID || o.BadgeID == nil {
				continue
			}
			// Уже выданный за практику бейдж дополняется отметкой «подтвердил спустя время», а не
			// выдаётся второй раз (§14).
			if err := s.grantBadge(ctx, tx, userID, *o.BadgeID, confirmEvidence, refs, now); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE badge_grants SET confirmed_later_at = $3
				WHERE user_id = $1 AND badge_id = $2 AND confirmed_later_at IS NULL`, userID, *o.BadgeID, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// NotificationAccepted — провайдер принял уведомление. Это не доставка и не прочтение (§18).
func (s *Service) NotificationAccepted(ctx context.Context, tx pgx.Tx, userID, notificationID, triggerType string, now time.Time) error {
	_, _, err := s.record(ctx, tx, fact{userID: userID, name: "notification_provider_accepted", sourceType: "notification",
		sourceID: notificationID, dedupe: "notification:" + notificationID, payload: map[string]any{"triggerType": triggerType}, at: now})
	return err
}

// LabStep — шаг лаборатории с предусмотренным ответом: решение, разбор или сравнение.
// Закрывает учебный день (§15).
func (s *Service) LabStep(ctx context.Context, tx pgx.Tx, userID, name, sourceType, sourceID string, now time.Time) error {
	factID, _, err := s.record(ctx, tx, fact{userID: userID, name: name, sourceType: sourceType, sourceID: sourceID,
		dedupe: name + ":" + sourceID, at: now})
	if err != nil {
		return err
	}
	return s.creditDay(ctx, tx, userID, factID, now)
}

// LabCaseCompleted — первый полный кейс семейства даёт XP; повторные эксперименты — нет (§10).
func (s *Service) LabCaseCompleted(ctx context.Context, tx pgx.Tx, userID, experimentID, caseFamilyID string, now time.Time) error {
	factID, _, err := s.record(ctx, tx, fact{userID: userID, name: "lab_case_completed", sourceType: "lab_experiment", sourceID: experimentID,
		dedupe: "lab_case_completed:" + experimentID, payload: map[string]any{"caseFamilyId": caseFamilyID}, at: now})
	if err != nil {
		return err
	}
	if err := s.grantXP(ctx, tx, userID, "lab:"+caseFamilyID, s.Rules.XP.Lab, factID, now); err != nil {
		return err
	}
	return s.creditDay(ctx, tx, userID, factID, now)
}

// ThreeScenarios — все три сценария разобраны на одном исходном решении (§14).
func (s *Service) ThreeScenarios(ctx context.Context, tx pgx.Tx, userID, decisionID string, now time.Time) error {
	return s.grantBadge(ctx, tx, userID, BadgeThreeScenarios, confirmParticipation, map[string]any{"decisionId": decisionID}, now)
}
