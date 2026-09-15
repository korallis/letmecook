// One public synthetic case through the actual pinned worker and router.
import assert from 'node:assert/strict';
import { execFile, spawn } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdir, readFile, writeFile, readdir, open } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { createHash, randomBytes } from 'node:crypto';
import { setTimeout as delay } from 'node:timers/promises';
import { commonArgs, inspectProfile, LABEL } from '../router-boundary-bridge/isolation.ts';
import { inputProcess } from '../router-boundary-bridge/lifecycle.ts';
import { workerArgs, inspectWorker } from '../harness/native/isolation.ts';
import { assertRuntime } from '../isolation/profile.ts';
import { profile } from '../native-evaluation/fixtures.mjs';
import { digest, settingsDigest, pins } from '../harness/native/client.ts';
import { validateNativeRouterPolicy, assertNativeBinding } from '../inference-boundary/native-policy.ts';
import { prepareCase, promptFor, checkJob, rulesFor, WRITE_PATHS } from './case01.ts';
import { retainEvidence, readEvidence, readSnapshot, captureSnapshot, retainCandidate } from './artifacts/index.ts';
import { runCheck, IMAGE } from './checks/index.ts';
import { stageBaselineWorker } from './native/staging.ts';
import { validateWorkerInput, type WorkerInput } from './native/input.ts';
import { verifyBaselineTranscript } from './native/transcript.ts';
import { writeExport } from './collect.ts';
import { fixture } from './fixture.ts';
import type { CheckEvidence, EvidenceRef } from './execution-contract.ts';

const execute = promisify(execFile), root = resolve(import.meta.dirname, '../..');
const image = 'gaffer-router-extension-deps:0.5.75-locked';
const hash = (bytes: Uint8Array | string) => createHash('sha256').update(bytes).digest('hex');

