import test from 'node:test';
import assert from 'node:assert/strict';
import { DatabaseSync } from 'node:sqlite';
import { observeResponse, responseObservations } from './overlay/response-observation.mjs';

test('response metadata is a closed classification with no reflected strings', () => {
  for (const [value, mediaType] of [[null,'missing'],['text/event-stream; charset=utf-8','sse'],['Application/JSON; account=synthetic_private','json'],[' text/html ','html'],['application/problem+json','other'],['application/synthetic_private','other'],['text/event-stream.invalid','other'],['','other']]) {
    const response = new Response('synthetic_private', {status:202, headers:value===null?{}:{'content-type':value,'x-account':'synthetic_private'}});
    // Response(string) supplies text/plain by default; remove it for missing MIME.
    if(value===null)response.headers.delete('content-type');
    assert.deepEqual(observeResponse(response), {status:202,mediaType,bodyPresent:true});
    assert(!JSON.stringify(observeResponse(response)).includes('synthetic_private'));
  }
  assert.deepEqual(observeResponse(new Response(null,{status:204})),{status:204,mediaType:'missing',bodyPresent:false});
  assert.equal(observeResponse(new Response('')).bodyPresent,true,'body presence is a Fetch stream, not a byte count');
  assert.throws(()=>observeResponse(Response.error()),/unsupported_response_status/);
});

test('old schema stays unavailable, exact operations are immutable, and failed transactions roll back', () => {
  const sql=new DatabaseSync(':memory:');let fail=false;
  sql.exec('PRAGMA foreign_keys=ON; CREATE TABLE gaffer_operations(request_id TEXT,ordinal INTEGER,PRIMARY KEY(request_id,ordinal)); INSERT INTO gaffer_operations VALUES(\'old\',1),(\'new\',1),(\'new\',2)');
  const db={exec:s=>sql.exec(s),get:(s,p=[])=>sql.prepare(s).get(...p),run:(s,p=[])=>sql.prepare(s).run(...p),transaction:fn=>{sql.exec('BEGIN IMMEDIATE');try{fn();if(fail)throw Error('storage_commit_failure');sql.exec('COMMIT');}catch(e){sql.exec('ROLLBACK');throw e;}}};
  try {
    let observations=responseObservations(db);
    assert.equal(observations.read('old',1),null);
    const response=new Response(null,{status:204,headers:{'content-type':'text/html; synthetic_private=secret'}});
    observations.record('new',1,response);
    assert.throws(()=>observations.record('new',1,new Response('changed')),/UNIQUE/);
    assert.throws(()=>observations.record('absent',1,response),/FOREIGN KEY/);
    fail=true;assert.throws(()=>observations.record('new',2,response),/storage_commit_failure/);fail=false;
    observations=responseObservations(db);
    assert.equal(observations.read('old',1),null);assert.equal(observations.read('new',2),null);
    assert.deepEqual(observations.read('new',1),{status:204,mediaType:'html',bodyPresent:false});
    assert.deepEqual(Object.keys(sql.prepare('SELECT * FROM gaffer_response_observations').get()),['request_id','ordinal','status','media_type','body_present']);
  } finally {sql.close();}
});
