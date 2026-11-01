// Пакет api — HTTP API /api/v2 на net/http.ServeMux (спецификация §20, приложение А).
// Обработчик декодирует запрос, проверяет доступ и вызывает прикладной сценарий; бизнес-правила
// живут в пакетах identity, learning и content. userId берётся только из credential.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"teyljunior/internal/app"
	"teyljunior/internal/assessment"
	"teyljunior/internal/challenge"
	"teyljunior/internal/content"
	"teyljunior/internal/core"
	"teyljunior/internal/httpx"
	"teyljunior/internal/idem"
	"teyljunior/internal/identity"
	"teyljunior/internal/lab"
	"teyljunior/internal/learning"
	"teyljunior/internal/legacy"
	"teyljunior/internal/money"
	"teyljunior/internal/notify"
	"teyljunior/internal/progression"
	"teyljunior/internal/ratelimit"
	"teyljunior/internal/store"
)

// MaxBody — предел тела запроса (§20).
const MaxBody = 16 << 10

// Limits — пределы частоты (§20: 429). Нули — без ограничения.
type Limits struct {
	SessionsPerIPPerHour     int
	RequestsPerUserPerMinute int
}

// Deps — зависимости HTTP-слоя.
type Deps struct {
	DB       *store.DB
	Clock    core.Clock
	IDs      core.IDs
	Services *app.Services
	Limits   Limits
	Logger   *slog.Logger
}

type server struct {
	db        *store.DB
	clock     core.Clock
	ids       core.IDs
	catalog   *content.Catalog
	learning  *learning.Service
	assess    *assessment.Service
	prog      *progression.Service
	lab       *lab.Service
	money     *money.Service
	challenge *challenge.Service
	d7        *legacy.D7
	notify    *notify.Service
	ipLimit   *ratelimit.Limiter
	userLimit *ratelimit.Limiter
	log       *slog.Logger
}

// New собирает обработчик всех маршрутов.
func New(d Deps) http.Handler {
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	sv := d.Services
	s := &server{
		db: d.DB, clock: d.Clock, ids: d.IDs, log: d.Logger,
		catalog: sv.Catalog, assess: sv.Assess, prog: sv.Prog, learning: sv.Learning, lab: sv.Lab, money: sv.Money,
		challenge: sv.Challenge, d7: sv.D7, notify: sv.Notify,
		ipLimit:   ratelimit.New(d.Clock, d.Limits.SessionsPerIPPerHour, time.Hour),
		userLimit: ratelimit.New(d.Clock, d.Limits.RequestsPerUserPerMinute, time.Minute),
	}
	return s.routes()
}

// limited отвечает 429 с Retry-After, если ключ исчерпал предел.
func (s *server) limited(w http.ResponseWriter, r *http.Request, l *ratelimit.Limiter, key string) bool {
	ok, retry := l.Allow(key)
	if ok {
		return false
	}
	w.Header().Set("Retry-After", strconv.Itoa(max(1, int(retry.Seconds()))))
	httpx.WriteError(w, r, &core.Error{Status: http.StatusTooManyRequests, Code: "RATE_LIMITED", Message: "слишком много запросов; повтори позже"})
	return true
}

type userHandler func(w http.ResponseWriter, r *http.Request, userID string)

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	user := func(pattern string, h userHandler) { mux.Handle(pattern, s.authed(h)) }

	mux.HandleFunc("GET /api/v2/health", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /api/v2/sessions", s.postSession)

	user("GET /api/v2/bootstrap", s.getBootstrap)
	user("PATCH /api/v2/profile", s.patchProfile)
	user("DELETE /api/v2/account", s.deleteAccount)
	user("GET /api/v2/learning/assignments", s.getAssignments)
	user("POST /api/v2/learning/practice-assignments", s.postPractice)
	user("POST /api/v2/learning/attempts", s.postAttempt)
	user("GET /api/v2/learning/attempts/{attemptId}", s.getAttempt)
	user("POST /api/v2/learning/attempts/{attemptId}/support", s.postSupport)
	user("POST /api/v2/learning/attempts/{attemptId}/submission", s.postSubmission)
	user("GET /api/v2/assessment-sets", s.getSets)
	user("POST /api/v2/assessment-sets/diagnostic", s.postDiagnostic)
	user("POST /api/v2/assessment-sets/{setId}/finalization", s.postFinalization)
	user("GET /api/v2/next-step", s.getNextStep)
	user("GET /api/v2/progress", s.getProgress)
	user("GET /api/v2/lab/cases", s.getLabCases)
	user("GET /api/v2/lab/experiments", s.getExperiments)
	user("POST /api/v2/lab/experiments", s.postExperiment)
	user("GET /api/v2/lab/experiments/{experimentId}", s.getExperiment)
	user("POST /api/v2/lab/experiments/{experimentId}/decisions", s.postDecision)
	user("GET /api/v2/lab/decisions/{decisionId}", s.getDecision)
	user("POST /api/v2/lab/experiments/{experimentId}/runs", s.postRun)
	user("GET /api/v2/lab/runs/{runId}", s.getRun)
	user("POST /api/v2/lab/runs/{runId}/reflection", s.postReflection)
	user("GET /api/v2/lab/experiments/{experimentId}/comparison", s.getComparison)
	user("POST /api/v2/lab/experiments/{experimentId}/comparison", s.postComparison)
	user("GET /api/v2/challenges", s.getChallenges)
	user("POST /api/v2/challenges/{familyId}/participation", s.postParticipation)
	user("GET /api/v2/challenges/{familyId}/participation", s.getParticipation)
	user("GET /api/v2/inventory", s.getInventory)
	user("PUT /api/v2/inventory/equipment", s.putEquipment)
	user("GET /api/v2/legacy/assessment", s.getLegacyAssessment)
	user("POST /api/v2/legacy/assessment/answer", s.postLegacyAnswer)
	user("GET /api/v2/reminders", s.getReminders)
	user("PUT /api/v2/reminders", s.putReminders)
	user("PUT /api/v2/device", s.putDevice)
	user("POST /api/v2/telemetry", s.postTelemetry)
	user("GET /api/v2/money/plan", s.getPlan)
	user("PUT /api/v2/money/plan", s.putPlan)
	user("GET /api/v2/money/expenses", s.getExpenses)
	user("POST /api/v2/money/expenses", s.postExpense)
	user("PATCH /api/v2/money/expenses/{expenseId}", s.patchExpense)
	user("GET /api/v2/money/checkins", s.getCheckins)
	user("POST /api/v2/money/checkins", s.postCheckin)
	user("GET /api/v2/money/actions", s.getActions)
	user("POST /api/v2/money/actions", s.postAction)
	user("POST /api/v2/money/actions/{actionId}/completion", s.postActionCompletion)
	user("POST /api/v2/money/actions/{actionId}/cancellation", s.postActionCancellation)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, core.NotFound())
	})
	return s.middleware(mux)
}

