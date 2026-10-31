import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { Store, ApiError } from '../server/store.ts';
import { createServer } from '../server/http.ts';
import type { AppState, CharacterId } from '../shared/types.ts';

const clock=()=>Date.UTC(2026,9,31,15);
const goal={title:'Наушники',target:12000,saved:2000,months:4,monthly:2500,income:12000,essential:8500,reserve:1000};
const validationError=(error:unknown)=>error instanceof ApiError&&error.status===400&&error.code==='VALIDATION';

function personalState(db:Store,id:string){
 db.updateProfile(id,{name:'Мира',ageBand:'18-20',onboardingCompleted:true,reducedMotion:true,focus:'gentle'});
 db.saveGoal(id,goal);
 db.answerLesson(id,'budget','available');
 db.checkin(id,{type:'saved',amount:100,note:'На цель'},'first-saving');
 db.planAction(id,{type:'review',note:'Проверить свой бюджет',dueDate:'2026-11-01'},'first-review');
 db.addExpense(id,{amount:200,category:'food',occurredOn:'2026-10-31',note:'Обед'},'first-expense');
 return db.state(id);
}

test('anonymous sessions start with no selected character',t=>{
 const db=new Store(':memory:',clock);t.after(()=>db.close());
 const session=db.createSession();const id=db.authenticate(session.token);
 assert.deepEqual(session.state.profile,{name:'',ageBand:'other',onboardingCompleted:false,reducedMotion:false,character:null,focus:null,focusUpdatedAt:null,createdAt:new Date(clock()).toISOString()});
 assert.equal(db.state(id).profile.character,null);
 const persisted=JSON.parse(String(db.db.prepare('SELECT profile FROM sessions WHERE id=?').get(id)!.profile));
 assert.equal(persisted.character,null);
 assert.equal(db.updateProfile(id,{character:'lumi'}).profile.character,'lumi');
});

test('legacy character migration and reopening preserve personal state and existing focus',t=>{
 const dir=mkdtempSync(join(tmpdir(),'teyl-character-'));const path=join(dir,'db.sqlite');
 let db=new Store(path,clock);t.after(()=>{db.close();rmSync(dir,{recursive:true});});
 const {token}=db.createSession();const id=db.authenticate(token);const before=personalState(db,id);
 const legacy={...before.profile} as Record<string,unknown>;delete legacy.character;
 db.db.prepare('UPDATE sessions SET profile=? WHERE id=?').run(JSON.stringify(legacy),id);db.close();
 db=new Store(path,clock);
 assert.deepEqual(db.state(db.authenticate(token)),before);
 const persisted=JSON.parse(String(db.db.prepare('SELECT profile FROM sessions WHERE id=?').get(id)!.profile));
 assert.ok(Object.hasOwn(persisted,'character'));assert.equal(persisted.character,null);
 const selected=db.updateProfile(id,{character:'nova'});
 assert.deepEqual(selected,{...before,profile:{...before.profile,character:'nova'}});
 db.close();db=new Store(path,()=>clock()+60000);
 assert.deepEqual(db.state(db.authenticate(token)),selected);
});

test('character migration composes with missing focus fields and preserves a previously selected character',t=>{
 const dir=mkdtempSync(join(tmpdir(),'teyl-character-focus-'));const path=join(dir,'db.sqlite');
 let db=new Store(path,clock);t.after(()=>{db.close();rmSync(dir,{recursive:true});});
 const cases=[{character:undefined,remove:['character','focus','focusUpdatedAt']},{character:'spark' as CharacterId,remove:['focus','focusUpdatedAt']},{character:'lumi' as CharacterId,remove:['focusUpdatedAt']}];
 const expected=cases.map(({character,remove})=>{
  const {token}=db.createSession();const id=db.authenticate(token);
  const state=personalState(db,id);const legacy={...state.profile,character:character??null} as Record<string,unknown>;
  for(const key of remove)delete legacy[key];
  db.db.prepare('UPDATE sessions SET profile=? WHERE id=?').run(JSON.stringify(legacy),id);
  return{id,token,state:{...state,profile:{...state.profile,character:character??null,focus:remove.includes('focus')?null:state.profile.focus,focusUpdatedAt:null}}};
 });
 db.close();db=new Store(path,clock);
 for(const item of expected){
  assert.equal(db.authenticate(item.token),item.id);assert.deepEqual(db.state(item.id),item.state);
  const profile=JSON.parse(String(db.db.prepare('SELECT profile FROM sessions WHERE id=?').get(item.id)!.profile));
  for(const key of ['character','focus','focusUpdatedAt'])assert.ok(Object.hasOwn(profile,key));
 }
 db.close();db=new Store(path,clock);
 for(const item of expected)assert.deepEqual(db.state(item.id),item.state);
});

