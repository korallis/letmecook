import { request } from 'node:http';
import { readFileSync } from 'node:fs';
import { isAbsolute } from 'node:path';
import { canonical } from '../inference-boundary/json.ts';
import { validateBinding, type Binding } from '../inference-boundary/types.ts';
import { assertNativeBinding, assertPlannerPacket, validateNativeRouterPolicy, type NativeRouterPolicy } from '../inference-boundary/native-policy.ts';
import { NativeResponsesStream, validateNativeRequest } from '../router-authority-extension/overlay/native-responses.mjs';
import { PLANNER_PROTOCOL, PLANNER_SCHEMA_DIGEST } from '../router-authority-extension/overlay/native-planner.mjs';
import { ProbeError, sha256 } from './reader.ts';
import { NativePlannerTransport, type NativePlannerBoundaryPort, type NativePlannerPolicy, type ReleasedNativeCompletion } from './native-transport.ts';

export function plannerRuntimeIdentity(): string {
  const files = ['planner.ts','transport.ts','native-transport.ts','native-boundary-port.ts','native-consumer.ts','reader.ts','public-fixture.ts','package-lock.json'];
  const manifest = Object.fromEntries(files.map(file => [file, sha256(readFileSync(new URL(file,import.meta.url)))]));
  const schema = JSON.parse(readFileSync(new URL('../../tests/fixtures/planner/plan.schema.json',import.meta.url),'utf8'));
  if(sha256(canonical(schema))!==PLANNER_SCHEMA_DIGEST)throw new ProbeError('native_profile_denied');
  return sha256(canonical({...manifest,planSchema:PLANNER_SCHEMA_DIGEST}));
}
// The only production implementation: scoped private boundary UDS, never a
// provider URL, direct router endpoint, receipt store or supervisor control API.
export class NativeBoundaryPort implements NativePlannerBoundaryPort {
  private readonly socket:string; private readonly token:string; private readonly approved:NativeRouterPolicy;
  private readonly grant:Binding; private previous?:{request:any;output:any[]}; private stopped=false; private sending=false;
  readonly binding:NativePlannerBoundaryPort['binding'];
  constructor(socket:string,token:string,policy:NativeRouterPolicy,binding:Binding){
    this.approved=validateNativeRouterPolicy(policy);validateBinding(binding);assertNativeBinding(binding,this.approved);
    if(!isAbsolute(socket)||socket.includes('\0')||!/^\/[a-zA-Z0-9_./-]+$/.test(socket)||!/^[a-f0-9]{64}$/.test(token)||policy.native.protocol!==PLANNER_PROTOCOL||policy.native.planner?.source!==plannerRuntimeIdentity())throw new ProbeError('native_profile_denied');
    if(binding.routerId!==policy.routerId||binding.routeId!==policy.routeId||binding.revision!==policy.revision||binding.epoch!==policy.epoch)throw new ProbeError('native_profile_denied');
    this.socket=socket;this.token=token;this.grant=structuredClone(binding);
    this.binding=Object.freeze({role:'planner',protocol:PLANNER_PROTOCOL,sessionId:'ses_planner_'+sha256(canonical(binding)).slice(0,40)});
  }
  get policy():NativePlannerPolicy{return structuredClone(this.approved) as unknown as NativePlannerPolicy;}
  async complete(value:unknown,signal:AbortSignal,responseBytes:number):Promise<ReleasedNativeCompletion>{
    try{
      if(this.stopped||this.sending)throw new ProbeError('native_session_ended');
      if(signal.aborted)throw new ProbeError('cancelled');
      if(Date.now()>=Math.min(this.grant.expiresAt,this.grant.leaseExpiresAt))throw new ProbeError('lease_expired');
      const body=validateNativeRequest(value,this.approved.native,this.approved.routerModel,this.previous??null);
      assertPlannerPacket(body,this.approved);
      if(body.prompt_cache_key!==this.binding.sessionId)throw new ProbeError('native_profile_drift');
      const bytes=JSON.stringify(body),limit=Math.min(responseBytes,this.approved.limits.responseBytes,32768);
      if(!Number.isSafeInteger(limit)||limit<1)throw new ProbeError('response_budget_exhausted');
      this.sending=true;
      const released=await new Promise<ReleasedNativeCompletion>((resolve,reject)=>{
        const codec=new NativeResponsesStream(this.approved.native,body);let received=0;let semantic=false;
        const req=request({socketPath:this.socket,path:'/v1/responses',method:'POST',agent:false,signal,headers:{host:'localhost',authorization:'Bearer '+this.token,'content-type':'application/json','content-length':Buffer.byteLength(bytes)}},res=>{
          const fail=(error:ProbeError)=>{res.destroy();req.destroy();reject(error);};
          if(res.statusCode!==200||res.headers['content-type']?.split(';')[0]!=='text/event-stream'||res.headers['content-encoding']){fail(new ProbeError(res.statusCode===429?'request_budget_exhausted':res.statusCode===403?'policy_denied':'route_unavailable'));return;}
          const requestId=res.headers['x-gaffer-request-id'];if(typeof requestId!=='string'||!/^[a-f0-9]{32}$/.test(requestId)){fail(new ProbeError('invalid_request_identity'));return;}
          res.on('data',(chunk:Buffer)=>{try{received+=chunk.length;if(received>limit)throw new ProbeError('response_budget_exhausted');const next=codec.push(chunk);semantic ||= next.semantic;if(next.error)throw new ProbeError(semantic?'partial_failure':'invalid_stream');}catch(error){fail(error instanceof ProbeError?error:new ProbeError('invalid_stream'));}});
          res.on('end',()=>{try{
            if(signal.aborted||Date.now()>=Math.min(this.grant.expiresAt,this.grant.leaseExpiresAt))throw new ProbeError('cancelled_unknown');
            codec.end();if(!codec.nativeOutput)throw new ProbeError('invalid_stream');
            // The boundary withholds every byte until persisted original success.
            // A complete valid SSE/EOF here is necessary, never a direct-provider
            // receipt inference. Deployment owns the exclusive socket and fence.
            resolve({requestId,acceptedAt:Date.now(),response:{model:'gpt-6-astra',status:'completed',error:null,incomplete_details:null,output:structuredClone(codec.nativeOutput)}});
          }catch(error){reject(error instanceof ProbeError?error:new ProbeError(semantic?'partial_failure':'invalid_stream'));}});
          res.on('error',()=>reject(new ProbeError(signal.aborted?'cancelled_unknown':semantic?'partial_failure':'route_unavailable')));
        });
        req.on('error',()=>reject(new ProbeError(signal.aborted?'cancelled_unknown':'route_unavailable')));req.end(bytes);
      });
      this.previous={request:structuredClone(body),output:structuredClone(released.response.output)};return released;
    }catch(error){this.stopped=true;throw error instanceof ProbeError?error:new ProbeError('native_boundary_denied');}
    finally{this.sending=false;}
  }
}
export function nativePlannerTransport(socket:string,token:string,policy:NativeRouterPolicy,binding:Binding){return new NativePlannerTransport(new NativeBoundaryPort(socket,token,policy,binding));}
