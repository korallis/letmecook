import { createStatusAdapter, INSPECTED_COMMIT } from './status.ts';

// Synthetic, in-memory example; no network request and no environment credentials.
const adapter = createStatusAdapter({
  routerId: 'synthetic-router', origin: 'https://router.invalid', managementCredential: 'synthetic-only', inspectedCommit: INSPECTED_COMMIT,
  connections: [{ ref: 'subscription-a', providerRef: 'provider-a', upstreamId: 'fixture-a', upstreamProvider: 'fixture-provider' }],
  routes: [{ routeId: 'worker', policyRevision: 'fixture-v1', connectionRefs: ['subscription-a'] }],
}, {
  fetch: async () => Response.json({ connections: [{ id: 'fixture-a', provider: 'fixture-provider', isActive: true,
    routingStatus: 'eligible', authState: 'ok', healthStatus: 'healthy', quotaState: 'ok', lastCheckedAt: new Date().toISOString(),
    providerSpecificData: { token: 'synthetic-token-excluded-from-projection' } }] }),
});
process.stdout.write(JSON.stringify(await adapter.snapshot('worker'), null, 2) + '\n');
