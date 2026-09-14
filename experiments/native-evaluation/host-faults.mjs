// Test-only preload: simulate persistent record open/fsync failure after the
// actual deployment has started. No production path imports this module.
import fs from 'node:fs';
import { syncBuiltinESMExports } from 'node:module';
const original=fs.promises.open;
fs.promises.open=async function(path,...args){
 const target=typeof path==='string'&&path.startsWith(process.env.GAFFER_TEST_RECORD_FAILURE+'.')&&path.endsWith('.next');
 if(target&&process.env.GAFFER_TEST_RECORD_FAILURE_MODE==='open')throw Object.assign(Error('injected_record_open_failure'),{code:'ENOSPC'});
 const handle=await original.call(this,path,...args);
 if(target&&process.env.GAFFER_TEST_RECORD_FAILURE_MODE==='sync')handle.sync=async()=>{throw Object.assign(Error('injected_record_sync_failure'),{code:'EIO'});};
 return handle;
};
syncBuiltinESMExports();
