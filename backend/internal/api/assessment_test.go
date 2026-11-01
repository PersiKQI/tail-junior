package api_test

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	bankfiles "teyljunior/content"
	"teyljunior/internal/content"
)

// key — верные пары карточек, нужных в тестах.
var key = map[string][2]string{
	"L01": {"fit", "deadline"}, "L03": {"terms", "early"}, "L09": {"minus24", "all"},
	"R02A": {"keep", "date"}, "R02B": {"minus18", "all"},
	"D01": {"available", "date"}, "D02": {"plus9", "net"}, "D03": {"check", "independent"},
}

// answerLearning отвечает на learning задание верно.
func (e *env) answerLearning(tok, itemID string) {
	e.t.Helper()
	id := e.startAttempt(tok, e.assignment(tok, itemID)["assignmentId"].(string))["attemptId"].(string)
	r := e.submit(tok, id, key[itemID][0], key[itemID][1])
	expect(e.t, r, http.StatusCreated, "")
}

func (e *env) submit(tok, attemptID, choice, reason string) resp {
	e.t.Helper()
	return e.do("POST", "/learning/attempts/"+attemptID+"/submission", tok, e.key(), map[string]any{"choiceId": choice, "reasonId": reason})
}

func (e *env) sets(tok string) resp {
	e.t.Helper()
	r := e.do("GET", "/assessment-sets", tok, "", nil)
	expect(e.t, r, http.StatusOK, "")
	return r
}

// set возвращает набор по слоту.
func (e *env) set(tok, slot string) map[string]any {
	e.t.Helper()
	for _, it := range e.sets(tok).json(e.t)["items"].([]any) {
		if s := it.(map[string]any); s["slotFamilyId"] == slot {
			return s
		}
	}
	e.t.Fatalf("set %s not found", slot)
	return nil
}

// setItem — assignmentId карточки набора по itemId (набор должен быть открыт).
func setItem(t *testing.T, set map[string]any, itemID string) string {
	t.Helper()
	for _, it := range set["items"].([]any) {
		if m := it.(map[string]any); m["itemId"] == itemID {
			return m["assignmentId"].(string)
		}
	}
	t.Fatalf("item %s not in set %v", itemID, set["setId"])
	return ""
}

var correctCount = regexp.MustCompile(`"correct":\d`)

// noLeak проверяет, что в ответе нет ни правильности, ни разбора (§9).
func noLeak(t *testing.T, where string, r resp) {
	t.Helper()
	body := string(r.body)
	for _, s := range []string{`"choiceCorrect"`, `"reasonCorrect"`, `"passed"`, `"explanation"`, `"result":{`, `"evaluation":{`} {
		if strings.Contains(body, s) {
			t.Fatalf("%s leaks %s: %s", where, s, body)
		}
	}
	if correctCount.MatchString(body) {
		t.Fatalf("%s leaks correct count: %s", where, body)
	}
}

