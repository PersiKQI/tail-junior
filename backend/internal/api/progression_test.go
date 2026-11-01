package api_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

func init() {
	for id, pair := range map[string][2]string{
		"L02": {"keep", "access"}, "L05": {"rest", "protect"}, "L07": {"yes", "market"}, "L10": {"zero", "net"},
		"L13": {"no", "decision"}, "L14": {"normal", "market"}, "L15": {"weak", "sector"}, "L18": {"cash", "volatility"},
		"L19": {"stop", "signs"}, "L21": {"verify", "independent"}, "L24": {"limits", "assumptions"},
	} {
		key[id] = pair
	}
}

func (e *env) progress(tok string) map[string]any {
	e.t.Helper()
	r := e.do("GET", "/progress", tok, "", nil)
	expect(e.t, r, http.StatusOK, "")
	return r.json(e.t)
}

func participation(p map[string]any) map[string]any { return p["participation"].(map[string]any) }
func activity(p map[string]any) map[string]any      { return p["activity"].(map[string]any) }

func badges(p map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, b := range p["badges"].([]any) {
		m := b.(map[string]any)
		out[m["badgeId"].(string)] = m
	}
	return out
}

// answerWith отвечает на learning задание заданной парой.
func (e *env) answerWith(tok, itemID, choice, reason string) map[string]any {
	e.t.Helper()
	id := e.startAttempt(tok, e.assignment(tok, itemID)["assignmentId"].(string))["attemptId"].(string)
	r := e.submit(tok, id, choice, reason)
	expect(e.t, r, http.StatusCreated, "")
	return r.json(e.t)
}

func (e *env) post(tok, path string, body any, status int) map[string]any {
	e.t.Helper()
	r := e.do("POST", path, tok, e.key(), body)
	expect(e.t, r, status, "")
	return r.json(e.t)
}

