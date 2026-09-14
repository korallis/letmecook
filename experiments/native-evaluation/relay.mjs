import { createServer,connect } from 'node:net';
import { readFileSync,chmodSync,unlinkSync } from 'node:fs';
import { reviewedAddress } from './egress.mjs';
const config=JSON.parse(readFileSync('/config/relay.json','utf8'));
if(Object.keys(config).sort().join(',')!=='address,evidence,maxBytes,totalMs'||!['synthetic','reviewed-deployment'].includes(config.evidence)||!Number.isSafeInteger(config.maxBytes)||config.maxBytes<1||config.maxBytes>2097152||!Number.isSafeInteger(config.totalMs)||config.totalMs<1||config.totalMs>300000)throw Error('invalid_relay_profile');
const address=reviewedAddress(config.address,config.evidence==='synthetic'),active=new Set();let accepting=false,stopping=false;
const server=createServer(client=>{
 if(!accepting||stopping||active.size){client.destroy();return;}
 let bytes=0;const upstream=connect({host:address,port:443,family:4}),entry={client,upstream};active.add(entry);
 const close=()=>{client.destroy();upstream.destroy();active.delete(entry);clearTimeout(timer);};
 const timer=setTimeout(close,config.totalMs);
 for(const socket of [client,upstream]){socket.on('error',close);socket.on('close',close);socket.on('data',chunk=>{bytes+=chunk.length;if(bytes>config.maxBytes)close();});}
 client.pipe(upstream);upstream.pipe(client);
});
// Host supervisor enables only after exact namespace policy readback, negative
// probes and removal of the privileged helper. No worker sees this signal/socket.
process.once('SIGUSR1',()=>{accepting=true;server.listen('/egress/provider.sock',()=>{chmodSync('/egress/provider.sock',0o600);console.log(JSON.stringify({event:'relay_ready'}));});});
function stop(){stopping=true;accepting=false;clearInterval(keepAlive);for(const {client,upstream}of active){client.destroy();upstream.destroy();}server.close(()=>{try{unlinkSync('/egress/provider.sock');}catch{}process.exit(0);});if(!server.listening)process.exit(0);}
process.on('SIGTERM',stop);process.on('SIGINT',stop);console.log(JSON.stringify({event:'relay_waiting_for_verified_kernel_policy'}));const keepAlive=setInterval(()=>{},1000);
// Keep the unactivated namespace alive without opening a network listener.
process.stdin.resume();
