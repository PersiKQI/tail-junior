// Пакет content — учебный банк: разбор и проверка комплекта (банк + адаптер), импорт в
// редакторскую базу, публикация поставки и чтение опубликованных редакций (спецификация §6).
// Публичная проекция карточки собирается явным allowlist (§20): приватные поля — ключи,
// подсказка, объяснения — в неё не попадают.
package content

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Назначения карточек.
const (
	PurposeLearning   = "learning"
	PurposeDiagnostic = "diagnostic"
	PurposeReview     = "review"
)

// Ожидаемый состав банка первого релиза (§6, §7).
const (
	wantLearning   = 24
	wantDiagnostic = 3
	wantReview     = 6
	wantTerms      = 12
	wantStarter    = 12
)

// Option — вариант действия или объяснения.
type Option struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Source — первичный учебный материал.
type Source struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// PublicCard — содержимое карточки, которое разрешено отдавать клиенту.
type PublicCard struct {
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle"`
	Concept  string   `json:"concept"`
	Example  string   `json:"example"`
	Question string   `json:"question"`
	Choices  []Option `json:"choices"`
	Reasons  []Option `json:"reasons"`
	TermIDs  []string `json:"termIds"`
	Sources  []Source `json:"sources"`
}

// Feedback — адресные пояснения при ошибке в действии или объяснении.
type Feedback struct {
	Choice string `json:"choice"`
	Reason string `json:"reason"`
}

// PrivateCard — серверная часть карточки из банка.
type PrivateCard struct {
	AcceptedChoiceIDs []string `json:"acceptedChoiceIds"`
	AcceptedReasonIDs []string `json:"acceptedReasonIds"`
	Hint              string   `json:"hint,omitempty"`
	Explanation       string   `json:"explanation"`
	Feedback          Feedback `json:"feedback"`
}

// Item — карточка банка.
type Item struct {
	ItemID            string      `json:"itemId"`
	FamilyID          string      `json:"familyId"`
	Purpose           string      `json:"purpose"`
	ModuleID          string      `json:"moduleId"`
	Revision          int         `json:"revision"`
	GradingRevision   int         `json:"gradingRevision"`
	TopicTags         []string    `json:"topicTags"`
	AssessedOutcomeID string      `json:"assessedOutcomeId,omitempty"`
	AnchorLessonIDs   []string    `json:"anchorLessonIds,omitempty"`
	Public            PublicCard  `json:"public"`
	Private           PrivateCard `json:"private"`
}

type Module struct {
	ID    string `json:"id"`
	Order int    `json:"order"`
	Title string `json:"title"`
}

type Route struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	FamilyIDs []string `json:"familyIds"`
}

type Term struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Definition string `json:"definition"`
}

type ReviewSet struct {
	ID           string   `json:"id"`
	SlotFamilyID string   `json:"slotFamilyId"`
	DelayDays    int      `json:"delayDays"`
	Label        string   `json:"label"`
	ItemIDs      []string `json:"itemIds"`
}

type Diagnostic struct {
	SlotFamilyID string   `json:"slotFamilyId"`
	ItemIDs      []string `json:"itemIds"`
}

// Program — файл learning-program-server-draft.json.
type Program struct {
	ProgramVersion    string          `json:"programVersion"`
	LineageID         string          `json:"lineageId"`
	Locale            string          `json:"locale"`
	PublicationStatus string          `json:"publicationStatus"`
	Provenance        json.RawMessage `json:"provenance"`
	Modules           []Module        `json:"modules"`
	Routes            []Route         `json:"routes"`
	Terms             []Term          `json:"terms"`
	ReviewSets        []ReviewSet     `json:"reviewSets"`
	Diagnostic        Diagnostic      `json:"diagnostic"`
	Items             []Item          `json:"items"`
}

// Pair — допустимая пара «действие + объяснение».
type Pair [2]string

type AdapterItem struct {
	AllowedPairs []Pair `json:"allowedPairs"`
	Note         string `json:"note,omitempty"`
}

type Outcome struct {
	ID                  string   `json:"id"`
	Title               string   `json:"title"`
	DiagnosticItemIDs   []string `json:"diagnosticItemIds"`
	ReviewItemIDs       []string `json:"reviewItemIds"`
	PracticeMarkItemIDs []string `json:"practiceMarkItemIds"`
	BadgeID             *string  `json:"badgeId"`
}

