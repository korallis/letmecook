import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { Readable } from 'node:stream';
import { once } from 'node:events';
import { readFile,writeFile,chmod,rm,mkdir } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { existsSync } from 'node:fs';
import { Boundary } from '../inference-boundary/boundary.ts';
import { PolicyGate } from '../inference-boundary/policy.ts';
import { hashDocument,type RouterPolicy } from '../inference-boundary/router-policy.ts';
import { routerBinding } from '../inference-boundary/router-fixture.ts';
import { RouterAuthority } from './authority.ts';
if(!existsSync('/.dockerenv'))throw new Error('isolated_container_required');
const scenario=process.argv[2],recover=process.argv[3]==='recover';
const native=process.env.GAFFER_SYNTHETIC_NATIVE==='1';
// These dynamic imports execute only behind the pinned source-verifying loader.
const importModule=(path:string):Promise<any>=>import(path);
const {handleChat}=await importModule('/router-source/src/sse/handlers/chat.js');
const {getAdapter}=await importModule('/router-source/src/lib/db/driver.js');
const repo=await importModule('/router-source/src/lib/db/index.js');
const {authority,project}=await importModule('/router-source/gaffer-extension/authority.mjs');
await getAdapter();const a=authority();
const events:any[]=[],sends:any[]=[];
const emit=(event:string,detail:any={})=>events.push({event,at:Date.now(),...detail});
const resultPath='/work/result.json';
const shaFile=async(path:string)=>createHash('sha256').update(await readFile(path)).digest('hex');
const resources=async()=>{const r:any={};for(const k of ['memory.peak','memory.current','memory.events','pids.peak','cpu.stat'])try{r[k]=(await readFile('/sys/fs/cgroup/'+k,'utf8')).trim();}catch{r[k]=null;}return r;};
if(recover){
 const p=JSON.parse(await readFile('/work/approved.json','utf8'));const bridge=new RouterAuthority(a,p);
 const before=JSON.parse(await readFile('/work/journal/state.json','utf8'));
 const gate=await PolicyGate.open('/work/journal',bridge,500);let activated=false;try{await gate.activate();activated=true;}catch{}
 const after=gate.snapshot();assert.equal(activated,false);assert.equal(a.state().phase,'closed');
 if(['crash-send-possible','crash-post-send'].includes(scenario))assert.equal(after.reservations.length,1);
 if(scenario==='crash-reserved')assert.equal(after.decisions?.[0]?.verdict,'never_sent');
 if(scenario==='crash-receipt')assert.equal(after.decisions?.[0]?.verdict,'quiescent_failure');
 if(scenario==='crash-decision')assert.equal(after.decisions?.[0]?.verdict,'validated_success');
 await gate.close();await writeFile(resultPath,JSON.stringify({scenario,recovery:{beforeDisk:before,afterGateSnapshot:after,afterDisk:JSON.parse(await readFile('/work/journal/state.json','utf8')),routerPhase:a.state().phase,boot:a.state().boot,generation:a.state().generation,activated,replayed:false},resources:await resources()}));process.exit(0);
}
let inferenceCount=0;let boundary:Boundary;let gate:PolicyGate;let mutationPending:Promise<any>|undefined;let releaseMutation:(()=>void)|undefined;
const frame=(value:any)=>'data: '+JSON.stringify(value)+'\n\n';
const chat=(model:string,tools=false)=>{
 const event=(delta:any,finish_reason:any=null)=>frame({id:'synthetic_original',model,object:'chat.completion.chunk',choices:[{index:0,delta,finish_reason}]});
 if(!tools)return event({content:'Hello 🌍 café'})+event({},'stop')+'data: [DONE]\n\n';
 return event({tool_calls:[0,1].map(index=>({index,id:`call_original_${index}`,type:'function',function:{name:'read_file',arguments:'{"path":'}}))})+event({tool_calls:[1,0].map(index=>({index,function:{arguments:'"fixture.txt"}'}}))})+event({},'tool_calls')+'data: [DONE]\n\n';
};
const nativeFixtures=await importModule('/probe/native-fixtures.mjs');
const backend=createServer(async(req,res)=>{
 const chunks=[];for await(const chunk of req)chunks.push(chunk);const body=JSON.parse(Buffer.concat(chunks).toString());
 sends.push({path:req.url,model:body.model??null,capFields:Object.fromEntries(['max_tokens','max_output_tokens','max_completion_tokens'].filter(k=>Object.hasOwn(body,k)).map(k=>[k,body[k]])),account:req.headers['chatgpt-account-id']??(req.headers.authorization?.endsWith('_1')?'account_1':'account_2'),toolNames:body.tools?.map((t:any)=>t.name??t.function?.name)??[]});emit('original_send',{number:sends.length});
 if(req.url==='/token'){if(scenario==='refresh-stop'){setTimeout(()=>boundary.revoke('attempt_1'),30);return;}res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify({access_token:'synthetic_refreshed',refresh_token:'synthetic_rotated',expires_in:1000000}));return;}
 inferenceCount++;
 if(scenario==='crash-post-send'){await writeFile('/work/checkpoint.json',JSON.stringify({scenario,sends,events,gateSnapshot:gate.snapshot(),journalOnDisk:JSON.parse(await readFile('/work/journal/state.json','utf8')),receipt:a.receipt(gate.snapshot().reservations[0].requestId)}));process.kill(process.pid,'SIGKILL');return;}
 if(['stop','fence'].includes(scenario)){setTimeout(()=>scenario==='stop'?boundary.revoke('attempt_1'):a.fence(),30);return;}
 if(scenario==='account-fallback' && (req.headers.authorization?.endsWith('_1') || req.headers['chatgpt-account-id']==='synthetic_workspace_1') || scenario==='model-fallback' && body.model===(native?'gpt-6-astra':'synthetic-model') || scenario==='refresh' && inferenceCount===1){res.writeHead(scenario==='refresh'?401:429,{'content-type':'application/json'});res.end(JSON.stringify({error:{message:'Synthetic rejection'}}));return;}
 if(scenario==='drain'){assert.equal(await a.replace(a.state().generation,'next_revision',a.snapshot(),()=>{}),false);emit('router_draining');}
 res.writeHead(200,{'content-type':'text/event-stream'});
 let text:string;
 if(native){let items=[nativeFixtures.created(),nativeFixtures.completed()];if(scenario==='failed')items=[nativeFixtures.created(),nativeFixtures.failed()];if(scenario==='incomplete')items=[nativeFixtures.created(),nativeFixtures.incomplete()];if(scenario==='partial')items=[nativeFixtures.created()];if(['tools','backpressure-stop'].includes(scenario)&&inferenceCount===1)items=nativeFixtures.nativeToolEvents();text=nativeFixtures.frames(items).replaceAll('one.txt','fixture.txt').replaceAll('two.txt','fixture.txt');}
 else{text=chat(body.model,['tools','backpressure-stop'].includes(scenario)&&inferenceCount===1);if(scenario==='partial')text=text.split('\n\n')[0]+'\n\n';}
 const bytes=Buffer.from(text);for(let i=0;i<bytes.length;i+=17)res.write(bytes.subarray(i,i+17));res.end();
});
await new Promise<void>(resolve=>backend.listen(47771,'127.0.0.1',resolve));
const provider='openai-compatible-synthetic';
const config:any={settings:{requireApiKey:true,rtkEnabled:false,headroomEnabled:false,pxpipeEnabled:false,cavemanEnabled:false,ponytailEnabled:false,ccFilterNaming:false,capacityAdapter:Object.fromEntries(['vision','pdf','audioInput','videoInput'].map(k=>[k,{enabled:false,models:[]}]))},
 providerNodes:native?[]:[{id:provider,type:'openai-compatible',name:'synthetic',prefix:'synthetic',baseUrl:'http://127.0.0.1:47771/v1',apiType:'chat'}],
 providerConnections:[1,2].map(n=>native?{id:`account_${n}`,provider:'codex',authType:'oauth',name:'synthetic',priority:n,isActive:true,accessToken:`synthetic_access_${n}`,refreshToken:`synthetic_refresh_${n}`,expiresAt:new Date(Date.now()+(['proactive-refresh','refresh-stop'].includes(scenario)?-1000:1000000000)).toISOString(),lastRefreshAt:new Date().toISOString(),providerSpecificData:{chatgptAccountId:`synthetic_workspace_${n}`}}:{id:`account_${n}`,provider,authType:'apikey',name:'synthetic',priority:n,isActive:true,apiKey:`synthetic_account_${n}`,providerSpecificData:{baseUrl:'http://127.0.0.1:47771/v1',apiType:'chat'}}),
 apiKeys:[{id:'synthetic_ingress',key:'synthetic_gateway_key',name:'synthetic',isActive:true}],combos:[{id:'coding',name:'gaffer-coding',models:native?['cx/gpt-6-astra','cx/gpt-5.6-sol']:[`${provider}/synthetic-model`,`${provider}/fallback-model`]}]};
