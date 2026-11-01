package lab

import (
	"encoding/json"
	"math/big"
	"testing"

	bankfiles "teyljunior/content"
)

func loadPack(t *testing.T) *Pack {
	t.Helper()
	p, err := ParsePack(bankfiles.LabCase)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// allAllocations — все распределения 10 долей по 4 корзинам.
func allAllocations() []Allocation {
	var out []Allocation
	for c := 0; c <= 10; c++ {
		for b := 0; c+b <= 10; b++ {
			for s := 0; c+b+s <= 10; s++ {
				out = append(out, Allocation{Cash: c, Bond: b, Stock: s, Fund: 10 - c - b - s})
			}
		}
	}
	return out
}

// ratRound — независимое округление половины от нуля на рациональных числах.
func ratRound(r *big.Rat) int64 {
	n, d := new(big.Int).Set(r.Num()), r.Denom()
	neg := n.Sign() < 0
	n.Abs(n)
	q, m := new(big.Int).QuoRem(n, d, new(big.Int))
	if new(big.Int).Mul(m, big.NewInt(2)).Cmp(d) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	if neg {
		q.Neg(q)
	}
	return q.Int64()
}

func rat(a, b int64) *big.Rat { return big.NewRat(a, b) }

// independentEnding — тот же итог, посчитанный на big.Rat по тексту §11, без целочисленных
// приёмов Evaluate.
func independentEnding(m *Model, a Allocation, sc Scenario) int64 {
	var total int64
	fee := rat(m.FeeBps, 10000)
	for _, asset := range Assets {
		b := int64(a.units(asset)) * m.UnitMinor
		if asset == "cash" || b == 0 {
			total += b
			continue
		}
		// floor(B / (1 + fee))
		q := new(big.Rat).Quo(rat(b, 1), new(big.Rat).Add(rat(1, 1), fee))
		principal := new(big.Int).Quo(q.Num(), q.Denom()).Int64()
		entry := ratRound(new(big.Rat).Mul(rat(principal, 1), fee))
		rem := b - principal - entry
		r := sc.Rates[asset]
		price := ratRound(new(big.Rat).Mul(rat(principal, 1), rat(r.PriceBps, 10000)))
		income := ratRound(new(big.Rat).Mul(rat(principal, 1), rat(r.IncomeBps, 10000)))
		mv := principal + price
		exit := ratRound(new(big.Rat).Mul(rat(mv, 1), fee))
		total += rem + mv + income - exit
	}
	return total
}

// Полное конечное пространство: 286 распределений × 3 сценария (§24).
func TestAllAllocationsMatchIndependentCalculation(t *testing.T) {
	p := loadPack(t)
	m := &p.Model
	allocs := allAllocations()
	if len(allocs) != 286 {
		t.Fatalf("allocations = %d, want 286", len(allocs))
	}
	n := 0
	for _, a := range allocs {
		for _, sc := range m.Scenarios {
			res, err := Evaluate(m, a, sc.ID)
			if err != nil {
				t.Fatalf("%+v %s: %v", a, sc.ID, err)
			}
			if want := independentEnding(m, a, sc); res.Ending != want {
				t.Fatalf("%+v %s: ending %d, independent %d", a, sc.ID, res.Ending, want)
			}
			var sum, fees int64
			for _, row := range res.Rows {
				sum += row.Ending
				fees += row.EntryFee + row.ExitFee
				// Комиссия входа самофинансируемая: корзина не залезает в чужие деньги.
				if row.Principal+row.EntryFee+row.Remainder != row.Budget || row.Remainder < 0 || row.Remainder > 1 && row.Asset != "cash" {
					t.Fatalf("%+v %s %s: self-financing broken %+v", a, sc.ID, row.Asset, row)
				}
				if row.Asset == "cash" && (row.Ending != row.Budget || row.EntryFee+row.ExitFee != 0) {
					t.Fatalf("cash row changed: %+v", row)
				}
			}
			if sum != res.Ending || fees != res.Fees || res.Profit != res.Ending-res.Initial || res.Initial != 1000000 {
				t.Fatalf("%+v %s: totals inconsistent %+v", a, sc.ID, res)
			}
			n++
		}
	}
	if n != 858 {
		t.Fatalf("calculations = %d, want 858", n)
	}
}

func TestFixturesFromSpecification(t *testing.T) {
	p := loadPack(t)
	var f struct {
		Fixtures []struct {
			Allocation  Allocation `json:"allocation"`
			ScenarioID  string     `json:"scenarioId"`
			Months      int        `json:"months"`
			EndingMinor int64      `json:"endingMinor"`
			ProfitMinor int64      `json:"profitMinor"`
		} `json:"fixtures"`
		Rows []struct {
			Asset       string `json:"asset"`
			Budget      int64  `json:"budgetMinor"`
			ScenarioID  string `json:"scenarioId"`
			Principal   int64  `json:"principalMinor"`
			EntryFee    int64  `json:"entryFeeMinor"`
			Remainder   int64  `json:"remainderMinor"`
			PriceChange int64  `json:"priceChangeMinor"`
			Income      int64  `json:"incomeMinor"`
			MarketValue int64  `json:"marketValueMinor"`
			ExitFee     int64  `json:"exitFeeMinor"`
			Ending      int64  `json:"endingMinor"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(bankfiles.CalculationFixtures, &f); err != nil {
		t.Fatal(err)
	}
	for _, fx := range f.Fixtures {
		res, err := Evaluate(&p.Model, fx.Allocation, fx.ScenarioID)
		if err != nil {
			t.Fatal(err)
		}
		if res.Ending != fx.EndingMinor || res.Profit != fx.ProfitMinor || res.Months != fx.Months {
			t.Errorf("%+v %s: got %d/%d, want %d/%d", fx.Allocation, fx.ScenarioID, res.Ending, res.Profit, fx.EndingMinor, fx.ProfitMinor)
		}
	}
	for _, want := range f.Rows {
		var a Allocation
		switch want.Asset {
		case "bond":
			a = Allocation{Cash: 10 - int(want.Budget/100000), Bond: int(want.Budget / 100000)}
		case "fund":
			a = Allocation{Cash: 10 - int(want.Budget/100000), Fund: int(want.Budget / 100000)}
		}
		res, err := Evaluate(&p.Model, a, want.ScenarioID)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range res.Rows {
			if row.Asset != want.Asset {
				continue
			}
			if row.Principal != want.Principal || row.EntryFee != want.EntryFee || row.Remainder != want.Remainder ||
				row.PriceChange != want.PriceChange || row.Income != want.Income || row.MarketValue != want.MarketValue ||
				row.ExitFee != want.ExitFee || row.Ending != want.Ending {
				t.Errorf("row %s: got %+v, want %+v", want.Asset, row, want)
			}
		}
	}
}

func TestRoundHalfAwayFromZero(t *testing.T) {
	cases := []struct{ n, d, want int64 }{{5, 10, 1}, {4, 10, 0}, {-5, 10, -1}, {-4, 10, 0}, {15, 10, 2}, {-15, 10, -2}, {0, 10, 0}}
	for _, c := range cases {
		if got := roundDiv(c.n, c.d); got != c.want {
			t.Errorf("roundDiv(%d,%d) = %d, want %d", c.n, c.d, got, c.want)
		}
	}
}

func TestInvalidAllocation(t *testing.T) {
	p := loadPack(t)
	for _, a := range []Allocation{{Cash: 9}, {Cash: 11, Bond: -1}, {Cash: 5, Bond: 6}} {
		if _, err := Evaluate(&p.Model, a, "growth"); err == nil {
			t.Errorf("%+v accepted", a)
		}
	}
	if _, err := Evaluate(&p.Model, Allocation{Cash: 10}, "boom"); err == nil {
		t.Error("unknown scenario accepted")
	}
}
