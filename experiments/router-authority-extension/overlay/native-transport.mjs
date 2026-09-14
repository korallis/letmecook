import { connect as netConnect } from 'node:net';
import { connect as tlsConnect } from 'node:tls';
import { Agent } from 'undici';
const ORIGIN='https://chatgpt.com';
// No token, account, HTTP routing, response interpretation or retry lives here.
// The selected gateway network namespace is none; only this private UDS reaches
// the fixed-destination relay. TLS remains end-to-end in the authority process.
export function nativeDispatcher(socketPath){
 if(socketPath!=='/egress/provider.sock')throw Error('native_egress_socket_mismatch');
 for(const name of ['NODE_TLS_REJECT_UNAUTHORIZED','NODE_EXTRA_CA_CERTS','SSL_CERT_FILE','SSL_CERT_DIR'])if(process.env[name])throw Error('native_tls_environment_denied');
 const agent=new Agent({connections:1,pipelining:0,connect(options,callback){
  if(options.protocol!=='https:'||options.hostname!=='chatgpt.com'||String(options.port||443)!=='443'){callback(Error('native_tls_origin_denied'),null);return;}
  const socket=netConnect(socketPath);let settled=false,tls;
  const complete=(error,connected)=>{if(settled)return;settled=true;clearTimeout(timer);if(error){socket.destroy();tls?.destroy();}callback(error,connected);};
  const timer=setTimeout(()=>complete(Error('native_tls_connect_deadline'),null),10000);
  socket.once('error',error=>complete(error,null));
  socket.once('connect',()=>{tls=tlsConnect({socket,servername:'chatgpt.com',rejectUnauthorized:true,minVersion:'TLSv1.2'});tls.once('error',error=>complete(error,null));tls.once('secureConnect',()=>complete(null,tls));});
 }});
 return Object.freeze({
  dispatch(options,handler){if(String(options.origin)!==ORIGIN||options.path!=='/backend-api/codex/responses'||options.method!=='POST')throw Error('native_dispatch_origin_denied');return agent.dispatch(options,handler);},
  close:()=>agent.close(),destroy:()=>agent.destroy()
 });
}
