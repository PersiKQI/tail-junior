package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	bankfiles "teyljunior/content"
	"teyljunior/internal/api"
	"teyljunior/internal/app"
	"teyljunior/internal/challenge"
	"teyljunior/internal/content"
	"teyljunior/internal/core"
	"teyljunior/internal/dbtest"
	"teyljunior/internal/lab"
	"teyljunior/internal/notify"
	"teyljunior/internal/store"
)

// reverseShuffler делает порядок вариантов предсказуемым: обратный порядку банка.
type reverseShuffler struct{}

func (reverseShuffler) Perm(n int) []int {
	p := make([]int, n)
	for i := range p {
		p[i] = n - 1 - i
	}
	return p
}

type env struct {
	t        *testing.T
	srv      *httptest.Server
	db       *store.DB
	clock    *core.FixedClock
	services *app.Services
	provider *notify.DryRun
	keys     atomic.Int64
}

var start = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

// testPushKey — фиксированный ключ шифрования push token в тестах.
var testPushKey = bytes.Repeat([]byte{7}, 32)

// newEnv поднимает API на временной базе без ограничения частоты. publish=false — поставка
// только импортирована.
func newEnv(t *testing.T, publish bool) *env { return newEnvLimits(t, publish, api.Limits{}) }

func newEnvLimits(t *testing.T, publish bool, limits api.Limits) *env {
	t.Helper()
	dsn := dbtest.DSN(t)
	ctx := t.Context()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	b, err := content.ParseBundle(bankfiles.Program, bankfiles.Adapter)
	if err != nil {
		t.Fatal(err)
	}
	if err := content.Import(ctx, db, b, start); err != nil {
		t.Fatal(err)
	}
	pack, err := lab.ParsePack(bankfiles.LabCase)
	if err != nil {
		t.Fatal(err)
	}
	if err := lab.Import(ctx, db, pack, start); err != nil {
		t.Fatal(err)
	}
	if publish {
		if err := content.Publish(ctx, db, b.Program.ProgramVersion, "test-editor", start); err != nil {
			t.Fatal(err)
		}
		if err := lab.Publish(ctx, db, pack.Case.CaseFamilyID, pack.Case.Revision, "test-editor", start); err != nil {
			t.Fatal(err)
		}
		ch, err := challenge.ParseRelease(bankfiles.Challenge)
		if err != nil {
			t.Fatal(err)
		}
		if err := challenge.Import(ctx, db, ch, start); err != nil {
			t.Fatal(err)
		}
		if err := challenge.Publish(ctx, db, ch.FamilyID, ch.Revision, "test-editor", start); err != nil {
			t.Fatal(err)
		}
	}
	clock := core.NewFixedClock(start)
	provider := &notify.DryRun{}
	services, err := app.New(app.Deps{DB: db, Clock: clock, IDs: core.RandomIDs{}, Shuffler: reverseShuffler{},
		PushTokenKey: testPushKey, Provider: provider})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(api.New(api.Deps{DB: db, Clock: clock, IDs: core.RandomIDs{}, Services: services, Limits: limits}))
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, db: db, clock: clock, services: services, provider: provider}
}

type resp struct {
	status int
	body   []byte
}

func (r resp) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
	return m
}

func (r resp) code(t *testing.T) string {
	t.Helper()
	c, _ := r.json(t)["code"].(string)
	return c
}

func (e *env) do(method, path, token, key string, body any) resp {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			rd = strings.NewReader(b)
		default:
			raw, err := json.Marshal(b)
			if err != nil {
				e.t.Fatal(err)
			}
			rd = bytes.NewReader(raw)
		}
	}
	req, err := http.NewRequestWithContext(context.Background(), method, e.srv.URL+"/api/v2"+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return resp{status: res.StatusCode, body: raw}
}

func (e *env) key() string { return fmt.Sprintf("k-%d", e.keys.Add(1)) }

func (e *env) newUser() string {
	e.t.Helper()
	r := e.do("POST", "/sessions", "", "", nil)
	if r.status != http.StatusCreated {
		e.t.Fatalf("session: %d %s", r.status, r.body)
	}
	return r.json(e.t)["token"].(string)
}

