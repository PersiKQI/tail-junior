package learning

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"teyljunior/internal/assessment"
)

// NextStep — одна основная рекомендация с причиной (§7). Не зависит от скрытой правильности
// незавершённого набора: учитываются только статусы и сроки.
type NextStep struct {
	Kind         string  `json:"kind"`
	Reason       string  `json:"reason"`
	AttemptID    *string `json:"attemptId"`
	AssignmentID *string `json:"assignmentId"`
	SetID        *string `json:"setId"`
	ExperimentID *string `json:"experimentId"`
}

// Виды следующего шага. Событие появится на этапе 4.
const (
	StepContinueAttempt = "continue_attempt"
	StepReviewSet       = "review_set"
	StepLearning        = "learning"
	StepLabExperiment   = "lab_experiment"
	StepRepeat          = "repeat"
	StepNone            = "none"
)

// Next выбирает шаг по порядку §7: продолжить попытку, готовая отложенная пара, следующее
// новое задание маршрута, незаконченный лабораторный кейс, назначенный повтор, другая
// открытая тема. Назначенный повтор создаётся здесь же, поэтому нужна транзакция.
func (s *Service) Next(ctx context.Context, tx pgx.Tx, userID string, now time.Time) (NextStep, error) {
	var attemptID, assignmentID string
	err := tx.QueryRow(ctx, `SELECT id, assignment_id FROM attempts WHERE user_id = $1 AND status = 'started' ORDER BY issued_at DESC LIMIT 1`,
		userID).Scan(&attemptID, &assignmentID)
	if err == nil {
		return NextStep{Kind: StepContinueAttempt, Reason: "Твой ответ ещё не отправлен. Можно продолжить с этого шага.",
			AttemptID: &attemptID, AssignmentID: &assignmentID}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return NextStep{}, err
	}

	sets, err := s.Assess.Summaries(ctx, tx, userID, now)
	if err != nil {
		return NextStep{}, err
	}
	for _, st := range sets {
		if st.Kind == assessment.KindReview && (st.Status == assessment.StatusAvailable || st.Status == assessment.StatusInProgress) {
			return NextStep{Kind: StepReviewSet, Reason: "Готова новая задача по знакомой теме. Проверим решение без подсказок.",
				SetID: new(st.SetID)}, nil
		}
	}

	p, err := s.Summary(ctx, tx, userID)
	if err != nil {
		return NextStep{}, err
	}
	if p.NextAssignmentID != nil {
		return NextStep{Kind: StepLearning, Reason: "Следующее задание выбранного маршрута.", AssignmentID: p.NextAssignmentID}, nil
	}

	var experiment string
	err = tx.QueryRow(ctx, `SELECT id FROM lab_experiments WHERE user_id = $1 AND status = 'open' ORDER BY created_at LIMIT 1`, userID).Scan(&experiment)
	if err == nil {
		return NextStep{Kind: StepLabExperiment, Reason: "Лабораторный кейс не закончен. Твоё решение сохранено.", ExperimentID: &experiment}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return NextStep{}, err
	}

	repeat, err := s.ScheduleRepeat(ctx, tx, userID, now)
	if err != nil {
		return NextStep{}, err
	}
	if repeat != "" {
		return NextStep{Kind: StepRepeat, Reason: "Это повторение знакомой темы. Оно поможет вернуться к теме и не даст повторный XP.",
			AssignmentID: &repeat}, nil
	}

	var other string
	err = tx.QueryRow(ctx, `
		SELECT a.id FROM assignments a
		WHERE a.user_id = $1 AND a.role = 'learning' AND a.status = 'open'
		  AND NOT EXISTS (SELECT 1 FROM learning_completions c WHERE c.user_id = a.user_id AND c.family_id = a.family_id)
		ORDER BY a.item_id LIMIT 1`, userID).Scan(&other)
	if err == nil {
		return NextStep{Kind: StepLearning, Reason: "Маршрут пройден. Остались другие темы, если интересно.", AssignmentID: &other}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return NextStep{}, err
	}
	return NextStep{Kind: StepNone, Reason: "Все задания пройдены. Отложенные проверки появятся в свой срок."}, nil
}
