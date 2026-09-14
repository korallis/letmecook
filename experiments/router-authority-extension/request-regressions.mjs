import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {existsSync} from 'node:fs';
import {frames,created,completed} from './native-fixtures.mjs';
assert.ok(existsSync('/.dockerenv'));
const native=process.argv[2]==='request-native';
assert.equal(process.env.GAFFER_SYNTHETIC_NATIVE==='1',native);
const {handleChat}=await import('/router-source/src/sse/handlers/chat.js');
const {getAdapter}=await import('/router-source/src/lib/db/driver.js');
const repo=await import('/router-source/src/lib/db/index.js');
const {authority,project}=await import('/router-source/gaffer-extension/authority.mjs');
await getAdapter();const a=authority();const sends=[];
const backend=createServer(async(req,res)=>{
  const chunks=[];for await(const chunk of req)chunks.push(chunk);const body=JSON.parse(Buffer.concat(chunks).toString());sends.push(body);
  res.writeHead(200,{'content-type':'text/event-stream'});
  res.end(native?frames([created(),completed()]):'data: '+JSON.stringify({id:'synthetic_request_completion',model:body.model,object:'chat.completion.chunk',choices:[{index:0,delta:{content:'synthetic completed'},finish_reason:'stop'}]})+'\n\ndata: [DONE]\n\n');
});
await new Promise(resolve=>backend.listen(native?47771:0,'127.0.0.1',resolve));
const provider=native?'codex':'openai-compatible-synthetic';const baseUrl=`http://127.0.0.1:${backend.address().port}/v1`;
const config={
  settings:{requireApiKey:true,rtkEnabled:false,headroomEnabled:false,pxpipeEnabled:false,cavemanEnabled:false,ponytailEnabled:false,ccFilterNaming:false,capacityAdapter:Object.fromEntries(['vision','pdf','audioInput','videoInput'].map(k=>[k,{enabled:false,models:[]}]))},
  providerNodes:native?[]:[{id:provider,type:'openai-compatible',name:'synthetic node',prefix:provider,baseUrl,apiType:'chat'}],
  providerConnections:[{id:'synthetic_request_account',provider,authType:native?'oauth':'apikey',name:'synthetic request',priority:1,isActive:true,...(native?{accessToken:'synthetic_access',refreshToken:'synthetic_refresh',expiresAt:new Date(Date.now()+1000000000).toISOString(),lastRefreshAt:new Date().toISOString(),providerSpecificData:{chatgptAccountId:'synthetic_workspace'}}:{apiKey:'synthetic_key',providerSpecificData:{baseUrl,apiType:'chat'}})}],
  apiKeys:[{id:'synthetic_ingress',key:'synthetic_gateway_key',name:'synthetic ingress',isActive:true}],
  combos:[{id:'synthetic_route',name:'gaffer-synthetic',models:[native?'cx/gpt-6-astra':`${provider}/synthetic-model`]}]
};
assert.equal(await a.replace(a.state().generation,'revision_1',project(config),()=>repo.importDb(config)),true);
const parameters={type:'object',properties:{path:{type:'string',pattern:'^.+$'},pattern:{type:'string',pattern:String.raw`\\p{Cc}`}},required:['path'],additionalProperties:false};
const tool={type:'function',function:{name:'read_file',description:'Read synthetic text',parameters}};
const base={model:'gaffer-synthetic',stream:true,max_tokens:64,messages:[{role:'system',content:'Synthetic system instructions'},{role:'user',content:'Read synthetic text'}],tools:[tool]};
const call={id:'call_synthetic_1',type:'function',function:{name:'read_file',arguments:'{"path":"synthetic-one.txt"}'}};
const secondCall={id:'call_synthetic_2',type:'function',function:{name:'read_file',arguments:'{"path":"synthetic-two.txt"}'}};
const history=[...base.messages,{role:'assistant',content:null,tool_calls:[call,secondCall]},{role:'tool',tool_call_id:secondCall.id,content:''},{role:'tool',tool_call_id:call.id,content:'Synthetic file contents'}];
const request=(id,body)=>handleChat(new Request('http://127.0.0.1/v1/chat/completions',{method:'POST',headers:{'content-type':'application/json',authorization:'Bearer synthetic_gateway_key','x-gaffer-request-id':id,'x-gaffer-generation':String(a.state().generation),'x-gaffer-revision':a.state().revision},body:JSON.stringify(body)}));
const rejected=[];
const cases=[
  ['strict_true',{tools:[{...tool,function:{...tool.function,strict:true}}]}],
  ['strict_false',{tools:[{...tool,function:{...tool.function,strict:false}}]}],
  ...['none','required','auto'].map(choice=>[`choice_${choice}`,{tool_choice:choice}]),
  ['choice_function',{tool_choice:{type:'function',function:{name:'read_file'}}}],
  ['choice_hosted',{tool_choice:{type:'web_search'}}],
  ['choice_null',{tool_choice:null}],
  ['temperature_zero',{temperature:0}],
  ['parallel_control',{parallel_tool_calls:true}],
  ['structured_output',{response_format:{type:'json_schema',json_schema:{name:'synthetic',strict:true,schema:parameters}}}],
  ['hosted_tool',{tools:[{type:'web_search'}]}],
  ['tool_control',{tools:[{...tool,name:'override_name'}]}],
  ['function_control',{tools:[{...tool,function:{...tool.function,provider:'another_provider'}}]}],
  ['empty_tools',{tools:[]}],['null_tools',{tools:null}],
  ['missing_parameters',{tools:[{type:'function',function:{name:'read_file'}}]}],
  ['malformed_parameters',{tools:[{...tool,function:{...tool.function,parameters:[]}}]}],
  ['missing_properties',{tools:[{...tool,function:{...tool.function,parameters:{type:'object'}}}]}],
  ['removed_pattern',{tools:[{...tool,function:{...tool.function,parameters:{type:'object',properties:{path:{type:'string',pattern:String.raw`\p{Cc}`}}}}}]}],
  ['trimmed_name',{tools:[{...tool,function:{...tool.function,name:' read_file '}}]}],
  ['truncated_name',{tools:[{...tool,function:{...tool.function,name:'x'.repeat(129)}}]}],
  ['duplicate_tools',{tools:[tool,tool]}],
  ['named_message',{messages:[{role:'user',name:'silent_name',content:'synthetic'}]}],
  ['hosted_message',{messages:[{role:'user',content:[{type:'image_url',image_url:{url:'http://127.0.0.1:1/image'}}]}]}],
  ['user_tool_calls',{messages:[{role:'user',content:'synthetic',tool_calls:[call]}]}],
  ['call_control',{messages:[...base.messages,{role:'assistant',content:null,tool_calls:[{...call,strict:true}]},{role:'tool',tool_call_id:call.id,content:'synthetic'}]}],
  ['call_function_control',{messages:[...base.messages,{role:'assistant',content:null,tool_calls:[{...call,function:{...call.function,strict:true}}]},{role:'tool',tool_call_id:call.id,content:'synthetic'}]}],
  ['missing_call_id',{messages:[...base.messages,{role:'assistant',content:null,tool_calls:[{type:'function',function:call.function}]},{role:'tool',tool_call_id:call.id,content:'synthetic'}]}],
  ['malformed_arguments',{messages:[...base.messages,{role:'assistant',content:null,tool_calls:[{...call,function:{...call.function,arguments:'{"path":'}}]},{role:'tool',tool_call_id:call.id,content:'synthetic'}]}],
  ['primitive_arguments',{messages:[...base.messages,{role:'assistant',content:null,tool_calls:[{...call,function:{...call.function,arguments:'null'}}]},{role:'tool',tool_call_id:call.id,content:'synthetic'}]}],
  ['orphan_result',{messages:[...base.messages,{role:'tool',tool_call_id:call.id,content:'synthetic'}]}],
  ['unmatched_call',{messages:[...base.messages,{role:'assistant',content:null,tool_calls:[call]}]}],
  ['duplicate_call',{messages:[...base.messages,{role:'assistant',content:null,tool_calls:[call,call]},{role:'tool',tool_call_id:call.id,content:'synthetic'}]}],
  ['duplicate_system',{messages:[...base.messages,{role:'system',content:'silently dropped instructions'}]}],
  ['late_system',{messages:[base.messages[1],base.messages[0]]}],
  ['empty_messages',{messages:[]}],['blank_message',{messages:[{role:'user',content:'   '}]}]
];
for(const [id,patch] of cases){
  const count=sends.length;const response=await request(id,{...base,...patch});assert.equal(response.status,503);await response.text();
  assert.equal(sends.length,count);assert.deepEqual(a.receipt(id),{id,known:false,quiescent:false});assert.equal(a.state().phase,'active');
  rejected.push({id,status:503,sends:0,receiptKnown:false,quiescent:false});
}
const accepted=[];
for(const [id,body] of [['ordinary_tools',base],['matched_continuation',{...base,messages:history}]]){
  const count=sends.length;const response=await request(id,body);assert.equal(response.status,200);await response.text();assert.equal(sends.length,count+1);
  const wire=sends.at(-1);assert.equal(wire.tool_choice,undefined);assert.equal(wire.temperature,undefined);
  if(native){
    assert.deepEqual(wire.tools,[{type:'function',name:tool.function.name,description:tool.function.description,parameters}]);assert.equal(wire.instructions,base.messages[0].content);
    const calls=wire.input.filter(item=>item.type==='function_call');const results=wire.input.filter(item=>item.type==='function_call_output');
    assert.deepEqual(calls,id==='matched_continuation'?[call,secondCall].map(call=>({type:'function_call',call_id:call.id,name:call.function.name,arguments:call.function.arguments})):[]);
    assert.deepEqual(results,id==='matched_continuation'?history.filter(m=>m.role==='tool').map(m=>({type:'function_call_output',call_id:m.tool_call_id,output:m.content})):[]);
  }else{assert.deepEqual(wire.tools,body.tools);assert.deepEqual(wire.messages,body.messages);}
  assert.equal(a.receipt(id).quiescent,true);assert.equal(a.receipt(id).operations.length,1);
  accepted.push({id,wire:{tools:wire.tools,...(native?{input:wire.input,instructions:wire.instructions}:{messages:wire.messages})},receipt:a.receipt(id)});
}
console.log(JSON.stringify({scenario:native?'request-native':'request-compatible',regressionOf:'461cb2f372d88689dd6aed6cd3e1fe3ed060bd7e',native,rejected,accepted,sends:sends.length,liveAcceptance:false}));
await new Promise(resolve=>backend.close(resolve));process.exit(0);
