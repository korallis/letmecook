import { fixturePolicy, type Binding } from './types.ts';
import { hashDocument, type RouterPolicy } from './router-policy.ts';
export function routerPolicyFixture(native = false): RouterPolicy {
  const graph = {schema:1,profile:native ? '9router-0.5.75-synthetic-native-authority-v1' : '9router-0.5.75-synthetic-compatible-chat-v1',liveAdmission:false,nativeLiveAdmission:false,
    nodes:native ? [] : [{id:'openai-compatible-synthetic',prefix:'synthetic',baseUrl:'http://127.0.0.1:47771/v1',apiType:'chat'}],
    connections:native ? [{id:'account_1',provider:'codex',authType:'oauth',priority:1,isActive:true,endpoint:'http://127.0.0.1:47771/responses',refreshEndpoint:'http://127.0.0.1:47771/token',workspace:{chatgptAccountId:'synthetic'},billing:'synthetic-subscription',providerOutputBound:false}] : [{id:'account_1',provider:'openai-compatible-synthetic',authType:'apikey',priority:1,isActive:true,endpoint:'http://127.0.0.1:47771/v1/chat/completions',billing:'synthetic-api'}],
    routes:[{id:'coding',name:'gaffer-coding',strategy:'fallback',edges:[{member:native ? 'cx/gpt-6-astra' : 'openai-compatible-synthetic/synthetic-model',provider:native ? 'codex' : 'openai-compatible-synthetic',model:native ? 'gpt-6-astra' : 'synthetic-model',connections:['account_1']}]}],
    settings:{requireApiKey:true,comboStrategy:'fallback',accountStrategy:'fill-first',adapters:false,helpers:false,remoteResources:false,proxies:false,autoPing:false},
    bounds:{requestBytes:65536,responseBytes:1048576,requestMaxTokens:1024,providerOutputTokens:native ? null : 1024,ingressMs:3000,totalMs:30000,subattempts:16},
    terminals:['validated-original-chat-sse','validated-original-json-error',...(native ? ['validated-original-responses-sse','validated-original-refresh-json'] : [])]};
  return {schema:2,routerId:'synthetic_router',routeId:'coding',revision:'policy_1',epoch:1,routerModel:'gaffer-coding',profile:native ? 'router-native-chat-translation-synthetic-v1' : 'router-chat-text-tools-synthetic-v1',evidence:'synthetic',liveAdmission:false,graph,limits:fixturePolicy().limits,
    authority:{deploymentId:'synthetic_deployment',boot:'00000000-0000-0000-0000-000000000001',generation:9,revision:'policy_1',graphDigest:hashDocument(graph)},
    envelope:{consumer:'chat-read-file-v1',cap:'max_completion_tokens-to-max_tokens-v1',replacement:'read-only',sourceCommit:'17c4cc76877bd1755030a8414f8d0083f48dcccf',sourceLock:'1'.repeat(64),overlay:'2'.repeat(64),runtime:'3'.repeat(64)}};
}
export function routerBinding(p = routerPolicyFixture()): Binding {
  return {taskId:'task_1',attemptId:'attempt_1',grantId:'grant_1',leaseId:'lease_1',fence:1,role:'worker',routerId:p.routerId,routeId:p.routeId,revision:p.revision,epoch:p.epoch,expiresAt:Date.now()+10000,leaseExpiresAt:Date.now()+10000};
}
export function matchingReceipt(p: RouterPolicy,id: string,term = p.profile === 'router-native-chat-translation-synthetic-v1' ? 'provider_completed' : 'provider_terminal') {
  const edge = p.graph.routes[0].edges[0];
  return {id,known:true,quiescent:term !== 'unknown',boot:p.authority.boot,generation:p.authority.generation,revision:p.revision,route:p.routerModel,local_stop:'local_eof',handler_done:1,
    operations:[{request_id:id,ordinal:1,boot:p.authority.boot,generation:p.authority.generation,revision:p.revision,provider:edge.provider,model:edge.model,connection_id:edge.connections[0],terminal:term,local_stop:term === 'unknown' ? 'original_error' : 'original_eof'}]};
}
