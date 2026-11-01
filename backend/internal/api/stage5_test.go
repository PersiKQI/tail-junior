package api_test

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"teyljunior/internal/analytics"
	"teyljunior/internal/api"
	"teyljunior/internal/notify"
)

func (e *env) exporter(sink analytics.Sink) *analytics.Exporter {
	return &analytics.Exporter{DB: e.db, Sink: sink, Clock: e.clock, SubjectKey: []byte("test-subject-key"), Lease: time.Minute, Batch: 100}
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.Pool.QueryRow(e.t.Context(), sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) enableAnalytics(tok string) {
	e.t.Helper()
	rev := e.do("GET", "/bootstrap", tok, "", nil).json(e.t)["profile"].(map[string]any)["revision"]
	r := e.do("PATCH", "/profile", tok, e.key(), map[string]any{"expectedRevision": rev, "analyticsEnabled": true})
	expect(e.t, r, http.StatusOK, "")
	if r.json(e.t)["analyticsEnabled"] != true {
		e.t.Fatalf("profile = %s", r.body)
	}
}

func TestOutboxAndAnalytics(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newUser()

	// Без согласия на аналитику факты записываются, но в outbox не попадают.
	e.answerLearning(tok, "L01")
	if e.count(`SELECT count(*) FROM domain_facts`) == 0 || e.count(`SELECT count(*) FROM outbox`) != 0 {
		t.Fatal("facts must be recorded and nothing queued while analytics is off")
	}
	e.enableAnalytics(tok)
	e.answerLearning(tok, "L02")
	queued := e.count(`SELECT count(*) FROM outbox WHERE state = 'pending'`)
	if queued < 2 {
		t.Fatalf("queued = %d", queued)
	}

	sink := &analytics.MemorySink{}
	x := e.exporter(sink)
	n, err := x.Tick(t.Context())
	if err != nil || n != queued {
		t.Fatalf("tick = %d, %v; want %d", n, err, queued)
	}
	if n, _ := x.Tick(t.Context()); n != 0 {
		t.Fatalf("second tick exported %d", n)
	}
	var userID string
	if err := e.db.Pool.QueryRow(t.Context(), `SELECT id FROM users`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, ev := range sink.Events() {
		names = append(names, ev.EventName)
		if ev.Subject == userID || ev.Subject == "" || ev.ProgramVersion == nil || ev.SchemaVersion != 1 || ev.Origin != "server" {
			t.Fatalf("event = %+v", ev)
		}
		for k := range ev.Properties {
			if slices.Contains([]string{"amount", "note", "goalTitle", "token", "allocation", "freeText"}, k) {
				t.Fatalf("forbidden property %s in %+v", k, ev)
			}
		}
	}
	if !slices.Contains(names, "answer_accepted") || !slices.Contains(names, "xp_granted") {
		t.Fatalf("exported = %v", names)
	}

	// Ошибка получателя: повтор позже с тем же eventId, без потерь.
	sink.Fail = errors.New("receiver down")
	e.answerLearning(tok, "L03")
	if n, err := x.Tick(t.Context()); n != 0 || err != nil {
		t.Fatalf("failing tick = %d, %v", n, err)
	}
	if e.count(`SELECT count(*) FROM outbox WHERE state = 'pending' AND attempts = 1 AND available_at > $1`, e.clock.Now()) == 0 {
		t.Fatal("failed rows not rescheduled")
	}
	sink.Fail = nil
	if n, _ := x.Tick(t.Context()); n != 0 {
		t.Fatal("retried before backoff")
	}
	e.clock.Advance(5 * time.Second)
	if n, _ := x.Tick(t.Context()); n == 0 {
		t.Fatal("rows not exported after backoff")
	}

	// Истёкший lease возвращает строку в работу.
	e.answerLearning(tok, "L05")
	if _, err := e.db.Pool.Exec(t.Context(), `UPDATE outbox SET state = 'processing', lease_until = $1 WHERE state = 'pending'`,
		e.clock.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if n, _ := x.Tick(t.Context()); n == 0 {
		t.Fatal("expired lease not reclaimed")
	}
	if e.count(`SELECT count(*) FROM outbox WHERE state <> 'done'`) != 0 {
		t.Fatal("outbox not drained")
	}

	// Клиентские наблюдения: allowlist, лимит пакета, дедупликация.
	at := e.clock.Now().Format(time.RFC3339)
	expect(t, e.do("POST", "/telemetry", tok, e.key(), map[string]any{"events": []any{
		map[string]any{"clientEventId": "c1", "name": "app_opened", "clientOccurredAt": at, "amount": 100}}}),
		http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	expect(t, e.do("POST", "/telemetry", tok, e.key(), map[string]any{"events": []any{
		map[string]any{"clientEventId": "c1", "name": "budget_saved", "clientOccurredAt": at}}}),
		http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	many := []any{}
	for i := range 21 {
		many = append(many, map[string]any{"clientEventId": "m" + string(rune('a'+i)), "name": "app_opened", "clientOccurredAt": at})
	}
	expect(t, e.do("POST", "/telemetry", tok, e.key(), map[string]any{"events": many}), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	batch := map[string]any{"events": []any{
		map[string]any{"clientEventId": "c1", "name": "app_opened", "clientOccurredAt": at},
		map[string]any{"clientEventId": "c2", "name": "lab_result_viewed", "clientOccurredAt": at, "targetId": "lrn_x"},
	}}
	if r := e.post(tok, "/telemetry", batch, http.StatusOK); r["accepted"] != 2.0 || r["stored"] != true {
		t.Fatalf("telemetry = %v", r)
	}
	if r := e.post(tok, "/telemetry", batch, http.StatusOK); r["duplicates"] != 2.0 {
		t.Fatalf("duplicate telemetry = %v", r)
	}
	before := len(sink.Events())
	if n, _ := x.Tick(t.Context()); n != 2 {
		t.Fatalf("client events exported = %d", n)
	}
	var clientSeen bool
	for _, ev := range sink.Events() {
		clientSeen = clientSeen || (ev.Origin == "client" && ev.EventName == "lab_result_viewed" && ev.Properties["targetId"] == "lrn_x")
	}
	if !clientSeen || len(sink.Events()) != before+2 {
		t.Fatal("client event missing in export")
	}

	// Без согласия наблюдения принимаются, но не хранятся.
	tok2 := e.newUser()
	if r := e.post(tok2, "/telemetry", batch, http.StatusOK); r["stored"] != false {
		t.Fatalf("telemetry without consent = %v", r)
	}
	if e.count(`SELECT count(*) FROM client_events WHERE user_id <> $1`, userID) != 0 {
		t.Fatal("client events stored without consent")
	}
}

// moscow — локальное время учебного пояса по умолчанию.
func moscow(day, hour, minute int) time.Time {
	return time.Date(2026, 11, day, hour-3, minute, 0, 0, time.UTC)
}

func (e *env) setupPush(tok, mode string) {
	e.t.Helper()
	e.put(tok, "/device", map[string]any{"installationId": "inst-1", "platform": "ios", "provider": "dryrun",
		"pushToken": "ExponentPushToken[secret-1]", "permission": "granted"}, http.StatusOK)
	rev := e.do("GET", "/reminders", tok, "", nil).json(e.t)["revision"]
	body := map[string]any{"expectedRevision": rev, "mode": mode, "daysIso": []int{1, 2, 3}, "localTime": "13:00",
		"categories": []string{"learning", "event", "streak"}}
	if p := e.put(tok, "/reminders", body, http.StatusOK); p["pushReady"] != true {
		e.t.Fatalf("reminders = %v", p)
	}
}

func (e *env) tickNotify() int {
	e.t.Helper()
	n, err := e.services.Notify.Tick(e.t.Context(), e.db)
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestRemindersSettings(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newUser()
	p := e.do("GET", "/reminders", tok, "", nil).json(t)
	if p["mode"] != "off" || p["revision"] != 0.0 || p["suggestedMode"] != "three_per_week" || p["pushReady"] != false {
		t.Fatalf("defaults = %v", p)
	}
	base := map[string]any{"expectedRevision": 0, "mode": "three_per_week", "daysIso": []int{1, 3}, "localTime": "22:00", "categories": []string{"learning"}}
	expect(t, e.do("PUT", "/reminders", tok, e.key(), base), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	base["localTime"], base["daysIso"] = "18:00", []int{1, 2, 3, 4}
	expect(t, e.do("PUT", "/reminders", tok, e.key(), base), http.StatusUnprocessableEntity, "VALIDATION_FAILED")
	base["daysIso"] = []int{1, 3, 3}
	saved := e.put(tok, "/reminders", base, http.StatusOK)
	if saved["revision"] != 1.0 || len(saved["daysIso"].([]any)) != 2 || saved["permission"] != "undetermined" || saved["pushReady"] != false {
		t.Fatalf("saved = %v", saved)
	}
	expect(t, e.do("PUT", "/reminders", tok, e.key(), base), http.StatusConflict, "REVISION_CONFLICT")

	dev := e.put(tok, "/device", map[string]any{"installationId": "inst-1", "platform": "android", "provider": "dryrun",
		"pushToken": "ExponentPushToken[plain-secret]", "permission": "granted"}, http.StatusOK)
	if dev["hasPushToken"] != true || strings.Contains(string(e.do("GET", "/reminders", tok, "", nil).body), "plain-secret") {
		t.Fatalf("device = %v", dev)
	}
	var sealed []byte
	if err := e.db.Pool.QueryRow(t.Context(), `SELECT push_token_ciphertext FROM devices`).Scan(&sealed); err != nil || strings.Contains(string(sealed), "plain-secret") {
		t.Fatalf("push token stored in plain text: %v", err)
	}
	if p := e.do("GET", "/reminders", tok, "", nil).json(t); p["pushReady"] != true {
		t.Fatalf("after device = %v", p)
	}
}

func TestNotificationDelivery(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newUser()
	e.answerLearning(tok, "L01") // 1 ноября, активный день
	e.setupPush(tok, "daily")

	// До выбранного времени и после окна доставки ничего не отправляется.
	e.clock.Set(moscow(2, 12, 0))
	if e.tickNotify() != 0 {
		t.Fatal("sent before slot")
	}
	e.clock.Set(moscow(2, 15, 30))
	if e.tickNotify() != 0 {
		t.Fatal("sent after delivery window")
	}

	// 2 ноября: вчера был активный день, сегодня — нет: повод «серия».
	e.clock.Set(moscow(2, 13, 30))
	if e.tickNotify() != 1 {
		t.Fatal("streak invitation not sent")
	}
	sent := e.provider.Sent()
	if len(sent) != 1 || sent[0].Token != "ExponentPushToken[secret-1]" || sent[0].Title != "Люми" || sent[0].Route != "next-step" {
		t.Fatalf("sent = %+v", sent)
	}
	if e.count(`SELECT count(*) FROM domain_facts WHERE name = 'notification_provider_accepted'`) != 1 {
		t.Fatal("provider_accepted fact missing")
	}
	if e.tickNotify() != 0 {
		t.Fatal("second invitation within 24 hours")
	}
	jobID := sent[0].NotificationID
	e.post(tok, "/telemetry", map[string]any{"events": []any{map[string]any{"clientEventId": "o1", "name": "notification_opened",
		"clientOccurredAt": e.clock.Now().Format(time.RFC3339), "targetId": jobID}}}, http.StatusOK)
	if e.count(`SELECT count(*) FROM notification_jobs WHERE id = $1 AND opened_at IS NOT NULL`, jobID) != 1 {
		t.Fatal("opened_at not recorded")
	}

	// 3 ноября: незаконченная попытка. Скользящие 24 часа ещё не прошли — ждём.
	e.clock.Set(moscow(3, 10, 0))
	e.startAttempt(tok, e.assignment(tok, "L02")["assignmentId"].(string))
	e.clock.Set(moscow(3, 13, 20))
	if e.tickNotify() != 0 {
		t.Fatal("second invitation within rolling 24 hours")
	}
	// Второе приглашение без учебного возвращения — автопауза.
	e.clock.Set(moscow(3, 13, 40))
	if e.tickNotify() != 1 || e.provider.Sent()[1].Route != "attempt" {
		t.Fatalf("attempt invitation = %+v", e.provider.Sent())
	}
	if p := e.do("GET", "/reminders", tok, "", nil).json(t); p["autoPauseUntil"] == nil {
		t.Fatalf("auto pause not set: %v", p)
	}
	e.clock.Set(moscow(4, 13, 50))
	if e.tickNotify() != 0 {
		t.Fatal("sent during auto pause")
	}

	// Учебное действие снимает автопаузу; после него сегодня приглашений нет, завтра — есть.
	e.clock.Set(moscow(4, 14, 0))
	e.answerWith(tok, "L02", "keep", "access")
	if e.tickNotify() != 0 {
		t.Fatal("invitation after today's learning action")
	}
	e.clock.Set(moscow(5, 13, 5))
	if e.tickNotify() != 1 {
		t.Fatal("invitation after return not sent")
	}
	if p := e.do("GET", "/reminders", tok, "", nil).json(t); p["autoPauseUntil"] != nil {
		t.Fatalf("auto pause not cleared: %v", p)
	}

	// Отписка отменяет запланированное; новые приглашения не уходят.
	rev := e.do("GET", "/reminders", tok, "", nil).json(t)["revision"]
	e.put(tok, "/reminders", map[string]any{"expectedRevision": rev, "mode": "off", "daysIso": []int{1}, "localTime": "13:00", "categories": []string{}}, http.StatusOK)
	e.clock.Set(moscow(6, 13, 5))
	if e.tickNotify() != 0 {
		t.Fatal("sent after unsubscribe")
	}
}

func TestNotificationProviderOutcomes(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newUser()
	e.answerLearning(tok, "L01")
	e.setupPush(tok, "daily")
	e.clock.Set(moscow(2, 13, 30))

	// Известный отказ: повтор с тем же notificationId без новой квоты.
	e.provider.Respond = func(notify.Message) error { return notify.ErrRejected }
	if e.tickNotify() != 0 {
		t.Fatal("rejected counted as sent")
	}
	e.provider.Respond = nil
	if e.tickNotify() != 1 {
		t.Fatal("rejected message not retried")
	}
	if e.count(`SELECT count(*) FROM notification_jobs`) != 1 ||
		e.count(`SELECT count(*) FROM notification_jobs WHERE state = 'provider_accepted' AND attempts = 2`) != 1 {
		t.Fatal("retry must reuse the same job")
	}

	// Неоднозначный исход: unknown, автоматической повторной отправки нет, квота занята.
	tok2 := e.newUser()
	e.clock.Set(start)
	e.answerLearning(tok2, "L01")
	e.setupPush(tok2, "daily")
	e.clock.Set(moscow(2, 13, 40))
	e.provider.Respond = func(notify.Message) error { return errors.New("timeout") }
	e.tickNotify()
	e.provider.Respond = nil
	if e.tickNotify() != 0 {
		t.Fatal("ambiguous send was retried")
	}
	if e.count(`SELECT count(*) FROM notification_jobs WHERE state = 'unknown'`) != 1 {
		t.Fatal("ambiguous outcome not recorded as unknown")
	}

	// Обработчик упал после резервирования: истёкший lease — unknown, не повтор.
	if _, err := e.db.Pool.Exec(t.Context(), `UPDATE notification_jobs SET state = 'submitting', lease_until = $1 WHERE state = 'unknown'`,
		e.clock.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	e.tickNotify()
	if e.count(`SELECT count(*) FROM notification_jobs WHERE state = 'unknown'`) != 1 {
		t.Fatal("stale submitting not converted to unknown")
	}
}

func TestRateLimits(t *testing.T) {
	e := newEnvLimits(t, true, api.Limits{SessionsPerIPPerHour: 2, RequestsPerUserPerMinute: 3})
	tok := e.newUser()
	e.newUser()
	r := e.do("POST", "/sessions", "", "", nil)
	expect(t, r, http.StatusTooManyRequests, "RATE_LIMITED")
	for range 3 {
		expect(t, e.do("GET", "/bootstrap", tok, "", nil), http.StatusOK, "")
	}
	expect(t, e.do("GET", "/bootstrap", tok, "", nil), http.StatusTooManyRequests, "RATE_LIMITED")
	e.clock.Advance(time.Minute)
	expect(t, e.do("GET", "/bootstrap", tok, "", nil), http.StatusOK, "")
}
