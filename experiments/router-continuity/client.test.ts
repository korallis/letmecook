import test from 'node:test';
import assert from 'node:assert/strict';
import { classify,unchanged,validateRequest } from './client.ts';
import { classify as editClassify,settingsDigest } from '../harness/native/client.ts';
import { FIXTURE,EXPECTED } from './constants.ts';
const base='a'.repeat(40),artifact={baseSHA:base,headSHA:base,path:'greeting.txt' as const,content:'hello\n',changedFiles:[],status:'',diff:''};
const events:any[]=[{type:'text',native:{part:{text:EXPECTED}}},{type:'step_finish',native:{part:{reason:'stop'}}}];
test('text transport has separate success; unchanged file is never an accepted edit',()=>{assert.equal(classify(events,0,null,false,false,artifact,base),'continuity_transport_completed');assert.equal(editClassify(events,0,null,false,false,false),'artifact_mismatch');});
test('tool/error/partial/mismatch/cancel outcomes cannot become continuity success',()=>{
 assert.equal(classify([{type:'tool_use',native:{part:{state:{status:'error'}}}},...events] as any,0,null,false,false,artifact,base),'continuity_tool_refused');
 for(const entry of [{events:events.slice(0,1)},{events:[{type:'error'},...events]},{events:[{type:'text',native:{part:{text:'wrong'}}},events[1]]},{artifact:{...artifact,content:'changed'}},{code:1},{signal:'SIGKILL'},{invalid:true},{cancelled:true}]){const x={events,artifact,code:0,signal:null as string|null,invalid:false,cancelled:false,...entry};assert.notEqual(classify(x.events as any,x.code,x.signal,x.invalid,x.cancelled,x.artifact,base),'continuity_transport_completed');}
 assert(!unchanged({...artifact,diff:'secret write'},base));
});
test('fixed request rejects alternative settings, prompt, excess limits and unknown fields',()=>{
 const request={schema:1 as const,fixture:FIXTURE as typeof FIXTURE,baseSHA:base,settingsDigest:settingsDigest('ask'),bindingDigest:'b'.repeat(64),token:'c'.repeat(64),wallMs:30000,outputBytes:262144};validateRequest(request);
 for(const patch of [{settingsDigest:settingsDigest('allow')},{prompt:'arbitrary'},{wallMs:30001},{outputBytes:262145},{fixture:'other'},{token:'not-a-grant'}])assert.throws(()=>validateRequest({...request,...patch} as any));
});
