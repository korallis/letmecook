import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdir,readFile,writeFile,readdir } from 'node:fs/promises';
import { dirname,resolve,join } from 'node:path';
import { randomBytes,createHash } from 'node:crypto';
import { setTimeout as delay } from 'node:timers/promises';
import { inputProcess } from './lifecycle.ts';
import { commonArgs,inspectProfile,VARIANT,LABEL } from './isolation.ts';
import { assertRuntime,selectedProfile } from '../isolation/profile.ts';
const exec=promisify(execFile),root=resolve(import.meta.dirname,'../..');
const context=process.env.GAFFER_BRIDGE_DOCKER_CONTEXT??'desktop-linux';
const source=process.env.GAFFER_BRIDGE_ROUTER_SOURCE;assert(source&&source.startsWith('/'),'absolute public pinned source required');
const image=process.env.GAFFER_BRIDGE_IMAGE??'gaffer-router-extension-deps:0.5.75-locked';
const output=resolve(process.argv[2]??join(root,'docs/evidence/router-boundary-bridge-run.json'));
const runId='gaffer-bridge-'+randomBytes(6).toString('hex'),ids:string[]=[],volumes:string[]=[];
const stop=new AbortController();for(const signal of ['SIGINT','SIGTERM'] as const)process.on(signal,()=>stop.abort());let cleaning=false;
const evidence:any={schema:1,observedAt:new Date().toISOString(),runId,result:'blocked',variant:VARIANT,selectedProfile,realProviderCalled:false,syntheticUpstreamCalled:true,privateDataUsed:false,productionAdmission:false,nativeProviderOutputBound:false,independentReview:'required',sources:{},cases:[],cleanup:{verified:false}};
await mkdir(dirname(output),{recursive:true});await writeFile(output,JSON.stringify(evidence,null,2)+'\n');
const secrets:string[]=[];const redact=(s:string)=>{for(const secret of [root,source!,...secrets])s=s.replaceAll(secret,secret===root?'<gaffer-source>':secret===source?'<public-router-source>':'<scoped-token>');return s;};
async function docker(args:string[],timeout=15000){if(!cleaning)stop.signal.throwIfAborted();try{const result=await exec('docker',['--context',context,...args],{timeout,maxBuffer:4*1024*1024,signal:!cleaning&&['inspect','logs','wait','top'].includes(args[0])?stop.signal:undefined});return (result.stdout+(args[0]==='logs'?result.stderr:'')).trim();}catch(e:any){throw new Error(redact(e.stderr??e.message));}}
const inspect=async(id:string)=>JSON.parse(await docker(['inspect',id]))[0];
async function event(id:string,name:string){const until=Date.now()+10000;while(Date.now()<until){const logs=await docker(['logs',id]);const found=logs.split('\n').filter(s=>s.startsWith('{')).map(s=>{try{return JSON.parse(s);}catch{return null;}}).find(x=>x?.event===name);if(found)return found;const s=await inspect(id);if(!s.State.Running)throw new Error(`gateway exited before ${name}: ${redact(logs)} ${redact(await docker(['logs',id]))}`);await delay(50,undefined,{signal:stop.signal});}throw new Error('gateway_event_timeout');}
async function workerInput(id:string,grant:any){try{return await inputProcess('docker',['--context',context,'start','--attach','--interactive',id],JSON.stringify(grant),stop.signal);}catch(e:any){throw new Error(redact(e.message));}}

