// Prepare bytes and inputs only. No grants, start, provider sends or credentials.
import assert from 'node:assert/strict';
import { readFile,writeFile,lstat } from 'node:fs/promises';
import { join,resolve } from 'node:path';
import { stageSuite } from './suite-staging.ts';
import { suiteProfiles,suiteManifest } from './suite-profile.ts';
import { settingsDigest } from '../harness/native/client.ts';
import { INITIAL_SUITE } from '../router-authority-extension/overlay/initial-suite.mjs';
import { FIXTURE,FIRST,SECOND,TRANSITION } from './constants.ts';
export async function prepareSuite(directory:string,binary:string){const state=await lstat(directory);assert(state.isDirectory()&&!state.isSymbolicLink()&&state.uid===process.getuid?.()&&(state.mode&0o077)===0);const common=JSON.parse(await readFile(join(directory,'profile.json'),'utf8'));const profiles=suiteProfiles(common),stages=await stageSuite(resolve(import.meta.dirname,'../..'),join(directory,'staged'),binary);const suite={schema:1,variant:INITIAL_SUITE,scopeId:profiles[0].scope.id,stages};suiteManifest(suite,profiles);for(const [file,value]of Object.entries({'profiles.json':profiles,'suite.json':suite,'continuity.json':{schema:1,fixture:FIXTURE,firstAttemptId:FIRST,secondAttemptId:SECOND,transitionId:TRANSITION,settingsDigest:settingsDigest('ask'),scopeId:suite.scopeId}}))await writeFile(join(directory,file),JSON.stringify(value),{flag:'wx',mode:0o600});await writeFile(join(directory,'profile.json'),JSON.stringify(profiles[0]),{mode:0o600});return {variant:INITIAL_SUITE,staged:true,clockStarted:false};}
if(process.argv[1]&&resolve(process.argv[1])===resolve(import.meta.filename))console.log(JSON.stringify(await prepareSuite(resolve(process.argv[2]),process.env.GAFFER_OPENCODE_BINARY!)));
