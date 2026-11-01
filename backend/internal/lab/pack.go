package lab

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"teyljunior/internal/store"
)

// Option — вариант ответа.
type Option struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Reason — причина решения или сравнения со связью с условиями кейса.
type Reason struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Status string `json:"status"` // supported | misconception
}

// Probe — проверочная пара после расчёта сценария.
type Probe struct {
	Question     string      `json:"question"`
	Choices      []Option    `json:"choices"`
	Reasons      []Option    `json:"reasons"`
	AllowedPairs [][2]string `json:"allowedPairs"`
	Explanation  string      `json:"explanation"`
	Feedback     struct {
		Choice string `json:"choice"`
		Reason string `json:"reason"`
	} `json:"feedback"`
}

// Grade — действие и объяснение раздельно, passed — допустимая пара.
func (p Probe) Grade(choiceID, reasonID string) (choiceOK, reasonOK, passed bool) {
	for _, pr := range p.AllowedPairs {
		choiceOK = choiceOK || pr[0] == choiceID
		reasonOK = reasonOK || pr[1] == reasonID
		passed = passed || pr == [2]string{choiceID, reasonID}
	}
	return
}

// Case — авторский лабораторный кейс.
type Case struct {
	CaseFamilyID    string           `json:"caseFamilyId"`
	Revision        int              `json:"revision"`
	BundleRevision  int              `json:"bundleRevision"`
	ModelVersion    string           `json:"modelVersion"`
	Title           string           `json:"title"`
	Conditions      string           `json:"conditions"`
	InitialMinor    int64            `json:"initialMinor"`
	ReserveMinMinor int64            `json:"reserveMinMinor"`
	DecisionReasons []Reason         `json:"decisionReasons"`
	Probes          map[string]Probe `json:"probes"`
	Comparison      struct {
		KeepReasons   []Reason `json:"keepReasons"`
		ReviseReasons []Reason `json:"reviseReasons"`
	} `json:"comparison"`
}

func findReason(list []Reason, id string) (Reason, bool) {
	i := slices.IndexFunc(list, func(r Reason) bool { return r.ID == id })
	if i < 0 {
		return Reason{}, false
	}
	return list[i], true
}

// Pack — файл lab-case-LB01.json: расчётный пакет и кейс.
type Pack struct {
	Provenance string `json:"provenance"`
	Model      Model  `json:"model"`
	Case       Case   `json:"case"`
}

// ParsePack строго разбирает и проверяет пакет.
func ParsePack(data []byte) (*Pack, error) {
	var p Pack
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("lab pack: %w", err)
	}
	if errs := p.validate(); len(errs) > 0 {
		return nil, fmt.Errorf("lab pack is inconsistent:\n  %s", strings.Join(errs, "\n  "))
	}
	return &p, nil
}