func TestLabFullCase(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newUser()

	cases := e.do("GET", "/lab/cases", tok, "", nil)
	expect(t, cases, http.StatusOK, "")
	for _, secret := range []string{"priceBps", "allowedPairs", "misconception", "explanation", "endingMinor"} {
		if strings.Contains(string(cases.body), secret) {
			t.Fatalf("cases leak %s before decision: %s", secret, cases.body)
		}
	}
	exp := e.post(tok, "/lab/experiments", map[string]any{"caseFamilyId": "lab-LB01"}, http.StatusCreated)
	expID := exp["experimentId"].(string)
	again := e.post(tok, "/lab/experiments", map[string]any{"caseFamilyId": "lab-LB01"}, http.StatusOK)
	if again["experimentId"] != expID {
		t.Fatal("open experiment duplicated")
	}
	base := "/lab/experiments/" + expID

	expect(t, e.do("POST", base+"/runs", tok, e.key(), map[string]any{"decisionId": "ldc_missing", "scenarioId": "growth"}), http.StatusNotFound, "NOT_FOUND")
	expect(t, e.do("POST", base+"/decisions", tok, e.key(), map[string]any{"allocation": map[string]any{"cash": 9, "bond": 0, "stock": 0, "fund": 0}, "reasonId": "keep_access"}),
		http.StatusUnprocessableEntity, "INVALID_ALLOCATION")
	expect(t, e.do("POST", base+"/decisions", tok, e.key(), map[string]any{"allocation": map[string]any{"cash": 10, "bond": 0, "stock": 0, "fund": 0}, "reasonId": "nope"}),
		http.StatusUnprocessableEntity, "UNKNOWN_OPTION")

	// Резерв нарушен и причина ошибочна — решение всё равно сохраняется и разбирается.
	orig := e.post(tok, base+"/decisions", map[string]any{
		"allocation": map[string]any{"cash": 2, "bond": 3, "stock": 3, "fund": 2}, "reasonId": "fund_guaranteed", "note": "  попробую  ",
	}, http.StatusCreated)
	origID := orig["decisionId"].(string)
	if orig["kind"] != "original" || orig["constraintStatus"] != "reserve_violated" || orig["reasonStatus"] != "misconception" || orig["note"] != "попробую" {
		t.Fatalf("original = %v", orig)
	}
	expect(t, e.do("POST", base+"/comparison", tok, e.key(), map[string]any{"outcome": "keep", "reasonId": "fits_goal"}), http.StatusConflict, "COMPARISON_NOT_READY")

	want := map[string]float64{"growth": 1033336, "down": 902598, "early": 955486}
	runs := map[string]string{}
	for _, sc := range []string{"growth", "down", "early"} {
		run := e.post(tok, base+"/runs", map[string]any{"decisionId": origID, "scenarioId": sc}, http.StatusCreated)
		if run["result"].(map[string]any)["endingMinor"] != want[sc] {
			t.Fatalf("%s ending = %v, want %v", sc, run["result"].(map[string]any)["endingMinor"], want[sc])
		}
		if _, leaks := run["probe"].(map[string]any)["allowedPairs"]; leaks || run["reflection"] != nil {
			t.Fatalf("run exposes key or reflection: %v", run["probe"])
		}
		runs[sc] = run["runId"].(string)
	}
	if rep := e.post(tok, base+"/runs", map[string]any{"decisionId": origID, "scenarioId": "growth"}, http.StatusOK); rep["runId"] != runs["growth"] {
		t.Fatal("second run for the same decision and scenario")
	}
	if _, ok := badges(e.progress(tok))["three_scenarios"]; !ok {
		t.Fatal("three_scenarios badge missing")
	}

	ok := e.post(tok, "/lab/runs/"+runs["growth"]+"/reflection", map[string]any{"choiceId": "scenario_only", "reasonId": "authored"}, http.StatusCreated)
	if ok["reflection"].(map[string]any)["passed"] != true {
		t.Fatalf("growth reflection = %v", ok["reflection"])
	}
	wrong := e.post(tok, "/lab/runs/"+runs["down"]+"/reflection", map[string]any{"choiceId": "coupon_safe", "reasonId": "sum"}, http.StatusCreated)
	if rf := wrong["reflection"].(map[string]any); rf["passed"] != false || rf["choiceFeedback"] == nil || rf["reasonCorrect"] != true {
		t.Fatalf("down reflection = %v", rf)
	}
	e.post(tok, "/lab/runs/"+runs["down"]+"/reflection", map[string]any{"choiceId": "coupon_safe", "reasonId": "sum"}, http.StatusOK)
	expect(t, e.do("POST", "/lab/runs/"+runs["down"]+"/reflection", tok, e.key(), map[string]any{"choiceId": "total", "reasonId": "sum"}),
		http.StatusConflict, "REFLECTION_EXISTS")
	e.post(tok, "/lab/runs/"+runs["early"]+"/reflection", map[string]any{"choiceId": "horizon", "reasonId": "access"}, http.StatusCreated)

	alt := e.post(tok, base+"/decisions", map[string]any{
		"allocation": map[string]any{"cash": 4, "bond": 0, "stock": 0, "fund": 6}, "reasonId": "explore_with_reserve",
	}, http.StatusCreated)
	altID := alt["decisionId"].(string)
	if alt["kind"] != "alternative" || alt["sourceDecisionId"] != origID || alt["constraintStatus"] != "met" {
		t.Fatalf("alternative = %v", alt)
	}
	expect(t, e.do("POST", base+"/comparison", tok, e.key(), map[string]any{"outcome": "revise", "reasonId": "protect_reserve", "alternativeDecisionId": altID}),
		http.StatusConflict, "COMPARISON_NOT_READY")
	for _, sc := range []string{"growth", "down", "early"} {
		e.post(tok, base+"/runs", map[string]any{"decisionId": altID, "scenarioId": sc}, http.StatusCreated)
	}
	expect(t, e.do("POST", base+"/comparison", tok, e.key(), map[string]any{"outcome": "keep", "reasonId": "fits_goal", "alternativeDecisionId": altID}),
		http.StatusUnprocessableEntity, "VALIDATION_FAILED")

	cmp := e.post(tok, base+"/comparison", map[string]any{"outcome": "revise", "reasonId": "protect_reserve", "alternativeDecisionId": altID}, http.StatusCreated)
	rows := cmp["rows"].([]any)
	first := rows[0].(map[string]any)
	if len(rows) != 3 || first["original"].(map[string]any)["endingMinor"] != 1033336.0 || first["alternative"].(map[string]any)["endingMinor"] != 1022754.0 {
		t.Fatalf("comparison = %v", cmp)
	}
	e.post(tok, base+"/comparison", map[string]any{"outcome": "revise", "reasonId": "protect_reserve", "alternativeDecisionId": altID}, http.StatusOK)
	expect(t, e.do("POST", base+"/comparison", tok, e.key(), map[string]any{"outcome": "keep", "reasonId": "fits_goal"}), http.StatusConflict, "COMPARISON_EXISTS")
	expect(t, e.do("POST", base+"/decisions", tok, e.key(), map[string]any{"allocation": map[string]any{"cash": 10, "bond": 0, "stock": 0, "fund": 0}, "reasonId": "keep_access"}),
		http.StatusConflict, "EXPERIMENT_COMPLETED")

	ex := e.do("GET", base, tok, "", nil).json(t)
	if ex["status"] != "completed" || ex["completion"].(map[string]any)["complete"] != true || len(ex["decisions"].([]any)) != 2 || len(ex["runs"].([]any)) != 6 {
		t.Fatalf("experiment = %v", ex)
	}
	p := participation(e.progress(tok))
	if p["xp"] != 40.0 || p["labCasesCompleted"] != 1.0 {
		t.Fatalf("participation after lab = %v", p)
	}

	// Второй полный эксперимент того же кейса базового XP не даёт.
	exp2 := e.post(tok, "/lab/experiments", map[string]any{"caseFamilyId": "lab-LB01"}, http.StatusCreated)
	b2 := "/lab/experiments/" + exp2["experimentId"].(string)
	d2 := e.post(tok, b2+"/decisions", map[string]any{"allocation": map[string]any{"cash": 10, "bond": 0, "stock": 0, "fund": 0}, "reasonId": "keep_access"}, http.StatusCreated)
	for sc, pair := range map[string][2]string{"growth": {"scenario_only", "authored"}, "down": {"total", "sum"}, "early": {"horizon", "access"}} {
		run := e.post(tok, b2+"/runs", map[string]any{"decisionId": d2["decisionId"], "scenarioId": sc}, http.StatusCreated)
		if run["result"].(map[string]any)["endingMinor"] != 1000000.0 {
			t.Fatalf("all cash changed in %s", sc)
		}
		e.post(tok, "/lab/runs/"+run["runId"].(string)+"/reflection", map[string]any{"choiceId": pair[0], "reasonId": pair[1]}, http.StatusCreated)
	}
	e.post(tok, b2+"/comparison", map[string]any{"outcome": "keep", "reasonId": "fits_goal"}, http.StatusCreated)
	if p := participation(e.progress(tok)); p["xp"] != 40.0 {
		t.Fatalf("second case gave XP: %v", p["xp"])
	}
}

