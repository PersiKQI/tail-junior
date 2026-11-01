// Пакет notify — напоминания и приглашения (спецификация §17, §18): настройки и согласие,
// единственная установка с push token, шаблоны персонажей, выбор повода, атомарное
// резервирование квоты и dry-run доставка. Exactly once у внешнего провайдера не обещаем.
package notify

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	bankfiles "teyljunior/content"
	"teyljunior/internal/core"
	"teyljunior/internal/progression"
	"teyljunior/internal/store"
)

// Rules — параметры уведомлений из rules.json.
type Rules struct {
	Modes                  []string `json:"modes"`
	DefaultOffer           string   `json:"defaultOffer"`
	GlobalMaxPer24h        int      `json:"globalMaxPer24h"`
	ThreePerWeekMaxPer168h int      `json:"threePerWeekMaxPer168h"`
	DailyMaxPer168h        int      `json:"dailyMaxPer168h"`
	QuietHours             struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"quietHours"`
	AutoPause struct {
		AcceptedWithoutReturn int `json:"acceptedWithoutReturn"`
		WindowDays            int `json:"windowDays"`
		PauseDays             int `json:"pauseDays"`
	} `json:"autoPause"`
	DefaultDaysIso        []int  `json:"defaultDaysIso"`
	DefaultLocalTime      string `json:"defaultLocalTime"`
	DeliveryWindowMinutes int    `json:"deliveryWindowMinutes"`
	GentleReturnAfterDays int    `json:"gentleReturnAfterDays"`
	MaxProviderAttempts   int    `json:"maxProviderAttempts"`
}

// Template — отредактированная реплика персонажа.
type Template struct {
	TemplateID  string `json:"templateId"`
	Revision    int    `json:"revision"`
	CharacterID string `json:"characterId"`
	TriggerType string `json:"triggerType"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	TargetRoute string `json:"targetRoute"`
}

// Поводы приглашений в порядке приоритета (§18).
var triggerTypes = []string{"review_ready", "unfinished_attempt", "new_episode", "gentle_return", "streak"}

var categories = []string{"learning", "event", "streak"}

// Load читает правила и шаблоны и проверяет, что у каждого повода есть реплика каждого
// персонажа.
func Load() (*Rules, map[string]Template, error) {
	var all struct {
		Notifications Rules `json:"notifications"`
	}
	if err := json.Unmarshal(bankfiles.Rules, &all); err != nil {
		return nil, nil, fmt.Errorf("rules.json: %w", err)
	}
	r := all.Notifications
	if r.GlobalMaxPer24h <= 0 || r.DeliveryWindowMinutes <= 0 || r.MaxProviderAttempts <= 0 || len(r.DefaultDaysIso) == 0 {
		return nil, nil, errors.New("rules.json: неполные параметры уведомлений")
	}
	var file struct {
		Provenance string     `json:"provenance"`
		Templates  []Template `json:"templates"`
	}
	dec := json.NewDecoder(bytes.NewReader(bankfiles.NotificationTemplates))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return nil, nil, fmt.Errorf("notification-templates.json: %w", err)
	}
	tpl := map[string]Template{}
	for _, t := range file.Templates {
		if strings.ContainsAny(t.Body, "{}") {
			return nil, nil, fmt.Errorf("шаблон %s: подстановки запрещены", t.TemplateID)
		}
		tpl[t.TriggerType+"."+t.CharacterID] = t
	}
	for _, tt := range triggerTypes {
		for _, c := range []string{"lumi", "spark", "nova"} {
			if _, ok := tpl[tt+"."+c]; !ok {
				return nil, nil, fmt.Errorf("нет шаблона %s для персонажа %s", tt, c)
			}
		}
	}
	return &r, tpl, nil
}

// Service — настройки, устройство и доставка.
type Service struct {
	Rules     *Rules
	Templates map[string]Template
	Prog      *progression.Service
	IDs       core.IDs
	Clock     core.Clock
	Provider  Provider
	TokenKey  []byte // 32 байта AES-256-GCM для push token
	Lease     time.Duration
}

// Preferences — настройки напоминаний. Revision 0 — пользователь ещё не выбирал: режим off,
// предложение three_per_week не равно согласию (§18).
type Preferences struct {
	Revision       int        `json:"revision"`
	Mode           string     `json:"mode"`
	SuggestedMode  string     `json:"suggestedMode"`
	DaysIso        []int      `json:"daysIso"`
	LocalTime      string     `json:"localTime"`
	Categories     []string   `json:"categories"`
	PauseUntil     *string    `json:"pauseUntil"`
	AutoPauseUntil *time.Time `json:"autoPauseUntil"`
	QuietHours     [2]string  `json:"quietHours"`
	Permission     string     `json:"permission"`
	PushReady      bool       `json:"pushReady"`
}