func (p *Pack) validate() []string {
	var errs []string
	bad := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }
	m, c := p.Model, p.Case
	if m.Version == "" || m.UnitMinor <= 0 || m.Units <= 0 || m.FeeBps < 0 {
		bad("модель: нужны version, unitMinor, units и feeBps")
	}
	if len(m.Assets) != len(Assets) {
		bad("модель: ожидались корзины %v", Assets)
	}
	for _, sc := range m.Scenarios {
		if sc.Months <= 0 {
			bad("сценарий %s: months > 0", sc.ID)
		}
		for _, asset := range Assets {
			r, ok := sc.Rates[asset]
			if !ok {
				bad("сценарий %s: нет ставок для %s", sc.ID, asset)
			}
			if asset == "cash" && r != (Rate{}) {
				bad("сценарий %s: деньги без вложений не меняются", sc.ID)
			}
			if r.PriceBps <= -10000 {
				bad("сценарий %s: цена %s не может упасть до нуля и ниже", sc.ID, asset)
			}
		}
		probe, ok := c.Probes[sc.ID]
		if !ok {
			bad("кейс: нет проверочной пары для сценария %s", sc.ID)
			continue
		}
		if len(probe.AllowedPairs) == 0 || probe.Explanation == "" {
			bad("кейс: пара %s без допустимых ответов или разбора", sc.ID)
		}
		for _, pr := range probe.AllowedPairs {
			if !hasOption(probe.Choices, pr[0]) || !hasOption(probe.Reasons, pr[1]) {
				bad("кейс: пара %s ссылается на неизвестный вариант %v", sc.ID, pr)
			}
		}
	}
	if len(c.Probes) != len(m.Scenarios) {
		bad("кейс: проверочных пар %d, сценариев %d", len(c.Probes), len(m.Scenarios))
	}
	if c.ModelVersion != m.Version || c.CaseFamilyID == "" || c.Revision < 1 || c.BundleRevision < 1 {
		bad("кейс: нужны caseFamilyId, revision, bundleRevision и modelVersion модели")
	}
	if c.InitialMinor != int64(m.Units)*m.UnitMinor {
		bad("кейс: initialMinor должен равняться units × unitMinor")
	}
	if c.ReserveMinMinor < 0 || c.ReserveMinMinor > c.InitialMinor || c.ReserveMinMinor%m.UnitMinor != 0 {
		bad("кейс: reserveMinMinor — целое число долей в пределах капитала")
	}
	for _, list := range [][]Reason{c.DecisionReasons, c.Comparison.KeepReasons, c.Comparison.ReviseReasons} {
		supported := false
		for _, r := range list {
			if r.Status != "supported" && r.Status != "misconception" {
				bad("причина %s: статус supported или misconception", r.ID)
			}
			supported = supported || r.Status == "supported"
		}
		if !supported {
			bad("кейс: в каждом списке причин нужна хотя бы одна обоснованная")
		}
	}
	return errs
}

func hasOption(opts []Option, id string) bool {
	return slices.ContainsFunc(opts, func(o Option) bool { return o.ID == id })
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// Import записывает модель и кейс как draft. Модель неизменяема: та же версия с другим
// содержимым отклоняется; новый фактор — новая редакция модели и кейса (§11).
func Import(ctx context.Context, db *store.DB, p *Pack, now time.Time) error {
	model, err := json.Marshal(p.Model)
	if err != nil {
		return err
	}
	cs, err := json.Marshal(p.Case)
	if err != nil {
		return err
	}
	return db.InTx(ctx, func(tx pgx.Tx) error {
		var existing string
		if err := tx.QueryRow(ctx, `
			INSERT INTO lab_models (version, payload, sha256, imported_at) VALUES ($1, $2, $3, $4)
			ON CONFLICT (version) DO UPDATE SET version = EXCLUDED.version RETURNING sha256`,
			p.Model.Version, model, sha(model), now).Scan(&existing); err != nil {
			return err
		}
		if existing != sha(model) {
			return fmt.Errorf("модель %s уже существует с другим содержимым", p.Model.Version)
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO lab_cases (family_id, revision, model_version, bundle_revision, payload, sha256, status, imported_at)
			VALUES ($1, $2, $3, $4, $5, $6, 'draft', $7)
			ON CONFLICT (family_id, revision) DO UPDATE SET family_id = EXCLUDED.family_id RETURNING sha256`,
			p.Case.CaseFamilyID, p.Case.Revision, p.Model.Version, p.Case.BundleRevision, cs, sha(cs), now).Scan(&existing); err != nil {
			return err
		}
		if existing != sha(cs) {
			return fmt.Errorf("кейс %s r%d уже существует с другим содержимым", p.Case.CaseFamilyID, p.Case.Revision)
		}
		return nil
	})
}

// Publish публикует редакцию кейса после утверждения редактором.
func Publish(ctx context.Context, db *store.DB, familyID string, revision int, approvedBy string, now time.Time) error {
	if approvedBy == "" {
		return errors.New("публикация требует имени утвердившего редактора")
	}
	tag, err := db.Pool.Exec(ctx, `
		UPDATE lab_cases SET status = 'published', published_at = $3, approved_by = $4
		WHERE family_id = $1 AND revision = $2 AND status = 'draft'`, familyID, revision, now, approvedBy)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("кейс %s r%d не найден или уже опубликован", familyID, revision)
	}
	return nil
}
