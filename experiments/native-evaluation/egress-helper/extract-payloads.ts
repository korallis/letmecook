import './verify-dependencies.ts';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { cpSync, existsSync, lstatSync, mkdtempSync, readFileSync, readdirSync, realpathSync, rmSync } from 'node:fs';
import { join } from 'node:path';

assert.equal(process.getuid(), 0, 'payload extraction is a build-only root operation');
const manifest = JSON.parse(readFileSync(join(import.meta.dirname, 'manifest.json'), 'utf8'));
for (const p of manifest.packages) {
  // Stage first: extracting /lib directly would replace this base's /lib -> usr/lib.
  // Preserve existing top-level usrmerge links while copying authenticated payload.
  // Execute no maintainer or service script.
  const stage = mkdtempSync('/tmp/gaffer-egress-payload-');
  try {
    execFileSync('/usr/bin/dpkg-deb', ['--extract',
      join(import.meta.dirname, 'packages', p.localFilename), stage], { stdio: 'inherit' });
    for (const entry of readdirSync(stage, {withFileTypes: true})) {
      const target = '/' + entry.name;
      const destination = entry.isDirectory() && existsSync(target) && lstatSync(target).isSymbolicLink()
        ? realpathSync(target) : target;
      cpSync(join(stage, entry.name), destination, {
        recursive: true, force: true, preserveTimestamps: true, dereference: false, verbatimSymlinks: true,
      });
    }
  } finally { rmSync(stage, {recursive: true, force: true}); }
}
console.log(JSON.stringify({event: 'locked_payloads_extracted',
  packages: manifest.packages.map((p: any) => ({name: p.name, version: p.version})),
  maintainerScriptsExecuted: false, packageDatabaseUpdated: false}));