// GetPreferences читает настройки и готовность установки.
func (s *Service) GetPreferences(ctx context.Context, q store.Querier, userID string) (Preferences, error) {
	p := Preferences{Mode: "off", SuggestedMode: s.Rules.DefaultOffer, DaysIso: s.Rules.DefaultDaysIso, LocalTime: s.Rules.DefaultLocalTime,
		Categories: slices.Clone(categories), QuietHours: [2]string{s.Rules.QuietHours.From, s.Rules.QuietHours.To}, Permission: "undetermined"}
	var pause *time.Time
	err := q.QueryRow(ctx, `SELECT revision, mode, days_iso, local_time, categories, pause_until, auto_pause_until
		FROM reminder_preferences WHERE user_id = $1`, userID).
		Scan(&p.Revision, &p.Mode, &p.DaysIso, &p.LocalTime, &p.Categories, &pause, &p.AutoPauseUntil)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return p, err
	}
	if pause != nil {
		p.PauseUntil = new(pause.Format(time.DateOnly))
	}
	if p.AutoPauseUntil != nil {
		p.AutoPauseUntil = new(p.AutoPauseUntil.UTC())
	}
	var hasToken bool
	err = q.QueryRow(ctx, `SELECT permission, push_token_ciphertext IS NOT NULL FROM devices WHERE user_id = $1`, userID).Scan(&p.Permission, &hasToken)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return p, err
	}
	p.PushReady = p.Mode != "off" && p.Permission == "granted" && hasToken
	return p, nil
}

// PreferencesInput — новые настройки.
type PreferencesInput struct {
	ExpectedRevision int
	Mode             string
	DaysIso          []int
	LocalTime        string
	Categories       []string
	PauseUntil       *string
}

