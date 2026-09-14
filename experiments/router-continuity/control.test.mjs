import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { continuityManifest, continuityControl, continuitySettingsDigest } from './control.mjs';
import { digest } from '../router-authority-extension/overlay/native-profile.mjs';
import { classifyReceipt } from '../inference-boundary/router-policy.ts';
import { FIXTURE,FIRST,SECOND,TRANSITION,PROMPT,EXPECTED } from './constants.ts';
const captured=JSON.parse(readFileSync(new URL('../../tests/fixtures/harness/native/captured.json',import.meta.url)));
function setup() {
 const f=structuredClone(captured.gateway),p=f.policy; p.native.harness.settings=continuitySettingsDigest; p.graph.profileDigest=digest(p.native);p.authority.graphDigest=digest(p.graph);
 const d=f.journal.decisions.at(-1),r=f.receipts.find(x=>x.id===d.requestId);d.attemptId=FIRST;d.router.policy=p;d.router.binding.attemptId=FIRST;d.router.binding.native.profileDigest=digest(p.native);d.router.binding.expiresAt=d.router.binding.leaseExpiresAt=Date.now()+45000;
 d.router.nativeRequest.input=[{role:'user',content:[{type:'input_text',text:JSON.stringify(PROMPT)}]}];d.router.requestDigest=digest(JSON.stringify(d.router.nativeRequest));
 d.nativeOutput=d.nativeOutput.filter(x=>x.type==='message');d.nativeOutput[0].content[0].text=EXPECTED;
 r.native={profileDigest:digest(p.native),scopeId:p.native.scope.id,authorizationDigest:p.native.scope.authorizationDigest,bindingDigest:digest(d.router.binding),requestDigest:d.router.requestDigest};r.operations[0].output_digest=digest(d.nativeOutput);d.evidence=classifyReceipt(r,d.requestId,d.router);
 const scope={...f.scope,deadline:Date.now()+600000,state:'active',spent:1};
 const journal={...f.journal,reservations:[],decisions:[d]},state={phase:'active',...p.authority};
 const manifest=continuityManifest({schema:1,fixture:FIXTURE,firstAttemptId:FIRST,secondAttemptId:SECOND,transitionId:TRANSITION,settingsDigest:continuitySettingsDigest,scopeId:p.native.scope.id},[p.native]);
 const writes=[],health={active:true,status:'active',lockUntil:null,credentialsDigest:'unchanged'};let sends=0,fenced=0,mutations=0,quiet=true;
 const g={snapshot:()=>journal,admit:async()=>{sends++;return true;}};
 const a={state:()=>state,snapshot:()=>p.graph,quiescent:()=>quiet,evaluationScope:()=>scope,assertNativeCurrent:()=>{if(Date.now()>=Math.min(d.router.binding.expiresAt,d.router.binding.leaseExpiresAt)||Date.now()>=Math.min(...p.native.connections.map(c=>c.expiresAt-c.skewMs)))throw Error('expired');}};
 // Captured expiry is historical; this constructed test supplies a current clock.
 for(const c of p.native.connections)c.expiresAt=Date.now()+3600000;
 p.graph.profileDigest=digest(p.native);p.authority.graphDigest=digest(p.graph);state.graphDigest=p.authority.graphDigest;d.router.binding.native.profileDigest=digest(p.native);r.native.profileDigest=digest(p.native);r.native.bindingDigest=digest(d.router.binding);d.evidence=classifyReceipt(r,d.requestId,d.router);manifest.profileDigest=digest(p.native);
 const evidence=id=>({attemptId:id,selectedPolicy:p,decisions:id===FIRST?[d]:[],receipts:id===FIRST?[r]:[],reservations:[],scope});
 const options={manifest,packetDigest:'a'.repeat(64),policy:()=>p,gate:()=>g,authority:a,evidence,persist:(kind,value)=>{writes.push({kind,value});return {digest:digest(value),file:kind+'-'+digest(value)+'.json'};},health:async()=>structuredClone(health),markUnavailable:async(id,deadline)=>{assert.equal(id,r.operations[0].connection_id);mutations++;health.lockUntil=deadline;health.status='unavailable';},fence:()=>fenced++};
 const command={command:'continuity-transition',packetDigest:options.packetDigest,transitionId:TRANSITION};
 return {p,d,r,scope,journal,state,manifest,g,a,options,command,writes,health,setQuiet:x=>quiet=x,stats:()=>({sends,mutations,fenced})};
}
test('default deny, exact fixture and command fields',async()=>{
 const x=setup();assert.equal(continuityManifest(null,[x.p.native]),null);assert.throws(()=>continuityManifest({...x.manifest,account:'b'},[x.p.native]));
 const disabled=continuityControl({...x.options,manifest:null});await assert.rejects(disabled.transition(x.command));assert.deepEqual(x.stats(),{sends:0,mutations:0,fenced:0});
 for(const mutate of [c=>c.packetDigest='b'.repeat(64),c=>c.transitionId='another',c=>c.connectionId='b',c=>c.sql='anything']) {const c=structuredClone(x.command);mutate(c);await assert.rejects(continuityControl(x.options).transition(c));}assert.equal(x.writes.length,0);
});
test('one injected adapter call, durable intent/result; repeated command is idempotent',async()=>{
 const x=setup(),api=continuityControl(x.options);api.wrapGate(x.g);const result=await api.transition(x.command);assert.equal(result.acknowledged,true);assert.deepEqual(x.writes.map(x=>x.kind),['continuity-intent','continuity-result']);assert.equal(x.scope.spent,1);assert.deepEqual(await api.transition(x.command),result);assert.equal(x.stats().mutations,1);await x.g.admit();assert.equal(x.stats().sends,1);
});
for(const [name,mutate] of [
 ['profile',x=>x.p.native.harness.settings='b'.repeat(64)],['receipt',x=>x.r.operations[0].terminal='unknown'],['wrong prompt',x=>x.d.router.nativeRequest.input[0].content[0].text='different'],['stale fence',x=>x.state.generation++],['reservations',x=>x.journal.reservations.push({requestId:'unknown'})],['nonquiescent',x=>x.setQuiet(false)],['expired scope',x=>x.scope.deadline=Date.now()-1],['expired lease',x=>x.d.router.binding.leaseExpiresAt=Date.now()-1],['expired tokens',x=>x.p.native.connections[0].expiresAt=Date.now()-1],['missing decision',x=>x.d.delivery='unobserved'],['exhausted scope',x=>x.scope.spent=10]
])test('refuses '+name+' before intent or mutation',async()=>{const x=setup();mutate(x);await assert.rejects(continuityControl(x.options).transition(x.command));assert.equal(x.writes.length,0);assert.equal(x.stats().mutations,0);});
for(const kind of ['continuity-intent','continuity-result'])test(kind+' persistence failure fences all later admission/transition',async()=>{
 const x=setup(),save=x.options.persist;x.options.persist=(k,v)=>{if(k===kind)throw Error('synthetic_fsync');return save(k,v);};const api=continuityControl(x.options);api.wrapGate(x.g);await assert.rejects(api.transition(x.command));await assert.rejects(x.g.admit());await assert.rejects(api.transition(x.command));assert.equal(x.stats().fenced,1);assert.equal(x.stats().sends,0);assert.equal(x.stats().mutations,kind==='continuity-intent'?0:1);
});
test('transition excludes concurrent admission and control mutation',async()=>{
 const x=setup();let proceed,started;const entering=new Promise(r=>started=r),pause=new Promise(r=>proceed=r),mark=x.options.markUnavailable;x.options.markUnavailable=async(...args)=>{started();await pause;await mark(...args);};const api=continuityControl(x.options);api.wrapGate(x.g);const changing=api.transition(x.command);await entering;await assert.rejects(x.g.admit());assert.throws(()=>api.controlAllowed('grant'));await assert.rejects(api.transition(x.command));proceed();await changing;assert.equal(x.stats().sends,0);
});
test('already queued admission blocks transition before any mutation',async()=>{
 const x=setup();let proceed; x.g.admit=()=>new Promise(r=>proceed=r);const api=continuityControl(x.options);api.wrapGate(x.g);const pending=x.g.admit();await assert.rejects(api.transition(x.command));assert.equal(x.writes.length,0);proceed();await pending;
});
test('recovered intent refuses admission and replay',async()=>{const x=setup(),api=continuityControl({...x.options,recovered:true});api.wrapGate(x.g);await assert.rejects(x.g.admit());await assert.rejects(api.transition(x.command));assert.equal(x.stats().mutations,0);});
for(const field of ['credentialsDigest','active','lockUntil'])test('changed protected health '+field+' fails closed',async()=>{const x=setup(),mark=x.options.markUnavailable;x.options.markUnavailable=async(...args)=>{await mark(...args);x.health[field]=field==='credentialsDigest'?'changed':field==='active'?false:Date.now()+1;};const api=continuityControl(x.options);await assert.rejects(api.transition(x.command));assert.equal(x.stats().fenced,1);assert.equal(x.writes.filter(w=>w.kind==='continuity-result').length,0);});
