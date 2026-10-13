// Who is let in, as what, and what the page is made of.

import { Fail } from '../lib/check.mjs';
import { openContext, routeOf } from '../lib/browser.mjs';
import { SERVICE_NAME, SESSION_COOKIE } from '../lib/session.mjs';

const STUB_CODE = 'E2ECHK';

// With no session the map answers 401 through the real path and the page
// shows the login. The page asks for a code as soon as it is refused, and
// a code is a row in the server's table of waiting logins, so that one
// request is answered here in the browser and never sent.
export async function loginScreen({ browser, cfg, watch, closers = [] }) {
  const ctx = await openContext(browser, {
    cfg, watch,
    stubs: { 'POST /auth/start': { code: STUB_CODE, expiresIn: 600 }, 'GET /auth/status': { state: 'pending' } },
    expected: [['/api/map', 401]],
  });
  closers.push(() => ctx.close());
  try {
    const page = await ctx.newPage();
    const refused = page.waitForResponse((res) => routeOf(res.url()) === '/api/map', { timeout: 20_000 });
    // Waited for from before the page is asked for, so that it cannot be
    // missed, and answered here if the page never comes: a promise nobody
    // is waiting on when it fails takes the whole run down with it.
    refused.catch(() => {});
    const began = Date.now();
    try {
      await page.goto(`${cfg.publicUrl}/`, { waitUntil: 'domcontentloaded' });
    } catch {
      throw new Fail('the map could not be reached');
    }
    let status;
    try {
      status = (await refused).status();
    } catch {
      throw new Fail('the map did not answer a browser with no session at all');
    }
    if (status !== 401) throw new Fail('the map did not answer 401 to a browser with no session', { status });
    await page.waitForFunction(() => !document.getElementById('login').hidden, null, { timeout: 10_000 });
    const shown = await page.evaluate(() => ({
      command: document.getElementById('login-command').textContent,
      world: document.getElementById('world').textContent.trim().length,
      tiles: document.querySelectorAll('#map img.leaflet-tile').length,
    }));
    if (!shown.command.includes(STUB_CODE)) throw new Fail('the login is shown without the code to type');
    if (shown.tiles > 0) throw new Fail('the page drew tiles for a browser with no session', { tiles: shown.tiles });
    return { measures: { login_ms: Date.now() - began } };
  } finally {
    await ctx.close();
  }
}

// The service session reaches the map, says what it is, and its cookie is
// in the browser as a player's would be.
export async function session({ page, ctx, cfg }) {
  const host = new URL(cfg.publicUrl).hostname;
  const cookies = (await ctx.cookies(cfg.publicUrl)).filter((c) => c.name === SESSION_COOKIE);
  if (cookies.length !== 1) throw new Fail('the browser does not hold exactly one session cookie', { cookies: cookies.length });
  const [cookie] = cookies;
  const weak = [];
  if (!cookie.secure) weak.push('not Secure');
  if (!cookie.httpOnly) weak.push('not HttpOnly');
  if (cookie.sameSite !== 'Lax') weak.push('not SameSite=Lax');
  if (cookie.path !== '/') weak.push('a narrower Path');
  if (cookie.domain !== host) weak.push('not bound to the one host');
  if (weak.length > 0) throw new Fail('the session cookie in the browser is weaker than a player\'s', { weaknesses: weak.length });
  const seen = await page.evaluate(async (name) => {
    const me = await (await fetch('api/me', { cache: 'no-store' })).json();
    return {
      who: document.getElementById('who').textContent,
      whoHidden: document.getElementById('who').hidden,
      me: me.gamertag, service: me.service === true,
      scriptSees: document.cookie.includes(name),
      titled: document.title.length > 0,
    };
  }, SESSION_COOKIE);
  if (seen.me !== SERVICE_NAME || !seen.service) throw new Fail('the session is not the service identity');
  if (seen.whoHidden || seen.who !== SERVICE_NAME) throw new Fail('the page does not say the session is the service identity');
  if (seen.scriptSees) throw new Fail('script on the page can read the session cookie');
  return { measures: { ttl_seconds: cfg.session.ttlSeconds } };
}

// What a service session must not see, asked for on the live server: a
// refusal here is the thing working. Nothing but reads is tried; that a
// write is refused is the server's own tests' to prove.
export async function scope({ page }) {
  const got = await page.evaluate(async () => {
    const dimension = window.mcmap.dimension();
    const waypoints = await fetch('api/waypoints', { cache: 'no-store' });
    const icons = await fetch('api/icons', { cache: 'no-store' });
    const search = await fetch(`api/search?q=a&dimension=${encodeURIComponent(dimension)}&x=0&z=0&limit=1`, { cache: 'no-store' });
    const listed = icons.ok ? await icons.json() : null;
    const found = search.ok ? await search.json() : null;
    return {
      waypoints: waypoints.status,
      icons: icons.status,
      me: listed ? typeof listed.me === 'string' && listed.me !== '' : false,
      search: search.status,
      searched: found ? found.waypoints : '',
    };
  });
  if (got.waypoints !== 403 && got.waypoints !== 404) throw new Fail('waypoints did not refuse a service session', { waypoints_status: got.waypoints });
  if (got.me) throw new Fail('the icon list names a player as the service session');
  if (got.search === 200 && got.searched !== 'off') throw new Fail('a search for a service session went through a player\'s waypoints');
  return { measures: { waypoints_status: got.waypoints } };
}

