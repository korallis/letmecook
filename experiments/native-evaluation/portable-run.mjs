// Portable source/SQLite regressions. This is deliberately not a claim that a CI
// host matches the separately measured local Docker/ARM64 containment profile.
import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import assert from 'node:assert/strict';
import { routerCases } from './cases.mjs';
assert(existsSync('/.dockerenv'));
const results=[];
for(const scenario of routerCases){
 const file=scenario.startsWith('bootstrap-')?'/gaffer/experiments/native-evaluation/bootstrap-proof.mjs':scenario==='transport'?'/gaffer/experiments/native-evaluation/transport-proof.mjs':'/probe/native-local-integration.mjs';
 const child=spawn(process.execPath,['--experimental-loader','/gaffer/experiments/native-evaluation/fault-loader.mjs',file,scenario],{env:{...process.env,GAFFER_SYNTHETIC_NATIVE:'1',GAFFER_TEST_FAULT:scenario,DATA_DIR:'/tmp/native-ci-'+scenario.replace(/[^a-z0-9-]/gi,'-')},stdio:['ignore','pipe','pipe']});let out='',err='';child.stdout.on('data',b=>out+=b);child.stderr.on('data',b=>err+=b);const timer=setTimeout(()=>child.kill('SIGKILL'),45000);const exit=await new Promise(r=>child.once('exit',(code,signal)=>r({code,signal})));clearTimeout(timer);
 if(exit.code!==0){process.stderr.write(out+err);throw Error('native_case_failed:'+scenario);}
 const value=out.split('\n').filter(l=>l.startsWith('{')).map(l=>JSON.parse(l)).at(-1);assert(value);results.push(value);process.stderr.write('Passed native '+scenario+'\n');
}
console.log(JSON.stringify({schema:1,evidence:'portable-container-native-source-regressions',live:false,localContainmentClaim:false,runtime:{node:process.version,platform:process.platform,arch:process.arch},results}));
