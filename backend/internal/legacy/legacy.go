// Пакет legacy — аддитивный и воспроизводимый перенос снимка SQLite текущего Node-сервера
// (спецификация §23). До импорта создаётся резервная копия и манифест с количеством строк и
// hash; читается только копия. Прежние ответы и лаборатория сохраняются как legacy_records с
// ограничениями и не становятся новыми завершениями, свидетельствами или кейсами.
package legacy

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	_ "modernc.org/sqlite" // драйвер "sqlite" на чистом Go

	"teyljunior/internal/content"
	"teyljunior/internal/core"
	"teyljunior/internal/learning"
	"teyljunior/internal/store"
)

// SchemaVersion — версия формата манифеста импорта.
const SchemaVersion = 1

// d7Open, d7Close — окно старой самостоятельной проверки от первого ответа (store.ts).
const (
	d7Open  = 144 * time.Hour
	d7Close = 216 * time.Hour
)

// Ограничения переносимых записей — показываются вместе с историей, а не скрываются.
const (
	limitProfile    = "Исходный профиль: ageBand 21–22 отображён как other, focus в v2 не используется, исходные значения сохранены здесь."
	limitAnswer     = "Ответ прежней программы накоплений: не является заданием L01–L24 и не даёт XP, свидетельств и бейджей."
	limitInvestment = "Известны первая и последняя попытки, промежуточные — нет; порядок вариантов, помощь и время первого раскрытия неизвестны."
	limitLab        = "Снимок прежней лаборатории (модель V1): не является полным кейсом V2, итоги V1 не пересчитываются."
	limitD7         = "Прежний D7 (окно 144–216 часов от первого ответа): не RS07; сохраняется до исходного closesAt."
	limitAction     = "Выполненное действие прежней схемы: связь с созданной отметкой не хранилась."
	limitNoGoal     = "Запись прежней схемы без цели: в v2 отметки и действия привязаны к цели."
)

// Importer переносит снимок.
type Importer struct {
	DB       *store.DB
	Catalog  *content.Catalog
	Learning *learning.Service
	IDs      core.IDs
	Clock    core.Clock
}

// Manifest — итог импорта: сколько прочитано, перенесено и пропущено.
type Manifest struct {
	SourceSHA256  string         `json:"sourceSha256"`
	BackupPath    string         `json:"backupPath"`
	ImportedAt    time.Time      `json:"importedAt"`
	SchemaVersion int            `json:"schemaVersion"`
	Read          map[string]int `json:"read"`
	Imported      map[string]int `json:"imported"`
	Skipped       map[string]int `json:"skipped"`
	Problems      []string       `json:"problems"`
}

// Run делает резервную копию, читает её и переносит сессии. Уже перенесённые пользователи
// пропускаются: повторный импорт не создаёт дублей. Каждая сессия — своя транзакция.
func (im *Importer) Run(ctx context.Context, sqlitePath, backupDir string) (Manifest, error) {
	now := im.Clock.Now()
	data, err := os.ReadFile(sqlitePath)
	if err != nil {
		return Manifest{}, err
	}
	sum := sha256.Sum256(data)
	m := Manifest{SourceSHA256: hex.EncodeToString(sum[:]), ImportedAt: now, SchemaVersion: SchemaVersion,
		Read: map[string]int{}, Imported: map[string]int{}, Skipped: map[string]int{}, Problems: []string{}}
	if err := os.MkdirAll(backupDir, 0o750); err != nil {
		return m, err
	}
	m.BackupPath = filepath.Join(backupDir, "teyl-legacy-"+m.SourceSHA256[:16]+".sqlite")
	if _, err := os.Stat(m.BackupPath); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(m.BackupPath, data, 0o640); err != nil {
			return m, err
		}
	}
	snap, err := readSnapshot(ctx, m.BackupPath, m.Read)
	if err != nil {
		return m, err
	}
	version, err := im.Catalog.LatestPublished(ctx, im.DB.Pool)
	if err != nil {
		return m, err
	}
	for _, s := range snap.sessions {
		err := im.DB.InTx(ctx, func(tx pgx.Tx) error {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, s.ID).Scan(&exists); err != nil {
				return err
			}
			if exists {
				m.Skipped["sessions_already_imported"]++
				return nil
			}
			return im.importSession(ctx, tx, snap, s, version, m.SourceSHA256, now, &m)
		})
		if err != nil {
			m.Skipped["sessions_failed"]++
			m.Problems = append(m.Problems, fmt.Sprintf("session %s: %v", s.ID, err))
		}
	}
	counts, err := json.Marshal(m)
	if err != nil {
		return m, err
	}
	if _, err := im.DB.Pool.Exec(ctx, `
		INSERT INTO import_manifests (id, source_sha256, backup_path, imported_at, schema_version, counts)
		VALUES ($1, $2, $3, $4, $5, $6)`, im.IDs.New("imp"), m.SourceSHA256, m.BackupPath, now, SchemaVersion, counts); err != nil {
		return m, err
	}
	return m, os.WriteFile(m.BackupPath+".manifest.json", counts, 0o640)
}

