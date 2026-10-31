import type { GoalInput } from '../shared/types.ts';
export function forecastGoal(goal:GoalInput,incomeDelta:number,expenseDelta:number,monthly:number,months:number){
 const income=Math.max(0,goal.income+incomeDelta),essential=goal.essential+expenseDelta;
 const budgetBalance=income-essential-goal.reserve,available=Math.max(0,budgetBalance);
 const remaining=Math.max(0,goal.target-goal.saved),projected=goal.saved+monthly*months;
 const feasible=budgetBalance>=0&&monthly<=available&&projected>=goal.target;
 const affordableMonthly=Math.min(monthly,available);
 return {income,essential,available,remaining,projected,feasible,gap:Math.max(0,goal.target-projected),budgetBalance,affordableMonthly,affordableMonths:remaining===0?0:affordableMonthly>0?Math.ceil(remaining/affordableMonthly):null};
}
