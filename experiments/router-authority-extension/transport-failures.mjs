import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { existsSync } from 'node:fs';
import { frames, created } from './native-fixtures.mjs';
assert.ok(existsSync('/.dockerenv'));
const scenario=process.argv[2];
const refresh=scenario.startsWith('refresh-');
const native=refresh||scenario.startsWith('native-');
assert.equal(process.env.GAFFER_SYNTHETIC_NATIVE==='1',native);
const {handleChat}=await import('/router-source/src/sse/handlers/chat.js');
const {getAdapter}=await import('/router-source/src/lib/db/driver.js');
const repository=await import('/router-source/src/lib/db/index.js');
const {authority,project}=await import('/router-source/gaffer-extension/authority.mjs');
await getAdapter();const a=authority();let sends=0,closed=0;const paths=[];
const backend=createServer(async(req,res)=>{
  for await(const chunk of req){}sends++;paths.push(req.url);res.on('close',()=>closed++);
  if(scenario==='native-peek-deadline'){res.writeHead(200,{'content-type':'text/event-stream'});res.write(frames([created()]));return;}
  const status=scenario==='success-wrong-content-type-open'||refresh&&!scenario.includes('error')?200:429;
  res.writeHead(status,{'content-type':'application/json'});
  if(scenario.endsWith('cancel-complete-json')){res.write(JSON.stringify(refresh?{access_token:'synthetic_unproved',refresh_token:'synthetic_unproved_refresh'}:{error:{message:'synthetic rejection without EOF'}}));return;}
  if(scenario.includes('oversized')){res.write('x'.repeat(1100000));return;}
  if(scenario==='error-deadline'){res.write('{"error":');return;}
  if(scenario==='success-wrong-content-type-open'){res.write('{"unproved":"body"}');return;}
  if(scenario.includes('invalid-utf8')){res.end(Buffer.from([0xff,0xc3]));return;}
  if(scenario.includes('invalid-json')){res.end('{"error":');return;}
  if(scenario==='refresh-invalid-schema'){res.end(JSON.stringify({access_token:'synthetic_unproved',refresh_token:42}));return;}
  throw new Error('unhandled_scenario');
});
await new Promise(resolve=>backend.listen(native?47771:0,'127.0.0.1',resolve));
const baseUrl=`http://127.0.0.1:${backend.address().port}/v1`;
const provider=native?'codex':'openai-compatible-synthetic';
const config={
  settings:{requireApiKey:true,rtkEnabled:false,headroomEnabled:false,pxpipeEnabled:false,cavemanEnabled:false,ponytailEnabled:false,ccFilterNaming:false,capacityAdapter:Object.fromEntries(['vision','pdf','audioInput','videoInput'].map(k=>[k,{enabled:false,models:[]}]))},
  providerNodes:native?[]:[{id:provider,type:'openai-compatible',name:'synthetic node',prefix:provider,baseUrl,apiType:'chat'}],
  providerConnections:[{id:'synthetic_transport_account',provider,authType:native?'oauth':'apikey',name:'synthetic transport',priority:1,isActive:true,...(native?{accessToken:'synthetic_before_refresh',refreshToken:'synthetic_refresh',expiresAt:new Date(refresh?0:Date.now()+1000000000).toISOString(),lastRefreshAt:new Date(refresh?0:Date.now()).toISOString(),providerSpecificData:{chatgptAccountId:'synthetic_workspace'}}:{apiKey:'synthetic_key',providerSpecificData:{baseUrl,apiType:'chat'}})}],
  apiKeys:[{id:'synthetic_ingress',key:'synthetic_gateway_key',name:'synthetic ingress',isActive:true}],
  combos:[{id:'synthetic_route',name:'gaffer-synthetic',models:[native?'cx/gpt-6-astra':`${provider}/synthetic-model`]}]
};
assert.equal(await a.replace(a.state().generation,'revision_1',project(config),()=>repository.importDb(config)),true);
const id=scenario.replaceAll('-','_');const started=Date.now();
const pending=handleChat(new Request('http://127.0.0.1/v1/chat/completions',{
  method:'POST',headers:{'content-type':'application/json',authorization:'Bearer synthetic_gateway_key','x-gaffer-request-id':id,'x-gaffer-generation':String(a.state().generation),'x-gaffer-revision':a.state().revision},
  body:JSON.stringify({model:'gaffer-synthetic',stream:true,max_tokens:64,messages:[{role:'user',content:'synthetic original transport failure'}]})
}));
if(scenario.endsWith('cancel-complete-json')){
  const until=Date.now()+5000;while(sends===0){assert.ok(Date.now()<until,'original admission timeout');await new Promise(resolve=>setTimeout(resolve,5));}
  await new Promise(resolve=>setTimeout(resolve,100));a.cancel(id);
}
const response=await pending;
assert.equal(response.status,503);await response.text();
const elapsedMs=Date.now()-started;
const until=Date.now()+5000;while(closed!==sends){assert.ok(Date.now()<until,'physical_original_response_left_open');await new Promise(resolve=>setTimeout(resolve,5));}
assert.equal(sends,1,'failure must stop all later Base/account/refresh retries');assert.equal(closed,1);
const receipt=a.receipt(id);assert.equal(receipt.handler_done,1);assert.equal(receipt.operations.length,1);assert.equal(receipt.operations[0].terminal,'unknown');assert.equal(receipt.quiescent,false);
if(refresh){assert.deepEqual(paths,['/token']);assert.equal((await repository.getProviderConnectionById('synthetic_transport_account')).accessToken,'synthetic_before_refresh');}
if(scenario.endsWith('deadline')){assert.ok(elapsedMs>=29000&&elapsedMs<40000,'original transport must obey request deadline');}
a.cancel(id);assert.equal(a.receipt(id).quiescent,false);assert.equal(await a.replace(a.state().generation,'forbidden',project(config),()=>{}),false);
console.log(JSON.stringify({scenario,regressionOf:'c245c899c8d2562cb771f1f71258477fbaa88f77',sends,closed,openOriginalResponses:sends-closed,elapsedMs,afterHandlerCancelStillUnknown:true,receipt}));
await new Promise(resolve=>backend.close(resolve));process.exit(0);
