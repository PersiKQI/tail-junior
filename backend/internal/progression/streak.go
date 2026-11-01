package progression

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// Виды учебного дня.
const (
	dayActive    = "active"
	dayProtected = "protected"
)

// civil — календарная дата в учебном поясе, представленная полуночью UTC: арифметика дат идёт
// по календарю, а не прибавлением 24 часов, и не зависит от переходов DST (§15).
func civil(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// learningLocation возвращает учебный пояс пользователя. lock=true фиксирует его при первом
// зачётном дне; пустой пояс заменяется значением по умолчанию (§15).
func learningLocation(ctx context.Context, tx pgx.Tx, userID string, lock bool, now time.Time) (*time.Location, string, error) {
	var tz *string
	var locked *time.Time
	if err := tx.QueryRow(ctx, `SELECT learning_timezone, timezone_locked_at FROM users WHERE id = $1`, userID).Scan(&tz, &locked); err != nil {
		return nil, "", err
	}
	name := DefaultTimezone
	if tz != nil {
		name = *tz
	}
	if lock && locked == nil {
		if _, err := tx.Exec(ctx, `
			UPDATE users SET learning_timezone = $2, timezone_locked_at = $3,
			       profile_revision = profile_revision + CASE WHEN learning_timezone IS NULL THEN 1 ELSE 0 END
			WHERE id = $1`, userID, name, now); err != nil {
			return nil, "", err
		}
	}
	loc, err := time.LoadLocation(name)
	return loc, name, err
}

// creditDay закрывает сегодняшнюю дату по acceptedAt сервера (§15). Сначала обрабатываются
// завершённые дни, потом засчитывается текущий.
func (s *Service) creditDay(ctx context.Context, tx pgx.Tx, userID, factID string, now time.Time) error {
	loc, name, err := learningLocation(ctx, tx, userID, true, now)
	if err != nil {
		return err
	}
	today := civil(now, loc)
	if _, err := s.settle(ctx, tx, userID, today, now); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO study_days (user_id, local_date, kind, calendar_timezone, source_fact_id, created_at)
		VALUES ($1, $2, 'active', $3, $4, $5) ON CONFLICT (user_id, local_date) DO NOTHING`, userID, today, name, factID, now)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	if _, _, err := s.record(ctx, tx, fact{userID: userID, name: "study_day_credited", sourceType: "study_day",
		sourceID: today.Format(time.DateOnly), dedupe: "day:" + today.Format(time.DateOnly), at: now}); err != nil {
		return err
	}
	// Пополнение защиты за сегодняшний активный день.
	_, err = s.settle(ctx, tx, userID, today, now)
	return err
}

// streak — состояние серии на дату today.
type streak struct {
	charges        int
	totalActive    int
	curActive      int
	curProtected   int
	best           int
	chain          bool
	todayActive    bool
	firstActiveDay *time.Time
}

// settle проходит дни от первого активного до сегодняшнего включительно (§15):
//   - стартовый заряд выдаётся с первым активным днём; каждые N фактических дней — пополнение
//     не выше максимума;
//   - пропущенный завершённый день внутри начатой цепочки оплачивается зарядом (protected);
//   - пропуск сверх остатка заканчивает цепочку, после break заряды не тратятся до
//     следующего активного дня;
//   - сегодня без ответа — pending и не обрабатывается.
//
// Решения сохраняются (protected-дни и freeze_ledger по стабильным основаниям), поэтому
// повторный проход ничего не меняет и не списывает заряд дважды.
func (s *Service) settle(ctx context.Context, tx pgx.Tx, userID string, today, now time.Time) (streak, error) {
	r := s.Rules.Streak
	st := streak{charges: r.FreezeInitial}
	rows, err := tx.Query(ctx, `SELECT local_date, kind, calendar_timezone FROM study_days WHERE user_id = $1 ORDER BY local_date`, userID)
	if err != nil {
		return st, err
	}
	type day struct {
		kind, tz string
	}
	days := map[time.Time]day{}
	var first time.Time
	var tz string
	for rows.Next() {
		var d time.Time
		var x day
		if err := rows.Scan(&d, &x.kind, &x.tz); err != nil {
			rows.Close()
			return st, err
		}
		d = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
		if first.IsZero() {
			first, tz = d, x.tz
		}
		days[d] = x
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	if first.IsZero() {
		return st, nil
	}
	st.firstActiveDay = &first
	if err := s.ledger(ctx, tx, userID, "initial", r.FreezeInitial, now); err != nil {
		return st, err
	}
	for d := first; !d.After(today); d = d.AddDate(0, 0, 1) {
		x, ok := days[d]
		switch {
		case ok && x.kind == dayActive:
			if !st.chain {
				st.curActive, st.curProtected = 0, 0
			}
			st.chain = true
			st.curActive++
			st.totalActive++
			st.best = max(st.best, st.curActive)
			if st.totalActive%r.FreezeRefillEveryDays == 0 {
				delta := 0
				if st.charges < r.FreezeMax {
					delta = 1
				}
				st.charges += delta
				if err := s.ledger(ctx, tx, userID, "refill:"+strconv.Itoa(st.totalActive), delta, now); err != nil {
					return st, err
				}
			}
			if d.Equal(today) {
				st.todayActive = true
			}
		case ok && x.kind == dayProtected:
			st.charges--
			st.curProtected++
		case d.Equal(today):
			// Сегодня без ответа — pending.
		case st.chain && st.charges > 0:
			if _, err := tx.Exec(ctx, `
				INSERT INTO study_days (user_id, local_date, kind, calendar_timezone, created_at)
				VALUES ($1, $2, 'protected', $3, $4) ON CONFLICT DO NOTHING`, userID, d, tz, now); err != nil {
				return st, err
			}
			if err := s.ledger(ctx, tx, userID, "use:"+d.Format(time.DateOnly), -1, now); err != nil {
				return st, err
			}
			st.charges--
			st.curProtected++
		default:
			st.chain = false
			st.curActive, st.curProtected = 0, 0
		}
	}
	return st, nil
}

func (s *Service) ledger(ctx context.Context, tx pgx.Tx, userID, basis string, delta int, now time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO freeze_ledger (id, user_id, basis_key, delta, created_at) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, basis_key) DO NOTHING`, s.IDs.New("frz"), userID, basis, delta, now)
	return err
}