// assignment возвращает назначение по itemId.
func (e *env) assignment(token, itemID string) map[string]any {
	e.t.Helper()
	r := e.do("GET", "/learning/assignments", token, "", nil)
	if r.status != http.StatusOK {
		e.t.Fatalf("assignments: %d %s", r.status, r.body)
	}
	for _, it := range r.json(e.t)["items"].([]any) {
		a := it.(map[string]any)
		if a["itemId"] == itemID && a["role"] == "learning" {
			return a
		}
	}
	e.t.Fatalf("assignment %s not found", itemID)
	return nil
}

func (e *env) startAttempt(token, assignmentID string) map[string]any {
	e.t.Helper()
	r := e.do("POST", "/learning/attempts", token, e.key(), map[string]any{"assignmentId": assignmentID})
	if r.status != http.StatusCreated && r.status != http.StatusOK {
		e.t.Fatalf("start attempt: %d %s", r.status, r.body)
	}
	return r.json(e.t)
}

func expect(t *testing.T, r resp, status int, code string) {
	t.Helper()
	if r.status != status {
		t.Fatalf("status = %d, want %d: %s", r.status, status, r.body)
	}
	if code != "" && r.code(t) != code {
		t.Fatalf("code = %s, want %s: %s", r.code(t), code, r.body)
	}
}

func TestSessionRequiresPublishedProgram(t *testing.T) {
	e := newEnv(t, false)
	r := e.do("POST", "/sessions", "", "", nil)
	expect(t, r, http.StatusServiceUnavailable, "CONTENT_UNAVAILABLE")
}

func TestSessionAndBootstrap(t *testing.T) {
	e := newEnv(t, true)
	r := e.do("POST", "/sessions", "", "", map[string]any{"installationId": "inst-1"})
	expect(t, r, http.StatusCreated, "")
	token := r.json(t)["token"].(string)

	b := e.do("GET", "/bootstrap", token, "", nil)
	expect(t, b, http.StatusOK, "")
	m := b.json(t)
	p := m["progress"].(map[string]any)
	if p["learningTotal"] != 24.0 || p["routeTotal"] != 12.0 || p["learningCompleted"] != 0.0 || p["nextAssignmentId"] == nil {
		t.Fatalf("progress = %v", p)
	}
	if m["program"].(map[string]any)["version"] != "teyl-learning-2026-11-01-draft-1" {
		t.Fatalf("program = %v", m["program"])
	}
	if strings.Contains(string(b.body), token) {
		t.Fatal("bootstrap must not echo the token")
	}
}

func TestUnauthorized(t *testing.T) {
	e := newEnv(t, true)
	for _, tok := range []string{"", "not-a-token"} {
		r := e.do("GET", "/bootstrap", tok, "", nil)
		expect(t, r, http.StatusUnauthorized, "UNAUTHORIZED")
		if r.json(t)["requestId"] == "" {
			t.Fatal("error without requestId")
		}
	}
}

