// Trusted evidence watcher: 'unknown/running' is an ordinary in-flight original.
export function endedUnknown(evidence:any,bindingDigest:string,profileDigest:string,packetDigest:string){
 if(evidence.packetDigest!==packetDigest)throw Error('suite_evidence_packet');
 return evidence.receipts.some((r:any)=>r.known===true&&r.native?.bindingDigest===bindingDigest&&r.native.profileDigest===profileDigest&&r.operations.some((o:any)=>o.terminal==='unknown'&&['original_eof','original_error','original_cancel','transport_error','crash_unknown'].includes(o.local_stop)));
}
export function joinedPhysical(phases:any[],before:any,after:any){
 for(const k of ['id','boot','started','deadline','spec'])if(before[k]!==after[k])throw Error('suite_scope_changed');
 const seen=new Set<string>();for(const phase of phases){if(!phase.qualified)throw Error('suite_unqualified_phase');for(const receipt of phase.evidence.receipts)for(const op of receipt.operations){const key=receipt.id+':'+op.ordinal;if(seen.has(key)||op.request_id!==receipt.id||op.scope_id!==before.id||op.provider!=='codex'||op.model!=='gpt-6-astra'||op.terminal==='unknown'||op.local_stop!=='original_eof')throw Error('suite_physical_evidence');seen.add(key);}}
 if(after.spent-before.spent!==seen.size||after.spent>10||after.spent<before.spent)throw Error('suite_physical_debit_mismatch');return seen.size;
}
