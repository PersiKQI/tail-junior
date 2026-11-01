package api_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// examplePlan — пример приложения В: income 10 000 ₽, категории 6 000 ₽, резерв 1 000 ₽,
// взнос 2 500 ₽; цель 12 000 ₽, накоплено 2 000 ₽, 4 месяца.
func examplePlan(rev int, goal map[string]any) map[string]any {
	return map[string]any{
		"expectedRevision": rev, "incomeMinor": 1_000_000, "reserveMinor": 100_000, "monthlyContributionMinor": 250_000,
		"categories": map[string]any{"foodMinor": 300_000, "transportMinor": 100_000, "housingMinor": 150_000, "otherMinor": 50_000},
		"goal":       goal,
	}
}

func newGoal(title string, saved int) map[string]any {
	return map[string]any{"title": title, "targetMinor": 1_200_000, "savedMinor": saved, "months": 4}
}

func (e *env) put(tok, path string, body any, status int) map[string]any {
	e.t.Helper()
	r := e.do("PUT", path, tok, e.key(), body)
	expect(e.t, r, status, "")
	return r.json(e.t)
}

func TestMoneyPlan(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newUser()
	empty := e.do("GET", "/money/plan", tok, "", nil).json(t)
	if empty["revision"] != 0.0 || empty["goal"] != nil {
		t.Fatalf("empty plan = %v", empty)
	}
	p := e.put(tok, "/money/plan", examplePlan(0, newGoal("  Велосипед ", 200_000)), http.StatusOK)
	g := p["goal"].(map[string]any)
	if p["revision"] != 1.0 || p["availableMinor"] != 300_000.0 || p["unallocatedMinor"] != 50_000.0 || p["essentialMinor"] != 600_000.0 {
		t.Fatalf("plan = %v", p)
	}
	if g["title"] != "Велосипед" || g["requiredMonthlyMinor"] != 250_000.0 || g["projectedMinor"] != 1_200_000.0 || g["planFeasible"] != true || g["suggestedMonths"] != 4.0 {
		t.Fatalf("goal = %v", g)
	}
	goalID := g["goalId"].(string)

	expect(t, e.do("PUT", "/money/plan", tok, e.key(), examplePlan(0, nil)), http.StatusConflict, "REVISION_CONFLICT")
	bad := examplePlan(1, newGoal("x", 0))
	delete(bad["categories"].(map[string]any), "otherMinor")
	expect(t, e.do("PUT", "/money/plan", tok, e.key(), bad), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	expect(t, e.do("PUT", "/money/plan", tok, e.key(), examplePlan(1, map[string]any{"title": "x", "targetMinor": 100, "savedMinor": 0, "months": 121})),
		http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	expect(t, e.do("PUT", "/money/plan", tok, e.key(), examplePlan(1, map[string]any{"goalId": "gol_other", "title": "x", "targetMinor": 100, "savedMinor": 0, "months": 1})),
		http.StatusConflict, "GOAL_CHANGED")

	upd := newGoal("Велосипед и шлем", 300_000)
	upd["goalId"] = goalID
	p = e.put(tok, "/money/plan", examplePlan(1, upd), http.StatusOK)
	if p["goal"].(map[string]any)["goalId"] != goalID || p["revision"] != 2.0 {
		t.Fatalf("goal edit = %v", p)
	}
	p = e.put(tok, "/money/plan", examplePlan(2, newGoal("Ноутбук", 0)), http.StatusOK)
	if p["goal"].(map[string]any)["goalId"] == goalID {
		t.Fatal("new goal kept old id")
	}
	// Отрицательный остаток — допустимое состояние плана, а не ошибка формата.
	neg := examplePlan(3, nil)
	neg["incomeMinor"] = 100_000
	p = e.put(tok, "/money/plan", neg, http.StatusOK)
	if p["cashMinor"] != -600_000.0 || p["availableMinor"] != 0.0 || p["goal"] != nil {
		t.Fatalf("negative plan = %v", p)
	}
}

func TestExpensesJournal(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newUser()
	expect(t, e.do("POST", "/money/expenses", tok, e.key(), map[string]any{"amountMinor": 50_000, "categoryId": "food", "occurredOn": "2026-11-02"}),
		http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	expect(t, e.do("POST", "/money/expenses", tok, e.key(), map[string]any{"amountMinor": 50_000, "categoryId": "games", "occurredOn": "2026-11-01"}),
		http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	ids := []string{}
	for i, d := range []string{"2026-10-30", "2026-10-31", "2026-11-01"} {
		x := e.post(tok, "/money/expenses", map[string]any{"amountMinor": 10_000 * (i + 1), "categoryId": "food", "occurredOn": d, "note": "обед"}, http.StatusCreated)
		ids = append(ids, x["expenseId"].(string))
	}
	v := e.do("PATCH", "/money/expenses/"+ids[1], tok, e.key(), map[string]any{"expectedRevision": 1, "voided": true})
	expect(t, v, http.StatusOK, "")
	if v.json(t)["voidedAt"] == nil || v.json(t)["revision"] != 2.0 {
		t.Fatalf("voided = %s", v.body)
	}
	expect(t, e.do("PATCH", "/money/expenses/"+ids[1], tok, e.key(), map[string]any{"expectedRevision": 1, "voided": false}), http.StatusConflict, "REVISION_CONFLICT")

	oct := e.do("GET", "/money/expenses?month=2026-10", tok, "", nil).json(t)
	if oct["totalsMinor"].(map[string]any)["food"] != 10_000.0 || len(oct["items"].([]any)) != 2 {
		t.Fatalf("october = %v", oct)
	}
	page := e.do("GET", "/money/expenses?limit=2", tok, "", nil).json(t)
	items := page["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["occurredOn"] != "2026-11-01" || page["nextCursor"] == nil {
		t.Fatalf("page 1 = %v", page)
	}
	page2 := e.do("GET", "/money/expenses?limit=2&cursor="+page["nextCursor"].(string), tok, "", nil).json(t)
	if len(page2["items"].([]any)) != 1 || page2["nextCursor"] != nil {
		t.Fatalf("page 2 = %v", page2)
	}
	expect(t, e.do("GET", "/money/expenses?limit=101", tok, "", nil), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	// Самоотчёт о расходах не закрывает учебный день и не даёт XP.
	if p := e.progress(tok); activity(p)["todayStatus"] != "pending" || participation(p)["xp"] != 0.0 {
		t.Fatalf("expenses affected progress: %v", p)
	}
}

func TestCheckinsAndActions(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newUser()
	expect(t, e.do("POST", "/money/checkins", tok, e.key(), map[string]any{"kind": "saved", "amountMinor": 50_000}), http.StatusConflict, "NO_ACTIVE_GOAL")
	e.put(tok, "/money/plan", examplePlan(0, newGoal("Велосипед", 200_000)), http.StatusOK)

	e.post(tok, "/money/checkins", map[string]any{"kind": "saved", "amountMinor": 50_000, "note": "из подарка"}, http.StatusCreated)
	expect(t, e.do("POST", "/money/checkins", tok, e.key(), map[string]any{"kind": "reviewed"}), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	e.post(tok, "/money/checkins", map[string]any{"kind": "reviewed", "reviewedFields": []string{"budget"}}, http.StatusCreated)
	plan := e.do("GET", "/money/plan", tok, "", nil).json(t)
	if plan["goal"].(map[string]any)["savedMinor"] != 250_000.0 || plan["revision"] != 2.0 {
		t.Fatalf("plan after checkin = %v", plan)
	}

	expect(t, e.do("POST", "/money/actions", tok, e.key(), map[string]any{"kind": "save", "amountMinor": 100_000, "dueOn": "2026-12-02"}),
		http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	save := e.post(tok, "/money/actions", map[string]any{"kind": "save", "amountMinor": 100_000, "dueOn": "2026-11-04", "note": "копилка"}, http.StatusCreated)
	done := e.post(tok, "/money/actions/"+save["actionId"].(string)+"/completion", map[string]any{}, http.StatusOK)
	if done["status"] != "completed" || done["checkinId"] == nil {
		t.Fatalf("completed = %v", done)
	}
	again := e.post(tok, "/money/actions/"+save["actionId"].(string)+"/completion", map[string]any{}, http.StatusOK)
	if again["checkinId"] != done["checkinId"] {
		t.Fatal("second completion created another checkin")
	}
	expect(t, e.do("POST", "/money/actions/"+save["actionId"].(string)+"/cancellation", tok, e.key(), map[string]any{}), http.StatusConflict, "ACTION_COMPLETED")
	plan = e.do("GET", "/money/plan", tok, "", nil).json(t)
	if plan["goal"].(map[string]any)["savedMinor"] != 350_000.0 {
		t.Fatalf("saved after action = %v", plan["goal"])
	}
	if c := e.do("GET", "/money/checkins", tok, "", nil).json(t)["items"].([]any); len(c) != 3 {
		t.Fatalf("checkins = %d", len(c))
	}

	// Цель сменилась — действие не пополняет новую цель незаметно.
	review := e.post(tok, "/money/actions", map[string]any{"kind": "review", "dueOn": "2026-11-01"}, http.StatusCreated)
	e.put(tok, "/money/plan", examplePlan(int(plan["revision"].(float64)), newGoal("Ноутбук", 0)), http.StatusOK)
	expect(t, e.do("POST", "/money/actions/"+review["actionId"].(string)+"/completion", tok, e.key(), map[string]any{}), http.StatusConflict, "GOAL_CHANGED")
	if c := e.post(tok, "/money/actions/"+review["actionId"].(string)+"/cancellation", map[string]any{}, http.StatusOK); c["status"] != "cancelled" {
		t.Fatalf("cancel = %v", c)
	}
	acts := e.do("GET", "/money/actions", tok, "", nil).json(t)["items"].([]any)
	if len(acts) != 2 {
		t.Fatalf("actions = %v", acts)
	}
	if p := e.progress(tok); activity(p)["todayStatus"] != "pending" || participation(p)["xp"] != 0.0 {
		t.Fatalf("money actions affected progress: %v", p)
	}
}

func (e *env) completeCase(tok, expID string) {
	e.t.Helper()
	base := "/lab/experiments/" + expID
	d := e.post(tok, base+"/decisions", map[string]any{"allocation": map[string]any{"cash": 6, "bond": 2, "stock": 0, "fund": 2}, "reasonId": "explore_with_reserve"}, http.StatusCreated)
	for _, sc := range [][3]string{{"early", "horizon", "access"}, {"growth", "scenario_only", "authored"}, {"down", "total", "sum"}} {
		run := e.post(tok, base+"/runs", map[string]any{"decisionId": d["decisionId"], "scenarioId": sc[0]}, http.StatusCreated)
		e.post(tok, "/lab/runs/"+run["runId"].(string)+"/reflection", map[string]any{"choiceId": sc[1], "reasonId": sc[2]}, http.StatusCreated)
	}
	e.post(tok, base+"/comparison", map[string]any{"outcome": "keep", "reasonId": "fits_goal"}, http.StatusCreated)
}

func cosmetic(inv map[string]any, id string) map[string]any {
	for _, it := range inv["items"].([]any) {
		if m := it.(map[string]any); m["cosmeticId"] == id {
			return m
		}
	}
	return nil
}

func TestChallengeAndCosmetics(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newUser()
	inv := e.do("GET", "/inventory", tok, "", nil).json(t)
	if cosmetic(inv, "bg-default")["unlocked"] != true || cosmetic(inv, "bg-sunrise")["unlocked"] != false || inv["revision"] != 1.0 {
		t.Fatalf("initial inventory = %v", inv)
	}
	expect(t, e.do("PUT", "/inventory/equipment", tok, e.key(), map[string]any{"expectedRevision": 1, "equipment": map[string]any{"background": "bg-sunrise"}}),
		http.StatusUnprocessableEntity, "COSMETIC_LOCKED")
	e.answerLearning(tok, "L01")
	if cosmetic(e.do("GET", "/inventory", tok, "", nil).json(t), "bg-sunrise")["unlocked"] != true {
		t.Fatal("badge cosmetic not unlocked")
	}

	list := e.do("GET", "/challenges", tok, "", nil).json(t)["items"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["phase"] != "upcoming" {
		t.Fatalf("challenges = %v", list)
	}
	expect(t, e.do("POST", "/challenges/evt-money-early/participation", tok, e.key(), map[string]any{}), http.StatusConflict, "CHALLENGE_NOT_STARTED")
	expect(t, e.do("GET", "/challenges/evt-money-early/participation", tok, "", nil), http.StatusNotFound, "NOT_FOUND")

	e.clock.Set(time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC))
	joined := e.post(tok, "/challenges/evt-money-early/participation", map[string]any{}, http.StatusCreated)
	if joined["phase"] != "live" {
		t.Fatalf("joined = %v", joined)
	}
	expID := joined["participation"].(map[string]any)["experimentId"].(string)
	for _, ep := range joined["episodes"].([]any) {
		if ep.(map[string]any)["done"] != false {
			t.Fatalf("episode done before work: %v", ep)
		}
	}
	e.completeCase(tok, expID)
	got := e.do("GET", "/challenges/evt-money-early/participation", tok, "", nil).json(t)
	if got["participation"].(map[string]any)["completedAt"] == nil {
		t.Fatalf("participation not completed: %v", got)
	}
	again := e.post(tok, "/challenges/evt-money-early/participation", map[string]any{}, http.StatusOK)
	if again["participation"].(map[string]any)["experimentId"] != expID {
		t.Fatal("rejoin created new participation")
	}

	inv = e.do("GET", "/inventory", tok, "", nil).json(t)
	if cosmetic(inv, "acc-early-ribbon")["unlocked"] != true {
		t.Fatal("event cosmetic locked after completion")
	}
	eq := e.put(tok, "/inventory/equipment", map[string]any{"expectedRevision": 1, "equipment": map[string]any{"accessory": "acc-early-ribbon", "background": "bg-sunrise"}}, http.StatusOK)
	if eq["revision"] != 2.0 || eq["equipment"].(map[string]any)["accessory"] != "acc-early-ribbon" {
		t.Fatalf("equipment = %v", eq)
	}
	expect(t, e.do("PUT", "/inventory/equipment", tok, e.key(), map[string]any{"expectedRevision": 1, "equipment": map[string]any{"accessory": nil}}),
		http.StatusConflict, "REVISION_CONFLICT")
	if xp := participation(e.progress(tok))["xp"]; xp != 60.0 {
		t.Fatalf("event added XP beyond base sources: %v", xp)
	}

	// Кейс, выполненный вне события, засчитывается и в архиве — с той же наградой.
	tok2 := e.newUser()
	exp := e.post(tok2, "/lab/experiments", map[string]any{"caseFamilyId": "lab-LB01"}, http.StatusCreated)
	e.completeCase(tok2, exp["experimentId"].(string))
	e.clock.Set(time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC))
	late := e.post(tok2, "/challenges/evt-money-early/participation", map[string]any{}, http.StatusCreated)
	if late["phase"] != "archived" || late["participation"].(map[string]any)["completedAt"] == nil || late["participation"].(map[string]any)["experimentId"] != exp["experimentId"] {
		t.Fatalf("archive join = %v", late)
	}
}

// oldSchema — CREATE из server/store.ts tail-junior.
const oldSchema = `
CREATE TABLE sessions(id TEXT PRIMARY KEY,token_hash TEXT NOT NULL UNIQUE,profile TEXT NOT NULL,goal TEXT,first_answer_at TEXT);
CREATE TABLE answers(session_id TEXT NOT NULL,kind TEXT NOT NULL,content_id TEXT NOT NULL,choice_id TEXT NOT NULL,reason_id TEXT,correct INTEGER NOT NULL,created_at TEXT NOT NULL,content_version TEXT NOT NULL,PRIMARY KEY(session_id,kind,content_id));
CREATE TABLE checkins(id TEXT PRIMARY KEY,session_id TEXT NOT NULL,type TEXT NOT NULL,amount INTEGER,note TEXT NOT NULL,created_at TEXT NOT NULL,idempotency_key TEXT);
CREATE TABLE events(id INTEGER PRIMARY KEY AUTOINCREMENT,session_id TEXT NOT NULL,name TEXT NOT NULL,content_id TEXT,created_at TEXT NOT NULL,content_version TEXT NOT NULL);
CREATE TABLE budgets(session_id TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE actions(id TEXT PRIMARY KEY,session_id TEXT NOT NULL,type TEXT NOT NULL,amount INTEGER,note TEXT NOT NULL,due_date TEXT NOT NULL,completed_at TEXT,created_at TEXT NOT NULL,idempotency_key TEXT);
CREATE TABLE expenses(id TEXT PRIMARY KEY,session_id TEXT NOT NULL,amount INTEGER NOT NULL,category TEXT NOT NULL,occurred_on TEXT NOT NULL,note TEXT NOT NULL,voided_at TEXT,created_at TEXT NOT NULL,idempotency_key TEXT);
CREATE TABLE investment_answers(session_id TEXT NOT NULL,lesson_id TEXT NOT NULL,first_attempt TEXT NOT NULL,latest_attempt TEXT NOT NULL,content_version TEXT NOT NULL,PRIMARY KEY(session_id,lesson_id));
CREATE TABLE investment_labs(session_id TEXT PRIMARY KEY,value TEXT NOT NULL,content_version TEXT NOT NULL);
`

func oldHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func TestLegacyImport(t *testing.T) {
	e := newEnv(t, true)
	dir := t.TempDir()
	path := filepath.Join(dir, "teyl.sqlite")
	src, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	tokA := strings.Repeat("A", 43)
	firstAnswer := start.Add(-150 * time.Hour).Format(time.RFC3339)
	for _, stmt := range []string{
		oldSchema,
		`INSERT INTO sessions VALUES ('11111111-1111-4111-8111-111111111111', '` + oldHash(tokA) + `',
		 '{"name":"Миша","ageBand":"21-22","onboardingCompleted":true,"reducedMotion":true,"character":"spark","focus":"goal","focusUpdatedAt":"2026-10-30T10:00:00.000Z","createdAt":"2026-10-20T10:00:00.000Z"}',
		 '{"title":"Велосипед","target":12000,"saved":2000,"months":4,"monthly":2500,"income":10000,"essential":6000,"reserve":1000}', '` + firstAnswer + `')`,
		`INSERT INTO sessions VALUES ('22222222-2222-4222-8222-222222222222', '` + oldHash(strings.Repeat("B", 43)) + `',
		 '{"name":"","ageBand":"14-17","onboardingCompleted":false,"reducedMotion":false,"character":null,"focus":null,"focusUpdatedAt":null,"createdAt":"2026-10-25T10:00:00.000Z"}', NULL, NULL)`,
		`INSERT INTO sessions VALUES ('33333333-3333-4333-8333-333333333333', 'not-hex', '{"ageBand":"other"}', NULL, NULL)`,
		`INSERT INTO sessions VALUES ('44444444-4444-4444-8444-444444444444', '` + oldHash(strings.Repeat("D", 43)) + `',
		 '{"ageBand":"18-20","createdAt":"2026-10-31T10:00:00.000Z"}', NULL, '` + start.Add(-10*time.Hour).Format(time.RFC3339) + `')`,
		`INSERT INTO budgets VALUES ('11111111-1111-4111-8111-111111111111', '{"income":10000,"reserve":1000,"monthly":2500,"categories":[{"id":"food","amount":3000},{"id":"transport","amount":1000},{"id":"housing","amount":1500},{"id":"other","amount":500}]}')`,
		`INSERT INTO answers VALUES ('11111111-1111-4111-8111-111111111111','lesson','budget-1','a','r',1,'2026-10-26T10:00:00.000Z','savings-v1')`,
		`INSERT INTO investment_answers VALUES ('11111111-1111-4111-8111-111111111111','horizon','{"choiceId":"max","reasonId":"past","correct":false}','{"choiceId":"fit","reasonId":"deadline","correct":true}','investment-2026-10-31-v1')`,
		`INSERT INTO investment_labs VALUES ('11111111-1111-4111-8111-111111111111','{"allocation":{"cash":10,"bond":0,"stock":0,"fund":0}}','investment-2026-10-31-v1')`,
		`INSERT INTO checkins VALUES ('c1','11111111-1111-4111-8111-111111111111','saved',500,'подарок','2026-10-27T10:00:00.000Z',NULL)`,
		`INSERT INTO checkins VALUES ('c2','11111111-1111-4111-8111-111111111111','reviewed',NULL,'','2026-10-28T10:00:00.000Z',NULL)`,
		`INSERT INTO actions VALUES ('a1','11111111-1111-4111-8111-111111111111','save',300,'','2026-11-05',NULL,'2026-10-29T10:00:00.000Z',NULL)`,
		`INSERT INTO actions VALUES ('a2','11111111-1111-4111-8111-111111111111','review',NULL,'','2026-10-30','2026-10-30T12:00:00.000Z','2026-10-29T10:00:00.000Z',NULL)`,
		`INSERT INTO expenses VALUES ('x1','11111111-1111-4111-8111-111111111111',450,'food','2026-10-29','обед','2026-10-29T15:00:00.000Z','2026-10-29T12:00:00.000Z',NULL)`,
		`INSERT INTO events (session_id,name,created_at,content_version) VALUES ('11111111-1111-4111-8111-111111111111','session_created','2026-10-20T10:00:00.000Z','v1')`,
	} {
		if _, err := src.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt[:40], err)
		}
	}
	src.Close()

	backups := filepath.Join(dir, "backups")
	m, err := e.services.Legacy.Run(t.Context(), path, backups)
	if err != nil {
		t.Fatal(err)
	}
	if m.Read["sessions"] != 4 || m.Read["events"] != 1 || m.Imported["users"] != 3 || m.Skipped["sessions_failed"] != 1 || len(m.Problems) != 1 {
		t.Fatalf("manifest = %+v", m)
	}
	for _, f := range []string{m.BackupPath, m.BackupPath + ".manifest.json"} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("backup artefact %s: %v", f, err)
		}
	}

	// Прежний токен работает без нового старта.
	boot := e.do("GET", "/bootstrap", tokA, "", nil)
	expect(t, boot, http.StatusOK, "")
	bj := boot.json(t)
	prof := bj["profile"].(map[string]any)
	if prof["ageGroup"] != "other" || prof["nickname"] != "Миша" || prof["characterId"] != "spark" || prof["reducedMotion"] != true {
		t.Fatalf("profile = %v", prof)
	}
	lg := bj["legacy"].(map[string]any)
	if lg["imported"] != true || lg["investmentLessonsSeen"] != 1.0 || bj["progress"].(map[string]any)["learningCompleted"] != 0.0 {
		t.Fatalf("legacy = %v, progress = %v", lg, bj["progress"])
	}
	plan := e.do("GET", "/money/plan", tokA, "", nil).json(t)
	g := plan["goal"].(map[string]any)
	if plan["incomeMinor"] != 1_000_000.0 || plan["foodMinor"] != 300_000.0 || g["savedMinor"] != 200_000.0 || g["targetMinor"] != 1_200_000.0 || g["planFeasible"] != true {
		t.Fatalf("plan = %v", plan)
	}
	exp := e.do("GET", "/money/expenses", tokA, "", nil).json(t)["items"].([]any)
	if len(exp) != 1 || exp[0].(map[string]any)["amountMinor"] != 45_000.0 || exp[0].(map[string]any)["voidedAt"] == nil {
		t.Fatalf("expenses = %v", exp)
	}
	ch := e.do("GET", "/money/checkins", tokA, "", nil).json(t)["items"].([]any)
	acts := e.do("GET", "/money/actions", tokA, "", nil).json(t)["items"].([]any)
	if len(ch) != 2 || len(acts) != 1 || acts[0].(map[string]any)["amountMinor"] != 30_000.0 {
		t.Fatalf("checkins %v actions %v", ch, acts)
	}
	if p := participation(e.progress(tokA)); p["xp"] != 0.0 {
		t.Fatalf("legacy history gave XP: %v", p)
	}
	kinds := map[string]int{}
	rows, err := e.db.Pool.Query(t.Context(), `SELECT source_kind, count(*) FROM legacy_records GROUP BY source_kind`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			t.Fatal(err)
		}
		kinds[k] = n
	}
	rows.Close()
	want := map[string]int{"profile": 3, "answer": 1, "investment_answer": 1, "lab_snapshot": 1, "action": 1, "assessment_d7": 2}
	for k, n := range want {
		if kinds[k] != n {
			t.Fatalf("legacy kinds = %v, want %v", kinds, want)
		}
	}
	expect(t, e.do("GET", "/bootstrap", strings.Repeat("B", 43), "", nil), http.StatusOK, "")

	// Повторный импорт того же снимка не создаёт дублей.
	m2, err := e.services.Legacy.Run(t.Context(), path, backups)
	if err != nil {
		t.Fatal(err)
	}
	var users, records int
	if err := e.db.Pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM users), (SELECT count(*) FROM legacy_records)`).Scan(&users, &records); err != nil {
		t.Fatal(err)
	}
	if m2.Skipped["sessions_already_imported"] != 3 || users != 3 || records != 9 {
		t.Fatalf("reimport: manifest %+v, users %d, records %d", m2, users, records)
	}

	// Перенесённый контракт старого D7: окно A открыто, окно D ещё ждёт, у B окна нет.
	tokD := strings.Repeat("D", 43)
	expect(t, e.do("GET", "/legacy/assessment", strings.Repeat("B", 43), "", nil), http.StatusNotFound, "NOT_FOUND")
	if b := e.do("GET", "/bootstrap", tokA, "", nil).json(t)["legacy"].(map[string]any)["d7"].(map[string]any); b["status"] != "available" {
		t.Fatalf("bootstrap d7 = %v", b)
	}
	a := e.do("GET", "/legacy/assessment", tokA, "", nil).json(t)
	if a["status"] != "available" || a["question"] != "Как изменить план Дани?" || len(a["choices"].([]any)) != 3 {
		t.Fatalf("A window = %v", a)
	}
	if strings.Contains(string(e.do("GET", "/legacy/assessment", tokA, "", nil).body), "correctChoice") {
		t.Fatal("legacy D7 exposes key")
	}
	expect(t, e.do("POST", "/legacy/assessment/answer", tokA, e.key(), map[string]any{"choiceId": "nope", "reasonId": "budget"}),
		http.StatusUnprocessableEntity, "UNKNOWN_OPTION")
	res := e.post(tokA, "/legacy/assessment/answer", map[string]any{"choiceId": "keep", "reasonId": "budget"}, http.StatusOK)
	if res["correct"] != false || res["alreadyCompleted"] != false || res["assessment"].(map[string]any)["status"] != "completed" {
		t.Fatalf("A answer = %v", res)
	}
	again := e.post(tokA, "/legacy/assessment/answer", map[string]any{"choiceId": "adjust", "reasonId": "budget"}, http.StatusOK)
	if again["correct"] != false || again["alreadyCompleted"] != true {
		t.Fatalf("A repeat = %v", again)
	}
	if got := e.do("GET", "/legacy/assessment", tokA, "", nil).json(t); got["question"] != nil || got["correct"] != false {
		t.Fatalf("A after answer = %v", got)
	}
	if p := participation(e.progress(tokA)); p["xp"] != 0.0 {
		t.Fatalf("legacy D7 gave XP: %v", p)
	}

	d := e.do("GET", "/legacy/assessment", tokD, "", nil).json(t)
	if d["status"] != "waiting" || d["question"] != nil {
		t.Fatalf("D window = %v", d)
	}
	expect(t, e.do("POST", "/legacy/assessment/answer", tokD, e.key(), map[string]any{"choiceId": "adjust", "reasonId": "budget"}),
		http.StatusConflict, "ASSESSMENT_UNAVAILABLE")
	e.clock.Advance(210 * time.Hour) // после исходного closesAt = first + 216 ч
	if d = e.do("GET", "/legacy/assessment", tokD, "", nil).json(t); d["status"] != "expired" {
		t.Fatalf("D after close = %v", d)
	}
	expect(t, e.do("POST", "/legacy/assessment/answer", tokD, e.key(), map[string]any{"choiceId": "adjust", "reasonId": "budget"}),
		http.StatusConflict, "ASSESSMENT_UNAVAILABLE")
}
