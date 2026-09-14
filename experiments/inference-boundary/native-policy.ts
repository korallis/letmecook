import { canonical, keys } from './json.ts';
import { Denial, ref, type Binding, type Limits } from './types.ts';
import { hashDocument, validateIdentity, type AuthorityIdentity } from './router-policy.ts';
import { validateNativeProfile, nativeConsumerRole, type NativeProfile } from '../router-authority-extension/overlay/native-profile.mjs';
import { PLANNER_PROTOCOL, plannerRequestState } from '../router-authority-extension/overlay/native-planner.mjs';
export interface NativeRouterPolicy {
 schema:3;routerId:string;routeId:string;revision:string;epoch:number;routerModel:string;
 profile:'router-native-responses-local-v1';evidence:'synthetic'|'reviewed-deployment';liveAdmission:boolean;
 graph:Record<string,any>;limits:Omit<Limits,'outputTokens'>&{outputTokens:null};authority:AuthorityIdentity;native:NativeProfile;
}
export function validateNativeRouterPolicy(p:NativeRouterPolicy):NativeRouterPolicy {
 try {
  keys(p,['schema','routerId','routeId','revision','epoch','routerModel','profile','evidence','liveAdmission','graph','limits','authority','native'],['schema','routerId','routeId','revision','epoch','routerModel','profile','evidence','liveAdmission','graph','limits','authority','native']);
  const n=validateNativeProfile(p.native);validateIdentity(p.authority);
  if(p.schema!==3||p.profile!=='router-native-responses-local-v1'||![p.routerId,p.routeId,p.revision,p.routerModel].every(ref)||!Number.isSafeInteger(p.epoch)||p.epoch<1||p.authority.revision!==p.revision||p.authority.deploymentId!==n.deployment.id||p.evidence!==n.evidence||p.liveAdmission!==(n.evidence==='reviewed-deployment')||canonical(p.limits)!==canonical({...n.local,outputTokens:null}))throw Error();
  const g=p.graph;
  keys(g,['schema','profile','profileDigest','nodes','connections','routes','settings','bounds','terminals'],['schema','profile','profileDigest','nodes','connections','routes','settings','bounds','terminals']);
  if(g.schema!==2||g.profile!=='9router-0.5.75-native-local-v1'||g.profileDigest!==hashDocument(n)||hashDocument(g)!==p.authority.graphDigest||canonical(g.nodes)!=='[]'||canonical(g.bounds)!==canonical({...n.local,providerOutputTokens:null,providerMonetaryCap:null,scope:n.scope})||canonical(g.terminals)!==canonical(['validated-original-responses-sse','validated-original-json-error']))throw Error();
  if(canonical(g.settings)!==canonical({requireApiKey:true,comboStrategy:'fallback',accountStrategy:'fill-first',adapters:false,helpers:false,remoteResources:false,proxies:false,autoPing:false,refresh:false}))throw Error();
  if(!Array.isArray(g.connections)||g.connections.length!==n.connections.length||new Set(g.connections.map((c:any)=>c.id)).size!==g.connections.length)throw Error();
  for(const c of g.connections){
   keys(c,['id','provider','authType','priority','isActive','endpoint','workspace','billing','providerOutputBound','credentialRef','expiresAt','skewMs','refresh'],['id','provider','authType','priority','isActive','endpoint','workspace','billing','providerOutputBound','credentialRef','expiresAt','skewMs','refresh']);
   const admitted=n.connections.find(x=>x.id===c.id);if(!admitted||c.provider!=='codex'||c.authType!=='oauth'||c.isActive!==true||c.endpoint!==n.deployment.endpoint||c.billing!==admitted.billing||c.credentialRef!==admitted.credentialRef||c.expiresAt!==admitted.expiresAt||c.skewMs!==admitted.skewMs||c.refresh!==false||c.providerOutputBound!==false||!(c.priority===null||Number.isSafeInteger(c.priority)))throw Error();
   keys(c.workspace,['workspaceId','chatgptAccountId']);if(!Object.values(c.workspace).every(v=>typeof v==='string'&&/^[a-zA-Z0-9_-]{1,100}$/.test(v)))throw Error();
  }
  if(!Array.isArray(g.routes)||g.routes.length!==1)throw Error();const r=g.routes[0];keys(r,['id','name','strategy','edges'],['id','name','strategy','edges']);
  if(r.id!==p.routeId||r.name!==p.routerModel||r.strategy!=='fallback'||r.edges.length!==1)throw Error();const edge=r.edges[0];keys(edge,['member','provider','model','connections'],['member','provider','model','connections']);
  if(!['cx/gpt-6-astra','codex/gpt-6-astra'].includes(edge.member)||edge.provider!=='codex'||edge.model!=='gpt-6-astra'||canonical(edge.connections)!==canonical(g.connections.map((c:any)=>c.id)))throw Error();
  return structuredClone(p);
 }catch{throw new Denial('policy_denied');}
}
export function assertNativeBinding(binding:Binding,p:NativeRouterPolicy){
 if(binding.role!==nativeConsumerRole(p.native)||canonical(binding.native)!==canonical({profileDigest:hashDocument(p.native),scopeId:p.native.scope.id,authorizationDigest:p.native.scope.authorizationDigest}))throw new Denial('policy_denied');
}
export function assertPlannerPacket(request:any,p:NativeRouterPolicy){
 if(p.native.protocol!==PLANNER_PROTOCOL)return;
 const state=plannerRequestState(request,p.native);
 if(hashDocument({...state.packet,policy:p})!==state.inputRevision)throw new Denial('policy_denied');
}
