// Private, read-only retention after gateway shutdown. Never a public projection.
import { readFileSync,readdirSync } from 'node:fs';
import { digest } from '../router-authority-extension/overlay/native-profile.mjs';
const records={};
for(const file of readdirSync('/state'))if(/^(?:result\.json|(?:continuity-intent|continuity-result|deployment-packet|candidate)-[a-f0-9]{64}\.json)$/.test(file)){
 const value=JSON.parse(readFileSync('/state/'+file,'utf8'));if(file!=='result.json'&&!file.endsWith(digest(value)+'.json'))throw Error('continuity_retention_digest');records[file]=value;
}
console.log(JSON.stringify({records}));
