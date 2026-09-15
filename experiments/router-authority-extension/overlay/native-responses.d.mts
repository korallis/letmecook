import type { NativeProfile } from './native-profile.mjs';
export function validatePatch(text:string,profile:NativeProfile):unknown;
export function continuationOutput(output:any[],profile:NativeProfile):any[];
export function validateNativeRequest(body:any,profile:NativeProfile,model:string,previous?:{request:any;output:any[]}|null):any;
export function expectedPhysicalRequest(ingress:any,model:string,instructions:string):any;
export class NativeResponsesStream {
 constructor(profile:NativeProfile,request?:any);
 semanticOutput:boolean; nativeOutput:any[]|null;
 push(bytes:Uint8Array):{output:string[];semantic:boolean;error?:Error};
 end():string[];
}

export function validateNativeOutput(output:any[],profile:NativeProfile,request:any):any[];
