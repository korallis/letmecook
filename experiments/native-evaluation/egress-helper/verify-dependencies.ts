import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { execFileSync, spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { basename, join } from 'node:path';

const root = import.meta.dirname;
const manifest = JSON.parse(readFileSync(join(root, 'manifest.json'), 'utf8'));
const hash = (data: Uint8Array | string) => createHash('sha256').update(data).digest('hex');
const file = (name: string) => readFileSync(join(root, name));
const run = (program: string, args: string[], maxBuffer = 128 * 1024 * 1024) =>
  execFileSync(program, args, { encoding: 'utf8', maxBuffer });
const paragraphs = (text: string) => text.split('\n\n').map(paragraph =>
  Object.fromEntries(paragraph.split('\n').filter(line => /^[^\s:]+: /.test(line))
    .map(line => [line.slice(0, line.indexOf(':')), line.slice(line.indexOf(':') + 2)])));

assert.equal(process.arch, 'arm64');
assert.equal(process.platform, 'linux');
assert.equal(manifest.targetArchitecture, 'arm64');
assert.equal(manifest.base.reference, 'node@sha256:6dac556d980b7f0e5498d08f08cee0ca67798b4ad6c23964a9214920e67758d0');
assert.deepEqual(manifest.packages.map((p: any) => p.name).sort(),
  ['libmnl0', 'libnftables1', 'libnftnl11', 'libxtables12', 'nftables']);

const installed = run('/usr/bin/dpkg-query', ['-W',
  '-f=${binary:Package}\t${Version}\t${Architecture}\t${db:Status-Abbrev}\n']);
assert.equal(hash(installed), manifest.base.installedPackagesSha256, 'base package set drift');
assert.equal(hash(file('metadata/base-installed.tsv')), manifest.base.installedPackagesSha256);
const versions = new Map<string, string>();
for (const row of installed.trimEnd().split('\n')) {
  const [name, version, architecture, status] = row.split('\t');
  assert.equal(status.trim(), 'ii');
  assert(['arm64', 'all'].includes(architecture));
  versions.set(name.split(':')[0], version);
}
assert.equal(versions.get('debian-archive-keyring'), manifest.base.keyringPackage.version);

const release = file('metadata/bookworm.InRelease');
assert.equal(hash(release), manifest.repository.releaseSha256);
const verified = spawnSync('/usr/bin/gpgv', ['--keyring', manifest.signatureVerification.keyring,
  '--status-fd', '1', join(root, 'metadata/bookworm.InRelease')], { encoding: 'utf8' });
assert.equal(verified.status, 0, verified.stderr);
for (const fingerprint of manifest.signatureVerification.validSigningFingerprints) {
  assert(verified.stdout.includes('[GNUPG:] VALIDSIG ' + fingerprint + ' '), 'missing signed fingerprint');
}
const releaseText = release.toString('utf8');
assert(releaseText.includes('\nCodename: bookworm\n'));
const checksums = releaseText.split('\nSHA256:\n')[1].split('\n-----BEGIN PGP SIGNATURE-----')[0];
const indexRecord = checksums.split('\n').map(line => line.trim().split(/\s+/))
  .find(parts => parts[2] === manifest.repository.indexPath);
assert(indexRecord);
assert.equal(indexRecord[0], manifest.repository.indexSha256);
assert.equal(Number(indexRecord[1]), manifest.repository.indexBytes);
const index = file('metadata/Packages.xz');
assert.equal(hash(index), indexRecord[0]);
assert.equal(index.byteLength, Number(indexRecord[1]));
const indexFields = paragraphs(run('/usr/bin/xz', ['--decompress', '--stdout',
  join(root, 'metadata/Packages.xz')]));
const packageFields = new Map(indexFields.filter(p => p.Package).map(p => [p.Package, p]));

for (const p of manifest.packages) {
  assert.equal(p.architecture, 'arm64');
  assert.equal(basename(p.filename), p.localFilename);
  assert.match(p.localFilename, /^[a-z0-9][a-z0-9+.-]*_[^/]+_arm64\.deb$/);
  assert.equal(p.url, 'https://deb.debian.org/debian/' + p.filename);
  const signed = packageFields.get(p.name);
  assert(signed);
  for (const [key, expected] of Object.entries({
    Version: p.version, Architecture: p.architecture, Filename: p.filename,
    SHA256: p.sha256, Size: String(p.size), Depends: p.depends,
  })) assert.equal(signed[key], expected, 'signed package metadata mismatch: ' + p.name + ':' + key);
  assert.equal(signed['Pre-Depends'] ?? '', p.preDepends);
  assert.equal(signed.Source ?? signed.Package, p.source);
  const bytes = file('packages/' + p.localFilename);
  assert.equal(hash(bytes), p.sha256);
  assert.equal(bytes.byteLength, p.size);
  // Stream control metadata to stdout; dpkg-deb --field otherwise needs writable scratch.
  const controlTar = execFileSync('/usr/bin/dpkg-deb', ['--ctrl-tarfile',
    join(root, 'packages', p.localFilename)], { maxBuffer: 1024 * 1024 });
  const controlText = execFileSync('/usr/bin/tar', ['--extract', '--to-stdout',
    '--file', '-', './control'], { input: controlTar, encoding: 'utf8' });
  const control = paragraphs(controlText)[0];
  for (const [field, expected] of Object.entries({
    Package: p.name, Version: p.version, Architecture: p.architecture,
    Depends: p.depends, 'Pre-Depends': p.preDepends,
  })) assert.equal(control[field] ?? '', expected);
  assert(!versions.has(p.name), 'unexpected base package would be overwritten');
  versions.set(p.name, p.version);
}
for (const p of manifest.packages) {
  for (const dependency of [p.preDepends, p.depends].filter(Boolean).join(', ').split(',')) {
    const match = /^\s*([a-z0-9+.-]+)(?:\s+\((>=|<=|=|>>|<<)\s+([^()]+)\))?\s*$/.exec(dependency);
    assert(match, 'unsupported dependency syntax');
    const actual = versions.get(match[1]);
    assert(actual, 'missing dependency: ' + match[1]);
    if (match[2]) {
      const compared = spawnSync('/usr/bin/dpkg', ['--compare-versions', actual, match[2], match[3]]);
      assert.equal(compared.status, 0, 'unsatisfied dependency: ' + dependency);
    }
  }
}
console.log(JSON.stringify({event: 'locked_dependencies_verified', count: manifest.packages.length,
  maintainerScriptsExecuted: false, basePackageSet: manifest.base.installedPackagesSha256,
  releaseSha256: manifest.repository.releaseSha256, indexSha256: manifest.repository.indexSha256}));
