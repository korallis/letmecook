// Fresh isolated public fixture worker. Only the disposable boundary grant is
// supplied; no provider OAuth/auth files or host environment are inherited.
import { settings,environment } from './opencode-settings.mjs';
import { createServer,request } from 'node:http';
import { spawn,execFileSync } from 'node:child_process';
import { mkdirSync,writeFileSync,readFileSync,existsSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { connect } from 'node:net';
const mode=process.argv[2]??'roundtrip';if(!['roundtrip','disabled-hook'].includes(mode))throw Error('unsupported_proof_mode');
const grant=JSON.parse(readFileSync(0,'utf8'));if(!/^[a-f0-9]{64}$/.test(grant.token)||grant.model!=='gpt-6-astra')throw Error('invalid_worker_grant');
if(createHash('sha256').update(readFileSync('/fixture/opencode')).digest('hex')!=='01edb5839aa10d5b09133fedcb335a062ecad6e82552933bb14f71756f2b296b')throw Error('binary_digest_mismatch');
const deniedPaths=['/state','/control/gateway.sock','/egress/provider.sock','/private/config.json','/router-source','/probe','/var/run/docker.sock','/root/.codex/auth.json','/Users'];
if(deniedPaths.some(existsSync))throw Error('worker_secret_or_control_mount');
const tcpDenied=await new Promise(resolve=>{const s=connect({host:'1.1.1.1',port:443});const timer=setTimeout(()=>{s.destroy();resolve(true);},250);s.once('error',()=>{clearTimeout(timer);resolve(true);});s.once('connect',()=>{clearTimeout(timer);s.destroy();resolve(false);});});if(!tcpDenied)throw Error('worker_provider_tcp_reachable');
mkdirSync('/work/repo');writeFileSync('/work/repo/greeting.txt','hello\n');execFileSync('git',['init','-q','/work/repo']);execFileSync('git',['-C','/work/repo','add','.']);execFileSync('git',['-C','/work/repo','-c','user.name=Fixture','-c','user.email=fixture@example.invalid','commit','-qm','Public fixture']);
const config=settings(grant.token);
writeFileSync('/work/config.json',JSON.stringify(config));const captures=[],chunks=[];
const server=createServer(async(req,res)=>{
 try{if(req.method!=='POST'||req.url!=='/v1/responses'||req.headers.authorization!=='Bearer '+grant.token)throw Error('unsupported_worker_request');let body='';for await(const c of req){body+=c;if(Buffer.byteLength(body)>65536)throw Error('worker_request_bytes');}
 const value=JSON.parse(body);captures.push({path:req.url,headers:Object.fromEntries(Object.entries(req.headers).filter(([k])=>k!=='authorization')),body:value});
 const call=request({socketPath:'/router/inference.sock',path:req.url,method:'POST',headers:{host:'localhost',authorization:'Bearer '+grant.token,'content-type':'application/json','content-length':Buffer.byteLength(body)}});call.on('error',()=>res.destroy());call.on('response',upstream=>{res.writeHead(upstream.statusCode,upstream.headers);upstream.pipe(res);});call.end(body);
 }catch{res.writeHead(400,{'content-type':'application/json'}).end('{"error":"worker_profile_denied"}');}
});await new Promise(r=>server.listen(8765,'127.0.0.1',r));
const env=mode==='disabled-hook'?{...environment,OPENCODE_DISABLE_DEFAULT_PLUGINS:'true'}:environment;
const child=spawn('/fixture/opencode',['run','--format','json','--model','openai/gpt-6-astra','--title','Fixed native fixture','Change greeting.txt from hello to hello from probe.'],{cwd:'/work/repo',env,stdio:['ignore','pipe','pipe']});
child.stdout.on('data',c=>chunks.push(c.toString()));child.stderr.on('data',()=>{});
const deadline=setTimeout(()=>{child.kill('SIGTERM');setTimeout(()=>child.kill('SIGKILL'),1000).unref();},30000);
const exit=await new Promise(r=>child.once('exit',(code,signal)=>r({code,signal})));clearTimeout(deadline);server.closeAllConnections();await new Promise(r=>server.close(r));
console.log(JSON.stringify({event:'worker_finished',exit,captures,output:chunks.join(''),artifact:{path:'greeting.txt',content:readFileSync('/work/repo/greeting.txt','utf8')},containment:{tcpDenied,deniedPaths,workerOAuth:false,providerCredentials:false},config:{...config,provider:{openai:{...config.provider.openai,options:{...config.provider.openai.options,apiKey:'<boundary-grant>'}}}},environment:env}));