func TestReviewSetLifecycle(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newUser()

	rs := e.set(tok, "slot-RS02")
	if rs["status"] != "waiting" || rs["dueAt"] != nil || rs["label"] != "не раньше чем через 2 дня" {
		t.Fatalf("initial RS02 = %v", rs)
	}
	for _, it := range rs["items"].([]any) {
		m := it.(map[string]any)
		if m["title"] != nil || m["itemId"] != nil || len(m["missingAnchors"].([]any)) != 1 {
			t.Fatalf("waiting item exposes content or lacks anchors: %v", m)
		}
	}
	if len(e.sets(tok).json(t)["items"].([]any)) != 3 {
		t.Fatal("want RS02, RS07, RS30")
	}

	e.answerLearning(tok, "L01")
	if e.set(tok, "slot-RS02")["dueAt"] != nil {
		t.Fatal("set due before both anchors")
	}
	e.clock.Advance(time.Hour)
	e.answerLearning(tok, "L09")
	wantDue := start.Add(49 * time.Hour)
	rs = e.set(tok, "slot-RS02")
	if rs["dueAt"] != wantDue.Format(time.RFC3339) || rs["status"] != "waiting" {
		t.Fatalf("RS02 after anchors = %v, want due %s", rs["dueAt"], wantDue)
	}

	// До срока карточки не выдаются и набор нельзя закончить.
	var waitingAsg string
	if err := e.db.Pool.QueryRow(t.Context(), `SELECT id FROM assignments WHERE item_id = 'R02A'`).Scan(&waitingAsg); err != nil {
		t.Fatal(err)
	}
	expect(t, e.do("POST", "/learning/attempts", tok, e.key(), map[string]any{"assignmentId": waitingAsg}), http.StatusConflict, "SET_NOT_AVAILABLE")
	expect(t, e.do("POST", "/assessment-sets/"+rs["setId"].(string)+"/finalization", tok, e.key(), map[string]any{}),
		http.StatusConflict, "SET_NOT_AVAILABLE")

	e.clock.Advance(48 * time.Hour)
	rs = e.set(tok, "slot-RS02")
	if rs["status"] != "available" {
		t.Fatalf("status at due = %v", rs["status"])
	}
	next := e.do("GET", "/next-step", tok, "", nil)
	if next.json(t)["kind"] != "review_set" {
		t.Fatalf("next-step = %s", next.body)
	}

	a := e.startAttempt(tok, setItem(t, rs, "R02A"))
	aID := a["attemptId"].(string)
	if a["card"].(map[string]any)["purpose"] != "review" {
		t.Fatalf("card = %v", a["card"])
	}
	expect(t, e.do("POST", "/learning/attempts/"+aID+"/support", tok, e.key(), map[string]any{"kind": "hint"}), http.StatusConflict, "SUPPORT_UNAVAILABLE")

	// Ошибочный первый ответ: принят, но оценка скрыта везде.
	r := e.submit(tok, aID, "fund", "date")
	expect(t, r, http.StatusCreated, "")
	noLeak(t, "receipt", r)
	as := r.json(t)["assessment"].(map[string]any)
	if as["answered"] != 1.0 || as["assigned"] != 2.0 || as["finalized"] != false {
		t.Fatalf("progress = %v", as)
	}
	noLeak(t, "attempt", e.do("GET", "/learning/attempts/"+aID, tok, "", nil))
	noLeak(t, "sets", e.sets(tok))
	noLeak(t, "bootstrap", e.do("GET", "/bootstrap", tok, "", nil))
	noLeak(t, "next-step", e.do("GET", "/next-step", tok, "", nil))
	noLeak(t, "resubmission", e.submit(tok, aID, "fund", "date"))

	// Последний ответ финализирует completed и раскрывает оценку.
	b := e.startAttempt(tok, setItem(t, rs, "R02B"))
	last := e.submit(tok, b["attemptId"].(string), "minus18", "all")
	expect(t, last, http.StatusCreated, "")
	lj := last.json(t)
	if lj["evaluation"].(map[string]any)["passed"] != true || lj["assessment"].(map[string]any)["status"] != "completed" {
		t.Fatalf("last receipt = %s", last.body)
	}

	rs = e.set(tok, "slot-RS02")
	sum := rs["summary"].(map[string]any)
	if rs["finalizedStatus"] != "completed" || sum["correct"] != 1.0 || sum["answered"] != 2.0 {
		t.Fatalf("finalized RS02 = %v", rs)
	}
	for _, it := range rs["items"].([]any) {
		m := it.(map[string]any)
		res := m["result"].(map[string]any)
		if m["itemId"] == "R02A" && (res["passed"] != false || res["actualDelaySeconds"] != float64(49*3600)) {
			t.Fatalf("R02A result = %v", res)
		}
	}
	if e.do("GET", "/learning/attempts/"+aID, tok, "", nil).json(t)["evaluation"] == nil {
		t.Fatal("evaluation still hidden after finalization")
	}
	expect(t, e.do("POST", "/learning/attempts", tok, e.key(), map[string]any{"assignmentId": setItem(t, rs, "R02A")}), http.StatusConflict, "SET_FINALIZED")

	// Задержка RS07 ещё не наступила: якорей нет.
	if s := e.set(tok, "slot-RS07"); s["status"] != "waiting" || s["dueAt"] != nil {
		t.Fatalf("RS07 = %v", s)
	}
}

