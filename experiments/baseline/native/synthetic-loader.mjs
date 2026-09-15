// Source-locked test instrumentation. The authority, debit, wire and receipt
// checks execute unchanged. This loader is never selected for a live endpoint.
import { load as upstreamLoad } from '../../router-authority-extension/loader.mjs';
export { resolve } from '../../router-authority-extension/loader.mjs';
const once = (source, needle, replacement) => {
  if (source.split(needle).length !== 2) throw Error('baseline_source_anchor_changed');
  return source.replace(needle, replacement);
};
export async function load(url, context, next) {
  const result = await upstreamLoad(url, context, next);
  if (url.endsWith('/overlay/authority.mjs')) {
    let source = "import {tracePhysical} from '/gaffer/experiments/baseline/native/synthetic-control.ts';\n" + String(result.source);
    source = once(source, 'response = await call(url,', 'tracePhysical(ctx.id,ordinal,ctx.native.scope_id,options.body);\n        response = await call(url,');
    return { ...result, source };
  }
  if (!url.endsWith('/native-evaluation/gateway.mjs')) return result;
  let source = String(result.source);
  source = once(source, 'const {handleChat}=await import', `const baselineSources=JSON.parse(readFileSync('/config/source-manifest.json','utf8')).files;
for(const [path,hash] of Object.entries(baselineSources))if(createHash('sha256').update(readFileSync('/gaffer/'+path)).digest('hex')!==hash)throw Error('baseline_source_changed');
const {baselineManifest,baselineStart,caseEvents,baselineTrace}=await import('/gaffer/experiments/baseline/native/synthetic-control.ts');
const baselineCase=baselineManifest(JSON.parse(readFileSync('/config/baseline.json','utf8')),profiles);
const {handleChat}=await import`);
  source = once(source, 'await getAdapter();const a=authority();', 'const baselineDb=await getAdapter();const a=authority();');
  source = once(source, 'const observed={sends:[],decisions:[],artifacts:[]};', 'const observed={sends:[],decisions:[],artifacts:[],baseline:baselineTrace};');
  const response = "res.writeHead(200,{'content-type':'text/event-stream'});const bytes=Buffer.from(frames(continuityModule?.syntheticContinuityEvents(continuityFixture,n,body,events)??(n.protocol===PLANNER_PROTOCOL?plannerEvents(body,n,plannerScenario):events(!body.input.some(x=>x.type==='function_call_output')))));for(let i=0;i<bytes.length;i+=17)res.write(bytes.subarray(i,i+17));res.end();";
  source = once(source, response, "res.writeHead(200,{'content-type':'text/event-stream'});const bytes=Buffer.from(frames(caseEvents(body)));for(let i=0;i<bytes.length;i+=17)res.write(bytes.subarray(i,i+17));res.end();");
  source = once(source, "scopeStatusAtPreparation:'not_started',", "scopeStatusAtPreparation:'not_started',baselineCase,");
  source = once(source, "else if(message.command==='start'&&Object.keys(message).length===2&&message.packetDigest===packetDigest){result=a.startEvaluation();}", "else if(message.command==='start'&&Object.keys(message).length===3&&message.packetDigest===packetDigest){const start=baselineStart(message.registration,baselineCase,packetDigest,policy);persistImmutable('/state','baseline-start',start);result=a.startEvaluation();}");
  source = once(source, "else if(message.command==='evidence'", "else if(message.command==='baseline-proof'&&Object.keys(message).length===1){baselineTrace.scopeOperations=baselineDb.all('SELECT * FROM gaffer_scope_operations ORDER BY rowid');result=baselineTrace;}\n  else if(message.command==='evidence'");
  return { ...result, source };
}