// Adapter — файл content-adapter.json: явные допустимые пары и проверяемые исходы.
type Adapter struct {
	AdapterVersion string                 `json:"adapterVersion"`
	ProgramVersion string                 `json:"programVersion"`
	Provenance     string                 `json:"provenance"`
	Items          map[string]AdapterItem `json:"items"`
	Outcomes       []Outcome              `json:"outcomes"`
}

// Grading — приватная часть редакции в базе: ключ, пары и тексты разбора.
type Grading struct {
	AllowedPairs      []Pair   `json:"allowedPairs"`
	AcceptedChoiceIDs []string `json:"acceptedChoiceIds"`
	AcceptedReasonIDs []string `json:"acceptedReasonIds"`
	Hint              string   `json:"hint,omitempty"`
	Explanation       string   `json:"explanation"`
	Feedback          Feedback `json:"feedback"`
}

// Grade оценивает выбор: действие и объяснение проверяются независимо, passed — совпадение
// допустимой пары (§6, §8).
func (g Grading) Grade(choiceID, reasonID string) (choiceOK, reasonOK, passed bool) {
	return slices.Contains(g.AcceptedChoiceIDs, choiceID),
		slices.Contains(g.AcceptedReasonIDs, reasonID),
		slices.Contains(g.AllowedPairs, Pair{choiceID, reasonID})
}

// Manifest — публичное описание поставки, хранится в program_releases.manifest.
type Manifest struct {
	ProgramVersion string          `json:"programVersion"`
	LineageID      string          `json:"lineageId"`
	Locale         string          `json:"locale"`
	AdapterVersion string          `json:"adapterVersion"`
	Provenance     json.RawMessage `json:"provenance"`
	Modules        []Module        `json:"modules"`
	Routes         []Route         `json:"routes"`
	Terms          []Term          `json:"terms"`
	ReviewSets     []ReviewSet     `json:"reviewSets"`
	Diagnostic     Diagnostic      `json:"diagnostic"`
	Outcomes       []Outcome       `json:"outcomes"`
	// AnchorFamilies — для каждой review-карточки семейства learning, от первого принятия
	// которых отсчитывается её срок (§9).
	AnchorFamilies map[string][]string `json:"anchorFamilies"`
}

// ReviewSet возвращает отложенный набор по слоту.
func (m *Manifest) ReviewSet(slotFamilyID string) (ReviewSet, bool) {
	for _, rs := range m.ReviewSets {
		if rs.SlotFamilyID == slotFamilyID {
			return rs, true
		}
	}
	return ReviewSet{}, false
}

// Route возвращает маршрут по id.
func (m *Manifest) Route(id string) (Route, bool) {
	for _, r := range m.Routes {
		if r.ID == id {
			return r, true
		}
	}
	return Route{}, false
}

// Term возвращает термин по id.
func (m *Manifest) Term(id string) (Term, bool) {
	for _, t := range m.Terms {
		if t.ID == id {
			return t, true
		}
	}
	return Term{}, false
}

// Bundle — проверенный комплект для импорта.
type Bundle struct {
	Program Program
	Adapter Adapter
	SHA256  string // хеш банка и адаптера вместе
}

// Manifest собирает манифест поставки.
func (b *Bundle) Manifest() Manifest {
	p := b.Program
	family := map[string]string{}
	for _, it := range p.Items {
		family[it.ItemID] = it.FamilyID
	}
	anchors := map[string][]string{}
	for _, it := range p.Items {
		for _, a := range it.AnchorLessonIDs {
			anchors[it.ItemID] = append(anchors[it.ItemID], family[a])
		}
	}
	return Manifest{
		ProgramVersion: p.ProgramVersion, LineageID: p.LineageID, Locale: p.Locale,
		AdapterVersion: b.Adapter.AdapterVersion, Provenance: p.Provenance,
		Modules: p.Modules, Routes: p.Routes, Terms: p.Terms, ReviewSets: p.ReviewSets,
		Diagnostic: p.Diagnostic, Outcomes: b.Adapter.Outcomes, AnchorFamilies: anchors,
	}
}

// GradingOf — приватная часть карточки с явными парами из адаптера.
func (b *Bundle) GradingOf(it Item) Grading {
	return Grading{
		AllowedPairs:      b.Adapter.Items[it.ItemID].AllowedPairs,
		AcceptedChoiceIDs: it.Private.AcceptedChoiceIDs,
		AcceptedReasonIDs: it.Private.AcceptedReasonIDs,
		Hint:              it.Private.Hint,
		Explanation:       it.Private.Explanation,
		Feedback:          it.Private.Feedback,
	}
}

