import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
const before=JSON.parse(readFileSync(process.argv[2],'utf8'));
const {getAdapter}=await import('/router-source/src/lib/db/driver.js');
const {authority}=await import('/router-source/gaffer-extension/authority.mjs');
await getAdapter();const a=authority(),state=a.state(),receipt=a.receipt(before.receipt.id),scope=a.evaluationScope(before.scope.id);
// The old owner's close fences once, then the new boot advances once more.
assert.notEqual(state.boot,before.state.boot);assert.equal(state.generation,before.state.generation+2);assert.equal(state.phase,'closed');
assert.deepEqual({...scope},{...before.scope,state:'closed'});
const expected=structuredClone(before.receipt);
for(const operation of expected.operations){
  if(operation.terminal==='unknown')operation.local_stop='crash_unknown';
  if(!Object.hasOwn(operation,'response_observation'))operation.response_observation=null;
}
assert.deepEqual(receipt,expected);
assert.equal(receipt.operations.length,1);assert.equal(scope.spent,1);
if(!receipt.quiescent){
  let writerCalled=false;
  assert.equal(a.quiescent([receipt.id]),false);
  assert.equal(await a.replace(state.generation,'restart_forbidden',state.policy,()=>{writerCalled=true;}),false);
  assert.equal(writerCalled,false);
}
console.log(JSON.stringify({result:'passed',scope,receipt,state:{boot:state.boot,generation:state.generation,phase:state.phase},physicalSends:0}));
a.close();
