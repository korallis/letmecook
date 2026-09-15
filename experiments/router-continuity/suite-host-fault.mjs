// Explicit offline host loader. Never selected by production suite/deployment CLI.
import { registerHooks } from 'node:module';
const mode=process.env.GAFFER_SUITE_TEST;
if(!['storage','stop','unknown','consume-storage','direct','repair'].includes(mode))throw Error('suite_fault_opt_in_required');
registerHooks({load(url,context,next){const result=next(url,context);let source=String(result.source);
 if(url.endsWith('/native-evaluation/durable-records.mjs')&&['storage','consume-storage','direct','repair'].includes(mode))source=source.replace('export function persistImmutable(directory,kind,value){',`export function persistImmutable(directory,kind,value){
 if(kind==='${mode==='storage'?'initial-suite':'continuity-consumer'}' && (value.phases??value.workers)?.some(x=>x.observation)) {
  if(process.env.GAFFER_SUITE_AUDIT_FILE)writeFileSync(process.env.GAFFER_SUITE_AUDIT_FILE,JSON.stringify(value),{mode:0o600});
  throw Error('synthetic_persistent_observation_storage_failure');
 }
`);
 if(url.endsWith('/native-evaluation/deployment.ts')&&mode==='stop')source=source.replace("else{await control({command:'stop'});", "else{throw Error('synthetic_stop_command_failure');await control({command:'stop'});");
 if(url.endsWith('/native-evaluation/deployment.ts')&&['unknown','direct','repair'].includes(mode))source=source.replace("'/probe/loader.mjs'","'/gaffer/experiments/router-continuity/suite-gateway-fault.mjs'").replace("'--env','GAFFER_SYNTHETIC_NATIVE='+(synthetic?'1':'')","'--env','GAFFER_SYNTHETIC_NATIVE='+(synthetic?'1':''),'--env','GAFFER_SUITE_TEST="+mode+"'");
 return {...result,source};}});
