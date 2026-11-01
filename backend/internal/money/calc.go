// Пакет money — личный денежный план (спецификация §19): бюджет и текущая цель одним снимком
// с revision, журнал расходов, отметки накопления и запланированные действия. Всё это
// самоотчёт: суммы не переводятся, не складываются с виртуальным портфелем, не дают XP и не
// закрывают учебный день.
package money

// Пределы §19 и конфигурация первого пилота.
const (
	AmountMax     int64 = 100_000_000 // 1 000 000 ₽ в копейках
	MonthsMin           = 1
	MonthsMax           = 120
	PendingMax          = 20
	ExpensesMax         = 500
	ActionHorizon       = 30 // календарных дней от сегодня
	TitleMaxRunes       = 80
	NoteMaxRunes        = 200
	PageDefault         = 50
	PageMax             = 100
)

// Categories — фиксированные категории.
var Categories = []string{"food", "transport", "housing", "other"}

// Budget — бюджетная часть снимка.
type Budget struct {
	IncomeMinor              int64 `json:"incomeMinor"`
	ReserveMinor             int64 `json:"reserveMinor"`
	MonthlyContributionMinor int64 `json:"monthlyContributionMinor"`
	FoodMinor                int64 `json:"foodMinor"`
	TransportMinor           int64 `json:"transportMinor"`
	HousingMinor             int64 `json:"housingMinor"`
	OtherMinor               int64 `json:"otherMinor"`
}

// BudgetFigures — производные бюджета. Отрицательный остаток — допустимое состояние плана.
type BudgetFigures struct {
	EssentialMinor   int64 `json:"essentialMinor"`
	CashMinor        int64 `json:"cashMinor"`
	AvailableMinor   int64 `json:"availableMinor"`
	UnallocatedMinor int64 `json:"unallocatedMinor"`
}

// Figures считает: Essential = сумма категорий; Cash = income − essential − reserve;
// Available = max(0, cash); Unallocated = cash − monthlyContribution.
func (b Budget) Figures() BudgetFigures {
	essential := b.FoodMinor + b.TransportMinor + b.HousingMinor + b.OtherMinor
	cash := b.IncomeMinor - essential - b.ReserveMinor
	return BudgetFigures{EssentialMinor: essential, CashMinor: cash, AvailableMinor: max(0, cash), UnallocatedMinor: cash - b.MonthlyContributionMinor}
}

// GoalFigures — производные цели.
type GoalFigures struct {
	RemainingMinor       int64  `json:"remainingMinor"`
	RequiredMonthlyMinor int64  `json:"requiredMonthlyMinor"`
	ProjectedMinor       int64  `json:"projectedMinor"`
	PlanFeasible         bool   `json:"planFeasible"`
	GoalAchieved         bool   `json:"goalAchieved"`
	SuggestedMonths      *int64 `json:"suggestedMonths"`
}

func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }

// GoalFiguresFor считает (§19): Remaining = max(0, target − saved);
// RequiredMonthly = ceil(Remaining / months); Projected = saved + monthly × months;
// PlanFeasible = cash ≥ 0 и monthly ≤ cash и projected ≥ target; GoalAchieved = saved ≥ target;
// SuggestedMonths = 0 при достигнутой цели, null при положительном остатке и нулевом взносе,
// иначе ceil(Remaining / monthly).
func GoalFiguresFor(b Budget, target, saved int64, months int) GoalFigures {
	cash := b.Figures().CashMinor
	monthly := b.MonthlyContributionMinor
	f := GoalFigures{RemainingMinor: max(0, target-saved)}
	f.RequiredMonthlyMinor = ceilDiv(f.RemainingMinor, int64(months))
	f.ProjectedMinor = saved + monthly*int64(months)
	f.PlanFeasible = cash >= 0 && monthly <= cash && f.ProjectedMinor >= target
	f.GoalAchieved = saved >= target
	switch {
	case f.GoalAchieved:
		f.SuggestedMonths = new(int64(0))
	case monthly > 0:
		f.SuggestedMonths = new(ceilDiv(f.RemainingMinor, monthly))
	}
	return f
}
