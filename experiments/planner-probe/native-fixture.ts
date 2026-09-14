// In-memory consumer preparation only: this fake cannot issue a grant, validate
// a router policy, persist a decision, open a socket or call any model.
import { PlannerSession, type Budget } from './planner.ts';
import { snapshot, ProbeError } from './reader.ts';
import { BRIEF, FIXTURE_HASH, FIXTURE_ROOT } from './public-fixture.ts';
import { NATIVE_PLANNER_BUDGET, NATIVE_PLANNER_PROTOCOL, NATIVE_PLANNER_TOOL, NativePlannerTransport, type NativePlannerBoundaryPort, type NativePlannerPolicy, type ReleasedNativeCompletion } from './native-transport.ts';
export type NativeResponse = ReleasedNativeCompletion['response'];
export type NativeReply = NativeResponse | Error | ((body: any, signal: AbortSignal) => Promise<NativeResponse> | NativeResponse);
export const nativeResponse = (output: unknown[]): NativeResponse => ({ model: 'gpt-6-astra', status: 'completed', error: null, incomplete_details: null, output });
export const nativeText = (text: string): NativeResponse => nativeResponse([{ type: 'message', role: 'assistant', status: 'completed', content: [{ type: 'output_text', text, annotations: [] }] }]);
export const nativeRead = (argumentsText = '{"path":"fixture.txt"}'): NativeResponse => nativeResponse([
  { id: 'reasoning_1', type: 'reasoning', encrypted_content: 'opaque_ENCRYPTED_state_A', summary: [{ type: 'summary_text', text: 'Need the authorized fixture.' }] },
  { id: 'function_1', type: 'function_call', status: 'completed', call_id: 'native_original_call_1', name: 'read_file', arguments: argumentsText },
]);
export function consumerFixturePolicy(): NativePlannerPolicy & Record<string, unknown> {
  return { schema: 3, profile: 'router-native-responses-local-v1', routeId: 'fixture_planner_native', routerModel: 'gaffer-planner-native', evidence: 'synthetic', liveAdmission: false,
    // Deliberately incomplete as an authority policy. No shared validator can
    // mistake this consumer fixture for a deployed policy or grant.
    fixtureOnly: true, privateIdentity: 'SYNTHETIC_PRIVATE_DEPLOYMENT',
    limits: { requestBytes: 32768, responseBytes: 32768, outputTokens: null },
    native: { protocol: NATIVE_PLANNER_PROTOCOL, tools: [structuredClone(NATIVE_PLANNER_TOOL)], toolPaths: ['fixture.txt'],
      authorization: { model: 'gpt-6-astra', effort: 'xhigh', providerOutput: { requirement: 'not_required', capability: 'unavailable' }, providerMonetaryCap: { requirement: 'not_required', capability: 'unavailable' } },
      fixtureSourceIdentity: 'SYNTHETIC_PRIVATE_SOURCE', fixtureSettingsIdentity: 'SYNTHETIC_PRIVATE_SETTINGS' },
  };
}
export async function setupNative(replies: NativeReply[] = [], budget: Omit<Budget, 'outputTokens'> & { outputTokens: null } = NATIVE_PLANNER_BUDGET) {
  const policy = consumerFixturePolicy();
  const binding: NativePlannerBoundaryPort['binding'] = { role: 'planner', protocol: NATIVE_PLANNER_PROTOCOL, sessionId: 'ses_planner_fixture1' };
  const requests: { body: any; responseBytes: number }[] = [], releases: string[] = [];
  const port: NativePlannerBoundaryPort = { policy, binding, complete: async (body, signal, responseBytes) => {
    requests.push({ body: structuredClone(body), responseBytes });
    const reply = replies.shift();
    if (!reply) throw new ProbeError('route_unavailable');
    if (reply instanceof Error) throw reply;
    const response = typeof reply === 'function' ? await reply(body, signal) : reply;
    const requestId = requests.length.toString(16).padStart(32, '0');
    releases.push(requestId);
    return { requestId, acceptedAt: Date.now(), response: structuredClone(response) };
  } };
  const transport = new NativePlannerTransport(port);
  const file = await snapshot(FIXTURE_ROOT, 'fixture.txt', FIXTURE_HASH, 4096, AbortSignal.timeout(1000));
  const planner = new PlannerSession(transport, file, BRIEF, budget);
  return { planner, transport, file, policy, binding, requests, releases, port, replies };
}
