// Attach-only trusted consumer. It never prepares, starts, resets or restarts a scope.
import assert from 'node:assert/strict';
import { execFile,spawn } from 'node:child_process';
import { promisify } from 'node:util';
import { readFile,writeFile,mkdir,mkdtemp,lstat,rm } from 'node:fs/promises';
import { resolve,dirname,join } from 'node:path';
import { randomBytes } from 'node:crypto';
import { setTimeout as delay } from 'node:timers/promises';
import { digest,settingsDigest } from '../harness/native/client.ts';
import { createFixture } from '../harness/native/fixture.ts';
import { workerArgs,inspectWorker } from '../harness/native/isolation.ts';
import { LABEL } from '../router-boundary-bridge/isolation.ts';
import { persistImmutable } from '../native-evaluation/durable-records.mjs';
import { stageContinuity } from './staging.ts';
import { qualify,qualifyPair } from './evidence.ts';
import { FIXTURE,FIRST,SECOND,TRANSITION } from './constants.ts';
const execute=promisify(execFile),root=resolve(import.meta.dirname,'../..'),context=process.env.GAFFER_DOCKER_CONTEXT??'desktop-linux';
export async function consume(recordArgument:string,outputArgument:string,binary:string) {
 const recordPath=resolve(recordArgument),output=resolve(outputArgument);assert(binary?.startsWith('/'),'pinned binary required');
 for(const directory of new Set([dirname(recordPath),dirname(output)])){const s=await lstat(directory);assert(s.isDirectory()&&!s.isSymbolicLink()&&s.uid===process.getuid?.()&&(s.mode&0o077)===0,'private directories required');}
 const inputState=await lstat(recordPath);assert(inputState.isFile()&&!inputState.isSymbolicLink()&&inputState.uid===process.getuid?.()&&(inputState.mode&0o077)===0,'private record required');
 const record=JSON.parse(await readFile(recordPath,'utf8'));assert.equal(record.phase,'started','existing started scope required');
 const run='continuity-consumer-'+randomBytes(6).toString('hex'),staging=await mkdtemp(join(dirname(output),run+'-')),workers=new Set<string>(),image='gaffer-router-extension-deps:0.5.75-locked';
 let stopRequested=false,retainResources=false;const onStop=()=>{stopRequested=true;for(const worker of workers)void docker(['stop','--timeout','1',worker]).catch(()=>{});};for(const signal of ['SIGTERM','SIGINT'] as const)process.on(signal,onStop);
 const report:any={schema:1,run,staging,fixture:FIXTURE,result:'pending',issueComplete:false,scopeCreated:false,workers:[],cleanup:false};
 const save=async()=>{try{const saved=persistImmutable(dirname(output),'continuity-consumer',report);await writeFile(output,JSON.stringify({snapshot:saved.file,digest:saved.digest,result:report.result})+'\n',{mode:0o600});}catch(error){retainResources=true;report.artifactDurability='unavailable_after_storage_failure_and_stop';report.retainedResources=true;throw error;}};
 async function cli(command:string,argument?:string){assert(!stopRequested||['inspect','evidence'].includes(command),'consumer stopped');assert(['inspect','select','grant','evidence','continuity-transition'].includes(command));const x=await execute(process.execPath,[join(root,'experiments/native-evaluation/deployment.ts'),command,recordPath,...(argument?[argument]:[])],{timeout:15000,maxBuffer:4*1048576});return JSON.parse(x.stdout);}
 async function docker(args:string[],timeout=15000){const x=await execute('docker',['--context',context,...args],{timeout,maxBuffer:4*1048576});return(x.stdout+(args[0]==='logs'?x.stderr:'')).trim();}
 async function owned(name:string){const s=JSON.parse(await docker(['inspect',name]))[0];assert.equal(s.Config.Labels[LABEL],run);return s;}
 async function current(){assert(!stopRequested,'consumer stopped');const x=await cli('inspect');assert.equal(x.packetDigest,record.packet.packetDigest);assert.equal(x.current.scope?.state,'active');assert.equal(x.current.scope.phase,'initial');assert(!x.current.journal.reservations.length,'unresolved reservations');return x;}
 try{
  let inspected=await current();const manifest=inspected.packet.continuityFixture;assert(manifest&&manifest.fixture===FIXTURE&&manifest.settingsDigest===settingsDigest('ask'),'reviewed fixture required');
  assert(record.sourceManifest&&digest(record.sourceManifest.files)===inspected.selectedPolicy.native.deployment.overlay&&digest(record.sourceManifest.runtime)===inspected.selectedPolicy.native.deployment.runtime,'source manifest required');
  const staged=await stageContinuity(root,join(staging,'client'),binary);for(const [path,hash]of Object.entries(staged.hashes))assert.equal(hash,record.sourceManifest.files[path],'staging differs from reviewed packet');
  report.staging=staged;report.configuredEvidence=inspected.selectedPolicy.native.evidence;report.packetDigest=inspected.packetDigest;
  if(digest(inspected.selectedPolicy.native)!==manifest.profileDigest){await cli('select',manifest.profileDigest);inspected=await current();}
  const policy=inspected.selectedPolicy;assert.equal(digest(policy.native),manifest.profileDigest);assert.equal(policy.native.protocol,'opencode-1.18.30-responses-apply-patch-v1');
  assert(inspected.current.scope.spent<=8&&inspected.current.scope.deadline-Date.now()>=120000,'insufficient remaining initial envelope');
  const baseline=structuredClone(inspected.current.scope),baseSHA=createFixture(join(staging,'base'));report.scopeBefore=baseline;
  for(const attemptId of [FIRST,SECOND]){
   inspected=await current();for(const field of ['id','boot','started','deadline','spec'])assert.equal(inspected.current.scope[field],baseline[field]);assert.equal(digest(inspected.selectedPolicy),digest(policy));
   assert(inspected.current.scope.spent<10&&inspected.current.scope.deadline-Date.now()>=45000,'initial envelope too short');
   const binding={attemptId,grantId:attemptId+'_grant',taskId:attemptId+'_task',leaseId:attemptId+'_lease',fence:1,role:'worker',routerId:policy.routerId,routeId:policy.routeId,revision:policy.revision,epoch:policy.epoch,expiresAt:Date.now()+45000,leaseExpiresAt:Date.now()+45000,native:{profileDigest:digest(policy.native),scopeId:policy.native.scope.id,authorizationDigest:policy.native.scope.authorizationDigest}};
   const bindingPath=join(staging,attemptId+'.json');await writeFile(bindingPath,JSON.stringify(binding),{mode:0o600});const grant=await cli('grant',bindingPath);
   const request={schema:1 as const,fixture:FIXTURE as typeof FIXTURE,baseSHA,settingsDigest:settingsDigest('ask'),bindingDigest:digest(binding),token:grant.token,wallMs:30000,outputBytes:262144};
   const worker=run+'-'+attemptId;workers.add(worker);const args=workerArgs(worker,run);args.push('--interactive','--mount',`type=bind,source=${join(staging,'client')},target=/fixture,readonly`,'--mount',`type=volume,source=${record.inferenceVolume},target=/router,readonly,volume-nocopy`,image,'node','/fixture/experiments/router-continuity/worker.ts');await docker(args);
   const state=await owned(worker);assert.equal(state.Image,record.runtime.image);const entry:any={container:worker,binding,measured:inspectWorker(state,image,run,record.inferenceVolume,join(staging,'client'))};report.workers.push(entry);await save();
   const child=spawn('docker',['--context',context,'start','--attach','--interactive',worker],{stdio:['pipe','pipe','pipe']});child.stdout.resume();let stderr='';child.stderr.on('data',b=>{if(stderr.length<262144)stderr+=b.toString();});child.stdin.on('error',()=>{});child.stdin.end(JSON.stringify(request));const exited=new Promise(resolve=>child.once('close',(code,signal)=>resolve({code,signal})));
   const watchdog=setTimeout(()=>{void docker(['kill',worker]).catch(()=>{});child.kill('SIGKILL');},42000);
   try{
    const until=Date.now()+40000;while(Date.now()<until){const s=await owned(worker);if(s.State.Status==='created'){await delay(50);continue;}const lines=(await docker(['logs',worker])).split('\n');for(const line of lines){try{const x=JSON.parse(line);if(x.event==='continuity_worker_observation')entry.observation=x;}catch{}}if(entry.observation)break;if(!s.State.Running)throw Error('worker exited without observation');await delay(50);}assert(entry.observation,'worker observation deadline');
    entry.observedArtifact=JSON.parse(await docker(['exec',worker,'node','/fixture/experiments/harness/native/inspect-artifact.ts',baseSHA]));entry.evidence=await cli('evidence',attemptId);await save();entry.qualified=qualify({policy,binding,request,observation:entry.observation,observedArtifact:entry.observedArtifact,evidence:entry.evidence});await save();
   }finally{clearTimeout(watchdog);if((await owned(worker)).State.Running)await docker(['stop','--timeout','1',worker]);entry.exited=await exited;entry.stderr=stderr;try{entry.finalLogs=await docker(['logs',worker]);if(!entry.observedArtifact)entry.artifactObservation='unavailable_after_stop';}catch{retainResources=true;entry.finalLogsUnavailable=true;}assert.equal((await owned(worker)).State.Pid,0);await save();}
   if(attemptId===FIRST){entry.transition=await cli('continuity-transition',TRANSITION);report.transition=entry.transition;await save();}
  }
  const verified=qualifyPair(report.workers[0].qualified,report.workers[1].qualified,report.transition);
  // Facts, not an automatic upgrade from a configured profile to live acceptance.
  const {evidence:_syntheticLabel,live:_live,...facts}=verified;report.verifiedPair=facts;report.scopeAfter=(await current()).current.scope;report.result='passed';await save();
 }catch(error:any){report.result='failed';report.error=String(error.message??error);for(const entry of report.workers)if(!entry.evidence)try{entry.evidence=await cli('evidence',entry.binding.attemptId);}catch{entry.evidenceUnavailable=true;}await save();throw error;}
 finally{if(report.result!=='passed'){try{await execute(process.execPath,[join(root,'experiments/native-evaluation/deployment.ts'),'stop',recordPath],{timeout:15000,maxBuffer:1048576});report.sharedDeploymentStopped=true;}catch{report.sharedDeploymentStopped=false;await Promise.allSettled(record.containers.map(async(name:string)=>{const state=JSON.parse(await docker(['inspect',name]))[0];assert.equal(state.Config.Labels['dev.gaffer.native-deployment'],record.run);if(state.State.Running)await docker(['kill',name]);assert.equal(JSON.parse(await docker(['inspect',name]))[0].State.Pid,0);}));}}for(const worker of workers)try{const state=await owned(worker);if(state.State.Running)await docker(['stop','--timeout','1',worker]);if(!retainResources){await docker(['rm','--force',worker]);workers.delete(worker);}}catch{}report.cleanup=!workers.size;if(!report.cleanup)report.result='failed';report.retainedContainers=[...workers];try{await save();}finally{if(report.cleanup&&!retainResources)await rm(staging,{recursive:true});for(const signal of ['SIGTERM','SIGINT'] as const)process.off(signal,onStop);}}
 if(report.result!=='passed')throw Error('continuity_consumer_failed_or_cleanup_incomplete');
 return {result:report.result,output,scopeCreated:false};
}
if(process.argv[1]&&resolve(process.argv[1])===resolve(import.meta.filename))console.log(JSON.stringify(await consume(process.argv[2],process.argv[3],process.env.GAFFER_OPENCODE_BINARY!)));
