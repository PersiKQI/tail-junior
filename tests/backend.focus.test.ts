import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { Store, ApiError } from '../server/store.ts';

test('explicit focus uses server time, validates values and preserves profile and learning progress',()=>{
 let now=Date.UTC(2026,9,31,15);const db=new Store(':memory:',()=>now);const id=db.authenticate(db.createSession().token);
 assert.equal(db.state(id).profile.focus,null);assert.equal(db.state(id).profile.focusUpdatedAt,null);
 db.updateProfile(id,{name:'Саша',ageBand:'18-20',onboardingCompleted:true,reducedMotion:true});db.answerLesson(id,'budget','available');
 const before=db.state(id);let state=db.updateProfile(id,{focus:'curious'});
 assert.equal(state.profile.focus,'curious');assert.equal(state.profile.focusUpdatedAt,new Date(now).toISOString());
 assert.equal(state.profile.name,'Саша');assert.equal(state.profile.ageBand,'18-20');assert.equal(state.profile.reducedMotion,true);assert.equal(state.profile.onboardingCompleted,true);assert.deepEqual(state.progress,before.progress);assert.deepEqual(state.lessons,before.lessons);
 const first=state.profile.focusUpdatedAt;now+=60000;state=db.updateProfile(id,{name:'Александр'});assert.equal(state.profile.focusUpdatedAt,first);
 state=db.updateProfile(id,{focus:'gentle'});assert.equal(state.profile.focusUpdatedAt,new Date(now).toISOString());
 for(const focus of ['happy','',['goal'],1,false,{}])assert.throws(()=>db.updateProfile(id,{focus}),ApiError);
 assert.throws(()=>db.updateProfile(id,{focus:'goal',focusUpdatedAt:'2000-01-01T00:00:00Z'}),ApiError);
 assert.equal(db.state(id).profile.focus,'gentle');assert.deepEqual(db.state(id).progress,before.progress);
 state=db.updateProfile(id,{focus:null});assert.equal(state.profile.focus,null);assert.equal(state.profile.focusUpdatedAt,null);db.close();
});

test('old profile JSON migrates to null focus and saved preference survives reopening',()=>{
 const dir=mkdtempSync(join(tmpdir(),'teyl-focus-'));const path=join(dir,'db.sqlite');const now=Date.UTC(2026,9,31,15);let db=new Store(path,()=>now);const session=db.createSession();const id=db.authenticate(session.token);
 db.updateProfile(id,{name:'Мира',onboardingCompleted:true});db.answerLesson(id,'budget','available');
 const old={...db.state(id).profile} as Record<string,unknown>;delete old.focus;delete old.focusUpdatedAt;db.db.prepare('UPDATE sessions SET profile=? WHERE id=?').run(JSON.stringify(old),id);db.close();
 db=new Store(path,()=>now);let state=db.state(db.authenticate(session.token));assert.equal(state.profile.focus,null);assert.equal(state.profile.focusUpdatedAt,null);assert.equal(state.profile.name,'Мира');assert.equal(state.profile.onboardingCompleted,true);assert.equal(state.progress.completedLessons,1);
 const persisted=JSON.parse(String(db.db.prepare('SELECT profile FROM sessions WHERE id=?').get(id)!.profile));assert.ok(Object.hasOwn(persisted,'focus'));assert.ok(Object.hasOwn(persisted,'focusUpdatedAt'));
 db.updateProfile(id,{focus:'goal'});db.close();db=new Store(path,()=>now+60000);state=db.state(id);assert.equal(state.profile.focus,'goal');assert.equal(state.profile.focusUpdatedAt,new Date(now).toISOString());assert.equal(state.progress.completedLessons,1);db.close();rmSync(dir,{recursive:true});
});
