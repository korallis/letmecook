export function overlay(path, text) {
  const replace = (from, to) => {
    if (text.split(from).length !== 2) throw new Error(`overlay_anchor_mismatch:${path}`);
    text = text.replace(from, to);
  };
  if (path === 'open-sse/shared/machineId.js') replace('import { machineIdSync } from "node-machine-id";', 'import machineIdPackage from "node-machine-id";\nconst { machineIdSync } = machineIdPackage;');
  if (path === 'open-sse/providers/registry/codex.js' && process.env.GAFFER_SYNTHETIC_NATIVE === '1') {
    replace('baseUrl: "https://chatgpt.com/backend-api/codex/responses"', 'baseUrl: "http://127.0.0.1:47771/responses"');
    replace('tokenUrl: "https://auth.openai.com/oauth/token"', 'tokenUrl: "http://127.0.0.1:47771/token"');
  }
  if (path === 'open-sse/executors/codex.js') {
    text = `import { authority } from '../../gaffer-extension/authority.mjs';\n${text}`;
    replace('  async execute(args) {', '  async execute(args) {\n    authority().assertCurrent();');
    replace('    while (true) {\n      const result', '    while (true) {\n      authority().assertCurrent();\n      const result');
    replace('      await new Promise(r => setTimeout(r, delayMs));', '      await authority().delay(delayMs);');
  }
  if (path === 'open-sse/services/oauthCredentialManager.js') {
    text = `import { authority } from '../../gaffer-extension/authority.mjs';\n${text}`;
    replace('export async function refreshProviderCredentials(provider, credentials, log) {', `export async function refreshProviderCredentials(provider, credentials, log) {
  return authority().refresh(provider, credentials, () => refreshProviderCredentialsUnfenced(provider, credentials, log));
}
async function refreshProviderCredentialsUnfenced(provider, credentials, log) {`);
  }
  if (path === 'src/sse/services/tokenRefresh.js') {
    text = `import { authority } from '../../../gaffer-extension/authority.mjs';\n${text}`;
    replace('export async function updateProviderCredentials(connectionId, newCredentials) {', `export async function updateProviderCredentials(connectionId, newCredentials) {
  return authority().credentialUpdate(connectionId, () => updateProviderCredentialsUnfenced(connectionId, newCredentials));
}
async function updateProviderCredentialsUnfenced(connectionId, newCredentials) {`);
  }
  if (path === 'src/lib/db/driver.js') {
    text = `import { installAuthority } from '../../../gaffer-extension/authority.mjs';\n${text}`;
    replace('  return adapter;\n}', '  return installAuthority(adapter);\n}');
  }
  if (path === 'src/sse/handlers/chat.js') {
    text = `import { authority } from '../../../gaffer-extension/authority.mjs';\n${text}`;
    replace('export async function handleChat(request, clientRawRequest = null) {', `export async function handleChat(request, clientRawRequest = null) {
  await getSettings();
  try { return await authority().admission(request, admittedRequest => handleChatUnfenced(admittedRequest), clientRawRequest); }
  catch { return new Response(JSON.stringify({error:{message:'authority_denied'}}), {status:503,headers:{'content-type':'application/json'}}); }
}
async function handleChatUnfenced(request, clientRawRequest = null) {`);
    replace('  while (true) {\n    const credentials', '  while (true) {\n    authority().assertCurrent();\n    const credentials');
  }
  if (path === 'open-sse/executors/base.js') {
    text = `import { authority } from '../../gaffer-extension/authority.mjs';\n${text}`;
    replace('  async execute({ model, body, stream, credentials, signal, log, proxyOptions = null }) {', `  async execute(args) { return authority().executor(this.provider, args, () => this.executeUnfenced(args)); }
  async executeUnfenced({ model, body, stream, credentials, signal, log, proxyOptions = null }) {`);
    replace('      await new Promise(resolve => setTimeout(resolve, waitMs));', '      await authority().delay(waitMs);');
    replace('    for (let urlIndex = 0; urlIndex < fallbackCount; urlIndex++) {', '    for (let urlIndex = 0; urlIndex < fallbackCount; urlIndex++) {\n      authority().assertCurrent();');
  }
  if (path === 'open-sse/utils/proxyFetch.js') {
    text = `import { authority } from '../../gaffer-extension/authority.mjs';\n${text}`;
    replace('export async function proxyAwareFetch(url, options = {}, proxyOptions = null) {', `export async function proxyAwareFetch(url, options = {}, proxyOptions = null) {
  return authority().fetch(proxyAwareFetchUnfenced, url, options, proxyOptions);
}
async function proxyAwareFetchUnfenced(url, options = {}, proxyOptions = null) {`);
  }
  return text;
}