func TestXPRolesBadgesAndEvidence(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newUser()

	// Самостоятельный верный ответ из mapping — практическая отметка исхода.
	e.answerLearning(tok, "L09")
	p := e.progress(tok)
	b := badges(p)
	if participation(p)["xp"] != 20.0 || b["first_review"] == nil || b["whole_result"]["confirmation"] != "practice" {
		t.Fatalf("after L09: xp %v badges %v", participation(p)["xp"], b)
	}

	// Ответ с подсказкой не даёт отметку; ошибка не уменьшает XP.
	att := e.startAttempt(tok, e.assignment(tok, "L01")["assignmentId"].(string))["attemptId"].(string)
	e.post(tok, "/learning/attempts/"+att+"/support", map[string]any{"kind": "hint"}, http.StatusOK)
	expect(t, e.submit(tok, att, "fit", "deadline"), http.StatusCreated, "")
	if _, ok := badges(e.progress(tok))["money_by_deadline"]; ok {
		t.Fatal("badge granted for answer with hint")
	}
	e.answerWith(tok, "L02", "all", "growth")
	p = e.progress(tok)
	if pa := participation(p); pa["xp"] != 60.0 || pa["role"].(map[string]any)["id"] != "explorer" {
		t.Fatalf("after 3 families: %v", pa)
	}
	e.answerLearning(tok, "L18")
	if badges(e.progress(tok))["money_by_deadline"] == nil {
		t.Fatal("practice mark from second mapped item missing")
	}

	// Ручной повтор XP не даёт.
	pr := e.post(tok, "/learning/practice-assignments", map[string]any{"familyId": "f-L09"}, http.StatusCreated)
	pa := e.startAttempt(tok, pr["assignmentId"].(string))["attemptId"].(string)
	expect(t, e.submit(tok, pa, "minus24", "all"), http.StatusCreated, "")
	if x := participation(e.progress(tok))["xp"]; x != 80.0 {
		t.Fatalf("xp after manual practice = %v", x)
	}

	// Review: XP, свидетельства и подтверждения — только с финализацией пары.
	e.clock.Advance(49 * time.Hour)
	rs := e.set(tok, "slot-RS02")
	a := e.startAttempt(tok, setItem(t, rs, "R02A"))["attemptId"].(string)
	expect(t, e.submit(tok, a, "keep", "date"), http.StatusCreated, "")
	p = e.progress(tok)
	if participation(p)["xp"] != 80.0 || len(p["results"].(map[string]any)["evidence"].([]any)) != 0 || badges(p)["later_confirmed"] != nil {
		t.Fatalf("review rewards before finalization: %v", p)
	}
	// Верный ответ до финализации не попадает в число верных: correct считается только по
	// финализированным наборам.
	if cov := p["results"].(map[string]any)["review"].(map[string]any); cov["answered"] != 1.0 || cov["correct"] != 0.0 {
		t.Fatalf("coverage before finalization = %v", cov)
	}

	b2 := e.startAttempt(tok, setItem(t, rs, "R02B"))["attemptId"].(string)
	expect(t, e.submit(tok, b2, "minus18", "all"), http.StatusCreated, "")
	p = e.progress(tok)
	ev := p["results"].(map[string]any)["evidence"].([]any)
	if participation(p)["xp"] != 140.0 || len(ev) != 2 {
		t.Fatalf("after RS02: xp %v evidence %v", participation(p)["xp"], ev)
	}
	var texts []string
	for _, x := range ev {
		texts = append(texts, x.(map[string]any)["text"].(string))
	}
	if !slices.Contains(texts, "Получилось в новой задаче: учесть расходы в результате") {
		t.Fatalf("evidence texts = %v", texts)
	}
	b = badges(p)
	if b["later_confirmed"] == nil || b["whole_result"]["confirmedLaterAt"] == nil || b["whole_result"]["confirmation"] != "practice" {
		t.Fatalf("badges after evidence = %v", b)
	}
	cov := p["results"].(map[string]any)["review"].(map[string]any)
	if cov["assigned"] != 6.0 || cov["answered"] != 2.0 || cov["correct"] != 2.0 || cov["pending"] != 4.0 {
		t.Fatalf("coverage = %v", cov)
	}
	var grants int
	if err := e.db.Pool.QueryRow(t.Context(), `SELECT count(*) FROM badge_grants`).Scan(&grants); err != nil || grants != len(b) {
		t.Fatalf("badge rows %d vs view %d: %v", grants, len(b), err)
	}
}

