import { createServer as nodeServer } from 'node:http';
import type { IncomingMessage, ServerResponse } from 'node:http';
import { Store, ApiError } from './store.ts';

const BODY_LIMIT=16_384;
function reply(res:ServerResponse,status:number,body:unknown){res.statusCode=status;if(status===204){res.end();return;}res.setHeader('Content-Type','application/json; charset=utf-8');res.end(JSON.stringify(body));}
async function readBody(req:IncomingMessage):Promise<Record<string,unknown>>{
 const type=req.headers['content-type'];if(!type?.startsWith('application/json'))throw new ApiError(415,'CONTENT_TYPE','Отправьте данные в формате JSON.');
 const length=Number(req.headers['content-length']??0);if(length>BODY_LIMIT)throw new ApiError(413,'BODY_TOO_LARGE','Слишком много данных. Сократите текст.');
 let size=0;const chunks:Buffer[]=[];for await(const chunk of req){const buf=Buffer.isBuffer(chunk)?chunk:Buffer.from(chunk);size+=buf.length;if(size>BODY_LIMIT)throw new ApiError(413,'BODY_TOO_LARGE','Слишком много данных. Сократите текст.');chunks.push(buf);}
 let data:unknown;try{data=JSON.parse(Buffer.concat(chunks).toString('utf8')||'{}');}catch{throw new ApiError(400,'BAD_JSON','Не удалось прочитать данные. Повторите действие.');}
 if(!data||typeof data!=='object'||Array.isArray(data))throw new ApiError(400,'BAD_JSON','Нужны данные в формате объекта.');return data as Record<string,unknown>;
}
function defaultOrigin(origin:string){try{const u=new URL(origin);return ['http:','https:'].includes(u.protocol)&&['localhost','127.0.0.1','[::1]'].includes(u.hostname);}catch{return false;}}
export function createServer(options:{store:Store;allowedOrigins?:string[];production?:boolean}){
 const {store,allowedOrigins=[],production=false}=options;
 const sessionsByIp=new Map<string,{count:number;reset:number}>();
 return nodeServer(async(req,res)=>{
  res.setHeader('Cache-Control','no-store');res.setHeader('X-Content-Type-Options','nosniff');res.setHeader('Referrer-Policy','no-referrer');
  try{
   const origin=req.headers.origin;if(origin){if(!allowedOrigins.includes(origin)&&!( !production&&defaultOrigin(origin)))throw new ApiError(403,'ORIGIN','Этот адрес приложения не разрешён сервером.');res.setHeader('Access-Control-Allow-Origin',origin);res.setHeader('Vary','Origin');res.setHeader('Access-Control-Allow-Methods','GET, POST, PUT, PATCH, DELETE, OPTIONS');res.setHeader('Access-Control-Allow-Headers','Content-Type, Authorization, Idempotency-Key');}
   if(req.method==='OPTIONS'){reply(res,204,null);return;}
   const url=new URL(req.url??'/','http://localhost');const path=url.pathname;
   if(req.method==='GET'&&path==='/api/health'){reply(res,200,{status:'ok',service:'teyl-api'});return;}
   if(req.method==='POST'&&path==='/api/session'){
    const ip=req.socket.remoteAddress??'unknown';
    await readBody(req);
    const now=Date.now();const entry=sessionsByIp.get(ip);if(entry&&entry.reset>now&&entry.count>=30)throw new ApiError(429,'RATE_LIMIT','Слишком много новых сессий. Попробуйте через 10 минут.');
    if(!entry||entry.reset<=now)sessionsByIp.set(ip,{count:1,reset:now+600000});else entry.count++;
    if(sessionsByIp.size>1000)for(const[k,v]of sessionsByIp){if(v.reset<=now)sessionsByIp.delete(k);}
    reply(res,201,store.createSession());return;
   }
   if(!path.startsWith('/api/'))throw new ApiError(404,'NOT_FOUND','Адрес не найден.');
   const bearer=req.headers.authorization?.match(/^Bearer ([\w-]+)$/)?.[1];const id=store.authenticate(bearer);
   if(req.method==='GET'&&path==='/api/bootstrap'){reply(res,200,store.state(id));return;}
   if(req.method==='PATCH'&&path==='/api/profile'){reply(res,200,store.updateProfile(id,await readBody(req)));return;}
   if(req.method==='PUT'&&path==='/api/goal'){reply(res,200,store.saveGoal(id,await readBody(req)));return;}
   if(req.method==='PUT'&&path==='/api/budget'){reply(res,200,store.saveBudget(id,await readBody(req)));return;}
   if(req.method==='POST'&&path==='/api/expenses'){const key=req.headers['idempotency-key'];if(Array.isArray(key))throw new ApiError(400,'VALIDATION','Нужен один идентификатор расхода.');reply(res,200,store.addExpense(id,await readBody(req),key));return;}
   const expense=path.match(/^\/api\/expenses\/([a-f0-9-]{36})$/);if(req.method==='PATCH'&&expense){reply(res,200,store.setExpenseVoided(id,expense[1],await readBody(req)));return;}
   if(req.method==='POST'&&path==='/api/actions'){const key=req.headers['idempotency-key'];if(Array.isArray(key))throw new ApiError(400,'VALIDATION','Нужен один идентификатор действия.');reply(res,200,store.planAction(id,await readBody(req),key));return;}
   const action=path.match(/^\/api\/actions\/([a-f0-9-]{36})$/);
   if(req.method==='PATCH'&&action){reply(res,200,store.completeAction(id,action[1],await readBody(req)));return;}
   if(req.method==='DELETE'&&action){reply(res,200,store.deleteAction(id,action[1]));return;}
   if(req.method==='POST'&&path==='/api/checkins'){const key=req.headers['idempotency-key'];if(Array.isArray(key))throw new ApiError(400,'VALIDATION','Нужен один идентификатор отметки.');reply(res,200,store.checkin(id,await readBody(req),key));return;}
   if(req.method==='POST'&&path==='/api/assessment/answer'){const data=await readBody(req);reply(res,200,store.answerAssessment(id,data.choiceId,data.reasonId));return;}
   const investmentLesson=path.match(/^\/api\/investments\/lessons\/([a-z-]+)\/answer$/);if(req.method==='POST'&&investmentLesson){const data=await readBody(req);reply(res,200,store.answerInvestmentLesson(id,investmentLesson[1],data.choiceId,data.reasonId));return;}
   if(req.method==='POST'&&path==='/api/investments/lab'){reply(res,200,store.saveInvestmentLab(id,await readBody(req)));return;}
   const lesson=path.match(/^\/api\/lessons\/([a-z-]+)\/answer$/);if(req.method==='POST'&&lesson){const data=await readBody(req);reply(res,200,store.answerLesson(id,lesson[1],data.choiceId));return;}
   const sim=path.match(/^\/api\/simulations\/([a-z-]+)\/answer$/);if(req.method==='POST'&&sim){const data=await readBody(req);reply(res,200,store.answerSimulation(id,sim[1],data.choiceId,data.reasonId));return;}
   const decision=path.match(/^\/api\/simulations\/([a-z-]+)\/decision$/);if(req.method==='POST'&&decision){reply(res,200,store.decideSimulation(id,decision[1],await readBody(req)));return;}
   if(req.method==='DELETE'&&path==='/api/account'){store.deleteAccount(id);reply(res,204,null);return;}
   throw new ApiError(404,'NOT_FOUND','Адрес не найден.');
  }catch(error){if(error instanceof ApiError)reply(res,error.status,{error:error.message,code:error.code});else{console.error('[teyl-api] Request failed:',error instanceof Error?error.name:'unknown');reply(res,500,{error:'Не удалось сохранить данные. Повторите попытку.',code:'SERVER_ERROR'});}}
 });
}
