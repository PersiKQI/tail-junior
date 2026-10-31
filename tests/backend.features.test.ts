import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { Store, ApiError } from '../server/store.ts';
import { createServer } from '../server/http.ts';

const goal={title:'Наушники',target:12000,saved:2000,months:4,monthly:2500,income:12000,essential:8500,reserve:1000};
const budget={income:16000,reserve:1000,monthly:3000,categories:[{id:'food',amount:4000},{id:'transport',amount:1500},{id:'housing',amount:5000},{id:'other',amount:500}]};
const date='2026-10-31';
const clock=()=>Date.UTC(2026,9,31,15);
const setup=()=>{const db=new Store(':memory:',clock);const id=db.authenticate(db.createSession().token);return{db,id};};
const unlockPlan=(db:Store,id:string)=>{db.answerLesson(id,'budget','available');db.answerLesson(id,'goal','2500');db.answerLesson(id,'contribution','buffer');};
const unlockChange=(db:Store,id:string)=>{unlockPlan(db,id);db.decideSimulation(id,'plan',{monthly:2500,months:4,reasonId:'subtract'});db.answerLesson(id,'income','adjust');db.answerLesson(id,'expense','replan');db.answerLesson(id,'safety','official');};

test('budget updates category total and goal atomically; goal edits reset only inconsistent categories',()=>{
 const {db,id}=setup();assert.equal(db.state(id).budget.income,0);assert.equal(db.state(id).budget.categories.length,4);
 db.saveGoal(id,goal);assert.equal(db.state(id).budget.categories.find(c=>c.id==='other')!.amount,8500);
 let state=db.saveBudget(id,budget);assert.equal(state.goal!.essential,11000);assert.equal(state.goal!.monthly,3000);assert.equal(state.budget.available,4000);assert.equal(state.budget.unallocated,1000);assert.equal(state.goal!.feasible,true);
 const categories=state.budget.categories;
 state=db.saveGoal(id,{...state.goal!,income:17000});assert.deepEqual(state.budget.categories,categories);assert.equal(state.budget.income,17000);
 state=db.saveGoal(id,{...state.goal!,essential:8000});assert.equal(state.budget.categories.find(c=>c.id==='other')!.amount,8000);assert.equal(state.budget.categories.find(c=>c.id==='food')!.amount,0);
 state=db.saveBudget(id,{...budget,income:1000});assert.equal(state.budget.available,0);assert.equal(state.budget.unallocated,-14000);assert.equal(state.goal!.feasible,false);db.close();
});

test('budget rejects duplicate/missing categories, floats, extreme totals and enum coercion',()=>{
 const {db,id}=setup();
 for(const value of [{...budget,income:-1},{...budget,monthly:2.3},{...budget,categories:budget.categories.slice(1)},{...budget,categories:[...budget.categories.slice(0,3),budget.categories[0]]},{...budget,categories:budget.categories.map(c=>({...c,amount:400000}))},{...budget,categories:budget.categories.map(c=>({...c,id:[c.id]}))}])assert.throws(()=>db.saveBudget(id,value),ApiError);
 assert.equal(db.state(id).budget.income,0);db.close();
});

test('goal accepts an explicit consistent breakdown and rejects mismatches without losing categories',()=>{
 const {db,id}=setup();db.saveGoal(id,goal);let state=db.saveBudget(id,budget);
 const categories=state.budget.categories.map(c=>({...c,amount:c.id==='other'?c.amount+700:c.amount}));
 state=db.saveGoal(id,{...state.goal!,essential:11700,categories});
 assert.deepEqual(state.budget.categories.map(c=>c.amount),[4000,1500,5000,1200]);assert.equal(state.goal!.essential,11700);
 const before=state;
 for(const value of [{...state.goal!,essential:11800,categories},{...state.goal!,categories:categories.slice(1)},{...state.goal!,categories:[...categories.slice(0,3),categories[0]]},{...state.goal!,categories:categories.map(c=>({...c,amount:c.id==='food'?-1:c.amount}))}])assert.throws(()=>db.saveGoal(id,value),ApiError);
 assert.deepEqual(db.state(id).budget,before.budget);assert.deepEqual(db.state(id).goal,before.goal);db.close();
});

test('action creation retries are idempotent at the active limit and reject changed payloads',()=>{
 let now=clock();const db=new Store(':memory:',()=>now);const id=db.authenticate(db.createSession().token);db.saveGoal(id,goal);
 const payload={type:'save',amount:200,note:'На цель',dueDate:date};let state=db.planAction(id,payload,'create-one');const actionId=state.actions[0].id;
 for(let i=0;i<19;i++)db.planAction(id,{...payload,note:`Действие ${i}`},`extra-${i}`);
 state=db.planAction(id,{...payload,note:' На цель '},'create-one');assert.equal(state.actions.length,20);assert.ok(state.actions.some(a=>a.id===actionId));
 for(const change of [{amount:201},{note:'Другое действие'},{dueDate:'2026-11-01'},{type:'review',note:'Проверить свой бюджет'}])assert.throws(()=>db.planAction(id,{...payload,...change},'create-one'),(e:unknown)=>e instanceof ApiError&&e.status===409&&e.code==='IDEMPOTENCY_CONFLICT');
 assert.equal(db.db.prepare("SELECT COUNT(*) AS n FROM events WHERE name='action_planned'").get()!.n,20);
 now+=86400000;assert.equal(db.planAction(id,payload,'create-one').actions.length,20);
 const other=db.authenticate(db.createSession().token);db.saveGoal(other,goal);assert.equal(db.planAction(other,{...payload,dueDate:'2026-11-01'},'create-one').actions.length,1);
 assert.throws(()=>db.planAction(id,{...payload,dueDate:'2026-11-01'},'bad key'),ApiError);db.close();
});

