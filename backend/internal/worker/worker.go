// Пакет worker — фоновые задачи (спецификация §3): экспорт outbox и доставка приглашений.
// Обработчики повторяемы; при остановленном worker ответы и прогресс сохраняются, задерживаются
// только уведомления и экспорт аналитики.
package worker

import (
	"context"
	"log/slog"
	"time"

	"teyljunior/internal/analytics"
	"teyljunior/internal/notify"
	"teyljunior/internal/store"
)

// maxBatches — сколько партий outbox обрабатывается за один проход.
const maxBatches = 20

// Worker выполняет проходы с заданным интервалом.
type Worker struct {
	DB       *store.DB
	Exporter *analytics.Exporter
	Notify   *notify.Service
	Interval time.Duration
	Log      *slog.Logger
}

// Tick — один проход: outbox до опустошения (не больше maxBatches партий), затем доставка.
func (w *Worker) Tick(ctx context.Context) error {
	for range maxBatches {
		n, err := w.Exporter.Tick(ctx)
		if err != nil {
			return err
		}
		if n == 0 {
			break
		}
	}
	_, err := w.Notify.Tick(ctx, w.DB)
	return err
}

// Run повторяет проходы до отмены контекста. Ошибка прохода журналируется и не останавливает
// worker: следующая попытка будет на следующем интервале.
func (w *Worker) Run(ctx context.Context) error {
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	for {
		if err := w.Tick(ctx); err != nil && ctx.Err() == nil {
			w.Log.Error("worker tick failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
