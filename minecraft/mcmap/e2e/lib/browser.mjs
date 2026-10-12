// A browser context as every check gets one: watched, and unable to write.

import { readFile } from 'node:fs/promises';

const probe = await readFile(new URL('./probe.js', import.meta.url), 'utf8');

// An address as it may be written down: its path with whatever names a
// place, a player or a picture taken off, and never its query, which is
// where coordinates and gamertags travel.
export function routeOf(address) {
  let pathname;
  try { pathname = new URL(address).pathname; } catch { return 'unreadable'; }
  for (const prefix of ['/tiles', '/api/biomes/tiles', '/api/icons/mob', '/api/icons/picture']) {
    if (pathname.startsWith(`${prefix}/`)) return prefix;
  }
  return /^\/[a-z0-9_./-]{0,80}$/i.test(pathname) ? pathname : 'unreadable';
}

// Answers that are not faults. A sparse world has no tile for most of its
// grid; a head is gone when its player leaves between the list and the
// picture; and waypoints are a player's own, which a service session is
// refused, so that 403 is the server doing as it should.
const EXPECTED = new Map([
  ['/tiles', [404]],
  ['/api/biomes/tiles', [404]],
  ['/api/icons/head', [404]],
  ['/api/icons/mob', [404]],
  ['/api/icons/picture', [404]],
  ['/api/waypoints', [403, 404]],
]);

// Where in the page's scripts an error was thrown: the file and line, which
// say what broke without repeating anything the page was showing.
function whereOf(stack) {
  const at = /([a-z0-9_.-]+\.js):(\d+):\d+/i.exec(String(stack));
  return at ? `${at[1]}:${at[2]}` : 'unknown';
}

export class Watch {
  constructor() {
    this.errors = [];
    this.responses = new Map();
    this.failures = new Map();
    this.writes = [];
    // Kept for the evidence file, which stays where the map is, and for
    // nothing that is logged.
    this.raw = [];
  }

  bump(map, key) {
    map.set(key, (map.get(key) || 0) + 1);
  }

  counts() {
    const sum = (map) => [...map.values()].reduce((a, b) => a + b, 0);
    return { page_errors: this.errors.length, failed_responses: sum(this.responses), failed_requests: sum(this.failures), writes: this.writes.length };
  }
}

// Opens a context that identifies itself, records what goes wrong in it and
// lets nothing but a read leave it. stubs answers a request in the browser
// by "METHOD /path", for the one place the page itself would write: asking
// for a login code.
export async function openContext(browser, { cfg, watch, cookie = null, viewport = { width: 1300, height: 800 }, dpr = 1, touch = false, stubs = {}, expected = [] }) {
  const ctx = await browser.newContext({
    viewport,
    deviceScaleFactor: dpr,
    hasTouch: touch,
    isMobile: touch,
    userAgent: cfg.userAgent,
    ignoreHTTPSErrors: cfg.insecureLocalTls,
    serviceWorkers: 'block',
  });
  ctx.setDefaultTimeout(15_000);
  if (cookie) await ctx.addCookies([cookie]);
  await ctx.addInitScript(probe);
  await ctx.route('**/*', async (route) => {
    const request = route.request();
    const method = request.method();
    const key = `${method} ${routeOf(request.url())}`;
    if (Object.hasOwn(stubs, key)) {
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(stubs[key]) });
      return;
    }
    if (method !== 'GET' && method !== 'HEAD') {
      watch.writes.push(key);
      await route.abort('blockedbyclient');
      return;
    }
    await route.continue();
  });
  const allowed = new Map(EXPECTED);
  for (const [route, status] of expected) allowed.set(route, [...(allowed.get(route) || []), status]);
  const attach = (page) => {
    page.on('pageerror', (err) => {
      watch.errors.push(whereOf(err.stack));
      watch.raw.push({ kind: 'pageerror', text: String(err.stack || err) });
    });
    page.on('console', (message) => {
      // A failed load is counted once, by its response.
      if (message.type() !== 'error' || /Failed to load resource/.test(message.text())) return;
      watch.errors.push(whereOf(message.location()?.url ? `${message.location().url}:${message.location().lineNumber}:0` : ''));
      watch.raw.push({ kind: 'console', text: message.text() });
    });
    page.on('response', (response) => {
      const status = response.status();
      if (status < 400) return;
      const route = routeOf(response.url());
      if ((allowed.get(route) || []).includes(status)) return;
      watch.bump(watch.responses, `${status} ${route}`);
      watch.raw.push({ kind: 'response', status, url: response.url() });
    });
    page.on('requestfailed', (request) => {
      const why = request.failure()?.errorText || '';
      // Ended by the page itself: a stream shut on a change of dimension,
      // a tile no longer wanted, a write this context refused.
      if (/ERR_ABORTED|ERR_BLOCKED_BY_CLIENT/.test(why)) return;
      watch.bump(watch.failures, `${why.replace(/^net::/, '')} ${routeOf(request.url())}`);
      watch.raw.push({ kind: 'requestfailed', why, url: request.url() });
    });
  };
  ctx.on('page', attach);
  return ctx;
}

// The map page, loaded and showing a dimension.
export async function openMap(ctx, cfg, hash = '') {
  const page = await ctx.newPage();
  await page.goto(`${cfg.publicUrl}/${hash}`, { waitUntil: 'domcontentloaded' });
  await page.waitForFunction(() => window.mcmap && window.mcmap.icons && window.mcmap.dimension && window.mcmap.dimension() && document.getElementById('login').hidden, null, { timeout: 30_000 });
  return page;
}
