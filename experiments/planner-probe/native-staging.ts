import { mkdir,cp } from 'node:fs/promises';
import { join,dirname } from 'node:path';
export const nativeConsumerFiles=['planner.ts','native-consumer.ts','native-boundary-port.ts','native-transport.ts','transport.ts','reader.ts','public-fixture.ts','package.json','package-lock.json'].map(f=>'experiments/planner-probe/'+f)
 .concat(['json.ts','types.ts','protocol.ts','router-policy.ts','native-policy.ts','profiles.ts','profile-ids.ts'].map(f=>'experiments/inference-boundary/'+f),
 ['native-profile','native-planner','planner-plan-schema','native-responses','responses-terminal','scope-profile'].map(f=>'experiments/router-authority-extension/overlay/'+f+'.mjs'),
 ['tests/fixtures/planner/fixture.txt','tests/fixtures/planner/plan.schema.json']);
export async function stageNativePlanner(root:string,staging:string){
 for(const file of nativeConsumerFiles){await mkdir(join(staging,dirname(file)),{recursive:true});await cp(join(root,file),join(staging,file));}
 for(const pkg of ['ajv','fast-deep-equal','fast-uri','json-schema-traverse','require-from-string'])await cp(join(root,'experiments/planner-probe/node_modules',pkg),join(staging,'experiments/planner-probe/node_modules',pkg),{recursive:true});
}
