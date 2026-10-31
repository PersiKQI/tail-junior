import { spawn } from 'node:child_process';
const children=[];
for(const args of [['server/index.ts'],['node_modules/expo/bin/cli','start','--web','--port','8081']]){
 const child=spawn(process.execPath,args,{stdio:'inherit',env:process.env});children.push(child);
 child.on('exit',(code)=>{if(code)shutdown(code);});
}
let closing=false;
function shutdown(code=0){if(closing)return;closing=true;for(const child of children)child.kill('SIGTERM');setTimeout(()=>process.exit(code),100);}
process.on('SIGINT',()=>shutdown());process.on('SIGTERM',()=>shutdown());
