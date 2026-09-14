import assert from 'node:assert/strict';
import { existsSync } from 'node:fs';
assert.ok(existsSync('/.dockerenv'));
const {getAdapter}=await import('/router-source/src/lib/db/driver.js');
const repo=await import('/router-source/src/lib/db/index.js');
const {authority,project,canonical}=await import('/router-source/gaffer-extension/authority.mjs');
await getAdapter(); const a=authority();
const provider='openai-compatible-synthetic'; const baseUrl='http://127.0.0.1:47770/v1';
const config={settings:{requireApiKey:true,rtkEnabled:false,headroomEnabled:false,pxpipeEnabled:false,cavemanEnabled:false,ponytailEnabled:false,ccFilterNaming:false,capacityAdapter:Object.fromEntries(['vision','pdf','audioInput','videoInput'].map(k=>[k,{enabled:false,models:[]}]))},providerNodes:[{id:provider,type:'openai-compatible',name:'synthetic',prefix:provider,baseUrl,apiType:'chat'}],providerConnections:[{id:'synthetic_conn',provider,authType:'apikey',name:'synthetic',priority:1,isActive:true,apiKey:'synthetic_key',providerSpecificData:{baseUrl,apiType:'chat'}}],apiKeys:[{id:'synthetic_ingress',key:'synthetic_gateway_key',name:'synthetic',isActive:true}],combos:[{id:'synthetic_route',name:'gaffer-synthetic',models:[`${provider}/synthetic-model`]}]};
assert.equal(await a.replace(a.state().generation,'revision_1',project(config),()=>repo.importDb(config)),true);
const before=canonical(a.snapshot());const aDb=await getAdapter();
const keys=['ordinary_key','__proto__','constructor','prototype','toString','hasOwnProperty'];
const scopes=['modelAliases','pricing','mitmAlias','customModels'];
const statements=[];
const hiddenModel=`${provider}/other-model`;
const pricingValue={[provider]:{'synthetic-model':{input:999,output:888}}};
const setters={
  modelAliases:key=>repo.setModelAlias(key,{hidden_alias:hiddenModel}),
  pricing:key=>repo.updatePricing(Object.fromEntries([[key,pricingValue]])),
  mitmAlias:key=>repo.setMitmAliasAll(key,{hidden_alias:hiddenModel}),
  customModels:key=>repo.addCustomModel({providerAlias:key,id:'hidden_model',type:'llm'})
};
const {resolveModelAlias}=await import('/router-source/src/sse/services/model.js');
const assertUnchanged=async()=>{
  assert.deepEqual((await getAdapter()).all('SELECT scope,key FROM kv'),[]);
  assert.equal(a.state().phase,'active');assert.equal(canonical(a.snapshot()),before);
  assert.deepEqual(await repo.getModelAliases(),{});
  assert.equal(await resolveModelAlias('hidden_alias'),null);
  assert.equal(await repo.getPricingForModel(provider,'synthetic-model'),null);
  assert.deepEqual(await repo.getMitmAlias(),{});assert.deepEqual(await repo.getCustomModels(),[]);
};
await assertUnchanged();
for(const scope of scopes)for(const key of keys){
  await assert.rejects(()=>setters[scope](key),/unsupported_graph/);
  await assertUnchanged();
  // The raw guarded adapter must also see the exact reserved key, including
  // customModels whose repository normally constructs a composite key.
  assert.throws(()=>(aDb.run('INSERT INTO kv(scope,key,value) VALUES(?,?,?)',[scope,key,JSON.stringify(pricingValue)])),/unsupported_graph/);
  await assertUnchanged();statements.push({scope,key,repositoryRejected:true,rawRowRejected:true});
}
await assert.rejects(()=>repo.disableModels(provider,['synthetic-model']),/unsupported_kv_scope/);
await assertUnchanged();
await repo.updateProviderConnection('synthetic_conn',{testStatus:'active',lastUsedAt:new Date().toISOString(),modelLock_synthetic_model:null,backoffLevel:0});
await assertUnchanged();
const failedReadbacks=[];
for(const scope of scopes){
  // A privileged writer may stage unsupported data, but cannot reopen admission.
  await assert.rejects(()=>a.replace(a.state().generation,`rejected_${scope}`,project(config),()=>aDb.run('INSERT INTO kv(scope,key,value) VALUES(?,?,?)',[scope,'__proto__',JSON.stringify(pricingValue)])),/unsupported_graph/);
  assert.equal(a.state().phase,'closed');assert.throws(()=>a.snapshot(),/unsupported_graph/);
  assert.deepEqual(aDb.all('SELECT scope,key FROM kv').map(row=>({...row})),[{scope,key:'__proto__'}]);
  failedReadbacks.push({scope,key:'__proto__',admission:'closed'});
  assert.equal(await a.replace(a.state().generation,`recovered_${scope}`,project(config),()=>repo.importDb(config)),true);
  await assertUnchanged();
}
console.log(JSON.stringify({scenario:'policy-reserved-keys',regressionOf:'4294e66649a981c9d227d8e1323ac9eaee4efe85',severity:'P2-policy-projection',unapprovedInferenceDemonstrated:false,statements,failedReadbacks,aliasResolution:null,effectivePricing:null,healthWriteAllowed:true,unknownScopeRejected:true}));
process.exit(0);
