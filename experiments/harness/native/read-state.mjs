// Trusted, read-only retention after the gateway exits, including failed stop.
import { readFileSync, readdirSync, lstatSync } from 'node:fs';
import { createHash } from 'node:crypto';
const records = {}, inventory = [];
function visit(directory = '') {
  for (const name of readdirSync('/state/' + directory)) {
    const relative = directory + name, path = '/state/' + relative, stat = lstatSync(path);
    if (stat.isSymbolicLink()) throw Error('unexpected_state_symlink');
    if (stat.isDirectory()) { inventory.push({ path: relative, type: 'directory' }); visit(relative + '/'); }
    else if (stat.isFile()) {
      const bytes = readFileSync(path); inventory.push({ path: relative, type: 'file', bytes: bytes.length, digest: createHash('sha256').update(bytes).digest('hex') });
      if (/^(result\.json|boundary\/state\.json|(?:candidate|artifact|artifact-ack|deployment-packet)-[a-f0-9]{64}\.json)$/.test(relative)) records[relative] = JSON.parse(bytes.toString());
    }
  }
}
visit(); console.log(JSON.stringify({ result: records['result.json'] ?? null, records, inventory }));
