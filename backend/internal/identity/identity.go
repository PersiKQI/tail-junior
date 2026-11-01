// Пакет identity — анонимный профиль на одном устройстве (спецификация §4): создание
// сессии, аутентификация по opaque token, профиль и удаление аккаунта.
package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
	_ "time/tzdata" // IANA-зоны встроены: проверка learningTimezone не зависит от ОС (§15)
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"teyljunior/internal/core"
	"teyljunior/internal/store"
)

// tokenBytes — 256 бит случайности (§4).
const tokenBytes = 32

const (
	nicknameMaxRunes     = 40
	installationIDMaxLen = 128
)

// NewToken — случайный opaque token в base64url без выравнивания.
func NewToken() string {
	b := make([]byte, tokenBytes)
	rand.Read(b) //nolint:errcheck // crypto/rand.Read не возвращает ошибок
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashToken — SHA-256 UTF-8 строки токена, а не декодированных байтов: так совместимы
// credential, перенесённые из текущего Node backend (§4, §23).
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// Authenticate возвращает userId активного credential.
func Authenticate(ctx context.Context, q store.Querier, token string) (string, error) {
	if token == "" {
		return "", core.Unauthorized()
	}
	var userID string
	err := q.QueryRow(ctx, `SELECT user_id FROM credentials WHERE token_hash = $1 AND revoked_at IS NULL`, HashToken(token)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", core.Unauthorized()
	}
	return userID, err
}

// CreateUser создаёт пользователя и его единственный активный credential. Токен
// возвращается один раз и на сервере не хранится.
func CreateUser(ctx context.Context, tx pgx.Tx, ids core.IDs, now time.Time, installationID string) (userID, token string, err error) {
	if installationID != "" && (len(installationID) > installationIDMaxLen || !printableASCII(installationID)) {
		return "", "", core.Validation("installationId", "до 128 печатных ASCII-символов")
	}
	userID = ids.New(core.PrefixUser)
	token = NewToken()
	var inst *string
	if installationID != "" {
		inst = &installationID
	}
	if _, err = tx.Exec(ctx, `INSERT INTO users (id, created_at, installation_id) VALUES ($1, $2, $3)`, userID, now, inst); err != nil {
		return "", "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO credentials (id, user_id, token_hash, created_at) VALUES ($1, $2, $3, $4)`,
		ids.New(core.PrefixCredential), userID, HashToken(token), now)
	return userID, token, err
}

// Profile — публичная проекция профиля.
type Profile struct {
	UserID           string    `json:"userId"`
	Nickname         *string   `json:"nickname"`
	AgeGroup         *string   `json:"ageGroup"`
	CharacterID      *string   `json:"characterId"`
	ReducedMotion    bool      `json:"reducedMotion"`
	Route            string    `json:"route"`
	LearningTimezone *string   `json:"learningTimezone"`
	TimezoneLocked   bool      `json:"timezoneLocked"`
	AnalyticsEnabled bool      `json:"analyticsEnabled"`
	Revision         int       `json:"revision"`
	CreatedAt        time.Time `json:"createdAt"`
}

// GetProfile читает профиль пользователя.
func GetProfile(ctx context.Context, q store.Querier, userID string) (Profile, error) {
	var p Profile
	var locked *time.Time
	err := q.QueryRow(ctx, `
		SELECT id, nickname, age_group, character_id, reduced_motion, route, learning_timezone,
		       timezone_locked_at, analytics_enabled, profile_revision, created_at
		FROM users WHERE id = $1`, userID).
		Scan(&p.UserID, &p.Nickname, &p.AgeGroup, &p.CharacterID, &p.ReducedMotion, &p.Route,
			&p.LearningTimezone, &locked, &p.AnalyticsEnabled, &p.Revision, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, core.Unauthorized()
	}
	p.TimezoneLocked = locked != nil
	p.CreatedAt = p.CreatedAt.UTC()
	return p, err
}

// Patch — изменение профиля. Nil-поле не меняется; Null-значение очищает необязательное поле.
type Patch struct {
	Nickname         Opt[string]
	AgeGroup         Opt[string]
	CharacterID      Opt[string]
	ReducedMotion    Opt[bool]
	Route            Opt[string]
	LearningTimezone Opt[string]
	// AnalyticsEnabled — опциональный экспорт и клиентская аналитика. Выключение не ломает XP
	// и ответы: операционные факты записываются всегда (§22).
	AnalyticsEnabled Opt[bool]
}

// Opt — значение поля PATCH: Set — поле передано, Null — передано null.
type Opt[T any] struct {
	Set   bool
	Null  bool
	Value T
}

// UnmarshalJSON вызывается только для присутствующего поля, поэтому отличает «не передано»
// от null.
func (o *Opt[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Null = true
		return nil
	}
	return json.Unmarshal(b, &o.Value)
}

// UpdateProfile применяет патч при совпадении expectedRevision (§20). Учебный пояс после
// первого зачётного дня фиксирован (§15).
func UpdateProfile(ctx context.Context, tx pgx.Tx, userID string, expectedRevision int, p Patch) (Profile, error) {
	cur, err := GetProfile(ctx, tx, userID)
	if err != nil {
		return cur, err
	}
	if cur.Revision != expectedRevision {
		return cur, core.RevisionConflict("профиль уже изменился, откройте свежую версию")
	}
	if err := applyOptional(&cur.Nickname, p.Nickname, "nickname", validNickname); err != nil {
		return cur, err
	}
	if err := applyOptional(&cur.AgeGroup, p.AgeGroup, "ageGroup", oneOf("14-17", "18-20", "other")); err != nil {
		return cur, err
	}
	if err := applyOptional(&cur.CharacterID, p.CharacterID, "characterId", oneOf("lumi", "spark", "nova")); err != nil {
		return cur, err
	}
	if p.ReducedMotion.Set {
		if p.ReducedMotion.Null {
			return cur, core.Validation("reducedMotion", "ожидалось true или false")
		}
		cur.ReducedMotion = p.ReducedMotion.Value
	}
	if p.AnalyticsEnabled.Set {
		if p.AnalyticsEnabled.Null {
			return cur, core.Validation("analyticsEnabled", "ожидалось true или false")
		}
		cur.AnalyticsEnabled = p.AnalyticsEnabled.Value
	}
	if p.Route.Set {
		if p.Route.Null || (p.Route.Value != "starter" && p.Route.Value != "full") {
			return cur, core.Validation("route", "ожидалось starter или full")
		}
		cur.Route = p.Route.Value
	}
	if p.LearningTimezone.Set {
		if cur.TimezoneLocked {
			return cur, core.Conflict("TIMEZONE_LOCKED", "учебный часовой пояс уже зафиксирован")
		}
		if err := applyOptional(&cur.LearningTimezone, p.LearningTimezone, "learningTimezone", validTimezone); err != nil {
			return cur, err
		}
	}
	err = tx.QueryRow(ctx, `
		UPDATE users SET nickname = $2, age_group = $3, character_id = $4, reduced_motion = $5,
		       route = $6, learning_timezone = $7, analytics_enabled = $8, profile_revision = profile_revision + 1
		WHERE id = $1 RETURNING profile_revision`,
		userID, cur.Nickname, cur.AgeGroup, cur.CharacterID, cur.ReducedMotion, cur.Route, cur.LearningTimezone, cur.AnalyticsEnabled).
		Scan(&cur.Revision)
	return cur, err
}

func applyOptional(dst **string, o Opt[string], field string, valid func(string) string) error {
	if !o.Set {
		return nil
	}
	if o.Null {
		*dst = nil
		return nil
	}
	v := o.Value
	if field == "nickname" {
		v = strings.TrimSpace(v)
	}
	if msg := valid(v); msg != "" {
		return core.Validation(field, msg)
	}
	*dst = &v
	return nil
}

func validNickname(s string) string {
	n := utf8.RuneCountInString(s)
	if n == 0 || n > nicknameMaxRunes {
		return "от 1 до 40 символов"
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "управляющие символы недопустимы"
		}
	}
	return ""
}

func oneOf(values ...string) func(string) string {
	return func(s string) string {
		for _, v := range values {
			if s == v {
				return ""
			}
		}
		return "допустимо: " + strings.Join(values, ", ")
	}
}

func validTimezone(s string) string {
	if s == "" || s == "Local" {
		return "ожидался идентификатор IANA, например Europe/Moscow"
	}
	if _, err := time.LoadLocation(s); err != nil {
		return "неизвестный часовой пояс IANA"
	}
	return ""
}

func printableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// DeleteAccount удаляет пользователя в одной транзакции: credential и все индивидуальные
// записи уходят каскадом (§4). Повтор с утратившим силу токеном получит 401 на аутентификации.
func DeleteAccount(ctx context.Context, tx pgx.Tx, userID string) error {
	_, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	return err
}
