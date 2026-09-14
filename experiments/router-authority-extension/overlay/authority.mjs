import { AsyncLocalStorage } from 'node:async_hooks';
import { randomUUID, createHash } from 'node:crypto';
import { mergeWithDefaults } from '../src/lib/db/repos/settingsRepo.js';
import { existsSync } from 'node:fs';
import { createResponsesTerminalObserver } from './responses-terminal.mjs';
import { createChatTerminalObserver } from './chat-terminal.mjs';
import { stripCodexUnsupportedPatterns } from 'open-sse/utils/codexToolSchema.js';
import { CODEX_DEFAULT_INSTRUCTIONS } from 'open-sse/config/codexInstructions.js';
import { evaluationScopes } from './evaluation-scope.mjs';
import { validateNativeProfile } from './native-profile.mjs';
import { validateNativeRequest, expectedPhysicalRequest, NativeResponsesStream } from './native-responses.mjs';
import { deploymentOwner } from './deployment-owner.mjs';
import { nativeDispatcher } from './native-transport.mjs';

const context = new AsyncLocalStorage();
let instance;
let nativeProfile = null;
export const canonical = value => JSON.stringify(value, (_, v) => v && typeof v === 'object' && !Array.isArray(v) ? Object.fromEntries(Object.keys(v).sort().map(k => [k, v[k]])) : v);
export const digest = value => createHash('sha256').update(canonical(value)).digest('hex');
const deny = (reason = 'authority_closed') => { throw new Error(reason); };
const requireThat = (condition, reason) => { if (!condition) deny(reason); };
const ref = value => typeof value === 'string' && /^[a-zA-Z0-9_-]{1,100}$/.test(value);
const disabled = ['rtkEnabled', 'headroomEnabled', 'pxpipeEnabled', 'cavemanEnabled', 'ponytailEnabled', 'ccFilterNaming', 'outboundProxyEnabled', 'cloudEnabled', 'tunnelEnabled', 'tailscaleEnabled', 'quotaAutoPingEnabled'];
const settingKeys = new Set([...disabled, 'requireApiKey', 'capacityAdapter', 'comboStrategy', 'comboStrategies', 'fallbackStrategy', 'providerStrategies']);
const healthKeys = new Set(['apiKey', 'testStatus', 'lastTested', 'lastError', 'lastErrorAt', 'errorCode', 'rateLimitedUntil', 'backoffLevel', 'lastUsedAt', 'consecutiveUseCount']);
const tokenKeys = new Set(['accessToken','refreshToken','idToken','expiresAt','expiresIn','lastRefreshAt','tokenType','scope']);
const nativeSynthetic = process.env.GAFFER_SYNTHETIC_NATIVE === '1' && existsSync('/.dockerenv');
const nativeEndpoint = 'http://127.0.0.1:47771/responses';
const nativeRefreshEndpoint = 'http://127.0.0.1:47771/token';
const mitmHosts = ['cloudcode-pa.googleapis.com','daily-cloudcode-pa.googleapis.com','api.individual.githubcopilot.com','q.us-east-1.amazonaws.com','codewhisperer.us-east-1.amazonaws.com','api2.cursor.sh'];
const closedKeys = (value, allowed) => requireThat(value && typeof value === 'object' && !Array.isArray(value) && Object.keys(value).every(k => allowed.includes(k)), 'unsupported_policy_field');
const object = value => value !== null && typeof value === 'object' && !Array.isArray(value);
const toolName = value => typeof value === 'string' && /^[a-zA-Z0-9_-]{1,64}$/.test(value);
const toolId = value => typeof value === 'string' && /^[a-zA-Z0-9_-]{1,64}$/.test(value);

function admittedHeaders(request) {
  const forwarded=['authorization','x-api-key','x-gaffer-request-id','x-gaffer-generation','x-gaffer-revision'];
  const transport={
    'content-type': value=>/^application\/json(?:;\s*charset=utf-8)?$/i.test(value),
    accept:value=>['*/*','text/event-stream'].includes(value),
    'accept-language':value=>value==='*',
    'user-agent':value=>['node','undici','gaffer-boundary/1'].includes(value),
    host:value=>/^127\.0\.0\.1(?::[0-9]{1,5})?$/.test(value) && (!value.includes(':') || Number(value.split(':')[1])>=1 && Number(value.split(':')[1])<=65535),
    connection:value=>['keep-alive','close'].includes(value.toLowerCase()),
    'content-length':value=>/^(0|[1-9][0-9]{0,5})$/.test(value) && Number(value)<=(nativeProfile?.local.requestBytes??65536),
    'transfer-encoding':value=>value.toLowerCase()==='chunked',
    'accept-encoding':value=>/^(?:gzip|deflate|br|identity)(?:,\s*(?:gzip|deflate|br|identity))*$/.test(value),
    'sec-fetch-mode':value=>['cors','same-origin','no-cors'].includes(value)
  };
  let bytes=0,count=0;
  for(const [name,value] of request.headers){
    bytes+=Buffer.byteLength(name)+Buffer.byteLength(value);count++;
    requireThat(bytes<=8192 && count<=32,'request_headers_limit');
    if(forwarded.includes(name))continue;
    requireThat(Object.hasOwn(transport,name) && transport[name](value),'unsupported_request_header');
  }
  requireThat(request.headers.has('content-type'),'unsupported_request_header');
  requireThat(!(request.headers.has('content-length') && request.headers.has('transfer-encoding')),'ambiguous_request_framing');
  requireThat(!(request.headers.has('authorization') && request.headers.has('x-api-key')),'ambiguous_api_auth');
  if(request.headers.has('authorization'))requireThat(/^Bearer [^\s,]{1,4096}$/.test(request.headers.get('authorization')),'unsupported_api_auth');
  if(request.headers.has('x-api-key'))requireThat(/^[^\s,]{1,4096}$/.test(request.headers.get('x-api-key')),'unsupported_api_auth');
  // Never pass client identity, framing or session metadata to router detection,
  // normalization or credentials.rawHeaders. Authentication remains router-owned.
  const headers=new Headers({'content-type':'application/json',accept:'text/event-stream'});
  for(const name of forwarded)if(request.headers.has(name))headers.set(name,request.headers.get(name));
  return headers;
}

