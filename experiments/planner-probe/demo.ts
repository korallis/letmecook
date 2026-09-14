import { setup, proposal, finishText } from './fixture.ts';
import { toolStream } from '../inference-boundary/fixture.ts';
import { livePreflight } from './capabilities.ts';
import { setTimeout as pause } from 'node:timers/promises';

export async function demonstrate() {
  const fixture = await setup();
  try {
    fixture.replies.push(toolStream(), finishText(JSON.stringify(proposal(fixture.planner.inputRevision))));
    const result = await fixture.planner.run();
    // HTTP EOF precedes the boundary's separate authority reconciliation. Give
    // the honest fixture time to record quiescence before shutting it down.
    const deadline = Date.now() + 1000;
    while (fixture.boundary.audit.length < fixture.calls.length && Date.now() < deadline) await pause(5);
    if (fixture.boundary.audit.length !== 2 || fixture.boundary.audit.some(audit => audit.outcome !== 'completed' || audit.quiescence !== 'verified')) throw new Error('synthetic_quiescence_unverified');
    await fixture.close();
    if (result.outcome !== 'plan_proposed') throw new Error('synthetic_demo_failed');
    return { evidence: 'synthetic_http_integration', result, requests: fixture.calls.length, boundaryAudit: fixture.boundary.audit, live: livePreflight() };
  } catch (error) { await fixture.close().catch(() => {}); throw error; }
}
if (import.meta.main) {
  try { console.log(JSON.stringify(await demonstrate(), null, 2)); }
  catch { console.error('synthetic_demo_failed'); process.exitCode = 1; }
}
