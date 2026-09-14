import { readFile,writeFile,mkdir } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { join } from 'node:path';
const root=import.meta.dirname,manifest=JSON.parse(await readFile(join(root,'manifest.json'),'utf8'));
for(const input of [{url:manifest.repository.indexUrl,path:'metadata/Packages.xz',sha256:manifest.repository.indexSha256,size:manifest.repository.indexBytes},...manifest.packages.map((p:any)=>({...p,path:'packages/'+p.localFilename}))]){
 if(new URL(input.url).origin!=='https://deb.debian.org'||!Number.isSafeInteger(input.size)||input.size<1||input.size>16000000)throw Error('invalid_locked_download');
 const response=await fetch(input.url,{redirect:'error',signal:AbortSignal.timeout(30000)});if(!response.ok||!response.body)throw Error('locked_dependency_unavailable');
 let length=0;const chunks:Uint8Array[]=[];for await(const chunk of response.body){length+=chunk.length;if(length>input.size)throw Error('locked_dependency_size');chunks.push(chunk);}
 const bytes=Buffer.concat(chunks);if(bytes.length!==input.size||createHash('sha256').update(bytes).digest('hex')!==input.sha256)throw Error('locked_dependency_hash');
 await mkdir(join(root,input.path.startsWith('metadata/')?'metadata':'packages'),{recursive:true});await writeFile(join(root,input.path),bytes);
}
console.log('Downloaded exact locked inputs; the offline Docker build verifies signatures and dependency closure.');
