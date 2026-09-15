// Portable source/SQLite regressions. This is deliberately not a claim that a CI
// host matches the separately measured local Docker/ARM64 containment profile.
import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import assert from 'node:assert/strict';
import { routerCases } from './cases.mjs';
import { collectProcessOutput,caseOutput,writeJSON } from '../router-authority-extension/process-output.mjs';
assert(existsSync('/.dockerenv'));
const results=[];
for(const scenario of routerCases){
 const file=scenario.startsWith('bootstrap-')?'/gaffer/experiments/native-evaluation/bootstrap-proof.mjs':scenario==='transport'?'/gaffer/experiments/native-evaluation/transport-proof.mjs':'/probe/native-local-integration.mjs';
 const child=spawn(process.execPath,['--experimental-loader','/gaffer/experiments/native-evaluation/fault-loader.mjs',file,scenario],{env:{...process.env,GAFFER_SYNTHETIC_NATIVE:'1',GAFFER_TEST_FAULT:scenario,DATA_DIR:'/tmp/native-ci-'+scenario.replace(/[^a-z0-9-]/gi,'-')},stdio:['ignore','pipe','pipe']});const exit=await collectProcessOutput(child);const {stdout:out,stderr:err}=exit;
 if(exit.code!==0){await writeJSON({schema:1,evidence:'portable-container-process-failure',live:false,scenario,exit:exit.code,signal:exit.signal,stdout:out,stderr:err});throw Error('native_case_failed:'+scenario);}
 let value;try{value=caseOutput(out);}catch{await writeJSON({schema:1,evidence:'portable-container-output-failure',live:false,scenario,stdout:out,stderr:err});throw Error('native_case_invalid_output:'+scenario);}assert(value);results.push(value);process.stderr.write('Passed native '+scenario+'\n');
}
await writeJSON({schema:1,evidence:'portable-container-native-source-regressions',live:false,localContainmentClaim:false,runtime:{node:process.version,platform:process.platform,arch:process.arch},results});
