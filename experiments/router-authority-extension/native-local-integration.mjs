import assert from 'node:assert/strict';
import { writeJSON } from './process-output.mjs';
import { createServer, request as httpRequest } from 'node:http';
import { Readable } from 'node:stream';
import { readFileSync,writeFileSync,mkdirSync } from 'node:fs';
import { once } from 'node:events';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { responseCases, expectedObservation, syntheticResponse } from './response-diagnostic-fixtures.mjs';
import { profile,request,events,frames } from '/gaffer/experiments/native-evaluation/fixtures.mjs';
import { Boundary } from '/gaffer/experiments/inference-boundary/boundary.ts';
import { PolicyGate } from '/gaffer/experiments/inference-boundary/policy.ts';
import { RouterAuthority } from '/gaffer/experiments/router-boundary-bridge/authority.ts';
import { continuationOutput } from './overlay/native-responses.mjs';
import { failed,created } from './native-fixtures.mjs';
import { digest } from './overlay/native-profile.mjs';
const scenario=process.argv[2]??'roundtrip';
const responseCase=responseCases[scenario];let responseTransportClosed=false;
const caseRoot='/tmp/native-case-'+scenario.replace(/[^a-z0-9-]/gi,'-');mkdirSync(caseRoot,{mode:0o700});
const journalPath=caseRoot+'/journal',routerSocket=caseRoot+'/router.sock',boundarySocket=caseRoot+'/boundary.sock';
const fault=await import('/gaffer/experiments/native-evaluation/faults.mjs');
const {handleChat}=await import('/router-source/src/sse/handlers/chat.js');
const {getAdapter}=await import('/router-source/src/lib/db/driver.js');
const repository=await import('/router-source/src/lib/db/index.js');
const {authority,project}=await import('/router-source/gaffer-extension/authority.mjs');
await getAdapter();const a=authority(),n=profile();
if(['release-scope','release-frame'].includes(scenario)){n.scope.elapsedMs=1500;Object.assign(n.local,{totalMs:1500,firstOutputMs:1500,idleMs:1500,attemptMs:1500});}
if(scenario==='release-token')for(const c of n.connections){c.skewMs=1000;c.expiresAt=Date.now()+2500;}
if(scenario==='release-request')Object.assign(n.local,{totalMs:500,firstOutputMs:500,idleMs:500});
if(scenario==='aggregate10')n.local.idleMs=4000;if(scenario==='charged-unreleased-retry')Object.assign(n.local,{totalMs:4000,firstOutputMs:3000,idleMs:3000});
if(scenario==='baseline32'){n.scope.phase='baseline';n.scope.maxInferenceAttempts=32;n.scope.elapsedMs=900000;n.local.attemptMs=900000;}
if(scenario==='retry-deadline'){Object.assign(n.local,{totalMs:500,firstOutputMs:500,idleMs:500});}
if(scenario==='slow-request'){n.local.totalMs=1000;n.local.firstOutputMs=1000;n.local.idleMs=1000;}
if(scenario==='slow-scope'){n.scope.elapsedMs=1500;Object.assign(n.local,{totalMs:1500,firstOutputMs:1500,idleMs:1500,attemptMs:1500});}
if(scenario==='slow-token')for(const c of n.connections){c.skewMs=1000;c.expiresAt=Date.now()+2500;}
const variant=structuredClone(n);variant.toolPaths.push('second.txt');a.configureNative(['registry','registry-unknown'].includes(scenario)?[n,variant]:n);
const config={settings:{requireApiKey:true,rtkEnabled:false,headroomEnabled:false,pxpipeEnabled:false,cavemanEnabled:false,ponytailEnabled:false,ccFilterNaming:false,capacityAdapter:Object.fromEntries(['vision','pdf','audioInput','videoInput'].map(k=>[k,{enabled:false,models:[]}]))},providerConnections:n.connections.map((c,i)=>({id:c.id,provider:'codex',authType:'oauth',name:'synthetic native',priority:i+1,isActive:true,accessToken:'synthetic_access_'+i,expiresAt:new Date(c.expiresAt).toISOString(),providerSpecificData:{chatgptAccountId:'synthetic_workspace_'+i}})),apiKeys:[{id:'synthetic_ingress',key:'synthetic_gateway_key',name:'synthetic ingress',isActive:true}],combos:[{id:'native_route',name:'gaffer_native',models:['cx/gpt-6-astra']}]};
assert.equal(await a.replace(a.state().generation,'native_revision',project(config),()=>repository.importDb(config)),true);a.startEvaluation();
const state=a.state(),p={schema:3,routerId:'native_router',routeId:'native_route',revision:state.revision,epoch:1,routerModel:'gaffer_native',profile:'router-native-responses-local-v1',evidence:n.evidence,liveAdmission:false,graph:a.snapshot(),limits:{...n.local,outputTokens:null},authority:{deploymentId:n.deployment.id,boot:state.boot,generation:state.generation,revision:state.revision,graphDigest:digest(a.snapshot())},native:n};
const bridge=new RouterAuthority(a,p);let gate=await PolicyGate.open(journalPath,bridge,1000);await gate.activate();
const sends=[],decisions=[],translated=[];const finalize=gate.finalize.bind(gate);gate.finalize=async(...args)=>{if(scenario==='decision-write')gate.syncDirectory=async()=>{throw Error('injected_native_decision_sync_failure');};if(scenario==='altered-translation')args[4]=events(false).at(-1).response.output;await finalize(...args);decisions.push({id:args[0],at:Date.now()});};
const backend=createServer(async(req,res)=>{
 let text='';for await(const c of req)text+=c;const body=JSON.parse(text);sends.push({path:req.url,body});
 assert.equal(req.url,'/responses');assert.equal(body.model,'gpt-6-astra');assert.deepEqual(body.reasoning,{effort:'xhigh',summary:'auto'});assert.equal(body.store,false);assert(['max_tokens','max_output_tokens','max_completion_tokens'].every(k=>!Object.hasOwn(body,k)));
 if(responseCase){res.once('close',()=>{responseTransportClosed=true;});syntheticResponse(res,responseCase,frames(events(false)));return;}
 if(scenario==='aggregate10'&&sends.length===1){res.writeHead(429,{'content-type':'application/json'}).end(JSON.stringify({error:{message:'synthetic account exhausted'}}));return;}
 if(['unauthorized401','unauthorized403'].includes(scenario)){res.writeHead(Number(scenario.slice(-3)),{'content-type':'application/json'});res.end(JSON.stringify({error:{message:'synthetic denied token'}}));return;}
 if(scenario==='redirect'){res.writeHead(307,{location:'http://127.0.0.1:47772/unapproved'}).end();return;}
 res.writeHead(200,{'content-type':'text/event-stream'});
 if(['unknown-retry','registry-unknown'].includes(scenario)){res.write(frames(events(true).slice(0,1))+': server_is_overloaded\n\n');return;}
 let output=events(sends.length===1&&!['aggregate10','baseline32','registry'].includes(scenario));
 if(['retry-deadline','retry-cancel'].includes(scenario)||(scenario==='charged-unreleased-retry'&&sends.length===1)){const failure=failed();failure.response.error.message='server_is_overloaded';output=[created(),failure];}
 const bytes=Buffer.from(frames(output));for(let i=0;i<bytes.length;i+=13)res.write(bytes.subarray(i,i+13));if(scenario!=='terminal-no-eof')res.end();
});backend.listen(47771,'127.0.0.1');await once(backend,'listening');
const upstream=createServer(async(req,res)=>{const signal=new AbortController();res.on('close',()=>{if(!res.writableEnded)signal.abort();});try{const result=await handleChat(new Request('http://127.0.0.1'+req.url,{method:'POST',headers:req.headers,body:Readable.toWeb(req),duplex:'half',signal:signal.signal}));res.writeHead(result.status,Object.fromEntries(result.headers));if(result.body)for await(const c of result.body){translated.push(Buffer.from(c).toString());res.write(c);}res.end();}catch{res.destroy();}});upstream.listen(routerSocket);await once(upstream,'listening');
let boundary=new Boundary(gate,routerSocket,'synthetic_gateway_key');await boundary.listen(boundarySocket);
if(['release-scope','release-token','release-frame'].includes(scenario))await new Promise(r=>setTimeout(r,750));
const binding={attemptId:'native_attempt',grantId:'native_grant',taskId:'native_task',leaseId:'native_lease',fence:1,role:'worker',routerId:p.routerId,routeId:p.routeId,revision:p.revision,epoch:1,expiresAt:Date.now()+20000,leaseExpiresAt:Date.now()+20000,native:{profileDigest:digest(n),scopeId:n.scope.id,authorizationDigest:n.scope.authorizationDigest}};if(scenario==='slow-grant')binding.expiresAt=Date.now()+1500;if(scenario==='release-lease')binding.leaseExpiresAt=Date.now()+500;let token=boundary.issue(binding);
async function send(body){const bytes=JSON.stringify(body),call=httpRequest({socketPath:boundarySocket,path:'/v1/responses',method:'POST',headers:{host:'localhost',authorization:'Bearer '+token,'content-type':'application/json','content-length':Buffer.byteLength(bytes)}});const result=once(call,'response');call.end(bytes);const [r]=await result;let text='',firstAt=null;for await(const c of r){firstAt??=Date.now();text+=c;}return {status:r.statusCode,text,firstAt,id:r.headers['x-gaffer-request-id']};}
for(const role of ['planner','reviewer']){assert.throws(()=>boundary.issue({...binding,attemptId:'role_'+role,role}));await assert.rejects(gate.admit({...binding,attemptId:'admit_'+role,role},()=>true,undefined,digest(JSON.stringify(request())),request()));}
const first=request();
if(scenario==='retry-cancel')setTimeout(()=>{for(const r of gate.snapshot().reservations)a.cancel(r.requestId);},200);
let durabilityEnded=null,privateReleaseDenied=false;
const assertPrivate=bridge.assertCurrent.bind(bridge);bridge.assertCurrent=(...args)=>{try{return assertPrivate(...args);}catch(error){if(durabilityEnded)privateReleaseDenied=true;throw error;}};
if(scenario==='release-frame'){const release=gate.assertRelease.bind(gate);gate.assertRelease=id=>{Atomics.wait(new Int32Array(new SharedArrayBuffer(4)),0,0,800);return release(id);};}
if(scenario.startsWith('release-')){const sync=gate.syncDirectory.bind(gate);let delayed=false;gate.syncDirectory=async()=>{await sync();if(!delayed&&gate.state.decisions.some(d=>d.verdict==='validated_success')){delayed=true;if(scenario==='release-frame'){}else if(['release-scope','release-token'].includes(scenario))await new Promise(r=>setTimeout(r,800));else Atomics.wait(new Int32Array(new SharedArrayBuffer(4)),0,0,800);durabilityEnded=Date.now();if(['release-request','release-lease'].includes(scenario)){assert.throws(()=>bridge.assertCurrent(gate.state.reservations[0]));privateReleaseDenied=true;}}};}
const r1=await send(first);
if(responseCase){
 const until=Date.now()+1500;while(!boundary.audit.length||!responseTransportClosed){assert(Date.now()<until,'physical response not disposed');await new Promise(r=>setTimeout(r,5));}
 const journal=gate.snapshot(),records=[...journal.reservations,...journal.decisions];assert.equal(records.length,1);
 const receipt=a.receipt(records[0].requestId),scope=a.evaluationScope(n.scope.id);
 assert.equal(sends.length,1);assert.equal(scope.spent,1);assert.equal(receipt.operations.length,1);
 const operation=receipt.operations[0];assert.equal(operation.request_id,receipt.id);assert.equal(operation.ordinal,1);
 assert.deepEqual(operation.response_observation,expectedObservation(responseCase));
 assert(!JSON.stringify(operation.response_observation??null).includes('synthetic_private'));
 if(responseCase.legacy)assert.equal(Object.hasOwn(operation,'response_observation'),false);
 if(responseCase.storageFailure){assert(fault.proof.triggered>0);if(scenario==='response-write-stop')assert(fault.proof.triggered>=2);}
 if(responseCase.valid){assert.equal(receipt.quiescent,true);assert.equal(operation.terminal,'provider_completed');assert.equal(r1.status,200);assert.match(r1.text,/response.completed/);assert.equal(journal.decisions[0].verdict,'validated_success');assert(r1.firstAt>=decisions[0].at);}
 else {
  assert.equal(receipt.quiescent,false);assert.equal(operation.terminal,'unknown');assert.equal(operation.output_digest,null);assert.equal(journal.decisions.length,0);assert.equal(journal.reservations.length,1);assert(!r1.text.includes('event: response.'));
  token=boundary.issue({...binding,attemptId:'response_replacement',grantId:'response_replacement'});const replacement=await send(first);assert(!replacement.text.includes('event: response.'));assert.equal(sends.length,1);assert.equal(a.evaluationScope(n.scope.id).spent,1);
  let writerCalled=false;assert.equal(a.quiescent([receipt.id]),false);assert.equal(await a.replace(a.state().generation,'response_forbidden',a.snapshot(),()=>{writerCalled=true;}),false);assert.equal(writerCalled,false);
 }
 await boundary.close();upstream.closeAllConnections();backend.closeAllConnections();await new Promise(r=>upstream.close(r));await new Promise(r=>backend.close(r));
 const before={state:a.state(),scope:a.evaluationScope(n.scope.id),receipt:a.receipt(receipt.id)};writeFileSync(caseRoot+'/before-restart.json',JSON.stringify(before));a.close();
 const {stdout}=await promisify(execFile)(process.execPath,['--experimental-loader','/gaffer/experiments/native-evaluation/fault-loader.mjs','/probe/response-restart.mjs',caseRoot+'/before-restart.json'],{env:{...process.env,GAFFER_TEST_FAULT:''},timeout:10000,maxBuffer:1024*1024});
 const restart=stdout.split('\n').filter(l=>l.startsWith('{')).map(l=>JSON.parse(l)).at(-1);assert.equal(restart.result,'passed');
 await writeJSON({scenario,result:'passed',live:false,evidence:'isolated-pinned-router-original-response-diagnostics',physicalSendCount:sends.length,responseTransportClosed,scope,receipt,journal,fault:fault.proof,released:responseCase.valid===true,replacementDenied:responseCase.valid!==true,restart});process.exit(0);
}
if(scenario.startsWith('release-')){assert.equal(sends.length,1);assert(durabilityEnded);assert.equal(privateReleaseDenied,true);assert(!r1.text.includes('event: response.'),'expired output released');const until=Date.now()+1000;while(!boundary.audit.length){assert(Date.now()<until);await new Promise(r=>setTimeout(r,5));}const journal=gate.snapshot(),scope=a.evaluationScope(n.scope.id);assert.equal(scope.spent,1);assert.equal(journal.decisions.length,1);assert.equal(journal.decisions[0].verdict,'validated_success');assert.notEqual(journal.decisions[0].delivery,'completed');if(['release-scope','release-token','release-frame'].includes(scenario))assert.equal(scope.state,'closed');const receipts=journal.decisions.map(d=>a.receipt(d.requestId));assert.equal(receipts[0].operations[0].terminal,'provider_completed');await writeJSON({scenario,result:'passed',live:false,physicalSends:sends,scope,receipts,journal,durabilityEnded,privateReleaseDenied,firstAt:r1.firstAt,released:false,audit:boundary.audit});await boundary.close();upstream.closeAllConnections();backend.closeAllConnections();process.exit(0);}
if(scenario==='role-reload'){
 assert.match(r1.text,/response.completed/);const until=Date.now()+1500;while(!boundary.audit.length){assert(Date.now()<until);await new Promise(r=>setTimeout(r,5));}await boundary.close();const path=journalPath+'/state.json',saved=readFileSync(path,'utf8'),journal=JSON.parse(saved);journal.decisions[0].router.binding.role='planner';writeFileSync(path,JSON.stringify(journal));await assert.rejects(PolicyGate.open(journalPath,bridge,1000));writeFileSync(path,saved);const reopened=await PolicyGate.open(journalPath,bridge,1000);await reopened.close();assert.equal(sends.length,1);await writeJSON({scenario,result:'passed',live:false,physicalSends:sends,roleReloadDenied:true});upstream.closeAllConnections();backend.closeAllConnections();process.exit(0);
}
if(scenario==='registry'){
 assert.match(r1.text,/Completed/);const until=Date.now()+1500;while(!boundary.audit.length){assert(Date.now()<until);await new Promise(r=>setTimeout(r,5));}
 const originalScope=a.evaluationScope(n.scope.id),oldState=a.state();assert.throws(()=>a.selectNativeProfile('0'.repeat(64),oldState.generation));assert.throws(()=>a.selectNativeProfile(digest(variant),oldState.generation-1));
 await boundary.close();const selected=a.selectNativeProfile(digest(variant),oldState.generation);assert.equal(selected.state.boot,oldState.boot);assert.equal(selected.state.generation,oldState.generation+1);assert.deepEqual(a.evaluationScope(n.scope.id),originalScope);assert.throws(()=>a.startEvaluation());
 const policy={...p,native:variant,epoch:2,revision:selected.state.revision,graph:selected.graph,authority:{...p.authority,generation:selected.state.generation,revision:selected.state.revision,graphDigest:digest(selected.graph)}};
 gate=await PolicyGate.open(journalPath,new RouterAuthority(a,policy),1000);await gate.activate();boundary=new Boundary(gate,routerSocket,'synthetic_gateway_key');await boundary.listen(boundarySocket);
 assert.throws(()=>boundary.issue({...binding,attemptId:'stale_registry'}));token=boundary.issue({...binding,attemptId:'selected_attempt',grantId:'selected_grant',revision:policy.revision,epoch:2,native:{...binding.native,profileDigest:digest(variant)}});const r2=await send(first);assert.match(r2.text,/Completed/);assert.equal(sends.length,2);const scope=a.evaluationScope(n.scope.id);assert.equal(scope.started,originalScope.started);assert.equal(scope.deadline,originalScope.deadline);assert.equal(scope.spent,2);
 await writeJSON({scenario,result:'passed',live:false,physicalSends:sends,originalScope,scope,registry:a.nativeProfiles(),oldState,selected,decisions:gate.snapshot().decisions});await boundary.close();upstream.closeAllConnections();backend.closeAllConnections();process.exit(0);
}
if(['aggregate10','baseline32'].includes(scenario)){
 const requests=scenario==='aggregate10'?9:32;assert.equal(r1.status,200);assert.match(r1.text,/Completed/,JSON.stringify({receipts:gate.snapshot().reservations.map(r=>a.receipt(r.requestId)),audit:boundary.audit}));
 for(let i=1;i<requests;i++){token=boundary.issue({...binding,attemptId:'native_attempt_'+i,grantId:'native_grant_'+i});const result=await send(first);assert.equal(result.status,200);assert.match(result.text,/Completed/);}
 const expected=scenario==='aggregate10'?10:32;assert.equal(sends.length,expected);assert.equal(a.evaluationScope(n.scope.id).spent,expected);
 token=boundary.issue({...binding,attemptId:'native_exhausted',grantId:'native_exhausted'});const exhausted=await send(first);assert(!exhausted.text.includes('event: response.'));assert.equal(sends.length,expected);
 const until=Date.now()+1500;while(boundary.audit.length<requests+1){assert(Date.now()<until);await new Promise(r=>setTimeout(r,5));}
 const journal=gate.snapshot(),receipts=journal.decisions.map(r=>a.receipt(r.requestId));if(scenario==='aggregate10'){assert.equal(receipts[0].operations.length,2);assert.equal(receipts[0].operations[0].terminal,'provider_rejected');assert.notEqual(receipts[0].operations[0].connection_id,receipts[0].operations[1].connection_id);};
 await writeJSON({scenario,result:'passed',live:false,physicalSends:sends,scope:a.evaluationScope(n.scope.id),receipts,journal});await boundary.close();upstream.closeAllConnections();backend.closeAllConnections();process.exit(0);
}
if(scenario!=='roundtrip'){
 if(scenario.startsWith('slow-')||['admission-write','debit-write'].includes(scenario)||scenario.startsWith('override-'))assert.notEqual(r1.status,200);assert(!r1.text.includes('event: response.'));
 const auditDeadline=Date.now()+1500;while(!boundary.audit.length){assert(Date.now()<auditDeadline);await new Promise(r=>setTimeout(r,5));}assert.equal(decisions.length,0);
 const journal=gate.snapshot();assert(!journal.decisions.some(d=>d.verdict==='validated_success'));
 const scope=a.evaluationScope(n.scope.id);const receipts=[...journal.reservations,...journal.decisions].map(r=>a.receipt(r.requestId));
 if(scenario.startsWith('slow-')){assert.equal(fault.proof.triggered,1);assert.equal(fault.proof.committed,true);assert.equal(sends.length,0);assert.equal(scope.spent,1);assert.equal(receipts[0].operations[0].terminal,'unknown');assert.equal(journal.reservations.length,1);}
 else if(['admission-write','debit-write'].includes(scenario)){assert(fault.proof.triggered>0);assert.equal(sends.length,0);assert.equal(scope.spent,0);}
 else if(scenario.startsWith('override-')){assert.equal(sends.length,0);assert.equal(scope.spent,0);}
 else if(scenario==='receipt-write'){assert(fault.proof.triggered>0);assert.equal(sends.length,1);assert.equal(scope.spent,1);assert.equal(receipts[0].operations[0].terminal,'unknown');}
 else if(scenario==='decision-write'){assert.equal(sends.length,1);assert.equal(scope.spent,1);assert.equal(journal.reservations.length,1);const disk=JSON.parse(readFileSync(journalPath+'/state.json','utf8'));assert.equal(disk.decisions[0].verdict,'validated_success');assert.equal(disk.decisions[0].delivery,'unobserved');}
 else if(scenario==='altered-translation'){assert.equal(sends.length,1);assert.equal(journal.reservations.length,0);assert.equal(journal.decisions[0].verdict,'quiescent_failure');}
 else if(['unknown-retry','registry-unknown','terminal-no-eof','redirect'].includes(scenario)){assert.equal(sends.length,1);assert.equal(receipts[0].operations[0].terminal,'unknown');assert.equal(journal.reservations.length,1);assert.equal(await a.replace(a.state().generation,'forbidden',a.snapshot(),()=>{}),false);if(scenario==='registry-unknown')assert.throws(()=>a.selectNativeProfile(digest(variant),a.state().generation));}
 else if(scenario==='charged-unreleased-retry'){assert.equal(sends.length,2);assert.equal(scope.spent,2);assert.equal(receipts[0].quiescent,true);assert.deepEqual(receipts[0].operations.map(o=>[o.terminal,o.local_stop]),[['provider_failed','original_cancel'],['provider_completed','original_eof']]);}
 else if(['retry-deadline','retry-cancel'].includes(scenario)){assert.equal(sends.length,1);assert.equal(scope.spent,1);}
 else if(['unauthorized401','unauthorized403'].includes(scenario)){assert(sends.length>=1&&sends.length<=2);assert(receipts.every(r=>r.operations.every(o=>o.model==='gpt-6-astra')));assert.equal(scope.spent,sends.length);}
 else throw Error('unknown_scenario');
 await writeJSON({scenario,evidence:'isolated-pinned-router-native-failure',live:false,physicalSends:sends,scope,receipts,journal,fault:fault.proof,result:'passed'});
 if(scenario==='decision-write')await assert.rejects(boundary.close());else await boundary.close();upstream.closeAllConnections();backend.closeAllConnections();process.exit(0);
}
assert.equal(r1.status,200);assert.match(r1.text,/response.completed/);assert(r1.firstAt>=decisions[0].at);
const output=events(true).at(-1).response.output,next={...first,input:[...first.input,...continuationOutput(output,n),{type:'function_call_output',call_id:'call_synthetic',output:'Success. Updated greeting.txt'}]};
const r2=await send(next);assert.equal(r2.status,200);assert.match(r2.text,/Completed 🌍 café/);assert(r2.firstAt>=decisions[1].at);assert.equal(sends.length,2);assert.equal(a.evaluationScope(n.scope.id).spent,2);{const until=Date.now()+1000;while(boundary.audit.length<2){assert(Date.now()<until);await new Promise(r=>setTimeout(r,5));}}assert.equal(gate.snapshot().reservations.length,0);assert(gate.snapshot().decisions.every(d=>d.verdict==='validated_success'&&d.nativeOutput));
const r3=await send(next);assert.notEqual(r3.status,200);assert.equal(sends.length,2);
await writeJSON({scenario,evidence:'isolated-pinned-router-native-responses-synthetic',live:false,policy:p,physicalSends:sends,receipts:gate.snapshot().decisions.map(d=>a.receipt(d.requestId)),decisions:gate.snapshot().decisions,scope:a.evaluationScope(n.scope.id),completionTimes:[r1.firstAt,r2.firstAt],decisionTimes:decisions});
await boundary.close();upstream.closeAllConnections();backend.closeAllConnections();await new Promise(r=>upstream.close(r));await new Promise(r=>backend.close(r));process.exit(0);
