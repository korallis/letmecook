export const proof={mode:process.env.GAFFER_TEST_FAULT??'',triggered:0,committed:false};
export function faultAdapter(db){
 let operation=false;
 return new Proxy(db,{get(target,key){
  if(key==='run')return(sql,params)=>{
   if(proof.mode==='admission-write'&&sql.startsWith('INSERT INTO gaffer_native_admissions')){proof.triggered++;throw Error('injected_admission_persistence_failure');}
   if(proof.mode==='debit-write'&&sql.startsWith('INSERT INTO gaffer_scope_operations')){proof.triggered++;throw Error('injected_debit_persistence_failure');}
   if(proof.mode==='receipt-write'&&sql.startsWith('UPDATE gaffer_scope_operations SET output_digest')){proof.triggered++;throw Error('injected_original_receipt_persistence_failure');}
   const value=target.run(sql,params);if(sql.startsWith('INSERT INTO gaffer_operations'))operation=true;return value;
  };
  if(key==='transaction')return fn=>{const value=target.transaction(fn);if(operation&&proof.mode.startsWith('slow-')&&!proof.triggered){proof.triggered++;proof.committed=true;Atomics.wait(new Int32Array(new SharedArrayBuffer(4)),0,0,2000);}return value;};
  const value=target[key];return typeof value==='function'?value.bind(target):value;
 }});
}
