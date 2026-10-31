import type { InvestmentAllocation, InvestmentAssetId, InvestmentPortfolioResult, InvestmentScenarioId } from './types.ts';

export const INVESTMENT_ASSETS = [
 {id:'cash',title:'Без вложений',description:'Виртуальные деньги без процента'},
 {id:'bond',title:'Облигации',description:'Цена и купон учитываются отдельно'},
 {id:'stock',title:'Акции',description:'Цена может заметно меняться'},
 {id:'fund',title:'Фонд',description:'Несколько активов, но риск остаётся'},
] as const;
export const INVESTMENT_SCENARIOS = [
 {id:'growth',title:'Рынок вырос',months:3,description:'Облигации +1% и купон 2%; акции +6%; фонд +4%.'},
 {id:'down',title:'Рынок снизился',months:3,description:'Облигации −6% и купон 2%; акции −20%; фонд −12%.'},
 {id:'early',title:'Деньги нужны раньше',months:1,description:'Продажа через месяц: облигации −3%, выплаты купона нет; акции −8%; фонд −5%. Накопленный купонный доход в модели не учтён.'},
] as const;
export const INVESTMENT_LAB_QUESTION='Какой вывод верен?';
export const INVESTMENT_LAB_REASONS=[
 {id:'risk',text:'Ни доход, ни сохранность вложенной суммы не гарантированы; срок и состав меняют результат'},
 {id:'fund_safe',text:'Любой фонд защищает от убытка в любой момент'},
 {id:'coupon_safe',text:'Купон по облигации исключает общий убыток'},
] as const;
export const DEFAULT_INVESTMENT_ALLOCATION:InvestmentAllocation={cash:10,bond:0,stock:0,fund:0};
export const INVESTMENT_MODEL_NOTICE='Все сценарии и изменения цен вымышлены: это учебная модель, не прогноз. 10 долей по 1 000 виртуальных ₽. Купоны и изменения заданы за весь период, не за год. Комиссия — 0,1% при входе и 0,1% от рыночной стоимости при выходе. Для сравнения комиссии расчётно вычитаются из итоговой суммы; модель не проводит покупки и не выдаёт кредит на их оплату. Накопленный купонный доход (НКД) не моделируется: в реальной продаже купонной облигации он обычно учитывается в расчётах с покупателем. Налоги, инфляция, дефолты и другие издержки в расчёт не включены.';

const rates:Record<InvestmentScenarioId,Record<InvestmentAssetId,{priceBps:number;incomeBps:number}>>={
 growth:{cash:{priceBps:0,incomeBps:0},bond:{priceBps:100,incomeBps:200},stock:{priceBps:600,incomeBps:0},fund:{priceBps:400,incomeBps:0}},
 down:{cash:{priceBps:0,incomeBps:0},bond:{priceBps:-600,incomeBps:200},stock:{priceBps:-2000,incomeBps:0},fund:{priceBps:-1200,incomeBps:0}},
 early:{cash:{priceBps:0,incomeBps:0},bond:{priceBps:-300,incomeBps:0},stock:{priceBps:-800,incomeBps:0},fund:{priceBps:-500,incomeBps:0}},
};
export function validateInvestmentAllocation(value:unknown):InvestmentAllocation{
 if(!value||typeof value!=='object'||Array.isArray(value))throw new Error('Распредели 10 долей между четырьмя вариантами.');
 const data=value as Record<string,unknown>;const ids=INVESTMENT_ASSETS.map(a=>a.id);
 if(Object.keys(data).length!==4||Object.keys(data).some(id=>!ids.includes(id as InvestmentAssetId)))throw new Error('Нужны ровно четыре варианта: деньги, облигации, акции и фонд.');
 for(const id of ids)if(typeof data[id]!=='number'||!Number.isInteger(data[id])||(data[id] as number)<0||(data[id] as number)>10)throw new Error('Каждая доля — целое число от 0 до 10.');
 const allocation=data as InvestmentAllocation;if(ids.reduce((sum,id)=>sum+allocation[id],0)!==10)throw new Error('Всего должно получиться 10 долей — 10 000 виртуальных ₽.');
 return{cash:allocation.cash,bond:allocation.bond,stock:allocation.stock,fund:allocation.fund};
}
export function evaluatePortfolio(value:InvestmentAllocation,scenarioId:InvestmentScenarioId):InvestmentPortfolioResult{
 const allocation=validateInvestmentAllocation(value);
 if(!INVESTMENT_SCENARIOS.some(s=>s.id===scenarioId))throw new Error('Выбери один из трёх учебных сценариев.');
 // Work in kopecks; round each income, price change and fee before summing.
 const raw=INVESTMENT_ASSETS.map(({id})=>{const initial=allocation[id]*100_000;const r=rates[scenarioId][id];const priceChange=Math.round(initial*r.priceBps/10_000);const cashIncome=Math.round(initial*r.incomeBps/10_000);const fees=id==='cash'?0:Math.round(initial/1000)+Math.round((initial+priceChange)/1000);const profit=priceChange+cashIncome-fees;return{asset:id,units:allocation[id],initial,ending:initial+profit,cashIncome,priceChange,fees,profit};});
 const sum=(key:'initial'|'ending'|'cashIncome'|'priceChange'|'fees'|'profit')=>raw.reduce((total,row)=>total+row[key],0)/100;
 return{scenarioId,initial:sum('initial'),ending:sum('ending'),cashIncome:sum('cashIncome'),priceChange:sum('priceChange'),fees:sum('fees'),profit:sum('profit'),rows:raw.map(row=>({...row,initial:row.initial/100,ending:row.ending/100,cashIncome:row.cashIncome/100,priceChange:row.priceChange/100,fees:row.fees/100,profit:row.profit/100}))};
}