func TestDiagnosticPartialAndSkipped(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newUser()

	d := e.do("POST", "/assessment-sets/diagnostic", tok, e.key(), map[string]any{})
	expect(t, d, http.StatusCreated, "")
	set := d.json(t)
	setID := set["setId"].(string)
	if set["status"] != "available" || len(set["items"].([]any)) != 3 {
		t.Fatalf("diagnostic = %v", set)
	}
	again := e.do("POST", "/assessment-sets/diagnostic", tok, e.key(), map[string]any{})
	expect(t, again, http.StatusOK, "")
	if again.json(t)["setId"] != setID {
		t.Fatal("second primary diagnostic created")
	}

	d1 := e.startAttempt(tok, setItem(t, set, "D01"))["attemptId"].(string)
	r := e.submit(tok, d1, "available", "date")
	expect(t, r, http.StatusCreated, "")
	noLeak(t, "diagnostic receipt", r)
	d2 := e.startAttempt(tok, setItem(t, set, "D02"))["attemptId"].(string)

	// Чужой набор неотличим от отсутствующего.
	other := e.newUser()
	expect(t, e.do("POST", "/assessment-sets/"+setID+"/finalization", other, e.key(), map[string]any{}), http.StatusNotFound, "NOT_FOUND")

	f := e.do("POST", "/assessment-sets/"+setID+"/finalization", tok, e.key(), map[string]any{})
	expect(t, f, http.StatusOK, "")
	fj := f.json(t)
	sum := fj["summary"].(map[string]any)
	if fj["finalizedStatus"] != "partial" || fj["baselineEligible"] != true || sum["answered"] != 1.0 || sum["skipped"] != 2.0 || sum["correct"] != 1.0 {
		t.Fatalf("partial = %v", fj)
	}
	// Поздний ответ не попадает в первичный набор; первая финализация неизменна.
	expect(t, e.submit(tok, d2, "plus9", "net"), http.StatusConflict, "SET_FINALIZED")
	f2 := e.do("POST", "/assessment-sets/"+setID+"/finalization", tok, e.key(), map[string]any{})
	if f2.json(t)["finalizedAt"] != fj["finalizedAt"] || f2.json(t)["finalizedStatus"] != "partial" {
		t.Fatal("finalization changed on repeat")
	}
	if e.do("POST", "/assessment-sets/diagnostic", tok, e.key(), map[string]any{}).json(t)["setId"] != setID {
		t.Fatal("diagnostic recreated after partial")
	}
	// После финализации повтор принятого ответа уже раскрывает оценку.
	if rc := e.submit(tok, d1, "available", "date").json(t); rc["evaluation"] == nil {
		t.Fatal("evaluation hidden after finalization")
	}

	// Без ответов — skipped; пропуск не считается ошибкой.
	tok2 := e.newUser()
	s2 := e.do("POST", "/assessment-sets/diagnostic", tok2, e.key(), map[string]any{}).json(t)
	sk := e.do("POST", "/assessment-sets/"+s2["setId"].(string)+"/finalization", tok2, e.key(), map[string]any{}).json(t)
	if sk["finalizedStatus"] != "skipped" || sk["summary"].(map[string]any)["correct"] != 0.0 {
		t.Fatalf("skipped = %v", sk)
	}

	// Обучение до финализации — не чистый входной замер.
	tok3 := e.newUser()
	e.answerLearning(tok3, "L01")
	s3 := e.do("POST", "/assessment-sets/diagnostic", tok3, e.key(), map[string]any{}).json(t)
	var final map[string]any
	for _, item := range []string{"D01", "D02", "D03"} {
		id := e.startAttempt(tok3, setItem(t, s3, item))["attemptId"].(string)
		final = e.submit(tok3, id, key[item][0], key[item][1]).json(t)
	}
	if final["assessment"].(map[string]any)["status"] != "completed" {
		t.Fatalf("auto finalization = %v", final)
	}
	if got := e.set(tok3, "slot-D"); got["baselineEligible"] != false || got["summary"].(map[string]any)["correct"] != 3.0 {
		t.Fatalf("diagnostic after learning = %v", got)
	}
}

// Ручная финализация и последний ответ одновременно: порядок задаёт блокировка набора, и
// проигравший не получает «первый результат» (§21).
func TestFinalizeRacesLastAnswer(t *testing.T) {
	e := newEnv(t, true)
	for range 5 {
		tok := e.newUser()
		set := e.do("POST", "/assessment-sets/diagnostic", tok, e.key(), map[string]any{}).json(t)
		for _, item := range []string{"D01", "D02"} {
			id := e.startAttempt(tok, setItem(t, set, item))["attemptId"].(string)
			expect(t, e.submit(tok, id, key[item][0], key[item][1]), http.StatusCreated, "")
		}
		d3 := e.startAttempt(tok, setItem(t, set, "D03"))["attemptId"].(string)

		var wg sync.WaitGroup
		var ans, fin resp
		wg.Go(func() { ans = e.submit(tok, d3, "check", "independent") })
		wg.Go(func() {
			fin = e.do("POST", "/assessment-sets/"+set["setId"].(string)+"/finalization", tok, e.key(), map[string]any{})
		})
		wg.Wait()

		got := e.set(tok, "slot-D")
		switch ans.status {
		case http.StatusCreated:
			if got["finalizedStatus"] != "completed" || fin.json(t)["finalizedStatus"] != "completed" {
				t.Fatalf("answer won but set = %v, fin = %s", got["finalizedStatus"], fin.body)
			}
		case http.StatusConflict:
			if ans.code(t) != "SET_FINALIZED" || got["finalizedStatus"] != "partial" {
				t.Fatalf("finalization won but answer %s, set %v", ans.body, got["finalizedStatus"])
			}
		default:
			t.Fatalf("answer = %d %s", ans.status, ans.body)
		}
	}
}

