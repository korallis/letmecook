import assert from 'node:assert/strict';
import { createServer, request as httpRequest } from 'node:http';
import { Readable } from 'node:stream';
import { once } from 'node:events';
import { profile,request,events,frames } from '/gaffer/experiments/native-evaluation/fixtures.mjs';
import { Boundary } from '/gaffer/experiments/inference-boundary/boundary.ts';
import { PolicyGate } from '/gaffer/experiments/inference-boundary/policy.ts';
import { RouterAuthority } from '/gaffer/experiments/router-boundary-bridge/authority.ts';
import { continuationOutput } from './overlay/native-responses.mjs';
import { digest } from './overlay/native-profile.mjs';
const scenario=process.argv[2]??'roundtrip';
const fault=await import('/gaffer/experiments/native-evaluation/faults.mjs');
const {handleChat}=await import('/router-source/src/sse/handlers/chat.js');
const {getAdapter}=await import('/router-source/src/lib/db/driver.js');
const repository=await import('/router-source/src/lib/db/index.js');
const {authority,project}=await import('/router-source/gaffer-extension/authority.mjs');
await getAdapter();const a=authority(),n=profile();
if(scenario==='slow-request'){n.local.totalMs=1000;n.local.firstOutputMs=1000;n.local.idleMs=1000;}
if(scenario==='slow-scope'){n.scope.elapsedMs=1500;Object.assign(n.local,{totalMs:1500,firstOutputMs:1500,idleMs:1500,attemptMs:1500});}
if(scenario==='slow-token')for(const c of n.connections){c.skewMs=1000;c.expiresAt=Date.now()+2500;}
a.configureNative(n);
const config={settings:{requireApiKey:true,rtkEnabled:false,headroomEnabled:false,pxpipeEnabled:false,cavemanEnabled:false,ponytailEnabled:false,ccFilterNaming:false,capacityAdapter:Object.fromEntries(['vision','pdf','audioInput','videoInput'].map(k=>[k,{enabled:false,models:[]}]))},providerConnections:n.connections.map((c,i)=>({id:c.id,provider:'codex',authType:'oauth',name:'synthetic native',priority:i+1,isActive:true,accessToken:'synthetic_access_'+i,expiresAt:new Date(c.expiresAt).toISOString(),providerSpecificData:{chatgptAccountId:'synthetic_workspace_'+i}})),apiKeys:[{id:'synthetic_ingress',key:'synthetic_gateway_key',name:'synthetic ingress',isActive:true}],combos:[{id:'native_route',name:'gaffer_native',models:['cx/gpt-6-astra']}]};
assert.equal(await a.replace(a.state().generation,'native_revision',project(config),()=>repository.importDb(config)),true);a.startEvaluation();
const state=a.state(),p={schema:3,routerId:'native_router',routeId:'native_route',revision:state.revision,epoch:1,routerModel:'gaffer_native',profile:'router-native-responses-local-v1',evidence:n.evidence,liveAdmission:false,graph:a.snapshot(),limits:{...n.local,outputTokens:null},authority:{deploymentId:n.deployment.id,boot:state.boot,generation:state.generation,revision:state.revision,graphDigest:digest(a.snapshot())},native:n};
const bridge=new RouterAuthority(a,p),gate=await PolicyGate.open('/tmp/native-boundary-journal',bridge,1000);await gate.activate();
const sends=[],decisions=[],translated=[];const finalize=gate.finalize.bind(gate);gate.finalize=async(...args)=>{if(scenario==='altered-translation')args[4]=events(false).at(-1).response.output;await finalize(...args);decisions.push({id:args[0],at:Date.now()});};
const backend=createServer(async(req,res)=>{
 let text='';for await(const c of req)text+=c;const body=JSON.parse(text);sends.push({path:req.url,body});
 assert.equal(req.url,'/responses');assert.equal(body.model,'gpt-6-astra');assert.deepEqual(body.reasoning,{effort:'xhigh',summary:'auto'});assert.equal(body.store,false);assert(['max_tokens','max_output_tokens','max_completion_tokens'].every(k=>!Object.hasOwn(body,k)));
 res.writeHead(200,{'content-type':'text/event-stream'});const bytes=Buffer.from(frames(events(sends.length===1)));for(let i=0;i<bytes.length;i+=13)res.write(bytes.subarray(i,i+13));res.end();
});backend.listen(47771,'127.0.0.1');await once(backend,'listening');
const upstream=createServer(async(req,res)=>{const signal=new AbortController();res.on('close',()=>{if(!res.writableEnded)signal.abort();});try{const result=await handleChat(new Request('http://127.0.0.1'+req.url,{method:'POST',headers:req.headers,body:Readable.toWeb(req),duplex:'half',signal:signal.signal}));res.writeHead(result.status,Object.fromEntries(result.headers));if(result.body)for await(const c of result.body){translated.push(Buffer.from(c).toString());res.write(c);}res.end();}catch{res.destroy();}});upstream.listen('/tmp/native-router.sock');await once(upstream,'listening');
const boundary=new Boundary(gate,'/tmp/native-router.sock','synthetic_gateway_key');await boundary.listen('/tmp/native-boundary.sock');
const binding={attemptId:'native_attempt',grantId:'native_grant',taskId:'native_task',leaseId:'native_lease',fence:1,role:'worker',routerId:p.routerId,routeId:p.routeId,revision:p.revision,epoch:1,expiresAt:Date.now()+20000,leaseExpiresAt:Date.now()+20000,native:{profileDigest:digest(n),scopeId:n.scope.id,authorizationDigest:n.scope.authorizationDigest}};if(scenario==='slow-grant')binding.expiresAt=Date.now()+1500;const token=boundary.issue(binding);
async function send(body){const bytes=JSON.stringify(body),call=httpRequest({socketPath:'/tmp/native-boundary.sock',path:'/v1/responses',method:'POST',headers:{host:'localhost',authorization:'Bearer '+token,'content-type':'application/json','content-length':Buffer.byteLength(bytes)}});const result=once(call,'response');call.end(bytes);const [r]=await result;let text='',firstAt=null;for await(const c of r){firstAt??=Date.now();text+=c;}return {status:r.statusCode,text,firstAt,id:r.headers['x-gaffer-request-id']};}
const first=request(),r1=await send(first);
if(scenario!=='roundtrip'){
 if(!['receipt-write','altered-translation'].includes(scenario))assert.notEqual(r1.status,200);assert(!r1.text.includes('event: response.'));
 const auditDeadline=Date.now()+1500;while(!boundary.audit.length){assert(Date.now()<auditDeadline);await new Promise(r=>setTimeout(r,5));}assert.equal(decisions.length,0);
 const journal=gate.snapshot();assert(!journal.decisions.some(d=>d.verdict==='validated_success'));
 const scope=a.evaluationScope(n.scope.id);const receipts=[...journal.reservations,...journal.decisions].map(r=>a.receipt(r.requestId));
 if(scenario.startsWith('slow-')){assert.equal(fault.proof.triggered,1);assert.equal(fault.proof.committed,true);assert.equal(sends.length,0);assert.equal(scope.spent,1);assert.equal(receipts[0].operations[0].terminal,'unknown');assert.equal(journal.reservations.length,1);}
 else if(['admission-write','debit-write'].includes(scenario)){assert(fault.proof.triggered>0);assert.equal(sends.length,0);assert.equal(scope.spent,0);}
 else if(scenario.startsWith('override-')){assert.equal(sends.length,0);assert.equal(scope.spent,0);}
 else if(scenario==='receipt-write'){assert(fault.proof.triggered>0);assert.equal(sends.length,1);assert.equal(scope.spent,1);assert.equal(receipts[0].operations[0].terminal,'unknown');}
 else if(scenario==='altered-translation'){assert.equal(sends.length,1);assert.equal(journal.reservations.length,0);assert.equal(journal.decisions[0].verdict,'quiescent_failure');}
 else throw Error('unknown_scenario');
 console.log(JSON.stringify({scenario,evidence:'isolated-pinned-router-native-failure',live:false,physicalSends:sends,scope,receipts,journal,fault:fault.proof,result:'passed'}));
 await boundary.close();upstream.closeAllConnections();backend.closeAllConnections();process.exit(0);
}
assert.equal(r1.status,200);assert.match(r1.text,/response.completed/);assert(r1.firstAt>=decisions[0].at);
const output=events(true).at(-1).response.output,next={...first,input:[...first.input,...continuationOutput(output,n),{type:'function_call_output',call_id:'call_synthetic',output:'Success. Updated greeting.txt'}]};
const r2=await send(next);assert.equal(r2.status,200);assert.match(r2.text,/Completed 🌍 café/);assert(r2.firstAt>=decisions[1].at);assert.equal(sends.length,2);assert.equal(a.evaluationScope(n.scope.id).spent,2);{const until=Date.now()+1000;while(boundary.audit.length<2){assert(Date.now()<until);await new Promise(r=>setTimeout(r,5));}}assert.equal(gate.snapshot().reservations.length,0);assert(gate.snapshot().decisions.every(d=>d.verdict==='validated_success'&&d.nativeOutput));
const r3=await send(next);assert.notEqual(r3.status,200);assert.equal(sends.length,2);
console.log(JSON.stringify({scenario,evidence:'isolated-pinned-router-native-responses-synthetic',live:false,policy:p,physicalSends:sends,receipts:gate.snapshot().decisions.map(d=>a.receipt(d.requestId)),decisions:gate.snapshot().decisions,scope:a.evaluationScope(n.scope.id),completionTimes:[r1.firstAt,r2.firstAt],decisionTimes:decisions}));
await boundary.close();upstream.closeAllConnections();backend.closeAllConnections();await new Promise(r=>upstream.close(r));await new Promise(r=>backend.close(r));process.exit(0);