func TestLearningFlow(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newUser()

	a := e.assignment(tok, "L09")
	if a["status"] != "open" || a["inRoute"] != true {
		t.Fatalf("assignment = %v", a)
	}
	asgID := a["assignmentId"].(string)

	r := e.do("POST", "/learning/attempts", tok, e.key(), map[string]any{"assignmentId": asgID})
	expect(t, r, http.StatusCreated, "")
	for _, secret := range []string{"acceptedChoiceIds", "acceptedReasonIds", "allowedPairs", "explanation", "hint", "feedback", "evaluation\":{"} {
		if strings.Contains(string(r.body), `"`+secret) {
			t.Fatalf("attempt leaks %q: %s", secret, r.body)
		}
	}
	att := r.json(t)
	attID := att["attemptId"].(string)
	card := att["card"].(map[string]any)
	choices := card["choices"].([]any)
	if choices[0].(map[string]any)["id"] != "minus50" || choices[2].(map[string]any)["id"] != "minus24" {
		t.Fatalf("option order not applied: %v", choices)
	}
	if att["revision"] != 1.0 || att["programVersion"] != "teyl-learning-2026-11-01-draft-1" {
		t.Fatalf("pinned revision missing: %v", att)
	}

	// Повторное открытие возвращает ту же попытку и тот же порядок.
	again := e.startAttempt(tok, asgID)
	if again["attemptId"] != attID {
		t.Fatalf("reopen created new attempt %v", again["attemptId"])
	}
	g := e.do("GET", "/learning/attempts/"+attID, tok, "", nil)
	expect(t, g, http.StatusOK, "")
	if fmt.Sprint(g.json(t)["card"].(map[string]any)["choices"]) != fmt.Sprint(choices) {
		t.Fatal("order changed on reopen")
	}

	// Ошибочный выбор с верным объяснением: оценка раздельная, шаг завершён.
	e.clock.Advance(time.Minute)
	s := e.do("POST", "/learning/attempts/"+attID+"/submission", tok, e.key(), map[string]any{"choiceId": "plus30", "reasonId": "all"})
	expect(t, s, http.StatusCreated, "")
	rc := s.json(t)
	ev := rc["evaluation"].(map[string]any)
	if ev["choiceCorrect"] != false || ev["reasonCorrect"] != true || ev["passed"] != false || ev["choiceFeedback"] == "" || ev["explanation"] == "" {
		t.Fatalf("evaluation = %v", ev)
	}
	if rc["firstForFamily"] != true || rc["completed"] != true {
		t.Fatalf("receipt = %v", rc)
	}
	subID := rc["submission"].(map[string]any)["submissionId"]

	// Тот же ответ с другим ключом — сохранённая квитанция; другой ответ — 409.
	same := e.do("POST", "/learning/attempts/"+attID+"/submission", tok, e.key(), map[string]any{"choiceId": "plus30", "reasonId": "all"})
	expect(t, same, http.StatusOK, "")
	if same.json(t)["submission"].(map[string]any)["submissionId"] != subID {
		t.Fatal("resubmission created new submission")
	}
	diff := e.do("POST", "/learning/attempts/"+attID+"/submission", tok, e.key(), map[string]any{"choiceId": "minus24", "reasonId": "all"})
	expect(t, diff, http.StatusConflict, "ATTEMPT_CLOSED")

	if e.assignment(tok, "L09")["status"] != "completed" {
		t.Fatal("assignment not completed")
	}
	reopen := e.do("POST", "/learning/attempts", tok, e.key(), map[string]any{"assignmentId": asgID})
	expect(t, reopen, http.StatusConflict, "ASSIGNMENT_COMPLETED")

	// Практика: создаётся один раз, верный ответ не переписывает первую попытку.
	pr := e.do("POST", "/learning/practice-assignments", tok, e.key(), map[string]any{"familyId": "f-L09"})
	expect(t, pr, http.StatusCreated, "")
	prID := pr.json(t)["assignmentId"].(string)
	pr2 := e.do("POST", "/learning/practice-assignments", tok, e.key(), map[string]any{"familyId": "f-L09"})
	expect(t, pr2, http.StatusOK, "")
	if pr2.json(t)["assignmentId"] != prID {
		t.Fatal("second open practice created")
	}
	patt := e.startAttempt(tok, prID)
	ps := e.do("POST", "/learning/attempts/"+patt["attemptId"].(string)+"/submission", tok, e.key(), map[string]any{"choiceId": "minus24", "reasonId": "all"})
	expect(t, ps, http.StatusCreated, "")
	if prc := ps.json(t); prc["firstForFamily"] != false || prc["evaluation"].(map[string]any)["passed"] != true {
		t.Fatalf("practice receipt = %v", prc)
	}
	first := e.do("GET", "/learning/attempts/"+attID, tok, "", nil).json(t)
	if first["submission"].(map[string]any)["choiceId"] != "plus30" {
		t.Fatal("first attempt was rewritten")
	}

	bs := e.do("GET", "/bootstrap", tok, "", nil).json(t)["progress"].(map[string]any)
	if bs["learningCompleted"] != 1.0 || bs["routeCompleted"] != 1.0 {
		t.Fatalf("progress = %v", bs)
	}
}

func TestPracticeBeforeFirstAnswerIsRejected(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newUser()
	r := e.do("POST", "/learning/practice-assignments", tok, e.key(), map[string]any{"familyId": "f-L01"})
	expect(t, r, http.StatusConflict, "PRACTICE_NOT_AVAILABLE")
	r = e.do("POST", "/learning/practice-assignments", tok, e.key(), map[string]any{"familyId": "f-R02A"})
	expect(t, r, http.StatusNotFound, "NOT_FOUND")
}

