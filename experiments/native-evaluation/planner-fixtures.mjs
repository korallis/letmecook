// Synthetic output only, selected by the closed planner descriptor. No control,
// provider access, source reads or policy mutation happens in this module.
import { events } from './fixtures.mjs';
import { plannerRequestState } from '../router-authority-extension/overlay/native-planner.mjs';
export function plannerProposal(revision,read=false){const refs=read?['capture','fixture.txt']:['capture'];return {schema:1,kind:'plan_proposal',input_revision:revision,outcome:'Correct the greeting spelling.',constraints:['Preserve other text.'],exclusions:['Execution and delivery require separate authority.'],criteria:[{id:'c1',statement:'The greeting spells world correctly.',evidence_required:'Review the proposed one-word diff.'}],allowed_paths:['fixture.txt'],allowed_systems:[],assumptions:[],unresolved_questions:[],steps:[{id:'s1',description:'Propose replacing wrld with world in the greeting.',criterion_ids:['c1'],source_refs:refs}],assessment:{work_class:'text_edit',consequence:'low',unknowns:[],source_refs:refs}};}
export function plannerEvents(body,profile,scenario='read'){
 const state=plannerRequestState(body,profile),repair=['repair','empty','invalid-repair','repair-tool','stale'].includes(scenario);
 const call=(state.phase==='assessment'&&!repair&&scenario!=='direct')||scenario==='repair-tool'&&state.phase==='repair'||scenario==='disabled-call'&&state.phase!=='assessment';
 if(call){
  const out=events(true);
  for(const e of out){if(e.item?.type==='function_call'){e.item.name=scenario==='worker-tool'?'apply_patch':'read_file';if(e.item.arguments)e.item.arguments='{"path":"fixture.txt"}';}
   if(e.type==='response.function_call_arguments.delta')e.delta='{"path":"fixture.txt"}';if(e.type==='response.function_call_arguments.done')e.arguments='{"path":"fixture.txt"}';
   for(const item of e.response?.output??[])if(item.type==='function_call'){item.name=scenario==='worker-tool'?'apply_patch':'read_file';item.arguments='{"path":"fixture.txt"}';}}
  return out;
 }
 let text=JSON.stringify(plannerProposal(state.inputRevision,state.fileRead));
 if((repair&&state.phase==='assessment')||(scenario==='read-repair'&&state.phase==='final'))text=scenario==='empty'?'':scenario==='stale'?JSON.stringify(plannerProposal('0'.repeat(64),state.fileRead)):'invalid plan';
 if(scenario==='invalid-repair'&&state.phase==='repair')text='still invalid';
 return JSON.parse(JSON.stringify(events(false)).replaceAll('Completed 🌍 café.',text.replace(/\\/g,'\\\\').replace(/"/g,'\\"').replace(/\n/g,'\\n')));
}
