import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp,readFile,rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { inputProcess } from './lifecycle.ts';
test('stop before inputProcess starts launches no child or side effect',async()=>{
 const dir=await mkdtemp(join(tmpdir(),'gaffer-stop-'));try{const path=join(dir,'started');const signal=AbortSignal.abort(new Error('stopped'));await assert.rejects(inputProcess(process.execPath,['-e',`require('fs').writeFileSync(${JSON.stringify(path)},'started')`],'',signal),/stopped/);await assert.rejects(readFile(path));}finally{await rm(dir,{recursive:true,force:true});}
});
test('stop during inputProcess kills the pending CLI and settles rather than hanging',async()=>{
 const abort=new AbortController();const work=inputProcess(process.execPath,['-e','setInterval(()=>{},1000)'],'',abort.signal);setTimeout(()=>abort.abort(),30);await assert.rejects(work,/input_process_failed/);
});
test('a noncooperative CLI is killed after bounded graceful cancellation',async()=>{
 const dir=await mkdtemp(join(tmpdir(),'gaffer-kill-'));try{const path=join(dir,'ready'),abort=new AbortController();const work=inputProcess(process.execPath,['-e',`process.on('SIGTERM',()=>{});require('fs').writeFileSync(${JSON.stringify(path)},'ready');setInterval(()=>{},1000)`],'',abort.signal);const until=Date.now()+1000;while(true){try{await readFile(path);break;}catch{assert(Date.now()<until);await new Promise(r=>setTimeout(r,5));}}const at=Date.now();abort.abort();await assert.rejects(work);assert(Date.now()-at<1500);}finally{await rm(dir,{recursive:true,force:true});}
});
test('explicit bounded input deadline terminates a stalled CLI; invalid limits never start', async () => {
 const signal = new AbortController().signal;
 for (const limit of [0, -1, 45001, NaN]) await assert.rejects(inputProcess(process.execPath, ['-e', 'process.exit(0)'], '', signal, limit), /invalid_input_process_deadline/);
 const at = Date.now(); await assert.rejects(inputProcess(process.execPath, ['-e', 'setInterval(()=>{},1000)'], '', signal, 50)); assert(Date.now() - at < 1500);
});