test('all character changes are isolated, repeatable and preserve unrelated profile and learning state',t=>{
 const db=new Store(':memory:',clock);t.after(()=>db.close());
 const id=db.authenticate(db.createSession().token);const other=db.authenticate(db.createSession().token);
 const before=personalState(db,id);const otherBefore=db.state(other);
 for(const character of ['lumi','spark','nova'] as const){
  const expected={...before,profile:{...before.profile,character}};
  assert.deepEqual(db.updateProfile(id,{character}),expected);
  assert.deepEqual(db.updateProfile(id,{character}),expected);
  assert.deepEqual(db.state(other),otherBefore);
 }
 assert.equal(db.updateProfile(id,{name:'Мирослава'}).profile.character,'nova');
 assert.equal(db.updateProfile(other,{character:'spark'}).profile.character,'spark');
 assert.equal(db.state(id).profile.character,'nova');
});

test('invalid character or mixed profile payload never persists partial changes',t=>{
 const db=new Store(':memory:',clock);t.after(()=>db.close());
 const id=db.authenticate(db.createSession().token);personalState(db,id);db.updateProfile(id,{character:'lumi'});
 const before=db.state(id);const rawBefore=db.session(id).profile;
 const eventsBefore=db.db.prepare('SELECT COUNT(*) AS n FROM events').get()!.n;
 for(const character of [null,undefined,'unknown','','LUMI',['spark'],true,false,1,{}, {id:'nova'}]){
  assert.throws(()=>db.updateProfile(id,{name:'Изменённое имя',ageBand:'21-22',focus:'goal',character}),validationError);
  assert.deepEqual(db.state(id),before);assert.equal(db.session(id).profile,rawBefore);
 }
 for(const payload of [{character:'spark',reducedMotion:'false'},{character:'spark',bankAccount:'123456'},{character:'spark',createdAt:'2000-01-01'}]){
  assert.throws(()=>db.updateProfile(id,payload),validationError);assert.deepEqual(db.state(id),before);
 }
 assert.equal(db.db.prepare('SELECT COUNT(*) AS n FROM events').get()!.n,eventsBefore);
});

test('HTTP PATCH profile saves and returns only supported characters for the authenticated anonymous session',async t=>{
 const store=new Store(':memory:',clock);const server=createServer({store});
 t.after(async()=>{if(server.listening)await new Promise<void>(resolve=>server.close(()=>resolve()));store.close();});
 await new Promise<void>(resolve=>server.listen(0,'127.0.0.1',resolve));
 const address=server.address();assert.ok(address&&typeof address==='object');const base=`http://127.0.0.1:${address.port}/api`;
 const create=async()=>{
  const response=await fetch(base+'/session',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'});
  assert.equal(response.status,201);return await response.json() as {token:string;state:AppState};
 };
 const session=await create();const other=await create();assert.equal(session.state.profile.character,null);
 const id=store.authenticate(session.token);personalState(store,id);const before=store.state(id);
 const patch=(payload:unknown,token=session.token)=>fetch(base+'/profile',{method:'PATCH',headers:{Authorization:`Bearer ${token}`,'Content-Type':'application/json'},body:JSON.stringify(payload)});
 const unauthenticated=await fetch(base+'/profile',{method:'PATCH',headers:{'Content-Type':'application/json'},body:JSON.stringify({character:'nova'})});
 assert.equal(unauthenticated.status,401);
 for(const character of ['lumi','spark','nova'] as const){
  const response=await patch({character});assert.equal(response.status,200);
  assert.deepEqual(await response.json(),JSON.parse(JSON.stringify({...before,profile:{...before.profile,character}})));
 }
 const selected=store.state(id);
 for(const payload of [{character:null},{character:'unknown'},{character:['lumi']},{character:false},{character:{id:'lumi'}},{name:'Новое имя',character:'unknown'},{character:'lumi',bankAccount:'123456'}]){
  const response=await patch(payload);assert.equal(response.status,400);
  assert.equal((await response.json() as {code:string}).code,'VALIDATION');assert.deepEqual(store.state(id),selected);
 }
 const bootstrap=await fetch(base+'/bootstrap',{headers:{Authorization:`Bearer ${session.token}`}});assert.equal(bootstrap.status,200);assert.deepEqual(await bootstrap.json(),JSON.parse(JSON.stringify(selected)));
 assert.deepEqual(store.state(store.authenticate(other.token)),other.state);
 const changeOther=await patch({character:'spark'},other.token);assert.equal(changeOther.status,200);await changeOther.json();
 assert.equal(store.state(id).profile.character,'nova');
});
