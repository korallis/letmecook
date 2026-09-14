import assert from 'node:assert/strict';
import { containerArgs, assertContainer, PROFILE } from '../isolation/profile.ts';
import { pins } from './adapter.ts';
export const VARIANT = pins.isolationVariant as string;
export function harnessContainerArgs(name: string, runId: string, staging: string, volume: string, gateway = false) {
  const args = containerArgs(name, runId, staging, volume, gateway);
  // Versioned resource extension: all other #5 controls stay exact.
  if (!gateway) for (const flag of ['--memory', '--memory-swap']) args[args.indexOf(flag) + 1] = '512m';
  return args;
}
export function assertHarnessContainer(state: any, staging: string, volume: string, gateway = false) {
  assert.equal(state.HostConfig.Memory, (gateway ? 128 : 512) * 1024 * 1024);
  assert.equal(state.HostConfig.MemorySwap, state.HostConfig.Memory);
  const legacy = structuredClone(state);
  legacy.HostConfig.Memory = legacy.HostConfig.MemorySwap = 128 * 1024 * 1024;
  assertContainer(legacy, staging, volume, gateway);
  return { variant: VARIANT, predecessor: PROFILE, workerMemoryMiB: 512, gatewayMemoryMiB: 128 };
}

// Actual-router topology: accepted 768 MiB gateway plus the separately measured
// 512 MiB OpenCode worker. The full effective controls are checked and recorded.
import { commonArgs, inspectProfile } from '../router-boundary-bridge/isolation.ts';
export const ROUTER_VARIANT = 'm0-opencode512-router768-uds-v1';
export function routerHarnessArgs(name: string, run: string, gateway: boolean) {
  const args = commonArgs(name, run, gateway);
  if (!gateway) for (const flag of ['--memory', '--memory-swap']) args[args.indexOf(flag) + 1] = '512m';
  return args;
}
export function inspectRouterHarness(state: any, image: string, run: string, gateway: boolean, volume: string, binds: Record<string, string>) {
  assert.equal(state.HostConfig.Memory, (gateway ? 768 : 512) * 1048576);
  assert.equal(state.HostConfig.MemorySwap, state.HostConfig.Memory);
  const predecessor = structuredClone(state);
  if (!gateway) predecessor.HostConfig.Memory = predecessor.HostConfig.MemorySwap = 128 * 1048576;
  const measured = inspectProfile(predecessor, image, run, gateway, volume, binds);
  return { ...measured, variant: ROUTER_VARIANT, memory: state.HostConfig.Memory, memorySwap: state.HostConfig.MemorySwap };
}
