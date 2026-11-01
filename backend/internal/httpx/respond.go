package httpx

import (
	"context"
	"encoding/json"
	"net/http"

	"teyljunior/internal/core"
)

type ctxKey int

const requestIDKey ctxKey = 0

// WithRequestID кладёт идентификатор запроса в контекст.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID — идентификатор текущего запроса.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// errorBody — формат ошибки §20: code, message, requestId, необязательные fieldErrors.
type errorBody struct {
	Code        string            `json:"code"`
	Message     string            `json:"message"`
	RequestID   string            `json:"requestId"`
	FieldErrors []core.FieldError `json:"fieldErrors,omitempty"`
}

// WriteError пишет ошибку в едином формате.
func WriteError(w http.ResponseWriter, r *http.Request, e *core.Error) {
	body, _ := json.Marshal(errorBody{Code: e.Code, Message: e.Message, RequestID: RequestID(r.Context()), FieldErrors: e.FieldErrors})
	WriteRaw(w, e.Status, body)
}

// WriteJSON сериализует ответ.
func WriteJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		WriteError(w, r, core.Internal())
		return
	}
	WriteRaw(w, status, body)
}

// WriteRaw пишет готовое JSON-тело. Личные данные не кешируются посредниками.
func WriteRaw(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(body) //nolint:errcheck // клиент мог закрыть соединение
}
