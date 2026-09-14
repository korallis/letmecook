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
