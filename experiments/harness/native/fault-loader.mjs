// This loader is selected only by router-run.ts after synthetic-only admission.
import { load as sharedLoad } from '../../native-evaluation/fault-loader.mjs';
export { resolve } from '../../native-evaluation/fault-loader.mjs';
const replaceOne = (source, needle, replacement) => { if (source.split(needle).length !== 2) throw Error('native_harness_fault_anchor_changed'); return source.replace(needle, replacement); };
export async function load(url, context, next) {
  const result = await sharedLoad(url, context, next);
  if (url.endsWith('/native-evaluation/gateway.mjs')) {
    let source = String(result.source);
    source = "import {sendFixture,beforeFinalize,fault} from '/gaffer/experiments/harness/native/gateway-fixtures.mjs';\n" + source;
    source = replaceOne(source, "const observed={sends:[],decisions:[],artifacts:[]};", "const observed={sends:[],decisions:[],artifacts:[],harnessFault:fault};");
    source = replaceOne(source, 'gate.finalize=async(...args)=>{await finalize(...args);', 'gate.finalize=async(...args)=>{await beforeFinalize();await finalize(...args);');
    source = replaceOne(source, "res.writeHead(200,{'content-type':'text/event-stream'});const bytes=Buffer.from(frames(events(observed.sends.length===1)));for(let i=0;i<bytes.length;i+=17)res.write(bytes.subarray(i,i+17));res.end();", 'await sendFixture(res,events,frames,observed.sends.length,body);');
    return { ...result, source };
  }
  if (url.endsWith('/native-evaluation/evidence-control.mjs')) return { ...result, source: "import {beforeArtifact} from '/gaffer/experiments/harness/native/gateway-fixtures.mjs';\n" + replaceOne(String(result.source), 'const saved = persistCandidate(value);', 'const saved = persistCandidate(beforeArtifact(value));') };
  return result;
}
