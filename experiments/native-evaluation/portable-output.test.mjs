import assert from 'node:assert/strict';
import {test} from 'node:test';
import {spawn} from 'node:child_process';
import {collectProcessOutput,caseOutput} from '../router-authority-extension/process-output.mjs';
const child=code=>spawn(process.execPath,['--input-type=module','-e',code],{stdio:['ignore','pipe','pipe']});

test('large final evidence survives pipe backpressure before forced process exit',async()=>{
 const module=new URL('../router-authority-extension/process-output.mjs',import.meta.url).href;
 const p=child(`import {writeJSON} from ${JSON.stringify(module)};await writeJSON({result:'passed',text:'🌍'.repeat(1024*1024)});process.exit(0);`);
 p.stdout.pause();const result=collectProcessOutput(p);p.stdout.pause();setTimeout(()=>p.stdout.resume(),100);
 const output=await result;assert.equal(output.code,0);assert.deepEqual(caseOutput(output.stdout),{result:'passed',text:'🌍'.repeat(1024*1024)});
});
test('parent exit does not truncate delayed inherited stdout or split UTF8',async()=>{
 const descendant=`setTimeout(()=>process.stdout.write(Buffer.from([0xf0,0x9f])),60);setTimeout(()=>process.stdout.write(Buffer.from([0x8c,0x8d,0x22,0x7d,0x0a])),100);`;
 const p=child(`import {spawn} from 'node:child_process';process.stdout.write('{"text":"');spawn(process.execPath,['-e',${JSON.stringify(descendant)}],{stdio:['ignore',1,2]}).unref();process.exit(0);`);
 let exited=false;p.once('exit',()=>{exited=true});const output=await collectProcessOutput(p);assert(exited);assert.equal(output.code,0);assert.deepEqual(caseOutput(output.stdout),{text:'🌍'});
});
test('spawn failure and permanently truncated records remain failures',async()=>{
 await assert.rejects(collectProcessOutput(spawn('/nonexistent/gaffer-output-fixture',[],{stdio:['ignore','pipe','pipe']})),{code:'ENOENT'});
 const output=await collectProcessOutput(child(`process.stdout.write('{"result":"pass');`));assert.equal(output.code,0);assert.throws(()=>caseOutput(output.stdout),SyntaxError);assert.throws(()=>caseOutput(''),/evidence_record_missing/);assert.throws(()=>caseOutput('{broken\n{"result":"passed"}\n'),SyntaxError);
});
