// Actual production packaging and staged entries, fake providers/credentials only.
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdir,readFile,writeFile } from 'node:fs/promises';
import { resolve,join } from 'node:path';
import { randomBytes } from 'node:crypto';
import { profile } from '../native-evaluation/fixtures.mjs';
import { digest } from '../harness/native/client.ts';
import { persistImmutable } from '../native-evaluation/durable-records.mjs';
import { runSuite } from './suite-run.ts';
import { prepareSuite } from './suite-prepare.ts';
const execute=promisify(execFile),directory=resolve(process.argv[2]??'/tmp/initial-suite-proof-'+randomBytes(6).toString('hex')),mode=process.argv[3]??'normal',root=resolve(import.meta.dirname,'../..'),source=process.env.GAFFER_ROUTER_SOURCE,binary=process.env.GAFFER_OPENCODE_BINARY,context=process.env.GAFFER_DOCKER_CONTEXT??'desktop-linux';assert(source?.startsWith('/')&&binary?.startsWith('/'));assert(['normal','storage','stop','unknown','stale','dependency'].includes(mode));if(['storage','stop','unknown'].includes(mode))assert.equal(process.env.GAFFER_SUITE_TEST,mode);
await mkdir(directory,{recursive:true,mode:0o700});const inputs=join(directory,'inputs');await mkdir(inputs,{mode:0o700});const record=join(directory,'deployment.json'),n=profile();n.deployment.id='synthetic_suite_'+randomBytes(6).toString('hex');
const config={settings:{requireApiKey:true,rtkEnabled:false,headroomEnabled:false,pxpipeEnabled:false,cavemanEnabled:false,ponytailEnabled:false,ccFilterNaming:false,capacityAdapter:Object.fromEntries(['vision','pdf','audioInput','videoInput'].map(k=>[k,{enabled:false,models:[]}]))},providerConnections:n.connections.map((c:any,i:number)=>({id:c.id,provider:'codex',authType:'oauth',name:'Synthetic',priority:i+1,isActive:true,accessToken:'synthetic_access_'+i,expiresAt:new Date(c.expiresAt).toISOString(),providerSpecificData:{chatgptAccountId:'synthetic_workspace_'+i}})),combos:[{id:'native_route',name:'gpt-6-astra',models:['cx/gpt-6-astra']}]};
for(const [name,value]of Object.entries({'profile.json':n,'config.json':config,'relay.json':{address:'172.19.0.2',evidence:'synthetic'}}))await writeFile(join(inputs,name),JSON.stringify(value),{mode:0o600});
await prepareSuite(inputs,binary!);const result:any={schema:1,evidence:'production-initial-suite-synthetic',live:false,issueComplete:false,mode,result:'failed',expectedSuiteResult:mode==='normal'?'passed':'failed',faultLoader:['storage','stop','unknown'].includes(mode)};
const cli=async(command:string,arg?:string)=>{const x=await execute(process.execPath,[join(root,'experiments/native-evaluation/deployment.ts'),command,record,...(arg?[arg]:[]),...(command==='prepare'?[source!]:[])],{timeout:45000,maxBuffer:8*1048576});return JSON.parse(x.stdout);};
const docker=async(args:string[])=>{const x=await execute('docker',['--context',context,...args],{timeout:15000,maxBuffer:8*1048576});return (x.stdout+(args[0]==='logs'?x.stderr:'')).trim();};
let deployment:any;
try{
 result.prepared=await cli('prepare',inputs);result.before=await cli('inspect');assert.equal(result.before.current.scope,null);assert(result.before.packet.initialSuite);deployment=JSON.parse(await readFile(record,'utf8'));assert.equal(digest(deployment.sourceManifest.files),result.before.selectedPolicy.native.deployment.overlay);
 if(mode==='dependency')await writeFile(join(inputs,'staged/planner/experiments/planner-probe/node_modules/ajv/dist/ajv.js'),'synthetic changed dependency');
 let consumerError;try{result.consumed=await runSuite(record,mode==='stale'?'0'.repeat(64):result.prepared.packetDigest,join(directory,'consumer.json'));}catch(error:any){consumerError=String(error.stack??error);result.consumerError=consumerError;}
 assert.equal(!!consumerError,mode!=='normal','suite completion/failure must match the declared case');result.observedSuiteResult=consumerError?'failed':'passed';
 try{const pointer=JSON.parse(await readFile(join(directory,'consumer.json'),'utf8'));result.consumer=JSON.parse(await readFile(join(directory,pointer.snapshot),'utf8'));assert.equal(digest(result.consumer),pointer.digest);}catch{assert.equal(mode,'storage');}
 deployment=JSON.parse(await readFile(record,'utf8'));for(const id of deployment.containers){const s=JSON.parse(await docker(['inspect',id]))[0];assert.equal(s.State.Pid,0);}
 result.retained=JSON.parse(await docker(['run','--rm','--network','none','--user','1000:1000','--cap-drop','ALL','--security-opt','no-new-privileges=true','--read-only','--memory','128m','--memory-swap','128m','--pids-limit','32','--mount',`type=volume,source=${deployment.stateVolume},target=/state,readonly,volume-nocopy`,'--mount',`type=bind,source=${root},target=/gaffer,readonly`,image(),'node','/gaffer/experiments/router-continuity/read-state.mjs']));
 const final=result.retained.records['result.json'];
 if(mode==='normal'){assert.equal(result.consumer.result,'passed');assert.equal(result.consumer.scopeCreated,true);assert.equal(result.consumer.cleanup,true);assert.equal(final.scope.spent,6);assert.equal(final.observed.sends.length,6);assert.equal(final.scope.started,result.consumer.scopeBefore.started);assert.equal(final.scope.deadline,result.consumer.scopeBefore.deadline);for(const phase of result.consumer.phases.slice(0,2))assert.deepEqual(result.retained.records[phase.ack.file],phase.candidate);assert.deepEqual(result.retained.records[result.consumer.transition.file],result.consumer.transition.result);}
 else if(mode==='storage'){assert(final);assert.equal(final.scope.spent,2);assert.equal(final.observed.sends.length,2);assert.equal(final.observed.artifacts.length,0);assert(result.consumer?.phases[0].container);assert(!result.consumer.phases[0].observation,'last durable consumer snapshot must predate failed observation save');const id=result.consumer.phases[0].container,s=JSON.parse(await docker(['inspect',id]))[0];assert.equal(s.State.Pid,0);const logs=await docker(['logs',id]);assert(logs.includes('native_worker_observation'));result.retainedConsumers=[{id,state:s.State,logs}];result.artifactDurability='unavailable_after_storage_failure_and_stop';}
 else if(mode==='unknown'){assert.equal(final.scope.spent,1);assert.equal(final.observed.sends.length,1);assert.equal(result.consumer.phases.length,1);assert(final.receipts.some((r:any)=>r.operations.some((o:any)=>o.terminal==='unknown')));assert.equal(final.observed.artifacts.length,0);}
 else if(mode==='stop'){assert.equal(result.consumer.result,'failed');assert(result.consumer.stopError);assert.equal(result.consumer.phases.length,4);assert(result.consumer.phases.every((x:any)=>x.qualified));assert(result.consumer.fencing.every((x:string)=>x==='fulfilled'));}
 else{assert.equal(final.scope,null);assert.equal(final.observed.sends.length,0);}
 result.result='passed';
}catch(error:any){result.error=String(error.stack??error);process.exitCode=1;}
finally{
 // Test cleanup only after private diagnostic retention; no real deployment here.
 persistImmutable(directory,'initial-suite-deployment',result);let r;try{r=JSON.parse(await readFile(record,'utf8'));}catch{}const errors=[];
 if(r){for(const entry of result.retainedConsumers??[])try{await docker(['rm',entry.id]);}catch{errors.push(entry.id);}for(const id of [...r.transient,...r.containers])try{const s=JSON.parse(await docker(['inspect',id]))[0];assert.equal(s.Config.Labels['dev.gaffer.native-deployment'],r.run);await docker(['rm','--force',id]);}catch{errors.push(id);}for(const v of r.volumes)try{const s=JSON.parse(await docker(['volume','inspect',v]))[0];assert.equal(s.Labels['dev.gaffer.native-state'],n.deployment.id);await docker(['volume','rm',v]);}catch{errors.push(v);}if(r.network)try{const s=JSON.parse(await docker(['network','inspect',r.network]))[0];assert.equal(s.Labels['dev.gaffer.native-deployment'],r.run);await docker(['network','rm',r.network]);}catch{errors.push(r.network);}}
 result.cleanup=errors.length===0;result.cleanupErrors=errors;const saved=persistImmutable(directory,'initial-suite-deployment',result);await writeFile(join(directory,'result.json'),JSON.stringify({result:result.result,cleanup:result.cleanup,snapshot:saved.file,digest:saved.digest})+'\n',{mode:0o600});console.log(JSON.stringify({result:result.result,cleanup:result.cleanup,mode,directory}));
}
function image(){return 'gaffer-router-extension-deps:0.5.75-locked';}
