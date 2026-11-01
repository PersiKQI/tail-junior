package legacy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	bankfiles "teyljunior/content"
	"teyljunior/internal/core"
	"teyljunior/internal/store"
)

// Option — вариант ответа прежней проверки.
type Option struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// D7Content — карточка прежней самостоятельной проверки (assessmentContent из store.ts).
type D7Content struct {
	Provenance      string   `json:"provenance"`
	ContentVersion  string   `json:"contentVersion"`
	OpenAfterHours  int      `json:"openAfterHours"`
	CloseAfterHours int      `json:"closeAfterHours"`
	Question        string   `json:"question"`
	Context         string   `json:"context"`
	Choices         []Option `json:"choices"`
	Reasons         []Option `json:"reasons"`
	CorrectChoice   string   `json:"correctChoice"`
	CorrectReason   string   `json:"correctReason"`
	Explanation     string   `json:"explanation"`
}

// LoadD7 разбирает встроенную карточку.
func LoadD7() (*D7Content, error) {
	var c D7Content
	dec := json.NewDecoder(bytes.NewReader(bankfiles.LegacyD7))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("legacy-d7.json: %w", err)
	}
	has := func(opts []Option, id string) bool {
		return slices.ContainsFunc(opts, func(o Option) bool { return o.ID == id })
	}
	if !has(c.Choices, c.CorrectChoice) || !has(c.Reasons, c.CorrectReason) || c.OpenAfterHours <= 0 || c.CloseAfterHours <= c.OpenAfterHours {
		return nil, errors.New("legacy-d7.json: ключ или окно не согласованы")
	}
	return &c, nil
}

// Статусы прежнего контракта.
const (
	D7Waiting   = "waiting"
	D7Available = "available"
	D7Expired   = "expired"
	D7Completed = "completed"
)

// D7View — состояние перенесённого окна. Вопрос виден только пока окно открыто; ключ и разбор
// — только после ответа (как в store.ts).
type D7View struct {
	Status      string    `json:"status"`
	AvailableAt time.Time `json:"availableAt"`
	ClosesAt    time.Time `json:"closesAt"`
	Correct     *bool     `json:"correct"`
	Question    string    `json:"question,omitempty"`
	Context     string    `json:"context,omitempty"`
	Choices     []Option  `json:"choices,omitempty"`
	Reasons     []Option  `json:"reasons,omitempty"`
}

// D7Result — ответ на проверку.
type D7Result struct {
	Correct          bool   `json:"correct"`
	Title            string `json:"title"`
	Explanation      string `json:"explanation"`
	AlreadyCompleted bool   `json:"alreadyCompleted"`
	Assessment       D7View `json:"assessment"`
}

// D7 — перенесённый контракт старого D7. Новые окна не создаются: работают только записи
// assessment_d7 из импорта.
type D7 struct{ Content *D7Content }

type d7Window struct {
	FirstAnswerAt time.Time `json:"firstAnswerAt"`
}

// Get возвращает состояние окна; пользователю без перенесённого окна — 404.
func (d *D7) Get(ctx context.Context, q store.Querier, userID string, now time.Time) (D7View, error) {
	var raw []byte
	err := q.QueryRow(ctx, `SELECT payload FROM legacy_records WHERE user_id = $1 AND source_kind = 'assessment_d7'`, userID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return D7View{}, core.NotFound()
	}
	if err != nil {
		return D7View{}, err
	}
	var w d7Window
	if err := json.Unmarshal(raw, &w); err != nil {
		return D7View{}, err
	}
	c := d.Content
	v := D7View{
		AvailableAt: w.FirstAnswerAt.Add(time.Duration(c.OpenAfterHours) * time.Hour).UTC(),
		ClosesAt:    w.FirstAnswerAt.Add(time.Duration(c.CloseAfterHours) * time.Hour).UTC(),
	}
	var correct bool
	err = q.QueryRow(ctx, `SELECT correct FROM legacy_d7_answers WHERE user_id = $1`, userID).Scan(&correct)
	switch {
	case err == nil:
		v.Status, v.Correct = D7Completed, &correct
	case !errors.Is(err, pgx.ErrNoRows):
		return D7View{}, err
	case now.Before(v.AvailableAt):
		v.Status = D7Waiting
	case !now.Before(v.ClosesAt):
		v.Status = D7Expired
	default:
		v.Status = D7Available
		v.Question, v.Context, v.Choices, v.Reasons = c.Question, c.Context, c.Choices, c.Reasons
	}
	return v, nil
}

// Answer принимает единственный ответ. Повтор после сохранения возвращает прежний результат с
// alreadyCompleted, вне окна — 409 ASSESSMENT_UNAVAILABLE, как в store.ts.
func (d *D7) Answer(ctx context.Context, tx pgx.Tx, userID, choiceID, reasonID string, now time.Time) (D7Result, error) {
	c := d.Content
	has := func(opts []Option, id string) bool {
		return slices.ContainsFunc(opts, func(o Option) bool { return o.ID == id })
	}
	if !has(c.Choices, choiceID) || !has(c.Reasons, reasonID) {
		return D7Result{}, core.Unprocessable("UNKNOWN_OPTION", "выберите действие и объяснение")
	}
	v, err := d.Get(ctx, tx, userID, now)
	if err != nil {
		return D7Result{}, err
	}
	switch v.Status {
	case D7Completed:
		return D7Result{Correct: *v.Correct, Title: "Проверка уже сохранена", Explanation: c.Explanation, AlreadyCompleted: true, Assessment: v}, nil
	case D7Available:
	default:
		return D7Result{}, core.Conflict("ASSESSMENT_UNAVAILABLE", "проверка сейчас недоступна; дата есть на экране прогресса")
	}
	correct := choiceID == c.CorrectChoice && reasonID == c.CorrectReason
	if _, err := tx.Exec(ctx, `
		INSERT INTO legacy_d7_answers (user_id, choice_id, reason_id, correct, content_version, accepted_at)
		VALUES ($1, $2, $3, $4, $5, $6)`, userID, choiceID, reasonID, correct, c.ContentVersion, now); err != nil {
		return D7Result{}, err
	}
	if v, err = d.Get(ctx, tx, userID, now); err != nil {
		return D7Result{}, err
	}
	title := "Спасибо, ответ сохранён"
	if correct {
		title = "Ты справился с новой задачей"
	}
	return D7Result{Correct: correct, Title: title, Explanation: c.Explanation, Assessment: v}, nil
}
