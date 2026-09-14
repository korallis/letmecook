import { validateNativeProfile, type NativeProfile } from '../router-authority-extension/overlay/native-profile.mjs';
import { PLANNER_PROTOCOL, PLANNER_TOOL, PLANNER_SETTINGS_DIGEST, PLANNER_SCHEMA_DIGEST } from '../router-authority-extension/overlay/native-planner.mjs';
import { plannerRuntimeIdentity } from './native-boundary-port.ts';
// Start from the reviewed common deployment/authorization/scope/connection
// envelope. This changes only the closed consumer descriptor and local limits.
export function plannerProfile(common:NativeProfile):NativeProfile {
  const {harness:_worker,planner:_old,...base}=structuredClone(common);
  return validateNativeProfile({...base,protocol:PLANNER_PROTOCOL,tools:[structuredClone(PLANNER_TOOL)],toolPaths:['fixture.txt'],
    local:{...base.local,requestBytes:32768,responseBytes:32768,requestCount:3,totalMs:5000,firstOutputMs:Math.min(4000,base.local.firstOutputMs),idleMs:Math.min(2000,base.local.idleMs)},
    planner:{source:plannerRuntimeIdentity(),settings:PLANNER_SETTINGS_DIGEST,schema:PLANNER_SCHEMA_DIGEST}});
}
