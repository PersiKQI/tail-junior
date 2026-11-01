// Пакет app собирает прикладные сервисы из общих зависимостей. Один набор используют HTTP
// API, worker и teylctl, чтобы правила не расходились между режимами запуска.
package app

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"

	bankfiles "teyljunior/content"
	"teyljunior/internal/assessment"
	"teyljunior/internal/challenge"
	"teyljunior/internal/content"
	"teyljunior/internal/core"
	"teyljunior/internal/lab"
	"teyljunior/internal/learning"
	"teyljunior/internal/legacy"
	"teyljunior/internal/money"
	"teyljunior/internal/notify"
	"teyljunior/internal/progression"
	"teyljunior/internal/store"
)

// Deps — общие зависимости.
type Deps struct {
	DB           *store.DB
	Clock        core.Clock
	IDs          core.IDs
	Shuffler     core.Shuffler
	PushTokenKey []byte          // 32 байта; nil — случайный ключ процесса (только разработка)
	Provider     notify.Provider // nil — DryRun
}

// Services — собранные сервисы.
type Services struct {
	Catalog   *content.Catalog
	Prog      *progression.Service
	Assess    *assessment.Service
	Learning  *learning.Service
	Lab       *lab.Service
	Money     *money.Service
	Challenge *challenge.Service
	D7        *legacy.D7
	Notify    *notify.Service
	Legacy    *legacy.Importer
}

// RateLimits — пределы частоты из rules.json.
type RateLimits struct {
	SessionsPerIPPerHour     int `json:"sessionsPerIpPerHour"`
	RequestsPerUserPerMinute int `json:"requestsPerUserPerMinute"`
}

// LoadRateLimits читает раздел rateLimits.
func LoadRateLimits() (RateLimits, error) {
	var all struct {
		RateLimits RateLimits `json:"rateLimits"`
	}
	if err := json.Unmarshal(bankfiles.Rules, &all); err != nil {
		return RateLimits{}, err
	}
	return all.RateLimits, nil
}

// leaseDefault — сколько отправка уведомления закреплена за обработчиком.
const leaseDefault = 2 * time.Minute

// New проверяет встроенный комплект правил и собирает сервисы.
func New(d Deps) (*Services, error) {
	catalog := content.NewCatalog(d.DB)
	rules, err := progression.LoadRules()
	if err != nil {
		return nil, err
	}
	d7, err := legacy.LoadD7()
	if err != nil {
		return nil, err
	}
	notifyRules, templates, err := notify.Load()
	if err != nil {
		return nil, err
	}
	key := d.PushTokenKey
	if key == nil {
		key = make([]byte, 32)
		rand.Read(key) //nolint:errcheck // crypto/rand.Read не возвращает ошибок
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("ключ push token: нужно 32 байта, получено %d", len(key))
	}
	provider := d.Provider
	if provider == nil {
		provider = &notify.DryRun{}
	}
	prog := &progression.Service{Catalog: catalog, IDs: d.IDs, Rules: rules}
	assess := &assessment.Service{Catalog: catalog, IDs: d.IDs, Prog: prog}
	learn := &learning.Service{Catalog: catalog, IDs: d.IDs, Shuffle: d.Shuffler, Assess: assess, Prog: prog}
	labSvc := &lab.Service{IDs: d.IDs, Prog: prog}
	return &Services{
		Catalog: catalog, Prog: prog, Assess: assess, Learning: learn, Lab: labSvc,
		Money:     &money.Service{IDs: d.IDs},
		Challenge: &challenge.Service{Lab: labSvc, Prog: prog, IDs: d.IDs},
		D7:        &legacy.D7{Content: d7},
		Notify: &notify.Service{Rules: notifyRules, Templates: templates, Prog: prog, IDs: d.IDs, Clock: d.Clock,
			Provider: provider, TokenKey: key, Lease: leaseDefault},
		Legacy: &legacy.Importer{DB: d.DB, Catalog: catalog, Learning: learn, IDs: d.IDs, Clock: d.Clock},
	}, nil
}
