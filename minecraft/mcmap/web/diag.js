'use strict';

// What is kept in memory for a troubleshooting report, from the first
// moment of the page: a short log of the events worth knowing, the last
// few errors, which requests failed and how, and the live stream's
// coming and going. It runs in the head so that what happens during the
// load is seen. Nothing here is sent anywhere, and nothing kept holds a
// gamertag, a name or a place: an event says what happened and not to
// whom or where. The first part of the file has no page in it and is run
// on its own by the tests.
(() => {
  // --- what a report may say -----------------------------------------------------

  // FNV-1a over bytes, as eight hex digits: the server's fingerprint of
  // each file of the page, computed over the same bytes here.
  function fingerprint(bytes) {
    let h = 0x811c9dc5;
    for (let i = 0; i < bytes.length; i++) h = Math.imul(h ^ bytes[i], 0x01000193);
    return (h >>> 0).toString(16).padStart(8, '0');
  }

  // Which scripts the page loaded are not the ones the server has now.
  // loaded and served are { file: fingerprint }. The page and its scripts
  // are cached apart for a few minutes, so after a release a page may be
  // new with some scripts old, or the reverse, and bugs follow that no
  // version of either has.
  function mixed(loaded, served) {
    const stale = [];
    const same = [];
    const unknown = [];
    for (const [file, print] of Object.entries(loaded)) {
      if (!Object.hasOwn(served, file)) unknown.push(file);
      else if (served[file] === print) same.push(file);
      else stale.push(file);
    }
    stale.sort();
    let verdict = 'current';
    if (stale.length > 0) verdict = same.length > 0 ? 'mixed: some scripts are older than the page' : 'old: every script is older than the page';
    return { verdict, stale, current: same.length, unknown: unknown.sort() };
  }

  const WORD = /^[a-z0-9_.:-]{1,48}$/;
  // The settings as a report holds them. The keys that hold what a person
  // typed are replaced by how many there were: view names, which players
  // are filtered, and the names a waypoint or a name tag goes by. Places
  // are kept only when asked for.
  function scrubSettings(all, includePosition) {
    const count = (list) => (Array.isArray(list) ? `[${list.length} redacted]` : '[redacted]');
    const out = {};
    for (const [section, value] of Object.entries(all || {})) {
      if (section === 'v') {
        out.v = value;
      } else if (section === 'layers') {
        out.layers = {};
        for (const [key, v] of Object.entries(value || {})) {
          if (typeof v === 'boolean' || typeof v === 'number') out.layers[key] = v;
          else if (v && typeof v === 'object') {
            // A choice over a layer's items: which, and not what they are called.
            out.layers[key] = Object.fromEntries(Object.entries(v).map(([k, list]) => [k, Array.isArray(list) ? count(list) : typeof list === 'string' && WORD.test(list) && k === 'mode' ? list : list === null ? null : '[redacted]']));
          } else out.layers[key] = '[redacted]';
        }
      } else if (section === 'views') {
        out.views = {
          count: Array.isArray(value.list) ? value.list.length : 0,
          order: Array.isArray(value.order) ? value.order.length : 0,
          hidden: value.hidden || [],
          startSet: value.start !== null && value.start !== undefined,
          activeSet: value.active !== null && value.active !== undefined,
          saved: (value.list || []).map((view) => ({
            name: '[redacted]',
            layers: Object.keys(view.layers || {}).length,
            players: view.players ? count(view.players.hidden) : undefined,
            place: view.place ? (includePosition ? view.place : { d: view.place.d, zoom: view.place.zoom, position: 'omitted' }) : undefined,
            look: view.look ? Object.keys(view.look).length : undefined,
          })),
        };
      } else if (section === 'look' || section === 'live' || section === 'trails' || section === 'shortcuts' || section === 'grid' || section === 'history' || section === 'panel' || section === 'debug') {
        out[section] = JSON.parse(JSON.stringify(value));
      } else if (section === 'biome') {
        out.biome = { only: value && value.only ? '[redacted]' : null };
      } else {
        out[section] = '[omitted]';
      }
    }
    return out;
  }

  // The page's own address as a report holds it: the dimension and the
  // zoom, and the place only when asked for. What is open over the map
  // and any view carried in the address are left out: the second holds a
  // name.
  function scrubLink(href, includePosition) {
    let url;
    try { url = new URL(href); } catch { return '[unreadable address]'; }
    const parts = url.hash.slice(1).split('/');
    const [d, x, z, zoom, extra, view] = parts;
    const place = includePosition ? `${x}/${z}` : '…/…';
    const hash = parts.length > 3 ? `#${d}/${place}/${zoom}${extra ? '/…' : ''}${view ? '/<view omitted>' : ''}` : '';
    return `${url.origin}${url.pathname}${url.search}${hash}`;
  }

  window.mcmapDiag = { fingerprint, mixed, scrubSettings, scrubLink };

  // --- what is kept ------------------------------------------------------------------

  const CAP = 200;
  const TEXT = 160;
  const ERRORS = 5;
  const FAILURES = 20;
  const FRAMES = 240;
  const events = [];
  const errors = [];
  const failures = [];
  const endpoints = new Map();
  const state = { longTasks: 0, longest: 0 };
  const snapshots = { 'api/markers': 'markers', 'api/structures': 'structures', 'api/biomes': 'biomes' };
  const seen = {};

  function log(kind, text) {
    events.push({ at: Date.now(), kind, text: String(text).slice(0, TEXT) });
    if (events.length > CAP) events.shift();
  }

  function oops(text) {
    errors.push({ at: Date.now(), text: String(text).slice(0, 240) });
    if (errors.length > ERRORS) errors.shift();
    log('error', text);
  }

  // File and line, without the address: the file is the page's own.
  const where = (file, line) => (file ? `${String(file).split('/').pop().split('?')[0]}:${line || 0}` : '');
  addEventListener('error', (e) => oops(`${e.message || 'error'} ${where(e.filename, e.lineno)}`.trim()));
  addEventListener('unhandledrejection', (e) => oops(`unhandled rejection: ${e.reason && e.reason.message ? e.reason.message : String(e.reason)}`));

  // An address as the kind of request it is: numbers and the query are
  // not part of the kind, so that a thousand tiles are one line.
  const kindOf = (input) => {
    try {
      const url = new URL(typeof input === 'string' ? input : input.url, location.href);
      return url.pathname.replace(/^\/+/, '').replace(/\d+/g, '#').slice(0, 64) || '/';
    } catch {
      return '?';
    }
  };

  function note(kind, status, ms) {
    const at = endpoints.get(kind) || { requests: 0, failed: 0, last: null };
    at.requests += 1;
    at.last = { status, ms: Math.round(ms), at: Date.now() };
    if (status === 0 || status >= 400) {
      at.failed += 1;
      failures.push({ at: Date.now(), kind, status, ms: Math.round(ms) });
      if (failures.length > FAILURES) failures.shift();
      log('network', `${kind} ${status === 0 ? 'failed to connect' : status} after ${Math.round(ms)} ms`);
    }
    endpoints.set(kind, at);
    // Not unbounded: a path with a letter-made id would make a kind each.
    if (endpoints.size > 64) endpoints.delete(endpoints.keys().next().value);
  }

  if (typeof window.fetch === 'function') {
    const native = window.fetch;
    window.fetch = function fetch(input) {
      const started = performance.now();
      const kind = kindOf(input);
      return native.apply(this, arguments).then((res) => {
        note(kind, res.status, performance.now() - started);
        if (stream.parse && res.ok && Object.hasOwn(snapshots, kind)) {
          // When the server last took the snapshot an answer is of, read
          // from a copy, and only while something is looking.
          res.clone().json().then((body) => { if (body && typeof body.at === 'string') seen[kind] = { at: body.at, when: Date.now() }; }, () => {});
        }
        return res;
      }, (err) => {
        note(kind, 0, performance.now() - started);
        throw err;
      });
    };
  }

  // The live stream, as the page's own script opens it.
  const stream = { opened: 0, errors: 0, connected: false, lastFrame: 0, frames: [], parse: false, entities: null, latest: new Map() };
  if (typeof window.EventSource === 'function') {
    const Native = window.EventSource;
    window.EventSource = class extends Native {
      constructor(url, init) {
        super(url, init);
        if (!/\/?api\/live\?/.test(String(url))) return;
        stream.opened += 1;
        log('stream', `opened (${stream.opened} so far)`);
        this.addEventListener('open', () => {
          stream.connected = true;
          log('stream', 'connected');
        });
        this.addEventListener('error', () => {
          stream.connected = false;
          stream.errors += 1;
          log('stream', 'error or closed');
        });
        this.addEventListener('message', (ev) => {
          stream.lastFrame = Date.now();
          stream.frames.push(stream.lastFrame);
          if (stream.frames.length > FRAMES) stream.frames.shift();
          if (!stream.parse) return;
          // Only counted, and only while something is looking.
          try {
            const frame = JSON.parse(ev.data);
            stream.entities = { players: (frame.players || []).length, mobs: (frame.mobs || []).length, more: frame.more || 0, morePlayers: frame.morePlayers || 0, stale: Boolean(frame.stale) };
            stream.latest.clear();
            for (const p of frame.players || []) if (stream.latest.size < 4096) stream.latest.set(`p:${p.i}`, { x: p.x, z: p.z });
            for (const m of frame.mobs || []) if (stream.latest.size < 4096) stream.latest.set(`m:${m.i}`, { x: m.x, z: m.z });
          } catch { /* a frame the page itself will report */ }
        });
      }
    };
  }

  try {
    new PerformanceObserver((list) => {
      for (const task of list.getEntries()) {
        state.longTasks += 1;
        state.longest = Math.max(state.longest, task.duration);
        log('long task', `${Math.round(task.duration)} ms`);
      }
    }).observe({ type: 'longtask' });
  } catch { /* a browser without long task timing says none */ }

  // --- what the page itself says happened --------------------------------------------

  const settings = window.mcmapSettings || null;
  if (settings && settings.opened && settings.opened()) {
    const o = settings.opened();
    log('settings', o.from === null ? `no record kept; started at version ${o.to}` : o.from === o.to ? `record version ${o.to} read` : `record version ${o.from} brought to ${o.to}`);
    if (settings.kept && settings.kept() !== 'yes') log('settings', `storage: ${settings.kept()}`);
  }

  document.addEventListener('DOMContentLoaded', () => {
    const app = window.mcmap;
    if (!app || !app.map) return;
    const { map } = app;
    let zoom = null;
    map.on('zoomend', () => {
      const now = Math.round(map.getZoom() * 100) / 100;
      if (now !== zoom) log('zoom', `to ${now}`);
      zoom = now;
    });
    let dimension = null;
    document.addEventListener('mcmap:view', () => {
      const now = app.dimension && app.dimension();
      if (now && now !== dimension) log('dimension', `now ${now}`);
      dimension = now;
    });
    let following = false;
    document.addEventListener('mcmap:inspect', () => {
      const now = Boolean(app.inspect && app.inspect.following && app.inspect.following());
      if (now !== following) log('follow', now ? 'on' : 'off');
      following = now;
    });
    document.addEventListener('mcmap:settings', (e) => {
      if (e.detail && e.detail.sections) log('settings', `changed: ${e.detail.sections.join(', ')}`);
    });
  });

  window.mcmapDiag.keep = {
    log,
    events: () => events.slice(),
    errors: () => errors.slice(),
    failures: () => failures.slice(),
    endpoints: () => [...endpoints].map(([kind, v]) => ({ kind, ...v })),
    longTasks: () => ({ count: state.longTasks, longest: Math.round(state.longest) }),
    stream,
    snapshots: () => Object.fromEntries(Object.entries(seen).map(([kind, v]) => [snapshots[kind], v])),
  };
})();
