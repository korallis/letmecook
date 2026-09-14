export interface NativeProfile {
 schema:1; limitsProfile:'native-subscription-local-v1'; protocol:'opencode-1.18.30-responses-apply-patch-v1'|'planner-probe-responses-read-file-v1'; evidence:'synthetic'|'reviewed-deployment';
 deployment:{id:string;sourceCommit:string;sourceLock:string;overlay:string;runtime:string;isolation:string;writerFence:string;credentialOwnershipRef:string;endpoint:string};
 authorization:{id:string;approved:true;model:'gpt-6-astra';effort:'xhigh';providerOutput:{requirement:'not_required';capability:'unavailable'};providerMonetaryCap:{requirement:'not_required';capability:'unavailable'};subscriptionEnvelopeRef:string;refresh:'denied';caseRef:string};
 scope:{id:string;caseRef:string;phase:'initial'|'baseline';authorizationDigest:string;maxInferenceAttempts:number;maxRefreshOperations:0;elapsedMs:number};
 connections:{id:string;credentialRef:string;billing:'existing-codex-subscription';expiresAt:number;skewMs:number}[];
 local:{requestBytes:number;responseBytes:number;concurrency:number;requestCount:number;totalMs:number;firstOutputMs:number;idleMs:number;attemptMs:number};
 tools:any[];toolPaths:string[];
 planner?:{source:string;settings:string;schema:string};
 harness?:{binary:string;source:string;settings:string;builtins:'enabled';nativeLLM:false;oauth:false;websockets:false};
}
export function validateNativeProfile(value:unknown):NativeProfile;
export function canonical(value:unknown):string;
export function digest(value:unknown):string;
export function safePath(value:unknown):boolean;
export const NATIVE_LIMITS:'native-subscription-local-v1';
export const NATIVE_PROTOCOL:'opencode-1.18.30-responses-apply-patch-v1';

export function nativeConsumerRole(profile:NativeProfile):'worker'|'planner';

export function validateNativeRegistry(input:unknown):NativeProfile[];
