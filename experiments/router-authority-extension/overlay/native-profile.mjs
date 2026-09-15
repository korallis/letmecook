import { createHash } from 'node:crypto';
import { validateScope } from './scope-profile.mjs';
export const NATIVE_LIMITS = 'native-subscription-local-v1';
export const NATIVE_PROTOCOL = 'opencode-1.18.30-responses-apply-patch-v1';
export function nativeConsumerRole(profile){if(profile.protocol===NATIVE_PROTOCOL)return 'worker';throw Error('unsupported_native_consumer');}
export const canonical = x => JSON.stringify(x, (_, v) => v && typeof v === 'object' && !Array.isArray(v) ? Object.fromEntries(Object.keys(v).sort().map(k => [k,v[k]])) : v);
export const digest = x => createHash('sha256').update(canonical(x)).digest('hex');
const check = (v, why='invalid_native_profile') => { if(!v)throw Error(why); };
const ref = x => typeof x === 'string' && /^[a-z][a-z0-9_-]{0,63}$/.test(x);
const sha = x => typeof x === 'string' && /^[a-f0-9]{64}$/.test(x);
const exact = (v,names) => check(v && typeof v === 'object' && !Array.isArray(v) && canonical(Object.keys(v).sort())===canonical([...names].sort()));
export const safePath = x => typeof x === 'string' && x.length <= 256 && !x.startsWith('/') && !x.includes('\\') && !/[\u0000-\u0020]/.test(x) && x.split('/').every(s=>s && s!=='.' && s!=='..' && s!=='.git');
export function validateNativeProfile(p) {
 exact(p,['schema','limitsProfile','protocol','evidence','deployment','authorization','scope','connections','local','tools','toolPaths','harness']);
 check(p.schema===1 && p.limitsProfile===NATIVE_LIMITS && p.protocol===NATIVE_PROTOCOL && ['synthetic','reviewed-deployment'].includes(p.evidence));
 exact(p.deployment,['id','sourceCommit','sourceLock','overlay','runtime','isolation','writerFence','credentialOwnershipRef','endpoint']);
 check(ref(p.deployment.id) && p.deployment.sourceCommit==='17c4cc76877bd1755030a8414f8d0083f48dcccf');
 check(['sourceLock','overlay','runtime','isolation','writerFence'].every(k=>sha(p.deployment[k])) && ref(p.deployment.credentialOwnershipRef));
 check(p.deployment.endpoint===(p.evidence==='synthetic'?'http://127.0.0.1:47771/responses':'https://chatgpt.com/backend-api/codex/responses'));
 exact(p.authorization,['id','approved','model','effort','providerOutput','providerMonetaryCap','subscriptionEnvelopeRef','refresh','caseRef']);
 check(ref(p.authorization.id) && p.authorization.approved===true && p.authorization.model==='gpt-6-astra' && p.authorization.effort==='xhigh' && p.authorization.refresh==='denied' && ref(p.authorization.subscriptionEnvelopeRef),'native_authority_required');
 check(canonical(p.authorization.providerOutput)===canonical({requirement:'not_required',capability:'unavailable'}) && canonical(p.authorization.providerMonetaryCap)===canonical({requirement:'not_required',capability:'unavailable'}),'provider_bound_unavailable');
 validateScope(p.scope);check(ref(p.authorization.caseRef)&&p.authorization.caseRef===p.scope.caseRef);check(p.scope.authorizationDigest===digest(p.authorization),'native_authority_mismatch');
 check(Array.isArray(p.connections) && p.connections.length>0 && p.connections.length<=8 && new Set(p.connections.map(c=>c.id)).size===p.connections.length);
 for(const c of p.connections){exact(c,['id','credentialRef','billing','expiresAt','skewMs']);check(ref(c.id)&&ref(c.credentialRef)&&c.billing==='existing-codex-subscription'&&Number.isSafeInteger(c.expiresAt)&&c.expiresAt>0&&Number.isSafeInteger(c.skewMs)&&c.skewMs>=1000&&c.skewMs<=60000,'unclassified_connection');}
 const maxima={requestBytes:262144,responseBytes:1048576,concurrency:1,requestCount:32,totalMs:300000,firstOutputMs:300000,idleMs:120000,attemptMs:p.scope.phase==='initial'?600000:900000};
 exact(p.local,Object.keys(maxima));for(const [k,max] of Object.entries(maxima))check(Number.isSafeInteger(p.local[k])&&p.local[k]>0&&p.local[k]<=max);
 check(p.local.totalMs<=p.scope.elapsedMs&&p.local.attemptMs<=p.scope.elapsedMs&&p.local.firstOutputMs<=p.local.totalMs&&p.local.idleMs<=p.local.totalMs);
 exact(p.harness,['binary','source','settings','builtins','nativeLLM','oauth','websockets']);
 check(p.harness.binary==='01edb5839aa10d5b09133fedcb335a062ecad6e82552933bb14f71756f2b296b'&&p.harness.source==='3104c1428ec91f809e5ab86631300de41eb6952e'&&sha(p.harness.settings)&&p.harness.builtins==='enabled'&&p.harness.nativeLLM===false&&p.harness.oauth===false&&p.harness.websockets===false,'incompatible_native_consumer');
 check(Array.isArray(p.toolPaths)&&p.toolPaths.length>0&&p.toolPaths.length<=64&&p.toolPaths.every(safePath)&&new Set(p.toolPaths).size===p.toolPaths.length);
 check(Array.isArray(p.tools)&&p.tools.length===1);const tool=p.tools[0];exact(tool,['type','name','description','parameters','strict']);
 check(tool.type==='function'&&tool.name==='apply_patch'&&tool.strict===false&&typeof tool.description==='string'&&tool.description.length>0&&tool.description.length<=32768);
 check(canonical(tool.parameters)===canonical({type:'object',properties:{patchText:{type:'string',description:'The full patch text that describes all changes to be made'}},required:['patchText']}),'unsupported_tool_schema');
 check(digest(tool)==='399421a670d0826ce774a6556e18657499ce86afaf0c22cefc6908e7b8a0b128','native_tool_profile_mismatch');
 return structuredClone(p);
}

export function validateNativeRegistry(input){
 const supplied=Array.isArray(input)?input:[input];check(supplied.length>0&&supplied.length<=8,'native_registry_size');
 const profiles=supplied.map(validateNativeProfile),first=profiles[0];check(new Set(profiles.map(digest)).size===profiles.length,'native_registry_duplicate');
 for(const candidate of profiles)for(const key of ['deployment','authorization','scope','connections','evidence'])check(canonical(candidate[key])===canonical(first[key]),'native_registry_scope_mismatch');
 return profiles;
}
