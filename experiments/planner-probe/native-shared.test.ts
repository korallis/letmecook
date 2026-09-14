import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { Ajv } from 'ajv';
import { nativeRead, nativeText, setupNative } from './native-fixture.ts';
import { proposal } from './public-fixture.ts';
import { plannerProfile } from './native-profile.ts';
import { canonical, validateNativeProfile, validateNativeRegistry, nativeConsumerRole, type NativeProfile } from '../router-authority-extension/overlay/native-profile.mjs';
import { NativeResponsesStream, validateNativeRequest, validateNativeOutput } from '../router-authority-extension/overlay/native-responses.mjs';
import { PLANNER_REPAIR, plannerPlanValid, plannerContinuation, plannerRequestState, validatePlannerCandidate } from '../router-authority-extension/overlay/native-planner.mjs';
// These fixtures contain only synthetic identities and literal protocol frames.
// @ts-expect-error shared fixture has no runtime TypeScript dependency
import { profile as workerProfile, frames } from '../native-evaluation/fixtures.mjs';
// @ts-expect-error shared fixture has no runtime TypeScript dependency
import { plannerEvents } from '../native-evaluation/planner-fixtures.mjs';
const profile = () => plannerProfile(workerProfile() as NativeProfile);
const packet = (body:any) => JSON.parse(body.input[1].content);
const valid = (body:any, read=false) => nativeText(JSON.stringify(proposal(packet(body).input_revision,read)));
async function chain(read=true, repair=false) {
 const responses:any[]=[];
 const f=await setupNative([...(read?[nativeRead()]:[]),...(repair?[nativeText('invalid plan')]:[]),body=>valid(body,read)]);
 const old=f.port.complete; f.port.complete=async(...args)=>{const r=await old(...args);responses.push(r.response.output);return r;};
 assert.equal((await f.planner.run()).outcome,'plan_proposed');
 return {requests:f.requests.map(x=>x.body),responses,n:profile()};
}

test('closed planner and worker profiles share one predeclared registry and retain distinct roles',()=>{
 const worker=workerProfile(),planner=plannerProfile(worker);
 assert.deepEqual(validateNativeRegistry([worker,planner]),[worker,planner]);
 assert.equal(nativeConsumerRole(worker),'worker');assert.equal(nativeConsumerRole(planner),'planner');
 for(const key of ['deployment','authorization','scope','connections'])assert.deepEqual(planner[key as keyof NativeProfile],worker[key]);
 for(const mutate of [
  (p:any)=>p.harness=worker.harness,(p:any)=>p.tools=worker.tools,(p:any)=>p.toolPaths.push('extra.txt'),
  (p:any)=>p.planner.settings='a'.repeat(64),(p:any)=>p.planner.schema='a'.repeat(64),(p:any)=>p.planner.source='opencode',
  (p:any)=>p.local.requestCount=4,(p:any)=>p.local.totalMs=5001,(p:any)=>p.authorization.effort='high',
 ]){const p=structuredClone(planner);mutate(p);assert.throws(()=>validateNativeProfile(p));}
 assert.throws(()=>validateNativeProfile({...worker,planner:planner.planner}));
 assert.throws(()=>validateNativeRegistry([worker,{...planner,scope:{...planner.scope,id:'another'}}]));
});

for(const [read,repair] of [[false,false],[true,false],[false,true],[true,true]])test(`exact read-only history read=${read} repair=${repair}`,async()=>{
 const {requests,responses,n}=await chain(read,repair);
 for(const [i,body] of requests.entries()){
  assert.deepEqual(validateNativeRequest(body,n,body.model,i?{request:requests[i-1],output:responses[i-1]}:null),body);
  const state=plannerRequestState(body,n);assert.equal(state.requests,i+1);
  assert.equal(state.phase,i===0?'assessment':repair&&i===requests.length-1?'repair':'final');
  validateNativeOutput(responses[i],n,body);
 }
 const last=requests.at(-1),text=responses.at(-1)[0].content[0].text;
 assert(plannerPlanValid(text,packet(last).input_revision,read));
 assert.throws(()=>validateNativeRequest(last,n,last.model,{request:last,output:responses.at(-1)}));
});

