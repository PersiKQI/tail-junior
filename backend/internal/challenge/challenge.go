// Пакет challenge — личное игровое событие поверх лабораторного кейса (спецификация §16).
// Событие не создаёт параллельный симулятор: эпизоды выводятся из фактов эксперимента.
// Неделя витрины — редакционный ритм; после endsAt событие уходит в архив с той же наградой.
// Нет PvP, рейтинга доходности, платных попыток и случайной награды.
package challenge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"teyljunior/internal/core"
	"teyljunior/internal/lab"
	"teyljunior/internal/progression"
	"teyljunior/internal/store"
)

// Эпизоды события и фазы.
const (
	EpisodeDecision   = "decision"
	EpisodeEarlyExit  = "early_exit"
	EpisodeComparison = "comparison"

	PhaseUpcoming  = "upcoming"
	PhaseLive      = "live"
	PhaseArchived  = "archived"
	PhaseWithdrawn = "withdrawn"
)

// earlyScenario — сценарий кейса, на котором держится второй эпизод.
const earlyScenario = "early"

// Episode — эпизод события.
type Episode struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// Release — файл события.
type Release struct {
	Provenance   string    `json:"provenance"`
	FamilyID     string    `json:"familyId"`
	Revision     int       `json:"revision"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	StartsAt     time.Time `json:"startsAt"`
	EndsAt       time.Time `json:"endsAt"`
	CaseFamilyID string    `json:"caseFamilyId"`
	CosmeticID   string    `json:"cosmeticId"`
	Episodes     []Episode `json:"episodes"`
}

// ParseRelease строго разбирает и проверяет событие.
func ParseRelease(data []byte) (*Release, error) {
	var r Release
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("challenge: %w", err)
	}
	ids := []string{}
	for _, e := range r.Episodes {
		ids = append(ids, e.ID)
	}
	if r.FamilyID == "" || r.Revision < 1 || r.CaseFamilyID == "" || r.CosmeticID == "" || !r.EndsAt.After(r.StartsAt) {
		return nil, errors.New("challenge: нужны familyId, revision, caseFamilyId, cosmeticId и endsAt позже startsAt")
	}
	if fmt.Sprint(ids) != fmt.Sprint([]string{EpisodeDecision, EpisodeEarlyExit, EpisodeComparison}) {
		return nil, fmt.Errorf("challenge: эпизоды %v, ожидались decision, early_exit, comparison", ids)
	}
	return &r, nil
}

// Import записывает событие как draft; та же редакция с другим содержимым отклоняется.
func Import(ctx context.Context, db *store.DB, r *Release, now time.Time) error {
	payload, err := json.Marshal(r)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(payload)
	sha := hex.EncodeToString(sum[:])
	var existing string
	err = db.Pool.QueryRow(ctx, `
		INSERT INTO challenge_releases (family_id, revision, starts_at, ends_at, status, case_family_id, cosmetic_id, payload, sha256, imported_at)
		VALUES ($1, $2, $3, $4, 'draft', $5, $6, $7, $8, $9)
		ON CONFLICT (family_id, revision) DO UPDATE SET family_id = EXCLUDED.family_id RETURNING sha256`,
		r.FamilyID, r.Revision, r.StartsAt, r.EndsAt, r.CaseFamilyID, r.CosmeticID, payload, sha, now).Scan(&existing)
	if err != nil {
		return err
	}
	if existing != sha {
		return fmt.Errorf("событие %s r%d уже импортировано с другим содержимым", r.FamilyID, r.Revision)
	}
	return nil
}

// Publish публикует событие вместе с утверждённым пакетом (§16).
func Publish(ctx context.Context, db *store.DB, familyID string, revision int, approvedBy string, now time.Time) error {
	if approvedBy == "" {
		return errors.New("публикация требует имени утвердившего редактора")
	}
	tag, err := db.Pool.Exec(ctx, `UPDATE challenge_releases SET status = 'published', published_at = $3, approved_by = $4
		WHERE family_id = $1 AND revision = $2 AND status = 'draft'`, familyID, revision, now, approvedBy)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("событие %s r%d не найдено или уже опубликовано", familyID, revision)
	}
	return nil
}

// Service — участие в событиях.
type Service struct {
	Lab  *lab.Service
	Prog *progression.Service
	IDs  core.IDs
}

// EpisodeView — эпизод с отметкой выполнения.
type EpisodeView struct {
	Episode
	Done bool `json:"done"`
}

// ParticipationView — участие пользователя.
type ParticipationView struct {
	ParticipationID string     `json:"participationId"`
	Revision        int        `json:"revision"`
	ExperimentID    string     `json:"experimentId"`
	JoinedAt        time.Time  `json:"joinedAt"`
	CompletedAt     *time.Time `json:"completedAt"`
}

// View — событие для пользователя.
type View struct {
	FamilyID      string             `json:"familyId"`
	Revision      int                `json:"revision"`
	Title         string             `json:"title"`
	Description   string             `json:"description"`
	Phase         string             `json:"phase"`
	StartsAt      time.Time          `json:"startsAt"`
	EndsAt        time.Time          `json:"endsAt"`
	CaseFamilyID  string             `json:"caseFamilyId"`
	CosmeticID    string             `json:"cosmeticId"`
	Episodes      []EpisodeView      `json:"episodes"`
	Participation *ParticipationView `json:"participation"`
}

func phase(status string, r *Release, now time.Time) string {
	switch {
	case status == "withdrawn":
		return PhaseWithdrawn
	case now.Before(r.StartsAt):
		return PhaseUpcoming
	case now.Before(r.EndsAt):
		return PhaseLive
	}
	return PhaseArchived
}

// latest — последняя опубликованная или отозванная редакция семейства.
func latest(ctx context.Context, q store.Querier, familyID string) (*Release, string, error) {
	var raw []byte
	var status string
	err := q.QueryRow(ctx, `SELECT payload, status FROM challenge_releases
		WHERE family_id = $1 AND status IN ('published', 'withdrawn') ORDER BY revision DESC LIMIT 1`, familyID).Scan(&raw, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", core.NotFound()
	}
	if err != nil {
		return nil, "", err
	}
	var r Release
	return &r, status, json.Unmarshal(raw, &r)
}

// List — активные, предстоящие и архивные события; участие досчитывается по фактам.
func (s *Service) List(ctx context.Context, tx pgx.Tx, userID string, now time.Time) ([]View, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT family_id FROM challenge_releases WHERE status IN ('published', 'withdrawn') ORDER BY family_id`)
	if err != nil {
		return nil, err
	}
	families, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := []View{}
	for _, f := range families {
		v, err := s.view(ctx, tx, userID, f, now)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Join вступает в событие или продолжает его из архива. Поздний пользователь начинает с
// первого эпизода; уже выполненный вне события кейс сопоставляется с эпизодами (§16).
func (s *Service) Join(ctx context.Context, tx pgx.Tx, userID, familyID string, now time.Time) (View, bool, error) {
	r, status, err := latest(ctx, tx, familyID)
	if err != nil {
		return View{}, false, err
	}
	switch phase(status, r, now) {
	case PhaseWithdrawn:
		return View{}, false, core.Gone("CHALLENGE_WITHDRAWN", "событие снято; история участия сохранена")
	case PhaseUpcoming:
		return View{}, false, core.Conflict("CHALLENGE_NOT_STARTED", "событие ещё не началось")
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM challenge_participations WHERE user_id = $1 AND family_id = $2)`,
		userID, familyID).Scan(&exists); err != nil {
		return View{}, false, err
	}
	if exists {
		v, err := s.view(ctx, tx, userID, familyID, now)
		return v, false, err
	}
	var experimentID string
	err = tx.QueryRow(ctx, `SELECT id FROM lab_experiments WHERE user_id = $1 AND case_family_id = $2
		ORDER BY (status = 'completed') DESC, created_at DESC LIMIT 1`, userID, r.CaseFamilyID).Scan(&experimentID)
	if errors.Is(err, pgx.ErrNoRows) {
		exp, _, err := s.Lab.CreateExperiment(ctx, tx, userID, r.CaseFamilyID, now)
		if err != nil {
			return View{}, false, err
		}
		experimentID = exp.ExperimentID
	} else if err != nil {
		return View{}, false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO challenge_participations (id, user_id, family_id, revision, experiment_id, joined_at)
		VALUES ($1, $2, $3, $4, $5, $6)`, s.IDs.New("chp"), userID, familyID, r.Revision, experimentID, now); err != nil {
		return View{}, false, err
	}
	v, err := s.view(ctx, tx, userID, familyID, now)
	return v, true, err
}