function validateRequest(body) {
  // Explicit tool_choice and temperature are unsupported by this common profile:
  // the pinned native path drops them, so they are outside this envelope.
  closedKeys(body, ['model','messages','stream','max_tokens','tools']);
  requireThat(body.stream === true && Number.isInteger(body.max_tokens) && body.max_tokens > 0 && body.max_tokens <= 1024 && Array.isArray(body.messages) && body.messages.length > 0 && body.messages.length <= 64, 'unsupported_request');
  if (Object.hasOwn(body,'tools')) {
    requireThat(Array.isArray(body.tools) && body.tools.length > 0 && body.tools.length <= 8, 'unsupported_tools');
    const names=new Set();
    for(const tool of body.tools){
      closedKeys(tool,['type','function']);closedKeys(tool.function,['name','description','parameters']);
      const fn=tool.function;
      requireThat(tool.type==='function' && toolName(fn.name) && !names.has(fn.name), 'unsupported_tools');names.add(fn.name);
      requireThat(!Object.hasOwn(fn,'description') || typeof fn.description==='string' && fn.description.length>0, 'unsupported_tool_description');
      requireThat(object(fn.parameters) && fn.parameters.type==='object' && object(fn.parameters.properties), 'unsupported_tool_parameters');
      // Schema data is not generally validated here. Refuse the precise schema
      // transformation performed by the pinned Codex executor, using its helper.
      requireThat(canonical(stripCodexUnsupportedPatterns(fn.parameters))===canonical(fn.parameters), 'unsupported_tool_schema_mutation');
    }
  }
  const pending=new Set(),seen=new Set();
  let system=false;
  for(const [index,m] of body.messages.entries()){
    requireThat(object(m), 'unsupported_message');
    if(m.role==='assistant'){
      closedKeys(m,['role','content','tool_calls']);
      requireThat(!pending.size && (typeof m.content==='string' && m.content.trim().length>0 || m.content===null && Object.hasOwn(m,'tool_calls')), 'unsupported_message');
      if(Object.hasOwn(m,'tool_calls')){
        requireThat(Array.isArray(m.tool_calls) && m.tool_calls.length>0 && m.tool_calls.length<=8, 'unsupported_tool_call');
        for(const call of m.tool_calls){
          closedKeys(call,['id','type','function']);closedKeys(call.function,['name','arguments']);
          requireThat(call.type==='function' && toolId(call.id) && !seen.has(call.id) && toolName(call.function.name) && typeof call.function.arguments==='string' && object(JSON.parse(call.function.arguments)), 'unsupported_tool_call');
          pending.add(call.id);seen.add(call.id);
        }
      }
    }else if(m.role==='tool'){
      closedKeys(m,['role','content','tool_call_id']);
      requireThat(typeof m.content==='string' && toolId(m.tool_call_id) && pending.delete(m.tool_call_id), 'unsupported_tool_result');
    }else{
      closedKeys(m,['role','content']);
      requireThat(!pending.size && ['system','user'].includes(m.role) && typeof m.content==='string' && m.content.trim().length>0, 'unsupported_message');
      if(m.role==='system'){requireThat(!system && index===0 && body.messages.length>1, 'unsupported_system_messages');system=true;}
    }
  }
  requireThat(!pending.size, 'unmatched_tool_calls');
}