// middleware: requestId, предел тела, перехват паники в 500 без подробностей.
func (s *server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := s.ids.New("req")
		w.Header().Set("X-Request-Id", reqID)
		r = r.WithContext(httpx.WithRequestID(r.Context(), reqID))
		r.Body = http.MaxBytesReader(w, r.Body, MaxBody)
		defer func() {
			if p := recover(); p != nil {
				s.log.Error("panic", "requestId", reqID, "path", r.URL.Path, "panic", p)
				httpx.WriteError(w, r, core.Internal())
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *server) authed(h userHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			httpx.WriteError(w, r, core.Unauthorized())
			return
		}
		userID, err := identity.Authenticate(r.Context(), s.db.Pool, strings.TrimSpace(token))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if s.limited(w, r, s.userLimit, userID) {
			return
		}
		h(w, r, userID)
	})
}

// clientIP — адрес соединения. X-Forwarded-For не доверяем: прокси в пилоте не настроен.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// fail пишет ошибку; внутренние причины остаются в журнале, наружу — обобщённый текст (§20).
func (s *server) fail(w http.ResponseWriter, r *http.Request, err error) {
	e := core.AsError(err)
	if e.Status >= http.StatusInternalServerError && e.Code == "INTERNAL" {
		s.log.Error("request failed", "requestId", httpx.RequestID(r.Context()), "method", r.Method, "path", r.URL.Path, "err", err)
	}
	httpx.WriteError(w, r, e)
}

var errTooLarge = core.Error{Status: http.StatusRequestEntityTooLarge, Code: "PAYLOAD_TOO_LARGE", Message: "тело запроса больше 16 KiB"}

// readJSON проверяет media type, размер и строго разбирает тело. Возвращает каноническую форму.
func readJSON(r *http.Request, v any) ([]byte, error) {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		return nil, &core.Error{Status: http.StatusUnsupportedMediaType, Code: "UNSUPPORTED_MEDIA_TYPE", Message: "ожидался application/json"}
	}
	body, err := io.ReadAll(r.Body)
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		e := errTooLarge
		return nil, &e
	}
	if err != nil {
		return nil, core.BadRequest("INVALID_JSON", "не удалось прочитать тело запроса")
	}
	return httpx.Decode(body, v)
}

// mutation — общий путь авторизованной мутации (§21): ключ и тело проверяются до транзакции;
// в транзакции — захват пользователя, поиск квитанции, действие, запись квитанции, commit.
// Ошибка действия откатывает всё и ключ не расходует. Повтор с тем же ключом возвращает
// исходный статус и тело, а не выполняет действие снова.
func (s *server) mutation(w http.ResponseWriter, r *http.Request, userID, route string, req any,
	act func(ctx context.Context, tx pgx.Tx, now time.Time) (int, any, error)) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		httpx.WriteError(w, r, core.BadRequest("IDEMPOTENCY_KEY_REQUIRED", "нужен заголовок Idempotency-Key"))
		return
	}
	if !idem.ValidKey(key) {
		httpx.WriteError(w, r, core.Validation("Idempotency-Key", "UUID или 1–80 символов A-Z, a-z, 0-9, _ и -"))
		return
	}
	canonical, err := readJSON(r, req)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ir := idem.Request{Method: r.Method, Route: route, Path: r.URL.Path, Canonical: canonical}
	var rc idem.Receipt
	err = s.db.InTx(r.Context(), func(tx pgx.Tx) error {
		ctx := r.Context()
		if err := store.LockUser(ctx, tx, userID); err != nil {
			return err
		}
		prev, err := idem.Lookup(ctx, tx, userID, key, ir)
		if err != nil || prev != nil {
			if prev != nil {
				rc = *prev
			}
			return err
		}
		now := s.clock.Now()
		status, resp, err := act(ctx, tx, now)
		if err != nil {
			return err
		}
		body, err := json.Marshal(resp)
		if err != nil {
			return err
		}
		rc = idem.Receipt{Status: status, Body: body}
		return idem.Save(ctx, tx, userID, key, ir, rc, now)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteRaw(w, rc.Status, rc.Body)
}

func created(ok bool) int {
	if ok {
		return http.StatusCreated
	}
	return http.StatusOK
}