// Get — участие пользователя; без участия — 404.
func (s *Service) Get(ctx context.Context, tx pgx.Tx, userID, familyID string, now time.Time) (View, error) {
	v, err := s.view(ctx, tx, userID, familyID, now)
	if err == nil && v.Participation == nil {
		return v, core.NotFound()
	}
	return v, err
}

// view собирает событие и, если есть участие, отмечает эпизоды по фактам эксперимента и
// фиксирует завершение с одной косметической наградой на семейство.
func (s *Service) view(ctx context.Context, tx pgx.Tx, userID, familyID string, now time.Time) (View, error) {
	r, status, err := latest(ctx, tx, familyID)
	if err != nil {
		return View{}, err
	}
	v := View{FamilyID: r.FamilyID, Revision: r.Revision, Title: r.Title, Description: r.Description, Phase: phase(status, r, now),
		StartsAt: r.StartsAt.UTC(), EndsAt: r.EndsAt.UTC(), CaseFamilyID: r.CaseFamilyID, CosmeticID: r.CosmeticID}
	for _, e := range r.Episodes {
		v.Episodes = append(v.Episodes, EpisodeView{Episode: e})
	}
	var p ParticipationView
	err = tx.QueryRow(ctx, `SELECT id, revision, experiment_id, joined_at, completed_at FROM challenge_participations
		WHERE user_id = $1 AND family_id = $2`, userID, familyID).Scan(&p.ParticipationID, &p.Revision, &p.ExperimentID, &p.JoinedAt, &p.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, nil
	}
	if err != nil {
		return View{}, err
	}
	done := map[string]bool{}
	var d, e, c bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM lab_decisions WHERE experiment_id = $1 AND source_decision_id IS NULL),
		       EXISTS (SELECT 1 FROM lab_runs r JOIN lab_decisions d ON d.id = r.decision_id
		               JOIN lab_reflections f ON f.run_id = r.id
		               WHERE r.experiment_id = $1 AND d.source_decision_id IS NULL AND r.scenario_id = $2),
		       (SELECT status = 'completed' FROM lab_experiments WHERE id = $1)`, p.ExperimentID, earlyScenario).Scan(&d, &e, &c); err != nil {
		return View{}, err
	}
	done[EpisodeDecision], done[EpisodeEarlyExit], done[EpisodeComparison] = d, e, c
	for i := range v.Episodes {
		v.Episodes[i].Done = done[v.Episodes[i].ID]
	}
	if d && e && c && p.CompletedAt == nil {
		if _, err := tx.Exec(ctx, `UPDATE challenge_participations SET completed_at = $2 WHERE id = $1`, p.ParticipationID, now); err != nil {
			return View{}, err
		}
		if err := s.Prog.ChallengeCompleted(ctx, tx, userID, familyID, p.ParticipationID, now); err != nil {
			return View{}, err
		}
		p.CompletedAt = &now
	}
	p.JoinedAt = p.JoinedAt.UTC()
	if p.CompletedAt != nil {
		p.CompletedAt = new(p.CompletedAt.UTC())
	}
	v.Participation = &p
	return v, nil
}
