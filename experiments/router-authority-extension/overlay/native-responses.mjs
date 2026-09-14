// The one reviewed consumer subset. Never forwards a tool event provisionally.
import { createResponsesTerminalObserver, parseUnambiguousJSON } from './responses-terminal.mjs';
import { canonical, safePath } from './native-profile.mjs';
import { PLANNER_PROTOCOL, plannerContinuation, validatePlannerRequest, validatePlannerOutput } from './native-planner.mjs';
const check=(value,why='unsupported_native_request')=>{if(!value)throw Error(why);};
const exact=(value,names,optional=[])=>check(value&&typeof value==='object'&&!Array.isArray(value)&&names.every(k=>Object.hasOwn(value,k))&&Object.keys(value).every(k=>[...names,...optional].includes(k)));
const text=x=>typeof x==='string'&&Buffer.byteLength(x)<=262144;
const identity=x=>typeof x==='string'&&/^[a-zA-Z0-9_-]{1,128}$/.test(x);
export function validatePatch(argumentsText,profile){
 check(text(argumentsText));const value=parseUnambiguousJSON(argumentsText);exact(value,['patchText']);check(text(value.patchText));
 const lines=value.patchText.split('\n');check(lines[0]==='*** Begin Patch'&&lines.at(-1)==='*** End Patch','invalid_patch_envelope');
 let files=0,hasFile=false;
 for(const line of lines.slice(1,-1)){
   if(line.startsWith('*** ')){
     const match=/^\*\*\* (?:Update File|Add File|Delete File|Move to): (.+)$/.exec(line);
     if(line==='*** End of File'){check(hasFile);continue;}
     check(match&&safePath(match[1])&&profile.toolPaths.includes(match[1]),'unauthorized_patch_path');hasFile=true;files++;
   }else check(hasFile&&(/^[ +\-]/.test(line)||line.startsWith('@@')||line===''),'invalid_patch_line');
 }
 check(files>0&&files<=64,'invalid_patch_files');return value;
}
export function continuationOutput(output,profile){
 if(profile.protocol===PLANNER_PROTOCOL)return plannerContinuation(output);
 check(Array.isArray(output)&&output.length>0&&output.length<=16,'invalid_native_output');
 const ids=new Set(),calls=new Set();
 return output.map(item=>{
  check(identity(item.id)&&!ids.has(item.id),'duplicate_output_identity');ids.add(item.id);
  if(item.type==='reasoning'){
   exact(item,['id','type','summary','encrypted_content'],['status']);check(text(item.encrypted_content)&&item.encrypted_content.length>0&&Array.isArray(item.summary)&&item.summary.length<=16);
   for(const s of item.summary){exact(s,['type','text']);check(s.type==='summary_text'&&text(s.text));}
   return {type:item.type,encrypted_content:item.encrypted_content,summary:item.summary};
  }
  if(item.type==='function_call'){
   exact(item,['id','type','call_id','name','arguments','status']);check(item.status==='completed'&&item.name==='apply_patch'&&identity(item.call_id)&&!calls.has(item.call_id));calls.add(item.call_id);validatePatch(item.arguments,profile);
   return {type:item.type,call_id:item.call_id,name:item.name,arguments:item.arguments};
  }
  exact(item,['id','type','role','status','content']);check(item.type==='message'&&item.role==='assistant'&&item.status==='completed'&&Array.isArray(item.content)&&item.content.length>0);
  for(const part of item.content){exact(part,['type','text','annotations'],['logprobs']);check(part.type==='output_text'&&text(part.text)&&Array.isArray(part.annotations)&&part.annotations.length===0&&(!part.logprobs||Array.isArray(part.logprobs)&&part.logprobs.length===0),'refusal_or_unsupported_content');}
  return {role:'assistant',content:item.content.map(p=>({type:'output_text',text:p.text}))};
 });
}
export function validateNativeRequest(body,profile,model,previous=null){
 if(profile.protocol===PLANNER_PROTOCOL)return validatePlannerRequest(body,profile,model,previous);
 exact(body,['model','input','stream','store','include','reasoning','tools','tool_choice','prompt_cache_key']);
 check(body.model===model&&body.stream===true&&body.store===false&&body.tool_choice==='auto');
 check(canonical(body.reasoning)===canonical({effort:'xhigh',summary:'auto'})&&canonical(body.include)===canonical(['reasoning.encrypted_content']),'exact_reasoning_required');
 check(canonical(body.tools)===canonical(profile.tools),'native_tool_profile_mismatch');
 check(typeof body.prompt_cache_key==='string'&&/^ses_[a-zA-Z0-9]{1,80}$/.test(body.prompt_cache_key));
 check(Array.isArray(body.input)&&body.input.length>1&&body.input.length<=128);
 if(previous){
   check(body.prompt_cache_key===previous.request.prompt_cache_key,'session_identity_drift');
   const normalized=continuationOutput(previous.output,profile),calls=normalized.filter(x=>x.type==='function_call');check(calls.length>0,'native_case_already_completed');
   const prefix=[...previous.request.input,...normalized];check(body.input.length===prefix.length+calls.length&&canonical(body.input.slice(0,prefix.length))===canonical(prefix),'native_continuation_drift');
   body.input.slice(prefix.length).forEach((result,i)=>{exact(result,['type','call_id','output']);check(result.type==='function_call_output'&&result.call_id===calls[i].call_id&&text(result.output)&&result.output.length>0,'native_tool_result_mismatch');});
 }else{
   for(const [i,item] of body.input.entries()){
     exact(item,['role','content']);check(item.role===(i===0?'developer':'user'));
     if(typeof item.content==='string')check(text(item.content)&&item.content.length>0);
     else {check(Array.isArray(item.content)&&item.content.length>0&&item.content.length<=16);for(const part of item.content){exact(part,['type','text']);check(part.type==='input_text'&&text(part.text)&&part.text.length>0);}}
   }
 }
 check(Buffer.byteLength(canonical(body))<=profile.local.requestBytes,'native_request_bytes');return structuredClone(body);
}
// Reviewed stock transformations only: preserve the actual stateless input,
// omit strict:false from tools, inject the pinned Codex default instructions.
export function expectedPhysicalRequest(ingress,model,instructions){
 const b=structuredClone(ingress);b.model=model;
 b.tools=b.tools.map(({strict,...tool})=>tool);b.instructions=instructions;return b;
}
export class NativeResponsesStream {
 constructor(profile,request=null){this.profile=profile;this.request=request;if(profile.protocol===PLANNER_PROTOCOL)check(request,'planner_request_required');this.observer=createResponsesTerminalObserver({maxBytes:profile.local.responseBytes,maxOutputItems:16});this.chunks=[];this.semanticOutput=false;this.nativeOutput=null;this.semanticBuffer='';this.semanticDecoder=new TextDecoder('utf-8',{fatal:true});}
 push(bytes){
   const state=this.observer.push(bytes);this.chunks.push(Buffer.from(bytes));let semantic=false;
   if(!state.invalidReason){
     let decoded=(this.pendingCR?'\r':'')+this.semanticDecoder.decode(bytes,{stream:true});this.pendingCR=decoded.endsWith('\r');if(this.pendingCR)decoded=decoded.slice(0,-1);this.semanticBuffer+=decoded.replace(/\r\n|\r/g,'\n');let end;
     while((end=this.semanticBuffer.indexOf('\n\n'))>=0){const block=this.semanticBuffer.slice(0,end);this.semanticBuffer=this.semanticBuffer.slice(end+2);const data=block.split('\n').filter(l=>l.startsWith('data:')).map(l=>l.slice(5).replace(/^ /,'')).join('\n');if(!data)continue;if(data==='[DONE]'){check(this.completedEvent&&!this.doneSentinel,'invalid_native_done');this.doneSentinel=true;continue;}const event=parseUnambiguousJSON(data);if(event.type==='response.completed')this.completedEvent=true;semantic ||= ['response.output_text.delta','response.function_call_arguments.delta','response.reasoning_summary_text.delta','response.reasoning_text.delta'].includes(event.type)&&typeof event.delta==='string'&&event.delta.length>0;}
   }
   this.semanticOutput ||= semantic;return {output:[],semantic,...(state.invalidReason?{error:Error(state.invalidReason)}:{})};
 }
 end(){
   const state=this.observer.finish({reason:'eof'});check(state.disposition==='provider_terminal'&&state.terminal.kind==='completed','native_original_success_required');
   const raw=Buffer.concat(this.chunks).toString('utf8');const frames=raw.replace(/\r\n|\r/g,'\n').split('\n\n');let complete;
   for(const frame of frames){const data=frame.split('\n').filter(l=>l.startsWith('data:')).map(l=>l.slice(5).replace(/^ /,'')).join('\n');if(!data||data==='[DONE]')continue;const event=parseUnambiguousJSON(data);if(event.type==='response.completed')complete=event.response;}
   check(complete&&complete.model==='gpt-6-astra'&&complete.status==='completed'&&complete.error===null&&complete.incomplete_details===null,'native_terminal_mismatch');
   validateNativeOutput(complete.output,this.profile,this.request);this.nativeOutput=structuredClone(complete.output);
   // Byte-for-byte frames are bounded and schema checked. The caller releases
   // this entire sequence only after durable original-receipt acceptance.
   return [raw];
 }
}

export function validateNativeOutput(output,profile,request){return profile.protocol===PLANNER_PROTOCOL?validatePlannerOutput(output,profile,request):continuationOutput(output,profile);}
