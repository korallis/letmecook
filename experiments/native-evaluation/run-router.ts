import { routerCases } from './cases.mjs';
import assert from 'node:assert/strict';
import { resolve } from 'node:path';
import { randomBytes } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { readFileSync, writeFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
const root=resolve(process.argv[2]??'/tmp/gaffer-native-router-matrix');const repository=resolve(import.meta.dirname,'../..');
import { mkdirSync } from 'node:fs';mkdirSync(root,{recursive:true});
const probe=repository+'/experiments/router-authority-extension';
const source=process.env.GAFFER_ROUTER_SOURCE;assert(source?.startsWith('/'),'absolute pinned router source required');
const image='gaffer-router-extension-deps:0.5.75-locked';
const context=process.env.GAFFER_DOCKER_CONTEXT??'desktop-linux', label='dev.gaffer.authority-extension', runId='native-matrix-'+randomBytes(5).toString('hex');
const commands=[];
function docker(args, timeout=15000) { commands.push(['docker','--context',context,...args]); return execFileSync('docker',['--context',context,...args],{encoding:'utf8',timeout,maxBuffer:8*1024*1024}); }
const version=JSON.parse(docker(['version','--format','{{json .}}']));
const info=JSON.parse(docker(['info','--format','{{json .}}']));
const profile=JSON.parse(readFileSync(repository+'/experiments/isolation/profile.json','utf8'));
assert.equal(version.Server.Version,profile.engineVersion); assert.equal(version.Server.KernelVersion,profile.kernelVersion); assert.equal(version.Server.Arch,profile.architecture);assert.equal(version.Server.Os,'linux'); assert.equal(info.CgroupVersion,'2');
for(const key of ['MemoryLimit','SwapLimit','CpuCfsQuota','PidsLimit'])assert.equal(info[key],true);
assert(info.SecurityOptions.includes('name=seccomp,profile=builtin'));
const imageId=JSON.parse(docker(['image','inspect',image]))[0].Id;
assert.equal(imageId,JSON.parse(readFileSync(probe+'/runtime-identity.json','utf8')).imageId);
const result:any={runId,startedAt:new Date().toISOString(),realProviderCalled:false,networkEgress:false,syntheticOriginalHttp:true,runtime:{server:version.Server,imageId,profile,cgroupVersion:info.CgroupVersion},cases:[],cleanup:[]};
writeFileSync(root+'/run.json',JSON.stringify(result,null,2)+'\n');
for(const scenario of (process.argv[3]?.split(',')??routerCases)){const name=scenario.replace(/[^a-z0-9-]/gi,'-'),file=scenario.startsWith('bootstrap-')?'../gaffer/experiments/native-evaluation/bootstrap-proof.mjs':scenario==='legacy'?'../gaffer/experiments/native-evaluation/legacy-proof.mjs':scenario==='transport'?'../gaffer/experiments/native-evaluation/transport-proof.mjs':'native-local-integration.mjs';
 const container=runId+'-'+name; let created=false;
 try {
  docker(['create','--name',container,'--label',`${label}=${runId}`,'--pull','never','--network','none','--user','1000:1000','--cap-drop','ALL','--security-opt','no-new-privileges=true','--read-only','--init','--cgroupns','private','--ipc','private','--cpus','1','--memory','768m','--memory-swap','768m','--pids-limit','64','--ulimit','nofile=256:256','--ulimit','core=0:0','--shm-size','4m','--restart','no','--log-driver','local','--log-opt','max-size=2m','--log-opt','max-file=1','--log-opt','compress=false','--mount',`type=bind,source=${probe},target=/probe,readonly`,'--mount',`type=bind,source=${source},target=/router-source,readonly`,'--mount',`type=bind,source=${repository},target=/gaffer,readonly`,'--tmpfs','/config:rw,nosuid,nodev,size=1m,uid=1000,gid=1000','--tmpfs','/egress:rw,nosuid,nodev,size=1m,uid=1000,gid=1000','--tmpfs','/tmp:rw,nosuid,nodev,size=180m,uid=1000,gid=1000','--env','HOME=/nonexistent','--env','NODE_OPTIONS=','--env',`DATA_DIR=/tmp/${name}`,'--env','GAFFER_SYNTHETIC_NATIVE=1','--env','GAFFER_TEST_FAULT='+scenario,image,'timeout','--signal=TERM','--kill-after=1s',scenario==='legacy'?'180s':'40s','node','--experimental-loader','/gaffer/experiments/native-evaluation/fault-loader.mjs',`/probe/${file}`,scenario]); created=true;
  const before=JSON.parse(docker(['inspect',container]))[0]; const h=before.HostConfig;
  assert.equal(before.Image,imageId);assert.equal(before.Config.User,'1000:1000');assert.equal(before.Config.Labels[label],runId);assert.equal(h.NetworkMode,'none');assert.equal(h.Privileged,false);assert.equal(h.ReadonlyRootfs,true);assert.deepEqual(h.CapDrop,['ALL']);assert(!h.CapAdd?.length);assert.deepEqual(h.SecurityOpt,['no-new-privileges=true']);assert.equal(h.Init,true);assert.equal(h.PidMode,'');assert.equal(h.IpcMode,'private');assert.equal(h.CgroupnsMode,'private');assert.equal(h.Memory,768*1024*1024);assert.equal(h.MemorySwap,h.Memory);assert.equal(h.NanoCpus,1e9);assert.equal(h.PidsLimit,64);assert.equal(h.RestartPolicy.Name,'no');assert(!h.Devices?.length&&!h.DeviceRequests?.length&&!Object.keys(h.PortBindings??{}).length);assert.equal(before.Mounts.length,3);assert(before.Mounts.every(m=>m.Type==='bind'&&!m.RW));assert.deepEqual(h.Tmpfs,{'/config':'rw,nosuid,nodev,size=1m,uid=1000,gid=1000','/tmp':'rw,nosuid,nodev,size=180m,uid=1000,gid=1000','/egress':'rw,nosuid,nodev,size=1m,uid=1000,gid=1000'});
  writeFileSync(root+'/'+name+'.inspect-before.json',JSON.stringify(before,null,2)+'\n');
  let stdout='',stderr='',executionError=null;
  try { stdout=docker(['start','--attach',container],scenario==='legacy'?185000:45000); } catch(e){stdout=e.stdout??'';stderr=e.stderr??'';executionError=String(e.message);}
  writeFileSync(root+'/'+name+'.stdout.log',stdout);writeFileSync(root+'/'+name+'.stderr.log',stderr);
  const after=JSON.parse(docker(['inspect',container]))[0];writeFileSync(root+'/'+name+'.inspect-after.json',JSON.stringify(after,null,2)+'\n');
  const records=stdout.split('\n').filter(l=>l.startsWith('{')).flatMap(l=>{try{return [JSON.parse(l)];}catch{return []}}); const observation=records.at(-1)??null;
  if(observation)writeFileSync(root+'/'+name+'.json',JSON.stringify(observation,null,2)+'\n');
  result.cases.push({name,scenario,exitCode:after.State.ExitCode,oomKilled:after.State.OOMKilled,pid:after.State.Pid,executionError,report:root+'/'+name+'.json',observation});
  assert.equal(after.State.ExitCode,0);assert.equal(after.State.OOMKilled,false);assert.equal(after.State.Pid,0);assert(observation);
  console.log(JSON.stringify({name,sends:observation.physicalSendCount??observation.sends?.length??observation.physicalSends?.length,terminals:observation.receipt?.operations.map(o=>o.terminal),quiescent:observation.receipt?.quiescent,observed:observation.result??'roundtrip_passed'}));
 } finally {
  if(created){const state=JSON.parse(docker(['inspect',container]))[0];assert.equal(state.Config.Labels[label],runId);docker(['rm','--force',container]);result.cleanup.push({container,removed:true});}
  writeFileSync(root+'/run.json',JSON.stringify(result,null,2)+'\n');writeFileSync(root+'/commands.json',JSON.stringify(commands,null,2)+'\n');
 }
}
const remaining=docker(['ps','-aq','--filter',`label=${label}=${runId}`]).trim();assert.equal(remaining,'');result.cleanupVerified=true;result.finishedAt=new Date().toISOString();writeFileSync(root+'/run.json',JSON.stringify(result,null,2)+'\n');writeFileSync(root+'/commands.json',JSON.stringify(commands,null,2)+'\n');
writeFileSync(root+'/runner.sha256',createHash('sha256').update(readFileSync(import.meta.filename)).digest('hex')+'\n');
