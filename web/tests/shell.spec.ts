import AxeBuilder from '@axe-core/playwright';
import { expect, test, type Page } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { startDaemon } from './daemon.ts';

let base = '';
let stop = async () => {};
const shots = 'reports/screenshots';

test.beforeAll(async () => {
  mkdirSync(shots, { recursive: true });
  ({ base, stop } = await startDaemon());
});
test.afterAll(async () => stop());

const axe = async (page: Page) => {
  const r = await new AxeBuilder({ page }).analyze();
  expect(r.violations.filter(v => v.impact === 'serious' || v.impact === 'critical'), JSON.stringify(r.violations, null, 1)).toEqual([]);
  return r;
};

const status = (page: Page) => page.getByRole('status');

test('renders the real fixture snapshot with honest fixture-only labels', async ({ page }) => {
  await page.goto(base + '/');
  await expect(status(page)).toHaveText(/^Read: bounded fixture snapshot decoded$/);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Gaffer');
  await expect(page.getByText('Fixture-only read view')).toBeVisible();
  await expect(page.getByRole('definition').filter({ hasText: 'fixture-only' }).first()).toBeVisible();
  const api = await (await page.request.get(base + '/api/v1/snapshot?limit=50')).json();
  const tasks = page.getByRole('region', { name: /^Tasks/ }).getByRole('row');
  await expect(tasks).toHaveCount(api.tasks.length + 1);
  await expect(page.getByRole('cell', { name: api.tasks[0].task_id })).toBeVisible();
  await expect(page.getByRole('row').filter({ hasText: api.tasks[0].task_id }).getByRole('cell')).toHaveText([api.tasks[0].task_id, 'reconciling', 'unknown', '1', String(api.tasks[0].attempt.revision), 'stop', 'not_started', 'unknown', 'yes']);
  const events = page.getByRole('region', { name: /^Events/ }).getByRole('row');
  await expect(events).toHaveCount(api.events.length + 1);
  await expect(page.getByRole('cell', { name: 'assign', exact: true })).toBeVisible();
  await expect(page.getByRole('cell', { name: 'transition', exact: true })).toBeVisible();
  for (const c of api.missing_capabilities) await expect(page.getByRole('listitem').filter({ hasText: new RegExp(`^${c}$`) })).toBeVisible();
  const text = await page.locator('body').innerText();
  for (const forbidden of [/saved/i, /acknowledged/i, /executing/i, /review passed/i, /accepted/i, /sign in/i, /log ?in/i, /device/i, /approve/i, /account/i, /offline/i, /stop task/i]) {
    expect(text, forbidden.source).not.toMatch(forbidden);
  }
  await expect(page.getByRole('button')).toHaveCount(1);
  await expect(page.getByRole('button', { name: 'Refresh snapshot' })).toBeVisible();
  await expect(page.locator('form, input, select, textarea, a[href]')).toHaveCount(0);
  await axe(page);
});

