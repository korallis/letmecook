import test from 'node:test';
import assert from 'node:assert/strict';
import { profile,request,events,frames,patch } from './fixtures.mjs';
import { validateNativeProfile, digest } from '../router-authority-extension/overlay/native-profile.mjs';
import { NativeResponsesStream,validateNativeRequest,continuationOutput,validatePatch } from '../router-authority-extension/overlay/native-responses.mjs';
test('exact pinned native profile, initial request and stateless encrypted continuation',()=>{
 const p=validateNativeProfile(profile()),r=request(),output=events(true).at(-1).response.output;validateNativeRequest(r,p,r.model);
 const next={...r,input:[...r.input,...continuationOutput(output,p),{type:'function_call_output',call_id:'call_synthetic',output:'Success.'}]};
 assert.deepEqual(validateNativeRequest(next,p,r.model,{request:r,output}),next);
 for(const mutate of [b=>b.input[2].encrypted_content='drift',b=>b.input[3].call_id='different',b=>b.input[4].call_id='different',b=>b.input.pop(),b=>b.input.push(b.input.at(-1)),b=>b.prompt_cache_key='ses_changed',b=>b.input[0].content='changed prompt']){const b=structuredClone(next);mutate(b);assert.throws(()=>validateNativeRequest(b,p,r.model,{request:r,output}));}
});
test('native grants cannot silently acquire strict caps or widened paths/profiles',()=>{
 const p=profile();for(const mutate of [x=>delete x.authorization,x=>x.authorization.approved=false,x=>x.authorization.providerOutput={requirement:'hard',maxTokens:1},x=>x.authorization.providerMonetaryCap={requirement:'hard',max:1},x=>x.connections[1].billing='paid-api',x=>x.connections[1].billing='unknown',x=>x.authorization.effort='high',x=>x.authorization.model='gpt-5.6-sol',x=>x.harness.builtins='disabled',x=>x.harness.oauth=true,x=>x.protocol='chat-read-file-v1',x=>x.tools[0].description='changed',x=>x.scope.elapsedMs=900000,x=>x.local.concurrency=2,x=>x.scope.authorizationDigest=['a'.repeat(64)]]){const n=structuredClone(p);mutate(n);assert.throws(()=>validateNativeProfile(n));}
 for(const mutate of [b=>b.max_output_tokens=128,b=>b.max_tokens=128,b=>b.max_completion_tokens=128,b=>b.reasoning.effort='high',b=>b.store=true,b=>b.previous_response_id='resp_x',b=>b.tools[0].strict=true,b=>b.tools[0].parameters.additionalProperties=false,b=>b.input[1].content=[{type:'input_image',image_url:'https://example.invalid/a'}],b=>b.model='gpt-6-astra']){const r=request();mutate(r);assert.throws(()=>validateNativeRequest(r,p,'gaffer_native'));}
});
test('bounded patch schema and exact authorized paths before executable release',()=>{
 const p=profile();validatePatch(JSON.stringify({patchText:patch}),p);
 for(const path of ['../greeting.txt','/greeting.txt','.git/config','not-authorized.txt','greeting.txt\n*** Move to: ../escape'])assert.throws(()=>validatePatch(JSON.stringify({patchText:patch.replace('greeting.txt',path)}),p));
 for(const text of ['{"patchText":"a","patchText":"b"}',JSON.stringify({patchText:patch,command:'extra'}),JSON.stringify({patchText:'*** Begin Patch\n@@\n*** End Patch'})])assert.throws(()=>validatePatch(text,p));
});
test('every fragmented UTF-8/SSE boundary buffers all tools and validates native output',()=>{
 const p=profile();for(const tools of [false,true]){const wire=Buffer.from(frames(events(tools)));for(let split=0;split<=wire.length;split++){const s=new NativeResponsesStream(p);assert.deepEqual(s.push(wire.subarray(0,split)).output,[]);assert.deepEqual(s.push(wire.subarray(split)).output,[]);assert.equal(s.end().join(''),wire.toString());assert.equal(digest(s.nativeOutput),digest(events(tools).at(-1).response.output));}}
});
test('created events/comments do not satisfy output deadline or renew semantic idle',()=>{
 const s=new NativeResponsesStream(profile());assert.equal(s.push(Buffer.from(frames(events(true).slice(0,1)))).semantic,false);
 assert.equal(s.push(Buffer.from(': heartbeat\n\n')).semantic,false);assert.equal(s.push(Buffer.from(frames(events(true).slice(1,5)))).semantic,true);
 assert.equal(s.push(Buffer.from(': heartbeat\n\n')).semantic,false);assert.equal(s.push(Buffer.alloc(0)).semantic,false);
});
test('invalid, contradictory, fabricated, missing EOF and incomplete terminals release no tool frames',()=>{
 const p=profile();for(const mutate of [e=>e.pop(),e=>e.push(e.at(-1)),e=>e[4].item_id='fc_other',e=>e[3].item.call_id='different',e=>e[4].sequence_number=1,e=>e.at(-1).response.output.reverse(),e=>e.at(-1).response.output[0].encrypted_content='drift',e=>e.at(-1).response.output[1].arguments=JSON.stringify({patchText:patch.replace('greeting.txt','../escape')}),e=>e.at(-1).response.model='different',e=>e.at(-1).response.status='incomplete',e=>e.at(-1).response.error={message:'failure'},e=>e.at(-1).response.output[1].type='custom_tool_call']){const e=events(true);mutate(e);const s=new NativeResponsesStream(p);assert.deepEqual(s.push(Buffer.from(frames(e))).output,[]);assert.throws(()=>s.end());}
 const open=new NativeResponsesStream(p);assert.deepEqual(open.push(Buffer.from(frames(events(true)))).output,[]);assert.equal(open.nativeOutput,null);
});
