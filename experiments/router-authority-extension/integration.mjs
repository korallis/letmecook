import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { Readable } from 'node:stream';
import { readFileSync, existsSync } from 'node:fs';
const nativeFetch = globalThis.fetch;
if (!existsSync('/.dockerenv')) throw new Error('isolated_container_required');
const { handleChat } = await import('/router-source/src/sse/handlers/chat.js');
const { getAdapter, getAdapterSync } = await import('/router-source/src/lib/db/driver.js');
const repository = await import('/router-source/src/lib/db/index.js');
const { authority, project } = await import('/router-source/gaffer-extension/authority.mjs');
await getAdapter();
const a = authority();
const evidence = [];
const report = (test, detail) => evidence.push({test, result:'passed', ...detail});
const mode = process.argv[2] || 'main';

if (mode === 'restart') {
  assert.equal(a.state().phase,'closed');
  assert.equal(a.receipt('crash_request').quiescent,false);
  assert.equal(a.receipt('never_observed').quiescent,false);
  console.log(JSON.stringify({scenario:mode,phase:a.state().phase,crashReceipt:a.receipt('crash_request')}));
  process.exit(0);
}

let upstreamMode = 'success'; let sendCount = 0; let releaseHeld;
const terminal = model => 'data: '+JSON.stringify({id:'synthetic_completion',model,object:'chat.completion.chunk',choices:[{index:0,delta:{content:'synthetic'},finish_reason:null}]})+'\n\n' +
  'data: '+JSON.stringify({id:'synthetic_completion',model,object:'chat.completion.chunk',choices:[{index:0,delta:{},finish_reason:'stop'}]})+'\n\ndata: [DONE]\n\n';
const backend = createServer(async (req,res) => {
  const chunks=[]; for await (const chunk of req) chunks.push(chunk);
  const body=JSON.parse(Buffer.concat(chunks).toString());
  sendCount++;
  if (upstreamMode === 'hang') return;
  if(upstreamMode==='hold'){releaseHeld=()=>{res.writeHead(200,{'content-type':'text/event-stream'});res.end(terminal(body.model));};return;}
  if (upstreamMode === 'reject-first' && req.headers.authorization === 'Bearer synthetic_account_1') {
    res.writeHead(429,{'content-type':'application/json'}); res.end(JSON.stringify({error:{message:'synthetic quota exhausted'}})); return;
  }
  if (upstreamMode === 'retry') { res.writeHead(502,{'content-type':'application/json'}); res.end(JSON.stringify({error:{message:'synthetic retry'}})); return; }
  if (upstreamMode === 'redirect') { res.writeHead(307,{location:'http://127.0.0.1:1/not-authorized'}); res.end(); return; }
  res.writeHead(200,{'content-type':'text/event-stream'});
  if(upstreamMode==='identity-drift'){res.end(terminal(body.model).replace('"id":"synthetic_completion","model":"'+body.model+'","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},','"id":"different_response","model":"different_model","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},'));return;}
  if(upstreamMode==='error-with-finish'){res.end('data: '+JSON.stringify({id:'synthetic_completion',model:body.model,object:'chat.completion.chunk',error:{message:'synthetic failure'},choices:[{index:0,delta:{},finish_reason:'stop'}]})+'\n\ndata: [DONE]\n\n');return;}
  if(upstreamMode==='incomplete-tool'){res.end('data: '+JSON.stringify({id:'synthetic_completion',model:body.model,object:'chat.completion.chunk',choices:[{index:0,delta:{tool_calls:[{index:0,id:'call_synthetic',type:'function',function:{name:'read_file',arguments:'{"path":'}}]},finish_reason:'tool_calls'}]})+'\n\ndata: [DONE]\n\n');return;}
  res.end(upstreamMode === 'partial' ? terminal(body.model).split('\n\n')[0]+'\n\n' : terminal(body.model));
});
await new Promise(resolve=>backend.listen(0,'127.0.0.1',resolve));
const baseUrl=`http://127.0.0.1:${backend.address().port}/v1`;
const provider='openai-compatible-synthetic';
const config = {
  settings:{requireApiKey:true,rtkEnabled:false,headroomEnabled:false,pxpipeEnabled:false,cavemanEnabled:false,ponytailEnabled:false,ccFilterNaming:false,capacityAdapter:Object.fromEntries(['vision','pdf','audioInput','videoInput'].map(k=>[k,{enabled:false,models:[]}]))},
  providerNodes:[{id:provider,type:'openai-compatible',name:'synthetic node',prefix:provider,baseUrl,apiType:'chat'}],
  providerConnections:[1,2].map(n=>({id:`synthetic_account_${n}`,provider,authType:'apikey',name:`synthetic account ${n}`,priority:n,isActive:true,apiKey:`synthetic_account_${n}`,providerSpecificData:{baseUrl,apiType:'chat'}})),
  apiKeys:[{id:'synthetic_ingress',key:'synthetic_gateway_key',name:'synthetic ingress',isActive:true}],
  combos:[{id:'synthetic_route',name:'gaffer-synthetic',models:[`${provider}/synthetic-model`]}]
};
const activate = async (config, revision) => a.replace(a.state().generation,revision,project(config),()=>repository.importDb(config));
assert.equal(await activate(config,'revision_1'),true);
assert.equal(getAdapterSync().raw,undefined);
const settings = a.snapshot(); assert.equal(settings.settings.adapters,false);
report('real_router_sqlite_bootstrap',{driver:getAdapterSync().driver,phase:a.state().phase,profile:settings.profile});
if(mode==='unknown'){
  assert.equal(a.quiescent(['never_observed']),false);
  assert.equal(await activate(config,'revision_unknown_replacement'),false);assert.equal(a.state().phase,'draining');
  console.log(JSON.stringify({scenario:mode,unknownIdBlocksReplacement:true,receipt:a.receipt('never_observed')}));
  await new Promise(resolve=>backend.close(resolve));process.exit(0);
}