test('refresh re-reads and every failure state is a named status word', async ({ page }) => {
  await page.goto(base + '/');
  await expect(status(page)).toHaveText(/^Read/);
  // Malformed: valid JSON that fails the closed schema.
  await page.route('**/api/v1/snapshot*', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ version: 'read-provisional-v1', mode: 'production', tasks: [], events: [] }) }));
  await page.getByRole('button', { name: 'Refresh snapshot' }).click();
  await expect(status(page)).toHaveText('Malformed: response refused by schema check (HTTP 200)');
  await expect(page.getByRole('table')).toHaveCount(0);
  // Server error with the daemon's own error shape.
  await page.route('**/api/v1/snapshot*', route => route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ version: 'read-provisional-v1', error: 'store_unavailable', mode: 'fixture-only', missing_capabilities: ['execution', 'inference', 'artifact_custody', 'result_ack', 'acceptance', 'publication', 'merge', 'state_import', 'sessions'] }) }));
  await page.getByRole('button', { name: 'Refresh snapshot' }).click();
  await expect(status(page)).toHaveText('Server error: daemon refused the read (HTTP 503, code store_unavailable)');
  // Non-200 without the daemon error shape is malformed, not trusted.
  await page.route('**/api/v1/snapshot*', route => route.fulfill({ status: 500, contentType: 'text/html', body: '<h1>proxy</h1>' }));
  await page.getByRole('button', { name: 'Refresh snapshot' }).click();
  await expect(status(page)).toHaveText('Malformed: response refused by schema check (HTTP 500)');
  // Empty: schema-valid snapshot with nothing in it.
  const real = await (await page.request.get(base + '/api/v1/snapshot')).json();
  await page.route('**/api/v1/snapshot*', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ...real, tasks: [], events: [] }) }));
  await page.getByRole('button', { name: 'Refresh snapshot' }).click();
  await expect(status(page)).toHaveText('Empty: snapshot decoded with no tasks or events');
  await expect(page.getByRole('region', { name: /^Tasks \(0/ })).toBeVisible();
  // Unavailable: network failure before any response.
  await page.route('**/api/v1/snapshot*', route => route.abort('connectionrefused'));
  await page.getByRole('button', { name: 'Refresh snapshot' }).click();
  await expect(status(page)).toHaveText('Unavailable: request failed before a response');
  // Loading then unknown: no response within the budget.
  await page.route('**/api/v1/snapshot*', () => { /* never fulfil */ });
  await page.getByRole('button', { name: 'Refresh snapshot' }).click();
  await expect(status(page)).toHaveText('Loading: snapshot request in flight');
  await expect(page.getByRole('button', { name: 'Refresh snapshot' })).toBeDisabled();
  await expect(status(page)).toHaveText('Unknown: request timed out; daemon state not observed', { timeout: 8000 });
  await axe(page);
  // Recovery: back to the real daemon.
  await page.unroute('**/api/v1/snapshot*');
  await page.getByRole('button', { name: 'Refresh snapshot' }).click();
  await expect(status(page)).toHaveText(/^Read: bounded fixture snapshot decoded$/);
});

test('load and refresh only read snapshots without push or persistent state', async ({ page, context }) => {
  const requests: { method: string; url: string }[] = [];
  page.on('request', request => {
    if (!['document', 'script', 'stylesheet'].includes(request.resourceType())) {
      requests.push({ method: request.method(), url: request.url() });
    }
  });
  await page.addInitScript(() => {
    const calls: string[] = [];
    Object.assign(window, { forbiddenCalls: calls });
    for (const name of ['EventSource', 'WebSocket'] as const) {
      Object.defineProperty(window, name, { value: new Proxy(window[name], {
        construct(target, args) { calls.push(name); return Reflect.construct(target, args); },
      }) });
    }
    for (const [owner, method] of [[Storage.prototype, 'setItem'], [IDBFactory.prototype, 'open'],
      [IDBFactory.prototype, 'deleteDatabase'], [ServiceWorkerContainer.prototype, 'register']] as const) {
      Object.defineProperty(owner, method, { value: new Proxy(Reflect.get(owner, method), {
        apply(target, receiver, args) { calls.push(method); return Reflect.apply(target, receiver, args); },
      }) });
    }
    const cookie = Object.getOwnPropertyDescriptor(Document.prototype, 'cookie')!;
    Object.defineProperty(Document.prototype, 'cookie', { ...cookie,
      set(value: string) { calls.push('cookie'); cookie.set!.call(this, value); },
    });
  });
  await page.goto(base + '/');
  await expect(status(page)).toHaveText(/^Read/);
  await page.getByRole('button', { name: 'Refresh snapshot' }).click();
  await expect.poll(() => requests.length).toBe(2);
  await expect(status(page)).toHaveText(/^Read/);
  expect(requests).toEqual(Array.from({ length: 2 }, () => ({ method: 'GET', url: base + '/api/v1/snapshot?limit=50' })));
  expect(await page.evaluate(() => (window as typeof window & { forbiddenCalls: string[] }).forbiddenCalls)).toEqual([]);
  expect(await page.evaluate(async () => (await navigator.serviceWorker.getRegistrations()).length)).toBe(0);
  expect(await page.evaluate(async () => indexedDB.databases())).toEqual([]);
  expect(await page.evaluate(() => localStorage.length + sessionStorage.length)).toBe(0);
  expect(await context.cookies()).toEqual([]);
});

