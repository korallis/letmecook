import { spawn } from 'node:child_process';
// Cancelling this CLI can leave an uncertain Docker start. The caller retains
// the deterministic container name and reconciles/removes it after this settles.
export async function inputProcess(command:string,args:string[],input:string,signal:AbortSignal):Promise<string> {
 signal.throwIfAborted();
 return new Promise((resolve,reject)=>{
  const child=spawn(command,args,{stdio:['pipe','pipe','pipe']});let out='',err='';let overflow=false;
  let killTimer:ReturnType<typeof setTimeout>|undefined;
  const abort=()=>{child.kill('SIGTERM');killTimer ??= setTimeout(()=>child.kill('SIGKILL'),500);};
  signal.addEventListener('abort',abort,{once:true});if(signal.aborted)abort();
  const timer=setTimeout(abort,12000);
  child.stdout.on('data',b=>{out+=b;if(out.length>1048576){overflow=true;abort();}});
  child.stderr.on('data',b=>{err+=b;if(err.length>65536)err=err.slice(-65536);});
  child.stdin.on('error',()=>{});
  child.once('error',error=>{clearTimeout(timer);clearTimeout(killTimer);signal.removeEventListener('abort',abort);reject(error);});
  child.once('exit',code=>{clearTimeout(timer);clearTimeout(killTimer);signal.removeEventListener('abort',abort);if(code===0&&!signal.aborted&&!overflow)resolve(out);else reject(new Error(err||'input_process_failed'));});
  child.stdin.end(input);
 });
}
