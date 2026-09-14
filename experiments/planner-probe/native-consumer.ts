// Staged read-only consumer entry point. Its only network capability is the
// scoped inference socket; it never receives the private gateway control socket.
import assert from 'node:assert/strict';
import { access,readdir,readFile,writeFile } from 'node:fs/promises';
import { connect } from 'node:net';
import { request } from 'node:http';
import { PlannerSession } from './planner.ts';
import { nativePlannerTransport } from './native-boundary-port.ts';
import { NATIVE_PLANNER_BUDGET } from './native-transport.ts';
import { snapshot } from './reader.ts';
import { BRIEF,FIXTURE_ROOT,FIXTURE_HASH } from './public-fixture.ts';
let input='';for await(const chunk of process.stdin){input+=chunk;assert(Buffer.byteLength(input)<=65536);}
const grant=JSON.parse(input);assert.deepEqual(Object.keys(grant).sort(),['binding','policy','token']);
const file=await snapshot(FIXTURE_ROOT,'fixture.txt',FIXTURE_HASH,4096,AbortSignal.timeout(1000));
const planner=new PlannerSession(nativePlannerTransport('/router/inference.sock',grant.token,grant.policy,grant.binding),file,BRIEF,NATIVE_PLANNER_BUDGET),result=await planner.run();
assert.deepEqual(await planner.run(),result);
const inaccessible:string[]=[];
for(const path of ['/state','/control','/private','/config','/egress','/router-source','/probe','/gaffer','/consumer/experiments/router-authority-extension/overlay/evaluation-scope.mjs','/var/run/docker.sock','/Users','/consumer/experiments/native-evaluation','/consumer/experiments/inference-boundary/boundary.ts','/consumer/experiments/inference-boundary/policy.ts','/consumer/experiments/router-authority-extension/overlay/authority.mjs']){await assert.rejects(access(path));inaccessible.push(path);}
assert.deepEqual(await readdir('/router'),['inference.sock']);
await assert.rejects(writeFile(FIXTURE_ROOT+'/fixture.txt','unauthorized change'));assert.equal(await readFile(FIXTURE_ROOT+'/fixture.txt','utf8'),file.text);
const tcp=await new Promise<string>(resolve=>{const socket=connect({host:'1.1.1.1',port:443});socket.setTimeout(100,()=>{socket.destroy();resolve('blocked');});socket.once('error',()=>resolve('blocked'));socket.once('connect',()=>{socket.destroy();resolve('connected');});});assert.equal(tcp,'blocked');
const management=await new Promise<number>(resolve=>{const req=request({socketPath:'/router/inference.sock',path:'/api/providers',method:'GET',headers:{host:'localhost'}},res=>{res.resume();res.on('end',()=>resolve(res.statusCode!));});req.on('error',()=>resolve(0));req.end();});assert.equal(management,404);
console.log(JSON.stringify({result,repeatedRunCached:true,containment:{inaccessible,directTcp:tcp,management,readOnlyFixture:true},runtime:{node:process.version}}));
