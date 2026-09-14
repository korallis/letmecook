import assert from 'node:assert/strict';
import { mkdir,readdir,readFile,lstat } from 'node:fs/promises';
import { join } from 'node:path';
import { createHash } from 'node:crypto';
import { stageWorker } from '../harness/native/staging.ts';
import { stageContinuity } from './staging.ts';
import { stageNativePlanner } from '../planner-probe/native-staging.ts';
export const plannerDependencies=['ajv','fast-deep-equal','fast-uri','json-schema-traverse','require-from-string'];
export async function treeHashes(root:string){const hashes:Record<string,string>={};async function walk(path:string){for(const e of await readdir(join(root,path),{withFileTypes:true})){assert(!e.isSymbolicLink(),'staged_symlink');const p=path?path+'/'+e.name:e.name;if(e.isDirectory())await walk(p);else {assert(e.isFile());hashes[p]=createHash('sha256').update(await readFile(join(root,p))).digest('hex');}}}await walk('');return hashes;}
export async function stageSuite(root:string,target:string,binary:string){await mkdir(target,{mode:0o700});await stageWorker(root,join(target,'worker'),binary);await stageContinuity(root,join(target,'continuity'),binary);await mkdir(join(target,'planner'));await stageNativePlanner(root,join(target,'planner'));return {worker:await treeHashes(join(target,'worker')),planner:await treeHashes(join(target,'planner')),continuity:await treeHashes(join(target,'continuity'))};}
export async function verifySuiteStages(target:string,expected:any){const state=await lstat(target);assert(state.isDirectory()&&!state.isSymbolicLink()&&state.uid===process.getuid?.()&&(state.mode&0o077)===0,'private_suite_stage');assert.deepEqual((await readdir(target)).sort(),['continuity','planner','worker']);for(const name of ['worker','planner','continuity'])assert.deepEqual(await treeHashes(join(target,name)),expected[name],'staged_bytes_changed');}
