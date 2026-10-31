import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync,rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Store,ApiError } from '../server/store.ts';
import { createServer } from '../server/http.ts';
import type { AppState,InvestmentAnswerResult,InvestmentLabResult } from '../shared/types.ts';

const answers=[['horizon','fit','deadline'],['deposit-bond','yes','market'],['coupon','minus24','all'],['stock','no','decision'],['fund','normal','market'],['promises','pause','prices']];
const input={allocation:{cash:2,bond:3,stock:3,fund:2},scenarioId:'down',reasonId:'risk'};
const clock=()=>Date.UTC(2026,9,31,15);
function setup(){const db=new Store(':memory:',clock);const id=db.authenticate(db.createSession().token);return{db,id};}
function base(state:AppState){const {investments,...rest}=state;return rest;}
const status=(expected:number)=>(error:unknown)=>error instanceof ApiError&&error.status===expected;

test('investment route opens independently, requires action plus reason and never discloses server keys',()=>{
 const{db,id}=setup();const before=db.state(id);const lessons=before.investments.lessons;
 assert.equal(before.goal,null);assert.equal(lessons.length,6);assert.deepEqual(lessons.map(l=>l.unlocked),[true,false,false,false,false,false]);
 for(const lesson of lessons){assert.ok(lesson.concept&&lesson.example&&lesson.question&&lesson.sources.length);for(const key of ['correctChoice','correctReason','explanation'])assert.ok(!(key in lesson));}
 assert.throws(()=>db.answerInvestmentLesson(id,'deposit-bond','yes','market'),status(409));
 for(const [choice,reason] of [[undefined,'deadline'],['fit',undefined],['bad','deadline'],['fit','bad']])assert.throws(()=>db.answerInvestmentLesson(id,'horizon',choice,reason),status(400));
 assert.throws(()=>db.answerInvestmentLesson(id,'missing','fit','deadline'),status(404));
 const result=db.answerInvestmentLesson(id,'horizon','fit','past');assert.equal(result.correct,false);assert.equal(result.state.investments.lessons[0].completed,true);assert.equal(result.state.investments.lessons[1].unlocked,true);
 assert.equal(result.state.investments.progress.completedLessons,1);assert.equal(result.state.investments.progress.nextLessonId,'deposit-bond');assert.deepEqual(base(result.state),base(before));assert.equal(result.state.assessment.status,'not_started');db.close();
});

test('server checks each lesson, preserves first attempt and deduplicates retry completion and analytics',()=>{
 let now=clock();const db=new Store(':memory:',()=>now);const id=db.authenticate(db.createSession().token);
 const wrong=db.answerInvestmentLesson(id,'horizon','max','short');const first=wrong.state.investments.lessons[0].firstAttempt;
 now+=60000;const right=db.answerInvestmentLesson(id,'horizon','fit','deadline');const saved=right.state.investments.lessons[0];assert.equal(right.correct,true);assert.equal(right.alreadyCompleted,true);assert.deepEqual(saved.firstAttempt,first);assert.equal(saved.latestAttempt!.correct,true);assert.notEqual(saved.latestAttempt!.createdAt,first!.createdAt);
 now+=60000;const retry=db.answerInvestmentLesson(id,'horizon','fit','deadline');assert.deepEqual(retry.state.investments.lessons[0],saved);
 for(const [lesson,choice,reason] of answers.slice(1)){const result=db.answerInvestmentLesson(id,lesson,choice,reason);assert.equal(result.correct,true,lesson);if(lesson==='coupon')assert.match(result.explanation,/976.*−24/);}
 const final=db.state(id);assert.equal(final.investments.progress.completedLessons,6);assert.equal(final.investments.progress.nextLessonId,null);assert.equal(final.progress.completedLessons,0);assert.equal(final.progress.firstAnswerAt,null);
 assert.equal(db.db.prepare('SELECT COUNT(*) AS n FROM investment_answers').get()!.n,6);assert.equal(db.db.prepare("SELECT COUNT(*) AS n FROM events WHERE name='investment_lesson_answered'").get()!.n,6);assert.equal(db.db.prepare('SELECT COUNT(*) AS n FROM answers').get()!.n,0);db.close();
});