func TestStreakAndProtection(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newUser()

	// Открытие приложения день не закрывает.
	e.do("GET", "/bootstrap", tok, "", nil)
	if a := activity(e.progress(tok)); a["todayStatus"] != "pending" || a["totalActiveDays"] != 0.0 || a["timezoneLocked"] != false {
		t.Fatalf("before answer = %v", a)
	}
	e.answerLearning(tok, "L01") // 1 ноября, 13:00 по Москве
	a := activity(e.progress(tok))
	if a["todayStatus"] != "active" || a["currentActiveDays"] != 1.0 || a["freezeCharges"] != 1.0 ||
		a["timezone"] != "Europe/Moscow" || a["timezoneLocked"] != true || a["today"] != "2026-11-01" {
		t.Fatalf("day 1 = %v", a)
	}
	rev := e.do("GET", "/bootstrap", tok, "", nil).json(t)["profile"].(map[string]any)["revision"]
	expect(t, e.do("PATCH", "/profile", tok, e.key(), map[string]any{"expectedRevision": rev, "learningTimezone": "Asia/Tokyo"}),
		http.StatusConflict, "TIMEZONE_LOCKED")

	// 2 ноября пропущен, 3 ноября: день оплачен зарядом.
	e.clock.Advance(48 * time.Hour)
	a = activity(e.progress(tok))
	if a["currentActiveDays"] != 1.0 || a["currentProtectedDays"] != 1.0 || a["freezeCharges"] != 0.0 || a["todayStatus"] != "pending" {
		t.Fatalf("day 3 before answer = %v", a)
	}
	if a2 := activity(e.progress(tok)); a2["freezeCharges"] != 0.0 || a2["currentProtectedDays"] != 1.0 {
		t.Fatalf("repeat settle spent twice: %v", a2)
	}
	e.answerLearning(tok, "L02")
	if a = activity(e.progress(tok)); a["currentActiveDays"] != 2.0 || a["currentProtectedDays"] != 1.0 {
		t.Fatalf("day 3 = %v", a)
	}

	// 4 и 5 ноября без зарядов — цепочка закончилась, лучший результат сохранён.
	e.clock.Advance(72 * time.Hour)
	if a = activity(e.progress(tok)); a["currentActiveDays"] != 0.0 || a["bestActiveDays"] != 2.0 {
		t.Fatalf("after break = %v", a)
	}
	// Граница суток по учебному поясу: 21:30 UTC — уже 7 ноября в Москве.
	e.clock.Advance(11*time.Hour + 30*time.Minute)
	if a = activity(e.progress(tok)); a["today"] != "2026-11-07" {
		t.Fatalf("local today = %v", a["today"])
	}
	e.answerLearning(tok, "L05")
	if a = activity(e.progress(tok)); a["currentActiveDays"] != 1.0 || a["totalActiveDays"] != 3.0 || a["freezeCharges"] != 0.0 {
		t.Fatalf("new chain = %v", a)
	}
	var protected int
	if err := e.db.Pool.QueryRow(t.Context(), `SELECT count(*) FROM study_days WHERE kind = 'protected'`).Scan(&protected); err != nil || protected != 1 {
		t.Fatalf("protected days = %d, %v", protected, err)
	}
}