// Строки исходной схемы store.ts.
type session struct {
	ID, TokenHash, Profile string
	Goal, FirstAnswerAt    *string
}

type answer struct {
	SessionID, Kind, ContentID, ChoiceID, CreatedAt, ContentVersion string
	ReasonID                                                        *string
	Correct                                                         bool
}

type checkin struct {
	ID, SessionID, Type, Note, CreatedAt string
	Amount                               *int64
}

type action struct {
	ID, SessionID, Type, Note, DueDate, CreatedAt string
	Amount                                        *int64
	CompletedAt                                   *string
}

type expense struct {
	ID, SessionID, Category, OccurredOn, Note, CreatedAt string
	Amount                                               int64
	VoidedAt                                             *string
}

type investmentAnswer struct {
	SessionID, LessonID, First, Latest, ContentVersion string
}

type snapshot struct {
	sessions    []session
	answers     map[string][]answer
	checkins    map[string][]checkin
	actions     map[string][]action
	expenses    map[string][]expense
	budgets     map[string]string
	investments map[string][]investmentAnswer
	labs        map[string][2]string // value, content_version
}

func readSnapshot(ctx context.Context, path string, read map[string]int) (*snapshot, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	tables := map[string]bool{}
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		tables[n] = true
	}
	rows.Close()
	if !tables["sessions"] {
		return nil, errors.New("в снимке нет таблицы sessions")
	}
	s := &snapshot{answers: map[string][]answer{}, checkins: map[string][]checkin{}, actions: map[string][]action{},
		expenses: map[string][]expense{}, budgets: map[string]string{}, investments: map[string][]investmentAnswer{}, labs: map[string][2]string{}}
	each := func(table, query string, scan func(*sql.Rows) error) error {
		if !tables[table] {
			return nil
		}
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			return fmt.Errorf("%s: %w", table, err)
		}
		defer rows.Close()
		for rows.Next() {
			if err := scan(rows); err != nil {
				return fmt.Errorf("%s: %w", table, err)
			}
			read[table]++
		}
		return rows.Err()
	}
	steps := []error{
		each("sessions", `SELECT id, token_hash, profile, goal, first_answer_at FROM sessions ORDER BY id`, func(r *sql.Rows) error {
			var x session
			err := r.Scan(&x.ID, &x.TokenHash, &x.Profile, &x.Goal, &x.FirstAnswerAt)
			s.sessions = append(s.sessions, x)
			return err
		}),
		each("answers", `SELECT session_id, kind, content_id, choice_id, reason_id, correct, created_at, content_version FROM answers`, func(r *sql.Rows) error {
			var x answer
			err := r.Scan(&x.SessionID, &x.Kind, &x.ContentID, &x.ChoiceID, &x.ReasonID, &x.Correct, &x.CreatedAt, &x.ContentVersion)
			s.answers[x.SessionID] = append(s.answers[x.SessionID], x)
			return err
		}),
		each("checkins", `SELECT id, session_id, type, amount, note, created_at FROM checkins ORDER BY created_at`, func(r *sql.Rows) error {
			var x checkin
			err := r.Scan(&x.ID, &x.SessionID, &x.Type, &x.Amount, &x.Note, &x.CreatedAt)
			s.checkins[x.SessionID] = append(s.checkins[x.SessionID], x)
			return err
		}),
		each("actions", `SELECT id, session_id, type, amount, note, due_date, completed_at, created_at FROM actions ORDER BY created_at`, func(r *sql.Rows) error {
			var x action
			err := r.Scan(&x.ID, &x.SessionID, &x.Type, &x.Amount, &x.Note, &x.DueDate, &x.CompletedAt, &x.CreatedAt)
			s.actions[x.SessionID] = append(s.actions[x.SessionID], x)
			return err
		}),
		each("expenses", `SELECT id, session_id, amount, category, occurred_on, note, voided_at, created_at FROM expenses ORDER BY created_at`, func(r *sql.Rows) error {
			var x expense
			err := r.Scan(&x.ID, &x.SessionID, &x.Amount, &x.Category, &x.OccurredOn, &x.Note, &x.VoidedAt, &x.CreatedAt)
			s.expenses[x.SessionID] = append(s.expenses[x.SessionID], x)
			return err
		}),
		each("budgets", `SELECT session_id, value FROM budgets`, func(r *sql.Rows) error {
			var id, v string
			err := r.Scan(&id, &v)
			s.budgets[id] = v
			return err
		}),
		each("investment_answers", `SELECT session_id, lesson_id, first_attempt, latest_attempt, content_version FROM investment_answers`, func(r *sql.Rows) error {
			var x investmentAnswer
			err := r.Scan(&x.SessionID, &x.LessonID, &x.First, &x.Latest, &x.ContentVersion)
			s.investments[x.SessionID] = append(s.investments[x.SessionID], x)
			return err
		}),
		each("investment_labs", `SELECT session_id, value, content_version FROM investment_labs`, func(r *sql.Rows) error {
			var id, v, ver string
			err := r.Scan(&id, &v, &ver)
			s.labs[id] = [2]string{v, ver}
			return err
		}),
		// События аналитики старого сервера не переносятся: они не источник наград (§5).
		each("events", `SELECT id FROM events`, func(r *sql.Rows) error { var id int64; return r.Scan(&id) }),
	}
	if err := errors.Join(steps...); err != nil {
		return nil, err
	}
	return s, nil
}

