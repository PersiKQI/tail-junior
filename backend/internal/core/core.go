// Пакет core — общие примитивы сервиса: время, идентификаторы, случайный порядок и прикладные
// ошибки. Время и случайность внедряются через интерфейсы (спецификация §3), чтобы тесты
// проверяли даты, повторы и порядок вариантов без ожидания реального времени.
package core

import (
	"crypto/rand"
	"math/big"
	"strings"
	"sync"
	"time"
)

// Clock — источник текущего времени.
type Clock interface{ Now() time.Time }

// SystemClock — реальное время UTC с точностью до микросекунд: столько хранит timestamptz,
// и значение в ответе совпадает с прочитанным из базы.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

// FixedClock — управляемые часы для тестов; безопасны для конкурентного чтения.
type FixedClock struct {
	mu sync.Mutex
	t  time.Time
}

func NewFixedClock(t time.Time) *FixedClock { return &FixedClock{t: t.UTC()} }

func (c *FixedClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// Set переводит часы на t (в UTC).
func (c *FixedClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t.UTC()
}

// Advance сдвигает часы на d.
func (c *FixedClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// IDs выдаёт идентификаторы сущностей.
type IDs interface{ New(prefix string) string }

// RandomIDs — prefix_ + 20 символов base32 из crypto/rand (100 бит).
type RandomIDs struct{}

func (RandomIDs) New(prefix string) string {
	return prefix + "_" + strings.ToLower(rand.Text()[:20])
}

// Shuffler задаёт порядок вариантов карточки при выдаче попытки.
type Shuffler interface{ Perm(n int) []int }

// CryptoShuffler — перестановка Фишера — Йетса на crypto/rand.
type CryptoShuffler struct{}

func (CryptoShuffler) Perm(n int) []int {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	for i := n - 1; i > 0; i-- {
		j, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			panic(err) // crypto/rand не возвращает ошибок на поддерживаемых платформах
		}
		p[i], p[j.Int64()] = p[j.Int64()], p[i]
	}
	return p
}

// Префиксы идентификаторов.
const (
	PrefixUser       = "usr"
	PrefixCredential = "crd"
	PrefixAssignment = "asg"
	PrefixAttempt    = "att"
	PrefixSubmission = "sub"
	PrefixEvaluation = "evl"
	PrefixAssistance = "ast"
	PrefixExposure   = "exp"
	PrefixSet        = "set"
)
