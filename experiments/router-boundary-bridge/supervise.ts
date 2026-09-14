import { spawn } from 'node:child_process';
import { readFile,rm,writeFile } from 'node:fs/promises';
import assert from 'node:assert/strict';
const scenario=process.argv[2];let child:ReturnType<typeof spawn>;let stop=false;
async function run(recover=false){
 if(stop)throw new Error('supervisor_stopped_before_spawn');
 child=spawn(process.execPath,['--experimental-loader','/probe/loader.mjs','/bridge/gateway.ts',scenario,...(recover?['recover']:[])],{env:process.env,stdio:['ignore','pipe','pipe']});
 child.stdout!.pipe(process.stdout);let stderr='';child.stderr!.on('data',b=>{stderr+=b;if(stderr.length>65536)stderr=stderr.slice(-65536);});
 const timer=setTimeout(()=>child.kill('SIGKILL'),15000);
 const result=await new Promise<{code:number|null;signal:string|null}>(resolve=>child.once('exit',(code,signal)=>resolve({code,signal})));clearTimeout(timer);
 process.stderr.write(stderr);if(result.code!==0&&result.signal!=='SIGKILL')throw new Error(stderr);
 return result;
}
for(const signal of ['SIGTERM','SIGINT'] as const)process.on(signal,()=>{stop=true;child?.kill('SIGUSR2');});
try{
 const first=await run();
 if(scenario.startsWith('crash-')){if(stop)throw new Error('supervisor_stopped_before_recovery');assert.equal(first.signal,'SIGKILL');const checkpoint=JSON.parse(await readFile('/work/checkpoint.json','utf8'));await rm('/work/journal/owner.lock');await rm('/router/inference.sock',{force:true});assert.equal((await run(true)).code,0);const result=JSON.parse(await readFile('/work/result.json','utf8'));await writeFile('/work/result.json',JSON.stringify({...result,checkpoint,supervisor:{verifiedChildExit:'SIGKILL',staleLockRemovedAfterExit:true}}));}
 else assert.equal(first.code,0);
 console.log(JSON.stringify({event:'finished',result:JSON.parse(await readFile('/work/result.json','utf8'))}));
}catch(error){console.error(String(error));process.exitCode=1;}
