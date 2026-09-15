export const INITIAL_SUITE:'initial-suite-v1';
export const SUITE_PHASES:readonly string[];
export const SUITE_TIMES:Record<string,{wallMs:number;totalMs:number;firstOutputMs:number;idleMs:number}>;
export function phaseConsumer(phase:string):string;
export function suiteTiming(profile:any):{wallMs:number;totalMs:number;firstOutputMs:number;idleMs:number}|null;
export function phasePlan(scope:any,phase:string,now?:number):{phase:string;wallMs:number;sessionDeadline:number;grantDeadline:number;scopeDeadline:number;requiredMs:number;requiredSends:number};
export function assertSuiteRequest(request:any,consumer:string):void;
