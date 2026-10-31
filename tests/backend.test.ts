import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Store, calculateGoal, ApiError } from '../server/store.ts';
import { createServer } from '../server/http.ts';

const goal = {title:'Наушники',target:12000,saved:2000,months:4,monthly:2500,income:12000,essential:8500,reserve:1000};

test('goal calculation includes obligations, reserve, deadline and zero contribution', () => {
  assert.deepEqual(calculateGoal(goal), {available:2500,requiredMonthly:2500,projected:12000,feasible:true,suggestedMonths:4});
  assert.equal(calculateGoal({...goal,monthly:3000}).feasible,false);
  assert.equal(calculateGoal({...goal,income:9000}).feasible,false);
  assert.equal(calculateGoal({...goal,monthly:0}).suggestedMonths,null);
  assert.equal(calculateGoal({...goal,saved:12000,monthly:0}).feasible,true);
  assert.equal(calculateGoal({...goal,saved:13000,monthly:0}).requiredMonthly,0);
  assert.equal(calculateGoal({...goal,target:12001}).requiredMonthly,2501);
});

test('fixed route gates content, preserves first attempt and has no duplicate completion events', () => {
  const db=new Store(':memory:'); const {token}=db.createSession(); const id=db.authenticate(token);
  assert.throws(()=>db.answerLesson(id,'income','adjust'), /Сначала/);
  assert.equal(db.answerLesson(id,'budget','all').correct,false);
  assert.equal(db.answerLesson(id,'budget','available').correct,true);
  assert.equal(db.state(id).progress.completedLessons,1);
  db.answerLesson(id,'goal','2500');db.answerLesson(id,'contribution','buffer');
  assert.equal(db.state(id).progress.nextSimulationId,'plan');
  assert.equal(db.state(id).progress.nextLessonId,null);
  assert.throws(()=>db.answerLesson(id,'income','adjust'),/Сначала/);
  db.answerSimulation(id,'plan','2500','subtract');
  db.answerLesson(id,'income','adjust');db.answerLesson(id,'expense','replan');db.answerLesson(id,'safety','official');
  db.answerSimulation(id,'change','extend','essentials');
  const state=db.state(id);
  assert.equal(state.progress.completedLessons,6);assert.equal(state.progress.completedSimulations,2);
  assert.equal(state.progress.nextLessonId,null); assert.equal(state.progress.nextSimulationId,null);
  assert.equal(db.db.prepare("SELECT COUNT(*) AS n FROM events WHERE name='lesson_answered'").get()!.n,6);
  assert.equal(db.db.prepare("SELECT correct FROM answers WHERE content_id='budget'").get()!.correct,0);
  assert.equal(JSON.stringify(state).includes('explanations'),false);
  db.close();
});

test('goal and progress are isolated and survive reopening SQLite',()=>{
  const dir=mkdtempSync(join(tmpdir(),'teyl-db-')); const path=join(dir,'db.sqlite');
  let db=new Store(path); const a=db.createSession(),b=db.createSession(); const aid=db.authenticate(a.token),bid=db.authenticate(b.token);
  db.saveGoal(aid,goal);db.answerLesson(aid,'budget','available');
  assert.equal(db.state(bid).goal,null);assert.equal(db.state(bid).progress.completedLessons,0);
  const raw=db.db.prepare('SELECT token_hash FROM sessions WHERE id=?').get(aid)!.token_hash;
  assert.notEqual(raw,a.token);db.close();
  db=new Store(path);assert.equal(db.state(db.authenticate(a.token)).goal!.title,'Наушники');
  assert.equal(db.state(db.authenticate(a.token)).progress.completedLessons,1);
  db.deleteAccount(aid);assert.throws(()=>db.authenticate(a.token),/Сессия/);
  assert.equal(db.state(bid).goal,null);db.close();rmSync(dir,{recursive:true});
});

