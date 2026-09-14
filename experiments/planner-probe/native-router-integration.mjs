// Offline-only pinned-router integration. This fixture is deliberately absent
// from production consumer staging and never accepts provider credentials.
import assert from 'node:assert/strict';
import { createServer, request as httpRequest } from 'node:http';
import { Readable } from 'node:stream';
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { once } from 'node:events';
import { profile,frames } from '../native-evaluation/fixtures.mjs';
import { plannerEvents } from '../native-evaluation/planner-fixtures.mjs';
import { plannerProfile } from './native-profile.ts';
import { NativeBoundaryPort } from './native-boundary-port.ts';
import { NativePlannerTransport,NATIVE_PLANNER_BUDGET } from './native-transport.ts';
import { PlannerSession } from './planner.ts';
import { snapshot } from './reader.ts';
import { BRIEF,FIXTURE_ROOT,FIXTURE_HASH } from './public-fixture.ts';
import { Boundary } from '../inference-boundary/boundary.ts';
import { PolicyGate } from '../inference-boundary/policy.ts';
import { RouterAuthority } from '../router-boundary-bridge/authority.ts';
import { digest } from '../router-authority-extension/overlay/native-profile.mjs';
const scenario=process.argv[2]??'read',root='/tmp/planner-'+scenario.replace(/[^a-z0-9-]/gi,'-');mkdirSync(root,{mode:0o700});
const journalPath=root+'/journal',routerSocket=root+'/router.sock',boundarySocket=root+'/boundary.sock';
const fault=await import('../native-evaluation/faults.mjs');
const {handleChat}=await import('/router-source/src/sse/handlers/chat.js');
const {getAdapter}=await import('/router-source/src/lib/db/driver.js');
const repository=await import('/router-source/src/lib/db/index.js');
const {authority,project}=await import('/router-source/gaffer-extension/authority.mjs');
await getAdapter();const a=authority(),worker=profile(),n=plannerProfile(worker);
if(scenario==='release-scope'){n.scope.elapsedMs=1500;Object.assign(n.local,{totalMs:1500,firstOutputMs:1500,idleMs:1500,attemptMs:1500});}
if(scenario==='release-request')Object.assign(n.local,{totalMs:500,firstOutputMs:500,idleMs:500});
if(scenario==='unknown')Object.assign(n.local,{totalMs:700,firstOutputMs:700,idleMs:500});
a.configureNative(n);
const config={settings:{requireApiKey:true,rtkEnabled:false,headroomEnabled:false,pxpipeEnabled:false,cavemanEnabled:false,ponytailEnabled:false,ccFilterNaming:false,capacityAdapter:Object.fromEntries(['vision','pdf','audioInput','videoInput'].map(k=>[k,{enabled:false,models:[]}]))},providerConnections:n.connections.map((c,i)=>({id:c.id,provider:'codex',authType:'oauth',name:'synthetic native',priority:i+1,isActive:true,accessToken:'synthetic_access_'+i,expiresAt:new Date(c.expiresAt).toISOString(),providerSpecificData:{chatgptAccountId:'synthetic_workspace_'+i}})),apiKeys:[{id:'synthetic_ingress',key:'synthetic_gateway_key',name:'synthetic ingress',isActive:true}],combos:[{id:'native_route',name:'gaffer_native',models:['cx/gpt-6-astra']}]};
assert.equal(await a.replace(a.state().generation,'native_revision',project(config),()=>repository.importDb(config)),true);a.startEvaluation();
const state=a.state(),p={schema:3,routerId:'native_router',routeId:'native_route',revision:state.revision,epoch:1,routerModel:'gaffer_native',profile:'router-native-responses-local-v1',evidence:n.evidence,liveAdmission:false,graph:a.snapshot(),limits:{...n.local,outputTokens:null},authority:{deploymentId:n.deployment.id,boot:state.boot,generation:state.generation,revision:state.revision,graphDigest:digest(a.snapshot())},native:n};
const bridge=new RouterAuthority(a,p),gate=await PolicyGate.open(journalPath,bridge,1000);await gate.activate();
const sends=[],requests=[],decisionTimes=[];let delayed=false,durabilityEnded=null;
const finalize=gate.finalize.bind(gate);gate.finalize=async(...args)=>{if(scenario==='decision-write')gate.syncDirectory=async()=>{throw Error('injected_decision_sync');};await finalize(...args);decisionTimes.push({requestId:args[0],at:Date.now()});};
if(scenario.startsWith('release-')){const sync=gate.syncDirectory.bind(gate);gate.syncDirectory=async()=>{await sync();if(!delayed&&gate.state.decisions.some(d=>d.verdict==='validated_success')){delayed=true;Atomics.wait(new Int32Array(new SharedArrayBuffer(4)),0,0,900);durabilityEnded=Date.now();}};}
const backend=createServer(async(req,res)=>{
 let text='';for await(const c of req)text+=c;const body=JSON.parse(text);sends.push({path:req.url,body});
 assert.equal(req.url,'/responses');assert.equal(body.model,'gpt-6-astra');assert.deepEqual(body.reasoning,{effort:'xhigh',summary:'auto'});assert.equal(body.store,false);assert(!Object.keys(body).some(k=>['max_tokens','max_output_tokens','max_completion_tokens'].includes(k)));assert.equal(body.tools[0].name,'read_file');assert.equal(Object.hasOwn(body.tools[0],'strict'),false);assert(!text.includes('synthetic_workspace_'));
 res.writeHead(200,{'content-type':'text/event-stream'});
 const output=plannerEvents(body,n,scenario);if(scenario==='unknown'){res.write(frames(output.slice(0,1)));return;}
 const bytes=Buffer.from(frames(output));for(let i=0;i<bytes.length;i+=13)res.write(bytes.subarray(i,i+13));res.end();
});backend.listen(47771,'127.0.0.1');await once(backend,'listening');
const upstream=createServer(async(req,res)=>{const signal=new AbortController();res.on('close',()=>{if(!res.writableEnded)signal.abort();});try{const result=await handleChat(new Request('http://127.0.0.1'+req.url,{method:'POST',headers:req.headers,body:Readable.toWeb(req),duplex:'half',signal:signal.signal}));res.writeHead(result.status,Object.fromEntries(result.headers));if(result.body)for await(const c of result.body)res.write(c);res.end();}catch{res.destroy();}});upstream.listen(routerSocket);await once(upstream,'listening');
const boundary=new Boundary(gate,routerSocket,'synthetic_gateway_key');await boundary.listen(boundarySocket);
const binding={attemptId:'planner_attempt',grantId:'planner_grant',taskId:'planner_task',leaseId:'planner_lease',fence:1,role:'planner',routerId:p.routerId,routeId:p.routeId,revision:p.revision,epoch:1,expiresAt:Date.now()+20000,leaseExpiresAt:Date.now()+20000,native:{profileDigest:digest(n),scopeId:n.scope.id,authorizationDigest:n.scope.authorizationDigest}};
if(scenario==='release-lease')binding.leaseExpiresAt=Date.now()+500;
for(const role of ['worker','reviewer'])assert.throws(()=>boundary.issue({...binding,role,attemptId:'wrong_'+role}));
let token=boundary.issue(binding);const port=new NativeBoundaryPort(boundarySocket,token,p,binding),send=port.complete.bind(port);port.complete=async(...args)=>{requests.push(structuredClone(args[0]));return send(...args);};
const file=await snapshot(FIXTURE_ROOT,'fixture.txt',FIXTURE_HASH,4096,AbortSignal.timeout(1000));
const planner=new PlannerSession(new NativePlannerTransport(port),file,BRIEF,NATIVE_PLANNER_BUDGET);
if(scenario==='release-scope')await new Promise(r=>setTimeout(r,750));
const result=await planner.run();assert.deepEqual(await planner.run(),result);
const until=Date.now()+1500;while(boundary.audit.length<requests.length){assert(Date.now()<until);await new Promise(r=>setTimeout(r,5));}
const journal=gate.snapshot(),receipts=journal.decisions.concat(journal.reservations.filter(r=>!journal.decisions.some(d=>d.requestId===r.requestId))).map(r=>a.receipt(r.requestId)),scope=a.evaluationScope(n.scope.id);
assert.equal(scope.spent,sends.length);assert.equal(result.authority,'proposal_only');
const success=['read','direct','clarification','read-repair','repair','empty','role-reload'].includes(scenario);
if(success){
 assert.equal(result.outcome,scenario==='clarification'?'clarification_proposed':'plan_proposed',JSON.stringify({result,journal,receipts,audit:boundary.audit}));
 assert.equal(result.completions.length,sends.length);assert.equal(journal.reservations.length,0);assert(journal.decisions.every(d=>d.verdict==='validated_success'&&d.delivery==='completed'));
 for(const completion of result.completions){assert(completion.acceptedAt>=decisionTimes.find(d=>d.requestId===completion.requestId).at);const receipt=receipts.find(r=>r.requestId===completion.requestId||r.id===completion.requestId)??receipts[result.completions.indexOf(completion)];assert.equal(receipt.operations.at(-1).terminal,'provider_completed');}
 assert.equal(result.usage.repairs,['read-repair','repair','empty'].includes(scenario)?1:0);assert.equal(result.usage.files,['direct','clarification','repair','empty'].includes(scenario)?0:1);
 // A fresh grant, transport and session may not assess this unchanged input again.
 const other={...binding,attemptId:'replacement_attempt',grantId:'replacement_grant'};token=boundary.issue(other);
 const replacement=new PlannerSession(new NativePlannerTransport(new NativeBoundaryPort(boundarySocket,token,p,other)),file,BRIEF,NATIVE_PLANNER_BUDGET),retry=await replacement.run();
 assert.equal(retry.inputRevision,result.inputRevision);assert.notEqual(retry.outcome,'plan_proposed');assert.equal(retry.completions.length,0);assert.equal(a.evaluationScope(n.scope.id).spent,sends.length);
 if(scenario==='role-reload'){
  await boundary.close();const path=journalPath+'/state.json',saved=readFileSync(path,'utf8'),value=JSON.parse(saved);value.decisions[0].router.binding.role='worker';writeFileSync(path,JSON.stringify(value));await assert.rejects(PolicyGate.open(journalPath,bridge,1000));writeFileSync(path,saved);
  const reopened=await PolicyGate.open(journalPath,bridge,1000);await reopened.close();
 }
}else{
 assert.notEqual(result.outcome,'plan_proposed');assert.equal(result.usage.repairs,['invalid-repair','repair-tool'].includes(scenario)?1:0);
 if(scenario.startsWith('release-')){assert(durabilityEnded);assert.equal(journal.decisions[0].verdict,'validated_success');assert.notEqual(journal.decisions[0].delivery,'completed');assert.equal(result.usage.files,0);assert.equal(result.completions.length,0);}
 if(['receipt-write','decision-write','unknown','worker-tool'].includes(scenario)){assert.equal(result.usage.files,0);assert.equal(result.completions.length,0);}
 if(scenario==='unknown'){assert.equal(journal.reservations.length,1);assert.equal(receipts[0].operations[0].terminal,'unknown');}
 if(scenario==='receipt-write')assert(fault.proof.triggered>0);
}
if(scenario==='decision-write')await assert.rejects(boundary.close());else if(scenario!=='role-reload')await boundary.close();
const report=JSON.stringify({scenario,result:'passed',evidence:'isolated-pinned-router-native-planner-synthetic',live:false,planner:result,policy:p,physicalSends:sends,requests,receipts,journal,scope,decisionTimes,durabilityEnded,fault:fault.proof,source:{fixture:FIXTURE_HASH}});
for(let offset=0;offset<report.length;offset+=8000)await new Promise((resolve,reject)=>process.stdout.write(JSON.stringify({reportChunk:report.slice(offset,offset+8000),offset})+'\n',error=>error?reject(error):resolve()));
upstream.closeAllConnections();backend.closeAllConnections();await new Promise(r=>upstream.close(r));await new Promise(r=>backend.close(r));process.exit(0);
