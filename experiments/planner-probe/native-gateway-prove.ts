// Synthetic two-container proof of the production gateway, staged planner,
// private evidence/ack controls and serial registry selection. Never a launcher.
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { readFile,writeFile,mkdir,mkdtemp,chmod,rm,readdir } from 'node:fs/promises';
import { join,resolve } from 'node:path';
import { randomBytes,createHash } from 'node:crypto';
import { commonArgs,inspectProfile,LABEL } from '../router-boundary-bridge/isolation.ts';
import { inputProcess } from '../router-boundary-bridge/lifecycle.ts';
import { assertRuntime } from '../isolation/profile.ts';
import { digest } from '../router-authority-extension/overlay/native-profile.mjs';
// @ts-expect-error pure synthetic fixture
import { profile } from '../native-evaluation/fixtures.mjs';
import { plannerProfile } from './native-profile.ts';
import { nativePlannerCandidate,acknowledgeNativePlanner } from './native-evidence.ts';
import { stageNativePlanner,nativeConsumerFiles } from './native-staging.ts';
const execute=promisify(execFile),repository=resolve(import.meta.dirname,'../..'),root=resolve(process.argv[2]??'/tmp/planner-native-gateway-proof'),source=process.env.GAFFER_ROUTER_SOURCE;
const scenario=process.argv[3]??'read';assert(['read','clarification'].includes(scenario));const expectedOutcome=scenario==='clarification'?'clarification_proposed':'plan_proposed';
assert(source?.startsWith('/'),'pinned synthetic source required');await mkdir(root,{recursive:true});
const context=process.env.GAFFER_DOCKER_CONTEXT??'desktop-linux',image='gaffer-router-extension-deps:0.5.75-locked',run='planner-gateway-'+randomBytes(6).toString('hex'),ids:string[]=[],volumes:string[]=[],commands:string[][]=[];
const report:any={schema:1,run,scenario,live:false,realProviderCalled:false,syntheticOriginalHttp:true,result:'failed',sources:{},cleanup:false};
let staging='',configuration='';
async function docker(args:string[],timeout=15000){commands.push(args);const r=await execute('docker',['--context',context,...args],{timeout,maxBuffer:16*1024*1024});return(r.stdout+(args[0]==='logs'?r.stderr:'')).trim();}
const inspect=async(name:string)=>JSON.parse(await docker(['inspect',name]))[0];
const abort=new AbortController();
async function input(name:string,value:unknown){return inputProcess('docker',['--context',context,'start','--attach','--interactive',name],JSON.stringify(value),abort.signal);}
async function volume(suffix:string,tmpfs=true){const name=run+'-'+suffix;volumes.push(name);await docker(['volume','create','--label',LABEL+'='+run,...(tmpfs?['--driver','local','--opt','type=tmpfs','--opt','device=tmpfs','--opt','o=size=1m,uid=1000,gid=1000,mode=0700']:[]),name]);return name;}
let controlVolume='';
async function control(message:unknown){
 const name=run+'-control-'+randomBytes(3).toString('hex');ids.push(name);
 const program="const http=require('node:http');let b='';process.stdin.on('data',c=>b+=c);process.stdin.on('end',()=>{const q=http.request({socketPath:'/control/gateway.sock',path:'/control',method:'POST'},r=>{let t='';r.on('data',c=>t+=c);r.on('end',async()=>{if(r.statusCode!==200)process.exitCode=1;for(let i=0;i<t.length;i+=8000)await new Promise(ok=>process.stdout.write(JSON.stringify({chunk:t.slice(i,i+8000)})+'\\n',ok));});});q.setTimeout(3000,()=>q.destroy());q.on('error',()=>process.exit(1));q.end(b);});";
 await docker([...commonArgs(name,run,false),'--interactive','--mount',`type=volume,source=${controlVolume},target=/control,readonly,volume-nocopy`,image,'node','-e',program]);
 const output=await input(name,message);const state=await inspect(name);assert.equal(state.State.ExitCode,0);return JSON.parse(output.trim().split('\n').map(l=>JSON.parse(l).chunk).join(''));
}
try{
 async function hashTree(path:string){for(const item of await readdir(join(repository,path),{withFileTypes:true})){if(item.name==='node_modules'||item.name==='evidence')continue;const file=path+'/'+item.name;if(item.isDirectory())await hashTree(file);else if(/\.(ts|mjs|json|txt)$/.test(file))report.sources[file]=createHash('sha256').update(await readFile(join(repository,file))).digest('hex');}}
 for(const path of ['experiments/planner-probe','experiments/native-evaluation','experiments/router-authority-extension','experiments/inference-boundary','experiments/router-boundary-bridge','tests/fixtures/planner'])await hashTree(path);
 const version=JSON.parse(await docker(['version','--format','{{json .}}'])),info=JSON.parse(await docker(['info','--format','{{json .}}']));assertRuntime(version,info);report.runtime={server:version.Server,image:JSON.parse(await docker(['image','inspect',image]))[0].Id};assert.equal(report.runtime.image,JSON.parse(await readFile(join(repository,'experiments/router-authority-extension/runtime-identity.json'),'utf8')).imageId);
 staging=await mkdtemp('/tmp/planner-native-staged-');await chmod(staging,0o755);await stageNativePlanner(repository,staging);report.stagedFiles=nativeConsumerFiles;
 configuration=await mkdtemp('/tmp/planner-native-config-');await chmod(configuration,0o755);
 const worker=profile(),n=plannerProfile(worker);worker.deployment.id=n.deployment.id='synthetic_planner_'+randomBytes(5).toString('hex');
 const config={settings:{requireApiKey:true,rtkEnabled:false,headroomEnabled:false,pxpipeEnabled:false,cavemanEnabled:false,ponytailEnabled:false,ccFilterNaming:false,capacityAdapter:Object.fromEntries(['vision','pdf','audioInput','videoInput'].map(k=>[k,{enabled:false,models:[]}]))},providerConnections:n.connections.map((c:any,i:number)=>({id:c.id,provider:'codex',authType:'oauth',name:'Synthetic',priority:i+1,isActive:true,accessToken:'synthetic_access_'+i,expiresAt:new Date(c.expiresAt).toISOString(),providerSpecificData:{chatgptAccountId:'synthetic_workspace_'+i}})),combos:[{id:'native_route',name:'gaffer_native',models:['cx/gpt-6-astra']}]};
 for(const [file,value]of Object.entries({'profiles.json':[n,worker],'config.json':config}))await writeFile(join(configuration,file),JSON.stringify(value),{mode:0o644});
 const inference=await volume('inference');controlVolume=await volume('control');const stateVolume=await volume('state',false);
 const init=run+'-init';ids.push(init);await docker(['create','--name',init,'--label',LABEL+'='+run,'--network','none','--user','0:0','--cap-drop','ALL','--cap-add','CHOWN','--security-opt','no-new-privileges=true','--read-only','--mount',`type=volume,source=${stateVolume},target=/state,volume-nocopy`,image,'chown','1000:1000','/state']);await docker(['start',init]);assert.equal(Number(await docker(['wait',init])),0);
 const gateway=run+'-gateway';ids.push(gateway);await docker([...commonArgs(gateway,run,true),'--mount',`type=bind,source=${repository},target=/gaffer,readonly`,'--mount',`type=bind,source=${source},target=/router-source,readonly`,'--mount',`type=bind,source=${configuration},target=/config,readonly`,'--mount',`type=bind,source=${configuration},target=/private,readonly`,'--mount',`type=volume,source=${stateVolume},target=/state,volume-nocopy`,'--mount',`type=volume,source=${controlVolume},target=/control,volume-nocopy`,'--mount',`type=volume,source=${inference},target=/router,volume-nocopy`,'--env','DATA_DIR=/state/router-db','--env','GAFFER_SYNTHETIC_NATIVE=1','--env','GAFFER_SYNTHETIC_PLANNER_CASE='+scenario,image,'node','--experimental-loader','/gaffer/experiments/router-authority-extension/loader.mjs','/gaffer/experiments/native-evaluation/gateway.mjs']);
 report.gatewayInspect=await inspect(gateway);assert.equal(report.gatewayInspect.Image,report.runtime.image);assert.equal(report.gatewayInspect.HostConfig.NetworkMode,'none');assert.equal(report.gatewayInspect.HostConfig.Privileged,false);assert.equal(report.gatewayInspect.HostConfig.ReadonlyRootfs,true);assert.deepEqual(report.gatewayInspect.HostConfig.CapDrop,['ALL']);await docker(['start',gateway]);
 const until=Date.now()+10000;while(!(await docker(['logs',gateway])).includes('"event":"gateway_ready"')){assert(Date.now()<until);assert((await inspect(gateway)).State.Running,await docker(['logs',gateway]));await new Promise(r=>setTimeout(r,30));}
 const prepared=await control({command:'inspect'});report.prepared=prepared;assert.equal(prepared.current.scope,null);assert.equal(prepared.packet.profiles.length,2);
 const scope=await control({command:'start',packetDigest:prepared.packetDigest});report.startedScope=scope;
 const policy=prepared.selectedPolicy,binding={attemptId:'planner_public_fixture',grantId:'planner_fixture_grant',taskId:'planner_fixture_task',leaseId:'planner_fixture_lease',fence:1,role:'planner' as const,routerId:policy.routerId,routeId:policy.routeId,revision:policy.revision,epoch:policy.epoch,expiresAt:Date.now()+30000,leaseExpiresAt:Date.now()+30000,native:{profileDigest:digest(policy.native),scopeId:policy.native.scope.id,authorizationDigest:policy.native.scope.authorizationDigest}};
 const grant=await control({command:'grant',binding});
 const consumer=run+'-consumer';ids.push(consumer);await docker([...commonArgs(consumer,run,false),'--interactive','--mount',`type=bind,source=${staging},target=/consumer,readonly`,'--mount',`type=volume,source=${inference},target=/router,readonly,volume-nocopy`,image,'node','/consumer/experiments/planner-probe/native-consumer.ts']);
 report.consumerProfile=inspectProfile(await inspect(consumer),image,run,false,inference,{'/consumer':staging});
 const output=await input(consumer,{policy,binding,token:grant.token});report.consumer=JSON.parse(output.trim().split('\n').at(-1)!);assert.equal((await inspect(consumer)).State.ExitCode,0);assert.equal(report.consumer.result.outcome,expectedOutcome);
 const evidence=await control({command:'evidence',attemptId:binding.attemptId});report.evidence=evidence;
 const candidate=nativePlannerCandidate(report.consumer.result,evidence,binding);report.candidate=candidate;
 report.supervisorRejections=[];
 for(const [name,mutate] of Object.entries({
  missingReceipt:(x:any):any=>x.e.receipts.pop(),receiptDigest:(x:any):any=>x.e.decisions[0].evidence.receiptDigest='0'.repeat(64),
  missingDecision:(x:any):any=>x.e.decisions.pop(),pendingReservation:(x:any):any=>x.e.reservations.push({requestId:'pending'}),
  outputDrift:(x:any):any=>x.e.decisions.at(-1).nativeOutput[0].content[0].text='changed',
  role:(x:any):any=>x.b.role='worker',policy:(x:any):any=>x.e.selectedPolicy.revision='wrong_revision',
  earlyRead:(x:any):any=>x.r.completions[0].acceptedAt=x.e.decisionTimes[0].at-1,
  count:(x:any):any=>x.r.usage.repairs=1,proposal:(x:any):any=>x.r.proposal.input_revision='0'.repeat(64),
  settings:(x:any):any=>x.r.settings.reasoning.effort='high',
  outcome:(x:any):any=>x.r.outcome=expectedOutcome==='plan_proposed'?'clarification_proposed':'plan_proposed',
  unsupportedOutcome:(x:any):any=>x.r.outcome='approved',
  questions:(x:any):any=>{x.r.proposal.unresolved_questions=expectedOutcome==='plan_proposed'?['Forged question?']:[];x.r.outcome=expectedOutcome==='plan_proposed'?'clarification_proposed':'plan_proposed';},
 })){const x=structuredClone({r:report.consumer.result,e:evidence,b:binding});mutate(x);assert.throws(()=>nativePlannerCandidate(x.r,x.e,x.b));report.supervisorRejections.push(name);}
 for(const field of ['packetDigest','scopeDigest','bindingDigest','policyDigest'])await assert.rejects(control({command:'candidate',value:{...candidate,[field]:'0'.repeat(64)}}));
 await assert.rejects(control({command:'candidate',value:{...candidate,artifact:{...candidate.artifact,path:'fixture.txt'}}}));
 await assert.rejects(control({command:'candidate',value:{...candidate,requestIds:candidate.requestIds.slice(0,-1)}}));
 await assert.rejects(control({command:'candidate',value:{...candidate,artifact:{...candidate.artifact,metadata:{...candidate.artifact.metadata,outcome:expectedOutcome==='plan_proposed'?'clarification_proposed':'plan_proposed'}}}}));
 const forgedProposal=JSON.parse(candidate.artifact.content);forgedProposal.unresolved_questions=expectedOutcome==='plan_proposed'?['Forged question?']:[];
 await assert.rejects(control({command:'candidate',value:{...candidate,artifact:{...candidate.artifact,content:JSON.stringify(forgedProposal),metadata:{...candidate.artifact.metadata,outcome:expectedOutcome==='plan_proposed'?'clarification_proposed':'plan_proposed'}}}}));report.invalidCandidateSubmissions=8;
 const ack=await control({command:'candidate',value:candidate});report.acknowledgement=ack;assert.deepEqual(await control({command:'candidate',value:candidate}),ack);
 const after=await control({command:'evidence',attemptId:binding.attemptId});report.accepted=acknowledgeNativePlanner(candidate,ack,after.artifacts);assert.equal(report.accepted.outcome,scenario==='clarification'?'acknowledged_clarification_proposal':'acknowledged_plan_proposal');assert.equal(after.scope.spent,scenario==='clarification'?1:2);assert.equal(after.decisions.length,scenario==='clarification'?1:2);assert.deepEqual(after.decisions,evidence.decisions);assert.equal(after.scope.started,scope.started);assert.equal(after.scope.deadline,scope.deadline);
 // Registry selection is generation-fenced and keeps the one started scope.
 const selected=await control({command:'select',packetDigest:prepared.packetDigest,profileDigest:digest(prepared.packet.profiles[1])});report.workerSelection=selected;assert.deepEqual(selected.scope,after.scope);assert.equal(selected.policy.authority.boot,policy.authority.boot);assert.equal(selected.policy.authority.generation,policy.authority.generation+1);await assert.rejects(control({command:'grant',binding}));await assert.rejects(control({command:'start',packetDigest:prepared.packetDigest}));
 await control({command:'stop'});assert.equal(Number(await docker(['wait',gateway])),0);assert.equal((await inspect(gateway)).State.Pid,0);await assert.rejects(docker(['exec',gateway,'true']));await assert.rejects(docker(['exec',consumer,'true']));
 const reader=run+'-read';ids.push(reader);const program="const fs=require('node:fs');const x=JSON.parse(fs.readFileSync('/state/'+process.argv[1],'utf8'));for(const key of ['kind','packetDigest','policyDigest','scopeDigest'])if(!x[key])throw Error('missing_binding');console.log(JSON.stringify({digest:require('node:crypto').createHash('sha256').update(JSON.stringify(x,(_,v)=>v&&typeof v==='object'&&!Array.isArray(v)?Object.fromEntries(Object.keys(v).sort().map(k=>[k,v[k]])):v)).digest('hex'),candidate:x}));";
 await docker([...commonArgs(reader,run,false),'--mount',`type=volume,source=${stateVolume},target=/state,readonly,volume-nocopy`,image,'node','-e',program,ack.file]);await docker(['start',reader]);assert.equal(Number(await docker(['wait',reader])),0);report.durable=JSON.parse(await docker(['logs',reader]));assert.equal(report.durable.digest,ack.digest);assert.deepEqual(report.durable.candidate,candidate);
 report.result='passed';
}catch(error:any){report.error=String(error.stack??error);process.exitCode=1;report.failures=[];for(const id of ids)try{report.failures.push({name:id,state:(await inspect(id)).State,logs:await docker(['logs',id])});}catch{}}
finally{
 const errors:string[]=[];for(const id of ids)try{const s=await inspect(id);assert.equal(s.Config.Labels[LABEL],run);await docker(['rm','--force',id]);}catch{errors.push(id);}for(const v of volumes)try{const s=JSON.parse(await docker(['volume','inspect',v]))[0];assert.equal(s.Labels[LABEL],run);await docker(['volume','rm',v]);}catch{errors.push(v);}
 for(const path of [staging,configuration])if(path)await rm(path,{recursive:true,force:true});report.cleanup=!errors.length;report.cleanupErrors=errors;if(!report.cleanup)process.exitCode=1;
 const redact=(text:string)=>text.replaceAll(repository,'<gaffer-source>').replaceAll(source!,'<public-router-source>');await writeFile(join(root,'run.json'),redact(JSON.stringify(report,null,2))+'\n');await writeFile(join(root,'commands.json'),redact(JSON.stringify(commands,null,2))+'\n');console.log(JSON.stringify({result:report.result,cleanup:report.cleanup,path:join(root,'run.json'),error:report.error}));
}