// Неизвестный вариант — 422 без завершения; ключ ошибки не расходуется (§8, §21).
func TestUnknownOptionKeepsAttemptOpen(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newUser()
	att := e.startAttempt(tok, e.assignment(tok, "L01")["assignmentId"].(string))
	id := att["attemptId"].(string)
	key := e.key()
	r := e.do("POST", "/learning/attempts/"+id+"/submission", tok, key, map[string]any{"choiceId": "nope", "reasonId": "deadline"})
	expect(t, r, http.StatusUnprocessableEntity, "UNKNOWN_OPTION")
	if g := e.do("GET", "/learning/attempts/"+id, tok, "", nil).json(t); g["status"] != "started" {
		t.Fatalf("status = %v", g["status"])
	}
	ok := e.do("POST", "/learning/attempts/"+id+"/submission", tok, key, map[string]any{"choiceId": "fit", "reasonId": "deadline"})
	expect(t, ok, http.StatusCreated, "")
}

func TestIdempotency(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newUser()
	asg := e.assignment(tok, "L02")["assignmentId"].(string)

	missing := e.do("POST", "/learning/attempts", tok, "", map[string]any{"assignmentId": asg})
	expect(t, missing, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED")
	bad := e.do("POST", "/learning/attempts", tok, "has space", map[string]any{"assignmentId": asg})
	expect(t, bad, http.StatusUnprocessableEntity, "VALIDATION_FAILED")

	key := "7d0b9f5e-2f0a-4b8e-9c1d-6a2b3c4d5e6f"
	first := e.do("POST", "/learning/attempts", tok, key, map[string]any{"assignmentId": asg})
	expect(t, first, http.StatusCreated, "")
	// Тот же запрос с иным форматированием — повтор с исходным статусом.
	replay := e.do("POST", "/learning/attempts", tok, key, `{ "assignmentId" : "`+asg+`" }`)
	expect(t, replay, http.StatusCreated, "")
	if fmt.Sprint(first.json(t)) != fmt.Sprint(replay.json(t)) {
		t.Fatal("replay body differs")
	}
	other := e.do("POST", "/learning/attempts", tok, key, map[string]any{"assignmentId": e.assignment(tok, "L03")["assignmentId"]})
	expect(t, other, http.StatusConflict, "IDEMPOTENCY_CONFLICT")
	path := e.do("POST", "/learning/practice-assignments", tok, key, map[string]any{"familyId": "f-L02"})
	expect(t, path, http.StatusConflict, "IDEMPOTENCY_CONFLICT")

	// Ключи разных пользователей не пересекаются.
	tok2 := e.newUser()
	r2 := e.do("POST", "/learning/attempts", tok2, key, map[string]any{"assignmentId": e.assignment(tok2, "L02")["assignmentId"]})
	expect(t, r2, http.StatusCreated, "")
}

func TestIsolation(t *testing.T) {
	e := newEnv(t, true)
	a, b := e.newUser(), e.newUser()
	asg := e.assignment(a, "L01")["assignmentId"].(string)
	att := e.startAttempt(a, asg)["attemptId"].(string)

	expect(t, e.do("GET", "/learning/attempts/"+att, b, "", nil), http.StatusNotFound, "NOT_FOUND")
	expect(t, e.do("POST", "/learning/attempts", b, e.key(), map[string]any{"assignmentId": asg}), http.StatusNotFound, "NOT_FOUND")
	expect(t, e.do("POST", "/learning/attempts/"+att+"/submission", b, e.key(), map[string]any{"choiceId": "fit", "reasonId": "deadline"}),
		http.StatusNotFound, "NOT_FOUND")
}

func TestSupportIsRecorded(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newUser()
	id := e.startAttempt(tok, e.assignment(tok, "L01")["assignmentId"].(string))["attemptId"].(string)

	h := e.do("POST", "/learning/attempts/"+id+"/support", tok, e.key(), map[string]any{"kind": "hint"})
	expect(t, h, http.StatusOK, "")
	if h.json(t)["hint"] == "" {
		t.Fatal("empty hint")
	}
	term := e.do("POST", "/learning/attempts/"+id+"/support", tok, e.key(), map[string]any{"kind": "term", "termId": "liquidity"})
	expect(t, term, http.StatusOK, "")
	expect(t, e.do("POST", "/learning/attempts/"+id+"/support", tok, e.key(), map[string]any{"kind": "term", "termId": "xyz"}),
		http.StatusUnprocessableEntity, "UNKNOWN_TERM")
	if g := e.do("GET", "/learning/attempts/"+id, tok, "", nil).json(t); g["assistanceUsed"] != true {
		t.Fatal("assistance not recorded")
	}
	var n int
	if err := e.db.Pool.QueryRow(t.Context(), `SELECT count(*) FROM assistance_events WHERE attempt_id = $1`, id).Scan(&n); err != nil || n != 2 {
		t.Fatalf("assistance events = %d, %v", n, err)
	}
	e.do("POST", "/learning/attempts/"+id+"/submission", tok, e.key(), map[string]any{"choiceId": "fit", "reasonId": "deadline"})
	expect(t, e.do("POST", "/learning/attempts/"+id+"/support", tok, e.key(), map[string]any{"kind": "hint"}),
		http.StatusConflict, "ATTEMPT_CLOSED")
}

// Параллельные разные ответы с разными ключами: принимается ровно один (§21).
func TestConcurrentSubmissions(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newUser()
	id := e.startAttempt(tok, e.assignment(tok, "L09")["assignmentId"].(string))["attemptId"].(string)

	var wg sync.WaitGroup
	results := make([]resp, 3)
	for i, choice := range []string{"minus24", "plus30", "minus50"} {
		wg.Go(func() {
			results[i] = e.do("POST", "/learning/attempts/"+id+"/submission", tok, e.key(), map[string]any{"choiceId": choice, "reasonId": "all"})
		})
	}
	wg.Wait()
	var created, conflicts int
	for _, r := range results {
		switch r.status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("unexpected %d %s", r.status, r.body)
		}
	}
	if created != 1 || conflicts != 2 {
		t.Fatalf("created %d, conflicts %d", created, conflicts)
	}

	// Один ключ, много одновременных повторов — один факт и одинаковые ответы.
	id2 := e.startAttempt(tok, e.assignment(tok, "L10")["assignmentId"].(string))["attemptId"].(string)
	key := e.key()
	same := make([]resp, 8)
	for i := range same {
		wg.Go(func() {
			same[i] = e.do("POST", "/learning/attempts/"+id2+"/submission", tok, key, map[string]any{"choiceId": "zero", "reasonId": "net"})
		})
	}
	wg.Wait()
	for _, r := range same {
		expect(t, r, http.StatusCreated, "")
		if fmt.Sprint(r.json(t)) != fmt.Sprint(same[0].json(t)) {
			t.Fatal("replays differ")
		}
	}
	var n int
	if err := e.db.Pool.QueryRow(t.Context(), `SELECT count(*) FROM submissions WHERE attempt_id = $1`, id2).Scan(&n); err != nil || n != 1 {
		t.Fatalf("submissions = %d, %v", n, err)
	}
}

