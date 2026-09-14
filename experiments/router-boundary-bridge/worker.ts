// Deliberately standalone: this is the only source mounted into the worker.
import assert from 'node:assert/strict';
import { request } from 'node:http';
import { connect } from 'node:net';
import { access,readdir,readFile } from 'node:fs/promises';
let input='';for await(const chunk of process.stdin)input+=chunk;
const grant=JSON.parse(input);const requests:any[]=[];
async function complete(messages:any[],tools=false) {
 const body=JSON.stringify({model:grant.model,stream:true,max_completion_tokens:64,messages,...(tools?{tools:[{type:'function',function:{name:'read_file',description:'Read one authorized fixture path',parameters:{type:'object',properties:{path:{type:'string',enum:['fixture.txt']}},required:['path'],additionalProperties:false}}}]}:{})});
 return new Promise<any>(resolve=>{
  let settled=false;const end=(value:any)=>{if(!settled){settled=true;requests.push(value);resolve(value);}};
  const req=request({socketPath:'/router/inference.sock',path:'/v1/chat/completions',method:'POST',agent:false,headers:{host:'localhost',authorization:`Bearer ${grant.token}`,'content-type':'application/json','content-length':Buffer.byteLength(body)}},res=>{
   let text='';let firstTerminalAt:number|null=null;res.setEncoding('utf8');res.on('data',chunk=>{text+=chunk;if(text.length>1048576)req.destroy();if(firstTerminalAt===null&&(/"tool_calls"|\[DONE\]/.test(text)))firstTerminalAt=Date.now();});
   res.on('error',()=>end({requestId:res.headers['x-gaffer-request-id']??null,status:res.statusCode,completed:false,calls:[],transportError:true}));
   res.on('end',()=>{const packets=text.split('\n').filter(s=>s.startsWith('data: {')).map(s=>JSON.parse(s.slice(6)));const calls=packets.flatMap(p=>p.choices?.[0]?.delta?.tool_calls??[]);const completed=text.endsWith('data: [DONE]\n\n')&&!text.includes('event: error');end({requestId:res.headers['x-gaffer-request-id']??null,status:res.statusCode,completed,calls:completed?calls:[],firstTerminalAt,error:packets.find(p=>p.error)?.error?.code??null});});
  });req.setTimeout(8000,()=>req.destroy());req.on('error',()=>end({requestId:null,completed:false,calls:[],transportError:true}));req.end(body);
 });
}
const messages:any[]=[{role:'user',content:'Read fixture.txt and report a synthetic result.'}];
const first=await complete(messages,grant.tools);
if(first.completed && first.calls.length){
 assert.equal(first.calls.length,2);assert.equal(new Set(first.calls.map((c:any)=>c.id)).size,2);
 for(const c of first.calls)assert.deepEqual(JSON.parse(c.function.arguments),{path:'fixture.txt'});
 messages.push({role:'assistant',content:null,tool_calls:first.calls.map(({index,...c}:any)=>c)});
 for(const c of first.calls)messages.push({role:'tool',tool_call_id:c.id,content:'Public synthetic fixture content.'});
 await complete(messages,false);
}
const inaccessible:string[]=[];
for(const path of ['/router-source','/probe','/boundary','/inference-boundary','/bridge','/work/router.sock','/work/journal/state.json','/work/approved.json','/var/run/docker.sock']){await assert.rejects(access(path));inaccessible.push(path);}
const sharedEntries=await readdir('/router');if(grant.crash)assert(sharedEntries.every(s=>s==='inference.sock'));else assert.deepEqual(sharedEntries,['inference.sock']);
const tcp=await new Promise<string>(resolve=>{const s=connect({host:'1.1.1.1',port:443});s.setTimeout(100,()=>{s.destroy();resolve('blocked');});s.once('error',()=>resolve('blocked'));s.once('connect',()=>{s.destroy();resolve('connected');});});assert.equal(tcp,'blocked');
const management=await new Promise<number>(resolve=>{const req=request({socketPath:'/router/inference.sock',path:'/api/providers',method:'GET',headers:{host:'localhost'}},res=>{res.resume();res.on('end',()=>resolve(res.statusCode!));});req.on('error',()=>resolve(0));req.end();});assert.equal(management,grant.crash?0:404);
const cg=async(name:string)=>{try{return(await readFile('/sys/fs/cgroup/'+name,'utf8')).trim();}catch{return null;}};
console.log(JSON.stringify({schema:1,role:grant.role,requests,toolEffects:first.completed?first.calls.length:0,containment:{inaccessible,sharedEntries,directTcp:tcp,management},resources:{memoryPeak:await cg('memory.peak'),memoryEvents:await cg('memory.events'),pidsPeak:await cg('pids.peak')}}));