const gateway = createServer(async (req,res) => {
  const cancel = new AbortController();
  res.on('close',()=>{if(!res.writableEnded) cancel.abort();});
  const request = new Request(`http://127.0.0.1${req.url}`,{method:'POST',headers:req.headers,body:Readable.toWeb(req),duplex:'half',signal:cancel.signal});
  const response = await handleChat(request);
  res.writeHead(response.status,Object.fromEntries(response.headers));
  try { if(response.body) for await(const chunk of response.body) res.write(chunk); res.end(); } catch { res.destroy(); }
});
await new Promise(resolve=>gateway.listen(0,'127.0.0.1',resolve));
const origin=`http://127.0.0.1:${gateway.address().port}`;
const headers = id => ({'content-type':'application/json',authorization:'Bearer synthetic_gateway_key','x-gaffer-request-id':id,'x-gaffer-generation':String(a.state().generation),'x-gaffer-revision':a.state().revision});
const body = {model:'gaffer-synthetic',stream:true,max_tokens:64,messages:[{role:'user',content:'synthetic test'}]};
const request = async (id, extra={}) => nativeFetch(origin+'/v1/chat/completions',{method:'POST',headers:headers(id),body:JSON.stringify(body),...extra});
const waitUntil = async predicate => { const until=Date.now()+5000; while(!predicate()) {if(Date.now()>until)throw new Error('wait_timeout'); await new Promise(resolve=>setTimeout(resolve,10));} };
const completed = await request('complete_request'); assert.equal(completed.status,200); assert.match(await completed.text(),/synthetic/);
assert.equal(a.receipt('complete_request').quiescent,true); assert.equal(a.receipt('complete_request').operations[0].terminal,'provider_terminal');
report('original_terminal_through_stock_chat',{receipt:a.receipt('complete_request')});
for(const kind of ['oversized','stalled']) {
  const before=sendCount;
  const incoming=new ReadableStream({start(controller){controller.enqueue(new TextEncoder().encode(kind==='oversized'?'x'.repeat(70000):'{'));if(kind==='oversized')controller.close();}});
  const rejected=await request(`ingress_${kind}`,{body:incoming,duplex:'half'});assert.equal(rejected.status,503);await rejected.text();
  assert.equal(sendCount,before);assert.equal(a.receipt(`ingress_${kind}`).known,false);
}
report('bounded_streaming_ingress',{oversizedAndStalledDeniedBeforeUpstream:true});

