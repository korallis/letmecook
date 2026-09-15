import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp,readFile,rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { PolicyGate } from './policy.ts';
import { RouterAuthority } from '../router-boundary-bridge/authority.ts';
import { routerPolicyFixture,routerBinding,matchingReceipt } from './router-fixture.ts';
import { validatePolicy,fixturePolicy,TOOL } from './types.ts';
import { hashDocument,classifyReceipt } from './router-policy.ts';
import { ChatStream,validateRequest } from './protocol.ts';

async function setup(native = false,ms = 150) {
  const p=routerPolicyFixture(native), binding=routerBinding(p);
  let phase='active',generation=p.authority.generation,graph=structuredClone(p.graph),calls=0,cancels=0;
  const receipts=new Map<string,unknown>();
  const control={state:()=>({boot:p.authority.boot,generation,revision:p.revision,phase,policy:p.graph}),snapshot:()=>graph,receipt:(id:string)=>receipts.get(id) ?? {id,known:false,quiescent:false},quiescent:(ids:string[])=>{calls++;return ids.every(id=>(receipts.get(id) as any)?.quiescent !== false);},cancel:()=>{cancels++;}};
  const a=new RouterAuthority(control,p),dir=await mkdtemp(join(tmpdir(),'gaffer-router-gate-'));
  const gate=await PolicyGate.open(dir,a,ms);await gate.activate();
  const admit=()=>gate.admit(binding,()=>true,undefined,hashDocument('request'));
  return {p,binding,gate,a,dir,receipts,admit,get calls(){return calls;},get cancels(){return cancels;},phase:(s:string)=>{phase=s;},generation:()=>{generation++;},graph:()=>{graph={...graph,liveAdmission:true};},async close(){await gate.close();await rm(dir,{recursive:true,force:true});}};
}
test('schema-2 exact graph, envelope, local epoch separation and fixture preservation',()=>{
  assert.equal(validatePolicy(fixturePolicy()).schema,1);
  for(const native of [false,true]) { const p=routerPolicyFixture(native);assert.equal(validatePolicy(p).schema,2);assert.notEqual(p.epoch,p.authority.generation);
    for(const mutate of [(x:any)=>x.liveAdmission=true,(x:any)=>x.extra=1,(x:any)=>x.graph.routes[0].edges[0].connections.push('unapproved'),(x:any)=>x.authority.graphDigest='0'.repeat(64),(x:any)=>x.envelope.cap='drop',(x:any)=>x.graph.routes.push(x.graph.routes[0]),(x:any)=>x.limits.outputTokens=1025]) {const bad=structuredClone(p);mutate(bad);assert.throws(()=>validatePolicy(bad));}
  }
});
test('exact cap mapping and unsupported request controls reject before dispatch',()=>{
  const p=routerPolicyFixture(true);const body={model:p.routerModel,messages:[{role:'user',content:'fixture'}],stream:true,max_completion_tokens:32,tools:[TOOL]};
  const mapped=JSON.parse(validateRequest(body,p));assert.equal(mapped.max_tokens,32);assert(!Object.hasOwn(mapped,'max_completion_tokens'));
  for(const more of [{tool_choice:'auto'},{temperature:0},{stream_options:{include_usage:true}},{max_tokens:32},{max_completion_tokens:1025},{tools:[{...TOOL,function:{...TOOL.function,strict:true}}]},{messages:[{role:'assistant',content:''}]}])assert.throws(()=>validateRequest({...body,...more},p));
});
test('native translated EOF prepares completion only in explicitly receipt-gated codec',()=>{
  const raw=Buffer.from('data: {"choices":[{"index":0,"delta":{"content":"fixture"},"finish_reason":"stop"}]}\n\n');
  const old=new ChatStream('id','model',false);old.push(raw);assert.throws(()=>old.end());
  const native=new ChatStream('id','model',false,'receipt-gated-eof');native.push(raw);assert(native.end().join('').includes('[DONE]'));
});
test('strict receipts separate zero-operation failure, native failure, refresh, and final inference',()=>{
  const p=routerPolicyFixture(true),id='a'.repeat(32),saved={policy:p,binding:routerBinding(p),requestDigest:hashDocument('req'),send:'send_possible' as const};
  const receipt=matchingReceipt(p,id);assert.equal(classifyReceipt(receipt,id,saved).disposition,'original_success');
  const zero={...receipt,operations:[]};assert.equal(classifyReceipt(zero,id,saved).disposition,'quiescent_failure');
  for(const terminal of ['provider_failed','provider_incomplete','provider_rejected'])assert.equal(classifyReceipt(matchingReceipt(p,id,terminal),id,saved).disposition,'quiescent_failure');
  const refresh={...receipt,operations:[{...receipt.operations[0],model:'credential_refresh',terminal:'provider_refresh_terminal'}]};assert.equal(classifyReceipt(refresh,id,saved).disposition,'quiescent_failure');
  const unknown=matchingReceipt(p,id,'unknown');unknown.operations.push({...receipt.operations[0],ordinal:2});assert.equal(classifyReceipt(unknown,id,saved).disposition,'pending_or_unknown');
  for(const mutate of [(r:any)=>r.id='bad',(r:any)=>r.route='bad',(r:any)=>r.boot='bad',(r:any)=>r.generation++,(r:any)=>r.revision='bad',(r:any)=>r.operations[0].ordinal=2,(r:any)=>r.operations.push(r.operations[0]),(r:any)=>r.operations[0].connection_id='bad',(r:any)=>r.operations[0].local_stop='running',(r:any)=>r.quiescent=false]){const r=structuredClone(receipt);mutate(r);assert.equal(classifyReceipt(r,id,saved).disposition,'pending_or_unknown');}
});
test('durable send and receipt decision precede release, delay never queries unknown quiescence',async()=>{
  const f=await setup();try{const {reservation:r}=await f.admit();let disk=JSON.parse(await readFile(join(f.dir,'state.json'),'utf8'));assert.equal(disk.reservations[0].router.send,'reserved');await f.gate.markSend(r.requestId);disk=JSON.parse(await readFile(join(f.dir,'state.json'),'utf8'));assert.equal(disk.reservations[0].router.send,'send_possible');const before=f.calls;
    const pending=f.gate.finalize(r.requestId,hashDocument('completion'),()=>true,AbortSignal.timeout(1000));
    await new Promise(resolve=>setTimeout(resolve,30));assert.equal(f.gate.snapshot().decisions!.length,0);assert.equal(f.calls,before);f.receipts.set(r.requestId,matchingReceipt(f.p,r.requestId));await pending;
    f.gate.assertRelease(r.requestId);disk=JSON.parse(await readFile(join(f.dir,'state.json'),'utf8'));assert.equal(disk.decisions[0].verdict,'validated_success');const evidence=disk.decisions[0].evidence;
    f.receipts.set(r.requestId,{...matchingReceipt(f.p,r.requestId),local_stop:'cancelled_unknown'});await f.gate.finish(r.requestId,'cancelled_unknown');assert.deepEqual(f.gate.snapshot().decisions![0].evidence,evidence);assert.equal(f.gate.snapshot().decisions![0].delivery,'cancelled_unknown');
  }finally{await f.close();}
});
test('closed response diagnostics preserve legacy classification and never qualify unknown work',()=>{
 const p=routerPolicyFixture(true),id='a'.repeat(32),saved={policy:p,binding:routerBinding(p),requestDigest:hashDocument('req'),send:'send_possible' as const};
 for(const terminal of ['provider_completed','provider_rejected','unknown']){
  const legacy=matchingReceipt(p,id,terminal),expected=classifyReceipt(legacy,id,saved).disposition;
  for(const observation of [null,{status:200,mediaType:'sse',bodyPresent:true},{status:204,mediaType:'html',bodyPresent:false}]){
   const current=structuredClone(legacy);(current.operations[0] as any).response_observation=observation;
   assert.equal(classifyReceipt(current,id,saved).disposition,expected);
  }
 }
 for(const observation of [{status:0,mediaType:'sse',bodyPresent:true},{status:600,mediaType:'sse',bodyPresent:true},{status:200.1,mediaType:'sse',bodyPresent:true},{status:200,mediaType:'reflected_private_value',bodyPresent:true},{status:200,mediaType:'sse',bodyPresent:1},{status:200,mediaType:'sse',bodyPresent:true,headers:'private'},{status:200,mediaType:'sse'},'private']){
  const receipt=matchingReceipt(p,id);(receipt.operations[0] as any).response_observation=observation;
  assert.equal(classifyReceipt(receipt,id,saved).disposition,'pending_or_unknown');
 }
});
test('new and legacy completed decisions reopen with their original diagnostic bytes',async()=>{
 for(const observation of [undefined,null,{status:200,mediaType:'sse',bodyPresent:true}]){
  const f=await setup();try{
   const {reservation:r}=await f.admit();await f.gate.markSend(r.requestId);const receipt=matchingReceipt(f.p,r.requestId);
   if(observation!==undefined)(receipt.operations[0] as any).response_observation=observation;
   f.receipts.set(r.requestId,receipt);await f.gate.finalize(r.requestId,hashDocument('out'),()=>true,AbortSignal.timeout(500));
   const evidence=f.gate.snapshot().decisions![0].evidence;await f.gate.close();
   const reopened=await PolicyGate.open(f.dir,f.a);try{assert.deepEqual(reopened.snapshot().decisions![0].evidence,evidence);}finally{await reopened.close();}
  }finally{await rm(f.dir,{recursive:true,force:true});}
 }
});
test('ordinary drain preserves current completion but denies new admission; fencing prevents release',async()=>{
 const f=await setup();try{const {reservation:r}=await f.admit();await f.gate.markSend(r.requestId);f.receipts.set(r.requestId,matchingReceipt(f.p,r.requestId));f.phase('draining');await assert.rejects(f.admit());await f.gate.finalize(r.requestId,hashDocument('out'),()=>true,AbortSignal.timeout(500));f.gate.assertRelease(r.requestId);f.generation();assert.throws(()=>f.gate.assertRelease(r.requestId));}finally{await f.close();}
});
test('changed graph closes admission and missing authority never substitutes fixture success',async()=>{const f=await setup();try{f.graph();await assert.rejects(f.admit());}finally{await f.close();}});
test('uncertain send cannot be erased by legacy neverForwarded or restart',async()=>{
 const f=await setup();try{const {reservation:r}=await f.admit();await f.gate.markSend(r.requestId);assert.equal(await f.gate.finish(r.requestId,'failed_before_output',true),false);await f.gate.close();const reopened=await PolicyGate.open(f.dir,f.a);try{await assert.rejects(reopened.activate());assert.equal(reopened.snapshot().reservations[0].router!.send,'send_possible');}finally{await reopened.close();}}finally{await rm(f.dir,{recursive:true,force:true});}
});
test('reserved no-send recovery is distinct from an absent receipt after possible send',async()=>{const f=await setup();try{await f.admit();await f.gate.close();const reopened=await PolicyGate.open(f.dir,f.a);try{await reopened.activate();assert.equal(reopened.snapshot().decisions![0].verdict,'never_sent');assert.equal(reopened.snapshot().reservations.length,0);}finally{await reopened.close();}}finally{await rm(f.dir,{recursive:true,force:true});}});
test('receipt timeout and direct cancellation retain uncertainty and bound shutdown',async()=>{const f=await setup(false,30);try{const {reservation:r}=await f.admit();await f.gate.markSend(r.requestId);const pending=f.gate.finalize(r.requestId,hashDocument('out'),()=>true,AbortSignal.timeout(1000));f.gate.cancel(r.requestId);assert.equal(f.cancels,1);await assert.rejects(pending);assert.equal(f.gate.snapshot().decisions!.length,0);assert.equal(f.gate.snapshot().reservations.length,1);}finally{await f.close();}});
test('read-only replacement refuses without control mutation, even when drained',async()=>{const f=await setup();try{await assert.rejects(f.gate.drainAndReplace({...f.p,epoch:2,revision:'policy_2'}),/replacement_read_only/);assert.equal(f.gate.snapshot().phase,'closed');assert.equal(f.gate.snapshot().policy!.revision,'policy_1');}finally{await f.close();}});
test('router compatible codec normalizes only one duplicate DONE and fixture remains strict',()=>{
 const stream='data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\ndata: [DONE]\n\n';
 const fixture=new ChatStream('id','model',false);assert(fixture.push(Buffer.from(stream)).error);
 const router=new ChatStream('id','model',false,'router-done');assert(!router.push(Buffer.from(stream)).error);assert(router.end().join('').endsWith('data: [DONE]\n\n'));
 const excess=new ChatStream('id','model',false,'router-done');assert(excess.push(Buffer.from(stream+'data: [DONE]\n\n')).error);
});
test('schema-2 historical call IDs use the router whole-string length bound',()=>{
 const p=routerPolicyFixture();for(const length of [64,65]){const id='call_'+'x'.repeat(length-5);const body={model:p.routerModel,stream:true,max_completion_tokens:32,messages:[{role:'assistant',content:null,tool_calls:[{id,type:'function',function:{name:'read_file',arguments:'{"path":"fixture.txt"}'}}]},{role:'tool',tool_call_id:id,content:'fixture'}]};if(length===64)assert.doesNotThrow(()=>validateRequest(body,p));else assert.throws(()=>validateRequest(body,p));}
});
test('impossible finished receipt lifecycle and inactive connections cannot attest completion',()=>{
 const p=routerPolicyFixture(),id='b'.repeat(32),saved={policy:p,binding:routerBinding(p),requestDigest:hashDocument('req'),send:'send_possible' as const};const r=matchingReceipt(p,id);
 for(const local_stop of ['running','crash_unknown'])assert.equal(classifyReceipt({...r,local_stop},id,saved).disposition,'pending_or_unknown');
 p.graph.connections[0].isActive=false;p.authority.graphDigest=hashDocument(p.graph);assert.equal(classifyReceipt(r,id,saved).disposition,'pending_or_unknown');
});
test('router cancellation after decision persistence denies new release without erasing evidence',async()=>{const f=await setup();try{const {reservation:r}=await f.admit();await f.gate.markSend(r.requestId);f.receipts.set(r.requestId,matchingReceipt(f.p,r.requestId));await f.gate.finalize(r.requestId,hashDocument('out'),()=>true,AbortSignal.timeout(500));const evidence=f.gate.snapshot().decisions![0].evidence;f.receipts.set(r.requestId,{...matchingReceipt(f.p,r.requestId),local_stop:'cancelled_unknown'});assert.throws(()=>f.gate.assertRelease(r.requestId),/cancelled/);assert.deepEqual(f.gate.snapshot().decisions![0].evidence,evidence);}finally{await f.close();}});
test('failed journal write cannot expose volatile success in the trusted snapshot',async()=>{
 const f=await setup();const {mkdir}=await import('node:fs/promises');let obstruction='';try{const {reservation:r}=await f.admit();await f.gate.markSend(r.requestId);f.receipts.set(r.requestId,matchingReceipt(f.p,r.requestId));const owner=JSON.parse(await readFile(join(f.dir,'owner.lock'),'utf8')).owner;obstruction=join(f.dir,`state.${owner}.next`);await mkdir(obstruction);await assert.rejects(f.gate.finalize(r.requestId,hashDocument('out'),()=>true,AbortSignal.timeout(500)));assert.equal(f.gate.snapshot().decisions!.length,0);assert.throws(()=>f.gate.assertRelease(r.requestId));const disk=JSON.parse(await readFile(join(f.dir,'state.json'),'utf8'));assert.equal(disk.decisions.length,0);assert.equal(disk.reservations.length,1);}finally{await rm(obstruction,{recursive:true,force:true});await f.close();}
});
test('extra semantic content after duplicated DONE remains invalid',()=>{const p=new ChatStream('id','model',false,'router-done');const text='data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\ndata: {"choices":[{"index":0,"delta":{"content":"late"}}]}\n\n';assert(p.push(Buffer.from(text)).error);});
test('decision and live reservation with the same ID require identical durable joins',async()=>{
 const f=await setup();try{const {reservation:r}=await f.admit();await f.gate.markSend(r.requestId);f.receipts.set(r.requestId,matchingReceipt(f.p,r.requestId));await f.gate.finalize(r.requestId,hashDocument('out'),()=>true,AbortSignal.timeout(500));await f.gate.close();const path=join(f.dir,'state.json'),state=JSON.parse(await readFile(path,'utf8'));state.decisions[0].taskId='other_task';state.decisions[0].router.binding.taskId='other_task';const {writeFile}=await import('node:fs/promises');await writeFile(path,JSON.stringify(state));await assert.rejects(PolicyGate.open(f.dir,f.a),/boundary_closed/);}finally{await rm(f.dir,{recursive:true,force:true});}
});
test('schema-1 fixture history cannot be reinterpreted by router-backed activation',async()=>{
 const f=await setup();await f.gate.close();const {writeFile}=await import('node:fs/promises');const policy=fixturePolicy();const {fingerprint}=await import('./types.ts');await writeFile(join(f.dir,'state.json'),JSON.stringify({schema:1,generation:'fixture',phase:'closed',policy,fingerprint:fingerprint(policy),reservations:[],counts:{attempt_1:1}}));const gate=await PolicyGate.open(f.dir,f.a);try{await assert.rejects(gate.activate(),/policy_denied/);assert.equal(gate.snapshot().schema,1);assert.equal(gate.snapshot().counts.attempt_1,1);}finally{await gate.close();await rm(f.dir,{recursive:true,force:true});}
});
test('failure after rename before directory sync gives no release acknowledgement and reopens without replay',async()=>{
 const f=await setup();try{const {reservation:r}=await f.admit();await f.gate.markSend(r.requestId);f.receipts.set(r.requestId,matchingReceipt(f.p,r.requestId));(f.gate as any).syncDirectory=async()=>{throw new Error('injected_parent_sync_failure');};await assert.rejects(f.gate.finalize(r.requestId,hashDocument('out'),()=>true,AbortSignal.timeout(500)));assert.equal(f.gate.snapshot().decisions!.length,0);assert.throws(()=>f.gate.assertRelease(r.requestId));const disk=JSON.parse(await readFile(join(f.dir,'state.json'),'utf8'));assert.equal(disk.decisions[0].verdict,'validated_success');assert.equal(disk.decisions[0].delivery,'unobserved');await assert.rejects(f.gate.close());
 // The closed test owner has no outstanding writer. Simulate supervised offline
 // lock recovery, then validate existing bytes rather than replaying a request.
 await rm(join(f.dir,'owner.lock'));const reopened=await PolicyGate.open(f.dir,f.a);try{await reopened.activate();assert.equal(reopened.snapshot().reservations.length,0);assert.equal(reopened.snapshot().decisions![0].delivery,'unobserved');assert.equal(reopened.snapshot().counts.attempt_1,1);}finally{await reopened.close();}
 }finally{await rm(f.dir,{recursive:true,force:true});}
});
test('a locally closed gate cannot begin sending, authorize completion, or release a prior decision',async()=>{
 for(const stage of ['reserved','sent','decided']){const f=await setup();try{const {reservation:r}=await f.admit();if(stage!=='reserved'){await f.gate.markSend(r.requestId);f.receipts.set(r.requestId,matchingReceipt(f.p,r.requestId));}if(stage==='decided')await f.gate.finalize(r.requestId,hashDocument('out'),()=>true,AbortSignal.timeout(500));await assert.rejects(f.gate.drainAndReplace({...f.p,epoch:2,revision:'policy_2'}));if(stage==='reserved')await assert.rejects(f.gate.markSend(r.requestId));else if(stage==='sent')await assert.rejects(f.gate.finalize(r.requestId,hashDocument('out'),()=>true,AbortSignal.timeout(500)));else assert.throws(()=>f.gate.assertRelease(r.requestId));}finally{await f.close();}}
});
