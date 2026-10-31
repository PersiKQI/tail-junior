import test from 'node:test';
import assert from 'node:assert/strict';
import { evaluatePortfolio, validateInvestmentAllocation, INVESTMENT_SCENARIOS } from '../shared/investment.ts';
import type { InvestmentAllocation, InvestmentScenarioId } from '../shared/types.ts';

const allocation:InvestmentAllocation={cash:2,bond:3,stock:3,fund:2};
test('fictional mixed portfolio includes both commissions and separates cash income from price movement',()=>{
 const expectations={growth:{ending:10333.71,cashIncome:60,priceChange:290,fees:16.29,profit:333.71},down:{ending:9025.02,cashIncome:60,priceChange:-1020,fees:14.98,profit:-974.98},early:{ending:9554.43,cashIncome:0,priceChange:-430,fees:15.57,profit:-445.57}};
 for(const scenarioId of ['growth','down','early'] as const){
  const result=evaluatePortfolio(allocation,scenarioId);assert.equal(result.initial,10000);assert.equal(result.scenarioId,scenarioId);
  for(const key of ['ending','cashIncome','priceChange','fees','profit'] as const)assert.equal(result[key],expectations[scenarioId][key],`${scenarioId}.${key}`);
 }
 const bond=evaluatePortfolio({cash:9,bond:1,stock:0,fund:0},'growth').rows.find(r=>r.asset==='bond')!;
 assert.equal(bond.fees,2.01);assert.equal(bond.profit,27.99);assert.equal(bond.ending,1027.99);
 const loss=evaluatePortfolio({cash:0,bond:10,stock:0,fund:0},'down');assert.equal(loss.cashIncome,200);assert.equal(loss.profit,-419.4);
});

test('all-cash has no invented yield or fees and every permitted allocation reconciles to kopecks',()=>{
 for(const scenario of INVESTMENT_SCENARIOS){
  const cash=evaluatePortfolio({cash:10,bond:0,stock:0,fund:0},scenario.id);assert.equal(cash.ending,10000);assert.equal(cash.fees,0);assert.equal(cash.profit,0);
  for(let c=0;c<=10;c++)for(let b=0;b<=10-c;b++)for(let s=0;s<=10-c-b;s++){
   const result=evaluatePortfolio({cash:c,bond:b,stock:s,fund:10-c-b-s},scenario.id);
   const kopecks=(value:number)=>Math.round(value*100);
   assert.equal(result.initial,10000);assert.equal(kopecks(result.ending),kopecks(result.initial)+kopecks(result.profit));
   assert.equal(kopecks(result.profit),kopecks(result.priceChange)+kopecks(result.cashIncome)-kopecks(result.fees));
   for(const key of ['initial','ending','cashIncome','priceChange','fees','profit'] as const)assert.equal(kopecks(result[key]),result.rows.reduce((sum,row)=>sum+kopecks(row[key]),0));
  }
 }
});

test('allocation rejects missing, extra, fractional, negative and nonnumeric units and unknown scenarios',()=>{
 for(const invalid of [null,[],{}, {cash:10,bond:0,stock:0}, {...allocation,other:0}, {...allocation,cash:-1,bond:6}, {...allocation,cash:2.5,bond:2.5}, {...allocation,cash:'2'}, {...allocation,cash:NaN}, {...allocation,cash:Infinity}, {...allocation,cash:11}, {...allocation,cash:1}])assert.throws(()=>validateInvestmentAllocation(invalid));
 assert.throws(()=>evaluatePortfolio(allocation,'random' as InvestmentScenarioId));
 const copy=validateInvestmentAllocation(allocation);copy.cash=0;assert.equal(allocation.cash,2);
});
