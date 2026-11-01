package progression

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"teyljunior/internal/core"
	"teyljunior/internal/store"
)

const unlockDefault = "default"

// unlockBy открывает предметы, привязанные к источнику (badge:<id> или challenge:<family>).
// Открытая косметика остаётся навсегда, даже если подтверждение бейджа позже исправлено (§14).
func (s *Service) unlockBy(ctx context.Context, tx pgx.Tx, userID, source string, now time.Time) error {
	for _, c := range s.Rules.Cosmetics.Items {
		if c.Unlock != source {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO cosmetic_inventory (user_id, cosmetic_id, source_key, unlocked_at) VALUES ($1, $2, $3, $4)
			ON CONFLICT DO NOTHING`, userID, c.ID, source, now); err != nil {
			return err
		}
	}
	return nil
}

// ChallengeCompleted — завершение события: один косметический предмет по семейству события.
func (s *Service) ChallengeCompleted(ctx context.Context, tx pgx.Tx, userID, familyID, participationID string, now time.Time) error {
	if _, _, err := s.record(ctx, tx, fact{userID: userID, name: "challenge_completed", sourceType: "challenge_participation",
		sourceID: participationID, dedupe: "challenge:" + familyID, payload: map[string]any{"familyId": familyID}, at: now}); err != nil {
		return err
	}
	return s.unlockBy(ctx, tx, userID, "challenge:"+familyID, now)
}

// CosmeticView — предмет каталога для пользователя.
type CosmeticView struct {
	CosmeticID string     `json:"cosmeticId"`
	Title      string     `json:"title"`
	Slot       string     `json:"slot"`
	Unlocked   bool       `json:"unlocked"`
	UnlockedAt *time.Time `json:"unlockedAt"`
	Source     string     `json:"source"`
}

// Inventory — открытые предметы, выбор и персонаж. Выбор персонажа влияет на новые
// приглашения и не меняет правила наград (§17).
type Inventory struct {
	CharacterID *string            `json:"characterId"`
	Revision    int                `json:"revision"`
	Items       []CosmeticView     `json:"items"`
	Equipment   map[string]*string `json:"equipment"`
}

// Inventory собирает инвентарь. Выданные ранее бейджи досинхронизируются в инвентарь:
// предмет не теряется, если бейдж выдан до появления косметики.
func (s *Service) Inventory(ctx context.Context, tx pgx.Tx, userID string, now time.Time) (Inventory, error) {
	rows, err := tx.Query(ctx, `SELECT badge_id, granted_at FROM badge_grants WHERE user_id = $1`, userID)
	if err != nil {
		return Inventory{}, err
	}
	type grant struct {
		id string
		at time.Time
	}
	grants, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (grant, error) {
		var g grant
		return g, r.Scan(&g.id, &g.at)
	})
	if err != nil {
		return Inventory{}, err
	}
	for _, g := range grants {
		if err := s.unlockBy(ctx, tx, userID, "badge:"+g.id, g.at); err != nil {
			return Inventory{}, err
		}
	}
	return s.inventory(ctx, tx, userID)
}

func (s *Service) inventory(ctx context.Context, q store.Querier, userID string) (Inventory, error) {
	inv := Inventory{Items: []CosmeticView{}, Equipment: map[string]*string{}}
	if err := q.QueryRow(ctx, `SELECT character_id, equipment_revision FROM users WHERE id = $1`, userID).Scan(&inv.CharacterID, &inv.Revision); err != nil {
		return inv, err
	}
	unlocked := map[string]time.Time{}
	rows, err := q.Query(ctx, `SELECT cosmetic_id, unlocked_at FROM cosmetic_inventory WHERE user_id = $1`, userID)
	if err != nil {
		return inv, err
	}
	for rows.Next() {
		var id string
		var at time.Time
		if err := rows.Scan(&id, &at); err != nil {
			rows.Close()
			return inv, err
		}
		unlocked[id] = at.UTC()
	}
	if err := rows.Err(); err != nil {
		return inv, err
	}
	for _, c := range s.Rules.Cosmetics.Items {
		v := CosmeticView{CosmeticID: c.ID, Title: c.Title, Slot: c.Slot, Source: c.Unlock, Unlocked: c.Unlock == unlockDefault}
		if at, ok := unlocked[c.ID]; ok {
			v.Unlocked, v.UnlockedAt = true, new(at)
		}
		inv.Items = append(inv.Items, v)
	}
	for _, slot := range s.Rules.Cosmetics.Slots {
		inv.Equipment[slot] = nil
	}
	rows, err = q.Query(ctx, `SELECT slot, cosmetic_id FROM cosmetic_equipment WHERE user_id = $1`, userID)
	if err != nil {
		return inv, err
	}
	for rows.Next() {
		var slot, id string
		if err := rows.Scan(&slot, &id); err != nil {
			rows.Close()
			return inv, err
		}
		inv.Equipment[slot] = &id
	}
	return inv, rows.Err()
}

// Equip выбирает уже открытые предметы. Отсутствующий слот не меняется, null снимает предмет.
func (s *Service) Equip(ctx context.Context, tx pgx.Tx, userID string, expectedRevision int, equipment map[string]*string, now time.Time) (Inventory, error) {
	inv, err := s.Inventory(ctx, tx, userID, now)
	if err != nil {
		return inv, err
	}
	if inv.Revision != expectedRevision {
		return inv, core.RevisionConflict("оформление уже изменилось; открой свежую версию")
	}
	for slot, id := range equipment {
		if !slices.Contains(s.Rules.Cosmetics.Slots, slot) {
			return inv, core.Validation("equipment."+slot, "допустимые слоты: "+strings.Join(s.Rules.Cosmetics.Slots, ", "))
		}
		if id == nil {
			if _, err := tx.Exec(ctx, `DELETE FROM cosmetic_equipment WHERE user_id = $1 AND slot = $2`, userID, slot); err != nil {
				return inv, err
			}
			continue
		}
		i := slices.IndexFunc(inv.Items, func(c CosmeticView) bool { return c.CosmeticID == *id })
		if i < 0 || inv.Items[i].Slot != slot {
			return inv, core.Validation("equipment."+slot, "нет такого предмета для этого слота")
		}
		if !inv.Items[i].Unlocked {
			return inv, core.Unprocessable("COSMETIC_LOCKED", "этот предмет ещё не открыт").WithField("equipment."+slot, "предмет закрыт")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO cosmetic_equipment (user_id, slot, cosmetic_id) VALUES ($1, $2, $3)
			ON CONFLICT (user_id, slot) DO UPDATE SET cosmetic_id = EXCLUDED.cosmetic_id`, userID, slot, *id); err != nil {
			return inv, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET equipment_revision = equipment_revision + 1 WHERE id = $1`, userID); err != nil {
		return inv, err
	}
	return s.inventory(ctx, tx, userID)
}