// ParseBundle строго разбирает банк и адаптер и проверяет их согласованность.
func ParseBundle(programJSON, adapterJSON []byte) (*Bundle, error) {
	var b Bundle
	if err := strictUnmarshal(programJSON, &b.Program); err != nil {
		return nil, fmt.Errorf("program: %w", err)
	}
	if err := strictUnmarshal(adapterJSON, &b.Adapter); err != nil {
		return nil, fmt.Errorf("adapter: %w", err)
	}
	h := sha256.New()
	h.Write(programJSON)
	h.Write([]byte{0})
	h.Write(adapterJSON)
	b.SHA256 = hex.EncodeToString(h.Sum(nil))
	if errs := b.validate(); len(errs) > 0 {
		return nil, fmt.Errorf("content bundle is inconsistent:\n  %s", strings.Join(errs, "\n  "))
	}
	return &b, nil
}

func strictUnmarshal(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data after JSON value")
	}
	return nil
}

// validate возвращает все найденные несогласованности, а не только первую: редактору удобнее
// исправить комплект за один проход.
func (b *Bundle) validate() []string {
	var errs []string
	bad := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }
	p, a := b.Program, b.Adapter

	if p.ProgramVersion == "" || p.LineageID == "" || p.Locale == "" {
		bad("programVersion, lineageId и locale обязательны")
	}
	if a.ProgramVersion != p.ProgramVersion {
		bad("адаптер %q относится к другой поставке, ожидалась %q", a.ProgramVersion, p.ProgramVersion)
	}

	modules := map[string]bool{}
	for _, m := range p.Modules {
		modules[m.ID] = true
	}
	terms := map[string]bool{}
	for _, t := range p.Terms {
		if t.ID == "" || t.Title == "" || t.Definition == "" || terms[t.ID] {
			bad("термин %q пустой или повторяется", t.ID)
		}
		terms[t.ID] = true
	}
	if len(p.Terms) != wantTerms {
		bad("терминов %d, ожидалось %d", len(p.Terms), wantTerms)
	}

	items := map[string]Item{}
	families := map[string]bool{}
	counts := map[string]int{}
	for _, it := range p.Items {
		if _, dup := items[it.ItemID]; dup || it.ItemID == "" {
			bad("itemId %q пустой или повторяется", it.ItemID)
		}
		if families[it.FamilyID] || it.FamilyID == "" {
			bad("%s: familyId %q пустой или повторяется", it.ItemID, it.FamilyID)
		}
		items[it.ItemID] = it
		families[it.FamilyID] = true
		counts[it.Purpose]++
		errs = append(errs, b.validateItem(it, modules, terms)...)
	}
	for purpose, want := range map[string]int{PurposeLearning: wantLearning, PurposeDiagnostic: wantDiagnostic, PurposeReview: wantReview} {
		if counts[purpose] != want {
			bad("карточек %s: %d, ожидалось %d", purpose, counts[purpose], want)
		}
	}
	for id := range a.Items {
		if _, ok := items[id]; !ok {
			bad("адаптер описывает неизвестную карточку %s", id)
		}
	}

	learningFamilies := map[string]bool{}
	for _, it := range p.Items {
		if it.Purpose == PurposeLearning {
			learningFamilies[it.FamilyID] = true
		}
	}
	routeSize := map[string]int{}
	for _, r := range p.Routes {
		seen := map[string]bool{}
		for _, f := range r.FamilyIDs {
			if !learningFamilies[f] || seen[f] {
				bad("маршрут %s: семейство %q не learning или повторяется", r.ID, f)
			}
			seen[f] = true
		}
		routeSize[r.ID] = len(r.FamilyIDs)
	}
	if routeSize["starter"] != wantStarter || routeSize["full"] != wantLearning || len(p.Routes) != 2 {
		bad("маршруты: нужны starter из %d и full из %d заданий", wantStarter, wantLearning)
	}

	inSlot := map[string]bool{}
	for _, rs := range p.ReviewSets {
		if rs.DelayDays <= 0 || rs.SlotFamilyID == "" || len(rs.ItemIDs) != 2 {
			bad("набор %s: нужен положительный delayDays, slotFamilyId и пара карточек", rs.ID)
		}
		for _, id := range rs.ItemIDs {
			if items[id].Purpose != PurposeReview || inSlot[id] {
				bad("набор %s: %s не review или уже в другом наборе", rs.ID, id)
			}
			inSlot[id] = true
		}
	}
	for _, id := range p.Diagnostic.ItemIDs {
		if items[id].Purpose != PurposeDiagnostic {
			bad("диагностика: %s не diagnostic", id)
		}
	}
	for _, it := range p.Items {
		if it.Purpose == PurposeReview {
			if !inSlot[it.ItemID] {
				bad("%s: review не входит ни в один набор", it.ItemID)
			}
			if len(it.AnchorLessonIDs) == 0 {
				bad("%s: у review нет anchorLessonIds", it.ItemID)
			}
			for _, anchor := range it.AnchorLessonIDs {
				if items[anchor].Purpose != PurposeLearning {
					bad("%s: якорь %s не learning", it.ItemID, anchor)
				}
			}
		}
	}

	outcomes := map[string]bool{}
	for _, o := range a.Outcomes {
		outcomes[o.ID] = true
		for _, id := range slices.Concat(o.DiagnosticItemIDs, o.ReviewItemIDs, o.PracticeMarkItemIDs) {
			if _, ok := items[id]; !ok {
				bad("исход %s ссылается на неизвестную карточку %s", o.ID, id)
			}
		}
	}
	for _, it := range p.Items {
		if it.Purpose != PurposeLearning && !outcomes[it.AssessedOutcomeID] {
			bad("%s: assessedOutcomeId %q не описан в адаптере", it.ItemID, it.AssessedOutcomeID)
		}
	}
	return errs
}

