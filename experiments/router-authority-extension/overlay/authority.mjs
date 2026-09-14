import { AsyncLocalStorage } from 'node:async_hooks';
import { randomUUID, createHash } from 'node:crypto';
import { mergeWithDefaults } from '../src/lib/db/repos/settingsRepo.js';
import { existsSync } from 'node:fs';
import { createResponsesTerminalObserver } from './responses-terminal.mjs';
import { createChatTerminalObserver } from './chat-terminal.mjs';

const context = new AsyncLocalStorage();
let instance;
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
  requireThat((nodes.length > 0 || nativeSynthetic) && new Set(nodes.map(n => n.prefix)).size === nodes.length, 'ambiguous_node');
  const connections = (payload.providerConnections || []).map(c => {
    if (c.provider === 'codex') {
      requireThat(nativeSynthetic && ref(c.id) && c.authType === 'oauth' && typeof c.accessToken === 'string', 'native_provider_bound_unsupported');
      for (const key of Object.keys(c)) requireThat(['id','provider','authType','name','email','priority','isActive','createdAt','updatedAt','providerSpecificData'].includes(key) || healthKeys.has(key) || tokenKeys.has(key) || /^modelLock_[a-zA-Z0-9._/-]+$/.test(key), 'unsupported_native_metadata');
      closedKeys(c.providerSpecificData, ['workspaceId','chatgptAccountId']);
      requireThat(Object.values(c.providerSpecificData).every(ref), 'unsupported_workspace_identity');
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
        requireThat(nativeSynthetic && ['gpt-6-astra','gpt-5.6-sol','gpt-5.6-terra'].includes(match[2]), 'unsupported_native_model');
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
  for (const key of ['HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','http_proxy','https_proxy','all_proxy','HEADROOM_URL']) requireThat(!process.env[key], 'unsupported_environment');
  db.exec(`PRAGMA synchronous=FULL;
    CREATE TABLE IF NOT EXISTS gaffer_authority (id INTEGER PRIMARY KEY CHECK(id=1), boot TEXT NOT NULL, generation INTEGER NOT NULL, phase TEXT NOT NULL, revision TEXT, policy TEXT);
    CREATE TABLE IF NOT EXISTS gaffer_receipts (id TEXT PRIMARY KEY, boot TEXT NOT NULL, generation INTEGER NOT NULL, revision TEXT NOT NULL, route TEXT NOT NULL, local_stop TEXT NOT NULL, handler_done INTEGER NOT NULL DEFAULT 0);
    CREATE TABLE IF NOT EXISTS gaffer_missing (id TEXT PRIMARY KEY, boot TEXT NOT NULL, generation INTEGER NOT NULL);
    CREATE TABLE IF NOT EXISTS gaffer_operations (request_id TEXT NOT NULL, ordinal INTEGER NOT NULL, boot TEXT NOT NULL, generation INTEGER NOT NULL, revision TEXT NOT NULL, provider TEXT NOT NULL, model TEXT NOT NULL, connection_id TEXT NOT NULL, terminal TEXT NOT NULL, local_stop TEXT NOT NULL, PRIMARY KEY(request_id, ordinal));`);
  const boot = randomUUID();
  db.transaction(() => {
    const previous = db.get('SELECT * FROM gaffer_authority WHERE id=1');
    db.run('INSERT OR REPLACE INTO gaffer_authority VALUES(1, ?, ?, ?, ?, ?)', [boot, (previous?.generation || 0) + 1, 'closed', previous?.revision || null, previous?.policy || null]);
    db.run("UPDATE gaffer_receipts SET local_stop='crash_unknown' WHERE handler_done=0");
    db.run("UPDATE gaffer_operations SET local_stop='crash_unknown' WHERE terminal='unknown'");
  });
  const active = new Map();
  const state = () => { const s = db.get('SELECT * FROM gaffer_authority WHERE id=1'); requireThat(s.boot === boot, 'stale_boot'); return s; };
  const snapshot = () => project(payloadFromDb(db));
  const receipt = id => {
    const r = db.get('SELECT * FROM gaffer_receipts WHERE id=?', [id]);
    if (!r) return { id, known: false, quiescent: false };
    const operations = db.all('SELECT * FROM gaffer_operations WHERE request_id=? ORDER BY ordinal', [id]);
    return { ...r, known: true, operations, quiescent: r.handler_done === 1 && operations.every(o => o.terminal !== 'unknown') };
  };
  const allQuiet = () => db.get('SELECT COUNT(*) AS n FROM gaffer_missing').n===0 && db.all('SELECT id FROM gaffer_receipts').every(r => receipt(r.id).quiescent);
  const assertContext = ctx => { const s = state(); requireThat(ctx && !ctx.cancel.signal.aborted && ['active','draining'].includes(s.phase) && s.generation === ctx.generation && s.revision === ctx.revision, 'request_fenced'); };
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
    async admission(request, call) {
      requireThat(request.body, 'missing_body');
      const input=request.body.getReader();const chunks=[];let inputBytes=0;
      const ingressAbort=new AbortController();
      const abortIngress=()=>ingressAbort.abort(new Error('ingress_cancelled'));
      request.signal.addEventListener('abort',abortIngress,{once:true});if(request.signal.aborted)abortIngress();
      const ingressTimer=setTimeout(abortIngress,3000);
      try {
        while(true) {
          ingressAbort.signal.throwIfAborted();
          const part=await new Promise((resolve,reject)=>{
            const aborted=()=>reject(ingressAbort.signal.reason);
            ingressAbort.signal.addEventListener('abort',aborted,{once:true});
            input.read().then(resolve,reject).finally(()=>ingressAbort.signal.removeEventListener('abort',aborted));
          });
          if(part.done)break;
          inputBytes+=part.value.byteLength;requireThat(inputBytes<=65536,'request_bytes');chunks.push(Buffer.from(part.value));
        }
      } catch(error) {void input.cancel(error).catch(()=>{});throw error;}
      finally {clearTimeout(ingressTimer);request.signal.removeEventListener('abort',abortIngress);}
      const bodyText = new TextDecoder('utf-8',{fatal:true}).decode(Buffer.concat(chunks));
      const admittedRequest=new Request(request.url,{method:request.method,headers:request.headers,body:bodyText,signal:request.signal});
      const body = JSON.parse(bodyText);
      closedKeys(body, ['model','messages','stream','max_tokens','temperature','tools','tool_choice']);
      requireThat(new URL(request.url).pathname === '/v1/chat/completions' && body.stream === true && Number.isInteger(body.max_tokens) && body.max_tokens > 0 && body.max_tokens <= 1024 && Array.isArray(body.messages), 'unsupported_request');
      for (const m of body.messages) {
        closedKeys(m, ['role','content','tool_calls','tool_call_id','name']);
        requireThat(['system','user','assistant','tool'].includes(m.role) && (typeof m.content === 'string' || m.content === null && m.role === 'assistant'), 'unsupported_message');
        if (m.tool_calls) requireThat(Array.isArray(m.tool_calls) && m.tool_calls.every(t => t.type === 'function' && typeof t.function?.name === 'string' && typeof t.function?.arguments === 'string'), 'unsupported_tool_call');
      }
      if (body.tools) requireThat(Array.isArray(body.tools) && body.tools.every(t => t.type === 'function' && typeof t.function?.name === 'string'), 'unsupported_tools');
      const id = request.headers.get('x-gaffer-request-id');
      const generation = Number(request.headers.get('x-gaffer-generation')); const revision = request.headers.get('x-gaffer-revision');
      requireThat(ref(id), 'invalid_request_id');
      const cancel = new AbortController();
      const ctx = {id, boot, generation, revision, cancel, policy: null, route: body.model, transports:new Set()};
      db.transaction(() => {
        const s = state();requireThat(s.phase==='active','admission_closed'); assertContext(ctx); requireThat(!db.get('SELECT id FROM gaffer_receipts WHERE id=?', [id]), 'duplicate_request_id');
        // Serial native admission prevents cross-request refresh dedup dependencies.
        if (nativeSynthetic) requireThat(active.size === 0 && allQuiet(), 'native_serial_profile');
        const policy = JSON.parse(s.policy); requireThat(canonical(snapshot()) === s.policy && policy.routes.some(r => r.name === body.model), 'policy_mismatch');
        ctx.policy = policy;
        db.run("INSERT INTO gaffer_receipts VALUES(?, ?, ?, ?, ?, 'running', 0)", [id,boot,generation,revision,body.model]);
      });
      active.set(id, ctx);
      const onAbort = () => instance.cancel(id);
      request.signal.addEventListener('abort', onAbort, {once:true}); if (request.signal.aborted) onAbort();
      const timer = setTimeout(onAbort, 30000); timer.unref();
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
      requireThat(nativeSynthetic && provider === 'codex' && ctx.policy.connections.some(c=>c.id===credentials?.connectionId && c.provider===provider), 'unsupported_refresh');
      const result=await context.run({...ctx,executor:{provider,model:'credential_refresh',connection:credentials.connectionId,kind:'refresh'}},call);
      assertContext(ctx); return result;
    },
    credentialUpdate(connectionId, call) {
      const ctx=context.getStore();assertContext(ctx);
      requireThat(receipt(ctx.id).operations.some(o=>o.connection_id===connectionId && o.terminal==='provider_refresh_terminal'),'unproved_credential_update');
      return context.run({...ctx,credentialWrite:connectionId},call);
    },
    assertCurrent: () => assertContext(context.getStore()),
    async delay(ms) {
      const ctx = context.getStore(); assertContext(ctx);
      await new Promise((resolve,reject) => {
        const abort = () => { clearTimeout(timer); reject(ctx.cancel.signal.reason); };
        const timer = setTimeout(() => { ctx.cancel.signal.removeEventListener('abort',abort); resolve(); }, ms);
        ctx.cancel.signal.addEventListener('abort',abort,{once:true});
      });
      assertContext(ctx);
    },
    async fetch(call, url, options, proxyOptions) {
      const ctx = context.getStore(); assertContext(ctx); requireThat(ctx.executor, 'untracked_fetch');
      const conn = ctx.policy.connections.find(c => c.id === ctx.executor.connection);
      requireThat(String(url) === (ctx.executor.kind==='refresh' ? conn.refreshEndpoint : conn.endpoint) && !proxyOptions?.connectionProxyEnabled && !proxyOptions?.vercelRelayUrl && !proxyOptions?.enabled, 'unapproved_transport');
      requireThat(!mitmHosts.some(host => new URL(url).hostname.includes(host)), 'unsupported_mitm_transport');
      let ordinal;
      db.transaction(() => {
        assertContext(ctx); ordinal = db.get('SELECT COUNT(*) AS n FROM gaffer_operations WHERE request_id=?',[ctx.id]).n + 1;
        requireThat(ordinal <= 16, 'subattempt_limit');
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
        response = await call(url, {...options, signal: AbortSignal.any([ctx.cancel.signal,transportAbort.signal,...(options.signal ? [options.signal] : [])]), redirect:'error'}, proxyOptions);
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
        reader=response.body.getReader();
        let transportClosed=false;let responseBytes=0;
        const finalize=reason=>{
          const result=observer.finish({reason});
          const terminal=result.disposition==='provider_terminal' ? (native ? `provider_${result.terminal.kind}` : 'provider_terminal') : 'unknown';
          stop(terminal,`original_${reason}`);
        };
        return new Response(new ReadableStream({
          async pull(controller) {
            try {
              const part=await reader.read();if(transportClosed)return;
              if(part.done){transportClosed=true;finalize('eof');release();controller.close();return;}
              responseBytes+=part.value.byteLength;requireThat(responseBytes<=1048576,'response_bytes');
              const observed=observer.push(part.value);requireThat(!observed.invalidReason,'unsupported_original_stream');controller.enqueue(part.value);
            }catch(error){
              if(transportClosed)return;transportClosed=true;finalize('error');ctx.cancel.abort(error);
              void dispose(error);controller.error(error);
            }
          },
          start(controller){cancelObservation=()=>{if(transportClosed)return;transportClosed=true;finalize('cancel');controller.error(ctx.cancel.signal.reason||new Error('original_transport_cancelled'));};},
          async cancel(reason){cancelObservation();await dispose(reason);}
        }),{status:response.status,headers:response.headers});
      } catch (error) { stop('unknown','transport_error');ctx.cancel.abort(error);await dispose(error);throw error; }
    },
    // Deliberately private-process API; the HTTP service never exposes writer callbacks.
    close: () => { instance.fence(); db.close(); }
  });
  return guarded;
}

export function authority() { requireThat(instance, 'authority_not_initialized'); return instance; }
