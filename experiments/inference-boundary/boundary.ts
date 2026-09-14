import { createServer, request, type IncomingMessage, type ServerResponse } from 'node:http';
import { chmod } from 'node:fs/promises';
import { createHash, randomBytes } from 'node:crypto';
import { once } from 'node:events';
import { parseJSON } from './json.ts';
import { OPENCODE_PROFILE } from './profiles.ts';
import { ChatStream, validateRequest } from './protocol.ts';
import { PolicyGate } from './policy.ts';
import { hashDocument } from './router-policy.ts';
import { CHAT_PATH, Denial, validateBinding, type Binding, type Outcome, type Reason } from './types.ts';

interface Scope { binding: Binding; born: number; revoked: boolean }
export interface Audit {
  taskId: string; attemptId: string; requestId: string; role: string; routeId: string;
  policyRevision: string; outcome: Outcome; reason: Reason | null;
  source: 'boundary'; providerAttribution: 'unavailable';
  quiescence: 'verified' | 'unknown';
  receipt?: { digest: string; authorityDigest: string; verdict: string };
}
const hash = (token: string) => createHash('sha256').update(token).digest('hex');
const safeError = (error: unknown) => error instanceof Denial ? error : new Denial('upstream_failure', 502);
export class Boundary {
  private scopes = new Map<string, Scope>();
  private attempts = new Set<string>();
  private active = new Map<string, { scope: Scope; abort: AbortController }>();
  private pending = new Map<AbortController, Scope>();
  private handlers = new Set<Promise<void>>();
  private closed = false;
  private closing: Promise<void> | undefined;
  readonly audit: Audit[] = [];
  readonly server = createServer({ maxHeaderSize: 8192, connectionsCheckingInterval: 50 }, (req, res) => {
    const action = this.handle(req, res).catch(() => { res.destroy(); });
    this.handlers.add(action); void action.finally(() => this.handlers.delete(action));
  });
  readonly gate: PolicyGate;
  private readonly upstreamSocket: string;
  private readonly routerKey: string;
  constructor(gate: PolicyGate, upstreamSocket: string, routerKey: string) {
    this.gate = gate; this.upstreamSocket = upstreamSocket; this.routerKey = routerKey;
    if (routerKey.length < 16) throw new Error('invalid_router_key');
    this.server.maxConnections = 16; this.server.headersTimeout = 1000; this.server.requestTimeout = 1000;
    this.server.keepAliveTimeout = 100; this.server.maxRequestsPerSocket = 1;
    this.server.on('connect', (_req, socket) => socket.end('HTTP/1.1 404 Not Found\r\nConnection: close\r\nContent-Length: 0\r\n\r\n'));
    this.server.on('upgrade', (_req, socket) => socket.end('HTTP/1.1 404 Not Found\r\nConnection: close\r\nContent-Length: 0\r\n\r\n'));
    this.server.on('clientError', (_error, socket) => socket.end('HTTP/1.1 400 Bad Request\r\nConnection: close\r\nContent-Length: 0\r\n\r\n'));
  }
  async listen(socket: string) { this.server.listen(socket); await once(this.server, 'listening'); await chmod(socket, 0o600); }
  issue(binding: Binding): string {
    validateBinding(binding);
    if (this.closed || this.scopes.size >= 64 || this.attempts.has(binding.attemptId) || Object.hasOwn(this.gate.snapshot().counts, binding.attemptId)) throw new Denial('policy_denied');
    const token = randomBytes(32).toString('hex');
    this.scopes.set(hash(token), { binding: structuredClone(binding), born: Date.now(), revoked: false });
    this.attempts.add(binding.attemptId); return token;
  }
  revoke(attemptId: string) {
    for (const scope of this.scopes.values()) if (scope.binding.attemptId === attemptId) scope.revoked = true;
    for (const [abort, scope] of this.pending) if (scope.binding.attemptId === attemptId) abort.abort(new Denial('cancelled', 409));
    for (const item of this.active.values()) if (item.scope.binding.attemptId === attemptId) item.abort.abort(new Denial('cancelled', 409));
  }
  replaceFence(attemptId: string) { this.revoke(attemptId); }
  private current(scope: Scope) {
    const now = Date.now(); const policy = this.gate.snapshot().policy;
    return !this.closed && !scope.revoked && scope.binding.expiresAt > now && scope.binding.leaseExpiresAt > now && !!policy && now < scope.born + policy.limits.attemptMs;
  }
  private authenticate(req: IncomingMessage): Scope {
    const allowed = ['host', 'authorization', 'content-type', 'content-length', 'connection', 'transfer-encoding', 'accept', 'user-agent'];
    if (this.gate.snapshot().policy?.profile === OPENCODE_PROFILE) {
      allowed.push('x-session-affinity', 'x-session-id', 'accept-encoding');
      const session = req.headers['x-session-id'];
      if (session !== undefined && (typeof session !== 'string' || !/^ses_[a-zA-Z0-9]{1,64}$/.test(session))) throw new Denial('unsupported_request', 400);
      if (req.headers['x-session-affinity'] !== undefined && req.headers['x-session-affinity'] !== session) throw new Denial('unsupported_request', 400);
      if (req.headers['accept-encoding'] !== undefined && req.headers['accept-encoding'] !== 'gzip, deflate, br, zstd') throw new Denial('unsupported_request', 400);
    }
    const names = req.rawHeaders.filter((_, i) => i % 2 === 0).map(s => s.toLowerCase());
    if (names.some(name => !allowed.includes(name)) || new Set(names).size !== names.length || req.headers.host !== 'localhost') throw new Denial('unsupported_request', 400);
    const header = req.headers.authorization;
    if (!header || !/^Bearer [a-f0-9]{64}$/.test(header)) throw new Denial('unauthorized', 401);
    const scope = this.scopes.get(hash(header.slice(7)));
    if (!scope || !this.current(scope)) throw new Denial('unauthorized', 401);
    return scope;
  }
  private reject(req: IncomingMessage, res: ServerResponse, error: Denial, requestId?: string) {
    if (res.destroyed || res.writableEnded) return;
    if (res.headersSent) {
      res.end(`event: error\ndata: ${JSON.stringify({ error: { code: error.reason }, request_id: requestId })}\n\n`);
    } else {
      res.writeHead(error.status, { 'content-type': 'application/json', connection: 'close', 'cache-control': 'no-store' });
      res.end(JSON.stringify({ error: { code: error.reason }, ...(requestId ? { request_id: requestId } : {}) }));
    }
    req.resume();
  }
  private async body(req: IncomingMessage, limit: number, signal: AbortSignal) {
    if (req.headers['content-type'] !== 'application/json' || req.headers['content-encoding']) throw new Denial('unsupported_request', 415);
    const length = req.headers['content-length'];
    if (length && (!/^\d+$/.test(length) || Number(length) > limit)) throw new Denial('request_limit', 413);
    return new Promise<string>((resolve, reject) => {
      const chunks: Buffer[] = []; let size = 0;
      const abort = () => { clean(); reject(signal.reason); };
      const error = () => { clean(); reject(new Denial('cancelled', 409)); };
      const data = (chunk: Buffer) => {
        size += chunk.length;
        if (size > limit) { clean(); reject(new Denial('request_limit', 413)); return; }
        chunks.push(chunk);
      };
      const end = () => {
        clean();
        try { resolve(new TextDecoder('utf-8', { fatal: true }).decode(Buffer.concat(chunks))); }
        catch { reject(new Denial('unsupported_request', 400)); }
      };
      const clean = () => { req.off('data', data); req.off('end', end); req.off('error', error); req.off('aborted', error); signal.removeEventListener('abort', abort); };
      req.on('data', data); req.once('end', end); req.once('error', error); req.once('aborted', error);
      signal.addEventListener('abort', abort, { once: true }); if (signal.aborted) abort();
    });
  }
  private async handle(req: IncomingMessage, res: ServerResponse) {
    let scope: Scope | undefined; let requestId: string | undefined; let forwarded = false;
    let semantic = false; let outcome: Outcome = 'failed_before_output'; let reason: Reason | null = null;
    const abort = new AbortController(); const timers: NodeJS.Timeout[] = [];
    const rejectAborted = () => this.reject(req, res, safeError(abort.signal.reason), requestId);
    abort.signal.addEventListener('abort', () => { if (requestId) { try { this.gate.cancel(requestId); } catch { /* Preserve reservation if private control is unavailable. */ } } }, { once: true });
    abort.signal.addEventListener('abort', rejectAborted, { once: true });
    const cancel = () => { if (!res.writableEnded) abort.abort(new Denial('cancelled', 409)); };
    res.once('close', cancel);
    try {
      if (this.closed) throw new Denial('boundary_closed', 503);
      if (req.method !== 'POST' || req.url !== CHAT_PATH) throw new Denial('unsupported_request', 404);
      scope = this.authenticate(req);
      this.pending.set(abort, scope);
      const policy = this.gate.snapshot().policy;
      if (!policy) throw new Denial('boundary_closed', 503);
      const expiry = Math.min(scope.binding.expiresAt, scope.binding.leaseExpiresAt, scope.born + policy.limits.attemptMs);
      timers.push(setTimeout(() => abort.abort(new Denial('deadline', 408)), Math.min(policy.limits.totalMs, Math.max(1, expiry - Date.now()))));
      const raw = await this.body(req, policy.limits.requestBytes, abort.signal);
      let value: any;
      try { value = parseJSON(raw); } catch { throw new Denial('unsupported_request', 400); }
      const body = validateRequest(value, policy);
      const admission = await this.gate.admit(scope.binding, () => this.current(scope!) && !abort.signal.aborted, abort.signal, hashDocument(body));
      requestId = admission.reservation.requestId;
      this.active.set(requestId, { scope, abort });
      // Recheck after durable admission. A stop during I/O must never start a request.
      if (!this.current(scope) || abort.signal.aborted) throw new Denial('cancelled', 409);
      const decoder = new ChatStream(requestId, policy.routerModel, !!value.tools && value.tool_choice !== 'none', policy.profile === 'router-native-chat-translation-synthetic-v1' ? 'receipt-gated-eof' : policy.schema === 2 ? 'router-done' : 'done', policy.profile);
      const firstOutput = setTimeout(() => abort.abort(new Denial('deadline', 408)), policy.limits.firstOutputMs); timers.push(firstOutput);
      let idle: NodeJS.Timeout | undefined;
      if (policy.schema === 2) await this.gate.markSend(requestId);
      if (!this.current(scope) || abort.signal.aborted) throw new Denial('cancelled',409);
      const upstream = request({ socketPath: this.upstreamSocket, path: CHAT_PATH, method: 'POST', agent: false, signal: abort.signal, headers: {
        host: policy.schema === 2 ? '127.0.0.1' : 'localhost', authorization: `Bearer ${this.routerKey}`, 'content-type': 'application/json', 'content-length': Buffer.byteLength(body),
        'x-gaffer-request-id': requestId,
        ...(policy.schema === 2 ? { 'x-gaffer-generation': String(admission.reservation.router!.policy.authority.generation), 'x-gaffer-revision': admission.reservation.router!.policy.revision } : { 'x-gaffer-task-id': scope.binding.taskId, 'x-gaffer-attempt-id': scope.binding.attemptId, 'x-gaffer-policy-revision': scope.binding.revision }),
      } });
      const responsePromise = once(upstream, 'response', { signal: abort.signal }) as Promise<[IncomingMessage]>;
      upstream.on('error', () => {}); // Errors are consumed by the response promise/iterator and sanitized below.
      forwarded = true; upstream.end(body);
      const [response] = await responsePromise;
      if (response.statusCode !== 200 || response.headers['content-type']?.split(';')[0] !== 'text/event-stream' || response.headers['content-encoding']) {
        response.destroy(); upstream.destroy(); throw new Denial('upstream_failure', 502);
      }
      res.writeHead(200, { 'content-type': 'text/event-stream', 'cache-control': 'no-store', connection: 'close', 'x-gaffer-request-id': requestId });
      let rawBytes = 0; let responseBytes = 0;
      try {
        for await (const chunk of response) {
          if (!this.current(scope) || abort.signal.aborted) throw new Denial('cancelled', 409);
          rawBytes += chunk.length;
          if (rawBytes > policy.limits.responseBytes) throw new Denial('response_limit', 502);
          const parsed = decoder.push(chunk);
          if (parsed.semantic) {
            semantic = true; clearTimeout(firstOutput); if (idle) clearTimeout(idle);
            idle = setTimeout(() => abort.abort(new Denial('deadline', 408)), policy.limits.idleMs); timers.push(idle);
          }
          for (const output of parsed.output) {
            responseBytes += Buffer.byteLength(output);
            if (responseBytes > policy.limits.responseBytes) throw new Denial('response_limit', 502);
            if (!res.write(output)) await once(res, 'drain', { signal: abort.signal });
          }
          if (parsed.error) throw parsed.error;
        }
        const completion = decoder.end();
        if (responseBytes + completion.reduce((n,output) => n + Buffer.byteLength(output),0) > policy.limits.responseBytes) throw new Denial('response_limit',502);
        if (policy.schema === 2) {
          clearTimeout(firstOutput); if (idle) clearTimeout(idle);
          await this.gate.finalize(requestId,hashDocument(completion),() => this.current(scope!) && !abort.signal.aborted,abort.signal);
        }
        for (const output of completion) {
          if (!this.current(scope) || abort.signal.aborted) throw new Denial('cancelled',409);
          if (policy.schema === 2) this.gate.assertRelease(requestId);
          responseBytes += Buffer.byteLength(output);
          if (responseBytes > policy.limits.responseBytes) throw new Denial('response_limit', 502);
          if (!res.write(output)) await once(res, 'drain', { signal: abort.signal });
        }
        outcome = 'completed'; res.end();
      } finally { response.destroy(); upstream.destroy(); }
    } catch (error) {
      const failure = safeError(abort.signal.aborted ? abort.signal.reason : error); reason = failure.reason;
      outcome = forwarded && ['deadline', 'cancelled'].includes(reason) ? 'cancelled_unknown' : semantic ? 'partial_failure' : 'failed_before_output';
      abort.abort(failure); this.reject(req, res, failure, requestId);
    } finally {
      for (const timer of timers) clearTimeout(timer); res.off('close', cancel);
      abort.signal.removeEventListener('abort', rejectAborted);
      this.pending.delete(abort);
      if (requestId && scope) {
        let quiescence: Audit['quiescence'] = 'unknown';
        try { if (await this.gate.finish(requestId, outcome, !forwarded)) quiescence = 'verified'; }
        catch { reason = 'boundary_closed'; }
        this.active.delete(requestId);
        const decision = this.gate.snapshot().decisions?.find(d => d.requestId === requestId);
        this.audit.push({ taskId: scope.binding.taskId, attemptId: scope.binding.attemptId, requestId, role: scope.binding.role,
          routeId: scope.binding.routeId, policyRevision: scope.binding.revision, outcome, reason, source: 'boundary', providerAttribution: 'unavailable', quiescence, ...(decision?.evidence.receiptDigest ? {receipt:{digest:decision.evidence.receiptDigest,authorityDigest:decision.router.policy.authority.graphDigest,verdict:decision.verdict}} : {}) });
        if (this.audit.length > 128) this.audit.shift();
      }
    }
  }
  close(): Promise<void> {
    if (this.closing) return this.closing;
    this.closed = true;
    for (const abort of this.pending.keys()) abort.abort(new Denial('cancelled', 409));
    for (const { abort } of this.active.values()) abort.abort(new Denial('cancelled', 409));
    // Cancel handlers first so bounded receipt/persistence cleanup retains durable uncertainty.
    const closeGate = this.gate.snapshot().schema === 1 ? this.gate.close() : undefined;
    void closeGate?.catch(() => {});
    this.server.closeAllConnections();
    this.closing = (async () => {
      if (this.server.listening) await new Promise<void>(resolve => this.server.close(() => resolve()));
      await Promise.allSettled([...this.handlers]); await (closeGate ?? this.gate.close());
    })();
    return this.closing;
  }
}