test('existing action table migrates idempotency column without losing history',()=>{
 const dir=mkdtempSync(join(tmpdir(),'teyl-action-migration-'));const path=join(dir,'db.sqlite');let db=new Store(path,clock);const id=db.authenticate(db.createSession().token);db.saveGoal(id,goal);
 const action=db.planAction(id,{type:'save',amount:200,note:'',dueDate:date}).actions[0];
 db.db.exec('DROP INDEX actions_idempotency; ALTER TABLE actions DROP COLUMN idempotency_key;');db.close();
 db=new Store(path,clock);assert.equal(db.state(id).actions[0].id,action.id);
 const payload={type:'save',amount:300,note:'',dueDate:date};db.planAction(id,payload,'new-key');db.planAction(id,payload,'new-key');assert.equal(db.state(id).actions.length,2);
 db.close();rmSync(dir,{recursive:true});
});

test('save action completion creates one self-report and is idempotent; reviews do not change money',()=>{
 const {db,id}=setup();db.saveGoal(id,goal);let state=db.planAction(id,{type:'save',amount:400,note:'Отложить после подработки',dueDate:date});const action=state.actions[0];assert.equal(action.completedAt,null);
 state=db.completeAction(id,action.id,{completed:true});assert.equal(state.goal!.saved,2400);assert.equal(state.checkins.length,1);assert.ok(state.actions[0].completedAt);
 state=db.completeAction(id,action.id,{completed:true});assert.equal(state.goal!.saved,2400);assert.equal(state.checkins.length,1);
 assert.throws(()=>db.deleteAction(id,action.id),(e:unknown)=>e instanceof ApiError&&e.status===409);
 state=db.planAction(id,{type:'review',note:'Проверить поступления и изменить срок',dueDate:'2026-11-01'});const review=state.actions.find(a=>a.type==='review')!;
 state=db.completeAction(id,review.id,{completed:true});assert.equal(state.goal!.saved,2400);assert.equal(state.checkins.length,2);assert.equal(state.checkins[0].type,'reviewed');
 assert.equal(db.db.prepare("SELECT COUNT(*) AS n FROM events WHERE name='action_completed'").get()!.n,2);db.close();
});

test('actions are isolated, validate dates and meaningful notes, cap active count and allow cancellation',()=>{
 const {db,id}=setup();const other=db.authenticate(db.createSession().token);db.saveGoal(id,goal);db.saveGoal(other,goal);
 for(const value of [{type:'save',amount:0,note:'',dueDate:date},{type:'save',amount:200,note:'',dueDate:'2026-10-30'},{type:'save',amount:200,note:'',dueDate:'2026-12-01'},{type:'save',amount:200,note:'',dueDate:'2026-11-31'},{type:'review',note:'       ',dueDate:date},{type:'review',note:'aaaaaaaaaa',dueDate:date}])assert.throws(()=>db.planAction(id,value),ApiError);
 let state=db.planAction(id,{type:'save',amount:200,note:'',dueDate:'2026-11-30'});const actionId=state.actions[0].id;
 assert.throws(()=>db.completeAction(other,actionId,{completed:true}),(e:unknown)=>e instanceof ApiError&&e.status===404);
 assert.equal(db.deleteAction(other,actionId).actions.length,0);assert.equal(db.state(id).actions.length,1);
 assert.throws(()=>db.completeAction(id,actionId,{completed:false}),ApiError);
 for(let i=0;i<19;i++)db.planAction(id,{type:'save',amount:200,note:'',dueDate:date});
 assert.throws(()=>db.planAction(id,{type:'save',amount:200,note:'',dueDate:date}),(e:unknown)=>e instanceof ApiError&&e.status===409);
 state=db.deleteAction(id,actionId);assert.equal(state.actions.length,19);assert.equal(state.goal!.saved,2000);assert.equal(db.deleteAction(id,actionId).actions.length,19);db.planAction(id,{type:'save',amount:200,note:'',dueDate:date});db.close();
});

