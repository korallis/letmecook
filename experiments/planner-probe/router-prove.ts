import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdir,readFile,writeFile,readdir,mkdtemp,cp,rm,chmod } from 'node:fs/promises';
import { dirname,resolve,join } from 'node:path';
import { randomBytes,createHash } from 'node:crypto';
import { setTimeout as delay } from 'node:timers/promises';
import { inputProcess } from '../router-boundary-bridge/lifecycle.ts';
import { commonArgs,inspectProfile,VARIANT,LABEL } from '../router-boundary-bridge/isolation.ts';
import { assertRuntime,selectedProfile } from '../isolation/profile.ts';
const exec=promisify(execFile),root=resolve(import.meta.dirname,'../..');
let staging = '';
const context=process.env.GAFFER_BRIDGE_DOCKER_CONTEXT??'desktop-linux';
const source=process.env.GAFFER_BRIDGE_ROUTER_SOURCE;assert(source&&source.startsWith('/'),'absolute public pinned source required');
const image=process.env.GAFFER_BRIDGE_IMAGE??'gaffer-router-extension-deps:0.5.75-locked';
const output=resolve(process.argv[2]??join(root,'docs/evidence/planner-router-run.json'));
const runId='gaffer-planner-' +randomBytes(6).toString('hex'),ids:string[]=[],volumes:string[]=[];
const stop=new AbortController();for(const signal of ['SIGINT','SIGTERM'] as const)process.on(signal,()=>stop.abort());let cleaning=false;
const evidence:any={schema:1,observedAt:new Date().toISOString(),runId,result:'blocked',variant:'planner-consumer-128m-router-gateway-768m-uds-v1',gatewayVariant:VARIANT,issueComplete:false,selectedProfile,realProviderCalled:false,syntheticUpstreamCalled:true,privateDataUsed:false,productionAdmission:false,nativeProviderOutputBound:false,independentReview:'required',sources:{},cases:[],cleanup:{verified:false}};
await mkdir(dirname(output),{recursive:true});await writeFile(output,JSON.stringify(evidence,null,2)+'\n');
const secrets:string[]=[];const redact=(s:string)=>{for(const secret of [root,source!,...secrets])s=s.replaceAll(secret,secret===root?'<gaffer-source>':secret===source?'<public-router-source>':'<scoped-token>');return s;};
async function docker(args:string[],timeout=15000){if(!cleaning)stop.signal.throwIfAborted();try{const result=await exec('docker',['--context',context,...args],{timeout,maxBuffer:4*1024*1024,signal:!cleaning&&['inspect','logs','wait','top'].includes(args[0])?stop.signal:undefined});return (result.stdout+(args[0]==='logs'?result.stderr:'')).trim();}catch(e:any){throw new Error(redact(e.stderr??e.message));}}
const inspect=async(id:string)=>JSON.parse(await docker(['inspect',id]))[0];
async function event(id:string,name:string){const until=Date.now()+10000;while(Date.now()<until){const logs=await docker(['logs',id]);const found=logs.split('\n').filter(s=>s.startsWith('{')).map(s=>{try{return JSON.parse(s);}catch{return null;}}).find(x=>x?.event===name);if(found)return found;const s=await inspect(id);if(!s.State.Running)throw new Error(`gateway exited before ${name}: ${redact(logs)} ${redact(await docker(['logs',id]))}`);await delay(50,undefined,{signal:stop.signal});}throw new Error('gateway_event_timeout');}
async function workerInput(id:string,grant:any){try{return await inputProcess('docker',['--context',context,'start','--attach','--interactive',id],JSON.stringify(grant),stop.signal);}catch(e:any){throw new Error(redact(e.message));}}

