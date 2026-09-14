// Append-only content-addressed records. Acknowledgement follows both file and
// directory fsync; a retry can verify/re-sync an existing record, never replace it.
import { constants,openSync,writeFileSync,readFileSync,fsyncSync,closeSync,linkSync,unlinkSync,fstatSync } from 'node:fs';
import { join } from 'node:path';
import { randomBytes } from 'node:crypto';
import { canonical,digest } from '../router-authority-extension/overlay/native-profile.mjs';
export function persistImmutable(directory,kind,value){
 if(typeof kind!=='string'||!/^[a-z][a-z0-9-]{0,31}$/.test(kind))throw Error('invalid_record_kind');
 const bytes=canonical(value);if(Buffer.byteLength(bytes)>2097152)throw Error('record_bytes');
 const hash=digest(value),file=kind+'-'+hash+'.json',path=join(directory,file),temporary=join(directory,'.'+file+'.'+randomBytes(12).toString('hex')+'.next');
 let pending=false,created=false;
 try{
  const fd=openSync(temporary,constants.O_CREAT|constants.O_EXCL|constants.O_WRONLY|constants.O_NOFOLLOW,0o600);pending=true;
  try{writeFileSync(fd,bytes);fsyncSync(fd);}finally{closeSync(fd);}
  try{linkSync(temporary,path);created=true;}catch(error){if(error.code!=='EEXIST')throw error;}
  const existing=openSync(path,constants.O_RDONLY|constants.O_NOFOLLOW);
  try{if(!fstatSync(existing).isFile()||readFileSync(existing,'utf8')!==bytes)throw Error('immutable_record_mismatch');fsyncSync(existing);}finally{closeSync(existing);}
  unlinkSync(temporary);pending=false;
  const parent=openSync(directory,constants.O_RDONLY|constants.O_DIRECTORY|constants.O_NOFOLLOW);try{fsyncSync(parent);}finally{closeSync(parent);}
  return {digest:hash,file,created};
 }finally{if(pending)try{unlinkSync(temporary);}catch{/* Preserve partial evidence for supervised recovery. */}}
}
