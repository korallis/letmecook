import { request } from 'node:http';
import { setTimeout as pause } from 'node:timers/promises';
import { setup } from './fixture.ts';
import { TOOL, CHAT_PATH } from './types.ts';

const experiment = await setup();
try {
  const { token } = experiment.grant({ role: 'planner' });
  experiment.router.enqueue('tools');
  const body = JSON.stringify({ model: 'gaffer-coding', stream: true, max_completion_tokens: 32, messages: [{ role: 'user', content: 'Read fixture.txt' }], tools: [TOOL], tool_choice: 'required' });
  const result = await new Promise<string>((resolve, reject) => {
    const req = request({ socketPath: experiment.workerSocket, path: CHAT_PATH, method: 'POST', headers: { host: 'localhost', authorization: `Bearer ${token}`, 'content-type': 'application/json', 'content-length': Buffer.byteLength(body) } }, res => {
      let text = ''; res.setEncoding('utf8'); res.on('data', chunk => text += chunk); res.on('end', () => resolve(text)); res.on('error', reject);
    });
    req.on('error', reject); req.end(body);
  });
  if (!result.includes('[DONE]') || !result.includes('read_file')) throw new Error('demo_failed');
  // Let the completed fixture request record its quiescence before deliberate shutdown.
  const deadline = Date.now() + 1000;
  while (!experiment.boundary.audit.length && Date.now() < deadline) await pause(5);
  if (experiment.boundary.audit[0]?.outcome !== 'completed') throw new Error('demo_failed');
  await experiment.close();
  console.log(JSON.stringify({ synthetic: true, liveRouterCalled: false, completedToolCall: 'read_file', toolExecuted: false, attribution: experiment.boundary.audit }, null, 2));
} catch {
  await experiment.close().catch(() => {});
  console.error(JSON.stringify({ synthetic: true, result: 'demo_failed' })); process.exitCode = 1;
}