try{
 async function digestTree(dir:string){for(const name of (await readdir(join(root,dir),{withFileTypes:true})).sort((a,b)=>a.name.localeCompare(b.name))){if(name.name==='node_modules')continue;const file=dir+'/'+name.name;if(name.isDirectory())await digestTree(file);else if(/\.(ts|mjs|json)$/.test(name.name)||name.name==='Dockerfile')evidence.sources[file]=createHash('sha256').update(await readFile(join(root,file))).digest('hex');}}
 for(const dir of ['experiments/inference-boundary','experiments/router-boundary-bridge','experiments/router-authority-extension'])await digestTree(dir);
 const version=JSON.parse(await docker(['version','--format','{{json .}}'])),info=JSON.parse(await docker(['info','--format','{{json .}}']));assertRuntime(version,info);evidence.runtime={server:version.Server,cgroupVersion:info.CgroupVersion,securityOptions:info.SecurityOptions,image:JSON.parse(await docker(['image','inspect',image]))[0].Id};
 assert.equal(evidence.runtime.image,JSON.parse(await readFile(join(root,'experiments/router-authority-extension/runtime-identity.json'),'utf8')).imageId);
 const all=[['compatible','success'],['compatible','tools'],['compatible','account-fallback'],['compatible','model-fallback'],['compatible','zero-auth'],['compatible','changed-graph'],['compatible','malformed-receipt'],['compatible','delay'],['compatible','post-decision-cancel'],['compatible','drain'],['compatible','mutation-failed'],['compatible','mutation-timeout'],['compatible','persist-failure'],['compatible','persist-after-rename'],['compatible','pre-send-stop'],['compatible','backpressure-stop'],['native','receipt-stop'],['native','success'],['native','tools'],['native','failed'],['native','incomplete'],['native','partial'],['native','account-fallback'],['native','model-fallback'],['native','refresh'],['native','proactive-refresh'],['native','refresh-stop'],['native','stop'],['native','fence'],...['reserved','send-possible','post-send','receipt','decision'].map(s=>['compatible','crash-'+s])];
 const selected=process.env.GAFFER_BRIDGE_CASES?.split(',');
 for(const [profile,scenario] of all){if(selected&&!selected.includes(profile+':'+scenario))continue;stop.signal.throwIfAborted();const label=profile+'-'+scenario,volume=runId+'-'+label+'-socket';volumes.push(volume);await docker(['volume','create','--label',`${LABEL}=${runId}`,'--driver','local','--opt','type=tmpfs','--opt','device=tmpfs','--opt','o=size=1m,uid=1000,gid=1000,mode=0700',volume]);
  const gateway=runId+'-'+label+'-gateway';ids.push(gateway);const gatewayBinds={'/bridge':join(root,'experiments/router-boundary-bridge'),'/inference-boundary':join(root,'experiments/inference-boundary'),'/probe':join(root,'experiments/router-authority-extension'),'/router-source':source};const args=commonArgs(gateway,runId,true);
  for(const [destination,path] of Object.entries(gatewayBinds))args.push('--mount',`type=bind,source=${path},target=${destination},readonly`);args.push('--mount',`type=volume,source=${volume},target=/router,volume-nocopy`,'--env','DATA_DIR=/work/router-db','--env',`GAFFER_SYNTHETIC_NATIVE=${profile==='native'?'1':''}`,'--env',`PROOF_ROLE=${scenario==='tools'?'planner':'worker'}`,image,'node','/bridge/supervise.ts',scenario);
  await docker(args);const gatewayProfile=inspectProfile(await inspect(gateway),image,runId,true,volume,gatewayBinds);await docker(['start',gateway]);const ready=await event(gateway,'ready');secrets.push(ready.token);
  const worker=runId+'-'+label+'-worker';ids.push(worker);const workerBinds={'/fixture/worker.ts':join(root,'experiments/router-boundary-bridge/worker.ts')};const wargs=commonArgs(worker,runId,false);wargs.push('--interactive','--mount',`type=bind,source=${workerBinds['/fixture/worker.ts']},target=/fixture/worker.ts,readonly`,'--mount',`type=volume,source=${volume},target=/router,readonly,volume-nocopy`,image,'node','/fixture/worker.ts');await docker(wargs);const workerProfile=inspectProfile(await inspect(worker),image,runId,false,volume,workerBinds);
  const workerLogs=await workerInput(worker,ready);const consumer=JSON.parse(workerLogs.trim().split('\n').at(-1)!);const workerState=(await inspect(worker)).State;assert.equal(workerState.ExitCode,0);assert.equal(workerState.OOMKilled,false);
  if(!scenario.startsWith('crash-'))await docker(['kill','--signal','SIGTERM',gateway]);assert.equal(Number(await docker(['wait',gateway])),0);const finished=await event(gateway,'finished');const gatewayState=(await inspect(gateway)).State;assert.equal(gatewayState.OOMKilled,false);assert.equal(gatewayState.Pid,0);
  const success=['success','tools','delay','account-fallback','model-fallback','refresh','proactive-refresh','drain'].includes(scenario);
  assert.equal(consumer.requests.every((r:any)=>r.completed),success,label);
  if(success){assert.equal(finished.result.actualGlobalQuiescence,true);assert.equal(finished.result.gateSnapshot.reservations.length,0);assert.equal(finished.result.gateSnapshot.decisions.length,consumer.requests.length);for(const r of consumer.requests){const d=finished.result.gateSnapshot.decisions.find((d:any)=>d.requestId===r.requestId);assert.equal(d.verdict,'validated_success');assert.equal(d.delivery,'completed');const e=finished.result.events.find((e:any)=>e.event==='decision_persisted'&&e.requestId===r.requestId);assert(r.firstTerminalAt>=e.at);}}
  if(scenario==='tools'){assert.equal(consumer.toolEffects,2);assert.equal(consumer.requests.length,2);}else if(!success)assert.equal(consumer.toolEffects,0);
  if(scenario==='pre-send-stop'){assert.equal(finished.result.sends.length,0);assert.equal(finished.result.gateSnapshot.decisions[0].verdict,'never_sent');}
  if(['failed','incomplete'].includes(scenario)){assert.equal(finished.result.receipts[0].operations[0].terminal,'provider_'+scenario);assert.equal(finished.result.gateSnapshot.decisions[0].verdict,'quiescent_failure');}
  if(scenario==='partial'){assert.equal(finished.result.sends.length,1);assert.equal(finished.result.actualGlobalQuiescence,false);}
  if(['persist-failure','persist-after-rename'].includes(scenario)){assert.equal(finished.result.gateSnapshot.decisions.length,0);assert(finished.result.audit.every((a:any)=>!a.receipt));assert.equal(finished.result.closeFailed,true);assert.equal(finished.result.journalOnDisk.decisions.length,scenario==='persist-after-rename'?1:0);}
  if(scenario==='zero-auth'){assert.equal(finished.result.receipts[0].operations.length,0);assert.equal(finished.result.gateSnapshot.decisions[0].verdict,'quiescent_failure');}
  if(scenario==='malformed-receipt'||scenario==='partial')assert.equal(finished.result.gateSnapshot.reservations.length,1);
  await assert.rejects(docker(['exec',gateway,'true']));await assert.rejects(docker(['exec',worker,'true']));
  evidence.cases.push({profile,scenario,result:'passed',gatewayProfile,workerProfile,consumer,gateway:finished.result,stopped:{gatewayPid:gatewayState.Pid,workerPid:workerState.Pid,gatewayExit:gatewayState.ExitCode,workerExit:workerState.ExitCode,execAfterStopDenied:true}});await writeFile(output,redact(JSON.stringify(evidence,null,2))+'\n');console.log(JSON.stringify({scenario:label,result:'passed'}));
 }
 assert(evidence.cases.length>0);evidence.result=selected?'selected-cases-passed':'synthetic-bridge-passed';
}catch(error:any){evidence.error=redact(error.stack??String(error));process.exitCode=1;console.error(evidence.error);
 // Retain redacted actual failure observations with the exact pre-run sources.
 evidence.failedContainers=[];for(const id of ids)try{const s=await inspect(id);evidence.failedContainers.push({name:id,state:s.State,logs:redact(await docker(['logs',id]))});}catch{}
}finally{
 cleaning=true;const errors:string[]=[];
 for(const id of ids)try{const s=await inspect(id);assert.equal(s.Config.Labels[LABEL],runId);await docker(['rm','--force',id]);}catch{errors.push('container:'+id);}
 for(const volume of volumes)try{const v=JSON.parse(await docker(['volume','inspect',volume]))[0];assert.equal(v.Labels[LABEL],runId);await docker(['volume','rm',volume]);}catch{errors.push('volume:'+volume);}
 const containers=await docker(['ps','-aq','--filter',`label=${LABEL}=${runId}`]),remainingVolumes=await docker(['volume','ls','-q','--filter',`label=${LABEL}=${runId}`]);evidence.cleanup={verified:!errors.length&&!containers&&!remainingVolumes,containersRemaining:containers?containers.split('\n').length:0,volumesRemaining:remainingVolumes?remainingVolumes.split('\n').length:0,errors};
 if(!evidence.cleanup.verified||stop.signal.aborted){evidence.result='blocked';process.exitCode=1;}await writeFile(output,redact(JSON.stringify(evidence,null,2))+'\n');console.log(JSON.stringify({result:evidence.result,cases:evidence.cases.length,cleanup:evidence.cleanup.verified}));
}
