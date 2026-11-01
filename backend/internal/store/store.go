// Пакет store — пул PostgreSQL и транзакции с ограниченным повтором (спецификация §3, §21).
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"teyljunior/internal/core"
)

// maxTxAttempts — сколько раз выполняется транзакция при deadlock или serialization failure.
const maxTxAttempts = 3

// Querier — общее подмножество пула и транзакции для чтений.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// DB — пул соединений.
type DB struct{ Pool *pgxpool.Pool }

// Open подключается к базе dsn и проверяет соединение.
func Open(ctx context.Context, dsn string) (*DB, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &DB{Pool: pool}, nil
}

func (db *DB) Close() { db.Pool.Close() }

// InTx выполняет fn в транзакции. При deadlock (40P01) или serialization failure (40001)
// повторяется вся транзакция целиком, а не отдельный INSERT (§21). Ошибка fn откатывает всё.
func (db *DB) InTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	var err error
	for range maxTxAttempts {
		err = pgx.BeginFunc(ctx, db.Pool, fn)
		if !retryable(err) {
			return err
		}
	}
	return err
}

func retryable(err error) bool {
	if pe, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pe.Code == "40001" || pe.Code == "40P01"
	}
	return false
}

// IsUniqueViolation — нарушено уникальное ограничение name (пусто — любое).
func IsUniqueViolation(err error, name string) bool {
	pe, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pe.Code == "23505" && (name == "" || pe.ConstraintName == name)
}

// LockUser захватывает строку пользователя: в MVP это сериализует его взаимозависимые записи
// (§3). Удалённый между аутентификацией и транзакцией пользователь — 401.
func LockUser(ctx context.Context, tx pgx.Tx, userID string) error {
	var one int
	err := tx.QueryRow(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Unauthorized()
	}
	return err
}
