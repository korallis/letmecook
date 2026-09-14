import { OPENCODE_ROUTER_PROFILE } from './profile-ids.ts';
import { createHash } from 'node:crypto';
import { canonical, keys, object } from './json.ts';
import { Denial, ref, type Binding, type Limits } from './types.ts';
import { validateNativeRouterPolicy, type NativeRouterPolicy } from './native-policy.ts';

export const hashDocument = (value: unknown) => createHash('sha256').update(canonical(value)).digest('hex');
export const sha = (s: unknown): s is string => typeof s === 'string' && /^[a-f0-9]{64}$/.test(s);
export const uuid = (s: unknown): s is string => typeof s === 'string' && /^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(s);
const exact = (value: unknown, names: string[]) => keys(value, names, names);
const requireValue = (value: unknown): void => { if (!value) throw new Denial('policy_denied'); };
const routerRef = (s: unknown) => typeof s === 'string' && /^[a-zA-Z0-9_-]{1,100}$/.test(s);
export interface AuthorityIdentity { deploymentId: string; boot: string; generation: number; revision: string; graphDigest: string }
export type RouterPolicy = StrictRouterPolicy | NativeRouterPolicy;
export interface StrictRouterPolicy {
  schema: 2; routerId: string; routeId: string; revision: string; epoch: number; routerModel: string;
  profile: typeof OPENCODE_ROUTER_PROFILE | 'router-chat-text-tools-synthetic-v1' | 'router-native-chat-translation-synthetic-v1';
  evidence: 'synthetic'; liveAdmission: false; graph: Record<string, any>; limits: Limits;
  authority: AuthorityIdentity;
  envelope: { consumer: 'chat-read-file-v1' | 'opencode-1.18.30-usage-disabled-edit-v1'; cap: 'max_completion_tokens-to-max_tokens-v1' | 'max_tokens-preserved-v1'; replacement: 'read-only'; sourceCommit: string; sourceLock: string; overlay: string; runtime: string };
}
export interface RouterReservation {
  policy: RouterPolicy; binding: Binding; requestDigest: string; send: 'reserved' | 'send_possible';
  nativeRequest?: any;
}
export interface Operation {
  request_id: string; ordinal: number; boot: string; generation: number; revision: string;
  provider: string; model: string; connection_id: string; terminal: string; local_stop: string;
  scope_id?:string;body_digest?:string;output_digest?:string|null;
}
export interface ReceiptEvidence {
  disposition: 'pending_or_unknown' | 'quiescent_failure' | 'original_success';
  receiptDigest: string | null; operations: Operation[];
}
export function validateIdentity(value: AuthorityIdentity) {
  exact(value, ['deploymentId','boot','generation','revision','graphDigest']);
  requireValue(ref(value.deploymentId) && uuid(value.boot) && Number.isSafeInteger(value.generation) && value.generation > 0 && ref(value.revision) && sha(value.graphDigest));
}
export function validateRouterPolicy(p: RouterPolicy): RouterPolicy {
  if(p.schema===3)return validateNativeRouterPolicy(p);
  try {
    exact(p, ['schema','routerId','routeId','revision','epoch','routerModel','profile','evidence','liveAdmission','graph','limits','authority','envelope']);
    const opencode = p.profile === OPENCODE_ROUTER_PROFILE;
    const native = p.profile === 'router-native-chat-translation-synthetic-v1';
    requireValue(p.schema === 2 && (native || opencode || p.profile === 'router-chat-text-tools-synthetic-v1') && p.evidence === 'synthetic' && p.liveAdmission === false);
    requireValue([p.routerId,p.routeId,p.revision].every(ref) && Number.isSafeInteger(p.epoch) && p.epoch > 0 && routerRef(p.routerModel));
    validateIdentity(p.authority); requireValue(p.authority.revision === p.revision);
    exact(p.envelope, ['consumer','cap','replacement','sourceCommit','sourceLock','overlay','runtime']);
    requireValue(p.envelope.consumer === (opencode ? 'opencode-1.18.30-usage-disabled-edit-v1' : 'chat-read-file-v1') && p.envelope.cap === (opencode ? 'max_tokens-preserved-v1' : 'max_completion_tokens-to-max_tokens-v1') && p.envelope.replacement === 'read-only' && p.envelope.sourceCommit === '17c4cc76877bd1755030a8414f8d0083f48dcccf' && [p.envelope.sourceLock,p.envelope.overlay,p.envelope.runtime].every(sha));
    const g = p.graph;
    exact(g, ['schema','profile','liveAdmission','nativeLiveAdmission','nodes','connections','routes','settings','bounds','terminals']);
    requireValue(Buffer.byteLength(canonical(g)) <= 65536 && hashDocument(g) === p.authority.graphDigest && g.schema === 1 && g.liveAdmission === false && g.nativeLiveAdmission === false);
    requireValue(g.profile === (native ? '9router-0.5.75-synthetic-native-authority-v1' : opencode ? '9router-0.5.75-synthetic-opencode-edit-v1' : '9router-0.5.75-synthetic-compatible-chat-v1'));
    requireValue(canonical(g.settings) === canonical({requireApiKey:true,comboStrategy:'fallback',accountStrategy:'fill-first',adapters:false,helpers:false,remoteResources:false,proxies:false,autoPing:false}));
    requireValue(canonical(g.bounds) === canonical({requestBytes:65536,responseBytes:1048576,requestMaxTokens:1024,providerOutputTokens:native ? null : 1024,ingressMs:3000,totalMs:30000,subattempts:16}));
    requireValue(canonical(g.terminals) === canonical(['validated-original-chat-sse','validated-original-json-error',...(native ? ['validated-original-responses-sse','validated-original-refresh-json'] : [])]));
    for (const list of [g.nodes,g.connections,g.routes]) requireValue(Array.isArray(list) && list.length <= 32 && new Set(list.map((x: any) => x.id)).size === list.length);
    requireValue(g.connections.length > 0 && g.routes.length > 0 && (native ? g.nodes.length === 0 && g.connections.every((c: any) => c.provider === 'codex') : g.nodes.length > 0));
    requireValue(new Set(g.nodes.map((n: any) => n.prefix)).size === g.nodes.length && new Set(g.routes.map((r: any) => r.name)).size === g.routes.length);
    for (const n of g.nodes) {
      exact(n,['id','prefix','baseUrl','apiType']);
      const u = new URL(n.baseUrl);
      requireValue(/^openai-compatible-[a-z0-9-]+$/.test(n.id) && routerRef(n.prefix) && n.apiType === 'chat' && u.protocol === 'http:' && u.hostname === '127.0.0.1' && !u.username && !u.password && !u.search && !u.hash && !n.baseUrl.endsWith('/'));
    }
    for (const c of g.connections) {
      const codex = c.provider === 'codex';
      exact(c, ['id','provider','authType','priority','isActive','endpoint','billing',...(codex ? ['refreshEndpoint','workspace','providerOutputBound'] : [])]);
      requireValue(routerRef(c.id) && typeof c.isActive === 'boolean' && (c.priority === null || Number.isSafeInteger(c.priority)));
      if (codex) {
        requireValue(native && c.authType === 'oauth' && c.endpoint === 'http://127.0.0.1:47771/responses' && c.refreshEndpoint === 'http://127.0.0.1:47771/token' && c.billing === 'synthetic-subscription' && c.providerOutputBound === false);
        keys(c.workspace,['workspaceId','chatgptAccountId']); requireValue(Object.values(c.workspace).every(routerRef));
      } else {
        const n = g.nodes.find((n: any) => n.id === c.provider);
        requireValue(n && c.authType === 'apikey' && c.endpoint === `${n.baseUrl}/chat/completions` && c.billing === 'synthetic-api');
      }
    }
    for (const r of g.routes) {
      exact(r,['id','name','strategy','edges']);
      requireValue(routerRef(r.id) && routerRef(r.name) && r.strategy === 'fallback' && Array.isArray(r.edges) && r.edges.length > 0 && r.edges.length <= 8);
      requireValue(new Set(r.edges.map((e: any) => e.member)).size === r.edges.length);
      for (const e of r.edges) {
        exact(e,['member','provider','model','connections']);
        requireValue(typeof e.model === 'string' && /^[a-zA-Z0-9._-]+$/.test(e.model) && e.model !== 'credential_refresh');
        requireValue(e.provider === 'codex' ? native && ['gpt-6-astra','gpt-5.6-sol','gpt-5.6-terra'].includes(e.model) && [`cx/${e.model}`,`codex/${e.model}`].includes(e.member) : g.nodes.some((n: any) => n.id === e.provider) && e.member === `${e.provider}/${e.model}`);
        requireValue(Array.isArray(e.connections) && e.connections.length > 0 && canonical(e.connections) === canonical(g.connections.filter((c: any) => c.provider === e.provider).map((c: any) => c.id)));
      }
    }
    requireValue(g.routes.some((r: any) => r.id === p.routeId && r.name === p.routerModel));
    const maxima: Limits = {requestBytes:65536,responseBytes:1048576,outputTokens:1024,concurrency:native ? 1 : 4,requestCount:32,totalMs:30000,firstOutputMs:30000,idleMs:15000,attemptMs:600000};
    exact(p.limits,Object.keys(maxima));
    for (const k of Object.keys(maxima) as (keyof Limits)[]) requireValue(Number.isSafeInteger(p.limits[k]) && p.limits[k] > 0 && p.limits[k] <= maxima[k]);
    return structuredClone(p);
  } catch { throw new Denial('policy_denied'); }
}

