import { readFileSync, existsSync, statSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { pathToFileURL, fileURLToPath } from 'node:url';
import { join, dirname } from 'node:path';
import { overlay } from './patches.mjs';
import { createRequire } from 'node:module';

if (!existsSync('/.dockerenv')) throw new Error('isolated_container_required');
const root = process.env.ROUTER_SOURCE || '/router-source';
const rootUrl = pathToFileURL(root + '/').href;
const extensionRoot = dirname(fileURLToPath(import.meta.url));
const manifest = JSON.parse(readFileSync(join(extensionRoot, 'source-lock.json'), 'utf8'));
// Refuse altered source before evaluating ANY upstream module.
for (const [path, expected] of Object.entries(manifest.files)) {
  const actual = createHash('sha256').update(readFileSync(join(root,path))).digest('hex');
  if (actual !== expected) throw new Error(`source_digest_mismatch:${path}`);
}
export async function resolve(specifier, context, next) {
  if (specifier.startsWith('@/')) specifier = pathToFileURL(join(root,'src',specifier.slice(2))).href;
  if (specifier.startsWith('open-sse/')) specifier = pathToFileURL(join(root,specifier)).href;
  let url;
  try { if (specifier.startsWith('.') || specifier.startsWith('/') || specifier.startsWith('file:')) url = new URL(specifier, context.parentURL || rootUrl); } catch {}
  if (url?.protocol === 'file:' && url.pathname.includes('/gaffer-extension/')) {
    return {url:pathToFileURL(join(extensionRoot,'overlay',url.pathname.split('/gaffer-extension/')[1])).href,shortCircuit:true};
  }
  if (context.parentURL?.includes('/overlay/') && specifier === '../src/lib/db/repos/settingsRepo.js') return {url: rootUrl+'src/lib/db/repos/settingsRepo.js',shortCircuit:true};
  if (url?.href.startsWith(rootUrl)) {
    for (const suffix of ['', '.js', '/index.js']) if (existsSync(fileURLToPath(url)+suffix) && statSync(fileURLToPath(url)+suffix).isFile()) {
      return {url:url.href+suffix,shortCircuit:true};
    }
  }
  try { return await next(specifier,context); }
  catch (error) {
    if (specifier.startsWith('.') || specifier.startsWith('/') || specifier.includes(':')) throw error;
    const require = createRequire('/opt/router-deps/package.json');
    return next(pathToFileURL(require.resolve(specifier)).href,context);
  }
}
export async function load(url, context, next) {
  if (url.startsWith(rootUrl) && /\.(m?js)$/.test(url)) {
    const path = url.slice(rootUrl.length);
    if (!manifest.files[path]) throw new Error(`unlisted_source:${path}`);
    const source = overlay(path,readFileSync(fileURLToPath(url),'utf8'));
    return {format:'module',source,shortCircuit:true};
  }
  return next(url,context);
}