func TestProfilePatch(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newUser()

	expect(t, e.do("PATCH", "/profile", tok, e.key(), map[string]any{"expectedRevision": 5, "nickname": "Аня"}),
		http.StatusConflict, "REVISION_CONFLICT")
	ok := e.do("PATCH", "/profile", tok, e.key(), map[string]any{
		"expectedRevision": 1, "nickname": "  Аня ", "ageGroup": "14-17", "characterId": "nova",
		"route": "full", "learningTimezone": "Europe/Moscow",
	})
	expect(t, ok, http.StatusOK, "")
	p := ok.json(t)
	if p["nickname"] != "Аня" || p["revision"] != 2.0 || p["route"] != "full" {
		t.Fatalf("profile = %v", p)
	}
	cleared := e.do("PATCH", "/profile", tok, e.key(), map[string]any{"expectedRevision": 2, "nickname": nil})
	expect(t, cleared, http.StatusOK, "")
	if cleared.json(t)["nickname"] != nil {
		t.Fatal("null did not clear nickname")
	}
	expect(t, e.do("PATCH", "/profile", tok, e.key(), map[string]any{"expectedRevision": 3, "learningTimezone": "Mars/Base"}),
		http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	expect(t, e.do("PATCH", "/profile", tok, e.key(), map[string]any{"expectedRevision": 3, "ageGroup": "21-22"}),
		http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	expect(t, e.do("PATCH", "/profile", tok, e.key(), map[string]any{"expectedRevision": 3, "birthDate": "2010-01-01"}),
		http.StatusUnprocessableEntity, "VALIDATION_FAILED")

	bs := e.do("GET", "/bootstrap", tok, "", nil).json(t)["progress"].(map[string]any)
	if bs["routeTotal"] != 24.0 {
		t.Fatalf("full route total = %v", bs["routeTotal"])
	}
}

func TestStrictRequests(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newUser()
	expect(t, e.do("POST", "/learning/attempts", tok, e.key(), `{"assignmentId":"a","assignmentId":"b"}`),
		http.StatusBadRequest, "INVALID_JSON")
	expect(t, e.do("POST", "/learning/attempts", tok, e.key(), `{"assignmentId":"a"} {}`),
		http.StatusBadRequest, "INVALID_JSON")

	req, _ := http.NewRequest("POST", e.srv.URL+"/api/v2/learning/attempts", strings.NewReader(`{"assignmentId":"a"}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Idempotency-Key", e.key())
	req.Header.Set("Content-Type", "text/plain")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("media type: %d", res.StatusCode)
	}

	big := `{"assignmentId":"` + strings.Repeat("x", 17<<10) + `"}`
	expect(t, e.do("POST", "/learning/attempts", tok, e.key(), big), http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE")
	expect(t, e.do("GET", "/no-such-route", tok, "", nil), http.StatusNotFound, "NOT_FOUND")
}

func TestDeleteAccount(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newUser()
	id := e.startAttempt(tok, e.assignment(tok, "L01")["assignmentId"].(string))["attemptId"].(string)
	e.do("POST", "/learning/attempts/"+id+"/submission", tok, e.key(), map[string]any{"choiceId": "fit", "reasonId": "deadline"})
	e.put(tok, "/money/plan", examplePlan(0, newGoal("Велосипед", 0)), http.StatusOK)
	e.post(tok, "/money/checkins", map[string]any{"kind": "saved", "amountMinor": 100}, http.StatusCreated)
	e.post(tok, "/money/expenses", map[string]any{"amountMinor": 100, "categoryId": "food", "occurredOn": "2026-11-01"}, http.StatusCreated)
	e.post(tok, "/money/actions", map[string]any{"kind": "review", "dueOn": "2026-11-02"}, http.StatusCreated)
	e.do("GET", "/inventory", tok, "", nil)
	e.enableAnalytics(tok)
	e.setupPush(tok, "daily")
	e.post(tok, "/telemetry", map[string]any{"events": []any{map[string]any{"clientEventId": "d1", "name": "app_opened",
		"clientOccurredAt": e.clock.Now().Format(time.RFC3339)}}}, http.StatusOK)

	expect(t, e.do("DELETE", "/account", tok, "", nil), http.StatusNoContent, "")
	expect(t, e.do("DELETE", "/account", tok, "", nil), http.StatusUnauthorized, "UNAUTHORIZED")
	expect(t, e.do("GET", "/bootstrap", tok, "", nil), http.StatusUnauthorized, "UNAUTHORIZED")

	for _, table := range []string{"users", "credentials", "assignments", "assessment_sets", "attempts", "submissions", "evaluations",
		"learning_completions", "idempotency_receipts", "content_exposures", "domain_facts", "xp_ledger", "badge_grants",
		"study_days", "freeze_ledger", "lab_experiments", "lab_decisions", "money_plans", "goals", "checkins", "expenses",
		"planned_actions", "challenge_participations", "cosmetic_inventory", "cosmetic_equipment", "legacy_records",
		"devices", "reminder_preferences", "notification_jobs", "client_events", "outbox", "legacy_d7_answers"} {
		var n int
		if err := e.db.Pool.QueryRow(t.Context(), `SELECT count(*) FROM `+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s rows after delete = %d, %v", table, n, err)
		}
	}
}

// Опубликованная редакция неизменяема: повторный импорт того же комплекта ничего не меняет,
// а повторная публикация отклоняется.
func TestContentImportIsIdempotent(t *testing.T) {
	e := newEnv(t, true)
	b, err := content.ParseBundle(bankfiles.Program, bankfiles.Adapter)
	if err != nil {
		t.Fatal(err)
	}
	if err := content.Import(t.Context(), e.db, b, start); err != nil {
		t.Fatalf("reimport: %v", err)
	}
	if err := content.Publish(t.Context(), e.db, b.Program.ProgramVersion, "x", start); err == nil {
		t.Fatal("second publish must fail")
	}
	if err := content.Publish(t.Context(), e.db, "unknown", "x", start); err == nil {
		t.Fatal("publish of unknown version must fail")
	}
}
