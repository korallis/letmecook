import assert from 'node:assert/strict';
export const VARIANT='m0-router-boundary-gateway-768m-uds-v1';
export const LABEL='dev.gaffer.router-boundary-bridge';
export function commonArgs(name:string,run:string,gateway:boolean){
 return ['create','--name',name,'--label',`${LABEL}=${run}`,'--pull','never','--network','none','--user','1000:1000','--cap-drop','ALL','--security-opt','no-new-privileges=true','--read-only','--init','--cgroupns','private','--ipc','private','--cpus',gateway?'1':'0.5','--memory',gateway?'768m':'128m','--memory-swap',gateway?'768m':'128m','--pids-limit','64','--ulimit','nofile=256:256','--ulimit','core=0:0','--shm-size','4m','--tmpfs',`/work:rw,exec,nosuid,nodev,size=${gateway?'180m':'32m'},uid=1000,gid=1000,mode=0700`,'--tmpfs','/tmp:rw,noexec,nosuid,nodev,size=8m,uid=1000,gid=1000,mode=0700','--log-driver','local','--log-opt','max-size=2m','--log-opt','max-file=1','--log-opt','compress=false','--restart','no','--workdir','/work','--env','HOME=/nonexistent','--env','NODE_OPTIONS='];
}
export function inspectProfile(s:any,image:string,run:string,gateway:boolean,volume:string,binds:Record<string,string>){
 const h=s.HostConfig;
 assert.equal(s.Config.Image,image);assert.equal(s.Config.Labels[LABEL],run);assert.equal(s.Config.User,'1000:1000');
 assert.equal(h.NetworkMode,'none');assert.equal(h.Privileged,false);assert.equal(h.ReadonlyRootfs,true);assert.deepEqual(h.CapDrop,['ALL']);assert(!h.CapAdd?.length);assert.deepEqual(h.SecurityOpt,['no-new-privileges=true']);assert.equal(h.Init,true);assert.equal(h.PidMode,'');assert.equal(h.IpcMode,'private');assert.equal(h.CgroupnsMode,'private');assert.equal(h.Memory,(gateway?768:128)*1024*1024);assert.equal(h.MemorySwap,h.Memory);assert.equal(h.NanoCpus,gateway?1e9:5e8);assert.equal(h.PidsLimit,64);assert.equal(h.ShmSize,4*1024*1024);assert.equal(h.RestartPolicy.Name,'no');assert.equal(h.LogConfig.Type,'local');assert.deepEqual(h.LogConfig.Config,{'max-file':'1','max-size':'2m',compress:'false'});
 assert.deepEqual(h.Tmpfs,{'/work':`rw,exec,nosuid,nodev,size=${gateway?'180m':'32m'},uid=1000,gid=1000,mode=0700`,'/tmp':'rw,noexec,nosuid,nodev,size=8m,uid=1000,gid=1000,mode=0700'});
 assert.deepEqual([...h.Ulimits].sort((a,b)=>a.Name.localeCompare(b.Name)),[{Name:'core',Hard:0,Soft:0},{Name:'nofile',Hard:256,Soft:256}]);
 assert.equal(s.Mounts.length,Object.keys(binds).length+1);
 for(const [destination,source] of Object.entries(binds))assert(s.Mounts.some((m:any)=>m.Type==='bind'&&m.Destination===destination&&m.Source===source&&!m.RW));
 assert(s.Mounts.some((m:any)=>m.Type==='volume'&&m.Name===volume&&m.Destination==='/router'&&m.RW===gateway));
 assert(!h.Devices?.length&&!h.DeviceRequests?.length&&!Object.keys(h.PortBindings??{}).length);
 return {variant:VARIANT,image:s.Image,user:s.Config.User,network:h.NetworkMode,readOnlyRoot:h.ReadonlyRootfs,capDrop:h.CapDrop,securityOpt:h.SecurityOpt,namespaces:{pid:h.PidMode,ipc:h.IpcMode,cgroup:h.CgroupnsMode},memory:h.Memory,memorySwap:h.MemorySwap,nanoCpus:h.NanoCpus,pidsLimit:h.PidsLimit,ulimits:h.Ulimits,tmpfs:h.Tmpfs,log:h.LogConfig,mounts:s.Mounts.map((m:any)=>({type:m.Type,destination:m.Destination,writable:m.RW}))};
}