func TestFreezeRefillCap(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newUser()
	for i, item := range []string{"L01", "L02", "L03", "L04", "L05", "L06", "L07"} {
		if i > 0 {
			e.clock.Advance(24 * time.Hour)
		}
		choice, reason := anyChoice(e, tok, item)
		e.answerWith(tok, item, choice, reason)
	}
	a := activity(e.progress(tok))
	if a["currentActiveDays"] != 7.0 || a["freezeCharges"] != 2.0 {
		t.Fatalf("after 7 days = %v", a)
	}
	e.clock.Advance(48 * time.Hour)
	if a = activity(e.progress(tok)); a["freezeCharges"] != 1.0 || a["currentProtectedDays"] != 1.0 {
		t.Fatalf("after one missed day = %v", a)
	}
}

// anyChoice — любая допустимая пара из карточки: для серии правильность не нужна. Попытка,
// начатая здесь, затем продолжается в answerWith.
func anyChoice(e *env, tok, item string) (string, string) {
	e.t.Helper()
	if k, ok := key[item]; ok {
		return k[0], k[1]
	}
	att := e.startAttempt(tok, e.assignment(tok, item)["assignmentId"].(string))
	card := att["card"].(map[string]any)
	return card["choices"].([]any)[0].(map[string]any)["id"].(string), card["reasons"].([]any)[0].(map[string]any)["id"].(string)
}

func TestScheduledRepeat(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newUser()
	for _, item := range []string{"L01", "L02", "L05", "L07", "L09", "L10", "L13", "L14", "L15", "L19", "L21", "L24"} {
		if item == "L05" {
			e.answerWith(tok, item, "all", "later") // известная ошибка
			continue
		}
		e.answerLearning(tok, item)
	}
	if n := e.do("GET", "/next-step", tok, "", nil).json(t); n["kind"] == "repeat" {
		t.Fatal("repeat scheduled within 24 hours of the last answer")
	}
	e.clock.Advance(25 * time.Hour)
	n := e.do("GET", "/next-step", tok, "", nil).json(t)
	if n["kind"] != "repeat" {
		t.Fatalf("next-step = %v", n)
	}
	asg := n["assignmentId"].(string)
	var family, origin string
	var streak bool
	if err := e.db.Pool.QueryRow(t.Context(), `SELECT family_id, practice_origin, streak_eligible FROM assignments WHERE id = $1`, asg).
		Scan(&family, &origin, &streak); err != nil {
		t.Fatal(err)
	}
	if family != "f-L05" || origin != "scheduled" || !streak {
		t.Fatalf("repeat = %s %s %v, want known error f-L05", family, origin, streak)
	}
	if again := e.do("GET", "/next-step", tok, "", nil).json(t); again["assignmentId"] != asg {
		t.Fatal("second open repeat created")
	}

	xp := participation(e.progress(tok))["xp"]
	id := e.startAttempt(tok, asg)["attemptId"].(string)
	expect(t, e.submit(tok, id, "rest", "protect"), http.StatusCreated, "")
	p := e.progress(tok)
	if participation(p)["xp"] != xp || activity(p)["todayStatus"] != "active" {
		t.Fatalf("repeat: xp %v → %v, today %v", xp, participation(p)["xp"], activity(p)["todayStatus"])
	}
	// Та же тема не назначается раньше чем через 24 часа.
	next := e.do("GET", "/next-step", tok, "", nil).json(t)
	if err := e.db.Pool.QueryRow(t.Context(), `SELECT family_id FROM assignments WHERE id = $1`, next["assignmentId"]).Scan(&family); err != nil {
		t.Fatal(err)
	}
	if next["kind"] != "repeat" || family == "f-L05" {
		t.Fatalf("next repeat = %v (%s)", next, family)
	}
}
