// Regression fixture adapted from the independent PR64 coordinator probe.
// Runs the coordinator with in-memory Docker/control/check responses.
// No Docker daemon, provider, OpenCode process, or live state is accessed.
// Real /tmp persistence, frozen Git registration, snapshots, collector, and control
// flow are exercised. Captured transcript verification is supplied as a test double;
// capture.test.ts runs the real verifier on both unchanged historical captures.
import assert from 'node:assert/strict';
import { mock } from 'node:test';
import * as fs from 'node:fs/promises';
import * as cp from 'node:child_process';
import { promisify } from 'node:util';
import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';
import { join, dirname, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { createHash } from 'node:crypto';
const root=resolve(import.meta.dirname,'../..'), url=p=>pathToFileURL(join(root,p)).href;
const mode=process.argv[2]??'control';
const stopSignal=process.argv[3]??'SIGINT';
const base=await fs.realpath(await fs.mkdtemp('/tmp/gaffer-coordinator-')),parent=join(base,'new-parent');await fs.mkdir(parent);const output=join(parent,'run');
const capture=JSON.parse(await fs.readFile(join(root,'docs/evidence/baseline-native-case01-run.json'),'utf8')).cases[0];
const j=capture.records[capture.journal.ref],get=ref=>structuredClone(capture.records[ref.ref]);
const verified=get(j.transcript),capturedInput=get(j.verificationInput),capturedObservation=get(j.observation);
const log=[],containers=new Map(),volumes=new Map();let run,scope,packet,policy=get(j.packet).selectedPolicy,checks=0,signalEmitted=false,finalSave=false;
const hash=v=>createHash('sha256').update(v).digest('hex');
const source=join(base,'source');await fs.mkdir(join(source,'open-sse/config'),{recursive:true});
const instructions="export const CODEX_DEFAULT_INSTRUCTIONS='Independent synthetic coordinator fixture';\n";
await fs.writeFile(join(source,'open-sse/config/codexInstructions.js'),instructions);
await fs.writeFile(join(source,'package.json'),'{"type":"module"}');
const tarDir=join(base,'tar');await fs.mkdir(tarDir);
for(const f of get(get(j.candidate).candidate.bytes).files){await fs.mkdir(dirname(join(tarDir,f.path)),{recursive:true});await fs.writeFile(join(tarDir,f.path),Buffer.from(f.contentBase64,'base64'),{mode:f.mode});await fs.chmod(join(tarDir,f.path),f.mode);}
const tar=cp.execFileSync('tar',['--format=ustar','-cf','-','.'],{cwd:tarDir,maxBuffer:1048576});
const open=async(...args)=>{const handle=await fs.open(...args);return new Proxy(handle,{get(target,key){if(key==='sync')return async()=>{const path=String(args[0]);log.push({event:'sync',path});
    if((mode==='sync-run-failure'&&path===output)||(mode==='sync-parent-failure'&&path===parent)||(mode==='sync-ancestor-failure'&&path===base))throw Error('injected ancestry sync failure');
    await target.sync();
    if(mode==='ack-stop'&&path.includes('/.baseline-ack-')&&!signalEmitted)emitStop();
    if(mode==='final-save-stop'&&finalSave&&path.includes('/latest-')&&!signalEmitted)emitStop();
    if(mode==='export-stop'&&path.endsWith('public-summary.json')&&!signalEmitted)emitStop();
    return;
};if(key==='read')return async(...readArgs)=>{const value=await target.read(...readArgs);if(mode==='readback-stop'&&String(args[0]).includes('/baseline-ack-')&&!signalEmitted)emitStop();return value;};const v=Reflect.get(target,key,target);return typeof v==='function'?v.bind(target):v;}});};
mock.module('node:fs/promises',{exports:{...fs,open,readFile:async(...args)=>{
  const bytes=await fs.readFile(...args);
  if(String(args[0])===join(root,'experiments/router-authority-extension/source-lock.json')){const lock=JSON.parse(bytes);lock.files['open-sse/config/codexInstructions.js']=hash(instructions);return typeof bytes==='string'?JSON.stringify(lock):Buffer.from(JSON.stringify(lock));}
  return bytes;
}}});
const artifacts=await import(url('experiments/baseline/artifacts/index.ts'));
const {digest}=await import(url('experiments/baseline/validation.ts'));
const {IMAGE}=await import(url('experiments/baseline/checks/container.ts'));

const emitStop=()=>{assert(!signalEmitted);signalEmitted=true;log.push({event:stopSignal});process.emit(stopSignal);};
const realExec=promisify(cp.execFile);
async function fakeExec(command,args,options){
  if(command==='git'){
    if(args[0]==='show')return {stdout:capturedInput.expected.context.split('\n\nYou are powered')[0],stderr:''};
    const result=await realExec(command,args,options);
    if(mode==='pre-scope-stop'&&args.includes('commit'))emitStop();
    return result;
  }
  assert.equal(command,'docker');args=args.slice(args[0]==='--context'?2:0);
  log.push({event:'docker',args,stopped:signalEmitted});
  let result='';
  const name=args.at(-1);
  if(args[0]==='version')result=JSON.stringify({Server:{}});
  else if(args[0]==='info')result='{}';
  else if(args[0]==='image')result=JSON.stringify([{Id:IMAGE}]);
  else if(args[0]==='volume'){
    if(args[1]==='create'){const owner=args[args.indexOf('--label')+1].split('=')[1];volumes.set(name,{Labels:{owner}});result=name;}
    else if(args[1]==='inspect')result=JSON.stringify([volumes.get(name)]);
    else if(args[1]==='rm'){log.push({event:'repository-destroy',stopped:signalEmitted,parentSynced:log.some(x=>x.event==='sync'&&x.path===output),outputParentSynced:log.some(x=>x.event==='sync'&&x.path===parent),ancestorSynced:log.some(x=>x.event==='sync'&&x.path===base)});volumes.delete(name);result=name;}
  }else if(args[0]==='create'){
    const name=args[args.indexOf('--name')+1],owner=args[args.indexOf('--label')+1].split('=')[1];run=owner;
    const mounts=[];for(let i=0;i<args.length;i++)if(args[i]==='--mount'){const opts=Object.fromEntries(args[++i].split(',').map(x=>x.split('=')));mounts.push({Type:opts.type,Name:opts.source,Source:opts.source,Destination:opts.target,RW:!Object.hasOwn(opts,'readonly')});}
    if(mode==='worker-create-stop'&&name.endsWith('-worker'))emitStop();
    containers.set(name,{Image:IMAGE,Config:{Labels:{owner}},Mounts:mounts,State:{Status:'created',Pid:0,Paused:false,Running:false}});result=name;
  }else if(args[0]==='inspect'){assert(containers.has(name),name);result=JSON.stringify([containers.get(name)]);}
  else if(args[0]==='start'){containers.get(name).State={Status:'running',Pid:1,Paused:false,Running:true};result=name;}
  else if(args[0]==='wait'){containers.get(name).State={Status:'exited',Pid:0,Paused:false,Running:false};result='0';}
  else if(args[0]==='pause')containers.get(name).State.Paused=true;
  else if(args[0]==='unpause')containers.get(name).State.Paused=false;
  else if(args[0]==='stop')containers.get(name).State={Status:'exited',Pid:0,Paused:false,Running:false};
  else if(args[0]==='rm'){if(mode==='cleanup-stop'&&name.endsWith('-worker'))emitStop();if(mode==='cleanup-unknown'&&name.endsWith('-gateway'))throw Error('injected removal uncertainty');containers.delete(name);}
  else if(args[0]==='logs'){
    if(name.endsWith('-gateway'))result=JSON.stringify({event:'gateway_ready'});
    else{const observation=structuredClone(capturedObservation);observation.startedAt=scope.started+1;observation.endedAt=Date.now();
      const invocationFile=(await fs.readdir(join(output,'evidence'))).find(n=>n.startsWith('invocation-'));
      const invocation=JSON.parse(await fs.readFile(join(output,'evidence',invocationFile),'utf8'));
      observation.invocationDigest=digest(invocation);for(const key of ['registrationDigest','packetDigest','bindingDigest','settingsDigest'])observation[key]=invocation[key];
      result=JSON.stringify(observation);}

  }else if(args[0]==='cp')return {stdout:tar,stderr:Buffer.alloc(0)};
  else assert.fail('unexpected docker '+args.join(' '));
  return {stdout:result,stderr:''};
}
const execFile=()=>assert.fail('unexpected callback execFile');execFile[promisify.custom]=fakeExec;
const spawn=(command,args)=>{log.push({event:'spawn',args,stopped:signalEmitted});const child=new EventEmitter();child.stdout=new PassThrough();child.stderr=new PassThrough();child.stdin=new PassThrough();child.kill=()=>true;queueMicrotask(()=>child.emit('close',0,null));return child;};
mock.module('node:child_process',{exports:{...cp,execFile,spawn}});
mock.module(url('experiments/isolation/profile.ts'),{exports:{assertRuntime(){}}});
mock.module(url('experiments/router-boundary-bridge/isolation.ts'),{exports:{LABEL:'owner',commonArgs:(name,run)=>['create','--name',name,'--label','owner='+run],inspectProfile:()=>({})}});
mock.module(url('experiments/harness/native/isolation.ts'),{exports:{workerArgs:(name,run)=>['create','--name',name,'--label','owner='+run],inspectWorker:()=>({})}});
mock.module(url('experiments/baseline/native/staging.ts'),{exports:{stageBaselineWorker:async()=>({hashes:{},files:[],binary:'synthetic'})}});
mock.module(url('experiments/baseline/native/transcript.ts'),{exports:{verifyBaselineTranscript:()=>structuredClone(verified)}});
mock.module(url('experiments/router-boundary-bridge/lifecycle.ts'),{exports:{inputProcess:async(command,args,text)=>{
  const message=JSON.parse(text);log.push({event:'control',command:message.command,stopped:signalEmitted});let body;
  if(message.command==='inspect'){
    if(!packet){const descriptor=JSON.parse(await fs.readFile(join(output,'inputs/baseline.json'),'utf8'));packet={packet:{baselineCase:descriptor},selectedPolicy:policy,current:{scope:null}};packet.packetDigest=digest(packet.packet);}
    body=structuredClone(packet);if(scope)body.current={scope:{spent:0},journal:{reservations:[]}};
  }else if(message.command==='start'){scope={id:policy.native.scope.id,started:Date.now(),deadline:Date.now()+900000};scope.deadline=scope.started+900000;body=scope;}
  else if(message.command==='grant'){body={token:'a'.repeat(64)};if(mode==='pre-worker-stop')emitStop();}
  else if(message.command==='evidence'){body=get(j.routerEvidence).evidence;Object.assign(body.scope,scope);if(mode==='evidence-stop')emitStop();}
  else if(message.command==='baseline-proof'){body=get(j.routerEvidence).physical;body.registrationDigest=digest(JSON.parse(await fs.readFile(join(output,'registration/registration.json'),'utf8')));}
  else if(message.command==='stop')body={};
  else assert.fail('unexpected control');
  return JSON.stringify({status:200,body});
}}});
mock.module(url('experiments/baseline/checks/index.ts'),{exports:{IMAGE,NODE:'/usr/local/bin/node',BINARY_DIGEST:'0f8949d1028f6d61506b2d5bc57e7e6fe893d7b1997509b7847294fc9c616584',ENVIRONMENT_DIGEST:get(j.registration).toolchains[0].environmentDigest,runCheck:async(job,store,signal)=>{
  const status=signal.aborted?'not-run':job.revision==='candidate'&&job.checkId==='pins'?'passed':'failed';
  const check={status,exitCode:status==='passed'?0:status==='failed'?1:null,snapshotDigest:job.snapshot.treeDigest,checkId:job.checkId,cleanup:true};
  const retained=get(j.checks.find(x=>x.revision===job.revision&&x.check.checkId===job.checkId).check.observation);
  Object.assign(retained,check,{revision:job.revision,registrationDigest:job.registrationDigest});
  const runtime=await artifacts.retainEvidence(store,'check-runtime',get(retained.runtime.evidence));retained.runtime.evidence=runtime;
  const observation=await artifacts.retainEvidence(store,'check-observation',retained);
  if(++checks===(mode==='base-check-stop'?1:4)&&['base-check-stop','late-stop'].includes(mode))emitStop();
  return {...check,observation};
}}});
mock.module(url('experiments/baseline/artifacts/index.ts'),{exports:{...artifacts,retainEvidence:async(store,kind,value)=>{
  if(kind==='baseline-ack'&&['missing-evidence','substituted-evidence'].includes(mode)){
    const missing=(await fs.readdir(store.directory)).filter(name=>/^(invocation|worker-observation|router-evidence|check-observation|transcript-input)-/.test(name));
    for(const name of missing) { if(mode==='missing-evidence')await fs.unlink(join(store.directory,name));else await fs.writeFile(join(store.directory,name),'{}'); }log.push({event:'deleted-evidence-before-ack',missing});
  }
  if(kind==='run-journal')finalSave=value.result==='passed'&&Array.isArray(value.cleanupResults);
  const ref=await artifacts.retainEvidence(store,kind,value);
  if(kind==='invocation'&&mode==='invocation-stop')emitStop();
  if(kind==='baseline-dataset'&&mode==='dataset-stop')emitStop();
  if(kind==='baseline-ack')log.push({event:'ack-retained',stopped:signalEmitted});
  return ref;
}}});
process.env.GAFFER_DOCKER_CONTEXT='synthetic-test-double';process.env.GAFFER_ROUTER_SOURCE=source;process.env.GAFFER_OPENCODE_SOURCE=source;process.env.GAFFER_OPENCODE_BINARY=join(base,'not-executed');
try{
  const {runCase}=await import(url('experiments/baseline/run-case.ts'));
  let result,error;try{result=await runCase(output,'two-requests');}catch(e){error=String(e);}
  const journals=(await fs.readdir(join(output,'evidence'))).filter(x=>x.startsWith('run-journal-'));
  const records=await Promise.all(journals.map(async name=>JSON.parse(await fs.readFile(join(output,'evidence',name),'utf8'))));
  const last=records.find(r=>Array.isArray(r.cleanupResults)&&(!signalEmitted||r.result==='failed'));
  const summary={mode,result,error,signalEmitted,scopeStartedAfterStop:log.some(x=>x.event==='control'&&x.command==='start'&&x.stopped),workerCreatedAfterStop:log.some(x=>x.event==='docker'&&x.args[0]==='create'&&x.args[x.args.indexOf('--name')+1]?.endsWith('-worker')&&x.stopped),checks,acknowledged:Boolean(last?.acknowledgement),repositoryDestroyed:last?.repositoryDestroyed??false,cleanup:last?.cleanup,parentSyncAtDestruction:log.find(x=>x.event==='repository-destroy'),deletedEvidence:log.find(x=>x.event==='deleted-evidence-before-ack'),output};
  assert.equal(summary.scopeStartedAfterStop,false);assert.equal(summary.workerCreatedAfterStop,false);
  assert(!log.some(x=>x.event==='spawn'&&x.stopped));assert(!log.some(x=>x.event==='repository-destroy'&&x.stopped));
  assert(!log.some(x=>x.event==='control'&&['start','grant'].includes(x.command)&&x.stopped));
  if(mode==='control') {
    assert.equal(result?.result,'passed',error);assert(summary.acknowledged);assert(summary.repositoryDestroyed);assert(summary.cleanup);
    assert(summary.parentSyncAtDestruction.parentSynced);assert(summary.parentSyncAtDestruction.outputParentSynced);assert(summary.parentSyncAtDestruction.ancestorSynced);
    assert(log.findIndex(x=>x.event==='sync'&&x.path===base)<log.findIndex(x=>x.event==='ack-retained'));
  } else {
    if(mode.endsWith('-stop')) {assert(signalEmitted);assert.match(error,/AbortError/);}
    if(mode.startsWith('sync-'))assert.match(error,/injected ancestry sync failure/);
    if(mode==='missing-evidence')assert.match(error,/ENOENT/);
    if(mode==='substituted-evidence')assert.match(error,/evidence_digest_mismatch/);
    assert(error||result?.result==='failed',JSON.stringify(summary));assert.equal(last.result,'failed');
    assert.equal(summary.repositoryDestroyed,mode==='final-save-stop',JSON.stringify(summary));
    assert.equal(summary.cleanup,mode!=='cleanup-unknown');
    if(!['cleanup-unknown','cleanup-stop','dataset-stop','export-stop','final-save-stop'].includes(mode))assert.equal(summary.acknowledged,false);
    if(['pre-scope-stop','pre-worker-stop','invocation-stop','worker-create-stop','base-check-stop'].includes(mode))assert(!log.some(x=>x.event==='spawn'));
  }
  console.log(JSON.stringify(summary,null,2));
}finally{mock.restoreAll();await fs.rm(base,{recursive:true,force:true});}
