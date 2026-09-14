// Offline evidence processes must flush their last record and observe pipe EOF.
export function writeJSON(value, stream = process.stdout) {
 return new Promise((resolve,reject)=>stream.write(JSON.stringify(value)+'\n',error=>error?reject(error):resolve()));
}
export function collectProcessOutput(child, timeoutMs = 45000) {
 return new Promise((resolve,reject)=>{
  const out=[],err=[];
  child.stdout.on('data',b=>out.push(Buffer.from(b)));
  child.stderr.on('data',b=>err.push(Buffer.from(b)));
  const timer=setTimeout(()=>{child.kill('SIGKILL');reject(Error('evidence_process_timeout'));},timeoutMs);
  child.once('error',error=>{clearTimeout(timer);reject(error);});
  // Exit can precede stdio EOF; close waits for all inherited output descriptors.
  child.once('close',(code,signal)=>{clearTimeout(timer);resolve({code,signal,stdout:Buffer.concat(out).toString('utf8'),stderr:Buffer.concat(err).toString('utf8')});});
 });
}
export function caseOutput(stdout) {
 const records=stdout.split('\n').filter(l=>l.startsWith('{')).map(line=>JSON.parse(line));
 if(!records.length)throw Error('evidence_record_missing');
 return records.at(-1);
}
