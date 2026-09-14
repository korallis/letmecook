// No provider traffic: a local self-signed TLS server proves that the gateway's
// fixed-origin dispatcher attempts TLS but refuses untrusted peers before HTTP.
import assert from 'node:assert/strict';
import { createServer } from 'node:tls';
import { readFileSync,mkdirSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { once } from 'node:events';
import { nativeDispatcher } from '../router-authority-extension/overlay/native-transport.mjs';
mkdirSync('/tmp/tls');execFileSync('openssl',['req','-x509','-newkey','rsa:2048','-nodes','-keyout','/tmp/tls/key','-out','/tmp/tls/cert','-days','1','-subj','/CN=chatgpt.com','-addext','subjectAltName=DNS:chatgpt.com'],{stdio:'ignore'});
const key=readFileSync('/tmp/tls/key'),cert=readFileSync('/tmp/tls/cert');let handshakes=0,httpBytes=0;
const server=createServer({key,cert},s=>{s.on('data',b=>httpBytes+=b.length);s.on('error',()=>{});});server.on('connection',()=>handshakes++);server.on('tlsClientError',()=>{});server.listen('/egress/provider.sock');await once(server,'listening');
assert.throws(()=>nativeDispatcher('/tmp/other.sock'),/mismatch/);
for(const name of ['NODE_TLS_REJECT_UNAUTHORIZED','NODE_EXTRA_CA_CERTS','SSL_CERT_FILE','SSL_CERT_DIR']){process.env[name]='0';assert.throws(()=>nativeDispatcher('/egress/provider.sock'),/environment_denied/);delete process.env[name];}
const dispatcher=nativeDispatcher('/egress/provider.sock');
for(const url of ['http://chatgpt.com/backend-api/codex/responses','https://chatgpt.com/backend-api/codex/responses?other=1','https://chatgpt.com/oauth/token','https://example.invalid/backend-api/codex/responses'])await assert.rejects(fetch(url,{method:'POST',body:'{}',dispatcher}),e=>e.cause?.message==='native_dispatch_origin_denied');
assert.equal(handshakes,0);
await assert.rejects(fetch('https://chatgpt.com/backend-api/codex/responses',{method:'POST',body:'{}',dispatcher}),e=>e.cause?.code==='DEPTH_ZERO_SELF_SIGNED_CERT');assert.equal(handshakes,1);assert.equal(httpBytes,0);
await dispatcher.destroy();await new Promise(r=>server.close(r));console.log(JSON.stringify({scenario:'transport',live:false,result:'passed',handshakes,httpBytes,originDenied:true,tlsOverridesDenied:true,untrustedCertificateDenied:true}));
