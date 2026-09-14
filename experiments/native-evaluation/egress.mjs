import { isIPv4 } from 'node:net';
export function reviewedAddress(address,synthetic=false){
 if(typeof address!=='string'||!isIPv4(address))throw Error('reviewed_ipv4_required');
 const [a,b]=address.split('.').map(Number);
 if(!synthetic&&(a===0||a===10||a===127||a>=224||a===169&&b===254||a===172&&b>=16&&b<=31||a===192&&b===168||a===100&&b>=64&&b<=127||a===198&&[18,19,51].includes(b)||a===192&&[0,2].includes(b)||a===203&&b===0))throw Error('nonpublic_provider_address');
 return address;
}
export function nftRules(address,synthetic=false){
 const ip=reviewedAddress(address,synthetic);
 return `table inet gaffer_native {\n chain inbound { type filter hook input priority -150; policy drop; ct state established,related accept; }\n chain outbound { type filter hook output priority -150; policy drop; ip daddr ${ip} tcp dport 443 ct state new,established accept; }\n chain forwarded { type filter hook forward priority -150; policy drop; }\n}\n`;
}
// Validate exact effective filter rules, ignoring only kernel-assigned handles.
// Docker may also install NAT tables; a drop in this earlier filter is final.
export function verifyRuleset(value,address,synthetic=false){
 const ip=reviewedAddress(address,synthetic),actual=value?.nftables?.filter(x=>x.chain?.table==='gaffer_native'||x.rule?.table==='gaffer_native').map(x=>structuredClone(x));
 if(!Array.isArray(actual))throw Error('kernel_rules_missing');
 for(const item of actual)delete (item.chain??item.rule).handle;
 const chain=(name,hook)=>({chain:{family:'inet',table:'gaffer_native',name,type:'filter',hook,prio:-150,policy:'drop'}});
 const match=(left,right,op='==')=>({match:{op,left,right}}),ct=states=>match({ct:{key:'state'}},states,'in');
 const rule=(name,expr)=>({rule:{family:'inet',table:'gaffer_native',chain:name,expr}});
 const expected=[chain('inbound','input'),chain('outbound','output'),chain('forwarded','forward'),rule('inbound',[ct(['established','related']),{accept:null}]),rule('outbound',[match({payload:{protocol:'ip',field:'daddr'}},ip),match({payload:{protocol:'tcp',field:'dport'}},443),ct(['established','new']),{accept:null}])];
 const canonical=v=>JSON.stringify(v,(_,x)=>x&&typeof x==='object'&&!Array.isArray(x)?Object.fromEntries(Object.keys(x).sort().map(k=>[k,x[k]])):x);
 if(canonical(actual)!==canonical(expected))throw Error('kernel_rules_mismatch');return true;
}
