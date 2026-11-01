// Команда teylctl — редакторские операции с содержанием.
//
//	teylctl migrate                          применить миграции
//	teylctl validate                         проверить встроенный комплект без базы
//	teylctl import                           импортировать комплект и кейс лаборатории как draft
//	teylctl publish -version V -approved-by N  опубликовать поставку после утверждения
//	teylctl publish-lab -case C -revision R -approved-by N  опубликовать кейс лаборатории
//	teylctl publish-challenge -family F -revision R -approved-by N  опубликовать событие
//	teylctl import-legacy -sqlite S [-backup-dir D]  перенести снимок SQLite текущего сервера
//	teylctl withdraw -item I -revision R -grading-revision G -reason T
//	                                         отозвать редакцию с существенной ошибкой
//
// import и publish используют TEYL_DATABASE_URL. Флаги -program и -adapter позволяют
// проверить или импортировать файлы с диска вместо встроенных.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	bankfiles "teyljunior/content"
	"teyljunior/internal/app"
	"teyljunior/internal/challenge"
	"teyljunior/internal/content"
	"teyljunior/internal/core"
	"teyljunior/internal/lab"
	"teyljunior/internal/store"
	"teyljunior/migrations"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "teylctl:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("команда: migrate | validate | import | publish | publish-lab | publish-challenge | import-legacy | withdraw")
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	programPath := fs.String("program", "", "путь к learning-program-server-draft.json (по умолчанию встроенный)")
	adapterPath := fs.String("adapter", "", "путь к content-adapter.json (по умолчанию встроенный)")
	version := fs.String("version", "", "версия поставки для publish")
	approvedBy := fs.String("approved-by", "", "редактор, утвердивший поставку")
	item := fs.String("item", "", "itemId отзываемой редакции")
	revision := fs.Int("revision", 1, "revision отзываемой редакции")
	grading := fs.Int("grading-revision", 1, "gradingRevision отзываемой редакции")
	locale := fs.String("locale", "ru-RU", "locale отзываемой редакции")
	reason := fs.String("reason", "", "причина отзыва для истории")
	labPath := fs.String("lab", "", "путь к lab-case-LB01.json (по умолчанию встроенный)")
	caseID := fs.String("case", "", "caseFamilyId для publish-lab")
	family := fs.String("family", "", "familyId события для publish-challenge")
	sqlitePath := fs.String("sqlite", "", "снимок SQLite текущего сервера для import-legacy")
	backupDir := fs.String("backup-dir", "legacy-backups", "каталог резервной копии и манифеста import-legacy")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	switch args[0] {
	case "migrate":
		dsn := os.Getenv("TEYL_DATABASE_URL")
		if dsn == "" {
			return errors.New("TEYL_DATABASE_URL is not set")
		}
		return migrations.Up(ctx, dsn)
	case "validate":
		b, err := loadBundle(*programPath, *adapterPath)
		if err != nil {
			return err
		}
		pack, err := loadLab(*labPath)
		if err != nil {
			return err
		}
		fmt.Printf("ok: %s, %d карточек, sha256 %s; lab %s r%d, модель %s\n", b.Program.ProgramVersion, len(b.Program.Items), b.SHA256,
			pack.Case.CaseFamilyID, pack.Case.Revision, pack.Model.Version)
		return nil
	case "import":
		b, err := loadBundle(*programPath, *adapterPath)
		if err != nil {
			return err
		}
		db, err := openDB(ctx)
		if err != nil {
			return err
		}
		defer db.Close()
		if err := content.Import(ctx, db, b, core.SystemClock{}.Now()); err != nil {
			return err
		}
		pack, err := loadLab(*labPath)
		if err != nil {
			return err
		}
		if err := lab.Import(ctx, db, pack, core.SystemClock{}.Now()); err != nil {
			return err
		}
		ch, err := challenge.ParseRelease(bankfiles.Challenge)
		if err != nil {
			return err
		}
		if err := challenge.Import(ctx, db, ch, core.SystemClock{}.Now()); err != nil {
			return err
		}
		fmt.Printf("imported draft %s, lab case %s r%d, challenge %s r%d\n", b.Program.ProgramVersion,
			pack.Case.CaseFamilyID, pack.Case.Revision, ch.FamilyID, ch.Revision)
		return nil
	case "publish-challenge":
		if *family == "" {
			return errors.New("нужен -family")
		}
		db, err := openDB(ctx)
		if err != nil {
			return err
		}
		defer db.Close()
		if err := challenge.Publish(ctx, db, *family, *revision, *approvedBy, core.SystemClock{}.Now()); err != nil {
			return err
		}
		fmt.Printf("published challenge %s r%d (approved by %s)\n", *family, *revision, *approvedBy)
		return nil
	case "import-legacy":
		if *sqlitePath == "" {
			return errors.New("нужен -sqlite")
		}
		db, err := openDB(ctx)
		if err != nil {
			return err
		}
		defer db.Close()
		services, err := services(db)
		if err != nil {
			return err
		}
		m, err := services.Legacy.Run(ctx, *sqlitePath, *backupDir)
		if err != nil {
			return err
		}
		out, _ := json.MarshalIndent(m, "", "  ")
		fmt.Println(string(out))
		if len(m.Problems) > 0 {
			return fmt.Errorf("%d сессий не перенесено, подробности в манифесте", len(m.Problems))
		}
		return nil
	case "publish-lab":
		if *caseID == "" {
			return errors.New("нужен -case")
		}
		db, err := openDB(ctx)
		if err != nil {
			return err
		}
		defer db.Close()
		if err := lab.Publish(ctx, db, *caseID, *revision, *approvedBy, core.SystemClock{}.Now()); err != nil {
			return err
		}
		fmt.Printf("published lab case %s r%d (approved by %s)\n", *caseID, *revision, *approvedBy)
		return nil
	case "publish":
		if *version == "" {
			return errors.New("нужен -version")
		}
		db, err := openDB(ctx)
		if err != nil {
			return err
		}
		defer db.Close()
		if err := content.Publish(ctx, db, *version, *approvedBy, core.SystemClock{}.Now()); err != nil {
			return err
		}
		fmt.Printf("published %s (approved by %s)\n", *version, *approvedBy)
		return nil
	case "withdraw":
		if *item == "" || *reason == "" {
			return errors.New("нужны -item и -reason")
		}
		db, err := openDB(ctx)
		if err != nil {
			return err
		}
		defer db.Close()
		services, err := services(db)
		if err != nil {
			return err
		}
		rep, err := services.Learning.WithdrawRevision(ctx, db, content.RevisionKey{ItemID: *item, Revision: *revision, GradingRevision: *grading, Locale: *locale},
			*reason, core.SystemClock{}.Now())
		if err != nil {
			return err
		}
		fmt.Printf("withdrawn %s r%d g%d: users %d, attempts %d, replaced %d, closed %d, sets invalidated %d\n",
			*item, *revision, *grading, rep.Users, rep.AttemptsWithdrawn, rep.AssignmentsReplaced, rep.AssignmentsClosed, rep.SetsInvalidated)
		return nil
	default:
		return fmt.Errorf("неизвестная команда %q", args[0])
	}
}