test('assessment opens at exactly 144 hours, closes at 216, and saves both answer elements',()=>{
  let now=Date.UTC(2026,9,31);const start=now;const db=new Store(':memory:',()=>now);
  const id=db.authenticate(db.createSession().token);
  assert.equal(db.state(id).assessment.status,'not_started');db.answerLesson(id,'budget','available');
  now=start+144*3600000-1;assert.equal(db.state(id).assessment.status,'waiting');
  assert.throws(()=>db.answerAssessment(id,'adjust','budget'),/недоступна/);
  now++;assert.equal(db.state(id).assessment.status,'available');
  const result=db.answerAssessment(id,'adjust','budget');assert.equal(result.correct,true);
  assert.equal(db.state(id).assessment.status,'completed');
  assert.equal(db.answerAssessment(id,'borrow','growth').correct,true);
  const second=db.authenticate(db.createSession().token);db.answerLesson(second,'budget','available');
  now+=216*3600000;assert.equal(db.state(second).assessment.status,'expired');
  assert.throws(()=>db.answerAssessment(second,'adjust','budget'),/недоступна/);db.close();
});

test('validations reject nonfinite, unknown enums, huge text and negative financial amounts',()=>{
  const db=new Store(':memory:');const id=db.authenticate(db.createSession().token);
  for(const payload of [{...goal,target:NaN},{...goal,months:0},{...goal,saved:-1},{...goal,monthly:1.2},{...goal,title:' '.repeat(50)},{...goal,target:1000001}]){
    assert.throws(()=>db.saveGoal(id,payload),ApiError);
  }
  assert.throws(()=>db.updateProfile(id,{ageBand:'13'}),ApiError);
  assert.throws(()=>db.updateProfile(id,{ageBand:['18-20']}),ApiError);
  assert.throws(()=>db.updateProfile(id,{reducedMotion:'false'}),ApiError);
  assert.throws(()=>db.answerLesson(id,'budget','made-up'),ApiError);
  assert.throws(()=>db.checkin(id,{type:'saved',amount:100}),ApiError);
  db.saveGoal(id,goal);assert.throws(()=>db.checkin(id,{type:'saved',amount:-2}),ApiError);
  assert.throws(()=>db.checkin(id,{type:['saved'],amount:500}),ApiError);
  db.checkin(id,{type:'saved',amount:500,note:'Отложил'},'entry-one');
  db.checkin(id,{type:'saved',amount:500,note:'Отложил'},'entry-one');
  assert.equal(db.state(id).goal!.saved,2500);assert.equal(db.state(id).checkins.length,1);
  assert.throws(()=>db.checkin(id,{type:'saved',amount:600,note:'Отложил'},'entry-one'),(e:unknown)=>e instanceof ApiError&&e.status===409&&e.code==='IDEMPOTENCY_CONFLICT');
  assert.equal(db.state(id).goal!.saved,2500);
  db.checkin(id,{type:'reviewed',note:'Пересмотрел срок'});assert.equal(db.state(id).goal!.saved,2500);
  db.close();
});

