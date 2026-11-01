// Команда teylapi — HTTP API ТЭЙЛ /api/v2 и фоновый worker в одном исполняемом файле (§3).
//
// Переменные окружения:
//
//	TEYL_DATABASE_URL   адрес PostgreSQL (обязательно)
//	TEYL_ADDR           адрес прослушивания API, по умолчанию 127.0.0.1:8790
//	TEYL_PUSH_KEY       base64 32 байта для шифрования push token; одинаковый у api и worker
//	TEYL_ANALYTICS_KEY  ключ псевдонимов пользователей в экспорте аналитики
//	TEYL_WORKER_INTERVAL интервал проходов worker, по умолчанию 30s
//
// Флаги: -migrate применяет миграции; -mode api | worker | all.
// Уведомления отправляются только dry-run провайдером: реальная доставка людям в пилоте не
// включается (§18).
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"teyljunior/internal/analytics"
	"teyljunior/internal/api"
	"teyljunior/internal/app"
	"teyljunior/internal/core"
	"teyljunior/internal/notify"
	"teyljunior/internal/store"
	"teyljunior/internal/worker"
	"teyljunior/migrations"
)

const shutdownTimeout = 10 * time.Second

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	migrate := flag.Bool("migrate", false, "применить миграции перед запуском")
	mode := flag.String("mode", "api", "api | worker | all")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *migrate, *mode, log); err != nil {
		log.Error("teylapi stopped", "err", err)
		os.Exit(1)
	}
}

// key читает ключ из окружения: base64 для бинарного ключа, иначе строка как есть. Без
// значения — случайный ключ процесса с предупреждением (только для разработки).
func key(name string, binary bool, log *slog.Logger) ([]byte, error) {
	raw := os.Getenv(name)
	if raw == "" {
		log.Warn(name + " не задан: используется случайный ключ процесса, данные с ним не переживут перезапуск")
		k := make([]byte, 32)
		rand.Read(k) //nolint:errcheck // crypto/rand.Read не возвращает ошибок
		return k, nil
	}
	if !binary {
		return []byte(raw), nil
	}
	k, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, errors.New(name + ": ожидался base64")
	}
	return k, nil
}

func run(ctx context.Context, migrate bool, mode string, log *slog.Logger) error {
	if mode != "api" && mode != "worker" && mode != "all" {
		return errors.New("-mode: api, worker или all")
	}
	dsn := os.Getenv("TEYL_DATABASE_URL")
	if dsn == "" {
		return errors.New("TEYL_DATABASE_URL is not set")
	}
	if migrate {
		if err := migrations.Up(ctx, dsn); err != nil {
			return err
		}
	}
	db, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	pushKey, err := key("TEYL_PUSH_KEY", true, log)
	if err != nil {
		return err
	}
	analyticsKey, err := key("TEYL_ANALYTICS_KEY", false, log)
	if err != nil {
		return err
	}
	clock, ids := core.SystemClock{}, core.RandomIDs{}
	services, err := app.New(app.Deps{DB: db, Clock: clock, IDs: ids, Shuffler: core.CryptoShuffler{}, PushTokenKey: pushKey,
		Provider: &notify.DryRun{Log: log}})
	if err != nil {
		return err
	}

	g, ctx := errgroup.WithContext(ctx)
	if mode == "api" || mode == "all" {
		limits, err := app.LoadRateLimits()
		if err != nil {
			return err
		}
		addr := os.Getenv("TEYL_ADDR")
		if addr == "" {
			addr = "127.0.0.1:8790"
		}
		srv := &http.Server{
			Addr: addr,
			Handler: api.New(api.Deps{DB: db, Clock: clock, IDs: ids, Services: services, Logger: log,
				Limits: api.Limits{SessionsPerIPPerHour: limits.SessionsPerIPPerHour, RequestsPerUserPerMinute: limits.RequestsPerUserPerMinute}}),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
		}
		g.Go(func() error {
			log.Info("teylapi listening", "addr", addr)
			if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		})
		g.Go(func() error {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			return srv.Shutdown(shutdownCtx)
		})
	}
	if mode == "worker" || mode == "all" {
		interval := 30 * time.Second
		if raw := os.Getenv("TEYL_WORKER_INTERVAL"); raw != "" {
			if interval, err = time.ParseDuration(raw); err != nil {
				return err
			}
		}
		w := &worker.Worker{DB: db, Notify: services.Notify, Interval: interval, Log: log,
			Exporter: &analytics.Exporter{DB: db, Sink: analytics.LogSink{Log: log}, Clock: clock, SubjectKey: analyticsKey,
				Lease: time.Minute, Batch: 100}}
		g.Go(func() error {
			log.Info("worker started", "interval", interval.String())
			return w.Run(ctx)
		})
	}
	return g.Wait()
}
