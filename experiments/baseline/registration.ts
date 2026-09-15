// A frozen declaration, never a grant, preflight implementation or live-start API.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { exact, list, text, id, sha, integer, oneOf, time, reference, path, unique, digest, type Row } from './validation.ts';

const intake = JSON.parse(readFileSync(new URL('../../tests/fixtures/tasks/operator-intake.json', import.meta.url), 'utf8'));
export const BOUNDS = Object.freeze({ phase: 'baseline', maxInferenceAttempts: 32, elapsedMs: 900000, concurrentCases: 1,
  maxRefreshOperations: 0, retriesCount: true, unknownOriginalBlocksReplacement: true, model: 'gpt-6-astra', effort: 'xhigh',
  route: '9Router', providers: 'existing-codex-subscriptions', paidFallback: false, limitsProfile: 'native-subscription-local-v1',
  strictDefaultRetained: true, providerOutputCap: 'unavailable', providerMonetaryCap: 'unavailable' });

export function validateRegistration(value: unknown): Row {
  exact(value, 'schema kind origin caseId version frozenAt previousDigest authority approvedCaseDigest eligibility fixture paths criteria checks toolchains execution bounds operatorEffort');
  assert.equal(value.schema, 1); assert.equal(value.kind, 'frozen-baseline-declaration');
  oneOf(value.origin, ['operator', 'synthetic']); id(value.caseId); integer(value.version, 1, 1000); time(value.frozenAt);
  if (value.previousDigest !== null) sha(value.previousDigest);
  assert.equal(value.version === 1, value.previousDigest === null, 'registration_version_chain');
  reference(value.authority); sha(value.approvedCaseDigest);
  exact(value.eligibility, 'status source'); oneOf(value.eligibility.status, ['feasible', 'infeasible', 'needs-clarification']); reference(value.eligibility.source);
  exact(value.fixture, 'manifest startingCommit treeDigest context brief progressSeed');
  for (const k of ['manifest', 'context', 'brief', 'progressSeed']) reference(value.fixture[k]);
  sha(value.fixture.startingCommit, 40); sha(value.fixture.treeDigest);
  exact(value.paths, 'read write');
  for (const k of ['read', 'write']) { list(value.paths[k], k === 'read' ? 1 : 0, 256); value.paths[k].forEach(path); assert.equal(new Set(value.paths[k]).size, value.paths[k].length); }
  list(value.criteria, 1, 64); unique(value.criteria);
  value.criteria.forEach((c: unknown) => { exact(c, 'id textDigest'); sha(c.textDigest); });
  list(value.toolchains, 1, 32); unique(value.toolchains);
  value.toolchains.forEach((t: unknown) => { exact(t, 'id version binaryDigest environmentDigest'); text(t.version, 256); sha(t.binaryDigest); sha(t.environmentDigest); });
  list(value.checks, 1, 128); unique(value.checks);
  for (const c of value.checks) {
    exact(c, 'id criterionIds argv cwd toolchainId inputs required revisions');
    list(c.criterionIds, 1, 64); assert.equal(new Set(c.criterionIds).size, c.criterionIds.length);
    c.criterionIds.forEach((x: unknown) => assert(value.criteria.some((v: Row) => v.id === x), 'unknown_criterion'));
    list(c.argv, 1, 64); c.argv.forEach((x: unknown) => text(x, 4096)); if (c.cwd !== '.') path(c.cwd);
    assert(value.toolchains.some((t: Row) => t.id === c.toolchainId), 'unknown_toolchain'); reference(c.inputs);
    assert.equal(typeof c.required, 'boolean'); oneOf(c.revisions, ['base', 'candidate', 'both']);
  }
  assert(value.criteria.every((c: Row) => value.checks.some((check: Row) => check.criterionIds.includes(c.id))), 'unmapped_criterion');
  exact(value.execution, 'harness router namedRoute nativeProfileDigest packetDigest isolation preflight stopPlan');
  for (const k of ['harness', 'router', 'isolation', 'preflight', 'stopPlan']) reference(value.execution[k]);
  text(value.execution.namedRoute, 256); sha(value.execution.nativeProfileDigest); sha(value.execution.packetDigest);
  assert.deepEqual(value.bounds, BOUNDS, 'approved_baseline_bounds');
  exact(value.operatorEffort, 'treatment maximumActiveSeconds source'); reference(value.operatorEffort.source);
  oneOf(value.operatorEffort.treatment, ['cap-including-preparation', 'record-only']);
  if (value.operatorEffort.treatment === 'cap-including-preparation') integer(value.operatorEffort.maximumActiveSeconds, 1, 1800);
  else assert.equal(value.operatorEffort.maximumActiveSeconds, null, 'record_only_has_no_cap');
  if (value.origin === 'operator') {
    const approved = intake.cases.find((c: Row) => c.id === value.caseId); assert(approved, 'unknown_approved_case');
    assert.deepEqual(value.authority, { ref: intake.authority.reference, sha256: intake.authority.sha256 }, 'authority_reference_changed');
    assert.deepEqual(value.fixture.manifest, { ref: intake.privateManifest.reference, sha256: intake.privateManifest.sha256 }, 'private_manifest_reference_changed');
    assert.equal(value.approvedCaseDigest, digest(approved), 'approved_case_changed');
    assert.deepEqual(value.criteria.map((c: Row) => c.textDigest), approved.acceptanceCriteria.map(digest), 'approved_criteria_changed');
  } else assert(/^synthetic-[a-z0-9_-]+$/.test(value.caseId), 'synthetic_identity_required');
  return structuredClone(value);
}

export function registrationIdentity(value: unknown) {
  const registration = validateRegistration(value);
  return { registrationDigest: digest(registration), declarationValidated: true, executionAuthorized: false, evidenceAuthenticated: false };
}
