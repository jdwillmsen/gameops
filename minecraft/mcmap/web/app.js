'use strict';

(() => {
  // A region is the renderer's unit of extent: 512 blocks square.
  const REGION = 512;
  const TILE = 256;
  const POLL_MS = 60_000;
  // Once a refresh is due the map is asked more often than that, so the
  // countdown starts again within seconds of the snapshot landing: briskly
  // at first, then slowly, since a quiet window can last an hour.
  const DUE_POLL_MS = 5000;
  const DUE_BRISK_MS = 2 * 60_000;
  const DUE_SLOW_POLL_MS = 20_000;
  // The service counts its interval from the end of a cycle, not the start,
  // so a healthy refresh arrives a little after the countdown reaches zero.
  // Only past this is it called overdue.
  const DUE_GRACE_MS = 90_000;
  // How long after a snapshot its tiles are still expected at any moment.
  const DRAWING_MS = 5 * 60_000;
  const COUNTDOWN_MS = 250;
  const LABELS = { overworld: 'Overworld', nether: 'Nether', end: 'The End' };

  // Leaflet's simple CRS has +Y pointing up; a Minecraft map has +Z pointing
  // down. With this transformation lat is Z and lng is X, in blocks, and
  // zoom 0 is one block per pixel, which is exactly how the tiles are cut.
  const crs = L.extend({}, L.CRS.Simple, { transformation: new L.Transformation(1, 0, 1, 0) });

  const map = L.map('map', {
    crs,
    attributionControl: false,
    zoomSnap: 1,
    minZoom: -6,
    maxZoom: 3,
    maxBoundsViscosity: 0.8,
  });

  const el = {
    world: document.getElementById('world'),
    tabs: document.getElementById('dimensions'),
    coords: document.getElementById('coords'),
    status: document.getElementById('status'),
    refresh: document.getElementById('refresh'),
    grid: document.getElementById('grid'),
    copy: document.getElementById('copy'),
    goto: document.getElementById('goto'),
    gotoX: document.getElementById('goto-x'),
    gotoZ: document.getElementById('goto-z'),
    who: document.getElementById('who'),
    logout: document.getElementById('logout'),
    login: document.getElementById('login'),
    loginCommand: document.getElementById('login-command'),
    loginNote: document.getElementById('login-note'),
    loginNew: document.getElementById('login-new'),
  };

  // No cache-busting parameter: tiles are served with a short max-age and a
  // Last-Modified, so after a render the browser revalidates and only the
  // tiles that actually changed are downloaded again.
  const Tiles = L.TileLayer.extend({
    getTileUrl(c) {
      return `tiles/${this.options.dimension}/${c.z}/${c.x}/${c.y}.${this.options.format}`;
    },
  });

  const Grid = L.GridLayer.extend({
    createTile(c) {
      const canvas = L.DomUtil.create('canvas', 'leaflet-tile');
      canvas.width = canvas.height = TILE;
      const scale = 2 ** c.z;
      const span = TILE / scale;
      const x0 = c.x * span;
      const z0 = c.y * span;
      // The finest spacing that still leaves lines far enough apart to read.
      const step = [16, 128, REGION, 2048, 8192].find((s) => s * scale >= 32) || 32768;
      const ctx = canvas.getContext('2d');
      const line = (block, vertical) => {
        const p = Math.round((block - (vertical ? x0 : z0)) * scale) + 0.5;
        ctx.strokeStyle = block === 0 ? 'rgba(110,207,122,0.9)'
          : block % REGION === 0 ? 'rgba(255,255,255,0.45)' : 'rgba(255,255,255,0.18)';
        ctx.beginPath();
        if (vertical) { ctx.moveTo(p, 0); ctx.lineTo(p, TILE); } else { ctx.moveTo(0, p); ctx.lineTo(TILE, p); }
        ctx.stroke();
      };
      for (let b = Math.ceil(x0 / step) * step; b < x0 + span; b += step) line(b, true);
      for (let b = Math.ceil(z0 / step) * step; b < z0 + span; b += step) line(b, false);
      return canvas;
    },
  });

  let info = null;
  // The last answer as it was sent, to tell an unchanged one cheaply.
  let answer = '';
  let askedAt = 0;
  // The server's clock minus this one, so that a browser whose clock is
  // wrong still counts down to the right moment.
  let serverOffset = 0;
  // When the countdown stopped having a time to count to; null while it has.
  let dueSince = null;
  let current = null;
  let layer = null;
  // Leaflet's grid layers default to zoom 0 and up; this map lives below it.
  const grid = new Grid({ tileSize: TILE, zIndex: 5, minZoom: -12, maxZoom: 8 });

  const dimension = (id) => info && info.dimensions.find((d) => d.id === id);

  // Tells the live layer, which is a file of its own, that what it should
  // be showing may have changed: the dimension, the login, or the map data.
  const announce = () => document.dispatchEvent(new CustomEvent('mcmap:view'));
  const version = (d) => (d.renderedAt ? Date.parse(d.renderedAt) : 0);

  function parseHash() {
    const [id, x, z, zoom] = location.hash.slice(1).split('/');
    const n = [x, z, zoom].map(Number);
    if (!LABELS[id] || n.some((v) => !Number.isFinite(v))) return null;
    return { id, x: n[0], z: n[1], zoom: n[2] };
  }

  function writeHash() {
    if (!current) return;
    const c = map.getCenter();
    const hash = `#${current}/${Math.round(c.lng)}/${Math.round(c.lat)}/${map.getZoom()}`;
    // replaceState so panning does not fill the back button with positions.
    if (hash !== location.hash) history.replaceState(null, '', hash);
  }

  function show(id, view) {
    const d = dimension(id);
    if (!d || !d.rendered) return;
    current = id;
    if (layer) map.removeLayer(layer);

    const bounds = L.latLngBounds(
      [d.minRegionZ * REGION, d.minRegionX * REGION],
      [(d.maxRegionZ + 1) * REGION, (d.maxRegionX + 1) * REGION],
    );
    layer = new Tiles('', {
      dimension: id,
      format: d.format,
      tileSize: TILE,
      minNativeZoom: d.minZoom,
      maxNativeZoom: d.maxZoom,
      minZoom: d.minZoom,
      maxZoom: 3,
      bounds,
      noWrap: true,
      keepBuffer: 2,
    }).addTo(map);
    map.setMinZoom(d.minZoom);
    map.setMaxBounds(bounds.pad(0.25));

    if (view) map.setView([view.z, view.x], view.zoom);
    else if (bounds.contains([0, 0])) map.setView([0, 0], Math.max(d.minZoom, -2));
    else map.fitBounds(bounds);

    for (const b of el.tabs.children) b.setAttribute('aria-pressed', String(b.dataset.id === id));
    writeHash();
    describe();
    announce();
  }

  function tabs() {
    el.tabs.replaceChildren(...info.dimensions.map((d) => {
      const b = document.createElement('button');
      b.type = 'button';
      b.dataset.id = d.id;
      b.textContent = LABELS[d.id] || d.id;
      b.disabled = !d.rendered;
      if (!d.rendered) b.title = 'Not rendered yet';
      b.setAttribute('aria-pressed', String(d.id === current));
      b.addEventListener('click', () => show(d.id));
      return b;
    }));
  }

  function ago(iso) {
    const minutes = Math.max(0, Math.round((Date.now() - Date.parse(iso)) / 60_000));
    if (minutes < 1) return 'just now';
    if (minutes < 60) return `${minutes} min ago`;
    const hours = Math.round(minutes / 60);
    return hours < 48 ? `${hours} h ago` : `${Math.round(hours / 24)} days ago`;
  }

  function describe() {
    const d = dimension(current);
    el.status.classList.toggle('problem', Boolean(info && info.problem));
    if (!info) {
      el.status.textContent = '';
    } else if (!d || !d.rendered) {
      el.status.textContent = 'The first render is still running. This page updates when it is done.';
    } else {
      const when = d.renderedAt ? `Updated ${ago(d.renderedAt)}` : 'Rendered before the last restart';
      el.status.textContent = info.problem ? `${when}. The last refresh failed, so this is the previous map.` : when;
    }
  }

  // --- refresh countdown -------------------------------------------------
  //
  // Counted from the snapshot the server last took and the interval it
  // keeps. The server skips refreshes inside its quiet windows and backs
  // off after a failure, and says neither in advance, so past the expected
  // time this counts up and says overdue: it never sits at zero and never
  // starts again until a new snapshot has actually been seen.

  function clock(ms, round) {
    const s = Math.max(0, round(ms / 1000));
    const pad = (n) => String(n).padStart(2, '0');
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    return h ? `${h}:${pad(m)}:${pad(s % 60)}` : `${m}:${pad(s % 60)}`;
  }

  function countdown() {
    const locked = !el.login.hidden;
    el.refresh.hidden = locked || !info;
    if (locked || !info) return;
    const now = Date.now() + serverOffset;
    const snapshot = info.snapshotAt ? Date.parse(info.snapshotAt) : NaN;
    const every = info.refreshSeconds * 1000;
    let text;
    let overdue = false;
    let ask = true;
    if (!Number.isFinite(snapshot) || !(every > 0)) {
      // A service that has just started has taken no snapshot yet.
      text = 'Waiting for the first refresh';
    } else {
      const late = now - snapshot - every;
      if (late < 0) {
        // Never more than the interval, whatever the two clocks make of
        // a snapshot taken this instant.
        text = `Next refresh in ${clock(Math.min(-late, every), Math.ceil)}`;
        // The tiles of a snapshot follow it by as long as they take to draw.
        const d = dimension(current);
        ask = Boolean(d && d.renderedAt) && Date.parse(d.renderedAt) < snapshot && now - snapshot < DRAWING_MS;
        if (!ask) dueSince = null;
      } else if (late < DUE_GRACE_MS && !info.problem) {
        text = 'Refresh due now';
      } else {
        overdue = true;
        text = `Refresh overdue by ${clock(late, Math.floor)} · ${info.problem ? 'retrying' : 'may be in a quiet window'}`;
      }
    }
    if (el.refresh.textContent !== text) el.refresh.textContent = text;
    el.refresh.classList.toggle('problem', overdue);
    const hint = every > 0 ? `The map refreshes about every ${Math.round(every / 60_000)} min, except during the server's quiet windows` : '';
    if (el.refresh.title !== hint) el.refresh.title = hint;
    if (!ask || document.hidden) return;
    if (dueSince === null) dueSince = Date.now();
    const pace = Date.now() - dueSince < DUE_BRISK_MS ? DUE_POLL_MS : DUE_SLOW_POLL_MS;
    if (Date.now() - askedAt >= pace) load(true);
  }

  // --- login -----------------------------------------------------------
  //
  // The page shows a code; the player types it in game chat, which only
  // someone on the server can do; the page then collects its session. The
  // secret that collects it lives in a cookie this script cannot read.

  let loginTimer = null;

  function lock(locked) {
    document.body.classList.toggle('locked', locked);
    el.login.hidden = !locked;
    // Leaflet measured the map while it was hidden.
    if (!locked) map.invalidateSize();
    announce();
  }

  function note(text, problem) {
    el.loginNote.textContent = text;
    el.loginNote.classList.toggle('problem', Boolean(problem));
  }

  async function startLogin() {
    clearInterval(loginTimer);
    lock(true);
    el.loginNew.hidden = true;
    let started;
    try {
      const res = await fetch('auth/start', { method: 'POST' });
      if (!res.ok) throw new Error(String(res.status));
      started = await res.json();
    } catch {
      note('Cannot reach the map service. Check your connection and try again.', true);
      el.loginNew.hidden = false;
      return;
    }
    el.loginCommand.textContent = `!map ${started.code}`;
    note('Waiting for you to type the code in game…');
    loginTimer = setInterval(pollLogin, 2000);
  }

  async function pollLogin() {
    let state;
    try {
      const res = await fetch('auth/status', { cache: 'no-store' });
      if (!res.ok) return;
      ({ state } = await res.json());
    } catch {
      return; // a dropped request is not an answer; the next poll asks again
    }
    if (state === 'pending') return;
    clearInterval(loginTimer);
    if (state === 'ok') {
      lock(false);
      await load();
      return;
    }
    el.loginCommand.textContent = '!map';
    note('That code expired before it was used.', true);
    el.loginNew.hidden = false;
  }

  el.loginNew.addEventListener('click', startLogin);
  el.logout.addEventListener('click', async () => {
    await fetch('auth/logout', { method: 'POST' });
    location.reload();
  });

  async function identify() {
    if (!loginEnabled || !el.who.hidden) return;
    try {
      const res = await fetch('api/me', { cache: 'no-store' });
      if (!res.ok) return;
      el.who.textContent = (await res.json()).gamertag;
      el.who.hidden = false;
      el.logout.hidden = false;
    } catch { /* the name is decoration */ }
  }

  let loginEnabled = false;

  // checking is true when the countdown is only asking whether the refresh
  // has landed: an answer that says nothing new then changes nothing, so
  // the layers that reload on every announcement are not made to.
  async function load(checking) {
    if (!el.login.hidden) return; // logged out; the login flow reloads when done
    askedAt = Date.now();
    let next;
    let body;
    try {
      const res = await fetch('api/map', { cache: 'no-store' });
      if (res.status === 401) {
        // Not logged in, or the session ran out while the page was open.
        el.who.hidden = true;
        el.logout.hidden = true;
        startLogin();
        return;
      }
      if (!res.ok) throw new Error(String(res.status));
      body = await res.text();
      next = JSON.parse(body);
      // The header is cut to the whole second, so the server's clock is
      // half a second past it on average.
      const sent = Date.parse(res.headers.get('Date') || '');
      if (Number.isFinite(sent)) serverOffset = sent + 500 - Date.now();
      identify();
    } catch {
      el.status.classList.add('problem');
      el.status.textContent = 'Cannot reach the map service. Retrying.';
      return;
    }
    if (checking === true && body === answer) return;
    answer = body;
    const before = current && dimension(current);
    info = next;
    el.world.textContent = `${info.world} map`;
    document.title = `${info.world} map`;
    tabs();

    if (!current) {
      const want = parseHash();
      const first = info.dimensions.find((d) => d.rendered);
      if (want && dimension(want.id) && dimension(want.id).rendered) show(want.id, want);
      else if (first) show(first.id);
    } else {
      const now = dimension(current);
      if (layer && now && before && version(now) !== version(before)) {
        // A render can extend the map, so the layer is rebuilt with the new
        // extent rather than redrawn inside the old one. The view stays put.
        const c = map.getCenter();
        show(current, { x: c.lng, z: c.lat, zoom: map.getZoom() });
      }
    }
    describe();
    countdown();
    announce();
  }

  // What the layers build on. The map works the same without them. layers
  // is filled in by the panel's own script; ready is here from the start so
  // that a script which runs before the panel can wait for it.
  const layers = {};
  layers.ready = new Promise((resolve) => {
    document.addEventListener('mcmap:layers', () => resolve(layers), { once: true });
  });
  window.mcmap = {
    map,
    dimension: () => current,
    live: () => Boolean(info && info.live),
    // Never passes an argument on: load reads one as "only checking".
    reload: () => load(),
    layers,
  };

  const fmt = (n) => Math.floor(n).toLocaleString('en-US');

  function point(latlng) {
    const x = Math.floor(latlng.lng);
    const z = Math.floor(latlng.lat);
    let text = `X ${fmt(x)}, Z ${fmt(z)}`;
    // Nether travel is the one conversion players do in their heads.
    if (current === 'overworld') text += `  ·  nether ${fmt(x / 8)}, ${fmt(z / 8)}`;
    if (current === 'nether') text += `  ·  overworld ${fmt(x * 8)}, ${fmt(z * 8)}`;
    el.coords.textContent = text;
  }

  map.on('mousemove', (e) => point(e.latlng));
  map.on('moveend', () => {
    writeHash();
    // With no pointer to hover, the centre of the view is the useful point.
    if (matchMedia('(hover: none)').matches) point(map.getCenter());
  });

  addEventListener('hashchange', () => {
    const want = parseHash();
    if (!want) return;
    if (want.id !== current) show(want.id, want);
    else map.setView([want.z, want.x], want.zoom);
  });

  el.grid.addEventListener('change', () => {
    if (el.grid.checked) grid.addTo(map); else map.removeLayer(grid);
  });

  el.goto.addEventListener('submit', (e) => {
    e.preventDefault();
    const x = Number(el.gotoX.value);
    const z = Number(el.gotoZ.value);
    if (Number.isFinite(x) && Number.isFinite(z)) map.setView([z, x], Math.max(map.getZoom(), -1));
  });

  el.copy.addEventListener('click', async () => {
    writeHash();
    try {
      await navigator.clipboard.writeText(location.href);
      el.copy.textContent = 'Copied';
    } catch {
      el.copy.textContent = 'Copy the address bar';
    }
    setTimeout(() => { el.copy.textContent = 'Copy link'; }, 2000);
  });

  (async () => {
    try {
      const cfg = await (await fetch('api/config', { cache: 'no-store' })).json();
      loginEnabled = Boolean(cfg.login);
    } catch { /* load() reports an unreachable service */ }
    load();
  })();
  setInterval(() => load(), POLL_MS);
  setInterval(describe, 30_000);
  setInterval(countdown, COUNTDOWN_MS);
  document.addEventListener('visibilitychange', countdown);
})();