export async function runCase(directory: string, scenario: 'two-requests' | 'three-requests') {
  assert(['two-requests','three-requests'].includes(scenario));
  const source = process.env.GAFFER_ROUTER_SOURCE, binary = process.env.GAFFER_OPENCODE_BINARY, opencodeSource = process.env.GAFFER_OPENCODE_SOURCE;
  assert(source?.startsWith('/') && binary?.startsWith('/') && opencodeSource?.startsWith('/'), 'absolute_public_sources_and_binary_required');
  const context = process.env.GAFFER_DOCKER_CONTEXT ?? (await execute('docker',['context','show'])).stdout.trim(), output = resolve(directory);
  await mkdir(output, { mode: 0o700 }); // Exclusive run directory; never resumes an unknown run.
  const store = { directory: join(output, 'evidence') }; await mkdir(store.directory, { mode: 0o700 });
  const inputs = join(output, 'inputs'); await mkdir(inputs, { mode: 0o755 });
  const run = 'baseline-' + randomBytes(6).toString('hex'), containers = new Set<string>(), volumes: string[] = [];
  const abort = new AbortController(), report: any = { schema: 1, run, scenario, live: false, realProviderCalled: false, issueComplete: false, result: 'failed', cleanup: false };
  const onStop = () => abort.abort(); for (const signal of ['SIGTERM','SIGINT'] as const) process.on(signal, onStop);
  async function docker(args: string[], timeout = 15000) {
    const result = await execute('docker', ['--context', context, ...args], { timeout, maxBuffer: 2 * 1048576 });
    return (result.stdout + (args[0] === 'logs' ? result.stderr : '')).trim();
  }
  async function owned(name: string) { const state = JSON.parse(await docker(['inspect', name]))[0]; assert.equal(state.Config.Labels[LABEL], run); return state; }
  async function remove(name: string) {
    const state = await owned(name); if (state.State.Paused) await docker(['unpause', name]);
    if (state.State.Running) await docker(['stop','--timeout','1',name]);
    assert.equal((await owned(name)).State.Pid, 0); await docker(['rm',name]); containers.delete(name);
  }
  let controlVolume = '', gateway = '', worker = '', scope: any, observation: any, registration: any, registrationRef: EvidenceRef, registrationCommit = '', candidate: EvidenceRef | null = null;
  let child: ReturnType<typeof spawn> | undefined, childDone: Promise<unknown> | undefined;
  async function control(message: unknown) {
    const name = run + '-ctl-' + randomBytes(3).toString('hex'); containers.add(name);
    const code = "const http=require('node:http');let body='';process.stdin.on('data',c=>body+=c);process.stdin.on('end',()=>{const q=http.request({socketPath:'/control/gateway.sock',path:'/control',method:'POST'},r=>{let text='';r.on('data',c=>text+=c);r.on('end',()=>console.log(JSON.stringify({status:r.statusCode,body:JSON.parse(text)})))});q.setTimeout(5000,()=>q.destroy());q.on('error',()=>process.exit(1));q.end(body)})";
    await docker([...commonArgs(name,run,false),'--interactive','--mount',`type=volume,source=${controlVolume},target=/control,readonly,volume-nocopy`,image,'node','-e',code]);
    try {
      const value = JSON.parse(await inputProcess('docker',['--context',context,'start','--attach','--interactive',name],JSON.stringify(message),new AbortController().signal,10000));
      assert.equal(value.status,200,'baseline_control_refused'); return value.body;
    } finally { await remove(name); }
  }
  async function waitEvent(name: string, event: string, deadline: number) {
    while (Date.now() < deadline) {
      abort.signal.throwIfAborted(); const state = (await owned(name)).State;
      for (const line of (await docker(['logs',name])).split('\n')) { let value; try { value = JSON.parse(line); } catch { continue; } if (value.event === event) return value; }
      assert(!['exited','dead'].includes(state.Status), 'container_exited_before_' + event); await delay(50);
    }
    throw Error('deadline_before_' + event);
  }
  async function save() {
    const ref = await retainEvidence(store,'run-journal',report);
    const file = await open(join(output,'latest-' + ref.sha256 + '.json'),'wx',0o600); try { await file.writeFile(JSON.stringify(ref)+'\n'); await file.sync(); } finally { await file.close(); }
    return ref;
  }
  try {
    const version = JSON.parse(await docker(['version','--format','{{json .}}'])), info = JSON.parse(await docker(['info','--format','{{json .}}'])); assertRuntime(version,info);
    const runtime = { server: version.Server, image: JSON.parse(await docker(['image','inspect',image]))[0].Id, node: 'v24.21.0', arch: 'arm64' }; assert.equal(runtime.image,IMAGE);
    const sourceCommit = (await execute('git',['rev-parse','HEAD'],{cwd:root})).stdout.trim(), sources: Record<string,string> = {};
    async function walk(directory: string) { for (const item of await readdir(join(root,directory),{withFileTypes:true})) { if (item.name === 'node_modules' || item.name === 'evidence') continue; const path=directory+'/'+item.name; assert(!item.isSymbolicLink()); if(item.isDirectory()) await walk(path); else if(/\.(ts|mjs|json|txt)$/.test(path)) sources[path]=hash(await readFile(join(root,path))); } }
    for(const directory of ['experiments/baseline','experiments/harness','experiments/native-evaluation','experiments/inference-boundary','experiments/router-authority-extension','experiments/router-boundary-bridge']) await walk(directory);
    report.sourceCommit=sourceCommit; report.sources=sources; report.runtime=runtime;
    const preparedCase = await prepareCase(store,sourceCommit,sources); registration=preparedCase.registration;
    const stage=join(output,'worker'); report.staging=await stageBaselineWorker(root,stage,binary!);
    const native=profile(); native.harness.settings=settingsDigest('allow'); native.toolPaths=WRITE_PATHS;
    native.deployment.id=run; native.deployment.overlay=digest(sources); native.deployment.runtime=digest(runtime); native.deployment.sourceLock=sources['experiments/router-authority-extension/source-lock.json'];
    native.deployment.isolation=digest({workerMemoryMiB:768,checkMemoryMiB:128,runtime}); native.authorization.caseRef='case01';
    native.scope={...native.scope,id:run,caseRef:'case01',phase:'baseline',authorizationDigest:digest(native.authorization),maxInferenceAttempts:32,elapsedMs:900000};
    native.local={...native.local,totalMs:120000,firstOutputMs:90000,idleMs:45000,attemptMs:900000};
    const config={settings:{requireApiKey:true,rtkEnabled:false,headroomEnabled:false,pxpipeEnabled:false,cavemanEnabled:false,ponytailEnabled:false,ccFilterNaming:false,capacityAdapter:Object.fromEntries(['vision','pdf','audioInput','videoInput'].map(k=>[k,{enabled:false,models:[]}]))},providerConnections:native.connections.map((c:any,i:number)=>({id:c.id,provider:'codex',authType:'oauth',name:'Synthetic',priority:i+1,isActive:true,accessToken:'synthetic_access_'+i,expiresAt:new Date(c.expiresAt).toISOString(),providerSpecificData:{chatgptAccountId:'synthetic_workspace_'+i}})),combos:[{id:'native_route',name:'gpt-6-astra',models:['cx/gpt-6-astra']}]};
    for(const [name,value] of Object.entries({'profile.json':native,'profiles.json':[native],'config.json':config,'source-manifest.json':{files:sources,runtime},'baseline.json':preparedCase.descriptor})) await writeFile(join(inputs,name),JSON.stringify(value),{mode:0o644,flag:'wx'});
    for(const suffix of ['state','router','control']) { const volume=run+'-'+suffix; volumes.push(volume); await docker(['volume','create','--label',LABEL+'='+run,volume]);
      const init=run+'-init-'+suffix;containers.add(init);await docker([...commonArgs(init,run,false),'--user','0:0','--cap-add','CHOWN','--mount',`type=volume,source=${volume},target=/volume,volume-nocopy`,image,'chown','1000:1000','/volume']);await docker(['start',init]);assert.equal(Number(await docker(['wait',init])),0);await remove(init); }
    const [stateVolume,routerVolume,controlName]=volumes;controlVolume=controlName;report.stateVolume=stateVolume;
    gateway=run+'-gateway';containers.add(gateway);const args=commonArgs(gateway,run,true),binds={'/gaffer':root,'/probe':join(root,'experiments/router-authority-extension'),'/router-source':source!,'/config':inputs,'/private':inputs};
    for(const [target,path]of Object.entries(binds))args.push('--mount',`type=bind,source=${path},target=${target},readonly`);
    for(const [target,volume]of [['/state',stateVolume],['/router',routerVolume],['/control',controlVolume]])args.push('--mount',`type=volume,source=${volume},target=${target},volume-nocopy`);
    args.push('--env','DATA_DIR=/state/router-db','--env','GAFFER_SYNTHETIC_NATIVE=1','--env','GAFFER_BASELINE_CASE='+scenario,image,'node','--experimental-loader','/gaffer/experiments/baseline/native/synthetic-loader.mjs','/gaffer/experiments/native-evaluation/bootstrap.mjs');await docker(args);
    const inspected=await owned(gateway); assert.equal(inspected.Image,IMAGE); report.gateway=inspectProfile({...inspected,Mounts:inspected.Mounts.filter((m:any)=>!['/state','/control'].includes(m.Destination))},image,run,true,routerVolume,binds);
    for(const [target,volume]of [['/state',stateVolume],['/control',controlVolume]])assert(inspected.Mounts.some((m:any)=>m.Type==='volume'&&m.Name===volume&&m.Destination===target&&m.RW));
    await docker(['start',gateway]);await waitEvent(gateway,'gateway_ready',Date.now()+10000);
    const packet=await control({command:'inspect'});assert.equal(digest(packet.packet),packet.packetDigest);assert.equal(packet.current.scope,null);assert.deepEqual(packet.packet.baselineCase,preparedCase.descriptor);
    const policy=validateNativeRouterPolicy(packet.selectedPolicy);registration.execution.packetDigest=packet.packetDigest;registration.execution.nativeProfileDigest=digest(policy.native);
    registrationRef=await retainEvidence(store,'registration',registration);report.registration=registrationRef;report.packet=await retainEvidence(store,'packet',packet);
    // Freeze the complete declaration in an isolated local Git repository before start.
    const frozen=join(output,'registration');await mkdir(frozen,{mode:0o700});await writeFile(join(frozen,'registration.json'),JSON.stringify(registration)+'\n',{flag:'wx',mode:0o600});
    const git=(argv:string[])=>execute('git',['-c','core.hooksPath=/dev/null','-c','commit.gpgsign=false','-c','user.name=Gaffer synthetic fixture','-c','user.email=synthetic@example.invalid',...argv],{cwd:frozen,env:{PATH:process.env.PATH,HOME:frozen,GIT_CONFIG_NOSYSTEM:'1',GIT_CONFIG_GLOBAL:'/dev/null'}});
    await git(['init']);await git(['add','registration.json']);await git(['commit','-m','Freeze synthetic baseline declaration']);registrationCommit=(await git(['rev-parse','HEAD'])).stdout.trim();report.registrationCommit=registrationCommit;
    scope=await control({command:'start',packetDigest:packet.packetDigest,registration});report.scopeStarted=scope;await save();
    const checks: {revision:'base'|'candidate';check:CheckEvidence}[]=[];
    for(const id of ['pins','audit'])checks.push({revision:'base',check:await runCheck(await checkJob(store,registrationRef,preparedCase.base,preparedCase.executables,id,'base',scope.started+60000),store,abort.signal)});
    assert(checks.every(x=>x.check.cleanup),'base_check_cleanup_unknown');assert(checks.every(x=>x.check.status==='failed'),'synthetic_base_failure_missing');
    const before=await control({command:'inspect'});assert.equal(before.current.scope.spent,0);assert.equal(before.current.journal.reservations.length,0);
    const workerDeadline=Math.min(Date.now()+600000,scope.deadline-240000),grantDeadline=workerDeadline+60000;
    const binding:any={attemptId:'case01_attempt',grantId:'case01_grant',taskId:'case01_task',leaseId:'case01_lease',fence:1,role:'worker',routerId:policy.routerId,routeId:policy.routeId,revision:policy.revision,epoch:policy.epoch,expiresAt:grantDeadline,leaseExpiresAt:grantDeadline,native:{profileDigest:digest(policy.native),scopeId:policy.native.scope.id,authorizationDigest:policy.native.scope.authorizationDigest}};assertNativeBinding(binding,policy);
    for(const [path,expected]of Object.entries(sources))assert.equal(hash(await readFile(join(root,path))),expected,'source_changed_before_dispatch');
    for(const [path,expected]of Object.entries(report.staging.hashes))assert.equal(hash(await readFile(join(stage,path))),expected,'worker_stage_changed');
    const grant=await control({command:'grant',binding});
    const request:WorkerInput={schema:1,kind:'synthetic-baseline-worker',caseId:'synthetic-case01',registrationDigest:digest(registration),packetDigest:packet.packetDigest,profileDigest:digest(policy.native),bindingDigest:digest(binding),settingsDigest:settingsDigest('allow'),baseTreeDigest:preparedCase.base.treeDigest,context:preparedCase.context,contextDigest:preparedCase.descriptor.contextDigest,prompt:promptFor(preparedCase.context),promptDigest:preparedCase.descriptor.promptDigest,files:preparedCase.files,token:grant.token,deadline:workerDeadline,outputBytes:262144};validateWorkerInput(request);
    const invocation=await retainEvidence(store,'invocation',{...request,token:'<scoped-grant>'});report.invocation=invocation;
    worker=run+'-worker';containers.add(worker);await docker([...workerArgs(worker,run),'--interactive','--mount',`type=bind,source=${stage},target=/fixture,readonly`,'--mount',`type=volume,source=${routerVolume},target=/router,readonly,volume-nocopy`,image,'node','/fixture/experiments/baseline/native/worker.ts']);report.worker=inspectWorker(await owned(worker),image,run,routerVolume,stage);
    child=spawn('docker',['--context',context,'start','--attach','--interactive',worker],{stdio:['pipe','pipe','pipe']});child.stdout!.resume();child.stderr!.resume();child.stdin!.on('error',()=>{});child.stdin!.end(JSON.stringify(request));childDone=new Promise(resolve=>{child!.once('error',error=>resolve({error:String(error)}));child!.once('close',(code,signal)=>resolve({code,signal}));});
    observation=await waitEvent(worker,'baseline_worker_observation',workerDeadline);report.observation=await retainEvidence(store,'worker-observation',observation);
    assert.equal(observation.invalid,false);assert.equal(observation.reason,null);assert.equal(observation.exitCode,0);assert.equal(observation.rejected.length,0);
    await docker(['pause',worker]);assert.equal((await owned(worker)).State.Paused,true);
    const archive=await execute('docker',['--context',context,'cp',worker+':/work/repo/.','-'],{timeout:15000,maxBuffer:1048576,encoding:'buffer'});
    const baseManifest=(await readSnapshot(store,preparedCase.base)).manifest,captured=await captureSnapshot(archive.stdout,baseManifest,rulesFor(registration),store,'candidate');
    candidate=await retainCandidate(preparedCase.base,captured,rulesFor(registration),store);report.candidate=candidate;
    const candidateDeadline=Math.min(Date.now()+60000,scope.deadline-120000);
    for(const id of ['pins','audit'])checks.push({revision:'candidate',check:await runCheck(await checkJob(store,registrationRef,captured,preparedCase.executables,id,'candidate',candidateDeadline),store,abort.signal)});
    report.checks=checks;assert(checks.every(x=>x.check.cleanup));assert.equal(checks[2].check.status,'passed');assert.equal(checks[3].check.status,'failed');
    const evidence=await control({command:'evidence',attemptId:binding.attemptId}),physical=await control({command:'baseline-proof'});
    const raw=await retainEvidence(store,'router-evidence',{evidence,physical});report.routerEvidence=raw;
    const promptSource=(await execute('git',['show',pins.sourceCommit+':packages/opencode/src/session/prompt/gpt-astra.txt'],{cwd:opencodeSource!})).stdout;
    const day=new Date(observation.startedAt).toLocaleDateString('en-US',{timeZone:'UTC',weekday:'short',month:'short',day:'2-digit',year:'numeric'}).replaceAll(',','').replace(/(\w+ \w+) (\d+) (\d+)/,'$1 $2 $3');
    const developer=promptSource+'\n\nYou are powered by the model named gpt-6-astra. The exact model ID is openai/gpt-6-astra\nHere is some useful information about the environment you are running in:\n<env>\n  Working directory: /work/repo\n  Workspace root folder: /\n  Is directory a git repo: no\n  Platform: linux\n  Today\'s date: '+day+'\n</env>';
    const sourceLock=JSON.parse(await readFile(join(root,'experiments/router-authority-extension/source-lock.json'),'utf8'));
    const instructionsPath='open-sse/config/codexInstructions.js';assert.equal(hash(await readFile(join(source!,instructionsPath))),sourceLock.files[instructionsPath]);
    const {CODEX_DEFAULT_INSTRUCTIONS}=await import(pathToFileURL(join(source!,instructionsPath)).href);
    const verified=verifyBaselineTranscript({schema:1,origin:'synthetic',caseId:'case01',expected:{prompt:JSON.stringify(request.prompt),context:developer,physicalInstructions:CODEX_DEFAULT_INSTRUCTIONS,policyDigest:digest(policy),bindingDigest:digest(binding),packetDigest:packet.packetDigest,scopeDigest:digest(evidence.scope)},policy,binding,packetDigest:packet.packetDigest,scope:evidence.scope,requests:observation.requests,decisions:evidence.decisions,receipts:evidence.receipts,pendingReservations:evidence.reservations,durableDecisionTimes:evidence.decisionTimes,physicalRequests:physical.physicalRequests,scopeOperations:physical.scopeOperations,events:observation.events,exitCode:observation.exitCode,signal:observation.signal,localProcessExited:observation.localProcessExited});
    report.transcript=await retainEvidence(store,'verified-transcript',verified);assert.equal(verified.physicalAttempts,scenario==='two-requests'?2:3);
    const bundle=await readEvidence(store,candidate);await readSnapshot(store,captured);await readSnapshot(store,preparedCase.base);
    report.acknowledgement=await retainEvidence(store,'baseline-ack',{schema:1,kind:'synthetic-baseline-acknowledgement',registration:registrationRef,registrationCommit,packet:report.packet,bindingDigest:digest(binding),scopeDigest:digest(evidence.scope),candidate,bundleDigest:digest(bundle),transcript:report.transcript,checks:checks.map(x=>x.check.observation),independentlyAccepted:false,published:false});
    await control({command:'stop'});assert.equal(Number(await docker(['wait',gateway])),0);assert.equal((await owned(gateway)).State.Pid,0);
    await remove(worker);await childDone;await readSnapshot(store,captured); // Reconstructible after tmpfs destruction.
    const record=fixture().runs[0],endedAt=Date.now();record.runId=run;record.registrationDigest=digest(registration);record.registrationCommit=registrationCommit;
    record.startedAt=new Date(scope.started).toISOString();record.endedAt=new Date(endedAt).toISOString();record.elapsedMs=endedAt-scope.started;record.waitingMs=null;
    record.scope={id:scope.id,startedAt:record.startedAt,deadlineAt:new Date(scope.deadline).toISOString(),physicalAttempts:verified.physicalAttempts,operationsComplete:true,quiescence:'confirmed',evidence:raw};
    record.attempts=[{id:binding.attemptId,startedAt:new Date(observation.startedAt).toISOString(),endedAt:new Date(observation.endedAt).toISOString(),status:'completed',exitCode:0,invocation,progressNotes:registration.fixture.progressSeed,artifact:candidate,observations:[report.observation,raw],operations:evidence.receipts.flatMap((r:any)=>r.operations.map((op:any)=>({id:r.id+'_'+op.ordinal,source:op.ordinal===1?'original':'router-fallback',outcome:op.terminal==='provider_completed'?'success':op.terminal==='unknown'?'unknown':'failure',evidence:raw})))}];
    record.checks=checks.map(({revision,check})=>({id:revision+'-'+check.checkId,checkId:check.checkId,attemptId:revision==='base'?null:binding.attemptId,revision,status:check.status,exitCode:check.status==='passed'||check.status==='failed'?check.exitCode:null,candidateArtifact:revision==='base'?null:candidate,evidence:check.observation}));
    record.operatorIntervals=[];record.usage=[];record.ownershipCosts=[];record.rawObservations=[report.acknowledgement,raw,report.observation];
    const dataset={schema:1,registrations:[registration],runs:[record]};report.dataset=await retainEvidence(store,'baseline-dataset',dataset);report.export=await writeExport(dataset,join(output,'export'));report.result='passed';
  } catch(error) {
    report.error=String(error);if(gateway)try{report.gatewayLogs=await docker(['logs',gateway]);}catch{}if(worker)try{report.workerLogs=await docker(['logs',worker]);}catch{}
    try { await save(); } catch { report.persistenceFailed=true; }
    throw error;
  } finally {
    const cleanup=await Promise.allSettled([...containers].map(remove));report.cleanup=cleanup.every(x=>x.status==='fulfilled');report.cleanupResults=cleanup.map(x=>x.status);report.retainedStateVolumes=volumes;
    if(child)child.kill('SIGTERM');for(const signal of ['SIGTERM','SIGINT'] as const)process.off(signal,onStop);
    report.result=report.result==='passed'&&report.cleanup?'passed':'failed';await save();
  }
  return {result:report.result,output,live:false,issueComplete:false};
}
if(process.argv[1]&&resolve(process.argv[1])===resolve(import.meta.filename))console.log(JSON.stringify(await runCase(process.argv[2],process.argv[3] as 'two-requests'|'three-requests')));