type oldProfile struct {
	Name                string  `json:"name"`
	AgeBand             string  `json:"ageBand"`
	OnboardingCompleted bool    `json:"onboardingCompleted"`
	ReducedMotion       bool    `json:"reducedMotion"`
	Character           *string `json:"character"`
	Focus               *string `json:"focus"`
	FocusUpdatedAt      *string `json:"focusUpdatedAt"`
	CreatedAt           string  `json:"createdAt"`
}

type oldGoal struct {
	Title     string `json:"title"`
	Target    int64  `json:"target"`
	Saved     int64  `json:"saved"`
	Months    int    `json:"months"`
	Monthly   int64  `json:"monthly"`
	Income    int64  `json:"income"`
	Essential int64  `json:"essential"`
	Reserve   int64  `json:"reserve"`
}

type oldBudget struct {
	Income     int64 `json:"income"`
	Reserve    int64 `json:"reserve"`
	Monthly    int64 `json:"monthly"`
	Categories []struct {
		ID     string `json:"id"`
		Amount int64  `json:"amount"`
	} `json:"categories"`
}

// rub переводит прежние рубли в копейки (§23).
func rub(v int64) int64 { return v * 100 }

func parseTime(s string, fallback time.Time) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC()
	}
	return fallback
}

func (im *Importer) importSession(ctx context.Context, tx pgx.Tx, snap *snapshot, s session, version, sha string, now time.Time, m *Manifest) error {
	var p oldProfile
	if err := json.Unmarshal([]byte(s.Profile), &p); err != nil {
		return fmt.Errorf("profile: %w", err)
	}
	hash, err := hex.DecodeString(s.TokenHash)
	if err != nil || len(hash) != sha256.Size {
		return errors.New("token_hash не является hex SHA-256")
	}
	created := parseTime(p.CreatedAt, now)
	var nickname, age, character *string
	if n := strings.TrimSpace(p.Name); n != "" && utf8.RuneCountInString(n) <= 40 && !strings.ContainsFunc(n, unicode.IsControl) {
		nickname = &n
	}
	switch p.AgeBand {
	case "14-17", "18-20":
		age = new(p.AgeBand)
	case "21-22", "other":
		age = new("other")
	}
	if p.Character != nil && slices.Contains([]string{"lumi", "spark", "nova"}, *p.Character) {
		character = p.Character
	}
	// id прежней сессии становится userId, token_hash сохраняется: обновление backend без
	// принудительного нового старта (§4, §23).
	if _, err := tx.Exec(ctx, `
		INSERT INTO users (id, created_at, nickname, age_group, character_id, reduced_motion) VALUES ($1, $2, $3, $4, $5, $6)`,
		s.ID, created, nickname, age, character, p.ReducedMotion); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO credentials (id, user_id, token_hash, created_at) VALUES ($1, $2, $3, $4)`,
		im.IDs.New(core.PrefixCredential), s.ID, hash, created); err != nil {
		return err
	}
	if err := im.Learning.Enroll(ctx, tx, s.ID, version, now); err != nil {
		return err
	}
	rec := func(kind, id string, ver *string, payload any, limits string) error {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO legacy_records (id, user_id, source_kind, source_id, source_version, imported_at, payload, limitations, source_sha256)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, im.IDs.New("lgc"), s.ID, kind, id, ver, now, raw, limits, sha)
		if err == nil {
			m.Imported["legacy_"+kind]++
		}
		return err
	}
	if err := rec("profile", s.ID, nil, p, limitProfile); err != nil {
		return err
	}
	m.Imported["users"]++

	goalID, err := im.importMoney(ctx, tx, snap, s, now, m)
	if err != nil {
		return err
	}
	for _, x := range snap.expenses[s.ID] {
		on, err := time.Parse(time.DateOnly, x.OccurredOn)
		if err != nil || !slices.Contains([]string{"food", "transport", "housing", "other"}, x.Category) || x.Amount <= 0 {
			m.Skipped["expenses_invalid"]++
			continue
		}
		var voided *time.Time
		if x.VoidedAt != nil {
			voided = new(parseTime(*x.VoidedAt, now))
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO expenses (id, user_id, revision, amount_minor, category_id, occurred_on, note, created_at, voided_at)
			VALUES ($1, $2, 1, $3, $4, $5, $6, $7, $8)`, x.ID, s.ID, rub(x.Amount), x.Category, on, x.Note, parseTime(x.CreatedAt, now), voided); err != nil {
			return err
		}
		m.Imported["expenses"]++
	}
	for _, x := range snap.checkins[s.ID] {
		kind := map[string]string{"saved": "saved", "reviewed": "reviewed"}[x.Type]
		if goalID == "" || kind == "" || (kind == "saved") != (x.Amount != nil && *x.Amount > 0) {
			if err := rec("checkin", x.ID, nil, x, limitNoGoal); err != nil {
				return err
			}
			continue
		}
		var amt *int64
		fields := []string{"unknown"} // какие поля проверялись, прежняя схема не хранила
		if x.Amount != nil {
			amt, fields = new(rub(*x.Amount)), []string{}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO checkins (id, user_id, goal_id, kind, amount_minor, reviewed_fields, note, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, x.ID, s.ID, goalID, kind, amt, fields, x.Note, parseTime(x.CreatedAt, now)); err != nil {
			return err
		}
		m.Imported["checkins"]++
	}
	for _, x := range snap.actions[s.ID] {
		due, derr := time.Parse(time.DateOnly, x.DueDate)
		if x.CompletedAt != nil || goalID == "" || derr != nil || (x.Type != "save" && x.Type != "review") {
			limits := limitAction
			if goalID == "" {
				limits = limitNoGoal
			}
			if err := rec("action", x.ID, nil, x, limits); err != nil {
				return err
			}
			continue
		}
		var amt *int64
		if x.Type == "save" {
			if x.Amount == nil || *x.Amount <= 0 {
				if err := rec("action", x.ID, nil, x, limitNoGoal); err != nil {
					return err
				}
				continue
			}
			amt = new(rub(*x.Amount))
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO planned_actions (id, user_id, goal_id, kind, amount_minor, note, due_on, status, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', $8)`, x.ID, s.ID, goalID, x.Type, amt, x.Note, due, parseTime(x.CreatedAt, now)); err != nil {
			return err
		}
		m.Imported["planned_actions"]++
	}

	for _, a := range snap.answers[s.ID] {
		if err := rec("answer", a.Kind+":"+a.ContentID, &a.ContentVersion, a, limitAnswer); err != nil {
			return err
		}
	}
	for _, a := range snap.investments[s.ID] {
		payload := map[string]json.RawMessage{"firstAttempt": json.RawMessage(a.First), "latestAttempt": json.RawMessage(a.Latest)}
		if !json.Valid([]byte(a.First)) || !json.Valid([]byte(a.Latest)) {
			payload = map[string]json.RawMessage{"raw": mustJSON(a)}
		}
		if err := rec("investment_answer", a.LessonID, &a.ContentVersion, payload, limitInvestment); err != nil {
			return err
		}
	}
	if l, ok := snap.labs[s.ID]; ok {
		payload := json.RawMessage(l[0])
		if !json.Valid(payload) {
			payload = mustJSON(l[0])
		}
		if err := rec("lab_snapshot", s.ID, &l[1], payload, limitLab); err != nil {
			return err
		}
	}
	// Незавершённое окно старого D7 сохраняется до исходного closesAt; новые D7 не создаются.
	if s.FirstAnswerAt != nil && !slices.ContainsFunc(snap.answers[s.ID], func(a answer) bool { return a.Kind == "assessment" }) {
		first := parseTime(*s.FirstAnswerAt, time.Time{})
		if !first.IsZero() && now.Before(first.Add(d7Close)) {
			status := "waiting"
			if !now.Before(first.Add(d7Open)) {
				status = "available"
			}
			if err := rec("assessment_d7", s.ID, nil, map[string]any{
				"firstAnswerAt": first, "availableAt": first.Add(d7Open), "closesAt": first.Add(d7Close), "statusAtImport": status,
			}, limitD7); err != nil {
				return err
			}
		}
	}
	return nil
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// importMoney переносит бюджет и цель одним снимком плана revision 1. Возвращает id цели или "".
func (im *Importer) importMoney(ctx context.Context, tx pgx.Tx, snap *snapshot, s session, now time.Time, m *Manifest) (string, error) {
	var g *oldGoal
	if s.Goal != nil {
		g = &oldGoal{}
		if err := json.Unmarshal([]byte(*s.Goal), g); err != nil {
			return "", fmt.Errorf("goal: %w", err)
		}
	}
	raw, hasBudget := snap.budgets[s.ID]
	if g == nil && !hasBudget {
		return "", nil
	}
	var b oldBudget
	if hasBudget {
		if err := json.Unmarshal([]byte(raw), &b); err != nil {
			return "", fmt.Errorf("budget: %w", err)
		}
	} else {
		// Без журнала категорий прежние обязательные расходы цели попадают в other.
		b = oldBudget{Income: g.Income, Reserve: g.Reserve, Monthly: g.Monthly}
		b.Categories = append(b.Categories, struct {
			ID     string `json:"id"`
			Amount int64  `json:"amount"`
		}{"other", g.Essential})
	}
	cat := map[string]int64{}
	for _, c := range b.Categories {
		cat[c.ID] += c.Amount
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO money_plans (user_id, revision, income_minor, reserve_minor, monthly_contribution_minor,
		                         food_minor, transport_minor, housing_minor, other_minor, updated_at)
		VALUES ($1, 1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		s.ID, rub(b.Income), rub(b.Reserve), rub(b.Monthly), rub(cat["food"]), rub(cat["transport"]), rub(cat["housing"]), rub(cat["other"]), now); err != nil {
		return "", err
	}
	m.Imported["money_plans"]++
	if g == nil {
		return "", nil
	}
	title := strings.TrimSpace(g.Title)
	if title == "" || utf8.RuneCountInString(title) > 80 {
		title = "Цель"
	}
	months := min(max(g.Months, 1), 120)
	id := im.IDs.New("gol")
	if _, err := tx.Exec(ctx, `
		INSERT INTO goals (id, user_id, title, target_minor, saved_minor, months, active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, true, $7, $7)`, id, s.ID, title, max(rub(g.Target), 100), rub(g.Saved), months, now); err != nil {
		return "", err
	}
	m.Imported["goals"]++
	return id, nil
}