// A closed supported graph, not a best-effort redaction of arbitrary router JSON.
export function project(payload) {
  const raw = payload.settings || {};
  requireThat(Object.keys(raw).every(k => settingKeys.has(k)), 'unsupported_settings');
  const settings = mergeWithDefaults(raw);
  requireThat(settings.requireApiKey === true && disabled.every(k => !settings[k]), 'unsupported_enabled_feature');
  requireThat(settings.comboStrategy === 'fallback' && (!settings.fallbackStrategy || settings.fallbackStrategy === 'fill-first'), 'unsupported_strategy');
  requireThat(Object.keys(settings.comboStrategies).length === 0 && Object.keys(settings.providerStrategies).length === 0, 'unsupported_strategy_override');
  for (const name of ['vision', 'pdf', 'audioInput', 'videoInput']) {
    const entry = settings.capacityAdapter[name];
    requireThat(entry?.enabled === false && (!entry.models || entry.models.length === 0), 'capability_adapter_not_closed');
  }
  requireThat(Object.keys(settings.capacityAdapter).length === 4, 'unknown_adapter');
  for (const name of ['proxyPools', 'customModels']) requireThat((payload[name] || []).length === 0, 'unsupported_graph');
  for (const name of ['modelAliases', 'mitmAlias', 'pricing']) requireThat(Object.keys(payload[name] || {}).length === 0, 'unsupported_graph');
  const nodes = (payload.providerNodes || []).map(n => {
    closedKeys(n, ['id', 'type', 'name', 'createdAt', 'updatedAt', 'prefix', 'baseUrl', 'apiType']);
    requireThat(/^openai-compatible-[a-z0-9-]+$/.test(n.id) && n.type === 'openai-compatible' && ref(n.prefix) && n.apiType === 'chat', 'unsupported_node');
    const url = new URL(n.baseUrl);
    requireThat(['http:', 'https:'].includes(url.protocol) && !url.username && !url.password && !url.search && !url.hash && !n.baseUrl.endsWith('/'), 'unsupported_endpoint');
    requireThat(!mitmHosts.some(host => url.hostname.includes(host)), 'unsupported_mitm_transport');
    requireThat(url.protocol==='http:' && url.hostname==='127.0.0.1', 'non_synthetic_endpoint');
    return { id: n.id, prefix: n.prefix, baseUrl: n.baseUrl, apiType: 'chat' };
  }).sort((a,b) => a.id.localeCompare(b.id));
  requireThat((nodes.length > 0 || nativeSynthetic || nativeProfile) && new Set(nodes.map(n => n.prefix)).size === nodes.length, 'ambiguous_node');
  if(nativeProfile)requireThat(nodes.length===0,'native_no_paid_path');
  const connections = (payload.providerConnections || []).map(c => {
    if (c.provider === 'codex') {
      requireThat((nativeSynthetic || nativeProfile) && ref(c.id) && c.authType === 'oauth' && typeof c.accessToken === 'string' && c.accessToken.length>0, 'native_provider_bound_unsupported');
      for (const key of Object.keys(c)) requireThat(['id','provider','authType','name','email','priority','isActive','createdAt','updatedAt','providerSpecificData'].includes(key) || healthKeys.has(key) || tokenKeys.has(key) || /^modelLock_[a-zA-Z0-9._/-]+$/.test(key), 'unsupported_native_metadata');
      closedKeys(c.providerSpecificData, ['workspaceId','chatgptAccountId']);
      requireThat(Object.values(c.providerSpecificData).every(ref), 'unsupported_workspace_identity');
      if(nativeProfile){
        const admitted=nativeProfile.connections.find(x=>x.id===c.id);
        requireThat(admitted&&c.isActive!==false&&!c.refreshToken&&!c.idToken&&Date.parse(c.expiresAt)===admitted.expiresAt,'native_credential_profile_mismatch');
        return {id:c.id,provider:'codex',authType:'oauth',priority:c.priority??null,isActive:true,endpoint:nativeProfile.deployment.endpoint,workspace:c.providerSpecificData,billing:admitted.billing,providerOutputBound:false,credentialRef:admitted.credentialRef,expiresAt:admitted.expiresAt,skewMs:admitted.skewMs,refresh:false};
      }
      return {id:c.id,provider:'codex',authType:'oauth',priority:c.priority ?? null,isActive:c.isActive !== false,endpoint:nativeEndpoint,refreshEndpoint:nativeRefreshEndpoint,workspace:c.providerSpecificData,billing:'synthetic-subscription',providerOutputBound:false};
    }
    requireThat(ref(c.id) && c.authType === 'apikey' && typeof c.apiKey === 'string' && c.apiKey.length > 0, 'unsupported_credentials');
    const n = nodes.find(n => n.id === c.provider);
    requireThat(n, 'unsupported_provider');
    for (const key of Object.keys(c)) requireThat(['id','provider','authType','name','email','priority','isActive','createdAt','updatedAt','providerSpecificData'].includes(key) || healthKeys.has(key) || /^modelLock_[a-zA-Z0-9._/-]+$/.test(key), 'unsupported_connection_metadata');
    closedKeys(c.providerSpecificData, ['baseUrl', 'apiType']);
    requireThat(c.providerSpecificData.baseUrl === n.baseUrl && c.providerSpecificData.apiType === 'chat', 'node_connection_mismatch');
    return { id: c.id, provider: c.provider, authType: c.authType, priority: c.priority ?? null, isActive: c.isActive !== false, endpoint: `${n.baseUrl}/chat/completions`, billing: 'synthetic-api' };
  }).sort((a,b) => a.id.localeCompare(b.id));
  requireThat(connections.length > 0, 'empty_connections');
  const routes = (payload.combos || []).map(c => {
    requireThat(ref(c.id) && ref(c.name) && (!c.kind || c.kind === 'chat') && Array.isArray(c.models) && c.models.length > 0 && c.models.length <= 8, 'unsupported_combo');
    const edges = c.models.map(member => {
      const match = typeof member === 'string' && /^([a-zA-Z0-9_-]+)\/([a-zA-Z0-9._-]+)$/.exec(member);
      requireThat(match, 'nested_or_unresolved_model');
      if (['cx','codex'].includes(match[1])) {
        requireThat((nativeSynthetic || nativeProfile) && (nativeProfile?['gpt-6-astra']:['gpt-6-astra','gpt-5.6-sol','gpt-5.6-terra']).includes(match[2]), 'unsupported_native_model');
        return {member,provider:'codex',model:match[2],connections:connections.filter(c=>c.provider==='codex').map(c=>c.id)};
      }
      const n = nodes.find(n => n.id === match[1]);
      // Require the literal node ID: user prefixes cannot shadow built-in aliases.
      requireThat(n, 'unresolved_provider');
      return { member, provider: n.id, model: match[2], connections: connections.filter(c => c.provider === n.id).map(c => c.id) };
    });
    requireThat(edges.every(e => e.connections.length), 'empty_edge');
    return { id: c.id, name: c.name, strategy: 'fallback', edges };
  }).sort((a,b) => a.id.localeCompare(b.id));
  requireThat(routes.length > 0, 'empty_routes');
  if(nativeProfile){
    requireThat(connections.length===nativeProfile.connections.length && routes.length===1 && routes[0].edges.length===1,'native_full_graph_mismatch');
    return {schema:2,profile:'9router-0.5.75-native-local-v1',profileDigest:digest(nativeProfile),nodes,connections,routes,
      settings:{requireApiKey:true,comboStrategy:'fallback',accountStrategy:'fill-first',adapters:false,helpers:false,remoteResources:false,proxies:false,autoPing:false,refresh:false},
      bounds:{...nativeProfile.local,providerOutputTokens:null,providerMonetaryCap:null,scope:nativeProfile.scope},terminals:['validated-original-responses-sse','validated-original-json-error']};
  }
  return { schema: 1, profile: nativeSynthetic ? '9router-0.5.75-synthetic-native-authority-v1' : '9router-0.5.75-synthetic-compatible-chat-v1', liveAdmission:false, nativeLiveAdmission: false, nodes, connections, routes,
    settings: { requireApiKey: true, comboStrategy: 'fallback', accountStrategy: 'fill-first', adapters: false, helpers: false, remoteResources: false, proxies: false, autoPing: false },
    bounds: { requestBytes: 65536, responseBytes: 1048576, requestMaxTokens: 1024, providerOutputTokens: nativeSynthetic ? null : 1024, ingressMs:3000, totalMs: 30000, subattempts: 16 },
    terminals: ['validated-original-chat-sse', 'validated-original-json-error', ...(nativeSynthetic ? ['validated-original-responses-sse','validated-original-refresh-json'] : [])] };
}

