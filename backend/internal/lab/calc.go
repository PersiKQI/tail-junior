// Пакет lab — учебная лаборатория (спецификация §10, §11): расчётная модель V2, кейс,
// эксперименты, неизменяемые решения, расчёты сценариев, разборы и итоговое сравнение.
package lab

import (
	"fmt"
	"slices"
)

// Assets — порядок корзин в решении и в строках результата.
var Assets = []string{"cash", "bond", "stock", "fund"}

// Rate — изменение цены и выплата за период сценария в базисных пунктах.
type Rate struct {
	PriceBps  int64 `json:"priceBps"`
	IncomeBps int64 `json:"incomeBps"`
}

// Scenario — заданный автором сценарий; вероятностей и прогноза нет (§11).
type Scenario struct {
	ID     string          `json:"id"`
	Title  string          `json:"title"`
	Months int             `json:"months"`
	Rates  map[string]Rate `json:"rates"`
}

// Asset — описание корзины.
type Asset struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// Model — неизменяемый расчётный пакет teyl-lab-budget-v2.
type Model struct {
	Version                string     `json:"version"`
	UnitMinor              int64      `json:"unitMinor"`
	Units                  int        `json:"units"`
	FeeBps                 int64      `json:"feeBps"`
	Assets                 []Asset    `json:"assets"`
	Scenarios              []Scenario `json:"scenarios"`
	NotExcludedFromReality []string   `json:"notExcludedFromReality"`
	Notice                 string     `json:"notice"`
}

// Scenario возвращает сценарий по id.
func (m *Model) Scenario(id string) (Scenario, bool) {
	i := slices.IndexFunc(m.Scenarios, func(s Scenario) bool { return s.ID == id })
	if i < 0 {
		return Scenario{}, false
	}
	return m.Scenarios[i], true
}

// Allocation — 10 бюджетных долей.
type Allocation struct {
	Cash  int `json:"cash"`
	Bond  int `json:"bond"`
	Stock int `json:"stock"`
	Fund  int `json:"fund"`
}

func (a Allocation) units(asset string) int {
	switch asset {
	case "cash":
		return a.Cash
	case "bond":
		return a.Bond
	case "stock":
		return a.Stock
	default:
		return a.Fund
	}
}

// Valid — целые доли 0…units, в сумме ровно units.
func (a Allocation) Valid(units int) bool {
	sum := 0
	for _, asset := range Assets {
		u := a.units(asset)
		if u < 0 || u > units {
			return false
		}
		sum += u
	}
	return sum == units
}

// Row — строка расчёта корзины. Все суммы — int64 в копейках.
type Row struct {
	Asset       string `json:"asset"`
	Units       int    `json:"units"`
	Budget      int64  `json:"budgetMinor"`
	Principal   int64  `json:"principalMinor"`
	EntryFee    int64  `json:"entryFeeMinor"`
	Remainder   int64  `json:"remainderMinor"`
	PriceChange int64  `json:"priceChangeMinor"`
	Income      int64  `json:"incomeMinor"`
	MarketValue int64  `json:"marketValueMinor"`
	ExitFee     int64  `json:"exitFeeMinor"`
	Ending      int64  `json:"endingMinor"`
	Profit      int64  `json:"profitMinor"`
}

// Result — итог портфеля: сумма строк, округление выполнено до суммирования.
type Result struct {
	ScenarioID  string `json:"scenarioId"`
	Months      int    `json:"months"`
	Initial     int64  `json:"initialMinor"`
	Ending      int64  `json:"endingMinor"`
	Profit      int64  `json:"profitMinor"`
	Fees        int64  `json:"feesMinor"`
	Income      int64  `json:"incomeMinor"`
	PriceChange int64  `json:"priceChangeMinor"`
	Rows        []Row  `json:"rows"`
}

// roundDiv делит n на d > 0 с округлением половины от нуля (§11).
func roundDiv(n, d int64) int64 {
	if n >= 0 {
		return (2*n + d) / (2 * d)
	}
	return -((-2*n + d) / (2 * d))
}

const bps = 10000

// Evaluate считает сценарий для распределения (§11):
//
//	principal = floor(B × 10000 / (10000 + fee)); entryFee = round(principal × fee / 10000);
//	remainder = B − principal − entryFee; priceChange = round(principal × priceBps / 10000);
//	income = round(principal × incomeBps / 10000); marketValue = principal + priceChange;
//	exitFee = round(marketValue × fee / 10000); ending = remainder + marketValue + income − exitFee.
//
// Комиссия входа оплачивается из самой корзины, деньги резерва на неё не тратятся.
func Evaluate(m *Model, a Allocation, scenarioID string) (Result, error) {
	if !a.Valid(m.Units) {
		return Result{}, fmt.Errorf("распределение должно состоять из %d целых долей", m.Units)
	}
	sc, ok := m.Scenario(scenarioID)
	if !ok {
		return Result{}, fmt.Errorf("неизвестный сценарий %q", scenarioID)
	}
	res := Result{ScenarioID: sc.ID, Months: sc.Months, Initial: int64(m.Units) * m.UnitMinor}
	for _, asset := range Assets {
		u := a.units(asset)
		b := int64(u) * m.UnitMinor
		row := Row{Asset: asset, Units: u, Budget: b}
		if asset == "cash" || b == 0 {
			// Деньги без вложений: principal = B, комиссии и изменение нулевые.
			row.Principal, row.MarketValue, row.Ending = b, b, b
		} else {
			r := sc.Rates[asset]
			row.Principal = b * bps / (bps + m.FeeBps)
			row.EntryFee = roundDiv(row.Principal*m.FeeBps, bps)
			row.Remainder = b - row.Principal - row.EntryFee
			row.PriceChange = roundDiv(row.Principal*r.PriceBps, bps)
			row.Income = roundDiv(row.Principal*r.IncomeBps, bps)
			row.MarketValue = row.Principal + row.PriceChange
			row.ExitFee = roundDiv(row.MarketValue*m.FeeBps, bps)
			row.Ending = row.Remainder + row.MarketValue + row.Income - row.ExitFee
		}
		if row.MarketValue < 0 || row.Remainder < 0 || row.EntryFee < 0 || row.ExitFee < 0 {
			return Result{}, fmt.Errorf("модель %s дала недопустимую строку %s", m.Version, asset)
		}
		row.Profit = row.Ending - b
		res.Ending += row.Ending
		res.Fees += row.EntryFee + row.ExitFee
		res.Income += row.Income
		res.PriceChange += row.PriceChange
		res.Rows = append(res.Rows, row)
	}
	res.Profit = res.Ending - res.Initial
	return res, nil
}
