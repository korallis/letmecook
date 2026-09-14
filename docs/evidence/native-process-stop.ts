// Offline measurement of full-tree termination in the native worker profile.
import assert from 'node:assert/strict';
import { spawn,execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { writeFile,readFile } from 'node:fs/promises';
import { randomBytes,createHash } from 'node:crypto';
import { resolve } from 'node:path';
const role=process.argv[2];
if(['worker','child','grandchild'].includes(role)){
 process.on('SIGTERM',()=>{});
 if(role!=='grandchild')spawn(process.execPath,[import.meta.filename,role==='worker'?'child':'grandchild'],{stdio:'inherit'});
 else console.log(JSON.stringify({ready:true,role,pid:process.pid}));
 setInterval(()=>{},100);
}else{
 const {commonArgs}=await import('../../experiments/router-boundary-bridge/isolation.ts');
 const {assertRuntime}=await import('../../experiments/isolation/profile.ts');
 const exec=promisify(execFile),context=process.env.GAFFER_DOCKER_CONTEXT??'desktop-linux';
 const run='native-tree-'+randomBytes(8).toString('hex'),label='dev.gaffer.native-tree-proof',image='gaffer-router-extension-deps:0.5.75-locked',volume=run+'-inference';
 const output=resolve(role),commands=[];let created=false,volumeCreated=false;
 const result:any={schema:1,evidence:'synthetic-native-worker-process-tree-stop',live:false,result:'failed',cleanup:false,producerSha256:createHash('sha256').update(await readFile(import.meta.filename)).digest('hex')};
 async function docker(args:string[]){commands.push(args);const r=await exec('docker',['--context',context,...args],{timeout:15000,maxBuffer:1024*1024});return r.stdout.trim();}
 async function inspect(){const s=JSON.parse(await docker(['inspect',run]))[0];assert.equal(s.Config.Labels[label],run);return s;}
 try{
  const version=JSON.parse(await docker(['version','--format','{{json .}}'])),info=JSON.parse(await docker(['info','--format','{{json .}}']));assertRuntime(version,info);
  const identity=JSON.parse(await readFile(new URL('../../experiments/router-authority-extension/runtime-identity.json',import.meta.url),'utf8'));assert.equal(JSON.parse(await docker(['image','inspect',image]))[0].Id,identity.imageId);result.runtime={server:version.Server,image:identity.imageId};
  await docker(['volume','create','--label',label+'='+run,volume]);volumeCreated=true;
  const args=commonArgs(run,run,false);args[args.indexOf('--label')+1]=label+'='+run;args[args.indexOf('--memory')+1]='768m';args[args.indexOf('--memory-swap')+1]='768m';args.push('--mount',`type=bind,source=${import.meta.filename},target=/fixture/process-stop.ts,readonly`,'--mount',`type=volume,source=${volume},target=/router,readonly,volume-nocopy`,image,'node','/fixture/process-stop.ts','worker');
  await docker(args);created=true;result.before=await inspect();const h=result.before.HostConfig;assert.equal(h.Memory,768*1048576);assert.equal(h.MemorySwap,h.Memory);assert.equal(h.NetworkMode,'none');assert.equal(result.before.Config.User,'1000:1000');assert.equal(h.ReadonlyRootfs,true);assert.deepEqual(h.CapDrop,['ALL']);assert(!h.CapAdd?.length);assert.equal(h.PidsLimit,64);assert.equal(h.NanoCpus,5e8);assert.equal(h.Init,true);assert.equal(h.PidMode,'');assert.equal(h.IpcMode,'private');assert.equal(h.CgroupnsMode,'private');assert(result.before.Mounts.every(m=>!m.RW));
  await docker(['start',run]);const deadline=Date.now()+5000;while(!(await docker(['logs',run])).includes('"ready":true')){assert(Date.now()<deadline);await new Promise(r=>setTimeout(r,20));}
  const tree=(await docker(['top',run,'-eo','pid,ppid,args'])).split('\n');result.tree={titles:tree[0],Processes:tree.slice(1).map(line=>{const match=line.trim().match(/^(\d+)\s+(\d+)\s+(.+)$/);assert(match);return match.slice(1);})};for(const name of ['worker','child','grandchild'])assert(result.tree.Processes.some(p=>p.at(-1).endsWith('process-stop.ts '+name)));
  const started=Date.now();await docker(['stop','--time','1',run]);result.stopMs=Date.now()-started;assert(result.stopMs<5000);result.after=await inspect();assert.equal(result.after.State.Pid,0);assert.equal(result.after.State.Running,false);assert.equal(result.after.State.OOMKilled,false);assert.equal(result.after.State.ExitCode,137);await assert.rejects(docker(['exec',run,'true']));result.postStopExecDenied=true;result.result='passed';
 }catch(error){result.error=String(error);process.exitCode=1;}
 finally{
  if(created){await inspect();await docker(['rm','--force',run]);}if(volumeCreated){assert.equal(JSON.parse(await docker(['volume','inspect',volume]))[0].Labels[label],run);await docker(['volume','rm',volume]);}
  assert.equal(await docker(['ps','-aq','--filter','label='+label+'='+run]),'');result.cleanup=true;result.commands=commands;await writeFile(output,JSON.stringify(result,null,2)+'\n');console.log(JSON.stringify({result:result.result,cleanup:result.cleanup,output}));
 }
}
