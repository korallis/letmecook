import { spawn } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync, existsSync } from 'node:fs';
import { dirname, join } from 'node:path';
import assert from 'node:assert/strict';
if (!existsSync('/.dockerenv')) throw new Error('isolated_container_required');
const root=dirname(new URL(import.meta.url).pathname);
const results=[];
async function run(file,scenario,{native=false,data=scenario,expectedSignal=null,extraEnv={}}={}) {
  const child=spawn(process.execPath,['--experimental-loader',join(root,'loader.mjs'),join(root,file),scenario],{env:{...process.env,DATA_DIR:`/tmp/gaffer-evidence/${data}`,GAFFER_SYNTHETIC_NATIVE:native?'1':'',...extraEnv},stdio:['ignore','pipe','pipe']});
  let stdout='',stderr='';child.stdout.on('data',chunk=>{stdout+=chunk;});child.stderr.on('data',chunk=>{stderr+=chunk;});
  const timer=setTimeout(()=>child.kill('SIGKILL'),45000);
  const result=await new Promise(resolve=>child.once('exit',(code,signal)=>resolve({code,signal})));clearTimeout(timer);
  if(extraEnv.ROUTER_SOURCE){assert.notEqual(result.code,0);assert.match(stderr,/source_digest_mismatch/);return {scenario,result:'source_digest_refused_before_evaluation'};}
  if(expectedSignal)assert.equal(result.signal,expectedSignal);else if(result.code!==0){process.stderr.write(stdout+stderr);throw new Error(`${file}:${scenario}:exit_${result.code}:${result.signal}`);}
  const record=stdout.split('\n').filter(line=>line.startsWith('{')).map(line=>JSON.parse(line)).at(-1);assert.ok(record,'missing_result_record');
  console.error(`Passed ${file}: ${scenario}`);return record;
}
results.push(await run('integration.mjs','main'));
results.push(await run('policy-regressions.mjs','policy-reserved-keys'));
results.push(await run('request-regressions.mjs','request-compatible'));
results.push(await run('request-regressions.mjs','request-native',{native:true}));
results.push(await run('integration.mjs','unknown'));
results.push(await run('integration.mjs','crash',{data:'crash',expectedSignal:'SIGKILL'}));
results.push(await run('integration.mjs','restart',{data:'crash'}));
for(const scenario of ['success','tools','tools-identity-drift','tools-delta-drift','tools-duplicate-argument-keys','account-fallback','model-fallback','refresh','proactive-refresh','sse-retry','sse-retry-open-eof','retry-cancel','fence-retry','preheaders-cancel','fence-preheaders','refresh-cancel','failed','incomplete','partial','contradictory','oversized']) results.push(await run('native-integration.mjs',scenario,{native:true,data:'native-'+scenario}));
for(const scenario of ['error-oversized-open','error-invalid-utf8','error-invalid-json','success-wrong-content-type-open','refresh-oversized-open','refresh-error-oversized-open','refresh-invalid-utf8','refresh-invalid-schema','error-cancel-complete-json','refresh-cancel-complete-json','error-deadline','native-peek-deadline'])results.push(await run('transport-failures.mjs',scenario,{native:scenario.startsWith('refresh-')||scenario.startsWith('native-'),data:scenario}));
const manifest=JSON.parse(readFileSync(join(root,'source-lock.json')));const first=Object.keys(manifest.files)[0];
const altered=join('/tmp/gaffer-tampered',first);mkdirSync(dirname(altered),{recursive:true});writeFileSync(altered,readFileSync(join('/router-source',first))+'\n// mutation');
results.push(await run('integration.mjs','tampered',{extraEnv:{ROUTER_SOURCE:'/tmp/gaffer-tampered'}}));
console.log(JSON.stringify({schema:1,source:{repository:manifest.repository,commit:manifest.commit,version:manifest.version,verifiedFiles:Object.keys(manifest.files).length},runtime:{node:process.version,platform:process.platform,arch:process.arch},evidenceClass:'isolated-real-router-modules-sqlite-synthetic-http',liveAcceptance:false,results},null,2));
