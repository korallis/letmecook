import type { NativeProfile } from './native-profile.mjs';
export const PLANNER_PROTOCOL:'planner-probe-responses-read-file-v1';
export const PLANNER_TOOL:any;
export const PLANNER_REPAIR:string;
export const PLANNER_SYSTEM:string;
export const PLANNER_BUDGET:{assessments:number;repairs:number;requests:number;files:number;readBytes:number;requestBytes:number;responseBytes:number;outputTokens:null;totalMs:number};
export const PLANNER_SCHEMA_DIGEST:string;
export const PLANNER_SETTINGS_DIGEST:string;
export function validatePlannerDescriptor(profile:NativeProfile):void;
export function plannerPlanValid(text:string,revision:string,fileRead:boolean):boolean;
export function plannerContinuation(output:any[],toolsAllowed?:boolean):any[];
export function plannerOutputText(output:any[]):string;
export function plannerRequestState(body:any,profile:NativeProfile):{phase:'assessment'|'final'|'repair';fileRead:boolean;repairs:number;requests:number;inputRevision:string;packet:any};
export function validatePlannerRequest(body:any,profile:NativeProfile,model:string,previous?:{request:any;output:any[]}|null):any;
export function validatePlannerOutput(output:any[],profile:NativeProfile,request:any):any[];
export function validatePlannerCandidate(value:{text:string;policy:any;binding:any;decisions:any[];artifact:any}):boolean;

export function plannerBudget(profile:any):typeof PLANNER_BUDGET;
export function plannerSettingsDigest(profile:any):string;