test('HTTP authentication, malformed body, request limits, CORS and deletion', async(t)=>{
  const store=new Store(':memory:');const server=createServer({store});
  await new Promise<void>(r=>server.listen(0,'127.0.0.1',r));
  t.after(async()=>{await new Promise<void>(r=>server.close(()=>r()));store.close();});
  const addr=server.address();assert.ok(addr&&typeof addr==='object');const base=`http://127.0.0.1:${addr.port}`;
  const request=(path:string,options:RequestInit={})=>fetch(base+path,options);
  assert.equal((await request('/api/health')).status,200);
  assert.equal((await request('/api/bootstrap')).status,401);
  const unknown=await request('/api/bootstrap',{headers:{Authorization:`Bearer ${'a'.repeat(43)}`}});
  assert.equal(unknown.status,401);assert.equal((await unknown.json() as {code:string}).code,'UNAUTHORIZED');
  assert.equal((await request('/api/session',{method:'POST',headers:{'Content-Type':'application/json'},body:'{'})).status,400);
  assert.equal((await request('/api/session',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({x:'a'.repeat(17000)})})).status,413);
  assert.equal((await request('/api/session',{method:'POST',headers:{Origin:'https://attacker.example','Content-Type':'application/json'},body:'{}'})).status,403);
  const session=await (await request('/api/session',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'})).json() as {token:string};
  const headers={'Authorization':`Bearer ${session.token}`,'Content-Type':'application/json'};
  assert.equal((await request('/api/bootstrap',{headers})).status,200);
  const res=await request('/api/goal',{method:'PUT',headers,body:JSON.stringify(goal)});assert.equal(res.status,200);
  assert.equal((await request('/api/account',{method:'DELETE',headers})).status,204);
  assert.equal((await request('/api/bootstrap',{headers})).status,401);
});

test('saving limit is aggregate and failed marks leave goal, history and events unchanged',()=>{
  const db=new Store(':memory:');const id=db.authenticate(db.createSession().token);
  db.saveGoal(id,{...goal,saved:999900,target:1000000,monthly:0});
  db.checkin(id,{type:'saved',amount:100},'last-100');
  const before=db.db.prepare('SELECT COUNT(*) AS n FROM events').get()!.n;
  assert.equal(db.state(id).goal!.saved,1000000);
  assert.throws(()=>db.checkin(id,{type:'saved',amount:1},'excess'),ApiError);
  assert.equal(db.state(id).checkins.length,1);assert.equal(db.state(id).goal!.saved,1000000);
  assert.equal(db.db.prepare('SELECT COUNT(*) AS n FROM events').get()!.n,before);
  assert.equal(calculateGoal({...goal,saved:12000,monthly:0,income:0,essential:0,reserve:0}).feasible,true);
  assert.equal(calculateGoal({...goal,saved:12000,monthly:0,income:0,essential:1,reserve:0}).feasible,false);
  db.close();
});

test('D7 stays available until final millisecond, reason affects score, token shape is strict',()=>{
  let now=0;const db=new Store(':memory:',()=>now);const session=db.createSession();const id=db.authenticate(session.token);
  for(const value of [undefined,'',session.token+'a','invalid', ' '.repeat(43)])assert.throws(()=>db.authenticate(value),ApiError);
  db.answerLesson(id,'budget','available');now=216*3600000-1;
  assert.equal(db.state(id).assessment.status,'available');
  assert.equal(db.answerAssessment(id,'adjust','growth').correct,false);
  assert.equal(db.state(id).assessment.correct,false);
  const second=db.authenticate(db.createSession().token);db.answerLesson(second,'budget','available');now+=216*3600000;
  assert.equal(db.state(second).assessment.status,'expired');db.close();
});

test('parallel session creation is rate limited and production CORS uses explicit allowlist',async(t)=>{
  const store=new Store(':memory:');const server=createServer({store,production:true,allowedOrigins:['https://teyl.example']});
  await new Promise<void>(r=>server.listen(0,'127.0.0.1',r));
  t.after(async()=>{await new Promise<void>(r=>server.close(()=>r()));store.close();});
  const addr=server.address();assert.ok(addr&&typeof addr==='object');const base=`http://127.0.0.1:${addr.port}`;
  const denied=await fetch(base+'/api/health',{headers:{Origin:'http://localhost:8081'}});assert.equal(denied.status,403);
  const allowed=await fetch(base+'/api/health',{headers:{Origin:'https://teyl.example'}});assert.equal(allowed.status,200);assert.equal(allowed.headers.get('Access-Control-Allow-Origin'),'https://teyl.example');
  const statuses=await Promise.all(Array.from({length:32},async()=>{const r=await fetch(base+'/api/session',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'});await r.json();return r.status;}));
  assert.equal(statuses.filter(s=>s===201).length,30);assert.equal(statuses.filter(s=>s===429).length,2);
});