func TestNextStepOrder(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newUser()
	n := e.do("GET", "/next-step", tok, "", nil).json(t)
	boot := e.do("GET", "/bootstrap", tok, "", nil).json(t)["progress"].(map[string]any)
	if n["kind"] != "learning" || n["assignmentId"] != boot["nextAssignmentId"] {
		t.Fatalf("next = %v, bootstrap next = %v", n, boot["nextAssignmentId"])
	}
	att := e.startAttempt(tok, n["assignmentId"].(string))
	if n2 := e.do("GET", "/next-step", tok, "", nil).json(t); n2["kind"] != "continue_attempt" || n2["attemptId"] != att["attemptId"] {
		t.Fatalf("next with active attempt = %v", n2)
	}
}

// bundleV2 — та же программа с новой редакцией L03 в новой поставке.
func bundleV2(t *testing.T) *content.Bundle {
	t.Helper()
	var p, a map[string]any
	if err := json.Unmarshal(bankfiles.Program, &p); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(bankfiles.Adapter, &a); err != nil {
		t.Fatal(err)
	}
	const v2 = "teyl-learning-2026-11-02-draft-2"
	p["programVersion"], a["programVersion"] = v2, v2
	for _, it := range p["items"].([]any) {
		m := it.(map[string]any)
		if m["itemId"] == "L03" {
			m["revision"] = 2
			m["public"].(map[string]any)["example"] = "Исправленный пример: вклад на 6 месяцев, деньги могут понадобиться через 2 месяца."
		}
	}
	pj, _ := json.Marshal(p)
	aj, _ := json.Marshal(a)
	b, err := content.ParseBundle(pj, aj)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestWithdrawRevision(t *testing.T) {
	e := newEnv(t, true)
	a, b, c := e.newUser(), e.newUser(), e.newUser()
	aAtt := e.startAttempt(a, e.assignment(a, "L03")["assignmentId"].(string))["attemptId"].(string)

	e.answerLearning(c, "L01")
	e.answerLearning(c, "L09")
	e.clock.Advance(49 * time.Hour)
	cAtt := e.startAttempt(c, setItem(t, e.set(c, "slot-RS02"), "R02A"))["attemptId"].(string)

	v2 := bundleV2(t)
	if err := content.Import(t.Context(), e.db, v2, e.clock.Now()); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(time.Minute)
	if err := content.Publish(t.Context(), e.db, v2.Program.ProgramVersion, "test-editor", e.clock.Now()); err != nil {
		t.Fatal(err)
	}

	rep, err := e.services.Learning.WithdrawRevision(t.Context(), e.db, content.RevisionKey{ItemID: "L03", Revision: 1, GradingRevision: 1, Locale: "ru-RU"},
		"неверная ставка в примере", e.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Users != 3 || rep.AttemptsWithdrawn != 1 || rep.AssignmentsReplaced != 3 {
		t.Fatalf("report = %+v", rep)
	}

	// Незавершённая попытка отозвана без оценки; замена — новая редакция.
	expect(t, e.submit(a, aAtt, "terms", "early"), http.StatusGone, "CONTENT_WITHDRAWN")
	var replacement map[string]any
	for _, it := range e.do("GET", "/learning/assignments", a, "", nil).json(t)["items"].([]any) {
		m := it.(map[string]any)
		if m["itemId"] == "L03" && m["status"] == "open" {
			replacement = m
		}
	}
	if replacement == nil {
		t.Fatal("no replacement assignment")
	}
	natt := e.startAttempt(a, replacement["assignmentId"].(string))
	if natt["revision"] != 2.0 || !strings.HasPrefix(natt["card"].(map[string]any)["example"].(string), "Исправленный") {
		t.Fatalf("replacement attempt = %v", natt)
	}
	if p := e.do("GET", "/bootstrap", b, "", nil).json(t)["progress"].(map[string]any); p["learningTotal"] != 24.0 {
		t.Fatalf("learningTotal after replacement = %v", p["learningTotal"])
	}

	// Отзыв карточки защищённого набора делает весь набор invalidated, записи остаются.
	if _, err := e.services.Learning.WithdrawRevision(t.Context(), e.db, content.RevisionKey{ItemID: "R02A", Revision: 1, GradingRevision: 1, Locale: "ru-RU"},
		"ошибка в условии", e.clock.Now()); err != nil {
		t.Fatal(err)
	}
	rs := e.set(c, "slot-RS02")
	if rs["status"] != "invalidated" || rs["invalidationReason"] != "ошибка в условии" {
		t.Fatalf("RS02 after withdrawal = %v", rs)
	}
	expect(t, e.submit(c, cAtt, "keep", "date"), http.StatusConflict, "SET_INVALIDATED")
	if _, err := e.services.Learning.WithdrawRevision(t.Context(), e.db, content.RevisionKey{ItemID: "R02A", Revision: 1, GradingRevision: 1, Locale: "ru-RU"},
		"повтор", e.clock.Now()); err == nil {
		t.Fatal("second withdrawal must fail")
	}
}
