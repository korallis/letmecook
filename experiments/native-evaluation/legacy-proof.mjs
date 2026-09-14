import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
const {stdout}=await promisify(execFile)(process.execPath,['/probe/run-in-container.mjs'],{timeout:175000,maxBuffer:32*1024*1024});
console.log(JSON.stringify({scenario:'legacy',result:'passed',legacy:JSON.parse(stdout)}));
