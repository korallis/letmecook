import { mkdirSync,openSync,writeFileSync,fsyncSync,closeSync,readFileSync,unlinkSync,lstatSync } from 'node:fs';
import { join,resolve } from 'node:path';
import { randomUUID,createHash } from 'node:crypto';
let current;
function syncDirectory(path){const fd=openSync(path,'r');try{fsyncSync(fd);}finally{closeSync(fd);}}
export function acquireDeploymentOwner(directory){
 if(current||!directory.startsWith('/')||resolve(directory)!==directory)throw Error('deployment_owner_invalid');
 mkdirSync(directory,{recursive:true,mode:0o700});if(!lstatSync(directory).isDirectory()||lstatSync(directory).isSymbolicLink())throw Error('deployment_storage_invalid');
 const path=join(directory,'deployment-owner.lock'),record={schema:1,owner:randomUUID(),pid:process.pid},serialized=JSON.stringify(record);
 const fd=openSync(path,'wx',0o600);try{writeFileSync(fd,serialized);fsyncSync(fd);}finally{closeSync(fd);}syncDirectory(directory);
 let released=false;
 current=Object.freeze({digest:createHash('sha256').update(serialized).digest('hex'),
  assertCurrent(){if(released||readFileSync(path,'utf8')!==serialized)throw Error('deployment_owner_fenced');},
  release(quiescent){this.assertCurrent();if(!quiescent)throw Error('deployment_unknown_retains_owner');unlinkSync(path);syncDirectory(directory);released=true;current=null;}
 });return current;
}
export function deploymentOwner(){if(!current)throw Error('deployment_owner_required_before_database');current.assertCurrent();return current;}
