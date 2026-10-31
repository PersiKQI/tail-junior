import { resolve } from 'node:path';
import { Store } from './store.ts';
import { createServer } from './http.ts';

const port=Number(process.env.PORT??3001);
if(!Number.isInteger(port)||port<1||port>65535)throw new Error('PORT must be an integer between 1 and 65535.');
const host=process.env.HOST??'127.0.0.1';
const store=new Store(process.env.DATABASE_PATH??resolve('data/teyl.sqlite'));
const server=createServer({store,allowedOrigins:(process.env.CORS_ORIGINS??'').split(',').map((x:string)=>x.trim()).filter(Boolean),production:process.env.NODE_ENV==='production'});
server.listen(port,host,()=>console.log(`ТЭЙЛ API: http://${host}:${port}/api/health`));
function shutdown(){server.close(()=>{store.close();process.exit(0);});setTimeout(()=>process.exit(1),5000).unref();}
process.once('SIGINT',shutdown);process.once('SIGTERM',shutdown);
