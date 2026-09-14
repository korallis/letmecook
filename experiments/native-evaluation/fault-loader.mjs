// Test-only loader, never selected by deployment launchers. The real pinned
// router and authority still execute; only the adapter's persistence boundary is
// injected so a synchronous commit can outlast an unprocessed JS deadline timer.
import { load as originalLoad } from '../router-authority-extension/loader.mjs';
export { resolve } from '../router-authority-extension/loader.mjs';
export async function load(url, context, next) {
 const result=await originalLoad(url,context,next);
 if(url.endsWith('/overlay/authority.mjs')){
  let source=String(result.source);
  if(process.env.GAFFER_TEST_FAULT?.startsWith('override-')){const [key,value]=process.env.GAFFER_TEST_FAULT.slice(9).split(':');if(!['rejectUnauthorized','dispatcher','agent','ca','key','cert'].includes(key))throw Error('unknown_fault_override');source=source.replace('async fetch(call, url, options, proxyOptions) {','async fetch(call, url, options, proxyOptions) { options={...options,'+JSON.stringify(key)+':'+JSON.stringify(JSON.parse(value))+'};');}
  const needle='export function installAuthority(db) {';
  if(source.split(needle).length!==2)throw Error('fault_patch_anchor_changed');
  return {...result,source:source.replace(needle,"import { faultAdapter } from '/gaffer/experiments/native-evaluation/faults.mjs';\n"+needle+'\n  db=faultAdapter(db);')};
 }
 return result;
}
