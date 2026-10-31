import { DatabaseSync } from 'node:sqlite';
import { randomBytes, randomUUID, createHash } from 'node:crypto';
import { mkdirSync } from 'node:fs';
import { dirname } from 'node:path';
import type { AppState, Profile, GoalInput, Goal, Checkin, AnswerResult, Assessment, Budget, BudgetCategory, PlannedAction, SimulationOutcome, Expense, InvestmentsState, InvestmentAttempt, InvestmentLabState, InvestmentAnswerResult, InvestmentLabResult, InvestmentScenarioId } from '../shared/types.ts';
import { lessons, simulations, assessmentContent, CONTENT_VERSION } from './content.ts';
import { investmentLessons, INVESTMENT_CONTENT_VERSION } from './investment-content.ts';
import { DEFAULT_INVESTMENT_ALLOCATION, INVESTMENT_LAB_REASONS, INVESTMENT_SCENARIOS, evaluatePortfolio, validateInvestmentAllocation } from '../shared/investment.ts';

export class ApiError extends Error { status:number;code:string; constructor(status:number,code:string,message:string){super(message);this.status=status;this.code=code;} }
const fail=(message:string,code='VALIDATION')=>{throw new ApiError(400,code,message);};
const MONEY_MAX=1_000_000;
function object(value:unknown):Record<string,unknown>{ if(!value||typeof value!=='object'||Array.isArray(value))return fail('Нужны данные в формате объекта.');return value as Record<string,unknown>; }
function text(value:unknown,label:string,min:number,max:number):string { if(typeof value!=='string')return fail(`${label}: введите текст.`); const v=value.trim();if(v.length<min||v.length>max||/[\u0000-\u0008\u000b\u000c\u000e-\u001f]/.test(v))return fail(`${label}: от ${min} до ${max} символов.`);return v; }
function money(value:unknown,label:string,min=0,max=MONEY_MAX):number { if(typeof value!=='number'||!Number.isSafeInteger(value)||value<min||value>max)return fail(`${label}: целое число от ${min} до ${max}.`); return value; }
const tokenHash=(token:string)=>createHash('sha256').update(token).digest('hex');
type SessionRow={id:string;profile:string;goal:string|null;first_answer_at:string|null};
type AnswerRow={content_id:string;kind:string;choice_id:string;reason_id:string|null;correct:number;created_at:string};
type ActionRow={id:string;session_id:string;type:'save'|'review';amount:number|null;note:string;due_date:string;completed_at:string|null;created_at:string};
const CATEGORY_TITLES={food:'Еда',transport:'Транспорт',housing:'Жильё',other:'Другое'} as const;
const CATEGORY_IDS=Object.keys(CATEGORY_TITLES) as (keyof typeof CATEGORY_TITLES)[];
function defaultCategories(essential=0):BudgetCategory[]{return CATEGORY_IDS.map(id=>({id,title:CATEGORY_TITLES[id],amount:id==='other'?essential:0}));}
function parseCategories(value:unknown):BudgetCategory[]{
 if(!Array.isArray(value)||value.length!==4)return fail('Заполните четыре категории расходов.');
 const seen=new Set<string>();const categories:BudgetCategory[]=value.map(raw=>{
  const c=object(raw);if(typeof c.id!=='string'||!CATEGORY_IDS.includes(c.id as keyof typeof CATEGORY_TITLES)||seen.has(c.id))return fail('Каждая категория расходов должна встречаться один раз.');
  seen.add(c.id);const id=c.id as keyof typeof CATEGORY_TITLES;
  return{id,title:CATEGORY_TITLES[id],amount:money(c.amount,'Расходы по категории')};
 }).sort((a,b)=>CATEGORY_IDS.indexOf(a.id)-CATEGORY_IDS.indexOf(b.id));
 if(categories.reduce((sum,c)=>sum+c.amount,0)>MONEY_MAX)return fail('Общие расходы не должны превышать 1 000 000 ₽.');
 return categories;
}
function validateIdempotencyKey(key:string|undefined){if(key!==undefined&&(key.length<1||key.length>80||!/^[a-zA-Z0-9_-]+$/.test(key)))return fail('Некорректный идентификатор действия.');}
function calculateBudget(income:number,reserve:number,monthly:number,categories:BudgetCategory[]):Budget{
 const afterEssentials=income-categories.reduce((sum,c)=>sum+c.amount,0)-reserve;
 return{income,reserve,monthly,categories,available:Math.max(0,afterEssentials),unallocated:afterEssentials-monthly};
}
function meaningfulNote(value:unknown):string{const note=text(value,'Что проверить',8,200);if((note.match(/[\p{L}\p{N}]+/gu)?.length??0)<2)return fail('Опишите действие хотя бы двумя словами.');return note;}

export function calculateGoal(g:GoalInput):Pick<Goal,'available'|'requiredMonthly'|'projected'|'feasible'|'suggestedMonths'>{
 const remaining=Math.max(0,g.target-g.saved);const cash=g.income-g.essential-g.reserve;
 return {available:Math.max(0,cash),requiredMonthly:Math.ceil(remaining/g.months),projected:g.saved+g.monthly*g.months,feasible:cash>=0&&g.monthly<=cash&&g.saved+g.monthly*g.months>=g.target,suggestedMonths:g.monthly>0?Math.ceil(remaining/g.monthly):null};
}