function payloadFromDb(db) {
  const data = row => ({ ...JSON.parse(row.data), ...Object.fromEntries(Object.entries(row).filter(([k]) => k !== 'data')) });
  const result = { settings: JSON.parse(db.get('SELECT data FROM settings WHERE id = 1')?.data || '{}'),
    providerNodes: db.all('SELECT * FROM providerNodes').map(data),
    providerConnections: db.all('SELECT * FROM providerConnections').map(r => ({...data(r), isActive: r.isActive === 1})),
    combos: db.all('SELECT * FROM combos').map(r => ({...r, models: JSON.parse(r.models)})),
    proxyPools: db.all('SELECT * FROM proxyPools'), customModels: [], modelAliases: {}, mitmAlias: {}, pricing: {} };
  // Every KV-backed map is disabled in this profile. Check stored rows directly:
  // rebuilding a plain object loses __proto__ keys and can hide effective policy.
  const rows = db.all('SELECT scope FROM kv');
  requireThat(rows.every(row => ['customModels','modelAliases','mitmAlias','pricing'].includes(row.scope)), 'unsupported_kv_scope');
  requireThat(rows.length === 0, 'unsupported_graph');
  return result;
}

export function installAuthority(db) {
  requireThat(!instance && db.driver === 'node:sqlite', 'unsupported_runtime');
  const ownerAtBoot=process.env.GAFFER_NATIVE_DEPLOYMENT==='1'?deploymentOwner():null;
  let dispatcher;
  for (const key of ['HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','http_proxy','https_proxy','all_proxy','HEADROOM_URL']) requireThat(!process.env[key], 'unsupported_environment');
  db.exec(`PRAGMA synchronous=FULL;
    CREATE TABLE IF NOT EXISTS gaffer_authority (id INTEGER PRIMARY KEY CHECK(id=1), boot TEXT NOT NULL, generation INTEGER NOT NULL, phase TEXT NOT NULL, revision TEXT, policy TEXT);
    CREATE TABLE IF NOT EXISTS gaffer_receipts (id TEXT PRIMARY KEY, boot TEXT NOT NULL, generation INTEGER NOT NULL, revision TEXT NOT NULL, route TEXT NOT NULL, local_stop TEXT NOT NULL, handler_done INTEGER NOT NULL DEFAULT 0);
    CREATE TABLE IF NOT EXISTS gaffer_missing (id TEXT PRIMARY KEY, boot TEXT NOT NULL, generation INTEGER NOT NULL);
    CREATE TABLE IF NOT EXISTS gaffer_operations (request_id TEXT NOT NULL, ordinal INTEGER NOT NULL, boot TEXT NOT NULL, generation INTEGER NOT NULL, revision TEXT NOT NULL, provider TEXT NOT NULL, model TEXT NOT NULL, connection_id TEXT NOT NULL, terminal TEXT NOT NULL, local_stop TEXT NOT NULL, PRIMARY KEY(request_id, ordinal));`);
  const boot = randomUUID();
  const scopes=evaluationScopes(db,boot);
  db.exec(`CREATE TABLE IF NOT EXISTS gaffer_native_authorizations(id TEXT PRIMARY KEY, digest TEXT NOT NULL, record TEXT NOT NULL);
    CREATE TABLE IF NOT EXISTS gaffer_native_admissions(id TEXT PRIMARY KEY, binding TEXT NOT NULL, request_digest TEXT NOT NULL, request TEXT NOT NULL, profile_digest TEXT NOT NULL, scope_id TEXT NOT NULL, authorization_digest TEXT NOT NULL);`);
  db.transaction(() => {
    const previous = db.get('SELECT * FROM gaffer_authority WHERE id=1');
    db.run('INSERT OR REPLACE INTO gaffer_authority VALUES(1, ?, ?, ?, ?, ?)', [boot, (previous?.generation || 0) + 1, 'closed', previous?.revision || null, previous?.policy || null]);
    db.run("UPDATE gaffer_receipts SET local_stop='crash_unknown' WHERE handler_done=0");
    db.run("UPDATE gaffer_operations SET local_stop='crash_unknown' WHERE terminal='unknown'");
  });
  const active = new Map();
  const state = () => { ownerAtBoot?.assertCurrent();const s = db.get('SELECT * FROM gaffer_authority WHERE id=1'); requireThat(s.boot === boot, 'stale_boot'); return s; };
  const snapshot = () => project(payloadFromDb(db));
  const receipt = id => {
    const r = db.get('SELECT * FROM gaffer_receipts WHERE id=?', [id]);
    if (!r) return { id, known: false, quiescent: false };
    const n=db.get('SELECT * FROM gaffer_native_admissions WHERE id=?',[id]);
    const operations = db.all('SELECT * FROM gaffer_operations WHERE request_id=? ORDER BY ordinal', [id]).map(o=>{
      if(!n)return o;const debit=db.get('SELECT scope_id,body_digest,output_digest FROM gaffer_scope_operations WHERE request_id=? AND ordinal=?',[id,o.ordinal]);return {...o,...debit};
    });
    return { ...r, known: true, operations, quiescent: r.handler_done === 1 && operations.every(o => o.terminal !== 'unknown'),...(n?{native:{profileDigest:n.profile_digest,scopeId:n.scope_id,authorizationDigest:n.authorization_digest,bindingDigest:digest(JSON.parse(n.binding)),requestDigest:n.request_digest}}:{}) };
  };
  const allQuiet = () => db.get('SELECT COUNT(*) AS n FROM gaffer_missing').n===0 && db.all('SELECT id FROM gaffer_receipts').every(r => receipt(r.id).quiescent);
  const nativeTime=ctx=>{
    if(!ctx.native)return Infinity;
    const binding=JSON.parse(ctx.native.binding),limit=scopes.assertCurrent(ctx.native.scope_id,ctx.native.authorization_digest,ctx.tokenDeadline);
    const remaining=Math.min(limit.remainingMs,ctx.requestDeadline-performance.now(),binding.expiresAt-Date.now(),binding.leaseExpiresAt-Date.now());
    requireThat(remaining>0,'native_deadline');return remaining;
  };
  const assertContext = ctx => { const s = state(); requireThat(ctx && !ctx.cancel.signal.aborted && ['active','draining'].includes(s.phase) && s.generation === ctx.generation && s.revision === ctx.revision, 'request_fenced'); nativeTime(ctx); };
  const credentials = () => db.all('SELECT id, provider, data FROM providerConnections').map(row => ({id:row.id,provider:row.provider,secrets:Object.fromEntries(Object.entries(JSON.parse(row.data)).filter(([key])=>key==='apiKey'||tokenKeys.has(key)))}));
  const write = (method, sql, params) => {
    requireThat(!/gaffer_|sqlite_|\b(?:pragma|attach|detach|vacuum|create|drop|alter)\b/i.test(sql), 'protected_storage');
    return db.transaction(() => {
      const s = state(); const writer = context.getStore()?.writer;
      if (writer) requireThat(s.phase === 'mutating' && writer.boot === boot && writer.generation === s.generation, 'stale_writer');
      else requireThat(['active','draining'].includes(s.phase), 'writer_closed');
      if (!writer) requireThat(!/\bapiKeys\b/i.test(sql), 'key_writer_requires_drain');
      const before = writer ? null : canonical(snapshot());
      const beforeCredentials=writer ? [] : credentials();
      const result = db[method](sql, params);
      if (!writer) {
        requireThat(canonical(snapshot()) === before, 'policy_writer_requires_drain');
        const permit=context.getStore()?.credentialWrite;
        for(const after of credentials())if(canonical(after)!==canonical(beforeCredentials.find(c=>c.id===after.id)))requireThat(permit===after.id && after.provider==='codex','credential_writer_requires_drain');
      }
      return result;
    });
  };
  const guarded = Object.freeze({ driver: db.driver,
    run: (sql, params=[]) => write('run', sql, params),
    get: (sql, params=[]) => { requireThat(/^\s*SELECT\b/i.test(sql) && !/gaffer_|;/i.test(sql), 'read_only_query_required'); return db.get(sql,params); },
    all: (sql, params=[]) => { requireThat(/^\s*SELECT\b/i.test(sql) && !/gaffer_|;/i.test(sql), 'read_only_query_required'); return db.all(sql,params); },
    transaction: fn => db.transaction(() => { const value = fn(); requireThat(!value?.then, 'async_sql_transaction'); return value; }),
    exec: () => deny('raw_sql_denied'), checkpoint: () => db.checkpoint(), close: () => deny('trusted_shutdown_only') });
  instance = Object.freeze({
    state: () => ({...state(), policy: state().policy ? JSON.parse(state().policy) : null}), snapshot, project, receipt,
    configureNative(profile){
      requireThat(state().phase==='closed'&&allQuiet()&&!nativeProfile,'native_configuration_closed');
      const next=validateNativeProfile(profile);
      requireThat(next.evidence!=='synthetic'||nativeSynthetic,'native_synthetic_transport_required');
      if(next.evidence==='reviewed-deployment'){
        requireThat(ownerAtBoot&&ownerAtBoot.digest===next.deployment.writerFence&&!nativeSynthetic,'native_deployment_owner_required');
        dispatcher=nativeDispatcher('/egress/provider.sock');
      }
      db.transaction(()=>{
        const previous=db.get('SELECT digest FROM gaffer_native_authorizations WHERE id=?',[next.authorization.id]);
        requireThat(!previous||previous.digest===digest(next.authorization),'native_authorization_changed');
        db.run('INSERT OR IGNORE INTO gaffer_native_authorizations VALUES(?,?,?)',[next.authorization.id,digest(next.authorization),canonical(next.authorization)]);
      });nativeProfile=next;return digest(next);
    },
    startEvaluation(){requireThat(nativeProfile&&state().phase==='active','native_configuration_closed');return scopes.register(nativeProfile.scope,allQuiet);},
    evaluationScope:id=>scopes.read(id),
    closeEvaluation:id=>scopes.close(id,allQuiet),
    prepareNative(record){
      requireThat(nativeProfile&&record?.router?.policy?.schema===3&&record.router.send==='send_possible'&&ref(record.requestId),'native_preparation_required');
      const {policy,binding,requestDigest,nativeRequest}=record.router;
      requireThat(canonical(policy.native)===canonical(nativeProfile)&&policy.authority.boot===boot&&policy.authority.generation===state().generation&&policy.revision===state().revision&&policy.authority.graphDigest===digest(snapshot()),'native_preparation_mismatch');
      requireThat(canonical(binding.native)===canonical({profileDigest:digest(nativeProfile),scopeId:nativeProfile.scope.id,authorizationDigest:nativeProfile.scope.authorizationDigest})&&digest(JSON.stringify(nativeRequest))===requestDigest,'native_preparation_mismatch');
      const tokenDeadline=Math.min(...nativeProfile.connections.map(c=>c.expiresAt-c.skewMs));requireThat(Number.isSafeInteger(tokenDeadline)&&binding.expiresAt>Date.now()&&binding.leaseExpiresAt>Date.now(),'native_expired');
      scopes.assertCurrent(nativeProfile.scope.id,nativeProfile.scope.authorizationDigest,tokenDeadline);
      db.run('INSERT INTO gaffer_native_admissions VALUES(?,?,?,?,?,?,?)',[record.requestId,canonical(binding),requestDigest,JSON.stringify(nativeRequest),digest(nativeProfile),nativeProfile.scope.id,nativeProfile.scope.authorizationDigest]);
    },
    quiescent: ids => db.transaction(()=>{
      const s=state();requireThat(Array.isArray(ids) && ids.every(ref),'invalid_receipt_ids');
      for(const id of ids)if(!receipt(id).known)db.run('INSERT OR IGNORE INTO gaffer_missing VALUES(?, ?, ?)',[id,boot,s.generation]);
      return ids.every(id=>receipt(id).quiescent)&&allQuiet();
    }),
    async replace(expectedGeneration, revision, expectedPolicy, mutation) {
      requireThat(ref(revision), 'invalid_revision');
      let generation;
      db.transaction(() => {
        const s = state(); requireThat(s.generation === expectedGeneration && s.phase !== 'mutating' && revision !== s.revision, 'stale_generation');
        db.run("UPDATE gaffer_authority SET phase='draining' WHERE id=1");
      });
      if (!allQuiet()) return false;
      db.transaction(() => {
        const s = state(); requireThat(s.generation === expectedGeneration && s.phase === 'draining' && allQuiet(), 'drain_changed');
        generation = s.generation + 1;
        db.run("UPDATE gaffer_authority SET phase='mutating', generation=? WHERE id=1", [generation]);
      });
      try {
        await context.run({writer: {boot, generation}}, mutation);
        db.transaction(() => {
          const s = state(); requireThat(s.generation === generation && s.phase === 'mutating', 'stale_writer');
          const actual = snapshot(); requireThat(canonical(actual) === canonical(expectedPolicy), 'policy_readback_mismatch');
          db.run("UPDATE gaffer_authority SET phase='active', revision=?, policy=? WHERE id=1", [revision, canonical(actual)]);
        });
        return true;
      } catch (error) {
        if (state().generation === generation) db.run("UPDATE gaffer_authority SET phase='closed' WHERE id=1");
        throw error;
      }
    },
    cancel(id) {
      state(); const r = receipt(id); if (!r.known) return r;
      db.run("UPDATE gaffer_receipts SET local_stop='cancelled_unknown' WHERE id=?", [id]);
      active.get(id)?.cancel.abort(new Error('request_cancelled'));
      return receipt(id);
    },
    fence() {
      db.transaction(() => { state(); db.run("UPDATE gaffer_authority SET phase='closed', generation=generation+1 WHERE id=1"); });
      for (const ctx of active.values()) ctx.cancel.abort(new Error('request_fenced'));
    },
    async admission(request, call, clientRawRequest = null) {
      requireThat(request.body, 'missing_body');
      const input=request.body.getReader();const chunks=[];let inputBytes=0;
      let headers;
      const ingressAbort=new AbortController();
      const abortIngress=()=>ingressAbort.abort(new Error('ingress_cancelled'));
      request.signal.addEventListener('abort',abortIngress,{once:true});if(request.signal.aborted)abortIngress();
      const ingressTimer=setTimeout(abortIngress,3000);
      try {
        requireThat(clientRawRequest===null,'unsupported_raw_request');
        requireThat(request.method==='POST','unsupported_request_method');
        headers=admittedHeaders(request);
        while(true) {
          ingressAbort.signal.throwIfAborted();
          const part=await new Promise((resolve,reject)=>{
            const aborted=()=>reject(ingressAbort.signal.reason);
            ingressAbort.signal.addEventListener('abort',aborted,{once:true});
            input.read().then(resolve,reject).finally(()=>ingressAbort.signal.removeEventListener('abort',aborted));
          });
          if(part.done)break;
          inputBytes+=part.value.byteLength;requireThat(inputBytes<=(nativeProfile?.local.requestBytes??65536),'request_bytes');chunks.push(Buffer.from(part.value));
        }
        if(request.headers.has('content-length'))requireThat(Number(request.headers.get('content-length'))===inputBytes,'request_length_mismatch');
      } catch(error) {void input.cancel(error).catch(()=>{});throw error;}
      finally {clearTimeout(ingressTimer);request.signal.removeEventListener('abort',abortIngress);}
      const bodyText = new TextDecoder('utf-8',{fatal:true}).decode(Buffer.concat(chunks));
      const admittedRequest=new Request(request.url,{method:'POST',headers,body:bodyText,signal:request.signal});
      const body = JSON.parse(bodyText);
      requireThat(new URL(request.url).pathname === (nativeProfile?'/v1/responses':'/v1/chat/completions'), 'unsupported_request');
      if(!nativeProfile)validateRequest(body);
      const id = request.headers.get('x-gaffer-request-id');
      const generation = Number(request.headers.get('x-gaffer-generation')); const revision = request.headers.get('x-gaffer-revision');
      requireThat(ref(id), 'invalid_request_id');
      const cancel = new AbortController();
      const ctx = {id, boot, generation, revision, cancel, policy: null, route: body.model, transports:new Set()};
      if(nativeProfile){
        const prepared=db.get('SELECT * FROM gaffer_native_admissions WHERE id=?',[id]);
        requireThat(prepared&&prepared.profile_digest===digest(nativeProfile)&&prepared.request_digest===digest(bodyText)&&canonical(JSON.parse(prepared.request))===canonical(body),'native_preparation_required');
        ctx.native=prepared;ctx.tokenDeadline=Math.min(...nativeProfile.connections.map(c=>c.expiresAt-c.skewMs));
        requireThat(Number.isSafeInteger(ctx.tokenDeadline),'native_token_expiry_required');
        ctx.requestDeadline=performance.now()+nativeProfile.local.totalMs;
      }
      db.transaction(() => {
        const s = state();requireThat(s.phase==='active','admission_closed'); assertContext(ctx); requireThat(!db.get('SELECT id FROM gaffer_receipts WHERE id=?', [id]), 'duplicate_request_id');
        // Serial native admission prevents cross-request refresh dedup dependencies.
        if (nativeSynthetic || nativeProfile) requireThat(active.size === 0 && allQuiet(), 'native_serial_profile');
        const policy = JSON.parse(s.policy); requireThat(canonical(snapshot()) === s.policy && policy.routes.some(r => r.name === body.model), 'policy_mismatch');
        ctx.policy = policy;
        db.run("INSERT INTO gaffer_receipts VALUES(?, ?, ?, ?, ?, 'running', 0)", [id,boot,generation,revision,body.model]);
      });
      active.set(id, ctx);
      const onAbort = () => instance.cancel(id);
      request.signal.addEventListener('abort', onAbort, {once:true}); if (request.signal.aborted) onAbort();
      const timer = setTimeout(onAbort, Math.min(nativeProfile?.local.totalMs??30000,nativeTime(ctx))); timer.unref();
      let done = false;
      let onOutputAbort;
      ctx.onTransportsDrained=()=>{
        if(!done || ctx.transports.size)return;
        clearTimeout(timer);request.signal.removeEventListener('abort',onAbort);active.delete(id);
      };
      const finish = kind => {
        if (done) return; done = true;
        if(onOutputAbort)ctx.cancel.signal.removeEventListener('abort',onOutputAbort);
        db.run("UPDATE gaffer_receipts SET handler_done=1, local_stop=CASE WHEN local_stop='running' THEN ? ELSE local_stop END WHERE id=?", [kind,id]);
        // A handler returning does not release ownership of still-open physical work.
        if(ctx.transports.size)ctx.cancel.abort(new Error('handler_finished_with_open_transport'));
        ctx.onTransportsDrained();
      };
      try {
        return await context.run(ctx, async () => {
          assertContext(ctx); const response = await call(admittedRequest);assertContext(ctx);
          if (!response.body) { finish('local_eof'); return response; }
          const reader = response.body.getReader();
          return new Response(new ReadableStream({
            start(controller) {onOutputAbort=()=>{finish('local_cancel');void reader.cancel(ctx.cancel.signal.reason).catch(()=>{});controller.error(ctx.cancel.signal.reason);};ctx.cancel.signal.addEventListener('abort',onOutputAbort,{once:true});},
            async pull(controller) { try { assertContext(ctx);const part = await reader.read();assertContext(ctx); if (part.done) { finish('local_eof'); controller.close(); } else controller.enqueue(part.value); } catch (error) { finish('local_error');void reader.cancel(error).catch(()=>{});controller.error(error); } },
            async cancel() { instance.cancel(id); try { await reader.cancel(); } finally { finish('local_cancel'); } }
          }), {status:response.status, statusText:response.statusText, headers:response.headers});
        });
      } catch (error) { finish('local_error'); throw error; }
    },
    executor(provider, args, call) {
      const ctx = context.getStore(); assertContext(ctx);
      const route = ctx.policy.routes.find(r => r.name === ctx.route);
      requireThat(route.edges.some(e => e.provider === provider && e.model === args.model && e.connections.includes(args.credentials.connectionId)), 'unapproved_executor');
      return context.run({...ctx, executor: {provider, model: args.model, connection: args.credentials.connectionId, kind:'inference'}}, call);
    },
    async refresh(provider, credentials, call) {
      const ctx=context.getStore(); assertContext(ctx);
      requireThat(!nativeProfile,'refresh_not_authorized');
      requireThat(nativeSynthetic && provider === 'codex' && ctx.policy.connections.some(c=>c.id===credentials?.connectionId && c.provider===provider), 'unsupported_refresh');
      const result=await context.run({...ctx,executor:{provider,model:'credential_refresh',connection:credentials.connectionId,kind:'refresh'}},call);
      assertContext(ctx); return result;
    },
    credentialUpdate(connectionId, call) {
      const ctx=context.getStore();assertContext(ctx);
      requireThat(!nativeProfile,'refresh_not_authorized');
      requireThat(receipt(ctx.id).operations.some(o=>o.connection_id===connectionId && o.terminal==='provider_refresh_terminal'),'unproved_credential_update');
      return context.run({...ctx,credentialWrite:connectionId},call);
    },
    assertCurrent: () => assertContext(context.getStore()),
    noRefreshCredentials(provider,credentials){
      if(!nativeProfile)return false;
      const ctx=context.getStore();assertContext(ctx);
      const admitted=nativeProfile.connections.find(c=>c.id===(credentials.connectionId??credentials.id));
      requireThat(provider==='codex'&&admitted&&!credentials.refreshToken&&!credentials.idToken&&Date.parse(credentials.expiresAt)===admitted.expiresAt&&Date.now()<admitted.expiresAt-admitted.skewMs,'native_credential_expired');
      return true;
    },
    async delay(ms) {
      const ctx = context.getStore(); assertContext(ctx);
      requireThat(Number.isSafeInteger(ms)&&ms>=0&&ms<=300000,'invalid_retry_delay');
      const remaining=nativeTime(ctx);
      await new Promise((resolve,reject) => {
        const abort = () => { clearTimeout(timer); reject(ctx.cancel.signal.reason); };
        const timer = setTimeout(() => { ctx.cancel.signal.removeEventListener('abort',abort); if(ms>=remaining)reject(new Error('native_deadline'));else resolve(); }, Math.min(ms,remaining));
        ctx.cancel.signal.addEventListener('abort',abort,{once:true});
      });
      assertContext(ctx);
    },
    async fetch(call, url, options, proxyOptions) {
      const ctx = context.getStore(); assertContext(ctx); requireThat(ctx.executor, 'untracked_fetch');
      if(db.all('SELECT terminal FROM gaffer_operations WHERE request_id=?',[ctx.id]).some(o=>o.terminal==='unknown')){ctx.cancel.abort(new Error('prior_operation_unknown'));deny('prior_operation_unknown');}
      const conn = ctx.policy.connections.find(c => c.id === ctx.executor.connection);
      if(nativeProfile)requireThat(!['dispatcher','agent','ca','key','cert','rejectUnauthorized'].some(key=>Object.hasOwn(options,key)),'native_transport_override_denied');
      requireThat(String(url) === (ctx.executor.kind==='refresh' ? conn.refreshEndpoint : conn.endpoint) && !proxyOptions?.connectionProxyEnabled && !proxyOptions?.vercelRelayUrl && !proxyOptions?.enabled, 'unapproved_transport');
      requireThat(!mitmHosts.some(host => new URL(url).hostname.includes(host)), 'unsupported_mitm_transport');
      let ordinal;
      db.transaction(() => {
        assertContext(ctx); ordinal = db.get('SELECT COUNT(*) AS n FROM gaffer_operations WHERE request_id=?',[ctx.id]).n + 1;
        // A cancelled peek or failed transport is not permission to retry. This
        // gate is shared by inference, account/model fallback and refresh.
        requireThat(db.all('SELECT terminal FROM gaffer_operations WHERE request_id=?',[ctx.id]).every(o => o.terminal !== 'unknown'), 'prior_operation_unknown');
        requireThat(ordinal <= (nativeProfile?nativeProfile.scope.maxInferenceAttempts:16), 'subattempt_limit');
        if(nativeProfile){
          requireThat(ctx.native&&ctx.executor.kind==='inference'&&ctx.executor.model==='gpt-6-astra','native_send_not_authorized');
          requireThat(options.method==='POST'&&typeof options.body==='string','native_serialized_body_required');
          const actual=JSON.parse(options.body),expected=expectedPhysicalRequest(JSON.parse(ctx.native.request),ctx.executor.model,CODEX_DEFAULT_INSTRUCTIONS);
          requireThat(canonical(actual)===canonical(expected),'native_physical_body_changed');
          scopes.debit({id:ctx.native.scope_id,authorizationDigest:ctx.native.authorization_digest,tokenDeadline:ctx.tokenDeadline,requestId:ctx.id,ordinal,kind:ctx.executor.kind,bodyDigest:digest(actual)});
        }
        db.run("INSERT INTO gaffer_operations VALUES(?, ?, ?, ?, ?, ?, ?, ?, 'unknown', 'running')", [ctx.id,ordinal,boot,ctx.generation,ctx.revision,ctx.executor.provider,ctx.executor.model,ctx.executor.connection]);
      });
      const stop = (terminal, local) => db.run('UPDATE gaffer_operations SET terminal=?, local_stop=? WHERE request_id=? AND ordinal=?', [terminal,local,ctx.id,ordinal]);
      const transportAbort=new AbortController();
      let response, reader, cancelObservation;
      let released=false, disposing;
      const release=()=>{
        if(released)return;released=true;
        ctx.cancel.signal.removeEventListener('abort',onAbort);
        ctx.transports.delete(dispose);ctx.onTransportsDrained();
      };
      const dispose=reason=>{
        if(disposing)return disposing;
        cancelObservation?.();transportAbort.abort(reason);
        disposing=(async()=>{
          try {if(reader)await reader.cancel(reason);else if(response?.body)await response.body.cancel(reason);}
          catch { /* The abort signal still owns the supported native fetch. */ }
          finally {release();}
        })();
        return disposing;
      };
      const onAbort=()=>{void dispose(ctx.cancel.signal.reason);};
      ctx.transports.add(dispose);ctx.cancel.signal.addEventListener('abort',onAbort,{once:true});
      try {
        // Synchronous FULL SQLite commit can consume the remaining elapsed or
        // credential window before timer callbacks run. Keep its debit, but
        // recheck immediately before handing bytes to the transport.
        assertContext(ctx);
        response = await call(url, {...options,...(dispatcher?{dispatcher}:{}),signal: AbortSignal.any([ctx.cancel.signal,transportAbort.signal,...(options.signal ? [options.signal] : [])]), redirect:'error'}, proxyOptions);
        assertContext(ctx);
        if (!response.ok || ctx.executor.kind==='refresh') {
          reader = response.body?.getReader(); let bytes = 0; const chunks = [];
          if (reader) while (true) { const part = await reader.read(); assertContext(ctx);if (part.done) break; bytes += part.value.byteLength; requireThat(bytes <= 1048576, 'response_bytes'); chunks.push(Buffer.from(part.value)); }
          const text = new TextDecoder('utf-8',{fatal:true}).decode(Buffer.concat(chunks)); let rejection = false;
          let refreshed=false;
          try { const json = JSON.parse(text); rejection = response.status >= 400 && (typeof json.error?.message === 'string' || typeof json.error === 'string'); refreshed=ctx.executor.kind==='refresh' && response.ok && typeof json.access_token==='string' && typeof json.refresh_token==='string'; } catch {}
          requireThat(response.headers.get('content-type')?.includes('application/json') && (refreshed || rejection),'unsupported_original_json');
          stop(refreshed ? 'provider_refresh_terminal' : 'provider_rejected', 'original_eof');release();
          return new Response(text, {status:response.status,headers:response.headers});
        }
        requireThat(response.headers.get('content-type')?.includes('text/event-stream') && response.body, 'unsupported_provider_response');
        const native=ctx.executor.provider==='codex';
        const observer=native ? createResponsesTerminalObserver({maxBytes:1048576}) : createChatTerminalObserver({maxBytes:1048576,expectedModel:ctx.executor.model});
        const nativeCodec=nativeProfile?new NativeResponsesStream(nativeProfile):null;
        reader=response.body.getReader();
        let transportClosed=false;let responseBytes=0;
        const finalize=reason=>{
          const result=observer.finish({reason});
          let terminal=result.disposition==='provider_terminal' ? (native ? `provider_${result.terminal.kind}` : 'provider_terminal') : 'unknown';
          let outputDigest=null;
          if(nativeCodec&&terminal==='provider_completed'){
            try{requireThat(reason==='eof','native_original_eof_required');nativeCodec.end();outputDigest=digest(nativeCodec.nativeOutput);}catch{terminal='unknown';}
          }
          db.transaction(()=>{if(outputDigest)db.run('UPDATE gaffer_scope_operations SET output_digest=? WHERE request_id=? AND ordinal=?',[outputDigest,ctx.id,ordinal]);stop(terminal,`original_${reason}`);});
        };
        return new Response(new ReadableStream({
          async pull(controller) {
            try {
              const part=await reader.read();if(transportClosed)return;
              if(part.done){finalize('eof');transportClosed=true;release();controller.close();return;}
              responseBytes+=part.value.byteLength;requireThat(responseBytes<=1048576,'response_bytes');
              const observed=observer.push(part.value);requireThat(!observed.invalidReason,'unsupported_original_stream');nativeCodec?.push(part.value);controller.enqueue(part.value);
            }catch(error){
              if(transportClosed)return;transportClosed=true;try{finalize('error');}catch{/* Keep the original debit unknown if receipt persistence fails. */}ctx.cancel.abort(error);
              void dispose(error);controller.error(error);
            }
          },
          start(controller){cancelObservation=()=>{if(transportClosed)return;transportClosed=true;try{finalize('cancel');}catch{/* Durable operation remains unknown. */}controller.error(ctx.cancel.signal.reason||new Error('original_transport_cancelled'));};},
          async cancel(reason){cancelObservation();await dispose(reason);}
        }),{status:response.status,headers:response.headers});
      } catch (error) { stop('unknown','transport_error');ctx.cancel.abort(error);await dispose(error);throw error; }
    },
    // Deliberately private-process API; the HTTP service never exposes writer callbacks.
    close: () => { instance.fence();void dispatcher?.destroy();db.close(); }
  });
  return guarded;
}

export function authority() { requireThat(instance, 'authority_not_initialized'); return instance; }