func (b *Bundle) validateItem(it Item, modules, terms map[string]bool) []string {
	var errs []string
	bad := func(format string, args ...any) {
		errs = append(errs, it.ItemID+": "+fmt.Sprintf(format, args...))
	}
	switch it.Purpose {
	case PurposeLearning, PurposeDiagnostic, PurposeReview:
	default:
		bad("неизвестное назначение %q", it.Purpose)
	}
	if !modules[it.ModuleID] {
		bad("неизвестный модуль %q", it.ModuleID)
	}
	if it.Revision < 1 || it.GradingRevision < 1 {
		bad("revision и gradingRevision начинаются с 1")
	}
	pc := it.Public
	if pc.Title == "" || pc.Question == "" || pc.Example == "" {
		bad("пустой title, question или example")
	}
	if it.Purpose == PurposeLearning && (pc.Concept == "" || it.Private.Hint == "") {
		bad("у learning нужны concept и hint")
	}
	if it.Private.Explanation == "" {
		bad("пустое explanation")
	}
	choices, ok1 := optionIDs(pc.Choices)
	reasons, ok2 := optionIDs(pc.Reasons)
	if !ok1 || !ok2 || len(choices) < 2 || len(reasons) < 2 {
		bad("варианты пустые, повторяются или их меньше двух")
	}
	for _, t := range pc.TermIDs {
		if !terms[t] {
			bad("неизвестный термин %q", t)
		}
	}

	ai, ok := b.Adapter.Items[it.ItemID]
	if !ok || len(ai.AllowedPairs) == 0 {
		bad("в адаптере нет допустимых пар: декартово произведение автоматически не берётся")
		return errs
	}
	pairChoices, pairReasons := map[string]bool{}, map[string]bool{}
	for _, pr := range ai.AllowedPairs {
		if !choices[pr[0]] || !reasons[pr[1]] {
			bad("пара %v ссылается на неизвестный вариант", pr)
		}
		pairChoices[pr[0]], pairReasons[pr[1]] = true, true
	}
	if !sameSet(pairChoices, it.Private.AcceptedChoiceIDs) || !sameSet(pairReasons, it.Private.AcceptedReasonIDs) {
		bad("пары адаптера не совпадают с допустимыми наборами банка")
	}
	return errs
}

func optionIDs(opts []Option) (map[string]bool, bool) {
	ids := map[string]bool{}
	for _, o := range opts {
		if o.ID == "" || o.Text == "" || ids[o.ID] {
			return nil, false
		}
		ids[o.ID] = true
	}
	return ids, true
}

func sameSet(set map[string]bool, list []string) bool {
	if len(set) != len(list) {
		return false
	}
	for _, v := range list {
		if !set[v] {
			return false
		}
	}
	return true
}
