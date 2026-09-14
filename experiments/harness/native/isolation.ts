import assert from 'node:assert/strict';
import { commonArgs, inspectProfile } from '../../router-boundary-bridge/isolation.ts';
import { ISOLATION } from './client.ts';
export function workerArgs(name: string, run: string) {
  const args = commonArgs(name, run, false);
  for (const flag of ['--memory', '--memory-swap']) args[args.indexOf(flag) + 1] = '768m';
  return args;
}
export function inspectWorker(state: any, image: string, run: string, volume: string, staging: string) {
  assert.equal(state.HostConfig.Memory, 768 * 1048576); assert.equal(state.HostConfig.MemorySwap, state.HostConfig.Memory);
  const inherited = structuredClone(state); inherited.HostConfig.Memory = inherited.HostConfig.MemorySwap = 128 * 1048576;
  const measured = inspectProfile(inherited, image, run, false, volume, { '/fixture': staging });
  return { ...measured, variant: ISOLATION, memory: state.HostConfig.Memory, memorySwap: state.HostConfig.MemorySwap };
}
