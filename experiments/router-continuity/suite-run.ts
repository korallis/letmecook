// Trusted single coordinator. Consumer routines only attach to the started scope.
import assert from 'node:assert/strict';
import { execFile,spawn } from 'node:child_process';
import { promisify } from 'node:util';
import { readFile,writeFile,open,lstat,mkdir,rename,rm } from 'node:fs/promises';
import { resolve,join,dirname } from 'node:path';
import { randomBytes } from 'node:crypto';
import { setTimeout as delay } from 'node:timers/promises';
import { digest,settingsDigest } from '../harness/native/client.ts';
import { createFixture } from '../harness/native/fixture.ts';
import { prepareRun } from '../harness/native/admission.ts';
import { BRIEF } from '../harness/native/run.ts';
import { candidateEnvelope,acknowledgeCandidate } from '../harness/native/evidence.ts';
import { workerArgs,inspectWorker } from '../harness/native/isolation.ts';
import { commonArgs,inspectProfile,LABEL } from '../router-boundary-bridge/isolation.ts';
import { nativePlannerCandidate,acknowledgeNativePlanner } from '../planner-probe/native-evidence.ts';
import { validateNativeRouterPolicy,assertNativeBinding } from '../inference-boundary/native-policy.ts';
import { INITIAL_SUITE,SUITE_PHASES,phaseConsumer,phasePlan } from '../router-authority-extension/overlay/initial-suite.mjs';
import { persistImmutable } from '../native-evaluation/durable-records.mjs';
import { verifySuiteStages,treeHashes } from './suite-staging.ts';
import { suiteManifest } from './suite-profile.ts';
import { qualify,qualifyPair } from './evidence.ts';
import { FIXTURE,TRANSITION } from './constants.ts';
const execute=promisify(execFile),root=resolve(import.meta.dirname,'../..'),context=process.env.GAFFER_DOCKER_CONTEXT??'desktop-linux',image='gaffer-router-extension-deps:0.5.75-locked';
export async function runSuite(recordArgument:string,packetDigest:string,outputArgument:string){
 const recordPath=resolve(recordArgument),output=resolve(outputArgument);for(const d of new Set([dirname(recordPath),dirname(output)])){const s=await lstat(d);assert(s.isDirectory()&&!s.isSymbolicLink()&&s.uid===process.getuid?.()&&(s.mode&0o077)===0,'private_suite_directory');}
 const recordStat=await lstat(recordPath);assert(recordStat.isFile()&&!recordStat.isSymbolicLink()&&recordStat.uid===process.getuid?.()&&(recordStat.mode&0o077)===0);const record=JSON.parse(await readFile(recordPath,'utf8'));assert.equal(record.phase,'prepared','one prepared deployment required');
 const lock=await open(recordPath+'.suite-run','wx',0o600);await lock.writeFile(JSON.stringify({packetDigest,variant:INITIAL_SUITE})+'\n');await lock.sync();await lock.close();
 const run='initial-suite-'+randomBytes(6).toString('hex'),workers=new Set<string>(),work=output+'.work';await mkdir(work,{mode:0o700});
 let stopped=false,retainResources=false,scope:any;const report:any={schema:1,run,work,staging:record.suiteStage,variant:INITIAL_SUITE,result:'failed',issueComplete:false,packetDigest,phases:[],scopeCreated:false,cleanup:false};
 async function docker(args:string[],timeout=15000){const x=await execute('docker',['--context',context,...args],{timeout,maxBuffer:8*1048576});return(x.stdout+(args[0]==='logs'?x.stderr:'')).trim();}
 async function owned(name:string){const s=JSON.parse(await docker(['inspect',name]))[0];assert.equal(s.Config.Labels[LABEL],run);return s;}
 async function fence(){stopped=true;const ids=new Set([...record.containers,...record.transient]);let latest;try{latest=JSON.parse(await readFile(recordPath,'utf8'));if(latest.run===record.run)for(const id of [...latest.containers,...latest.transient])ids.add(id);}catch{}const checks=await Promise.allSettled([...ids].map(async name=>{const s=JSON.parse(await docker(['inspect',String(name)]))[0];assert.equal(s.Config.Labels['dev.gaffer.native-deployment'],record.run);if(s.State.Running)await docker(['kill',String(name)]);assert.equal(JSON.parse(await docker(['inspect',String(name)]))[0].State.Pid,0);}));report.fencing=checks.map(x=>x.status);}
 async function save(){try{const saved=persistImmutable(dirname(output),'initial-suite',report),temporary=output+'.'+randomBytes(8).toString('hex')+'.next';const f=await open(temporary,'wx',0o600);try{await f.writeFile(JSON.stringify({snapshot:saved.file,digest:saved.digest,result:report.result})+'\n');await f.sync();}finally{await f.close();}await rename(temporary,output);const d=await open(dirname(output),'r');try{await d.sync();}finally{await d.close();}}catch(error){retainResources=true;report.retainedResources=true;report.artifactDurability='unavailable_after_storage_failure_and_stop';throw error;}}
 async function cli(command:string,arg?:string){assert(!stopped||['inspect','evidence','stop'].includes(command),'suite_stopped');const x=await execute(process.execPath,[join(root,'experiments/native-evaluation/deployment.ts'),command,recordPath,...(arg?[arg]:[])],{timeout:15000,maxBuffer:8*1048576});return JSON.parse(x.stdout);}
 const onStop=()=>{stopped=true;for(const w of workers)void docker(['stop','--timeout','1',w]).catch(()=>{});void fence();};for(const signal of ['SIGTERM','SIGINT']as const)process.on(signal,onStop);
 async function current(){assert(!stopped);const x=await cli('inspect');assert.equal(x.packetDigest,packetDigest);assert.equal(digest(x.packet),packetDigest);assert.equal(x.current.scope.state,'active');for(const k of ['id','boot','started','deadline','spec'])assert.equal(x.current.scope[k],scope[k]);assert.equal(x.current.journal.reservations.length,0,'suite_unknown_original');return x;}
 async function candidate(value:any){const p=join(work,'candidate-'+randomBytes(5).toString('hex')+'.json');await writeFile(p,JSON.stringify(value),{flag:'wx',mode:0o600});return cli('candidate',p);}
 async function attached(phase:string,policy:any,binding:any,request:any,plan:any){
  const worker=run+'-'+phase;workers.add(worker);const consumer=phaseConsumer(phase),stage=join(record.suiteStage,consumer);const args=consumer==='planner'?commonArgs(worker,run,false):workerArgs(worker,run);args.push('--interactive','--mount',`type=bind,source=${stage},target=${consumer==='planner'?'/consumer':'/fixture'},readonly`,'--mount',`type=volume,source=${record.inferenceVolume},target=/router,readonly,volume-nocopy`,image,'node',consumer==='planner'?'/consumer/experiments/planner-probe/native-consumer.ts':consumer==='worker'?'/fixture/experiments/harness/native/worker.ts':'/fixture/experiments/router-continuity/worker.ts');await docker(args);
  const state=await owned(worker);assert.equal(state.Image,record.runtime.image);const entry:any={container:worker,phase,binding,plan,policy,measured:consumer==='planner'?inspectProfile(state,image,run,false,record.inferenceVolume,{'/consumer':stage}):inspectWorker(state,image,run,record.inferenceVolume,stage)};report.phases.push(entry);await save();
  assert(Date.now()<plan.sessionDeadline,'phase_deadline_before_start');const child=spawn('docker',['--context',context,'start','--attach','--interactive',worker],{stdio:['pipe','pipe','pipe']});child.stdout.resume();let stderr='';child.stderr.on('data',b=>{if(stderr.length<262144)stderr+=b;});child.stdin.on('error',()=>{});child.stdin.end(JSON.stringify(request));const exited=new Promise(r=>child.once('close',(code,signal)=>r({code,signal})));const timer=setTimeout(()=>{entry.deadlineExpired=true;void docker(['stop','--timeout','1',worker]).catch(()=>{});},Math.max(0,plan.sessionDeadline-Date.now()));
  try{
   while(Date.now()<plan.grantDeadline){assert(!stopped,'suite_stopped');const state=(await owned(worker)).State;if(state.Status==='created'){await delay(40);continue;}const lines=(await docker(['logs',worker])).split('\n');for(const line of lines)try{const x=JSON.parse(line);if(consumer==='planner'?x.result?.outcome: x.event===(consumer==='worker'?'native_worker_observation':'continuity_worker_observation'))entry.observation=x;}catch{}if(entry.observation)break;if(!state.Running)throw Error('phase_exited_without_observation');await delay(40);}assert(entry.observation,'phase_observation_deadline');assert(!entry.deadlineExpired,'phase_deadline_unknown');
   if(consumer!=='planner')entry.observedArtifact=JSON.parse(await docker(['exec',worker,'node','/fixture/experiments/harness/native/inspect-artifact.ts',request.baseSHA]));
   entry.evidence=await cli('evidence',binding.attemptId);await save();assert(Date.now()<plan.grantDeadline,'acknowledgement_deadline');
   if(consumer==='worker'){const e=entry.evidence;entry.candidate=candidateEnvelope({policy,packetDigest,scope:e.scope,binding,request,result:entry.observation.result,requests:entry.observation.requests,decisions:e.decisions,receipts:e.receipts,pendingReservations:e.reservations,durableDecisionTimes:e.decisionTimes,observedArtifact:entry.observedArtifact});entry.ack=await candidate(entry.candidate);entry.qualified=acknowledgeCandidate(entry.candidate,entry.ack,(await cli('evidence',binding.attemptId)).artifacts);}
   else if(consumer==='planner'){entry.candidate=nativePlannerCandidate(entry.observation.result,entry.evidence,binding);entry.ack=await candidate(entry.candidate);entry.qualified=acknowledgeNativePlanner(entry.candidate,entry.ack,(await cli('evidence',binding.attemptId)).artifacts);}
   else entry.qualified=qualify({policy,binding,request,observation:entry.observation,observedArtifact:entry.observedArtifact,evidence:entry.evidence});await save();
  }finally{clearTimeout(timer);if((await owned(worker)).State.Running)await docker(['stop','--timeout','1',worker]);entry.exit=await exited;entry.stderr=stderr;assert.equal((await owned(worker)).State.Pid,0);if(!entry.evidence)try{entry.evidence=await cli('evidence',binding.attemptId);}catch{entry.evidenceUnavailable=true;}await save();}
  return entry;
 }
 try{
  const inspected=await cli('inspect');assert.equal(inspected.packetDigest,packetDigest);assert.equal(digest(inspected.packet),packetDigest);assert.equal(inspected.current.scope,null);const declaration=inspected.packet.initialSuite;assert(declaration,'prepared_suite_required');const {profiles:_resolved,sessions:_sessions,acknowledgementMarginMs:_ack,stopMarginMs:_stop,...input}=declaration;assert.deepEqual(suiteManifest(input,inspected.packet.profiles),declaration);assert.equal(digest(record.sourceManifest.files),inspected.selectedPolicy.native.deployment.overlay);assert.equal(digest(record.sourceManifest.runtime),inspected.selectedPolicy.native.deployment.runtime);await verifySuiteStages(record.suiteStage,declaration.stages);
  // Recheck prepared code/dependency bytes before the only start.
  for(const [path,hash]of Object.entries(record.sourceManifest.files))assert.equal((await import('node:crypto')).createHash('sha256').update(await readFile(join(root,path))).digest('hex'),hash,'suite_source_changed');
  report.declaration=declaration;report.configuredEvidence=inspected.selectedPolicy.evidence;report.sourceManifest=record.sourceManifest;await save();scope=(await cli('start',packetDigest)).scope;report.scopeCreated=true;report.scopeBefore=scope;await save();
  const baseSHA=createFixture(join(work,'base'));
  for(const phase of SUITE_PHASES){let x=await current();const consumer=phaseConsumer(phase),selected=declaration.profiles[consumer];if(digest(x.selectedPolicy.native)!==selected){await cli('select',selected);x=await current();}const policy=validateNativeRouterPolicy(x.selectedPolicy);assert.equal(digest(policy.native),selected);await verifySuiteStages(record.suiteStage,declaration.stages);const plan=phasePlan(x.current.scope,phase),binding:any={attemptId:phase,grantId:phase+'_grant',taskId:phase+'_task',leaseId:phase+'_lease',fence:1,role:consumer==='planner'?'planner':'worker',routerId:policy.routerId,routeId:policy.routeId,revision:policy.revision,epoch:policy.epoch,expiresAt:plan.grantDeadline,leaseExpiresAt:plan.grantDeadline,native:{profileDigest:selected,scopeId:policy.native.scope.id,authorizationDigest:policy.native.scope.authorizationDigest}};assertNativeBinding(binding,policy);
   const file=join(work,phase+'.json');await writeFile(file,JSON.stringify(binding),{flag:'wx',mode:0o600});const grant=await cli('grant',file),tag={timing:INITIAL_SUITE as 'initial-suite-v1',packetDigest,profileDigest:selected,sessionDeadline:plan.sessionDeadline};let request:any;
   if(consumer==='worker')request=prepareRun(policy,binding,{...tag,baseSHA,brief:BRIEF,approval:'allow',limits:{wallMs:plan.wallMs,outputBytes:262144},token:grant.token});else if(consumer==='planner')request={binding,policy,token:grant.token,suite:tag};else request={schema:2,fixture:FIXTURE,...tag,baseSHA,settingsDigest:settingsDigest('ask'),bindingDigest:digest(binding),wallMs:plan.wallMs,outputBytes:262144,token:grant.token};
   await attached(phase,policy,binding,request,plan);x=await current();if(phase==='continuity_a'){report.transition=await cli('continuity-transition',TRANSITION);await save();}report.scopeAfter=x.current.scope;
  }
  report.continuity=qualifyPair(report.phases[2].qualified,report.phases[3].qualified,report.transition);assert.equal(report.scopeAfter.spent-scope.spent,6);report.result='passed';await save();
 }catch(error:any){report.result='failed';report.error=String(error.stack??error);try{await save();}catch{report.reportPersistenceFailed=true;}throw error;}
 finally{
  // Report failure cannot suppress independent physical stop.
  try{report.stop=await cli('stop');}catch(error:any){report.stopError=String(error.message??error);report.result='failed';await fence();}
  for(const worker of workers)try{const state=await owned(worker);if(state.State.Running)await docker(['stop','--timeout','1',worker]);if(!retainResources){await docker(['rm','--force',worker]);workers.delete(worker);}}catch{}report.cleanup=!workers.size;report.retainedContainers=[...workers];if(!report.cleanup)report.result='failed';try{await save();}finally{for(const signal of ['SIGTERM','SIGINT']as const)process.off(signal,onStop);if(report.cleanup&&!retainResources)await rm(work,{recursive:true});}if(report.result!=='passed')throw Error('initial_suite_failed_or_finalization_incomplete');
 }
 return {result:report.result,output};
}
if(process.argv[1]&&resolve(process.argv[1])===resolve(import.meta.filename))console.log(JSON.stringify(await runSuite(process.argv[2],process.argv[3],process.argv[4])));
