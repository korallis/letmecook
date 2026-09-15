// Shared seams for the isolated public case01 experiment. These types are not grants.
export type EvidenceRef = { ref: string; sha256: string };
export type EvidenceStore = { directory: string };
export type Revision = 'base' | 'candidate';
export type FileMode = 0o644 | 0o755;
export type ManifestFile = { path: string; mode: FileMode; size: number; sha256: string };
export type SnapshotManifest = { schema: 1; files: ManifestFile[]; treeDigest: string };
export type SnapshotFile = ManifestFile & { contentBase64: string };
export type SnapshotBytes = { schema: 1; kind: 'baseline-snapshot-bytes'; files: SnapshotFile[] };
export type SnapshotRef = { manifest: EvidenceRef; bytes: EvidenceRef; treeDigest: string };
export type CaseRules = {
  schema: 1;
  caseId: string;
  registrationDigest: string;
  readPaths: string[];
  writePaths: string[];
};

// A complete small synthetic repository; these limits do not attest a real repository fits.
export const CASE01_LIMITS = Object.freeze({
  files: 64,
  sourceBytes: 65536,
  archiveBytes: 1048576,
  artifactBytes: 1048576,
  contextBytes: 16384,
});

export type CheckJob = {
  schema: 1;
  registration: EvidenceRef;
  registrationDigest: string;
  checkId: string;
  revision: Revision;
  snapshot: SnapshotRef;
  executable: EvidenceRef;
  argv: string[];
  cwd: string;
  inputs: EvidenceRef;
  imageDigest: string;
  environmentDigest: string;
  deadline: number;
  outputBytes: number;
};

export type CheckEvidence = {
  observation: EvidenceRef;
  status: 'passed' | 'failed' | 'not-run' | 'unknown';
  exitCode: number | null;
  snapshotDigest: string;
  checkId: string;
  cleanup: boolean;
};

export type CandidateBundle = {
  schema: 1;
  kind: 'baseline-repository-bundle';
  caseId: string;
  registrationDigest: string;
  base: SnapshotRef;
  candidate: SnapshotRef;
  changedPaths: string[];
};

// Module ownership:
// artifacts/: retainEvidence/readEvidence; captureSnapshot(archive, baseManifest,
// rules, store, revision); retainCandidate(base, candidate, rules, store).
// checks/: runCheck(job, store, signal), using the same retained evidence references.
// Root: registration/packet admission, native worker/transcript, absolute deadlines,
// scope/receipt authority, observation adapter and synthetic case coordinator.
