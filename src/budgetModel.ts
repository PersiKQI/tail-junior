import type { BudgetCategoryId, Expense } from '../shared/types.ts';

export const budgetCategories = [
 {id:'food',title:'Еда',color:'#9270DA',tint:'#EEE5FF',icon:'heart'},
 {id:'transport',title:'Транспорт',color:'#C18D26',tint:'#FFF0C9',icon:'growth'},
 {id:'housing',title:'Жильё',color:'#2E927B',tint:'#DEF3EB',icon:'home'},
 {id:'other',title:'Другое',color:'#4F86D6',tint:'#E4EEFF',icon:'wallet'},
] as const;
export type ExpensePeriod='month'|'week'|'all';
export function localDateKey(date=new Date()):string{return `${date.getFullYear()}-${String(date.getMonth()+1).padStart(2,'0')}-${String(date.getDate()).padStart(2,'0')}`;}
export function weekStartKey(date=new Date()):string{const d=new Date(date.getFullYear(),date.getMonth(),date.getDate(),12);d.setDate(d.getDate()-(d.getDay()+6)%7);return localDateKey(d);}
export function summarizeExpenses(expenses:Expense[],now=new Date()){
 const today=localDateKey(now),month=today.slice(0,7),week=weekStartKey(now);const byCategory:Record<BudgetCategoryId,number>={food:0,transport:0,housing:0,other:0};let monthTotal=0,weekTotal=0,monthCount=0,weekCount=0;
 for(const expense of expenses){if(expense.voidedAt||expense.occurredOn>today)continue;if(expense.occurredOn.startsWith(month)){monthTotal+=expense.amount;monthCount++;byCategory[expense.category]+=expense.amount;}if(expense.occurredOn>=week){weekTotal+=expense.amount;weekCount++;}}
 return{monthTotal,weekTotal,monthCount,weekCount,byCategory};
}
export function filterExpenses(expenses:Expense[],period:ExpensePeriod,category:BudgetCategoryId|'all',showVoided:boolean,now=new Date()):Expense[]{
 const today=localDateKey(now),month=today.slice(0,7),week=weekStartKey(now);
 return expenses.filter(e=>(showVoided||!e.voidedAt)&&(category==='all'||e.category===category)&&(period==='all'||(e.occurredOn<=today&&(period==='month'?e.occurredOn.startsWith(month):e.occurredOn>=week))));
}
export function parseRubles(value:string):number{const normalized=value.replace(/[\s\u00a0\u202f]/g,'');return /^\d+$/.test(normalized)?Number(normalized):NaN;}
