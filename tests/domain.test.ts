import test from 'node:test';
import assert from 'node:assert/strict';
import {forecastGoal} from '../src/domain.ts';
const goal={title:'Наушники',target:12000,saved:2000,months:4,monthly:2500,income:12000,essential:8500,reserve:1000};
test('Personal scenario exposes unfunded projections instead of promising the target',()=>{
 const baseline=forecastGoal(goal,0,0,2500,4);assert.equal(baseline.feasible,true);assert.equal(baseline.projected,12000);
 const drop=forecastGoal(goal,-3000,0,2500,4);assert.equal(drop.feasible,false);assert.equal(drop.available,0);assert.equal(drop.affordableMonths,null);assert.equal(drop.projected,12000);assert.equal(drop.budgetBalance,-500);
});
test('Personal scenario handles longer timeline and already reached target',()=>{
 const slow=forecastGoal(goal,0,0,2000,5);assert.equal(slow.feasible,true);assert.equal(slow.affordableMonths,5);
 const expense=forecastGoal(goal,0,1500,1000,4);assert.equal(expense.feasible,false);assert.equal(expense.gap,6000);assert.equal(expense.affordableMonths,10);
 const reached=forecastGoal({...goal,saved:12000},0,0,0,4);assert.equal(reached.remaining,0);assert.equal(reached.affordableMonths,0);assert.equal(reached.feasible,true);
});
