package money

import "testing"

// Пример приложения В: income 10 000 ₽, категории 6 000 ₽, reserve 1 000 ₽, взнос 2 500 ₽;
// цель 12 000 ₽, накоплено 2 000 ₽, срок 4 месяца.
func TestSpecificationExample(t *testing.T) {
	b := Budget{IncomeMinor: 1_000_000, ReserveMinor: 100_000, MonthlyContributionMinor: 250_000,
		FoodMinor: 300_000, TransportMinor: 100_000, HousingMinor: 150_000, OtherMinor: 50_000}
	f := b.Figures()
	if f.EssentialMinor != 600_000 || f.AvailableMinor != 300_000 || f.UnallocatedMinor != 50_000 {
		t.Fatalf("budget = %+v", f)
	}
	g := GoalFiguresFor(b, 1_200_000, 200_000, 4)
	if g.RequiredMonthlyMinor != 250_000 || g.ProjectedMinor != 1_200_000 || !g.PlanFeasible || g.GoalAchieved || *g.SuggestedMonths != 4 {
		t.Fatalf("goal = %+v", g)
	}
}

func TestGoalEdgeCases(t *testing.T) {
	// Отрицательный остаток бюджета — допустимое состояние, план непосилен.
	b := Budget{IncomeMinor: 100_000, OtherMinor: 150_000}
	if f := b.Figures(); f.CashMinor != -50_000 || f.AvailableMinor != 0 {
		t.Fatalf("negative cash = %+v", f)
	}
	if g := GoalFiguresFor(b, 100_000, 0, 3); g.PlanFeasible || g.SuggestedMonths != nil || g.RequiredMonthlyMinor != 33_334 {
		t.Fatalf("zero contribution = %+v", g)
	}
	if g := GoalFiguresFor(Budget{}, 100_000, 150_000, 3); !g.GoalAchieved || *g.SuggestedMonths != 0 || g.RemainingMinor != 0 {
		t.Fatalf("achieved = %+v", g)
	}
}
