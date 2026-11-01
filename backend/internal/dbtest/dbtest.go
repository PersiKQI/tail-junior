// Пакет dbtest — временная база PostgreSQL с применёнными миграциями для интеграционных тестов.
// Нужен TEST_DATABASE_URL (make test-db && make test-pg); без него тесты пропускаются.
package dbtest

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"teyljunior/migrations"
)

// EnvURL — переменная с адресом административной базы, в которой создаются временные.
const EnvURL = "TEST_DATABASE_URL"

// DSN создаёт отдельную базу teyl_test_<nanos>_<rand> с применёнными миграциями и удаляет её
// после теста. Своя база на тест: тесты не видят данных друг друга и идут параллельно.
func DSN(t testing.TB) string {
	t.Helper()
	adminDSN := os.Getenv(EnvURL)
	if adminDSN == "" {
		t.Skip(EnvURL + " is not set")
	}
	ctx := t.Context()
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("dbtest: connect admin: %v", err)
	}
	// Имя из цифр и строчных букв base32 — безопасно подставлять в DDL без кавычек.
	name := fmt.Sprintf("teyl_test_%d_%s", time.Now().UnixNano(), strings.ToLower(rand.Text()[:6]))
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close(context.Background())
		t.Fatalf("dbtest: create database: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("dbtest: drop database %s: %v", name, err)
		}
		admin.Close(ctx)
	})
	u, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatalf("dbtest: parse %s: %v", EnvURL, err)
	}
	u.Path = "/" + name
	dsn := u.String()
	if err := migrations.Up(ctx, dsn); err != nil {
		t.Fatalf("dbtest: migrate: %v", err)
	}
	return dsn
}