func minutesOf(hhmm string) (int, bool) {
	t, err := time.Parse("15:04", hhmm)
	if err != nil || t.Format("15:04") != hhmm {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

// PutPreferences сохраняет согласие и расписание. Время выбирается вне тихих часов.
// Переход в off отменяет запланированные и зарезервированные задания; уже отправленное
// отозвать нельзя (§18).
func (s *Service) PutPreferences(ctx context.Context, tx pgx.Tx, userID string, in PreferencesInput, now time.Time) (Preferences, error) {
	cur, err := s.GetPreferences(ctx, tx, userID)
	if err != nil {
		return cur, err
	}
	if cur.Revision != in.ExpectedRevision {
		return cur, core.RevisionConflict("настройки уже изменились; открой свежую версию")
	}
	if !slices.Contains(s.Rules.Modes, in.Mode) {
		return cur, core.Validation("mode", "off, three_per_week или daily")
	}
	days := slices.Clone(in.DaysIso)
	slices.Sort(days)
	days = slices.Compact(days)
	for _, d := range days {
		if d < 1 || d > 7 {
			return cur, core.Validation("daysIso", "дни недели ISO 1–7")
		}
	}
	switch in.Mode {
	case "daily":
		days = []int{1, 2, 3, 4, 5, 6, 7}
	case "three_per_week":
		if len(days) == 0 || len(days) > 3 {
			return cur, core.Validation("daysIso", "от одного до трёх дней недели")
		}
	}
	m, ok := minutesOf(in.LocalTime)
	from, _ := minutesOf(s.Rules.QuietHours.To)    // 09:00 — начало разрешённого интервала
	until, _ := minutesOf(s.Rules.QuietHours.From) // 21:00 — начало тихих часов
	if !ok || m < from || m >= until {
		return cur, core.Validation("localTime", "ЧЧ:ММ с "+s.Rules.QuietHours.To+" до "+s.Rules.QuietHours.From)
	}
	cats := slices.Clone(in.Categories)
	slices.Sort(cats)
	cats = slices.Compact(cats)
	for _, c := range cats {
		if !slices.Contains(categories, c) {
			return cur, core.Validation("categories", "learning, event, streak")
		}
	}
	if in.Mode != "off" && len(cats) == 0 {
		return cur, core.Validation("categories", "выбери хотя бы одну тему")
	}
	var pause *time.Time
	if in.PauseUntil != nil {
		t, err := time.Parse(time.DateOnly, *in.PauseUntil)
		if err != nil || t.Format(time.DateOnly) != *in.PauseUntil {
			return cur, core.Validation("pauseUntil", "дата ГГГГ-ММ-ДД")
		}
		pause = &t
	}
	if days == nil {
		days = []int{}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO reminder_preferences (user_id, revision, mode, days_iso, local_time, categories, pause_until, updated_at)
		VALUES ($1, 1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (user_id) DO UPDATE SET revision = reminder_preferences.revision + 1, mode = $2, days_iso = $3,
		    local_time = $4, categories = $5, pause_until = $6, updated_at = $7`,
		userID, in.Mode, days, in.LocalTime, cats, pause, now); err != nil {
		return cur, err
	}
	if in.Mode == "off" {
		if _, err := tx.Exec(ctx, `UPDATE notification_jobs SET state = 'cancelled', lease_until = NULL
			WHERE user_id = $1 AND state IN ('planned', 'reserved')`, userID); err != nil {
			return cur, err
		}
	}
	return s.GetPreferences(ctx, tx, userID)
}

// DeviceInput — состояние единственной установки.
type DeviceInput struct {
	InstallationID string
	Platform       string
	Provider       string
	PushToken      *string
	Permission     string
}

// DeviceView — установка без токена: токен наружу не возвращается.
type DeviceView struct {
	InstallationID string    `json:"installationId"`
	Platform       string    `json:"platform"`
	Provider       string    `json:"provider"`
	Permission     string    `json:"permission"`
	HasPushToken   bool      `json:"hasPushToken"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// PutDevice обновляет единственную установку. Новая установка или новый токен заменяют
// прежние: старый токен больше не используется (§18).
func (s *Service) PutDevice(ctx context.Context, tx pgx.Tx, userID string, in DeviceInput, now time.Time) (DeviceView, error) {
	switch {
	case in.InstallationID == "" || len(in.InstallationID) > 128:
		return DeviceView{}, core.Validation("installationId", "от 1 до 128 символов")
	case !slices.Contains([]string{"ios", "android", "web"}, in.Platform):
		return DeviceView{}, core.Validation("platform", "ios, android или web")
	case !slices.Contains([]string{"expo", "fcm", "apns", "dryrun"}, in.Provider):
		return DeviceView{}, core.Validation("provider", "expo, fcm, apns или dryrun")
	case !slices.Contains([]string{"granted", "denied", "undetermined"}, in.Permission):
		return DeviceView{}, core.Validation("permission", "granted, denied или undetermined")
	case in.PushToken != nil && (*in.PushToken == "" || len(*in.PushToken) > 512):
		return DeviceView{}, core.Validation("pushToken", "от 1 до 512 символов")
	}
	var cipherText []byte
	var digest *string
	if in.PushToken != nil {
		var err error
		if cipherText, err = s.seal([]byte(*in.PushToken)); err != nil {
			return DeviceView{}, err
		}
		sum := sha256.Sum256([]byte(*in.PushToken))
		digest = new(hex.EncodeToString(sum[:]))
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO devices (user_id, installation_id, platform, provider, push_token_ciphertext, push_token_sha256, permission, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (user_id) DO UPDATE SET installation_id = $2, platform = $3, provider = $4, push_token_ciphertext = $5,
		    push_token_sha256 = $6, permission = $7, updated_at = $8`,
		userID, in.InstallationID, in.Platform, in.Provider, cipherText, digest, in.Permission, now); err != nil {
		return DeviceView{}, err
	}
	if in.Permission != "granted" {
		if _, err := tx.Exec(ctx, `UPDATE notification_jobs SET state = 'cancelled', lease_until = NULL
			WHERE user_id = $1 AND state IN ('planned', 'reserved')`, userID); err != nil {
			return DeviceView{}, err
		}
	}
	return DeviceView{InstallationID: in.InstallationID, Platform: in.Platform, Provider: in.Provider, Permission: in.Permission,
		HasPushToken: in.PushToken != nil, UpdatedAt: now.UTC()}, nil
}

func (s *Service) aead() (cipher.AEAD, error) {
	block, err := aes.NewCipher(s.TokenKey)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// seal шифрует push token: nonce || ciphertext.
func (s *Service) seal(plain []byte) ([]byte, error) {
	g, err := s.aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	rand.Read(nonce) //nolint:errcheck // crypto/rand.Read не возвращает ошибок
	return g.Seal(nonce, nonce, plain, nil), nil
}

func (s *Service) open(sealed []byte) (string, error) {
	g, err := s.aead()
	if err != nil {
		return "", err
	}
	if len(sealed) < g.NonceSize() {
		return "", errors.New("push token повреждён")
	}
	plain, err := g.Open(nil, sealed[:g.NonceSize()], sealed[g.NonceSize():], nil)
	return string(plain), err
}
