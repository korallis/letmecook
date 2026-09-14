import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { connect } from 'node:net';
import { createSocket } from 'node:dgram';
const config=JSON.parse(readFileSync('/config/probes.json','utf8'));
const tcp=(host,port)=>new Promise(resolve=>{const s=connect({host,port});let done=false;const end=result=>{if(done)return;done=true;clearTimeout(timer);s.destroy();resolve(result);};const timer=setTimeout(()=>end('blocked'),300);s.once('connect',()=>end('connected'));s.once('error',()=>end('blocked'));});
const results=[];
if(process.argv[2]==='baseline'){for(const [host,port] of [[config.address,443],[config.address,444],[config.other,443]])assert.equal(await tcp(host,port),'connected');console.log(JSON.stringify({event:'unfiltered_controls_reachable',controls:['selected443','selected444','other443']}));process.exit(0);}
for(const [name,host,port,expected] of [['selected',config.address,443,'connected'],['other-port',config.address,444,'blocked'],['other-destination',config.other,443,'blocked'],['host-gateway',config.gateway,443,'blocked'],['dns-tcp','127.0.0.11',53,'blocked'],['loopback','127.0.0.1',443,'blocked'],['metadata','169.254.169.254',80,'blocked'],['public-other','1.1.1.1',443,'blocked'],['ipv6','::1',443,'blocked']]){const result=await tcp(host,port);results.push({name,result});assert.equal(result,expected,name);}
const udp=await new Promise(resolve=>{const s=createSocket('udp4');let done=false;const finish=value=>{if(done)return;done=true;clearTimeout(timer);s.close();resolve(value);};const timer=setTimeout(()=>finish('blocked'),300);s.on('message',()=>finish('received'));s.on('error',()=>finish('blocked'));const message=Buffer.from('abcd01000001000000000000076578616d706c65036f72670000010001','hex');s.send(message,53,'127.0.0.11');});results.push({name:'dns-udp',result:udp});assert.equal(udp,'blocked');
console.log(JSON.stringify({event:'kernel_probes_passed',results}));
