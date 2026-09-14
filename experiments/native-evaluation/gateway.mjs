// Container-only trusted bootstrap. Source manifest validation belongs to the
// launcher before execution; upstream's loader validates every router source.
import { createServer } from 'node:http';
import { Readable } from 'node:stream';
import { once } from 'node:events';
import { readFileSync,existsSync,writeFileSync,openSync,fsyncSync,closeSync,mkdirSync,chmodSync } from 'node:fs';
import { randomBytes } from 'node:crypto';
import { Boundary } from '../inference-boundary/boundary.ts';
import { PolicyGate } from '../inference-boundary/policy.ts';
import { RouterAuthority } from '../router-boundary-bridge/authority.ts';
import { digest,validateNativeProfile } from '../router-authority-extension/overlay/native-profile.mjs';
import { events,frames } from './fixtures.mjs';
import { plannerEvents } from './planner-fixtures.mjs';
import { PLANNER_PROTOCOL, validatePlannerCandidate } from '../router-authority-extension/overlay/native-planner.mjs';
import { evidenceControls } from './evidence-control.mjs';
import { persistImmutable } from './durable-records.mjs';
const {acquireDeploymentOwner}=await import('/router-source/gaffer-extension/deployment-owner.mjs');
const owner=acquireDeploymentOwner('/state');process.env.GAFFER_NATIVE_DEPLOYMENT='1';
const profiles=existsSync('/config/profiles.json')?JSON.parse(readFileSync('/config/profiles.json','utf8')):[JSON.parse(readFileSync('/config/profile.json','utf8'))];for(const p of profiles){p.deployment.writerFence=owner.digest;validateNativeProfile(p);}let n=profiles[0];
const {handleChat}=await import('/router-source/src/sse/handlers/chat.js');
const {getAdapter}=await import('/router-source/src/lib/db/driver.js');
const repository=await import('/router-source/src/lib/db/index.js');
const {authority,project}=await import('/router-source/gaffer-extension/authority.mjs');
await getAdapter();const a=authority();a.configureNative(profiles);
const config=JSON.parse(readFileSync('/private/config.json','utf8')),key=randomBytes(32).toString('hex');
config.apiKeys=[{id:'native_private_ingress',key,name:'Private native boundary',isActive:true}];
const startupRevision='native_boot_'+a.state().generation;
if(!await a.replace(a.state().generation,startupRevision,project(config),()=>repository.importDb(config)))throw Error('native_initial_policy_refused');
const state=a.state(),route=a.snapshot().routes[0];
let policy={schema:3,routerId:'native_router',routeId:route.id,revision:state.revision,epoch:1,routerModel:route.name,profile:'router-native-responses-local-v1',evidence:n.evidence,liveAdmission:n.evidence==='reviewed-deployment',graph:a.snapshot(),limits:{...n.local,outputTokens:null},authority:{deploymentId:n.deployment.id,boot:state.boot,generation:state.generation,revision:state.revision,graphDigest:digest(a.snapshot())},native:n};
let gate=await PolicyGate.open('/state/boundary',new RouterAuthority(a,policy),1000);await gate.activate();
const observed={sends:[],decisions:[],artifacts:[]};
const persist=(path,value)=>{const fd=openSync(path,'w',0o600);try{writeFileSync(fd,JSON.stringify(value));fsyncSync(fd);}finally{closeSync(fd);}const dir=openSync('/state','r');try{fsyncSync(dir);}finally{closeSync(dir);}};
function observeDecisions(){const finalize=gate.finalize.bind(gate);gate.finalize=async(...args)=>{await finalize(...args);observed.decisions.push({requestId:args[0],at:Date.now()});};}observeDecisions();
let synthetic;
if(n.evidence==='synthetic'){
 synthetic=createServer(async(req,res)=>{let text='';for await(const c of req){text+=c;if(text.length>n.local.requestBytes){res.writeHead(413).end();return;}}const body=JSON.parse(text);observed.sends.push({path:req.url,body});
  if(req.url!=='/responses')throw Error('unexpected_refresh_egress');
  res.writeHead(200,{'content-type':'text/event-stream'});const bytes=Buffer.from(frames(n.protocol===PLANNER_PROTOCOL?plannerEvents(body,n):events(!body.input.some(x=>x.type==='function_call_output'))));for(let i=0;i<bytes.length;i+=17)res.write(bytes.subarray(i,i+17));res.end();
 });synthetic.listen(47771,'127.0.0.1');await once(synthetic,'listening');
}
const router=createServer(async(req,res)=>{const abort=new AbortController();res.on('close',()=>{if(!res.writableEnded)abort.abort();});try{const result=await handleChat(new Request('http://127.0.0.1'+req.url,{method:req.method,headers:req.headers,body:Readable.toWeb(req),duplex:'half',signal:abort.signal}));res.writeHead(result.status,Object.fromEntries(result.headers));if(result.body)for await(const c of result.body)res.write(c);res.end();}catch{res.destroy();}});
router.listen('/state/router.sock');await once(router,'listening');chmodSync('/state/router.sock',0o600);
let boundary=new Boundary(gate,'/state/router.sock',key);await boundary.listen('/router/inference.sock');
const packet={schema:1,policy,profiles:a.nativeProfiles(),registryDigest:digest(a.nativeProfiles()),capabilities:{providerOutputTokens:'unavailable',providerMonetaryCap:'unavailable',refresh:'denied'},scopeStatusAtPreparation:'not_started'};
const packetRecord=persistImmutable('/state','deployment-packet',packet),packetDigest=packetRecord.digest;
const evidenceApi=evidenceControls({policy:()=>policy,gate:()=>gate,authority:a,packetDigest,observed,persistCandidate:value=>persistImmutable('/state','candidate',value),validateProposal:validatePlannerCandidate});
let stopping=false,selecting=false;
async function stop(){
 if(stopping)return;stopping=true;
 await boundary.close();router.closeAllConnections();await new Promise(r=>router.close(r));
 synthetic?.closeAllConnections();if(synthetic)await new Promise(r=>synthetic.close(r));
 const journal=gate.snapshot(),ids=[...new Set([...journal.reservations,...(journal.decisions??[])].map(r=>r.requestId))],receipts=ids.map(id=>a.receipt(id));
 const result={schema:1,packetDigest,evidence:n.evidence,observed,policy,journal,receipts,scope:a.evaluationScope(n.scope.id)??null};persist('/state/result.json',result);
 const quiet=a.quiescent(ids);a.close();if(quiet)owner.release(true);
 control.close();console.log(JSON.stringify({event:'gateway_stopped',quiescent:quiet,resultDigest:digest(result)}));process.exit(0);
}
const control=createServer(async(req,res)=>{
 try{
  if(req.method!=='POST'||req.url!=='/control')throw Error('unsupported_control');let text='';for await(const c of req){text+=c;if(Buffer.byteLength(text)>2097152)throw Error('control_bytes');}const message=JSON.parse(text);let result;if(selecting&&!['inspect','stop'].includes(message.command))throw Error('selection_in_progress');
  if(message.command==='inspect'&&Object.keys(message).length===1)result={packet,packetDigest,selectedPolicy:policy,current:{boot:a.state().boot,generation:a.state().generation,revision:a.state().revision,scope:a.evaluationScope(n.scope.id)??null,journal:gate.snapshot()}};
  else if(message.command==='start'&&Object.keys(message).length===2&&message.packetDigest===packetDigest){result=a.startEvaluation();}
  else if(message.command==='select'&&Object.keys(message).length===3&&message.packetDigest===packetDigest){
   if(gate.snapshot().reservations.length||!a.quiescent([]))throw Error('selection_not_quiescent');selecting=true;
   try{const generation=a.state().generation;await boundary.close();const selected=a.selectNativeProfile(message.profileDigest,generation);n=selected.profile;policy={...policy,native:n,revision:selected.state.revision,epoch:policy.epoch+1,graph:selected.graph,limits:{...n.local,outputTokens:null},authority:{...policy.authority,generation:selected.state.generation,revision:selected.state.revision,graphDigest:digest(selected.graph)}};gate=await PolicyGate.open('/state/boundary',new RouterAuthority(a,policy),1000);await gate.activate();observeDecisions();boundary=new Boundary(gate,'/state/router.sock',key);await boundary.listen('/router/inference.sock');result={policy,scope:a.evaluationScope(n.scope.id)??null};selecting=false;}catch(error){void stop();throw error;}
  }
  else if(message.command==='grant'&&Object.keys(message).length===2){const binding=message.binding;if(!a.evaluationScope(n.scope.id))throw Error('evaluation_not_started');result={token:boundary.issue(binding),model:policy.routerModel};}
  else if(message.command==='evidence'&&Object.keys(message).length===2)result=evidenceApi.evidence(message.attemptId);
  else if(message.command==='candidate'&&Object.keys(message).length===2)result=evidenceApi.candidate(message.value);
  else if(message.command==='artifact'&&Object.keys(message).length===2){const value=message.value;if(!value||typeof value.path!=='string'||!n.toolPaths.includes(value.path)||typeof value.content!=='string'||Buffer.byteLength(value.content)>1048576)throw Error('unsupported_artifact');const scope=a.evaluationScope(n.scope.id);if(!scope)throw Error('evaluation_not_started');
   const saved=persistImmutable('/state','artifact',value);
   const acknowledgement=persistImmutable('/state','artifact-ack',{schema:1,packetDigest,packetFile:packetRecord.file,policyDigest:digest(policy),authority:policy.authority,profileDigest:digest(n),scope:{id:scope.id,caseRef:n.scope.caseRef,phase:scope.phase,boot:scope.boot,started:scope.started,deadline:scope.deadline,authorizationDigest:n.scope.authorizationDigest},artifact:{path:value.path,digest:saved.digest,file:saved.file}});
   const identity={digest:acknowledgement.digest,file:acknowledgement.file};observed.artifacts.push({path:value.path,digest:saved.digest,file:saved.file,acknowledgement:identity});result={acknowledged:true,digest:saved.digest,file:saved.file,acknowledgement:identity};}
  else if(message.command==='stop'&&Object.keys(message).length===1){res.writeHead(200,{'content-type':'application/json'}).end('{}');void stop();return;}
  else throw Error('unsupported_control');
  res.writeHead(200,{'content-type':'application/json'}).end(JSON.stringify(result));
 }catch{res.writeHead(403,{'content-type':'application/json'}).end('{"error":"control_denied"}');}
});control.listen('/control/gateway.sock');await once(control,'listening');chmodSync('/control/gateway.sock',0o600);
process.on('SIGTERM',()=>void stop());process.on('SIGINT',()=>void stop());console.log(JSON.stringify({event:'gateway_ready',packetDigest}));
