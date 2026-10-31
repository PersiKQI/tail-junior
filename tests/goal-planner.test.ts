import test from 'node:test';
import assert from 'node:assert/strict';
import { goalPaces, goalMilestones, projectionPoints } from '../src/goal-planner.ts';
const goal = { title:'Наушники', target:12000, saved:2000, months:4, monthly:2500, income:12000, essential:7500, reserve:1000 };

test('Pace alternatives cover remaining goal with integer rubles, flag unaffordable acceleration and bound the horizon', () => {
 const p = goalPaces(goal);
 assert.deepEqual(p.map(x=>[x.months,x.monthly]), [[6,1667],[4,2500],[3,3334]]);
 assert.ok(p.every(x=>x.forecast.feasible));
 assert.equal(goalPaces({...goal,essential:8500})[2].forecast.feasible,false);
 assert.equal(goalPaces({...goal,months:120})[0].months,120);
 assert.equal(goalPaces({...goal,months:1})[2].months,1);
 const reached=goalPaces({...goal,saved:14000});
 assert.equal(reached[0].monthly,0);
 assert.equal(reached[2].monthly,0);
 assert.equal(goalPaces({...goal,income:7000})[0].forecast.feasible,false);
});
test('Forecast points and milestones keep exact final values without invented affordable timelines', () => {
 assert.deepEqual(projectionPoints(2000,2500,4),[{month:0,amount:2000},{month:1,amount:4500},{month:2,amount:7000},{month:3,amount:9500},{month:4,amount:12000}]);
 const points=projectionPoints(0,10,120);assert.equal(points.length,7);assert.equal(points.at(-1)?.amount,1200);assert.equal(new Set(points.map(p=>p.month)).size,points.length);
 assert.equal(goalMilestones(goal,2500)[0].months,1);
 assert.equal(goalMilestones(goal,4000)[0].months,null);
 assert.equal(goalMilestones({...goal,saved:12000},0)[3].months,0);
 assert.equal(goalMilestones({...goal,income:0},500)[0].months,null);
});
