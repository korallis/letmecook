// Pure closed timing descriptor. No state, provider access or admission authority.
export const INITIAL_SUITE='initial-suite-v1';
export const SUITE_PHASES=Object.freeze(['worker','planner','continuity_a','continuity_b']);
export const SUITE_TIMES=Object.freeze({worker:Object.freeze({wallMs:180000,totalMs:120000,firstOutputMs:90000,idleMs:45000}),planner:Object.freeze({wallMs:150000,totalMs:120000,firstOutputMs:90000,idleMs:45000}),continuity:Object.freeze({wallMs:90000,totalMs:75000,firstOutputMs:60000,idleMs:30000})});
export const phaseConsumer=phase=>phase.startsWith('continuity_')?'continuity':phase;
export function suiteTiming(profile){
 if(profile?.schema!==2)return null;
 const t=profile.timing;
 if(!t||JSON.stringify(Object.keys(t).sort())!==JSON.stringify(['consumer','variant'])||t.variant!==INITIAL_SUITE||!Object.hasOwn(SUITE_TIMES,t.consumer))throw Error('suite_timing');
 if(profile.scope?.phase!=='initial'||profile.scope.maxInferenceAttempts!==10||profile.scope.elapsedMs!==600000||profile.scope.maxRefreshOperations!==0)throw Error('suite_scope');
 const selected=SUITE_TIMES[t.consumer];for(const key of ['totalMs','firstOutputMs','idleMs'])if(profile.local[key]!==selected[key])throw Error('suite_request_timing');
 if((t.consumer==='planner')!==(profile.protocol==='planner-probe-responses-read-file-v1'))throw Error('suite_consumer');
 return selected;
}
export function phasePlan(scope,phase,now=Date.now()){
 const index=SUITE_PHASES.indexOf(phase);if(index<0||scope?.state!=='active'||scope.phase!=='initial'||!Number.isSafeInteger(now)||scope.deadline-scope.started!==600000)throw Error('suite_scope');
 const remaining=SUITE_PHASES.slice(index),requiredMs=remaining.reduce((n,x)=>n+SUITE_TIMES[phaseConsumer(x)].wallMs+15000,0)+15000;
 const requiredSends=remaining.reduce((n,x)=>n+(['worker','planner'].includes(x)?2:1),0);
 if(scope.spent+requiredSends>10||now<scope.started||scope.deadline-now<requiredMs)throw Error('suite_remaining_envelope');
 const wallMs=SUITE_TIMES[phaseConsumer(phase)].wallMs,sessionDeadline=now+wallMs,grantDeadline=sessionDeadline+15000;
 return {phase,wallMs,sessionDeadline,grantDeadline,scopeDeadline:scope.deadline,requiredMs,requiredSends};
}
export function assertSuiteRequest(request,consumer){
 if(request.schema!==2)return;
 if(request.timing!==INITIAL_SUITE||![request.packetDigest,request.profileDigest].every(x=>typeof x==='string'&&/^[a-f0-9]{64}$/.test(x))||!Number.isSafeInteger(request.sessionDeadline)||request.sessionDeadline<=0||(request.limits?.wallMs??request.wallMs)!==SUITE_TIMES[consumer].wallMs)throw Error('suite_request');
}