// The headers that keep the page from being framed or fed from elsewhere,
// as they arrive through the gateway and not as the pod sets them.
export async function headers({ page, cfg }) {
  const got = await page.evaluate(async () => {
    const root = await fetch('./', { cache: 'no-store' });
    const map = await fetch('api/map', { cache: 'no-store' });
    const pick = (res, name) => res.headers.get(name) || '';
    return {
      csp: pick(root, 'Content-Security-Policy'), nosniff: pick(root, 'X-Content-Type-Options'), referrer: pick(root, 'Referrer-Policy'),
      hsts: pick(root, 'Strict-Transport-Security'), mapCache: pick(map, 'Cache-Control'),
    };
  });
  const missing = [];
  if (!got.csp.includes("default-src 'self'") || !got.csp.includes("frame-ancestors 'none'")) missing.push('Content-Security-Policy');
  if (got.nosniff !== 'nosniff') missing.push('X-Content-Type-Options');
  if (got.referrer !== 'no-referrer') missing.push('Referrer-Policy');
  if (cfg.publicUrl.startsWith('https:') && !/max-age=\d+/.test(got.hsts)) missing.push('Strict-Transport-Security');
  if (!got.mapCache.includes('no-store')) missing.push('Cache-Control on the map answer');
  if (missing.length > 0) throw new Fail('a security header did not arrive as the server sets it', { missing: missing.length });
  return {};
}

// Every script and stylesheet the page names is served, twice the same,
// and each made the part of the page it is there to make. A page whose
// files are of two releases fails here: by a script that is not what the
// server now serves, or by a part that never came to be.
export async function scripts({ page }) {
  const got = await page.evaluate(async () => {
    const digest = async (res) => [...new Uint8Array(await crypto.subtle.digest('SHA-256', await res.arrayBuffer()))].map((b) => b.toString(16).padStart(2, '0')).join('');
    const files = [...document.querySelectorAll('script[src], link[rel=stylesheet]')].map((el) => el.src || el.href);
    const out = { files: files.length, bad: 0, differ: 0, uncached: 0, longest: 0 };
    for (const [n, file] of files.entries()) {
      const one = await fetch(file, { cache: 'no-store' });
      const two = await fetch(`${file}?again=${n}`, { cache: 'no-store' });
      if (!one.ok || !two.ok || !/javascript|css/.test(one.headers.get('Content-Type') || '')) { out.bad += 1; continue; }
      if (await digest(one) !== await digest(two)) out.differ += 1;
      const age = /max-age=(\d+)/.exec(one.headers.get('Cache-Control') || '');
      if (!age) out.uncached += 1;
      else out.longest = Math.max(out.longest, Number(age[1]));
    }
    const app = window.mcmap || {};
    const parts = ['icons', 'inspect', 'layers', 'markers', 'structures', 'search', 'views', 'names', 'duration', 'room', 'chunk'];
    out.missing = parts.filter((part) => !app[part]).length + (window.mcmapSettings ? 0 : 1) + (window.L ? 0 : 1);
    return out;
  });
  if (got.files < 10) throw new Fail('the page names fewer files than it is made of', { files: got.files });
  if (got.bad > 0) throw new Fail('a file the page names is not served as what it is', { files_bad: got.bad });
  if (got.differ > 0) throw new Fail('the same file was served as two different ones', { files_differ: got.differ });
  if (got.missing > 0) throw new Fail('a part of the page was never made by its script', { parts_missing: got.missing });
  // An hour is past any release: a browser holding files that long is
  // the mixed page, kept.
  if (got.longest > 3600) throw new Fail('the page\'s files may be kept longer than an hour', { max_age_seconds: got.longest });
  return { measures: { files: got.files, max_age_seconds: got.longest } };
}

// Every answer the page is built from, within a time. Each address is one
// the page itself asks, for the dimension shown and the middle of the view.
export async function apiBudget({ page, cfg, report }) {
  const timed = await page.evaluate(async () => {
    const d = encodeURIComponent(window.mcmap.dimension());
    const centre = window.mcmap.map.getCenter();
    const at = `x=${Math.round(centre.lng)}&z=${Math.round(centre.lat)}`;
    const asks = [
      ['config', 'api/config'], ['me', 'api/me'], ['map', 'api/map'], ['icons', 'api/icons'], ['names', 'api/names'],
      ['markers', `api/markers?dimension=${d}`], ['structures', `api/structures?dimension=${d}&kinds=all`],
      ['biomes', `api/biomes?dimension=${d}`], ['biomes_at', `api/biomes/at?dimension=${d}&${at}`],
      ['trails', `api/trails?dimension=${d}`], ['search', `api/search?q=a&dimension=${d}&${at}&limit=5`],
    ];
    const out = [];
    for (const [name, address] of asks) {
      const began = performance.now();
      const res = await fetch(address, { cache: 'no-store' });
      await res.arrayBuffer();
      out.push({ name, status: res.status, ms: performance.now() - began });
    }
    return out;
  });
  const seconds = {};
  const measures = { asked: 0, not_served: 0, slowest_ms: 0 };
  const wrong = [];
  const slow = [];
  for (const { name, status, ms } of timed) {
    // A layer that is switched off is not served at all.
    if (status === 404) { measures.not_served += 1; continue; }
    measures.asked += 1;
    seconds[name] = Math.round(ms) / 1000;
    measures.slowest_ms = Math.max(measures.slowest_ms, Math.round(ms));
    if (status !== 200) wrong.push(name);
    else if (ms > cfg.apiBudgetMs) slow.push(name);
  }
  report.gauge('mcmap_e2e_api_seconds', seconds);
  measures.not_answered = wrong.length;
  measures.over_budget = slow.length;
  if (wrong.length > 0) throw new Fail('an answer the page is built from was not given', measures);
  if (slow.length > 0) throw new Fail('an answer the page is built from was slower than the budget', measures);
  return { measures };
}