func loadBundle(programPath, adapterPath string) (*content.Bundle, error) {
	program, adapter := bankfiles.Program, bankfiles.Adapter
	var err error
	if programPath != "" {
		if program, err = os.ReadFile(programPath); err != nil {
			return nil, err
		}
	}
	if adapterPath != "" {
		if adapter, err = os.ReadFile(adapterPath); err != nil {
			return nil, err
		}
	}
	return content.ParseBundle(program, adapter)
}

// services — прикладные сервисы для редакторских операций. Уведомления teylctl не
// отправляет, поэтому ключ push token случайный.
func services(db *store.DB) (*app.Services, error) {
	return app.New(app.Deps{DB: db, Clock: core.SystemClock{}, IDs: core.RandomIDs{}, Shuffler: core.CryptoShuffler{}})
}

func loadLab(path string) (*lab.Pack, error) {
	data := bankfiles.LabCase
	if path != "" {
		var err error
		if data, err = os.ReadFile(path); err != nil {
			return nil, err
		}
	}
	return lab.ParsePack(data)
}

func openDB(ctx context.Context) (*store.DB, error) {
	dsn := os.Getenv("TEYL_DATABASE_URL")
	if dsn == "" {
		return nil, errors.New("TEYL_DATABASE_URL is not set")
	}
	return store.Open(ctx, dsn)
}
