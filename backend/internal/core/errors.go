package core

import (
	"errors"
	"net/http"
)

// FieldError — ошибка конкретного поля запроса.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error — прикладная ошибка с HTTP-статусом и стабильным кодом (спецификация §20).
// Message — короткий текст для человека, без SQL, стека, токена и приватного контента.
type Error struct {
	Status      int
	Code        string
	Message     string
	FieldErrors []FieldError
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// WithField добавляет ошибку поля.
func (e *Error) WithField(field, msg string) *Error {
	c := *e
	c.FieldErrors = append(append([]FieldError(nil), e.FieldErrors...), FieldError{Field: field, Message: msg})
	return &c
}

func newErr(status int, code, msg string) *Error {
	return &Error{Status: status, Code: code, Message: msg}
}

// Конструкторы по статусам §20.
func BadRequest(code, msg string) *Error { return newErr(http.StatusBadRequest, code, msg) }
func Unauthorized() *Error {
	return newErr(http.StatusUnauthorized, "UNAUTHORIZED", "профиль не найден или токен недействителен")
}
func NotFound() *Error {
	return newErr(http.StatusNotFound, "NOT_FOUND", "объект не найден")
}
func Conflict(code, msg string) *Error      { return newErr(http.StatusConflict, code, msg) }
func Gone(code, msg string) *Error          { return newErr(http.StatusGone, code, msg) }
func Unprocessable(code, msg string) *Error { return newErr(http.StatusUnprocessableEntity, code, msg) }
func Unavailable(code, msg string) *Error   { return newErr(http.StatusServiceUnavailable, code, msg) }
func Internal() *Error {
	return newErr(http.StatusInternalServerError, "INTERNAL", "внутренняя ошибка сервиса")
}
func Validation(field, msg string) *Error {
	return Unprocessable("VALIDATION_FAILED", "проверьте поля запроса").WithField(field, msg)
}
func RevisionConflict(msg string) *Error { return Conflict("REVISION_CONFLICT", msg) }
func IdempotencyConflict() *Error {
	return Conflict("IDEMPOTENCY_CONFLICT", "ключ идемпотентности уже использован для другого запроса")
}

// AsError приводит любую ошибку к *Error; неизвестная — Internal.
func AsError(err error) *Error {
	if e, ok := errors.AsType[*Error](err); ok {
		return e
	}
	return Internal()
}
