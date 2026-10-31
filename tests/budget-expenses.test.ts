import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync,rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Store,ApiError } from '../server/store.ts';
import { createServer } from '../server/http.ts';

const goal={title:'Поездка',target:12000,saved:2000,months:4,monthly:2500,income:12000,essential:8500,reserve:1000};
const expense={amount:350,category:'food',occurredOn:'2026-10-31',note:'Обед',timezoneOffsetMinutes:-420};
const clock=()=>Date.UTC(2026,9,31,15);
function setup(){const db=new Store(':memory:',clock);const id=db.authenticate(db.createSession().token);return{db,id};}

test('manual expense is persisted separately, retries do not duplicate and changed payload conflicts',()=>{
 const{db,id}=setup();db.saveGoal(id,goal);const before=db.state(id);let state=db.addExpense(id,expense,'expense-1');
 assert.equal(state.expenses.length,1);assert.equal(state.expenses[0].amount,350);assert.deepEqual(state.goal,before.goal);assert.deepEqual(state.budget,before.budget);assert.deepEqual(state.checkins,before.checkins);
 const eid=state.expenses[0].id;state=db.addExpense(id,{...expense,note:' Обед '},'expense-1');assert.equal(state.expenses.length,1);assert.equal(state.expenses[0].id,eid);
 for(const change of [{amount:351},{category:'other'},{note:'Ужин'},{occurredOn:'2026-10-30'}])assert.throws(()=>db.addExpense(id,{...expense,...change},'expense-1'),(e:unknown)=>e instanceof ApiError&&e.status===409);
 assert.equal(db.db.prepare("SELECT COUNT(*) AS n FROM events WHERE name='expense_recorded'").get()!.n,1);db.close();
});

test('void and restore are idempotent and isolated without changing money',()=>{
 const{db,id}=setup();const other=db.authenticate(db.createSession().token);db.saveGoal(id,goal);const eid=db.addExpense(id,expense,'one').expenses[0].id;
 let state=db.setExpenseVoided(id,eid,{voided:true});const timestamp=state.expenses[0].voidedAt;assert.ok(timestamp);state=db.setExpenseVoided(id,eid,{voided:true});assert.equal(state.expenses[0].voidedAt,timestamp);
 state=db.setExpenseVoided(id,eid,{voided:false});assert.equal(state.expenses[0].voidedAt,null);db.setExpenseVoided(id,eid,{voided:false});assert.equal(db.state(id).goal!.saved,2000);
 assert.throws(()=>db.setExpenseVoided(other,eid,{voided:true}),(e:unknown)=>e instanceof ApiError&&e.status===404);assert.equal(db.state(other).expenses.length,0);
 assert.throws(()=>db.setExpenseVoided(id,eid,{voided:'true'}),ApiError);assert.equal(db.db.prepare("SELECT COUNT(*) AS n FROM events WHERE name IN ('expense_voided','expense_restored')").get()!.n,2);db.close();
});

test('expense validation handles calendar and local UTC boundaries without accepting future local days',()=>{
 const{db,id}=setup();
 for(const value of [{...expense,amount:0},{...expense,amount:1.5},{...expense,amount:1000001},{...expense,category:['food']},{...expense,category:'income'},{...expense,note:'x'.repeat(201)},{...expense,occurredOn:'2026-02-30'},{...expense,occurredOn:'2026-11-02'},{...expense,occurredOn:'1899-12-31'},{...expense,timezoneOffsetMinutes:841},{...expense,timezoneOffsetMinutes:1.5}])assert.throws(()=>db.addExpense(id,value),ApiError);
 db.close();const edge=new Store(':memory:',()=>Date.UTC(2026,9,31,21));const uid=edge.authenticate(edge.createSession().token);
 assert.equal(edge.addExpense(uid,{...expense,occurredOn:'2026-11-01',timezoneOffsetMinutes:-420}).expenses.length,1);
 assert.throws(()=>edge.addExpense(uid,{...expense,occurredOn:'2026-11-01',timezoneOffsetMinutes:0}),ApiError);
 const west=new Store(':memory:',()=>Date.UTC(2026,10,1,1));const wid=west.authenticate(west.createSession().token);assert.throws(()=>west.addExpense(wid,{...expense,occurredOn:'2026-11-01',timezoneOffsetMinutes:480}),ApiError);west.close();edge.close();
});

test('all 500 records are returned, voids retain capacity and existing retry still succeeds',()=>{
 const{db,id}=setup();for(let i=0;i<500;i++)db.addExpense(id,{...expense,note:`Запись ${i}`},`entry-${i}`);
 assert.equal(db.state(id).expenses.length,500);assert.throws(()=>db.addExpense(id,expense,'extra'),(e:unknown)=>e instanceof ApiError&&e.status===409&&e.code==='EXPENSE_LIMIT');
 const eid=db.state(id).expenses[0].id;db.setExpenseVoided(id,eid,{voided:true});assert.throws(()=>db.addExpense(id,expense,'extra'),ApiError);
 assert.equal(db.addExpense(id,{...expense,note:'Запись 0'},'entry-0').expenses.length,500);db.close();
});

test('expense table is added to existing database and records survive reopen/deletion',()=>{
 const dir=mkdtempSync(join(tmpdir(),'teyl-expenses-'));const path=join(dir,'db.sqlite');let db=new Store(path,clock);const id=db.authenticate(db.createSession().token);db.saveGoal(id,goal);db.db.exec('DROP TABLE expenses');db.close();
 db=new Store(path,clock);const eid=db.addExpense(id,expense,'persist').expenses[0].id;db.close();db=new Store(path,clock);assert.equal(db.state(id).expenses[0].id,eid);assert.equal(db.state(id).goal!.saved,2000);db.deleteAccount(id);assert.equal(db.db.prepare('SELECT COUNT(*) AS n FROM expenses').get()!.n,0);db.close();rmSync(dir,{recursive:true});
});

test('expense HTTP contract enforces auth, forwards retry keys and supports void/restore',async t=>{
 const db=new Store(':memory:',clock);const server=createServer({store:db});await new Promise<void>(r=>server.listen(0,'127.0.0.1',r));t.after(async()=>{await new Promise<void>(r=>server.close(()=>r()));db.close();});const address=server.address();assert.ok(address&&typeof address==='object');const base=`http://127.0.0.1:${address.port}`;const {token}=db.createSession();
 const req=(path:string,method:string,body:unknown,key?:string)=>fetch(base+path,{method,headers:{Authorization:`Bearer ${token}`,'Content-Type':'application/json',...(key?{'Idempotency-Key':key}:{})},body:JSON.stringify(body)});
 assert.equal((await fetch(base+'/api/expenses',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(expense)})).status,401);
 const created=await(await req('/api/expenses','POST',expense,'http')).json() as {expenses:{id:string}[]};const id=created.expenses[0].id;
 const repeated=await(await req('/api/expenses','POST',expense,'http')).json() as {expenses:{id:string}[]};assert.equal(repeated.expenses.length,1);
 assert.equal((await req('/api/expenses/'+id,'PATCH',{voided:true})).status,200);assert.equal((await req('/api/expenses/'+id,'PATCH',{voided:false})).status,200);
});