test('blank terminal output permits exactly one fixed repair with discovery disabled',async()=>{
 const f=await setupNative([nativeText(''),body=>valid(body)]);assert.equal((await f.planner.run()).outcome,'plan_proposed');
 const n=profile(),[first,repair]=f.requests.map(x=>x.body);
 validateNativeRequest(repair,n,first.model,{request:first,output:nativeText('').output});
 assert.equal(plannerRequestState(repair,n).repairs,1);
 assert.throws(()=>validateNativeOutput(nativeRead().output,n,repair));
 const second={...repair,input:[...repair.input,...plannerContinuation(nativeText('invalid').output),{role:'user',content:PLANNER_REPAIR}]};
 assert.throws(()=>validateNativeRequest(second,n,first.model,{request:repair,output:nativeText('invalid').output}));
});

test('history drift, invented evidence and caller reset are denied',async t=>{
 const {requests,responses,n}=await chain(true,true),[first,read,repair]=requests;
 for(const [name,mutate] of Object.entries({
  call_id:(x:any):any=>x.input[3].call_id='replacement',
  encrypted:(x:any):any=>x.input[2].encrypted_content='changed',
  arguments:(x:any):any=>x.input[3].arguments='{ "path":"fixture.txt"}',
  result_pair:(x:any):any=>x.input[4].call_id='replacement',
  result_content:(x:any):any=>{const p=JSON.parse(x.input[4].output);p.content+='x';x.input[4].output=JSON.stringify(p);},
  result_hash:(x:any):any=>{const p=JSON.parse(x.input[4].output);p.sha256='0'.repeat(64);x.input[4].output=JSON.stringify(p);},
  session:(x:any):any=>x.prompt_cache_key='ses_planner_new',
  discovery:(x:any):any=>x.tool_choice='auto',
  original_packet:(x:any):any=>{const p=packet(x);p.trusted_packet.capture+='x';x.input[1].content=JSON.stringify(p);},
  extra_input:(x:any):any=>x.input.push({role:'user',content:'Read another file'}),
 }))await t.test(name,()=>{const x=structuredClone(read);mutate(x);assert.throws(()=>validateNativeRequest(x,n,x.model,{request:first,output:responses[0]}));});
 assert.throws(()=>validateNativeRequest(read,n,read.model));
 const x=structuredClone(repair);x.input.at(-1).content+=' Read again.';assert.throws(()=>validateNativeRequest(x,n,x.model,{request:read,output:responses[1]}));
 const validFirst=nativeText(JSON.stringify(proposal(packet(first).input_revision,false))).output;
 assert.throws(()=>validateNativeRequest({...first,tool_choice:'none',input:[...first.input,...plannerContinuation(validFirst),{role:'user',content:PLANNER_REPAIR}]},n,first.model,{request:first,output:validFirst}));
});

test('closed request settings reject caps, worker identity and arbitrary schema',async t=>{
 const {requests:[body],n}=await chain(false);
 for(const [name,mutate] of Object.entries({
  model:(x:any):any=>x.model='gpt-6-astra',effort:(x:any):any=>x.reasoning.effort='high',store:(x:any):any=>x.store=true,
  cap:(x:any):any=>x.max_output_tokens=100,old_cap:(x:any):any=>x.max_tokens=100,previous:(x:any):any=>x.previous_response_id='resp_1',
  identity:(x:any):any=>x.prompt_cache_key='ses_opencode',tool:(x:any):any=>x.tools[0].name='apply_patch',
  schema:(x:any):any=>{const p=packet(x);p.plan_schema={type:'object'};x.input[1].content=JSON.stringify(p);},
  policy:(x:any):any=>{const p=packet(x);p.trusted_packet.policy.authority='execute';x.input[1].content=JSON.stringify(p);},
 }))await t.test(name,()=>{const x=structuredClone(body);mutate(x);assert.throws(()=>validateNativeRequest(x,n,body.model));});
});

