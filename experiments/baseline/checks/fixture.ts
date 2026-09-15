// Public, invented inputs. These programs validate a tiny declared fixture;
// neither is an upstream workflow execution or a real advisory audit.
import { createHash } from 'node:crypto';
import { fixture as registrationFixture } from '../fixture.ts';
import { digest } from '../validation.ts';
import { retainEvidence, makeManifest } from '../artifacts/index.ts';
import type { EvidenceStore, CheckJob, SnapshotFile, SnapshotRef } from '../execution-contract.ts';
import { IMAGE, NODE, BINARY_DIGEST, ENVIRONMENT_DIGEST } from './index.ts';

export const PINS_CHECK = `import fs from 'node:fs';
const pins=JSON.parse(fs.readFileSync('/inputs/pins.json','utf8')); const errors=[];
for(const [path,expected] of Object.entries(pins.files)) { const text=fs.readFileSync('/repo/'+path,'utf8'); if(!text.includes('uses: '+expected+'\\n')) errors.push(path); }
console.log(JSON.stringify({kind:'synthetic-pins-check',checked:Object.keys(pins.files),errors}));process.exitCode=errors.length?1:0;`;
export const AUDIT_CHECK = `import fs from 'node:fs';
const advisory=JSON.parse(fs.readFileSync('/inputs/advisory.json','utf8'));const lock=JSON.parse(fs.readFileSync('/repo/lock.json','utf8'));
const inherited=lock.dependency===advisory.dependency&&lock.version===advisory.version;
console.log(JSON.stringify({kind:'synthetic-frozen-audit',inherited,advisory:advisory.id,realAudit:false}));process.exitCode=inherited?1:0;`;
export function snapshotFile(path: string, content: string, mode: 0o644 | 0o755 = 0o644): SnapshotFile {
  const bytes = Buffer.from(content); return { path, mode, size: bytes.length, sha256: createHash('sha256').update(bytes).digest('hex'), contentBase64: bytes.toString('base64') };
}
export async function snapshot(store: EvidenceStore, files: SnapshotFile[]): Promise<SnapshotRef> {
  files = [...files].sort((a, b) => a.path < b.path ? -1 : a.path > b.path ? 1 : 0);
  const manifest = makeManifest(files);
  return { manifest: await retainEvidence(store, 'snapshot-manifest', manifest), bytes: await retainEvidence(store, 'snapshot-bytes', { schema: 1, kind: 'baseline-snapshot-bytes', files }), treeDigest: manifest.treeDigest };
}
export async function checkFixture(store: EvidenceStore, code = PINS_CHECK, options: { checkId?: string; candidate?: boolean; deadlineMs?: number; outputBytes?: number } = {}) {
  const checkId = options.checkId ?? 'pins', executable = snapshotFile('checks/' + checkId + '.mjs', code);
  const common = [executable, snapshotFile('lock.json', '{"dependency":"invented-package","version":"0.0.1"}\n'), snapshotFile('toolchain.txt', 'synthetic frozen toolchain\n', 0o755)];
  const paths = ['ci/first.yml', 'ci/second.yml'], pins = ['actions/checkout@' + '1'.repeat(40), 'actions/setup-node@' + '2'.repeat(40)];
  const base = await snapshot(store, [...common, ...paths.map((p, i) => snapshotFile(p, `steps:\n  - uses: ${i ? 'actions/setup-node@v4' : 'actions/checkout@v4'}\n`))]);
  const candidate = await snapshot(store, [...common, ...paths.map((p, i) => snapshotFile(p, `steps:\n  - uses: ${pins[i]}\n`))]);
  const inputFiles = [snapshotFile('pins.json', JSON.stringify({ files: Object.fromEntries(paths.map((p, i) => [p, pins[i]])) })),
    snapshotFile('advisory.json', '{"id":"invented-inherited-finding","dependency":"invented-package","version":"0.0.1"}')];
  const inputs = await retainEvidence(store, 'check-inputs', { schema: 1, kind: 'baseline-check-inputs', files: inputFiles, executable: { path: executable.path, sha256: executable.sha256 }, imageDigest: IMAGE });
  const registration = registrationFixture().registrations[0]; registration.caseId = 'synthetic-isolated-checks';
  registration.fixture.treeDigest = base.treeDigest; registration.fixture.manifest = base.manifest;
  registration.paths.read = [...common.map(f => f.path), ...paths]; registration.paths.write = paths;
  registration.toolchains = [{ id: 'node', version: '24.21.0', binaryDigest: BINARY_DIGEST, environmentDigest: ENVIRONMENT_DIGEST }];
  registration.checks = [{ id: checkId, criterionIds: registration.criteria.map((c: any) => c.id), argv: [NODE, '/repo/' + executable.path], cwd: '.', toolchainId: 'node', inputs, required: true, revisions: 'both' }];
  const job: CheckJob = { schema: 1, registration: await retainEvidence(store, 'registration', registration), registrationDigest: digest(registration), checkId, revision: options.candidate ? 'candidate' : 'base',
    snapshot: options.candidate ? candidate : base, executable: await retainEvidence(store, 'check-executable', { schema: 1, kind: 'baseline-check-executable', file: executable }),
    argv: registration.checks[0].argv, cwd: '.', inputs, imageDigest: IMAGE, environmentDigest: ENVIRONMENT_DIGEST, deadline: Date.now() + (options.deadlineMs ?? 10000), outputBytes: options.outputBytes ?? 16384 };
  return { job, base, candidate, registration };
}
