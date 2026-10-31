import test from 'node:test';
import assert from 'node:assert/strict';
import { summarizeExpenses,filterExpenses,parseRubles,localDateKey,weekStartKey } from '../src/budgetModel.ts';
import type { Expense } from '../shared/types.ts';
const entry=(id:string,occurredOn:string,amount:number,category:Expense['category']='food',voidedAt:string|null=null):Expense=>({id,occurredOn,amount,category,voidedAt,createdAt:'2026-11-02T00:00:00Z',note:''});

test('month/week totals use actual nonvoid records with cross-month week boundaries',()=>{
 const now=new Date(2026,10,1,12);const records=[entry('a','2026-10-26',100),entry('b','2026-10-31',200),entry('c','2026-11-01',300,'transport'),entry('d','2026-11-01',500,'food','2026-11-01T12:00:00Z'),entry('e','2026-11-02',700)];
 const result=summarizeExpenses(records,now);assert.equal(localDateKey(now),'2026-11-01');assert.equal(weekStartKey(now),'2026-10-26');assert.equal(result.monthTotal,300);assert.equal(result.weekTotal,600);assert.equal(result.byCategory.transport,300);assert.equal(result.byCategory.food,0);
 assert.deepEqual(filterExpenses(records,'month','all',false,now).map(x=>x.id),['c']);assert.deepEqual(filterExpenses(records,'month','all',true,now).map(x=>x.id),['c','d']);assert.equal(filterExpenses(records,'all','all',true,now).length,5);
});

test('amount parsing permits grouped whole rubles, rejects decimals and blank input',()=>{
 assert.equal(parseRubles('1 200'),1200);assert.equal(parseRubles('1\u202f200'),1200);assert.ok(Number.isNaN(parseRubles('1.5')));assert.ok(Number.isNaN(parseRubles('-5')));assert.ok(Number.isNaN(parseRubles('')));
});
