// Deterministic offline exercise of the same prepare/select/start/stop commands.
// No provider connection: relay destination is synthetic and inference is absent.
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { readFile,writeFile,mkdir,open,realpath,access } from 'node:fs/promises';
import { resolve,join } from 'node:path';
import { randomBytes } from 'node:crypto';
import { profile } from './fixtures.mjs';
import { digest } from '../router-authority-extension/overlay/native-profile.mjs';
const execute=promisify(execFile),root=resolve(process.argv[2]??'/tmp/native-deployment-proof'),source=process.env.GAFFER_ROUTER_SOURCE,context=process.env.GAFFER_DOCKER_CONTEXT??'desktop-linux';assert(source?.startsWith('/'));
await mkdir(root,{recursive:true,mode:0o700});const input=join(root,'input'),record=join(root,'deployment.json');await mkdir(input,{mode:0o700});
const n=profile();n.deployment.id='synthetic_prepare_'+randomBytes(6).toString('hex');const variant=structuredClone(n);variant.toolPaths.push('second.txt');
const config={settings:{requireApiKey:true,rtkEnabled:false,headroomEnabled:false,pxpipeEnabled:false,cavemanEnabled:false,ponytailEnabled:false,ccFilterNaming:false,capacityAdapter:Object.fromEntries(['vision','pdf','audioInput','videoInput'].map(k=>[k,{enabled:false,models:[]}]))},providerConnections:n.connections.map((c,i)=>({id:c.id,provider:'codex',authType:'oauth',name:'Synthetic',priority:i+1,isActive:true,accessToken:'synthetic_access_'+i,expiresAt:new Date(c.expiresAt).toISOString(),providerSpecificData:{chatgptAccountId:'synthetic_workspace_'+i}})),combos:[{id:'native_route',name:'gpt-6-astra',models:['cx/gpt-6-astra']}]};
for(const [name,value] of Object.entries({'profile.json':n,'profiles.json':[n,variant],'config.json':config,'relay.json':{address:'172.19.0.2',evidence:'synthetic'}}))await writeFile(join(input,name),JSON.stringify(value),{mode:0o600});
const failureMode=process.argv[3]??null;assert([null,'open','sync','gateway-stop'].includes(failureMode));
const result:any={failureMode,schema:1,evidence:'synthetic-deployment-controls',live:false,result:'failed'};
async function cli(command:string,argument?:string){const args=[join(import.meta.dirname,'deployment.ts'),command,record,...(argument?[argument]:[]),...(command==='prepare'?[source!]:[])];const r=await execute(process.execPath,args,{timeout:30000,maxBuffer:16*1024*1024});return JSON.parse(r.stdout);}
async function docker(args:string[]){const r=await execute('docker',['--context',context,...args],{timeout:15000,maxBuffer:16*1024*1024});return r.stdout.trim();}
try{
 result.prepared=await cli('prepare',input);result.before=await cli('inspect');assert.equal(result.before.current.scope,null);assert.equal(result.before.packet.profiles.length,2);assert.equal(result.before.packet.registryDigest,digest(result.before.packet.profiles));
 const incumbent=await readFile(record);const incumbentRecord=JSON.parse(incumbent.toString());const incumbentStates=await Promise.all(incumbentRecord.containers.map(async name=>JSON.parse(await docker(['inspect',name]))[0].State));await assert.rejects(cli('prepare',input),/EEXIST/);assert.deepEqual(await readFile(record),incumbent);const afterDuplicate=await Promise.all(incumbentRecord.containers.map(async name=>JSON.parse(await docker(['inspect',name]))[0].State));assert.deepEqual(afterDuplicate,incumbentStates);result.duplicatePrepare={denied:true,recordUnchanged:true,containerStatesUnchanged:true};
 const paused=execute(process.execPath,['--import',join(import.meta.dirname,'host-faults.mjs'),join(import.meta.dirname,'deployment.ts'),'inspect',record],{env:{...process.env,GAFFER_TEST_PAUSE_RECORD:await realpath(record)},timeout:15000,maxBuffer:1024*1024});
 const pauseDeadline=Date.now()+5000;while(true){try{await access(record+'.paused');break;}catch{assert(Date.now()<pauseDeadline,'command did not pause');await new Promise(r=>setTimeout(r,10));}}
 try{await assert.rejects(cli('start',result.prepared.packetDigest),/EEXIST/);assert.deepEqual(await readFile(record),incumbent);}finally{await writeFile(record+'.resume','resume');}
 await paused;result.commandSerialization={overlapDenied:true,recordUnchanged:true};
 result.started=await cli('start',result.prepared.packetDigest);const initial=result.started.scope;assert.equal(initial.spent,0);
 if(failureMode){
  if(failureMode==='gateway-stop'){const r=JSON.parse(await readFile(record,'utf8'));const s=JSON.parse(await docker(['inspect',r.gateway]))[0];assert.equal(s.Config.Labels['dev.gaffer.native-deployment'],r.run);await docker(['exec',r.gateway,'node','-e',"require('node:fs').mkdirSync('/state/result.json')"]);await assert.rejects(cli('stop'),/gateway_stop_failed/);assert.equal(JSON.parse(await readFile(record,'utf8')).phase,'closed_failure');}
  else await assert.rejects(execute(process.execPath,['--import',join(import.meta.dirname,'host-faults.mjs'),join(import.meta.dirname,'deployment.ts'),'inspect',record],{env:{...process.env,GAFFER_TEST_RECORD_FAILURE:await realpath(record),GAFFER_TEST_RECORD_FAILURE_MODE:failureMode},timeout:30000,maxBuffer:1024*1024}),/injected_record_/);
  const r=JSON.parse(await readFile(record,'utf8'));result.fenced=[];for(const name of r.containers){const s=JSON.parse(await docker(['inspect',name]))[0];assert.equal(s.Config.Labels['dev.gaffer.native-deployment'],r.run);assert.equal(s.State.Pid,0);result.fenced.push({name,state:s.State});}
  const program="const {DatabaseSync}=require('node:sqlite'),fs=require('node:fs');const db=new DatabaseSync('/state/router-db/db/data.sqlite',{readOnly:true});console.log(JSON.stringify({ownerRetained:fs.existsSync('/state/deployment-owner.lock'),scope:db.prepare('SELECT * FROM gaffer_scopes').get()}));db.close();";
  result.recovery=JSON.parse(await docker(['run','--rm','--network','none','--user','1000:1000','--cap-drop','ALL','--security-opt','no-new-privileges=true','--read-only','--memory','128m','--memory-swap','128m','--pids-limit','32','--mount',`type=volume,source=${r.stateVolume},target=/state,readonly,volume-nocopy`,'gaffer-router-extension-deps:0.5.75-locked','node','-e',program]));assert.equal(result.recovery.ownerRetained,true);assert.deepEqual(result.recovery.scope,initial);result.result='passed';
 }else{
 result.selected=await cli('select',digest(result.before.packet.profiles[1]));assert.deepEqual(result.selected.scope,initial);assert.equal(result.selected.policy.authority.boot,result.before.selectedPolicy.authority.boot);assert.equal(result.selected.policy.authority.generation,result.before.selectedPolicy.authority.generation+1);
 result.stopped=await cli('stop');const r=JSON.parse(await readFile(record,'utf8'));
 const saved=await docker(['run','--rm','--label','dev.gaffer.native-proof='+r.run,'--network','none','--user','1000:1000','--cap-drop','ALL','--security-opt','no-new-privileges=true','--read-only','--memory','128m','--memory-swap','128m','--pids-limit','32','--mount',`type=volume,source=${r.stateVolume},target=/state,readonly,volume-nocopy`,'gaffer-router-extension-deps:0.5.75-locked','cat','/state/result.json']);result.durable=JSON.parse(saved);assert.equal(result.durable.scope.spent,0);assert.equal(result.durable.scope.started,initial.started);assert.equal(result.durable.scope.deadline,initial.deadline);assert.equal(result.durable.observed.sends.length,0);assert.equal(result.durable.receipts.length,0);result.result='passed';
 }
}catch(error){result.error=String(error);process.exitCode=1;}
finally{
 const path=join(root,'result.json'),f=await open(path,'w',0o600);await f.writeFile(JSON.stringify(result,null,2));await f.sync();await f.close();const dir=await open(root,'r');await dir.sync();await dir.close();
 let r;try{r=JSON.parse(await readFile(record,'utf8'));}catch{}const errors=[];
 if(r){for(const id of [...r.transient,...r.containers])try{const s=JSON.parse(await docker(['inspect',id]))[0];assert.equal(s.Config.Labels['dev.gaffer.native-deployment'],r.run);await docker(['rm','--force',id]);}catch{errors.push(id);}for(const v of r.volumes)try{const s=JSON.parse(await docker(['volume','inspect',v]))[0];assert.equal(s.Labels['dev.gaffer.native-state'],n.deployment.id);await docker(['volume','rm',v]);}catch{errors.push(v);}if(r.network)try{const network=JSON.parse(await docker(['network','inspect',r.network]))[0];assert.equal(network.Labels['dev.gaffer.native-deployment'],r.run);await docker(['network','rm',r.network]);}catch{errors.push(r.network);}}
 result.cleanupErrors=errors;result.cleanup=!errors.length;await writeFile(path,JSON.stringify(result,null,2)+'\n');console.log(JSON.stringify({result:result.result,cleanup:result.cleanup,path}));
}
