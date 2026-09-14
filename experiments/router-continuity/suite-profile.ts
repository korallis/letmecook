import assert from 'node:assert/strict';
import { validateNativeProfile,validateNativeRegistry,digest } from '../router-authority-extension/overlay/native-profile.mjs';
import { plannerProfile } from '../planner-probe/native-profile.ts';
import { plannerSettingsDigest } from '../router-authority-extension/overlay/native-planner.mjs';
import { settingsDigest } from '../harness/native/client.ts';
import { INITIAL_SUITE,SUITE_TIMES } from '../router-authority-extension/overlay/initial-suite.mjs';
export function suiteProfiles(common:any){
 const base=validateNativeProfile(common);assert.equal(base.schema,1);assert.equal(base.scope.phase,'initial');
 const worker:any={...structuredClone(base),schema:2,timing:{variant:INITIAL_SUITE,consumer:'worker'}};worker.harness.settings=settingsDigest('allow');
 const planner:any={...plannerProfile(base),schema:2,timing:{variant:INITIAL_SUITE,consumer:'planner'}};
 const continuity:any={...structuredClone(base),schema:2,timing:{variant:INITIAL_SUITE,consumer:'continuity'}};continuity.harness.settings=settingsDigest('ask');
 for(const p of [worker,planner,continuity]){const t=SUITE_TIMES[p.timing.consumer as keyof typeof SUITE_TIMES];for(const k of ['totalMs','firstOutputMs','idleMs'] as const)p.local[k]=t[k];}
 planner.planner.settings=plannerSettingsDigest(planner);return validateNativeRegistry([worker,planner,continuity]);
}
export function suiteManifest(input:any,profiles:any[]){
 assert.deepEqual(Object.keys(input).sort(),['schema','scopeId','stages','variant']);assert.equal(input.schema,1);assert.equal(input.variant,INITIAL_SUITE);
 assert.deepEqual(Object.keys(input.stages).sort(),['continuity','planner','worker']);
 for(const [consumer,files]of Object.entries(input.stages)as [string,Record<string,string>][]){assert(Object.keys(files).length>0&&Object.keys(files).length<3000);for(const [path,hash]of Object.entries(files)){assert(/^[a-zA-Z0-9_./@-]+$/.test(path)&&!path.startsWith('/')&&!path.split('/').some(x=>['..','.',''].includes(x)));assert(/^[a-f0-9]{64}$/.test(hash));}if(consumer!=='planner')assert.equal(files.opencode,'01edb5839aa10d5b09133fedcb335a062ecad6e82552933bb14f71756f2b296b');}
 const selected=validateNativeRegistry(profiles);assert.equal(selected.length,3);const resolved:any={};for(const name of ['worker','planner','continuity']){const p=selected.filter(p=>p.schema===2&&p.timing?.variant===INITIAL_SUITE&&p.timing.consumer===name);assert.equal(p.length,1);assert.equal(p[0].scope.id,input.scopeId);resolved[name]=digest(p[0]);}
 assert.equal(selected.find(p=>p.timing?.consumer==='worker')?.harness?.settings,settingsDigest('allow'));assert.equal(selected.find(p=>p.timing?.consumer==='continuity')?.harness?.settings,settingsDigest('ask'));
 return {...structuredClone(input),profiles:resolved,sessions:SUITE_TIMES,acknowledgementMarginMs:15000,stopMarginMs:15000};
}