export class Store {
 db:DatabaseSync;clock:()=>number;
 constructor(path='data/teyl.sqlite',clock:()=>number=Date.now){
  this.clock=clock;if(path!==':memory:')mkdirSync(dirname(path),{recursive:true});
  this.db=new DatabaseSync(path);this.db.exec(`PRAGMA foreign_keys=ON;PRAGMA journal_mode=WAL;PRAGMA busy_timeout=3000;
   CREATE TABLE IF NOT EXISTS sessions(id TEXT PRIMARY KEY,token_hash TEXT NOT NULL UNIQUE,profile TEXT NOT NULL,goal TEXT,first_answer_at TEXT);
   CREATE TABLE IF NOT EXISTS answers(session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,kind TEXT NOT NULL,content_id TEXT NOT NULL,choice_id TEXT NOT NULL,reason_id TEXT,correct INTEGER NOT NULL,created_at TEXT NOT NULL,content_version TEXT NOT NULL,PRIMARY KEY(session_id,kind,content_id));
   CREATE TABLE IF NOT EXISTS checkins(id TEXT PRIMARY KEY,session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,type TEXT NOT NULL,amount INTEGER,note TEXT NOT NULL,created_at TEXT NOT NULL,idempotency_key TEXT,UNIQUE(session_id,idempotency_key));
   CREATE TABLE IF NOT EXISTS events(id INTEGER PRIMARY KEY AUTOINCREMENT,session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,name TEXT NOT NULL,content_id TEXT,created_at TEXT NOT NULL,content_version TEXT NOT NULL);
   CREATE TABLE IF NOT EXISTS budgets(session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,value TEXT NOT NULL);
   CREATE TABLE IF NOT EXISTS actions(id TEXT PRIMARY KEY,session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,type TEXT NOT NULL,amount INTEGER,note TEXT NOT NULL,due_date TEXT NOT NULL,completed_at TEXT,created_at TEXT NOT NULL,idempotency_key TEXT);
   CREATE TABLE IF NOT EXISTS expenses(id TEXT PRIMARY KEY,session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,amount INTEGER NOT NULL,category TEXT NOT NULL,occurred_on TEXT NOT NULL,note TEXT NOT NULL,voided_at TEXT,created_at TEXT NOT NULL,idempotency_key TEXT,UNIQUE(session_id,idempotency_key));
   CREATE INDEX IF NOT EXISTS expenses_session ON expenses(session_id,occurred_on);
   CREATE INDEX IF NOT EXISTS actions_session ON actions(session_id,due_date);
   CREATE INDEX IF NOT EXISTS events_session ON events(session_id);
   CREATE TABLE IF NOT EXISTS investment_answers(session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,lesson_id TEXT NOT NULL,first_attempt TEXT NOT NULL,latest_attempt TEXT NOT NULL,content_version TEXT NOT NULL,PRIMARY KEY(session_id,lesson_id));
   CREATE TABLE IF NOT EXISTS investment_labs(session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,value TEXT NOT NULL,content_version TEXT NOT NULL);
  `);
  this.transaction(()=>{
   if(!this.db.prepare('PRAGMA table_info(actions)').all().some(c=>c.name==='idempotency_key'))this.db.exec('ALTER TABLE actions ADD COLUMN idempotency_key TEXT');
   this.db.exec('CREATE UNIQUE INDEX IF NOT EXISTS actions_idempotency ON actions(session_id,idempotency_key)');
   for(const row of this.db.prepare('SELECT id,profile FROM sessions').all()){
    const profile=JSON.parse(String(row.profile)) as Partial<Profile>;
    if(!Object.hasOwn(profile,'character')||!Object.hasOwn(profile,'focus')||!Object.hasOwn(profile,'focusUpdatedAt')){
     if(!Object.hasOwn(profile,'character'))profile.character=null;
     if(!Object.hasOwn(profile,'focus'))profile.focus=null;
     if(!Object.hasOwn(profile,'focusUpdatedAt'))profile.focusUpdatedAt=null;
     this.db.prepare('UPDATE sessions SET profile=? WHERE id=?').run(JSON.stringify(profile),String(row.id));
    }
   }
  });
 }
 close(){this.db.close();}
 now(){return new Date(this.clock()).toISOString();}
 transaction<T>(fn:()=>T):T{this.db.exec('BEGIN IMMEDIATE');try{const result=fn();this.db.exec('COMMIT');return result;}catch(e){this.db.exec('ROLLBACK');throw e;}}
 event(id:string,name:string,contentId:string|null=null,version=CONTENT_VERSION){this.db.prepare('INSERT INTO events(session_id,name,content_id,created_at,content_version) VALUES(?,?,?,?,?)').run(id,name,contentId,this.now(),version);}
 session(id:string):SessionRow{const row=this.db.prepare('SELECT * FROM sessions WHERE id=?').get(id) as SessionRow|undefined;if(!row)throw new ApiError(401,'SESSION_EXPIRED','Сессия недоступна. Начните заново.');return row;}
 createSession(){const id=randomUUID(),token=randomBytes(32).toString('base64url');const profile:Profile={name:'',ageBand:'other',onboardingCompleted:false,reducedMotion:false,character:null,focus:null,focusUpdatedAt:null,createdAt:this.now()};this.db.prepare('INSERT INTO sessions(id,token_hash,profile) VALUES(?,?,?)').run(id,tokenHash(token),JSON.stringify(profile));this.event(id,'session_created');return{token,state:this.state(id)};}
 authenticate(token:string|undefined):string{if(!token||!/^[\w-]{43}$/.test(token))throw new ApiError(401,'UNAUTHORIZED','Сессия недоступна. Начните заново.');const row=this.db.prepare('SELECT id FROM sessions WHERE token_hash=?').get(tokenHash(token));if(!row)throw new ApiError(401,'UNAUTHORIZED','Сессия недоступна. Начните заново.');return String(row.id);}
 answers(id:string){return this.db.prepare('SELECT * FROM answers WHERE session_id=?').all(id) as unknown as AnswerRow[];}
 budget(id:string,row?:SessionRow):Budget{
  const stored=this.db.prepare('SELECT value FROM budgets WHERE session_id=?').get(id);
  if(stored)return JSON.parse(String(stored.value)) as Budget;
  const session=row??this.session(id);const goal:Goal|null=session.goal?JSON.parse(session.goal):null;
  return calculateBudget(goal?.income??0,goal?.reserve??0,goal?.monthly??0,defaultCategories(goal?.essential??0));
 }
 state(id:string):AppState{
  const row=this.session(id);const answered=this.answers(id);const completed=(kind:string,cid:string)=>answered.some(a=>a.kind===kind&&a.content_id===cid);
  const publicLessons=lessons.map((l,i)=>{const{correctChoice,explanations,...publicData}=l;return{...publicData,completed:completed('lesson',l.id),unlocked:i===0||(completed('lesson',lessons[i-1].id)&&(i!==3||completed('simulation','plan')))};});
  const publicSims=simulations.map((s,i)=>{const{correctChoice,correctReason,explanation,...publicData}=s;return{...publicData,completed:completed('simulation',s.id),unlocked:i===0?publicLessons.slice(0,3).every(l=>l.completed):publicLessons.every(l=>l.completed)&&completed('simulation','plan')};});
  const checkins=this.db.prepare('SELECT id,type,amount,note,created_at AS createdAt FROM checkins WHERE session_id=? ORDER BY created_at DESC,rowid DESC LIMIT 100').all(id) as unknown as Checkin[];
  const nextSim=publicSims.find(s=>s.unlocked&&!s.completed);
  const actions=this.db.prepare('SELECT id,type,amount,note,due_date AS dueDate,completed_at AS completedAt,created_at AS createdAt FROM actions WHERE session_id=? ORDER BY completed_at IS NOT NULL,due_date ASC,created_at DESC,rowid DESC LIMIT 200').all(id) as unknown as PlannedAction[];
  const expenses=this.db.prepare('SELECT id,amount,category,occurred_on AS occurredOn,note,voided_at AS voidedAt,created_at AS createdAt FROM expenses WHERE session_id=? ORDER BY occurred_on DESC,created_at DESC,rowid DESC').all(id) as unknown as Expense[];
  return {profile:JSON.parse(row.profile),goal:row.goal?JSON.parse(row.goal):null,budget:this.budget(id,row),expenses,actions,lessons:publicLessons,simulations:publicSims,progress:{completedLessons:publicLessons.filter(l=>l.completed).length,completedSimulations:publicSims.filter(s=>s.completed).length,totalLessons:6,totalSimulations:2,firstAnswerAt:row.first_answer_at,nextLessonId:nextSim?null:publicLessons.find(l=>l.unlocked&&!l.completed)?.id??null,nextSimulationId:nextSim?.id??null},checkins,assessment:this.assessment(row,answered),contentVersion:CONTENT_VERSION,investments:this.investments(id)};
 }
 investments(id:string):InvestmentsState{
  this.session(id);
  const rows=this.db.prepare('SELECT lesson_id,first_attempt,latest_attempt FROM investment_answers WHERE session_id=?').all(id);
  const completed=new Set(rows.map(row=>String(row.lesson_id)));
  const publicLessons=investmentLessons.map((lesson,index)=>{
   const {correctChoice,correctReason,explanation,...publicContent}=lesson;
   const row=rows.find(row=>row.lesson_id===lesson.id);
   return{...publicContent,completed:completed.has(lesson.id),unlocked:index===0||completed.has(investmentLessons[index-1].id),firstAttempt:row?JSON.parse(String(row.first_attempt)) as InvestmentAttempt:null,latestAttempt:row?JSON.parse(String(row.latest_attempt)) as InvestmentAttempt:null};
  });
  const stored=this.db.prepare('SELECT value FROM investment_labs WHERE session_id=?').get(id);
  const lab:InvestmentLabState=stored?JSON.parse(String(stored.value)):{allocation:{...DEFAULT_INVESTMENT_ALLOCATION},lastScenarioId:null,lastResult:null,lastReasonId:null,lastCorrect:null,reviewedScenarioIds:[],updatedAt:null};
  return{lessons:publicLessons,progress:{completedLessons:publicLessons.filter(lesson=>lesson.completed).length,totalLessons:investmentLessons.length,nextLessonId:publicLessons.find(lesson=>lesson.unlocked&&!lesson.completed)?.id??null},lab,contentVersion:INVESTMENT_CONTENT_VERSION};
 }
 answerInvestmentLesson(id:string,lessonId:string,choice:unknown,reason:unknown):InvestmentAnswerResult{
  const lesson=investmentLessons.find(lesson=>lesson.id===lessonId);
  if(!lesson)throw new ApiError(404,'NOT_FOUND','Учебная ситуация не найдена.');
  if(typeof choice!=='string'||!lesson.choices.some(item=>item.id===choice)||typeof reason!=='string'||!lesson.reasons.some(item=>item.id===reason))return fail('Выберите ответ и его объяснение.');
  const correct=choice===lesson.correctChoice&&reason===lesson.correctReason;
  let alreadyCompleted=false;
  this.transaction(()=>{
   const item=this.investments(id).lessons.find(item=>item.id===lessonId)!;
   if(!item.unlocked&&!item.completed)throw new ApiError(409,'LOCKED','Сначала разберите предыдущую ситуацию в разделе инвестиций.');
   alreadyCompleted=item.completed;
   // A network retry of the same submission does not rewrite the latest timestamp.
   if(item.latestAttempt?.choiceId===choice&&item.latestAttempt.reasonId===reason)return;
   const attempt:InvestmentAttempt={choiceId:choice,reasonId:reason,correct,createdAt:this.now()};
   this.db.prepare('INSERT INTO investment_answers(session_id,lesson_id,first_attempt,latest_attempt,content_version) VALUES(?,?,?,?,?) ON CONFLICT(session_id,lesson_id) DO UPDATE SET latest_attempt=excluded.latest_attempt').run(id,lessonId,JSON.stringify(attempt),JSON.stringify(attempt),INVESTMENT_CONTENT_VERSION);
   if(!alreadyCompleted)this.event(id,'investment_lesson_answered',lessonId,INVESTMENT_CONTENT_VERSION);
  });
  return{correct,title:correct?'Ты учёл важное':'Ситуация разобрана',explanation:lesson.explanation,alreadyCompleted,state:this.state(id)};
 }
 saveInvestmentLab(id:string,input:unknown):InvestmentLabResult{
  const data=object(input);
  if(Object.keys(data).some(key=>!['allocation','scenarioId','reasonId'].includes(key)))return fail('Отправьте только распределение, сценарий и объяснение.');
  let allocation;try{allocation=validateInvestmentAllocation(data.allocation);}catch(error){return fail(error instanceof Error?error.message:'Проверьте распределение долей.');}
  if(typeof data.scenarioId!=='string'||!INVESTMENT_SCENARIOS.some(item=>item.id===data.scenarioId))return fail('Выберите один из трёх учебных сценариев.');
  if(typeof data.reasonId!=='string'||!INVESTMENT_LAB_REASONS.some(item=>item.id===data.reasonId))return fail('Выберите объяснение результата.');
  const scenarioId=data.scenarioId as InvestmentScenarioId;const reasonId=data.reasonId;
  const result=evaluatePortfolio(allocation,scenarioId);const correct=reasonId==='risk';
  this.transaction(()=>{
   const previous=this.investments(id).lab;
   if(previous.lastScenarioId===scenarioId&&previous.lastReasonId===reasonId&&JSON.stringify(previous.allocation)===JSON.stringify(allocation))return;
   const reviewed=previous.reviewedScenarioIds.includes(scenarioId);
   const lab:InvestmentLabState={allocation,lastScenarioId:scenarioId,lastResult:result,lastReasonId:reasonId,lastCorrect:correct,reviewedScenarioIds:reviewed?previous.reviewedScenarioIds:[...previous.reviewedScenarioIds,scenarioId],updatedAt:this.now()};
   this.db.prepare('INSERT INTO investment_labs(session_id,value,content_version) VALUES(?,?,?) ON CONFLICT(session_id) DO UPDATE SET value=excluded.value,content_version=excluded.content_version').run(id,JSON.stringify(lab),INVESTMENT_CONTENT_VERSION);
   if(!reviewed)this.event(id,'investment_scenario_reviewed',scenarioId,INVESTMENT_CONTENT_VERSION);
  });
  const format=(value:number)=>value.toLocaleString('ru-RU',{maximumFractionDigits:2});
  const explanation=`В этом вымышленном сценарии итог — ${format(result.ending)} виртуальных ₽. Изменение цены: ${format(result.priceChange)} ₽, выплаты: ${format(result.cashIncome)} ₽, комиссии: ${format(result.fees)} ₽. Результат относительно 10 000 ₽: ${format(result.profit)} ₽. Купон и несколько активов не исключают убыток. Другая ситуация даст другой результат; это не прогноз. Для денег без вложений здесь нет процента и изменения цены; инфляция в расчёт не включена.`;
  return{correct,title:correct?'Вывод учитывает риск':'Посмотри на весь результат',explanation,result,state:this.state(id)};
 }
 assessment(row:SessionRow,answers:AnswerRow[]):Assessment{
  const a=answers.find(x=>x.kind==='assessment');const first=row.first_answer_at?Date.parse(row.first_answer_at):null;const open=first===null?null:first+144*3600000,close=first===null?null:first+216*3600000;
  const status=a?'completed':first===null?'not_started':this.clock()<open!?'waiting':this.clock()>=close!?'expired':'available';
  const base:Assessment={status,availableAt:open===null?null:new Date(open).toISOString(),closesAt:close===null?null:new Date(close).toISOString(),correct:a?!!a.correct:null};
  if(status==='available'){const{correctChoice,correctReason,explanation,...publicData}=assessmentContent;return{...base,...publicData};}return base;
 }
 updateProfile(id:string,input:unknown):AppState{
  const data=object(input);const profile:Profile=JSON.parse(this.session(id).profile);const allowed=['name','ageBand','onboardingCompleted','reducedMotion','character','focus'];
  if(Object.keys(data).some(k=>!allowed.includes(k)))fail('Есть неизвестное поле профиля.');
  if('name'in data)profile.name=text(data.name,'Имя',1,30);
  if('ageBand'in data){if(typeof data.ageBand!=='string'||!['14-17','18-20','21-22','other'].includes(data.ageBand))fail('Выберите возрастную группу.');profile.ageBand=data.ageBand as Profile['ageBand'];}
  if('character'in data){if(typeof data.character!=='string'||!['lumi','spark','nova'].includes(data.character))fail('Выберите одного из трёх персонажей.');profile.character=data.character as Profile['character'];}
  if('focus'in data){
   if(data.focus!==null&&(typeof data.focus!=='string'||!['curious','goal','gentle'].includes(data.focus)))fail('Выберите интерес, цель или спокойный темп.');
   profile.focus=data.focus as Profile['focus'];profile.focusUpdatedAt=profile.focus===null?null:this.now();
  }
  for(const key of ['onboardingCompleted','reducedMotion'] as const){if(key in data){if(typeof data[key]!=='boolean')fail('Настройка должна быть включена или выключена.');profile[key]=data[key] as boolean;}}
  this.db.prepare('UPDATE sessions SET profile=? WHERE id=?').run(JSON.stringify(profile),id);return this.state(id);
 }
 saveGoal(id:string,input:unknown):AppState{
  const data=object(input);const row=this.session(id);const title=text(data.title,'Название цели',1,60);const target=money(data.target,'Сумма цели',1);const saved=money(data.saved,'Уже накоплено');const months=money(data.months,'Срок в месяцах',1,120);const monthly=money(data.monthly,'Взнос');const income=money(data.income,'Поступления');const essential=money(data.essential,'Обязательные расходы');const reserve=money(data.reserve,'Запас');
  const suppliedCategories='categories'in data?parseCategories(data.categories):null;
  if(suppliedCategories&&suppliedCategories.reduce((sum,c)=>sum+c.amount,0)!==essential)return fail('Сумма категорий должна совпадать с обязательными расходами.');
  const values:GoalInput={title,target,saved,months,monthly,income,essential,reserve};const existing:Goal|null=row.goal?JSON.parse(row.goal):null;
  const goal:Goal={...values,...calculateGoal(values),id:existing?.id??randomUUID(),updatedAt:this.now()};
  this.transaction(()=>{
   const previous=this.budget(id);const categories=suppliedCategories??(previous.categories.reduce((sum,c)=>sum+c.amount,0)===essential?previous.categories:defaultCategories(essential));
   this.db.prepare('UPDATE sessions SET goal=? WHERE id=?').run(JSON.stringify(goal),id);
   this.db.prepare('INSERT INTO budgets(session_id,value) VALUES(?,?) ON CONFLICT(session_id) DO UPDATE SET value=excluded.value').run(id,JSON.stringify(calculateBudget(income,reserve,monthly,categories)));
   this.event(id,existing?'goal_revised':'goal_created');
  });return this.state(id);
 }
 saveBudget(id:string,input:unknown):AppState{
  const data=object(input);const income=money(data.income,'Поступления');const reserve=money(data.reserve,'Запас');const monthly=money(data.monthly,'Взнос');
  const categories=parseCategories(data.categories);const essential=categories.reduce((sum,c)=>sum+c.amount,0);
  const budget=calculateBudget(income,reserve,monthly,categories);
  this.transaction(()=>{
   const row=this.session(id);this.db.prepare('INSERT INTO budgets(session_id,value) VALUES(?,?) ON CONFLICT(session_id) DO UPDATE SET value=excluded.value').run(id,JSON.stringify(budget));
   if(row.goal){const goal:Goal=JSON.parse(row.goal);Object.assign(goal,{income,essential,reserve,monthly,updatedAt:this.now()});Object.assign(goal,calculateGoal(goal));this.db.prepare('UPDATE sessions SET goal=? WHERE id=?').run(JSON.stringify(goal),id);}
   this.event(id,'budget_updated');
  });return this.state(id);
 }
 recordAnswer(id:string,kind:string,cid:string,choice:string,reason:string|null,correct:boolean):boolean{
  const old=this.db.prepare('SELECT 1 FROM answers WHERE session_id=? AND kind=? AND content_id=?').get(id,kind,cid);if(old)return true;
  this.transaction(()=>{this.db.prepare('INSERT INTO answers(session_id,kind,content_id,choice_id,reason_id,correct,created_at,content_version) VALUES(?,?,?,?,?,?,?,?)').run(id,kind,cid,choice,reason,correct?1:0,this.now(),CONTENT_VERSION);
   if(kind==='lesson'||kind==='simulation')this.db.prepare('UPDATE sessions SET first_answer_at=COALESCE(first_answer_at,?) WHERE id=?').run(this.now(),id);
   this.event(id,`${kind}_answered`,cid);
  });return false;
 }
 answerLesson(id:string,cid:string,choice:unknown):AnswerResult{
  const content=lessons.find(l=>l.id===cid);if(!content)throw new ApiError(404,'NOT_FOUND','Задание не найдено.');if(typeof choice!=='string'||!content.choices.some(c=>c.id===choice))return fail('Выберите один из предложенных ответов.');
  const item=this.state(id).lessons.find(l=>l.id===cid)!;if(!item.unlocked&&!item.completed)throw new ApiError(409,'LOCKED','Сначала завершите предыдущий шаг.');const correct=choice===content.correctChoice;
  const alreadyCompleted=this.recordAnswer(id,'lesson',cid,choice,null,correct);return{correct,title:correct?'Получилось!':'Разберём вместе',explanation:content.explanations[choice],alreadyCompleted,state:this.state(id)};
 }
 answerSimulation(id:string,cid:string,choice:unknown,reason:unknown):AnswerResult{
  const c=simulations.find(s=>s.id===cid);if(!c)throw new ApiError(404,'NOT_FOUND','Симуляция не найдена.');if(typeof choice!=='string'||!c.choices.some(x=>x.id===choice)||typeof reason!=='string'||!c.reasons.some(x=>x.id===reason))return fail('Выберите действие и его объяснение.');
  const item=this.state(id).simulations.find(s=>s.id===cid)!;if(!item.unlocked&&!item.completed)throw new ApiError(409,'LOCKED','Сначала завершите предыдущий шаг.');const correct=choice===c.correctChoice&&reason===c.correctReason;const alreadyCompleted=this.recordAnswer(id,'simulation',cid,choice,reason,correct);return{correct,title:correct?'План стал понятнее':'Посмотрим на условия',explanation:c.explanation,alreadyCompleted,state:this.state(id)};
 }
 decideSimulation(id:string,cid:string,input:unknown):AnswerResult{
  const content=simulations.find(s=>s.id===cid);if(!content)throw new ApiError(404,'NOT_FOUND','Симуляция не найдена.');
  const data=object(input);const monthly=money(data.monthly,'Взнос',0,5000);const months=money(data.months,'Срок',1,12);
  if(typeof data.reasonId!=='string'||!content.reasons.some(r=>r.id===data.reasonId))return fail('Выберите объяснение решения.');
  const item=this.state(id).simulations.find(s=>s.id===cid)!;if(!item.unlocked&&!item.completed)throw new ApiError(409,'LOCKED','Сначала завершите предыдущий шаг.');
  let outcome:SimulationOutcome;let explanation:string;
  if(cid==='plan'){
   const projected=2000+monthly*months;const feasible=monthly<=2500&&projected>=12000;
   outcome={monthly,months,available:2500,projected,gap:Math.max(0,12000-projected),feasible,remaining:10000};
   const financial=monthly>2500?`Взнос ${monthly.toLocaleString('ru-RU')} ₽ превышает доступные 2 500 ₽. Сначала уменьши взнос: обязательные расходы и запас остаются важными.`:projected<12000?`При взносе ${monthly.toLocaleString('ru-RU')} ₽ за ${months} мес. получится ${projected.toLocaleString('ru-RU')} ₽. До цели не хватит ${(12000-projected).toLocaleString('ru-RU')} ₽. Можно увеличить срок или посильный взнос.`:`За ${months} мес. получится ${projected.toLocaleString('ru-RU')} ₽. Взнос помещается в доступные 2 500 ₽, цель покрыта.`;
   explanation=`${financial} Расчёт: уже накопленное + взнос × число месяцев. Он верен при неизменных поступлениях и расходах.${data.reasonId!==content.correctReason?' Причина выбора тоже важна: сравни доступный остаток и сумму, которую ещё нужно накопить.':''}`;
  }else{
   outcome={monthly,months,available:0,projected:null,gap:null,feasible:monthly===0,remaining:7500};
   explanation=monthly===0?'Пауза в пополнении сейчас посильна. После обязательных расходов остаётся 500 ₽ — даже запас 1 000 ₽ не покрыт. До цели осталось 7 500 ₽. Срок пересчитаем, когда станут понятны будущие поступления.':`Взнос ${monthly.toLocaleString('ru-RU')} ₽ сейчас непосилен: после обязательных расходов остаётся 500 ₽, а для запаса нужно 1 000 ₽. Можно поставить взнос на паузу. До цели осталось 7 500 ₽; будущий срок пока неизвестен.`;
   if(data.reasonId!==content.correctReason)explanation+=' Основание решения — нехватка свободного остатка после обязательных расходов и запаса.';
  }
  const correct=outcome.feasible&&data.reasonId===content.correctReason;
  const alreadyCompleted=this.recordAnswer(id,'simulation',cid,JSON.stringify({mode:'decision',monthly,months}),data.reasonId,correct);
  return{correct,title:correct?'Решение учитывает жизнь':'Проверим последствия',explanation,alreadyCompleted,outcome,state:this.state(id)};
 }
 answerAssessment(id:string,choice:unknown,reason:unknown):AnswerResult{
  const c=assessmentContent;if(typeof choice!=='string'||!c.choices.some(x=>x.id===choice)||typeof reason!=='string'||!c.reasons.some(x=>x.id===reason))return fail('Выберите действие и объяснение.');
  const state=this.state(id);if(state.assessment.status==='completed')return{correct:!!state.assessment.correct,title:'Проверка уже сохранена',explanation:c.explanation,alreadyCompleted:true,state};
  if(state.assessment.status!=='available')throw new ApiError(409,'ASSESSMENT_UNAVAILABLE','Проверка сейчас недоступна. Посмотрите дату на экране прогресса.');
  const correct=choice===c.correctChoice&&reason===c.correctReason;this.recordAnswer(id,'assessment','d7',choice,reason,correct);return{correct,title:correct?'Ты справился с новой задачей':'Спасибо, ответ сохранён',explanation:c.explanation,alreadyCompleted:false,state:this.state(id)};
 }
 checkin(id:string,input:unknown,key?:string):AppState{
  const data=object(input);if(typeof data.type!=='string'||!['saved','reviewed'].includes(data.type))return fail('Выберите тип отметки.');
  const note='note'in data?text(data.note,'Заметка',0,200):'';const amount=data.type==='saved'?money(data.amount,'Сумма пополнения',1):null;
  if(key!==undefined&&(key.length<1||key.length>80||!/^[a-zA-Z0-9_-]+$/.test(key)))return fail('Некорректный идентификатор отметки.');
  this.transaction(()=>{
   const row=this.session(id);if(!row.goal)return fail('Сначала сохраните цель.');
   const existing=key?this.db.prepare('SELECT type,amount,note FROM checkins WHERE session_id=? AND idempotency_key=?').get(id,key):undefined;
   if(existing){if(existing.type!==data.type||existing.amount!==amount||existing.note!==note)throw new ApiError(409,'IDEMPOTENCY_CONFLICT','Эта отметка уже сохранена с другими данными. Создайте новую отметку.');return;}
   const goal:Goal=JSON.parse(row.goal);if(amount!==null&&goal.saved+amount>MONEY_MAX)return fail('Общая сумма накоплений не должна превышать 1 000 000 ₽.');
   this.db.prepare('INSERT INTO checkins(id,session_id,type,amount,note,created_at,idempotency_key) VALUES(?,?,?,?,?,?,?)').run(randomUUID(),id,String(data.type),amount,note,this.now(),key??null);
   if(amount!==null){goal.saved+=amount;Object.assign(goal,calculateGoal(goal));goal.updatedAt=this.now();this.db.prepare('UPDATE sessions SET goal=? WHERE id=?').run(JSON.stringify(goal),id);}
   this.event(id,data.type==='saved'?'saving_self_reported':'plan_reviewed');
  });return this.state(id);
 }
 planAction(id:string,input:unknown,key?:string):AppState{
  const data=object(input);if(data.type!=='save'&&data.type!=='review')return fail('Выберите пополнение или пересмотр плана.');
  const amount=data.type==='save'?money(data.amount,'Сумма пополнения',1):null;
  const note=data.type==='review'?meaningfulNote(data.note):text(data.note??'','Заметка',0,200);
  validateIdempotencyKey(key);
  const dueDate=text(data.dueDate,'Дата',10,10);const parsed=Date.parse(`${dueDate}T00:00:00.000Z`);
  if(!/^\d{4}-\d{2}-\d{2}$/.test(dueDate)||!Number.isFinite(parsed)||new Date(parsed).toISOString().slice(0,10)!==dueDate)return fail('Введите существующую дату в формате ГГГГ-ММ-ДД.');
  const today=Date.parse(`${this.now().slice(0,10)}T00:00:00.000Z`);
  this.transaction(()=>{
   const row=this.session(id);if(!row.goal)return fail('Сначала сохраните цель.');
   const existing=key?this.db.prepare('SELECT type,amount,note,due_date FROM actions WHERE session_id=? AND idempotency_key=?').get(id,key):undefined;
   if(existing){if(existing.type!==data.type||existing.amount!==amount||existing.note!==note||existing.due_date!==dueDate)throw new ApiError(409,'IDEMPOTENCY_CONFLICT','Это действие уже сохранено с другими данными. Создайте новое действие.');return;}
   if(parsed<today||parsed>today+30*86400000)return fail('Выберите дату от сегодня до 30 дней вперёд.');
   const active=Number(this.db.prepare('SELECT COUNT(*) AS n FROM actions WHERE session_id=? AND completed_at IS NULL').get(id)!.n);
   if(active>=20)throw new ApiError(409,'ACTION_LIMIT','Уже запланировано 20 действий. Завершите или отмените одно из них.');
   const actionId=randomUUID();this.db.prepare('INSERT INTO actions(id,session_id,type,amount,note,due_date,created_at,idempotency_key) VALUES(?,?,?,?,?,?,?,?)').run(actionId,id,data.type as string,amount,note,dueDate,this.now(),key??null);this.event(id,'action_planned',actionId);
  });return this.state(id);
 }
 completeAction(id:string,actionId:string,input:unknown):AppState{
  const data=object(input);if(data.completed!==true)return fail('Действие можно только отметить выполненным.');
  this.transaction(()=>{
   const row=this.session(id);const action=this.db.prepare('SELECT * FROM actions WHERE id=? AND session_id=?').get(actionId,id) as ActionRow|undefined;
   if(!action)throw new ApiError(404,'NOT_FOUND','Действие не найдено.');if(action.completed_at)return;
   if(!row.goal)return fail('Сначала сохраните цель.');const goal:Goal=JSON.parse(row.goal);
   const note=action.type==='review'?meaningfulNote(action.note):action.note;
   if(action.amount!==null&&goal.saved+action.amount>MONEY_MAX)return fail('Общая сумма накоплений не должна превышать 1 000 000 ₽.');
   const now=this.now();this.db.prepare('INSERT INTO checkins(id,session_id,type,amount,note,created_at) VALUES(?,?,?,?,?,?)').run(randomUUID(),id,action.type==='save'?'saved':'reviewed',action.amount,note,now);
   if(action.amount!==null){goal.saved+=action.amount;Object.assign(goal,calculateGoal(goal));goal.updatedAt=now;this.db.prepare('UPDATE sessions SET goal=? WHERE id=?').run(JSON.stringify(goal),id);}
   this.db.prepare('UPDATE actions SET completed_at=? WHERE id=? AND session_id=?').run(now,actionId,id);
   this.event(id,action.type==='save'?'saving_self_reported':'plan_reviewed');this.event(id,'action_completed',actionId);
  });return this.state(id);
 }
 deleteAction(id:string,actionId:string):AppState{
  this.transaction(()=>{
   this.session(id);const action=this.db.prepare('SELECT * FROM actions WHERE id=? AND session_id=?').get(actionId,id) as ActionRow|undefined;
   if(!action)return;if(action.completed_at)throw new ApiError(409,'ACTION_COMPLETED','Выполненное действие уже в истории. Можно скорректировать накопленную сумму в цели.');
   this.db.prepare('DELETE FROM actions WHERE id=? AND session_id=?').run(actionId,id);this.event(id,'action_cancelled',actionId);
  });return this.state(id);
 }
 deleteAccount(id:string){this.session(id);this.db.prepare('DELETE FROM sessions WHERE id=?').run(id);}
 addExpense(id:string,input:unknown,key?:string):AppState{
  const data=object(input);const amount=money(data.amount,'Сумма расхода',1);
  if(typeof data.category!=='string'||!CATEGORY_IDS.includes(data.category as keyof typeof CATEGORY_TITLES))return fail('Выберите одну из четырёх категорий расходов.');
  const category=data.category;const note=text(data.note??'','Заметка',0,200);validateIdempotencyKey(key);
  const occurredOn=text(data.occurredOn,'Дата расхода',10,10);const parsed=Date.parse(`${occurredOn}T00:00:00.000Z`);
  if(!/^\d{4}-\d{2}-\d{2}$/.test(occurredOn)||!Number.isFinite(parsed)||new Date(parsed).toISOString().slice(0,10)!==occurredOn||occurredOn<'1900-01-01')return fail('Введите существующую дату не раньше 1900 года: ГГГГ-ММ-ДД.');
  const offset=data.timezoneOffsetMinutes??0;if(typeof offset!=='number'||!Number.isInteger(offset)||offset< -840||offset>720)return fail('Не удалось определить часовой пояс. Обновите приложение и повторите.');
  const localToday=new Date(this.clock()-offset*60000).toISOString().slice(0,10);
  this.transaction(()=>{
   this.session(id);const existing=key?this.db.prepare('SELECT amount,category,occurred_on,note FROM expenses WHERE session_id=? AND idempotency_key=?').get(id,key):undefined;
   if(existing){if(existing.amount!==amount||existing.category!==category||existing.occurred_on!==occurredOn||existing.note!==note)throw new ApiError(409,'IDEMPOTENCY_CONFLICT','Этот расход уже сохранён с другими данными. Создайте новую запись.');return;}
   if(occurredOn>localToday)return fail('Расход можно записать за сегодня или прошедший день.');
   const count=Number(this.db.prepare('SELECT COUNT(*) AS n FROM expenses WHERE session_id=?').get(id)!.n);
   if(count>=500)throw new ApiError(409,'EXPENSE_LIMIT','В журнале уже 500 записей — это лимит текущей версии. Отмена записи не освобождает место.');
   const expenseId=randomUUID();this.db.prepare('INSERT INTO expenses(id,session_id,amount,category,occurred_on,note,created_at,idempotency_key) VALUES(?,?,?,?,?,?,?,?)').run(expenseId,id,amount,category,occurredOn,note,this.now(),key??null);this.event(id,'expense_recorded',expenseId);
  });return this.state(id);
 }
 setExpenseVoided(id:string,expenseId:string,input:unknown):AppState{
  const data=object(input);if(typeof data.voided!=='boolean')return fail('Укажите, отменить или восстановить расход.');
  this.transaction(()=>{
   this.session(id);const existing=this.db.prepare('SELECT voided_at FROM expenses WHERE id=? AND session_id=?').get(expenseId,id);
   if(!existing)throw new ApiError(404,'NOT_FOUND','Запись расхода не найдена.');
   if(Boolean(existing.voided_at)===data.voided)return;
   this.db.prepare('UPDATE expenses SET voided_at=? WHERE id=? AND session_id=?').run(data.voided?this.now():null,expenseId,id);this.event(id,data.voided?'expense_voided':'expense_restored',expenseId);
  });return this.state(id);
 }
}
