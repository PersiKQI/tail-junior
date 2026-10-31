import type { GoalInput } from '../shared/types.ts';
import { forecastGoal } from './domain.ts';

export type PaceId = 'gentle' | 'current' | 'sooner';
export function goalPaces(goal: GoalInput) {
  const remaining = Math.max(0, goal.target - goal.saved);
  const gentleMonths = Math.min(120, Math.max(goal.months + 1, Math.ceil(goal.months * 1.5)));
  const soonerMonths = Math.max(1, goal.months - 1);
  return [
    { id: 'gentle' as PaceId, title: gentleMonths > goal.months ? 'Дольше' : 'За 120 мес.', detail: 'Взнос под выбранный срок', months: gentleMonths, monthly: Math.ceil(remaining / gentleMonths) },
    { id: 'current' as PaceId, title: 'Как сейчас', detail: 'Твой сохранённый план', months: goal.months, monthly: goal.monthly },
    { id: 'sooner' as PaceId, title: 'Раньше', detail: goal.months === 1 ? 'Минимальный срок' : 'На месяц раньше', months: soonerMonths, monthly: Math.ceil(remaining / soonerMonths) },
  ].map(pace => ({ ...pace, forecast: forecastGoal(goal, 0, 0, pace.monthly, pace.months) }));
}

export function projectionPoints(saved: number, monthly: number, months: number) {
  const count = Math.min(6, months);
  return [0, ...Array.from({ length: count }, (_, i) => Math.ceil((i + 1) * months / count))]
    .map(month => ({ month, amount: saved + monthly * month }));
}

export function goalMilestones(goal: GoalInput, monthly: number) {
  const affordable = goal.income - goal.essential - goal.reserve >= monthly && monthly > 0;
  return [25, 50, 75, 100].map(percent => {
    const amount = Math.ceil(goal.target * percent / 100);
    const remaining = Math.max(0, amount - goal.saved);
    return { percent, amount, remaining, reached: remaining === 0, months: remaining === 0 ? 0 : affordable ? Math.ceil(remaining / monthly) : null };
  });
}