test('action failure rolls back both completion and money; budget/actions survive reopen',()=>{
 const dir=mkdtempSync(join(tmpdir(),'teyl-features-'));const path=join(dir,'db.sqlite');let db=new Store(path,clock);const {token}=db.createSession();const id=db.authenticate(token);
 db.saveGoal(id,{...goal,saved:999900});db.saveBudget(id,budget);const action=db.planAction(id,{type:'save',amount:200,note:'Пополнить цель',dueDate:date}).actions[0];
 assert.throws(()=>db.completeAction(id,action.id,{completed:true}),ApiError);assert.equal(db.state(id).goal!.saved,999900);assert.equal(db.state(id).actions[0].completedAt,null);assert.equal(db.state(id).checkins.length,0);
 db.close();db=new Store(path,clock);assert.deepEqual(db.state(id).budget.categories.map(c=>c.amount),[4000,1500,5000,500]);assert.equal(db.state(id).actions[0].id,action.id);db.deleteAccount(id);assert.equal(db.db.prepare('SELECT COUNT(*) AS n FROM budgets').get()!.n,0);assert.equal(db.db.prepare('SELECT COUNT(*) AS n FROM actions').get()!.n,0);db.close();rmSync(dir,{recursive:true});
});

test('interactive plan evaluates consequence and explanation, honors gates and preserves first result',()=>{
 const {db,id}=setup();assert.throws(()=>db.decideSimulation(id,'plan',{monthly:2500,months:4,reasonId:'subtract'}),(e:unknown)=>e instanceof ApiError&&e.status===409);unlockPlan(db,id);
 let result=db.decideSimulation(id,'plan',{monthly:3000,months:4,reasonId:'subtract'});assert.equal(result.correct,false);assert.equal(result.outcome!.projected,14000);assert.equal(result.outcome!.feasible,false);assert.equal(result.outcome!.available,2500);
 result=db.decideSimulation(id,'plan',{monthly:2000,months:5,reasonId:'subtract'});assert.equal(result.correct,true);assert.equal(result.outcome!.projected,12000);assert.equal(result.outcome!.gap,0);assert.equal(result.alreadyCompleted,true);
 assert.equal(db.db.prepare("SELECT correct FROM answers WHERE kind='simulation' AND content_id='plan'").get()!.correct,0);
 result=db.decideSimulation(id,'plan',{monthly:2000,months:4,reasonId:'subtract'});assert.equal(result.correct,false);assert.equal(result.outcome!.gap,2000);
 assert.throws(()=>db.decideSimulation(id,'plan',{monthly:5001,months:4,reasonId:'subtract'}),ApiError);assert.throws(()=>db.decideSimulation(id,'plan',{monthly:2000,months:0,reasonId:'subtract'}),ApiError);db.close();
});

test('changed-income simulation checks current affordability without invented future projection',()=>{
 const {db,id}=setup();unlockChange(db,id);
 let result=db.decideSimulation(id,'change',{monthly:500,months:5,reasonId:'essentials'});assert.equal(result.correct,false);assert.equal(result.outcome!.available,0);assert.equal(result.outcome!.projected,null);assert.equal(result.outcome!.gap,null);assert.equal(result.outcome!.remaining,7500);
 result=db.decideSimulation(id,'change',{monthly:0,months:12,reasonId:'essentials'});assert.equal(result.correct,true);assert.equal(result.outcome!.feasible,true);assert.equal(result.state.progress.completedSimulations,2);db.close();
});

test('HTTP exposes budget, actions, completion, cancellation and decision through authenticated routes',async(t)=>{
 const store=new Store(':memory:',clock);const server=createServer({store});await new Promise<void>(r=>server.listen(0,'127.0.0.1',r));t.after(async()=>{await new Promise<void>(r=>server.close(()=>r()));store.close();});const addr=server.address();assert.ok(addr&&typeof addr==='object');const base=`http://127.0.0.1:${addr.port}`;
 const {token}=store.createSession();const id=store.authenticate(token);const req=(path:string,method:string,body?:unknown,key?:string)=>fetch(base+'/api'+path,{method,headers:{Authorization:`Bearer ${token}`,'Content-Type':'application/json',...(key?{'Idempotency-Key':key}:{})},body:body===undefined?undefined:JSON.stringify(body)});
 assert.equal((await req('/goal','PUT',goal)).status,200);assert.equal((await req('/budget','PUT',budget)).status,200);
 const payload={type:'save',amount:200,note:'',dueDate:date};const planned=await (await req('/actions','POST',payload,'http-key')).json() as {actions:{id:string}[]};const aid=planned.actions[0].id;
 const repeated=await (await req('/actions','POST',payload,'http-key')).json() as {actions:{id:string}[]};assert.equal(repeated.actions.length,1);assert.equal(repeated.actions[0].id,aid);
 assert.equal((await req('/actions','POST',{...payload,amount:201},'http-key')).status,409);
 assert.equal((await req(`/actions/${aid}`,'PATCH',{completed:true})).status,200);assert.equal((await req(`/actions/${aid}`,'DELETE')).status,409);
 const second=await (await req('/actions','POST',payload,'http-key-two')).json() as {actions:{id:string;completedAt:string|null}[]};const pending=second.actions.find(a=>!a.completedAt)!;
 assert.equal((await req(`/actions/${pending.id}`,'DELETE')).status,200);assert.equal((await req(`/actions/${pending.id}`,'DELETE')).status,200);
 unlockPlan(store,id);assert.equal((await req('/simulations/plan/decision','POST',{monthly:2500,months:4,reasonId:'subtract'})).status,200);
});
