// Пакет idem — квитанции идемпотентных мутаций (спецификация §21). Область уникальности —
// пользователь плюс ключ. Квитанция записывается в той же транзакции, что и действие, и только
// для успешного результата: ключ ошибки не расходуется.
package idem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"

	"teyljunior/internal/core"
)

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

// ValidKey — UUID либо 1–80 ASCII букв, цифр, дефисов и подчёркиваний. UUID в каноническом
// виде этому шаблону тоже соответствует.
func ValidKey(k string) bool { return keyPattern.MatchString(k) }

// Request — то, с чем связан ключ: метод, шаблон маршрута, конкретный путь и тело.
type Request struct {
	Method    string
	Route     string
	Path      string
	Canonical []byte
}

func (r Request) hash() string {
	h := sha256.New()
	for _, part := range [][]byte{[]byte(r.Method), []byte(r.Path), r.Canonical} {
		h.Write(part)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Receipt — сохранённый ответ.
type Receipt struct {
	Status int
	Body   []byte
}

// Lookup ищет квитанцию. Тот же ключ с другим методом, ресурсом или телом — 409.
func Lookup(ctx context.Context, tx pgx.Tx, userID, key string, req Request) (*Receipt, error) {
	var method, path, sum string
	var rc Receipt
	err := tx.QueryRow(ctx, `
		SELECT method, request_path, canonical_request_sha256, http_status, public_response
		FROM idempotency_receipts WHERE user_id = $1 AND key = $2`, userID, key).
		Scan(&method, &path, &sum, &rc.Status, &rc.Body)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if method != req.Method || path != req.Path || sum != req.hash() {
		return nil, core.IdempotencyConflict()
	}
	return &rc, nil
}

// Save записывает квитанцию успешного ответа.
func Save(ctx context.Context, tx pgx.Tx, userID, key string, req Request, rc Receipt, now time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO idempotency_receipts (user_id, key, method, route, request_path,
		                                  canonical_request_sha256, http_status, public_response, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		userID, key, req.Method, req.Route, req.Path, req.hash(), rc.Status, rc.Body, now)
	return err
}
