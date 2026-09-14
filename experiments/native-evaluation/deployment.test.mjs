import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync,readFileSync,writeFileSync,rmSync,existsSync,symlinkSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { acquireDeploymentOwner } from '../router-authority-extension/overlay/deployment-owner.mjs';
test('deployment owner precedes database startup; second process cannot change storage',()=>{
 const dir=mkdtempSync(join(tmpdir(),'gaffer-native-owner-')),owner=acquireDeploymentOwner(dir);writeFileSync(join(dir,'database-canary'),'generation1');
 const module=new URL('../router-authority-extension/overlay/deployment-owner.mjs',import.meta.url).href;
 const child=spawnSync(process.execPath,['--input-type=module','-e',`import {acquireDeploymentOwner} from ${JSON.stringify(module)};import{writeFileSync}from'node:fs';acquireDeploymentOwner(${JSON.stringify(dir)});writeFileSync(${JSON.stringify(join(dir,'database-canary'))},'changed');`],{encoding:'utf8'});
 assert.notEqual(child.status,0);assert.match(child.stderr,/EEXIST/);assert.equal(readFileSync(join(dir,'database-canary'),'utf8'),'generation1');
 assert.throws(()=>owner.release(false),/unknown/);assert(existsSync(join(dir,'deployment-owner.lock')));owner.release(true);rmSync(dir,{recursive:true});
});
test('owner identity changes fence incumbent and prevent removing successor lock',()=>{
 const dir=mkdtempSync(join(tmpdir(),'gaffer-native-owner-')),owner=acquireDeploymentOwner(dir),path=join(dir,'deployment-owner.lock');writeFileSync(path,'successor');assert.throws(()=>owner.assertCurrent(),/fenced/);assert.throws(()=>owner.release(true),/fenced/);assert.equal(readFileSync(path,'utf8'),'successor');rmSync(dir,{recursive:true});
});

import { persistImmutable } from './durable-records.mjs';
test('immutable acknowledged records survive retries and a fresh process without overwriting',()=>{
 const dir=mkdtempSync(join(tmpdir(),'gaffer-native-record-'));try{
  const first={path:'greeting.txt',content:'first'},a=persistImmutable(dir,'artifact',first),bytes=readFileSync(join(dir,a.file));assert.equal(a.created,true);
  const module=new URL('./durable-records.mjs',import.meta.url).href,child=spawnSync(process.execPath,['--input-type=module','-e',`import {persistImmutable} from ${JSON.stringify(module)};console.log(JSON.stringify(persistImmutable(${JSON.stringify(dir)},'artifact',${JSON.stringify(first)})));`],{encoding:'utf8'});assert.equal(child.status,0,child.stderr);assert.deepEqual(JSON.parse(child.stdout),{...a,created:false});
  const b=persistImmutable(dir,'artifact',{path:'greeting.txt',content:'second'});assert.notEqual(a.file,b.file);assert.deepEqual(readFileSync(join(dir,a.file)),bytes);
  writeFileSync(join(dir,a.file),'altered');assert.throws(()=>persistImmutable(dir,'artifact',first),/immutable_record_mismatch/);assert.equal(readFileSync(join(dir,a.file),'utf8'),'altered');
  rmSync(join(dir,a.file));symlinkSync(b.file,join(dir,a.file));assert.throws(()=>persistImmutable(dir,'artifact',first));assert.equal(readFileSync(join(dir,b.file),'utf8'),JSON.stringify({content:'second',path:'greeting.txt'}));
 }finally{rmSync(dir,{recursive:true});}
});