// Receipt validation uses the saved policy, including during recovery from an old boot.
export function classifyReceipt(raw: unknown, id: string, saved: RouterReservation): ReceiptEvidence {
  const pending: ReceiptEvidence = { disposition:'pending_or_unknown',receiptDigest:null,operations:[] };
  try {
    object(raw);
    if (raw.known === false) { exact(raw,['id','known','quiescent']); return pending; }
    const p = saved.policy;
    exact(raw,['id','boot','generation','revision','route','local_stop','handler_done','known','operations','quiescent',...(p.schema===3?['native']:[])]);
    if(p.schema===3)requireValue(canonical(raw.native)===canonical({profileDigest:hashDocument(p.native),scopeId:p.native.scope.id,authorizationDigest:p.native.scope.authorizationDigest,bindingDigest:hashDocument(saved.binding),requestDigest:saved.requestDigest}));
    requireValue(raw.known === true && raw.id === id && raw.boot === p.authority.boot && raw.generation === p.authority.generation && raw.revision === p.revision && raw.route === p.routerModel);
    requireValue(['running','local_eof','local_error','local_cancel','cancelled_unknown','crash_unknown'].includes(raw.local_stop) && [0,1].includes(raw.handler_done) && typeof raw.quiescent === 'boolean' && Array.isArray(raw.operations) && raw.operations.length <= (p.schema===3?p.native.scope.maxInferenceAttempts:16));
    const route = p.graph.routes.find((r: any) => r.id === p.routeId && r.name === p.routerModel);
    for (const [i,o] of raw.operations.entries()) {
      exact(o,['request_id','ordinal','boot','generation','revision','provider','model','connection_id','terminal','local_stop',...(p.schema===3?['scope_id','body_digest','output_digest']:[])]);
      if(p.schema===3)requireValue(o.scope_id===p.native.scope.id&&sha(o.body_digest)&&(o.output_digest===null||sha(o.output_digest))&&(o.terminal!=='provider_completed'||sha(o.output_digest)));
      requireValue(o.request_id === id && o.ordinal === i+1 && o.boot === raw.boot && o.generation === raw.generation && o.revision === raw.revision);
      requireValue(p.graph.connections.some((c: any) => c.id === o.connection_id && c.provider === o.provider && c.isActive === true));
      const refresh = o.model === 'credential_refresh';
      requireValue(refresh ? p.profile === 'router-native-chat-translation-synthetic-v1' && o.provider === 'codex' && route.edges.some((e: any) => e.provider === 'codex' && e.connections.includes(o.connection_id)) : route.edges.some((e: any) => e.provider === o.provider && e.model === o.model && e.connections.includes(o.connection_id)));
      const terminals = refresh ? ['unknown','provider_refresh_terminal','provider_rejected'] : o.provider === 'codex' ? ['unknown','provider_completed','provider_failed','provider_incomplete','provider_rejected'] : ['unknown','provider_terminal','provider_rejected'];
      requireValue(terminals.includes(o.terminal) && ['running','original_eof','original_cancel','original_error','transport_error','crash_unknown'].includes(o.local_stop));
      if (o.terminal !== 'unknown') requireValue(o.local_stop === 'original_eof');
    }
    requireValue(!(raw.handler_done === 1 && ['running','crash_unknown'].includes(raw.local_stop)));
    const quiet = raw.handler_done === 1 && raw.operations.every((o: Operation) => o.terminal !== 'unknown');
    requireValue(raw.quiescent === quiet);
    if (!quiet) return pending;
    const last = raw.operations.filter((o: Operation) => o.model !== 'credential_refresh').at(-1);
    return {disposition:last && ['provider_terminal','provider_completed'].includes(last.terminal) ? 'original_success' : 'quiescent_failure',receiptDigest:hashDocument(raw),operations:structuredClone(raw.operations)};
  } catch { return pending; }
}
