// Пакет migrations — SQL-миграции goose, встроенные в бинарники (teylapi -migrate, dbtest).
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sync"

	_ "github.com/jackc/pgx/v5/stdlib" // драйвер "pgx" для database/sql
	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var FS embed.FS

// gooseMu сериализует запуски: goose хранит FS и диалект в глобальных переменных, а тесты
// создают базы параллельно.
var gooseMu sync.Mutex

// Up применяет все миграции к базе dsn.
func Up(ctx context.Context, dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()
	gooseMu.Lock()
	defer gooseMu.Unlock()
	goose.SetBaseFS(FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}