for (const call of [()=>repository.updateCombo('synthetic_route',{models:[`${provider}/different`]}),()=>repository.updateSettings({capacityAdapter:{}}),()=>repository.importDb(config),()=>getAdapterSync().run("UPDATE gaffer_authority SET phase='active'"),()=>getAdapterSync().get("UPDATE combos SET name='bypass' RETURNING *"),()=>getAdapterSync().exec('DELETE FROM combos')]) await assert.rejects(async()=>call());
assert.equal(a.state().phase,'active');
await repository.updateProviderConnection('synthetic_account_1',{testStatus:'active',lastUsedAt:new Date().toISOString()});
await assert.rejects(()=>repository.updateProviderConnection('synthetic_account_1',{projectId:'changed-project'}));
await assert.rejects(()=>repository.updateProviderConnection('synthetic_account_1',{apiKey:'different_account_key'}),/credential_writer_requires_drain/);
await assert.rejects(async()=>getAdapterSync().all('SELECT * FROM GAFFER_AUTHORITY'),/read_only_query_required/);
assert.deepEqual(a.snapshot(),settings);
report('stock_repository_import_and_raw_entrypoint_guards',{healthWriteAllowed:true,policyChangingMetadataDenied:true});
await assert.rejects(async()=>getAdapterSync().run("INSERT INTO kv(scope,key,value) VALUES('settings','comboStrategy',?)",['"fallback"']),/unsupported_kv_scope/);
await assert.rejects(()=>repository.updateSettings({comboStrategy:'fusion'}),/unsupported_strategy/);
assert.equal((await repository.getSettings()).comboStrategy,'fallback');
report('regression_kv_shadow_cannot_mask_policy_writer',{forgedScopeRolledBack:true,effectiveStrategy:'fallback'});

const heldHeaders=headers('delayed_ingress'); const oldGeneration=a.state().generation;
const changed=structuredClone(config); changed.combos[0].models=[`${provider}/synthetic-model-2`];
assert.equal(await activate(changed,'revision_2'),true);
const delayed=await request('delayed_ingress',{headers:heldHeaders}); assert.equal(delayed.status,503); await delayed.text();
assert.equal(a.receipt('delayed_ingress').known,false); assert.equal(a.receipt('delayed_ingress').quiescent,false);
await assert.rejects(()=>a.replace(oldGeneration,'stale_revision',project(config),()=>repository.importDb(config)));
report('delayed_ingress_stale_generation',{unknownNeverQuiescent:true});

upstreamMode='reject-first'; const fallback=await request('fallback_request'); assert.equal(fallback.status,200); await fallback.text();
assert.equal(a.receipt('fallback_request').operations.length,2);
assert.deepEqual(a.receipt('fallback_request').operations.map(o=>o.terminal),['provider_rejected','provider_terminal']);
report('stock_account_fallback',{receipt:a.receipt('fallback_request')});

if(mode==='crash') {
  upstreamMode='hang'; void request('crash_request').catch(()=>{});
  await waitUntil(()=>a.receipt('crash_request').operations?.length===1);
  console.log(JSON.stringify({scenario:mode,receipt:a.receipt('crash_request')}));
  process.kill(process.pid,'SIGKILL');
}

upstreamMode='retry'; const retry=request('retry_cancel'); await waitUntil(()=>a.receipt('retry_cancel').operations?.length===1);
const beforeCancel=sendCount; a.cancel('retry_cancel'); const retryResponse=await retry; await retryResponse.text();
assert.equal(sendCount,beforeCancel); assert.equal(a.receipt('retry_cancel').operations.length,1);
report('cancel_stops_base_retry',{receipt:a.receipt('retry_cancel'),sendCountAfterCancel:sendCount-beforeCancel});

// Completed provider rejection can settle even if cancellation prevents the next retry.
assert.equal(a.receipt('retry_cancel').quiescent,true);
upstreamMode='success';
let releaseWriter; const writerBlocked=new Promise(resolve=>{releaseWriter=resolve;});
let enteredWriter; const entered=new Promise(resolve=>{enteredWriter=resolve;});
const replacing=a.replace(a.state().generation,'revision_3',project(changed),async()=>{enteredWriter();await writerBlocked;await repository.importDb(changed);});
await entered;
const raced=await request('writer_race'); assert.equal(raced.status,503); await raced.text();
assert.equal(a.receipt('writer_race').known,false); releaseWriter(); assert.equal(await replacing,true);
report('admission_writer_race',{admissionDeniedDuringMutation:true});
upstreamMode='hold';const admittedBeforeDrain=request('active_drain');await waitUntil(()=>typeof releaseHeld==='function');
assert.equal(await activate(changed,'revision_3_drain'),false);assert.equal(a.state().phase,'draining');
const refusedDuringDrain=await request('new_during_drain');assert.equal(refusedDuringDrain.status,503);await refusedDuringDrain.text();
releaseHeld();const drained=await admittedBeforeDrain;assert.equal(drained.status,200);await drained.text();assert.equal(a.receipt('active_drain').quiescent,true);
assert.equal(await activate(changed,'revision_3_drain'),true);upstreamMode='success';
report('drain_preserves_admitted_work',{newAdmissionDenied:true,existingOriginalTerminal:true});
let releaseLate;const lateWait=new Promise(resolve=>{releaseLate=resolve;});let lateWrite;
assert.equal(await a.replace(a.state().generation,'revision_late',project(changed),async()=>{lateWrite=(async()=>{await lateWait;await repository.updateCombo('synthetic_route',{name:'late_write'});})();void lateWrite.catch(()=>{});}),true);
releaseLate();await assert.rejects(()=>lateWrite,/stale_writer/);
report('late_writer_cannot_outlive_generation',{lateWriteDenied:true});

