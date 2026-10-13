// A browser context as every check gets one: watched, and unable to write.

import { readFile } from 'node:fs/promises';
import { Fail } from './check.mjs';
import { SCRIPTS, slug } from './fields.mjs';

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
// picture; waypoints are a player's own, which a service session is
// refused, so that 403 is the server doing as it should; and a map that
// keeps no history has no list of days, which the page asks for once.
const EXPECTED = new Map([
  ['/tiles', [404]],
  ['/api/biomes/tiles', [404]],
  ['/api/icons/head', [404]],
  ['/api/icons/mob', [404]],
  ['/api/icons/picture', [404]],
  ['/api/waypoints', [403, 404]],
  ['/api/history', [404]],
]);

// Where in the page's scripts an error was thrown: which of the page's own
// files, and the line. A file that is not one of them is "elsewhere".
function whereOf(stack) {
  const at = /([a-z0-9_.-]+\.js):(\d+):\d+/i.exec(String(stack));
  const script = at && SCRIPTS.includes(at[1]) ? at[1] : null;
  return { script, line: at && script ? Number(at[2]) : 0 };
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

  // Numbers only: how many of each, in which of the page's scripts, and
  // the worst status seen.
  counts() {
    const sum = (map) => [...map.values()].reduce((a, b) => a + b, 0);
    const out = { page_errors: this.errors.length, failed_responses: sum(this.responses), failed_requests: sum(this.failures), writes: this.writes.length };
    for (const { script, line } of this.errors) {
      const name = script ? `errors_in_${slug(script)}` : 'errors_elsewhere';
      out[name] = (out[name] || 0) + 1;
      if (script && !out.first_error_line) out.first_error_line = line;
    }
    if (this.worstStatus) out.worst_status = this.worstStatus;
    return out;
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
      watch.worstStatus = Math.max(watch.worstStatus || 0, status);
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
  try {
    await page.goto(`${cfg.publicUrl}/${hash}`, { waitUntil: 'domcontentloaded' });
  } catch {
    await page.close().catch(() => {});
    throw new Fail('the map could not be reached');
  }
  await page.waitForFunction(() => window.mcmap && window.mcmap.icons && window.mcmap.dimension && window.mcmap.dimension() && document.getElementById('login').hidden, null, { timeout: 30_000 });
  return page;
}