assert.equal(await a.replace(a.state().generation,'policy_1',project(config),()=>repo.importDb(config)),true);
const graph=a.snapshot(),state=a.state();
const p:RouterPolicy={schema:2,routerId:'synthetic_router',routeId:'coding',revision:state.revision,epoch:1,routerModel:'gaffer-coding',profile:native?'router-native-chat-translation-synthetic-v1':'router-chat-text-tools-synthetic-v1',evidence:'synthetic',liveAdmission:false,graph,
 limits:{requestBytes:65536,responseBytes:65536,outputTokens:64,concurrency:1,requestCount:3,totalMs:6000,firstOutputMs:4000,idleMs:2000,attemptMs:20000},
 authority:{deploymentId:'synthetic_bridge',boot:state.boot,generation:state.generation,revision:state.revision,graphDigest:hashDocument(graph)},
 envelope:{consumer:'chat-read-file-v1',cap:'max_completion_tokens-to-max_tokens-v1',replacement:'read-only',sourceCommit:'17c4cc76877bd1755030a8414f8d0083f48dcccf',sourceLock:await shaFile('/probe/source-lock.json'),overlay:await shaFile('/probe/overlay/authority.mjs'),runtime:await shaFile('/probe/runtime-identity.json')}};
await writeFile('/work/approved.json',JSON.stringify(p));
const control={state:()=>a.state(),snapshot:()=>a.snapshot(),receipt:(id:string)=>{const r=a.receipt(id);return scenario==='malformed-receipt'&&r.known?{...r,id:'mismatched'}:r;},quiescent:(ids:string[])=>a.quiescent(ids),cancel:(id:string)=>a.cancel(id)};
const bridge=new RouterAuthority(control,p);
if(['delay','receipt-stop'].includes(scenario)){const read=bridge.inspect.bind(bridge);bridge.inspect=async(...args)=>{const actual=await read(...args);if(actual.disposition==='original_success'){emit('receipt_visibility_delayed');if(scenario==='receipt-stop')setTimeout(()=>boundary.revoke('attempt_1'),30);await new Promise(resolve=>setTimeout(resolve,150));}return actual;};}
gate=await PolicyGate.open('/work/journal',bridge,500);await gate.activate();
const crash=async()=>{await writeFile('/work/checkpoint.json',JSON.stringify({scenario,sends,events,gateSnapshot:gate.snapshot(),journalOnDisk:JSON.parse(await readFile('/work/journal/state.json','utf8')),receipts:gate.snapshot().reservations.map(r=>a.receipt(r.requestId))}));process.kill(process.pid,'SIGKILL');};
if(['crash-reserved','crash-send-possible'].includes(scenario)){const mark=gate.markSend.bind(gate);gate.markSend=async id=>{if(scenario==='crash-send-possible')await mark(id);await crash();};}
const finalize=gate.finalize.bind(gate);
gate.finalize=async(...args)=>{
 if(scenario==='crash-receipt')await crash();
 if(scenario==='persist-after-rename')(gate as any).syncDirectory=async()=>{throw new Error('synthetic_directory_sync_failure');};
 if(scenario==='persist-failure'){const owner=JSON.parse(await readFile('/work/journal/owner.lock','utf8')).owner;await mkdir(`/work/journal/state.${owner}.next`);}
 if(scenario==='mutation-failed'){try{await a.replace(a.state().generation,'mutation_failed',a.snapshot(),async()=>{throw new Error('synthetic mutation');});}catch{emit('mutation_failed_closed');}}
 if(scenario==='mutation-timeout'){mutationPending=a.replace(a.state().generation,'mutation_timeout',a.snapshot(),()=>new Promise<void>(resolve=>{releaseMutation=resolve;}));void mutationPending!.catch(()=>{});await new Promise(resolve=>setTimeout(resolve,30));a.fence();emit('mutation_timeout_fenced');releaseMutation!();await mutationPending!.catch(()=>{});}
 await finalize(...args);emit('decision_persisted',{requestId:args[0]});
 if(scenario==='post-decision-cancel')a.cancel(args[0]);
 if(scenario==='crash-decision')await crash();
};
const upstream=createServer(async(req,res)=>{
 const abort=new AbortController();res.on('close',()=>{if(!res.writableEnded)abort.abort();});
 emit('ingress',{headers:Object.keys(req.headers),host:req.headers.host,requestId:req.headers['x-gaffer-request-id'],generation:req.headers['x-gaffer-generation'],revision:req.headers['x-gaffer-revision']});
 const request=new Request('http://127.0.0.1/v1/chat/completions',{method:'POST',headers:req.headers as HeadersInit,body:Readable.toWeb(req) as any,duplex:'half',signal:abort.signal} as RequestInit);
 const response=await handleChat(request);res.writeHead(response.status,Object.fromEntries(response.headers));
 try{if(response.body)for await(const chunk of response.body){emit('translated_chunk',{text:Buffer.from(chunk).toString()});res.write(chunk);}res.end();}catch{res.destroy();}
});
upstream.listen('/work/router.sock');await once(upstream,'listening');await chmod('/work/router.sock',0o600);
boundary=new Boundary(gate,'/work/router.sock',scenario==='zero-auth'?'synthetic_invalid_key':'synthetic_gateway_key');await boundary.listen('/router/inference.sock');
const binding=routerBinding(p);binding.role=process.env.PROOF_ROLE==='planner'?'planner':'worker';binding.expiresAt=binding.leaseExpiresAt=Date.now()+20000;
const token=boundary.issue(binding);
if(scenario==='pre-send-stop'){const admit=gate.admit.bind(gate);gate.admit=async(...args)=>{const result=await admit(...args);boundary.revoke('attempt_1');return result;};}
if(scenario==='backpressure-stop'){boundary.server.on('request',(_req,res)=>{const write=res.write.bind(res);let injected=false;res.write=((chunk:any,...args:any[])=>{const result=(write as any)(chunk,...args);if(!injected&&String(chunk).includes('tool_calls')){injected=true;emit('terminal_backpressure_injected');setTimeout(()=>boundary.revoke('attempt_1'),5);return false;}return result;}) as typeof res.write;});}
if(scenario==='changed-graph'){const next=structuredClone(config);next.combos[0].models.reverse();await a.replace(a.state().generation,'policy_2',project(next),()=>repo.importDb(next));}
console.log(JSON.stringify({event:'ready',token,role:binding.role,model:p.routerModel,tools:['tools','backpressure-stop'].includes(scenario),crash:scenario.startsWith('crash-')}));
let ending=false;
process.on('SIGUSR2',async()=>{if(ending)return;ending=true;let closeFailed=false;try{await boundary.close();}catch{closeFailed=true;assert(['persist-failure','persist-after-rename'].includes(scenario));}upstream.closeAllConnections();backend.closeAllConnections();await new Promise<void>(resolve=>upstream.close(()=>resolve()));await new Promise<void>(resolve=>backend.close(()=>resolve()));
 const journal=gate.snapshot();const ids=[...new Set([...journal.reservations.map(r=>r.requestId),...(journal.decisions??[]).map(d=>d.requestId)])];
 const receipts=ids.map(id=>a.receipt(id));
 if(native)assert(sends.every(s=>Object.keys(s.capFields).length===0));else assert(sends.every(s=>s.capFields.max_tokens===64));
 await writeFile(resultPath,JSON.stringify({scenario,native,runtime:{node:process.version,platform:process.platform,arch:process.arch},policy:p,sends,events,audit:boundary.audit,gateSnapshot:journal,journalOnDisk:JSON.parse(await readFile('/work/journal/state.json','utf8')),receipts,closeFailed,actualGlobalQuiescence:a.quiescent(ids.filter(id=>a.receipt(id).known)),routerPhase:a.state().phase,resources:await resources()}));await rm('/router/inference.sock',{force:true});process.exit(0);
});