test('keyboard path, visible focus, semantics and reduced motion', async ({ page }) => {
  await page.goto(base + '/');
  await expect(status(page)).toHaveText(/^Read/);
  await expect(page.locator('main')).toHaveCount(1);
  await expect(page.locator('header h1')).toHaveCount(1);
  await expect(page.locator('section h2')).toHaveCount(3);
  await expect(page.locator('table th[scope=col]').first()).toBeVisible();
  await page.keyboard.press('Tab');
  await expect(page.getByRole('button', { name: 'Refresh snapshot' })).toBeFocused();
  const outline = await page.getByRole('button').evaluate(e => getComputedStyle(e).outlineWidth);
  expect(outline).toBe('3px');
  await page.keyboard.press('Tab');
  await expect(page.getByRole('group', { name: /^Tasks table/ })).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(page.getByRole('group', { name: /^Events table/ })).toBeFocused();
  await page.keyboard.press('Shift+Tab');
  await page.keyboard.press('Shift+Tab');
  await page.keyboard.press('Enter');
  await expect(status(page)).toHaveText(/^Read/);
  await page.emulateMedia({ reducedMotion: 'reduce' });
  expect(await page.getByRole('button').evaluate(e => getComputedStyle(e).transitionProperty)).toBe('none');
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  expect(await page.getByRole('button').evaluate(e => getComputedStyle(e).transitionProperty)).toBe('background-color');
});

for (const [name, width, scheme] of [['360-light', 360, 'light'], ['360-dark', 360, 'dark'], ['1280-light', 1280, 'light'], ['1280-dark', 1280, 'dark']] as const) {
  test(`layout ${name}: no horizontal page overflow, axe clean`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await page.emulateMedia({ colorScheme: scheme });
    await page.goto(base + '/');
    await expect(status(page)).toHaveText(/^Read/);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
    const r = await axe(page);
    expect(r.violations.map(v => v.id)).not.toContain('color-contrast');
    await page.screenshot({ path: `${shots}/${name}.png`, fullPage: true });
  });
}

test('direct browser requests cannot enable actions, imports or remote binding', async ({ page }) => {
  await page.goto(base + '/');
  await expect(status(page)).toHaveText(/^Read/);
  const probe = async (init: RequestInit & { path: string }) => {
    const { path, ...rest } = init;
    return page.evaluate(async ({ path, rest }) => {
      try {
        const r = await fetch(path, rest as RequestInit);
        return { status: r.status, body: await r.text() };
      } catch (e) { return { status: 0, body: String(e) }; }
    }, { path, rest });
  };
  for (const [path, want] of [
    ['/api/v1/snapshot?enable=execution', 400], ['/api/v1/snapshot?import=/private/state', 400], ['/api/v1/snapshot?bind=0.0.0.0', 400],
    ['/api/v1/status?limit=1', 400], ['/api/v1/enroll', 404], ['/api/v1/grant', 404], ['/api/v1/accept', 404], ['/api/v1/publish', 404],
    ['/api/v1/stop', 404], ['/api/v1/sessions', 404], ['/api/v1/events/stream', 404], ['/sw.js', 404], ['/index.html', 404], ['/?mode=admin', 400],
  ] as const) {
    const r = await probe({ path });
    expect(r.status, path).toBe(want);
    expect(r.body).not.toMatch(/gaffer-fixture-|\/tmp\/|sqlite/i);
  }
  for (const method of ['POST', 'PUT', 'PATCH', 'DELETE']) {
    const r = await probe({ path: '/api/v1/snapshot', method, body: '{"action":"start"}', headers: { 'content-type': 'application/json' } });
    expect(r.status, method).toBe(405);
    expect(r.body).toContain('"error":"read_only"');
  }
  expect((await probe({ path: '/api/v1/status', headers: { 'X-Forwarded-For': '203.0.113.9' } })).status).toBe(403);
});