test('pinned schema checker agrees with Ajv and enforces references',()=>{
 const schema=JSON.parse(readFileSync(new URL('../../tests/fixtures/planner/plan.schema.json',import.meta.url),'utf8'));
 const validate=new Ajv({allErrors:true,strict:true}).compile(schema),revision='a'.repeat(64),good=proposal(revision);
 const values:any[]=[good,null,{},[],{...good,extra:true},{...good,outcome:''},{...good,outcome:'😀'.repeat(2401)},{...good,allowed_paths:['../bad']},{...good,criteria:[]},{...good,schema:2}];
 for(const value of values)assert.equal(plannerPlanValid(JSON.stringify(value),revision,true),!!validate(value));
 assert(!plannerPlanValid(JSON.stringify(good),revision,false));assert(!plannerPlanValid(JSON.stringify(good),'b'.repeat(64),true));
 const duplicate=structuredClone(good);duplicate.criteria.push(duplicate.criteria[0]);assert(!plannerPlanValid(JSON.stringify(duplicate),revision,true));
 const unknown=structuredClone(good);unknown.steps[0].criterion_ids=['missing'];assert(!plannerPlanValid(JSON.stringify(unknown),revision,true));
 assert(!plannerPlanValid(JSON.stringify(good).replace('"schema":1','"schema":1,"schema":1'),revision,true));
});

test('original SSE codec accepts planner lifecycle and rejects worker or disabled calls',async()=>{
 const {requests,n}=await chain(true,true);
 for(const body of requests){const codec=new NativeResponsesStream(n,body),bytes=Buffer.from(frames(plannerEvents(body,n,'read-repair')));for(let i=0;i<bytes.length;i+=7)assert(!codec.push(bytes.subarray(i,i+7)).error);codec.end();assert(codec.nativeOutput);}
 assert.throws(()=>new NativeResponsesStream(n));
 for(const [body,scenario] of [[requests[0],'worker-tool'],[requests[1],'disabled-call'],[requests[2],'repair-tool']] as const){
  const codec=new NativeResponsesStream(n,body);let failed=false;try{failed=!!codec.push(Buffer.from(frames(plannerEvents(body,n,scenario)))).error;codec.end();}catch{failed=true;}assert(failed);
 }
});

test('proposal callback matches completed durable decision, revision, named output and read state',async()=>{
 const {requests,responses,n}=await chain(true,true),request=requests.at(-1),text=responses.at(-1)[0].content[0].text;
 const input={text,policy:{native:n},binding:{role:'planner'},decisions:[{verdict:'validated_success',delivery:'completed',router:{nativeRequest:request},nativeOutput:responses.at(-1)}],artifact:{path:'plan-proposal.json',content:text,metadata:{authority:'proposal_only',inputRevision:packet(request).input_revision}}};
 assert(validatePlannerCandidate(input));
 assert(validatePlannerCandidate({...input,artifact:{...input.artifact,content:JSON.stringify(JSON.parse(text),null,2)}}));
 for(const mutate of [(x:any):any=>x.binding.role='worker',(x:any):any=>x.artifact.path='fixture.txt',(x:any):any=>x.artifact.metadata.execution=true,(x:any):any=>x.artifact.metadata.inputRevision='b'.repeat(64),(x:any):any=>x.decisions[0].delivery='unobserved',(x:any):any=>x.decisions[0].nativeOutput=nativeText('different').output]){
  const x=structuredClone(input);mutate(x);assert(!validatePlannerCandidate(x));
 }
 assert.equal(canonical(JSON.parse(text)),canonical(proposal(packet(request).input_revision,true)));
});
