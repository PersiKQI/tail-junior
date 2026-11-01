// Пакет ratelimit — ограничение частоты запросов фиксированным окном (спецификация §20: 429).
// Состояние в памяти процесса: при нескольких экземплярах API предел действует на каждый
// отдельно, этого достаточно для пилота.
package ratelimit

import (
	"sync"
	"time"

	"teyljunior/internal/core"
)

// maxKeys — после этого числа ключей устаревшие окна вычищаются.
const maxKeys = 10_000

type window struct {
	start time.Time
	count int
}

// Limiter — не больше limit событий на ключ за period.
type Limiter struct {
	mu     sync.Mutex
	clock  core.Clock
	limit  int
	period time.Duration
	keys   map[string]*window
}

// New создаёт ограничитель; limit ≤ 0 — без ограничения.
func New(clock core.Clock, limit int, period time.Duration) *Limiter {
	return &Limiter{clock: clock, limit: limit, period: period, keys: map[string]*window{}}
}

// Allow учитывает событие и сообщает, разрешено ли оно; при отказе — через сколько повторить.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	if l == nil || l.limit <= 0 {
		return true, 0
	}
	now := l.clock.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.keys) > maxKeys {
		for k, w := range l.keys {
			if now.Sub(w.start) >= l.period {
				delete(l.keys, k)
			}
		}
	}
	w, ok := l.keys[key]
	if !ok || now.Sub(w.start) >= l.period {
		w = &window{start: now}
		l.keys[key] = w
	}
	if w.count >= l.limit {
		return false, w.start.Add(l.period).Sub(now)
	}
	w.count++
	return true, 0
}
