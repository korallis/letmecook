// Offline-only loader selected by run.ts. No deployment launcher selects this.
import { load as upstreamLoad } from '../router-authority-extension/loader.mjs';
export { resolve } from '../router-authority-extension/loader.mjs';
const replace=(source,needle,value)=>{if(source.split(needle).length!==2)throw Error('continuity_fault_anchor');return source.replace(needle,value);};
export async function load(url,context,next) {
 const result=await upstreamLoad(url,context,next);
 if(!url.endsWith('/native-evaluation/gateway.mjs'))return result;
 let source=String(result.source),mode=process.env.GAFFER_CONTINUITY_FAULT;
 if(!['pair','partial','outage','tool','intent-write','result-write','active-stop','start-delay'].includes(mode)||process.env.GAFFER_SYNTHETIC_NATIVE!=='1')throw Error('continuity_synthetic_only');
 source=replace(source,"res.writeHead(200,{'content-type':'text/event-stream'});const bytes=Buffer.from(frames(continuityModule?.syntheticContinuityEvents(continuityFixture,n,body,events)??(n.protocol===PLANNER_PROTOCOL?plannerEvents(body,n):events(!body.input.some(x=>x.type==='function_call_output')))));for(let i=0;i<bytes.length;i+=17)res.write(bytes.subarray(i,i+17));res.end();",`res.writeHead(200,{'content-type':'text/event-stream'});let output=JSON.parse(JSON.stringify(events(${mode==='tool'})).replaceAll('Completed 🌍 café.','GAFFER_CONTINUITY_OK'));${['partial','active-stop'].includes(mode)?'output=output.slice(0,5);':''}const bytes=Buffer.from(frames(output));for(let i=0;i<bytes.length;i+=17)res.write(bytes.subarray(i,i+17));${mode==='active-stop'?"await new Promise(resolve=>res.once('close',resolve));":'res.end();'}`);
 if(mode==='outage')source=source.replaceAll("new Boundary(gate,'/state/router.sock',key)","new Boundary(gate,'/state/missing-router.sock',key)");
 if(mode.endsWith('-write'))source=replace(source,"persist:(kind,value)=>persistImmutable('/state',kind,value)",`persist:(kind,value)=>{if(kind==='continuity-${mode==='intent-write'?'intent':'result'}')throw Error('synthetic_continuity_fsync');return persistImmutable('/state',kind,value);}`);
 return {...result,source};
}
