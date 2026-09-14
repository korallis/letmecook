// Verify the reviewed source packet before evaluating the gateway or opening DB.
import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
const hash=bytes=>createHash('sha256').update(bytes).digest('hex');
const canonical=value=>JSON.stringify(value,(_,x)=>x&&typeof x==='object'&&!Array.isArray(x)?Object.fromEntries(Object.keys(x).sort().map(k=>[k,x[k]])):x);
const manifest=JSON.parse(readFileSync('/config/source-manifest.json','utf8')),profile=JSON.parse(readFileSync('/config/profile.json','utf8'));
if(hash(canonical(manifest.files))!==profile.deployment.overlay||hash(canonical(manifest.runtime))!==profile.deployment.runtime||process.version!==manifest.runtime.node||process.platform!=='linux'||process.arch!==manifest.runtime.arch)throw Error('deployment_runtime_manifest_mismatch');
for(const [path,digest] of Object.entries(manifest.files)){
 if(!/^experiments\/[a-z0-9_./-]+$/.test(path)||path.split('/').includes('..')||typeof digest!=='string'||hash(readFileSync('/gaffer/'+path))!==digest)throw Error('deployment_source_mismatch');
}
if(hash(readFileSync('/probe/source-lock.json'))!==profile.deployment.sourceLock)throw Error('deployment_source_lock_mismatch');
await import('./gateway.mjs');