const failedExpected=project(changed); failedExpected.routes[0].name='readback-mismatch';
await assert.rejects(()=>a.replace(a.state().generation,'revision_bad',failedExpected,()=>repository.importDb(changed)),/policy_readback_mismatch/);
assert.equal(a.state().phase,'closed');
assert.equal(await activate(changed,'revision_4'),true);
await assert.rejects(()=>a.replace(a.state().generation,'revision_throw',project(changed),async()=>{await repository.updateCombo('synthetic_route',{name:'uncertain-write'});throw new Error('simulated_mutation_failure');}),/simulated_mutation_failure/);
assert.equal(a.state().phase,'closed'); assert.equal(await activate(changed,'revision_5'),true);
report('mutation_exception_and_readback_failure',{recoveredOnlyByExplicitReplacement:true});

const buffered=await handleChat(new Request(origin+'/v1/chat/completions',{method:'POST',headers:headers('buffered_cancel'),body:JSON.stringify(body)}));
a.cancel('buffered_cancel');await assert.rejects(()=>buffered.text());
assert.equal(a.receipt('buffered_cancel').handler_done,1);
report('cancel_discards_queued_output',{newlyReadOutputBytes:0});

for(const malformed of ['identity-drift','error-with-finish','incomplete-tool']){
  upstreamMode=malformed;const id=malformed.replaceAll('-','_');await request(id).then(response=>response.text()).catch(()=>{});
  assert.equal(a.receipt(id).operations[0].terminal,'unknown');assert.equal(a.receipt(id).quiescent,false);
  report(`regression_chat_${id}`,{receipt:a.receipt(id)});
}
upstreamMode='partial'; const partial=await request('partial_request'); await partial.text();
assert.equal(a.receipt('partial_request').operations[0].terminal,'unknown'); assert.equal(a.receipt('partial_request').quiescent,false);
assert.equal(await activate(config,'revision_forbidden'),false); assert.equal(a.state().phase,'draining');
report('translated_eof_cannot_settle_partial',{receipt:a.receipt('partial_request'),replacementDenied:true});

const unsupported=structuredClone(config); delete unsupported.settings.capacityAdapter;
assert.throws(()=>project(unsupported),/capability_adapter_not_closed/);
unsupported.settings.capacityAdapter=config.settings.capacityAdapter; unsupported.combos[0].models=['cx/gpt-6-astra'];
assert.throws(()=>project(unsupported),/unsupported_native_model/);
report('hidden_default_and_unproven_native_profile_denied',{nativeOutputBoundUnsupported:true});
const mitm=structuredClone(config);mitm.providerNodes[0].baseUrl='https://api.individual.githubcopilot.com/v1';mitm.providerConnections.forEach(c=>{c.providerSpecificData.baseUrl=mitm.providerNodes[0].baseUrl;});
assert.throws(()=>project(mitm),/unsupported_mitm_transport/);
report('untracked_mitm_dispatch_graph_denied',{rejectedBeforeAdmission:true});
const outside=structuredClone(config);outside.providerNodes[0].baseUrl='https://unclassified.example/v1';outside.providerConnections.forEach(c=>{c.providerSpecificData.baseUrl=outside.providerNodes[0].baseUrl;});
assert.throws(()=>project(outside),/non_synthetic_endpoint/);
report('production_enrollment_not_inferred',{nonLoopbackDenied:true,liveAdmission:false});

await new Promise(resolve=>gateway.close(resolve)); await new Promise(resolve=>backend.close(resolve));
console.log(JSON.stringify({schema:1,source:JSON.parse(readFileSync(new URL('source-lock.json',import.meta.url))).commit,scenario:mode,realModules:['chat','chatCore','DefaultExecutor','BaseExecutor','proxyAwareFetch','combosRepo','connectionsRepo','importDb','nodeSqliteAdapter'],evidence}));
process.exit(0);