try{
 async function digestTree(dir:string){for(const name of (await readdir(join(root,dir),{withFileTypes:true})).sort((a,b)=>a.name.localeCompare(b.name))){if(name.name==='node_modules')continue;const file=dir+'/'+name.name;if(name.isDirectory())await digestTree(file);else if(/\.(ts|mjs|json|txt)$/.test(name.name)||name.name==='Dockerfile')evidence.sources[file]=createHash('sha256').update(await readFile(join(root,file))).digest('hex');}}
 for(const dir of ['experiments/inference-boundary','experiments/router-boundary-bridge','experiments/router-authority-extension','experiments/planner-probe','tests/fixtures/planner'])await digestTree(dir);
 const version=JSON.parse(await docker(['version','--format','{{json .}}'])),info=JSON.parse(await docker(['info','--format','{{json .}}']));assertRuntime(version,info);evidence.runtime={hostNode:process.version,server:version.Server,cgroupVersion:info.CgroupVersion,securityOptions:info.SecurityOptions,image:JSON.parse(await docker(['image','inspect',image]))[0].Id};
 assert.equal(evidence.runtime.image,JSON.parse(await readFile(join(root,'experiments/router-authority-extension/runtime-identity.json'),'utf8')).imageId);
 staging = await mkdtemp('/tmp/gaffer-planner-consumer-'); await chmod(staging,0o755);
 const staged = ['planner.ts','transport.ts','reader.ts','public-fixture.ts','router-consumer.ts','package.json'].map(f=>'experiments/planner-probe/'+f).concat(['json.ts','types.ts','protocol.ts','router-policy.ts','native-policy.ts','profiles.ts','profile-ids.ts'].map(f=>'experiments/inference-boundary/'+f),['native-profile','native-planner','planner-plan-schema','native-responses','responses-terminal','scope-profile','initial-suite'].map(f=>'experiments/router-authority-extension/overlay/'+f+'.mjs'),['tests/fixtures/planner/fixture.txt','tests/fixtures/planner/plan.schema.json']);
 for (const file of staged) { await mkdir(join(staging,dirname(file)),{recursive:true}); await cp(join(root,file),join(staging,file)); }
 for (const pkg of ['ajv','fast-deep-equal','fast-uri','json-schema-traverse','require-from-string']) await cp(join(root,'experiments/planner-probe/node_modules',pkg),join(staging,'experiments/planner-probe/node_modules',pkg),{recursive:true});
 evidence.consumerStagedFiles=staged; evidence.consumerPackages=['ajv@8.20.0','fast-deep-equal','fast-uri','json-schema-traverse','require-from-string'];
 const all=[...['read','read-repair','unknown-fallback','direct','repair','empty','whitespace','stale','duplicate','invalid-repair','repair-tool','injection','delay-read','delay-proposal','delay-repair','delay-eof','missing-receipt','mismatched-receipt','zero-auth','partial','unavailable','cancel','hold','receipt-cancel','scope-expiry','lease-expiry','changed-graph','graph-at-receipt','graph-after-decision','post-decision-cancel','persist-failure','read-budget','file-budget','request-budget','request-bytes','response-bytes'].map(s=>['compatible',s]),...['read','repair','terminal-text-only','failed','incomplete','partial','receipt-cancel'].map(s=>['native',s])];
 const selected=process.env.GAFFER_BRIDGE_CASES?.split(',');
 for(const [profile,scenario] of all){if(selected&&!selected.includes(profile+':'+scenario))continue;stop.signal.throwIfAborted();const label=profile+'-'+scenario,volume=runId+'-'+label+'-socket';volumes.push(volume);await docker(['volume','create','--label',`${LABEL}=${runId}`,'--driver','local','--opt','type=tmpfs','--opt','device=tmpfs','--opt','o=size=1m,uid=1000,gid=1000,mode=0700',volume]);
  const gateway=runId+'-'+label+'-gateway';ids.push(gateway);const gatewayBinds={'/planner-probe':join(root,'experiments/planner-probe'),'/router-boundary-bridge':join(root,'experiments/router-boundary-bridge'),'/inference-boundary':join(root,'experiments/inference-boundary'),'/probe':join(root,'experiments/router-authority-extension'),'/router-authority-extension':join(root,'experiments/router-authority-extension'),'/router-source':source};const args=commonArgs(gateway,runId,true);
  for(const [destination,path] of Object.entries(gatewayBinds))args.push('--mount',`type=bind,source=${path},target=${destination},readonly`);args.push('--mount',`type=volume,source=${volume},target=/router,volume-nocopy`,'--env','DATA_DIR=/work/router-db','--env',`GAFFER_SYNTHETIC_NATIVE=${profile==='native'?'1':''}`,'--env',`PROOF_ROLE=${scenario==='tools'?'planner':'worker'}`,image,'node','/planner-probe/router-supervise.ts',scenario);
  await docker(args);const gatewayProfile=inspectProfile(await inspect(gateway),image,runId,true,volume,gatewayBinds);await docker(['start',gateway]);const ready=await event(gateway,'ready');secrets.push(ready.token);
  const worker=runId+'-'+label+'-worker';ids.push(worker);const workerBinds={'/consumer':staging};const wargs=commonArgs(worker,runId,false);wargs.push('--interactive','--mount',`type=bind,source=${staging},target=/consumer,readonly`,'--mount',`type=volume,source=${volume},target=/router,readonly,volume-nocopy`,image,'node','/consumer/experiments/planner-probe/router-consumer.ts');await docker(wargs);const workerProfile=inspectProfile(await inspect(worker),image,runId,false,volume,workerBinds);
  const workerLogs=await workerInput(worker,ready);const consumer=JSON.parse(workerLogs.trim().split('\n').at(-1)!);const workerState=(await inspect(worker)).State;assert.equal(workerState.ExitCode,0);assert.equal(workerState.OOMKilled,false);
  if(!scenario.startsWith('crash-'))await docker(['kill','--signal','SIGTERM',gateway]);assert.equal(Number(await docker(['wait',gateway])),0);const finished=await event(gateway,'finished');const gatewayState=(await inspect(gateway)).State;assert.equal(gatewayState.OOMKilled,false);assert.equal(gatewayState.Pid,0);
  // Preserve actual observations before any assertion, including failed cases.
  const observation={profile,scenario,result:'observed',gatewayProfile,workerProfile,consumer,gateway:finished.result}; evidence.cases.push(observation); await writeFile(output,redact(JSON.stringify(evidence,null,2))+'\n');
  const success=['read','read-repair','direct','repair','empty','whitespace','stale','duplicate','delay-read','delay-proposal','delay-repair','delay-eof'].includes(scenario);
  assert.equal(consumer.result.outcome==='plan_proposed',success,label);
  assert.equal(consumer.result.authority,'proposal_only'); assert.equal(consumer.repeatedRunCached,true);
  assert(consumer.result.usage.requests<=3 && consumer.result.usage.repairs<=1 && consumer.result.usage.files<=1 && consumer.result.usage.readBytes<=4096);
  for (const r of consumer.requests) {
    assert(!Object.hasOwn(r.body,'tool_choice')); assert(!Object.hasOwn(r.body,'max_tokens')); assert.equal(r.body.max_completion_tokens,1024);
    assert.equal(Object.hasOwn(r.body,'tools'),r.toolsAllowed);
    const packet=JSON.stringify(JSON.parse(r.body.messages[1].content).trusted_packet); for(const key of ['deploymentId','graphDigest','synthetic_access','synthetic_gateway_key','synthetic_workspace','sourceLock']) assert(!packet.includes(key));
    if(r.requestId){const d=finished.result.gateSnapshot.decisions.find((d:any)=>d.requestId===r.requestId); assert.equal(d?.verdict,'validated_success'); assert.equal(d.delivery,'completed'); const persisted=finished.result.events.find((e:any)=>e.event==='decision_persisted'&&e.requestId===r.requestId);assert(r.completedAt>=persisted.at);}
  }
  for(const r of finished.result.ingress){assert.deepEqual(Object.keys(r.body).sort(),['max_tokens','messages','model','stream',...(r.body.tools?['tools']:[])].sort());assert.equal(r.host,'127.0.0.1');assert.equal(r.body.max_tokens,1024);assert(!Object.hasOwn(r.body,'max_completion_tokens'));assert(!Object.hasOwn(r.body,'tool_choice'));}
  for(const send of finished.result.sends){ if(profile==='native') for(const cap of ['max_tokens','max_completion_tokens','max_output_tokens'])assert(!Object.hasOwn(send.body,cap));else assert.equal(send.body.max_tokens,1024);}
  const readRequest=consumer.requests.find((r:any)=>r.body.messages.some((m:any)=>m.role==='tool'));
  if(readRequest){const first=consumer.result.completions[0];const assistant=readRequest.body.messages.find((m:any)=>m.role==='assistant');const tool=readRequest.body.messages.find((m:any)=>m.role==='tool');assert.equal(assistant.content,null);assert.equal(tool.tool_call_id,assistant.tool_calls[0].id);assert.equal(tool.tool_call_id,`call_${first.requestId}_0`);assert(!readRequest.body.tools);}
  if(profile==='native' && readRequest){const input=finished.result.sends[1].body.input;const call=input.find((x:any)=>x.type==='function_call');const output=input.find((x:any)=>x.type==='function_call_output');assert.equal(call.call_id,consumer.result.completions[0].calls[0]);assert.equal(output.call_id,call.call_id);assert.equal(call.name,'read_file');assert.deepEqual(JSON.parse(call.arguments),{path:'fixture.txt'});}
  if(scenario==='terminal-text-only'){assert.equal(consumer.result.outcome,'invalid_plan');assert.deepEqual(consumer.result.usage,{assessments:1,repairs:1,requests:2,files:0,readBytes:0});}
  const firstRead=consumer.effects.find((e:any)=>e.files>0),firstRepair=consumer.effects.find((e:any)=>e.repairs>0);
  for(const effect of [firstRead,firstRepair].filter(Boolean)){const p=finished.result.events.find((e:any)=>e.event==='decision_persisted');assert(p && effect.at>=p.at);}
  if(scenario==='unknown-fallback'){assert.equal(finished.result.sends.length,1);assert.equal(finished.result.receipts[0].operations[0].terminal,'unknown');assert.equal(consumer.result.usage.requests,1);assert.equal(consumer.result.usage.files,0);assert.equal(consumer.result.usage.repairs,0);}
  if(scenario==='read-repair'){assert.deepEqual(consumer.result.usage,{assessments:1,repairs:1,requests:3,files:1,readBytes:484});assert.equal(consumer.result.completions.length,3);}
  if(['file-budget','read-budget'].includes(scenario)){assert.equal(consumer.requests[0].body.tools,undefined);assert.equal(consumer.result.usage.files,0);assert.equal(consumer.result.usage.repairs,0);}
  if(scenario==='read'){assert.deepEqual(consumer.result.usage,{assessments:1,repairs:0,requests:2,files:1,readBytes:484});assert.equal(consumer.result.completions.length,2);}
  if(['repair','empty','whitespace','stale','duplicate','delay-repair','invalid-repair','repair-tool'].includes(scenario)){assert.equal(consumer.result.usage.repairs,1);assert.equal(consumer.requests.length,2);assert.equal(consumer.result.usage.files,0);assert(!consumer.requests[1].body.tools);assert(consumer.requests[1].body.messages.filter((m:any)=>m.role==='assistant').every((m:any)=>m.content.trim()));}
  if(['missing-receipt','mismatched-receipt','partial','persist-failure','receipt-cancel','scope-expiry','lease-expiry','graph-at-receipt','graph-after-decision','post-decision-cancel','failed','incomplete','cancel','hold'].includes(scenario)){assert.equal(consumer.result.usage.files,0);assert.equal(consumer.result.usage.repairs,0);assert.equal(consumer.requests.length,1);assert.equal(consumer.result.proposal,undefined);}
  if(['failed','incomplete'].includes(scenario)){assert.equal(finished.result.receipts[0].operations[0].terminal,'provider_'+scenario);assert.equal(finished.result.gateSnapshot.decisions[0].verdict,'quiescent_failure');}
  if(['scope-expiry','lease-expiry'].includes(scenario))assert.equal(finished.result.audit[0].reason,'cancelled');
  if(['changed-graph','graph-at-receipt','graph-after-decision'].includes(scenario)){const changed=finished.result.events.find((e:any)=>e.event==='router_graph_replaced');assert(changed);assert.notEqual(changed.graphDigest,finished.result.policy.authority.graphDigest);assert.equal(changed.revision,'policy_2');assert.equal(finished.result.sends.length,scenario==='changed-graph'?0:1);}
  if(scenario==='zero-auth'){assert.equal(finished.result.receipts[0].operations.length,0);assert.equal(consumer.result.usage.files,0);}
  if(scenario==='persist-failure'){assert.equal(finished.result.gateSnapshot.decisions.length,0);assert.equal(finished.result.closeFailed,true);}
  if(['missing-receipt','mismatched-receipt','partial'].includes(scenario))assert.equal(finished.result.gateSnapshot.reservations.length,1);
  observation.result='passed';
  await assert.rejects(docker(['exec',gateway,'true']));await assert.rejects(docker(['exec',worker,'true']));
  Object.assign(observation,{stopped:{gatewayPid:gatewayState.Pid,workerPid:workerState.Pid,gatewayExit:gatewayState.ExitCode,workerExit:workerState.ExitCode,execAfterStopDenied:true}});await writeFile(output,redact(JSON.stringify(evidence,null,2))+'\n');console.log(JSON.stringify({scenario:label,result:'passed'}));
 }
 assert(evidence.cases.length>0);evidence.result=selected?'selected-cases-passed':'synthetic-planner-router-passed';
}catch(error:any){evidence.error=redact(error.stack??String(error));process.exitCode=1;console.error(evidence.error);
 // Retain redacted actual failure observations with the exact pre-run sources.
 evidence.failedContainers=[];for(const id of ids)try{const s=await inspect(id);evidence.failedContainers.push({name:id,state:s.State,logs:redact(await docker(['logs',id]))});}catch{}
}finally{
 cleaning=true;const errors:string[]=[];
 try{if(staging)await rm(staging,{recursive:true,force:true});}catch{errors.push('consumer-staging');}
 for(const id of ids)try{const s=await inspect(id);assert.equal(s.Config.Labels[LABEL],runId);await docker(['rm','--force',id]);}catch{errors.push('container:'+id);}
 for(const volume of volumes)try{const v=JSON.parse(await docker(['volume','inspect',volume]))[0];assert.equal(v.Labels[LABEL],runId);await docker(['volume','rm',volume]);}catch{errors.push('volume:'+volume);}
 const containers=await docker(['ps','-aq','--filter',`label=${LABEL}=${runId}`]),remainingVolumes=await docker(['volume','ls','-q','--filter',`label=${LABEL}=${runId}`]);evidence.cleanup={verified:!errors.length&&!containers&&!remainingVolumes,containersRemaining:containers?containers.split('\n').length:0,volumesRemaining:remainingVolumes?remainingVolumes.split('\n').length:0,errors};
 if(!evidence.cleanup.verified||stop.signal.aborted){evidence.result='blocked';process.exitCode=1;}await writeFile(output,redact(JSON.stringify(evidence,null,2))+'\n');console.log(JSON.stringify({result:evidence.result,cases:evidence.cases.length,cleanup:evidence.cleanup.verified}));
}
