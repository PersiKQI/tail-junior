// Пакет progression — потребители доменных фактов (спецификация §12–§15): XP и роли участия,
// бейджи, учебные свидетельства, учебные дни и защита серии. Начисления делаются в той же
// транзакции, что и факт, по уникальным бизнес-основаниям; клиент не может «добавить XP» или
// «выдать бейдж» (§20).
package progression

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	bankfiles "teyljunior/content"
)

// DefaultTimezone — учебный пояс, если пользователь не выбрал свой до первого зачётного дня.
const DefaultTimezone = "Europe/Moscow"

// Role — роль участия: пороги XP и охвата (§13).
type Role struct {
	ID                       string `json:"id"`
	Title                    string `json:"title"`
	MinXP                    int    `json:"minXp"`
	MinLearningFamilies      int    `json:"minLearningFamilies"`
	MinModulesWithCompletion int    `json:"minModulesWithCompletion"`
	RequiresCompleteLabCase  bool   `json:"requiresCompleteLabCase"`
}

// Badge — бейдж первого релиза (§14).
type Badge struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	OutcomeID string `json:"outcomeId"`
}

// Rules — параметры правил из content/rules.json.
type Rules struct {
	RuleVersion string `json:"ruleVersion"`
	XP          struct {
		Learning    int `json:"learningFamilyFirstAccepted"`
		Review      int `json:"reviewSlotFirstAnsweredOnFinalization"`
		Lab         int `json:"labCaseFamilyFirstComplete"`
		MaxFullBank int `json:"maxFullBank"`
	} `json:"xp"`
	Roles  []Role  `json:"roles"`
	Badges []Badge `json:"badges"`
	Streak struct {
		FreezeInitial         int `json:"freezeInitial"`
		FreezeRefillEveryDays int `json:"freezeRefillEveryActiveDays"`
		FreezeMax             int `json:"freezeMax"`
	} `json:"streak"`
	Cosmetics struct {
		Slots []string   `json:"slots"`
		Items []Cosmetic `json:"items"`
	} `json:"cosmetics"`
}

// Cosmetic — предмет оформления персонажа. Unlock: default, badge:<id> или challenge:<family>.
type Cosmetic struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Slot   string `json:"slot"`
	Unlock string `json:"unlock"`
}

// LoadRules разбирает встроенный rules.json и проверяет, что нужные параметры заданы.
func LoadRules() (*Rules, error) {
	var r Rules
	if err := json.Unmarshal(bankfiles.Rules, &r); err != nil {
		return nil, fmt.Errorf("rules.json: %w", err)
	}
	if r.RuleVersion == "" || r.XP.Learning <= 0 || r.XP.Review <= 0 || r.XP.Lab <= 0 || len(r.Roles) == 0 ||
		r.Streak.FreezeRefillEveryDays <= 0 || r.Streak.FreezeMax < r.Streak.FreezeInitial {
		return nil, fmt.Errorf("rules.json: неполные параметры XP, ролей или серии")
	}
	for _, c := range r.Cosmetics.Items {
		if !slices.Contains(r.Cosmetics.Slots, c.Slot) || c.ID == "" || c.Unlock == "" {
			return nil, fmt.Errorf("rules.json: предмет %q с неизвестным слотом или без условия открытия", c.ID)
		}
		if badge, ok := strings.CutPrefix(c.Unlock, "badge:"); ok && !slices.ContainsFunc(r.Badges, func(b Badge) bool { return b.ID == badge }) {
			return nil, fmt.Errorf("rules.json: предмет %q открывается неизвестным бейджем %q", c.ID, badge)
		}
	}
	return &r, nil
}

func (r *Rules) badge(id string) Badge {
	for _, b := range r.Badges {
		if b.ID == id {
			return b
		}
	}
	return Badge{ID: id, Title: id}
}