test('lab recomputes consequences, records reviewed scenarios once and is isolated from all personal money and D7',()=>{
 let now=clock();const db=new Store(':memory:',()=>now);const id=db.authenticate(db.createSession().token);const other=db.authenticate(db.createSession().token);
 db.saveGoal(id,{title:'Поездка',target:12000,saved:2000,months:4,monthly:2500,income:12000,essential:8500,reserve:1000});db.answerLesson(id,'budget','available');const before=base(db.state(id));
 const first=db.saveInvestmentLab(id,input);assert.equal(first.correct,true);assert.equal(first.result.ending,9025.02);assert.equal(first.state.investments.lab.lastResult!.ending,9025.02);assert.deepEqual(first.state.investments.lab.reviewedScenarioIds,['down']);assert.deepEqual(base(first.state),before);
 now+=60000;const retry=db.saveInvestmentLab(id,input);assert.deepEqual(retry.state.investments.lab,first.state.investments.lab);
 const wrong=db.saveInvestmentLab(id,{...input,reasonId:'fund_safe'});assert.equal(wrong.correct,false);assert.equal(wrong.state.investments.lab.lastCorrect,false);assert.equal(wrong.state.investments.lab.reviewedScenarioIds.length,1);
 db.saveInvestmentLab(id,{...input,scenarioId:'growth'});const final=db.saveInvestmentLab(id,{...input,scenarioId:'early',allocation:{cash:10,bond:0,stock:0,fund:0}});assert.equal(final.result.ending,10000);assert.deepEqual(final.state.investments.lab.reviewedScenarioIds,['down','growth','early']);assert.deepEqual(base(final.state),before);
 assert.equal(db.db.prepare("SELECT COUNT(*) AS n FROM events WHERE name='investment_scenario_reviewed'").get()!.n,3);assert.equal(db.state(other).investments.lab.lastResult,null);assert.equal(db.state(other).investments.progress.completedLessons,0);assert.equal(db.state(other).goal,null);
 for(const invalid of [{...input,allocation:{cash:1,bond:0,stock:0,fund:0}},{...input,scenarioId:'future'},{...input,reasonId:'sure'},{...input,result:{ending:1000000}},{...input,allocation:{cash:10,bond:0,stock:0,fund:0,extra:0}}])assert.throws(()=>db.saveInvestmentLab(id,invalid),status(400));
 assert.deepEqual(db.state(id).investments.lab,final.state.investments.lab);assert.throws(()=>db.saveInvestmentLab('unknown',input),status(401));db.close();
});

test('additive migration preserves existing state, module survives restart and account deletion cascades',()=>{
 const dir=mkdtempSync(join(tmpdir(),'teyl-investment-'));const path=join(dir,'db.sqlite');let db=new Store(path,clock);
 try{
  const session=db.createSession();const id=db.authenticate(session.token);db.updateProfile(id,{name:'Саша',focus:'curious'});db.answerLesson(id,'budget','available');const before=base(db.state(id));
  db.db.exec('DROP TABLE investment_answers;DROP TABLE investment_labs;');db.close();db=new Store(path,clock);assert.deepEqual(base(db.state(id)),before);assert.equal(db.state(id).investments.progress.completedLessons,0);
  db.answerInvestmentLesson(id,'horizon','fit','deadline');db.saveInvestmentLab(id,input);const saved=db.state(id);db.close();db=new Store(path,clock);assert.deepEqual(db.state(db.authenticate(session.token)),saved);
  const other=db.authenticate(db.createSession().token);db.answerInvestmentLesson(other,'horizon','fit','deadline');db.saveInvestmentLab(other,input);db.deleteAccount(id);
  assert.throws(()=>db.authenticate(session.token),status(401));assert.equal(db.db.prepare('SELECT COUNT(*) AS n FROM investment_answers WHERE session_id=?').get(id)!.n,0);assert.equal(db.db.prepare('SELECT COUNT(*) AS n FROM investment_labs WHERE session_id=?').get(id)!.n,0);assert.equal(db.db.prepare('SELECT COUNT(*) AS n FROM events WHERE session_id=?').get(id)!.n,0);assert.equal(db.state(other).investments.progress.completedLessons,1);
 }finally{db.close();rmSync(dir,{recursive:true});}
});

test('investment HTTP routes enforce auth, return complete state and reject malformed input',async t=>{
 const db=new Store(':memory:',clock);const server=createServer({store:db});await new Promise<void>(resolve=>server.listen(0,'127.0.0.1',resolve));t.after(async()=>{await new Promise<void>(resolve=>server.close(()=>resolve()));db.close();});const address=server.address();assert.ok(address&&typeof address==='object');const baseUrl=`http://127.0.0.1:${address.port}`;const {token}=db.createSession();const other=db.createSession().token;
 const request=(path:string,body:unknown,auth:string|undefined=token)=>fetch(baseUrl+path,{method:'POST',headers:{'Content-Type':'application/json',...(auth?{Authorization:`Bearer ${auth}`}:{})},body:JSON.stringify(body)});
 for(const path of ['/api/investments/lessons/horizon/answer','/api/investments/lab']){assert.equal((await request(path,{},'')).status,401);assert.equal((await request(path,{},'x'.repeat(43))).status,401);}
 assert.equal((await request('/api/investments/lessons/coupon/answer',{choiceId:'minus24',reasonId:'all'})).status,409);assert.equal((await request('/api/investments/lessons/horizon/answer',{choiceId:'fit'})).status,400);assert.equal((await request('/api/investments/lessons/missing/answer',{choiceId:'fit',reasonId:'deadline'})).status,404);
 const answer=await request('/api/investments/lessons/horizon/answer',{choiceId:'fit',reasonId:'deadline'});assert.equal(answer.status,200);assert.equal((await answer.json() as InvestmentAnswerResult).correct,true);
 const result=await request('/api/investments/lab',input);assert.equal(result.status,200);assert.equal((await result.json() as InvestmentLabResult).result.ending,9025.02);assert.equal((await request('/api/investments/lab',{...input,allocation:{cash:10,bond:0,stock:0,fund:0.1}})).status,400);
 const reload=await fetch(baseUrl+'/api/bootstrap',{headers:{Authorization:`Bearer ${token}`}});assert.equal((await reload.json() as AppState).investments.lab.lastScenarioId,'down');const isolated=await fetch(baseUrl+'/api/bootstrap',{headers:{Authorization:`Bearer ${other}`}});assert.equal((await isolated.json() as AppState).investments.lab.lastResult,null);
});
