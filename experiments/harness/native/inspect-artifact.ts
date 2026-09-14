// Mounted read-only and invoked by the trusted supervisor after the binary exits.
import { readArtifact } from './run.ts';
console.log(JSON.stringify(readArtifact(process.argv[2])));
