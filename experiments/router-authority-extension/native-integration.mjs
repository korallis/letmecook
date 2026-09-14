import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { Readable } from 'node:stream';
import { existsSync } from 'node:fs';
import { frames, created, completed, failed, incomplete, nativeToolEvents } from './native-fixtures.mjs';
const nativeFetch=globalThis.fetch;
assert.ok(existsSync('/.dockerenv') && process.env.GAFFER_SYNTHETIC_NATIVE==='1');
const scenario=process.argv[2] || 'success';
const invalidTools=scenario.startsWith('tools-');
const {handleChat}=await import('/router-source/src/sse/handlers/chat.js');
const {getAdapter}=await import('/router-source/src/lib/db/driver.js');
const repository=await import('/router-source/src/lib/db/index.js');
const {authority,project}=await import('/router-source/gaffer-extension/authority.mjs');
await getAdapter(); const a=authority(); let sends=[]; let inferenceCount=0;
const backend=createServer(async(req,res)=>{
  const chunks=[];for await(const chunk of req)chunks.push(chunk);
  const body=JSON.parse(Buffer.concat(chunks).toString());
  sends.push({path:req.url,model:body.model ?? null,maxOutputFieldPresent:['max_tokens','max_output_tokens','max_completion_tokens'].some(k=>Object.hasOwn(body,k)),account:req.headers['chatgpt-account-id'] ?? null});
  if(req.url==='/token') {
    assert.equal(body.grant_type,'refresh_token');
    if(scenario==='refresh-cancel')return;
    res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({access_token:'synthetic_refreshed',refresh_token:'synthetic_rotated',expires_in:1000000}));return;
  }
  assert.equal(req.url,'/responses');assert.equal(body.stream,true);assert.equal(body.store,false);inferenceCount++;
  if(scenario==='preheaders-cancel' || scenario==='fence-preheaders')return;
  if(scenario==='refresh' && inferenceCount===1) {res.writeHead(401,{'content-type':'application/json'});res.end(JSON.stringify({error:{message:'synthetic expired access'}}));return;}
  if(scenario==='account-fallback' && req.headers['chatgpt-account-id']==='synthetic_workspace_1') {res.writeHead(429,{'content-type':'application/json'});res.end(JSON.stringify({error:{message:'synthetic subscription exhausted'}}));return;}
  if(scenario==='model-fallback' && body.model==='gpt-6-astra') {res.writeHead(429,{'content-type':'application/json'});res.end(JSON.stringify({error:{message:'synthetic model exhausted'}}));return;}
  res.writeHead(200,{'content-type':'text/event-stream'});
  if(scenario==='oversized'){res.end(frames([created()])+':'+ 'x'.repeat(1100000)+'\n\n');return;}
  let events=[created(),completed()];
  if(scenario==='tools' || invalidTools) {
    events=nativeToolEvents();
    // Exact first-review false-settlement inputs, through the original Codex stream.
    if(scenario==='tools-identity-drift')events=events.map(e=>e.type==='response.output_item.added' && e.item.id==='fc_01' ? {...e,item:{...e.item,call_id:'call_previous',name:'previous_tool'}} : e);
    if(scenario==='tools-delta-drift')events=events.map(e=>e.type==='response.function_call_arguments.delta' && e.item_id==='fc_01' && e.delta.includes('one.txt') ? {...e,delta:'"different.txt"}'} : e);
    if(scenario==='tools-duplicate-argument-keys')events=events.map(e=>e.type==='response.completed' ? {...e,response:{...e.response,output:e.response.output.map(item=>item.id==='fc_01'?{...item,arguments:'{"path":"one.txt","path":"different.txt"}'}:item)}} : e);
  }
  if(scenario==='failed')events=[created(),failed()];
  if(scenario==='incomplete')events=[created(),incomplete()];
  if(scenario==='partial')events=[created()];
  if(scenario==='contradictory')events=[created(),completed(),failed()];
  if((scenario==='sse-retry' || scenario==='retry-cancel' || scenario==='fence-retry') && inferenceCount===1) {
    const failure=failed();failure.response.error.message='server_is_overloaded';events=[created(),failure];
  }
  res.end(frames(events));
});
await new Promise(resolve=>backend.listen(47771,'127.0.0.1',resolve));
const config={
  settings:{requireApiKey:true,rtkEnabled:false,headroomEnabled:false,pxpipeEnabled:false,cavemanEnabled:false,ponytailEnabled:false,ccFilterNaming:false,capacityAdapter:Object.fromEntries(['vision','pdf','audioInput','videoInput'].map(k=>[k,{enabled:false,models:[]}]))},
  providerConnections:[1,2].map(n=>({id:`synthetic_native_${n}`,provider:'codex',authType:'oauth',name:`synthetic native ${n}`,priority:n,isActive:true,accessToken:`synthetic_access_${n}`,refreshToken:`synthetic_refresh_${n}`,expiresAt:new Date(Date.now()+(scenario.startsWith('proactive') || scenario==='refresh-cancel' ? -1000 : 1000000000)).toISOString(),lastRefreshAt:new Date().toISOString(),providerSpecificData:{chatgptAccountId:`synthetic_workspace_${n}`}})),
  apiKeys:[{id:'synthetic_ingress',key:'synthetic_gateway_key',name:'synthetic ingress',isActive:true}],
  combos:[{id:'native_route',name:'gaffer-native',models:['cx/gpt-6-astra','cx/gpt-5.6-sol','cx/gpt-5.6-terra']}]
};
assert.equal(await a.replace(a.state().generation,'native_revision_1',project(config),()=>repository.importDb(config)),true);
const originalPolicy=a.snapshot();
const gateway=createServer(async(req,res)=>{
  const cancel=new AbortController();res.on('close',()=>{if(!res.writableEnded)cancel.abort();});
  const request=new Request('http://127.0.0.1/v1/chat/completions',{method:'POST',headers:req.headers,body:Readable.toWeb(req),duplex:'half',signal:cancel.signal});
  const response=await handleChat(request);res.writeHead(response.status,Object.fromEntries(response.headers));
  try {if(response.body)for await(const chunk of response.body)res.write(chunk);res.end();}catch{res.destroy();}
});
await new Promise(resolve=>gateway.listen(0,'127.0.0.1',resolve));
const id=`native_${scenario.replaceAll('-','_')}`;
const controller=new AbortController();
const pending=nativeFetch(`http://127.0.0.1:${gateway.address().port}/v1/chat/completions`,{method:'POST',headers:{'content-type':'application/json',authorization:'Bearer synthetic_gateway_key','x-gaffer-request-id':id,'x-gaffer-generation':String(a.state().generation),'x-gaffer-revision':a.state().revision},body:JSON.stringify({model:'gaffer-native',stream:true,max_tokens:64,messages:[{role:'user',content:'synthetic native authority'}],...((scenario==='tools'||invalidTools) ? {tools:[{type:'function',function:{name:'read_file',parameters:{type:'object',properties:{path:{type:'string'}}}}}]} : {})}),signal:controller.signal}).then(async r=>({status:r.status,text:await r.text()})).catch(error=>({error:error.name}));
const waiting=['retry-cancel','fence-retry','preheaders-cancel','fence-preheaders','refresh-cancel'].includes(scenario);
if(waiting) {
  const until=Date.now()+5000;while(sends.length===0){assert.ok(Date.now()<until,'upstream admission timeout');await new Promise(resolve=>setTimeout(resolve,5));}
  if(scenario==='retry-cancel' || scenario==='fence-retry'){
    const untilTerminal=Date.now()+5000;while(!a.receipt(id).operations.some(o=>o.terminal==='provider_failed')){assert.ok(Date.now()<untilTerminal,'terminal receipt timeout');await new Promise(resolve=>setTimeout(resolve,5));}
  }
  const before=sends.length;
  if(scenario.startsWith('fence'))a.fence();else a.cancel(id);
  const reply=await pending;
  assert.equal(sends.length,before);assert.equal(sends.length,1);
  assert.equal(a.receipt(id).quiescent,scenario.endsWith('retry') || scenario==='retry-cancel');
} else {
  const reply=await pending;
  if(!invalidTools && !['oversized','contradictory'].includes(scenario))assert.equal(reply.status,200);
  const r=a.receipt(id);
  if(invalidTools || ['partial','contradictory','oversized'].includes(scenario)){assert.equal(r.quiescent,false);assert.equal(await a.replace(a.state().generation,'forbidden',originalPolicy,()=>{}),false);}
  else assert.equal(r.quiescent,true);
  if(invalidTools){assert.equal(reply.status,503);assert.equal(sends.length,1);assert.equal(r.operations.length,1);assert.equal(r.operations[0].terminal,'unknown');assert.equal(r.handler_done,1);assert.ok(!reply.text.includes('previous_tool')&&!reply.text.includes('different.txt'),'invalid original tool content must not be accepted');}
  if(scenario==='refresh'){assert.deepEqual(r.operations.map(o=>o.model),['gpt-6-astra','credential_refresh','gpt-6-astra']);assert.ok(r.operations.some(o=>o.terminal==='provider_refresh_terminal'));}
  if(scenario==='account-fallback')assert.equal(r.operations.length,2);
  if(scenario==='model-fallback')assert.deepEqual(r.operations.map(o=>o.model),['gpt-6-astra','gpt-6-astra','gpt-5.6-sol']);
  if(scenario==='sse-retry'){assert.equal(r.operations.length,2);assert.deepEqual(r.operations.map(o=>o.terminal),['provider_failed','provider_completed']);}
  if(scenario==='failed')assert.equal(r.operations[0].terminal,'provider_failed');
  if(scenario==='incomplete')assert.equal(r.operations[0].terminal,'provider_incomplete');
  if(scenario==='oversized')assert.equal(r.operations[0].local_stop,'original_error');
}
assert.equal(sends.every(s=>!s.maxOutputFieldPresent),true);
assert.deepEqual(a.snapshot(),originalPolicy);
if(scenario==='refresh' || scenario.startsWith('proactive'))assert.equal((await repository.getProviderConnectionById('synthetic_native_1')).accessToken,'synthetic_refreshed');
await assert.rejects(()=>repository.updateProviderConnection('synthetic_native_1',{providerSpecificData:{chatgptAccountId:'different_workspace'}}));
console.log(JSON.stringify({schema:1,scenario,nativeAuthorityOnly:true,providerOutputBound:false,liveAcceptance:false,sends,receipt:a.receipt(id),policy:a.snapshot()}));
gateway.closeAllConnections();backend.closeAllConnections();await new Promise(resolve=>gateway.close(resolve));await new Promise(resolve=>backend.close(resolve));process.exit(0);
