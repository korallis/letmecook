import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync,readFileSync,writeFileSync,rmSync,existsSync } from 'node:fs';
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
