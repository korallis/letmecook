import test from 'node:test';
import assert from 'node:assert/strict';
import { DatabaseSync } from 'node:sqlite';
import { evaluationScopes, validateScope } from './overlay/evaluation-scope.mjs';
function setup() {
  const sql=new DatabaseSync(':memory:'); let fail=false;
  const db={exec:s=>sql.exec(s),get:(s,p=[])=>sql.prepare(s).get(...p),run:(s,p=[])=>{if(fail&&s.startsWith('INSERT INTO gaffer_scope_operations'))throw Error('persistence');return sql.prepare(s).run(...p);},transaction:fn=>{sql.exec('BEGIN IMMEDIATE');try{const v=fn();sql.exec('COMMIT');return v;}catch(e){sql.exec('ROLLBACK');throw e;}}};
  let wall=1000000,mono=0;
  const scopes=evaluationScopes(db,'boot-a',{wall:()=>wall,mono:()=>mono});
  const spec={id:'initial_checks',caseRef:'initial_checks',phase:'initial',authorizationDigest:'a'.repeat(64),maxInferenceAttempts:10,maxRefreshOperations:0,elapsedMs:600000};
  const debit=(n,extra={})=>db.transaction(()=>scopes.debit({id:spec.id,authorizationDigest:spec.authorizationDigest,requestId:'request_'+n,ordinal:1,kind:'inference',bodyDigest:'b'.repeat(64),...extra}));
  return {db,scopes,spec,debit,clock:(w,m)=>{wall=w;mono=m;},fail:()=>{fail=true;}};
}
test('10 physical attempts shared across requests; failed sends consume allowance; no renewal',()=>{
 const s=setup();s.scopes.register(s.spec,()=>true);
 for(let n=0;n<10;n++)s.debit(n);
 assert.equal(s.scopes.read(s.spec.id).spent,10);assert.throws(()=>s.debit(11),/attempt_limit/);
 s.scopes.close(s.spec.id,()=>true);assert.throws(()=>s.scopes.register(s.spec,()=>true),/already_used/);
 assert.throws(()=>s.scopes.register({...s.spec,id:'renamed_initial'},()=>true),/UNIQUE/);
});
test('baseline supports 32 / 900000 without widening initial; scopes serial and unknown retained',()=>{
 const s=setup();assert.throws(()=>validateScope({...s.spec,elapsedMs:900000}));
 s.scopes.register({...s.spec,id:'case_a',caseRef:'case_a',phase:'baseline',maxInferenceAttempts:32,elapsedMs:900000},()=>true);
 assert.throws(()=>s.scopes.register({...s.spec,id:'case_b',caseRef:'case_b',phase:'baseline'},()=>true),/serial/);
 assert.throws(()=>s.scopes.close('case_a',()=>false),/unknown/);assert.equal(s.scopes.read('case_a').state,'active');
});
test('atomic debit rolls back if send-possible persistence fails',()=>{
 const s=setup();s.scopes.register(s.spec,()=>true);s.fail();assert.throws(()=>s.debit(1),/persistence/);assert.equal(s.scopes.read(s.spec.id).spent,0);
});
test('closed recovery retains debit and cannot reopen same scope',()=>{
 const s=setup();s.scopes.register(s.spec,()=>true);s.debit(1);
 const recovered=evaluationScopes(s.db,'boot-b');assert.equal(recovered.read(s.spec.id).spent,1);assert.equal(recovered.read(s.spec.id).state,'closed');
 assert.throws(()=>recovered.assertCurrent(s.spec.id,s.spec.authorizationDigest),/closed/);assert.throws(()=>recovered.register(s.spec,()=>true),/already_used/);
});
for(const [name,w,m,extra] of [['monotonic elapsed',1000001,600000,{}],['forward wall jump',1600000,1,{}],['backward wall jump',999999,1,{}],['token expiry',1000000,1,{tokenDeadline:1000000}]])test(name+' denies without a timer callback',()=>{
 const s=setup();s.scopes.register(s.spec,()=>true);s.clock(w,m);assert.throws(()=>s.debit(1,extra),/deadline|clock_changed/);assert.equal(s.scopes.read(s.spec.id).spent,0);
});
test('refresh and unauthorized scope cannot dispatch',()=>{
 const s=setup();s.scopes.register(s.spec,()=>true);assert.throws(()=>s.debit(1,{kind:'refresh'}),/refresh_not_authorized/);assert.throws(()=>s.debit(1,{authorizationDigest:'c'.repeat(64)}),/authorization_mismatch/);
});
