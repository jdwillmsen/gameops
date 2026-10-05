'use strict';

// Players and mobs, drawn where they are now. The server streams one whole
// picture of the current dimension about once a second; this keeps a marker
// per entity and moves it, so a thousand of them cost a thousand position
// updates and one repaint.
(() => {
  const app = window.mcmap;
  if (!app) return;
  const { map } = app;

  const SETTINGS_KEY = 'mcmap.live';
  // How long to leave it after the browser gives up on the stream before
  // asking whether the session is still good.
  const GIVE_UP_RETRY_MS = 5000;
  const READOUT_MS = 250;

  const CATEGORIES = {
    players: '#ffffff',
    hostile: '#e5534b',
    passive: '#5cc8f0',
    villager: '#d9a441',
    other: '#9aa3ad',
  };
  const ME = '#6ecf7a';

  // Type ids as the server reports them, without the minecraft: prefix.
  // Anything not listed is "other", which is where a mob added by a later
  // game version lands until it is put here.
  const TYPES = {
    hostile: `zombie husk drowned zombie_villager zombie_villager_v2 skeleton stray bogged wither_skeleton
      creeper spider cave_spider enderman endermite silverfish witch slime magma_cube blaze ghast phantom
      guardian elder_guardian shulker pillager vindicator evocation_illager ravager vex hoglin zoglin piglin
      piglin_brute zombie_pigman wither ender_dragon warden breeze creaking`,
    passive: `cow mooshroom pig sheep chicken horse donkey mule skeleton_horse zombie_horse llama trader_llama
      camel rabbit wolf cat ocelot fox parrot bat bee turtle dolphin cod salmon pufferfish tropicalfish squid
      glow_squid axolotl frog tadpole goat panda polar_bear strider allay sniffer armadillo iron_golem
      snow_golem copper_golem happy_ghast`,
    villager: 'villager villager_v2 wandering_trader',
  };
  const categoryOf = new Map();
  for (const [category, ids] of Object.entries(TYPES)) {
    for (const id of ids.split(/\s+/)) categoryOf.set(id, category);
  }

  const el = {
    filters: document.getElementById('live-filters'),
    readout: document.getElementById('live'),
  };
  const chips = new Map([...el.filters.querySelectorAll('button[data-live]')].map((b) => [b.dataset.live, b]));

  const settings = { on: true, players: true, hostile: true, passive: true, villager: true, other: true };
  try {
    const saved = JSON.parse(localStorage.getItem(SETTINGS_KEY) || '{}');
    for (const key of Object.keys(settings)) if (typeof saved[key] === 'boolean') settings[key] = saved[key];
  } catch { /* a browser that refuses storage still gets the defaults */ }

  // One canvas for every marker. Leaflet's default draws each as its own
  // SVG element, which is fine for a grid and not for a thousand mobs moving
  // every second. Only this layer is on the canvas: switching the whole map
  // over would change how everything else on it draws.
  const renderer = L.canvas({ padding: 0.5 });

  // A player's marker points the way they face. Leaflet's canvas has no
  // such shape, so this draws into the renderer's own context, the way its
  // built-in circle does; _renderer, _ctx and _point are Leaflet internals,
  // which is safe only because Leaflet is vendored at a fixed version.
  const Arrow = L.CircleMarker.extend({
    _updatePath() {
      const r = this._renderer;
      if (!r._drawing || this._empty()) return;
      const ctx = r._ctx;
      const p = this._point;
      const size = this._radius;
      ctx.beginPath();
      if (Number.isFinite(this.options.yaw)) {
        // Yaw 0 faces south, which is down the map, and turns clockwise
        // seen from above, as a canvas rotation does.
        ctx.save();
        ctx.translate(p.x, p.y);
        ctx.rotate((this.options.yaw * Math.PI) / 180);
        ctx.moveTo(0, size);
        ctx.lineTo(size * 0.75, -size * 0.75);
        ctx.lineTo(0, -size * 0.3);
        ctx.lineTo(-size * 0.75, -size * 0.75);
        ctx.closePath();
        ctx.restore();
      } else {
        ctx.arc(p.x, p.y, size * 0.6, 0, Math.PI * 2, false);
      }
      r._fillStroke(ctx, this);
    },
  });

  // Names reach here from the game, where players choose them, and Leaflet
  // treats a string given to a tooltip as HTML. An element holding text is
  // the form it cannot interpret.
  const text = (s) => {
    const span = document.createElement('span');
    span.textContent = s;
    return span;
  };

  const mobLayer = L.featureGroup();
  const playerLayer = L.featureGroup();
  mobLayer.bindTooltip((marker) => text(marker.options.label), { sticky: true, direction: 'top', className: 'live-tip' });

  // key -> { marker, category, x, z, yaw }
  const entities = new Map();

  let me = null;
  let source = null;
  let streamDimension = null;
  let retryTimer = null;
  // Server clock minus this one, taken from the first frame of a stream.
  let clockOffset = 0;
  let awaitingFirst = false;
  let frameAt = null;
  let frameSeen = 0;
  let ttlMs = 10_000;
  let stale = true;
  let more = 0;

  const fmt = (n) => n.toLocaleString('en-US');
  const layerOf = (category) => (category === 'players' ? playerLayer : mobLayer);

  function mobLabel(e) {
    const type = (e.t || 'unknown').replace(/_/g, ' ');
    return e.n ? `${e.n} (${type})` : type;
  }

  function make(e, category) {
    if (category === 'players') {
      const mine = me !== null && typeof e.n === 'string' && e.n.toLowerCase() === me;
      const marker = new Arrow([e.z, e.x], {
        renderer,
        radius: mine ? 10 : 8,
        yaw: e.r,
        color: '#0b0c0e',
        weight: 1.5,
        fillColor: mine ? ME : CATEGORIES.players,
        fillOpacity: 1,
      });
      marker.bindTooltip(text(e.n || 'Player'), {
        permanent: true,
        direction: 'top',
        offset: [0, -8],
        className: mine ? 'live-name me' : 'live-name',
      });
      return marker;
    }
    return L.circleMarker([e.z, e.x], {
      renderer,
      radius: 3.5,
      color: '#0b0c0e',
      weight: 1,
      fillColor: CATEGORIES[category],
      fillOpacity: 1,
      label: mobLabel(e),
    });
  }

  function forget(key) {
    const held = entities.get(key);
    layerOf(held.category).removeLayer(held.marker);
    entities.delete(key);
  }

  function clear() {
    mobLayer.clearLayers();
    playerLayer.clearLayers();
    entities.clear();
    stale = true;
    more = 0;
    frameAt = null;
    count();
  }

  function draw(frame) {
    const seen = new Set();
    let added = false;
    const place = (e, category, prefix) => {
      const key = prefix + e.i;
      seen.add(key);
      let held = entities.get(key);
      if (held && held.category !== category) {
        forget(key);
        held = null;
      }
      if (!held) {
        held = { marker: make(e, category), category, x: e.x, z: e.z, yaw: e.r };
        entities.set(key, held);
        if (settings[category]) layerOf(category).addLayer(held.marker);
        added = true;
        return;
      }
      if (held.x !== e.x || held.z !== e.z) {
        held.x = e.x;
        held.z = e.z;
        held.marker.setLatLng([e.z, e.x]);
      }
      if (category === 'players') {
        if (held.yaw !== e.r) {
          held.yaw = e.r;
          held.marker.options.yaw = e.r;
          held.marker.redraw();
        }
      } else {
        held.marker.options.label = mobLabel(e);
      }
    };
    for (const e of frame.players || []) place(e, 'players', 'p:');
    for (const e of frame.mobs || []) place(e, categoryOf.get(e.t) || 'other', 'm:');
    for (const key of [...entities.keys()]) if (!seen.has(key)) forget(key);
    // The canvas paints in the order markers were added; players stay on
    // top of a mob that arrived after them.
    if (added) playerLayer.eachLayer((marker) => marker.bringToFront());
    count();
  }

  function count() {
    const totals = { players: 0, hostile: 0, passive: 0, villager: 0, other: 0 };
    for (const held of entities.values()) totals[held.category] += 1;
    for (const [category, n] of Object.entries(totals)) {
      chips.get(category).querySelector('.count').textContent = stale ? '' : fmt(n);
    }
  }

  function onFrame(frame) {
    if (awaitingFirst) {
      awaitingFirst = false;
      clockOffset = Date.parse(frame.serverNow) - Date.now();
    }
    frameSeen = Date.now();
    if (Number.isFinite(frame.ttlSeconds) && frame.ttlSeconds > 0) ttlMs = frame.ttlSeconds * 1000;
    stale = Boolean(frame.stale);
    more = frame.more || 0;
    frameAt = frame.at ? Date.parse(frame.at) : null;
    draw(frame);
    readout();
  }

  function close() {
    clearTimeout(retryTimer);
    if (source) source.close();
    source = null;
    streamDimension = null;
  }

  function open(dimension) {
    close();
    clear();
    streamDimension = dimension;
    awaitingFirst = true;
    const es = new EventSource(`api/live?dimension=${encodeURIComponent(dimension)}`);
    source = es;
    es.onmessage = (ev) => {
      if (source !== es) return;
      let frame;
      try { frame = JSON.parse(ev.data); } catch { return; }
      onFrame(frame);
    };
    es.onerror = () => {
      if (source !== es) return;
      // A dropped connection the browser retries by itself, and says so by
      // staying in the connecting state.
      if (es.readyState !== EventSource.CLOSED) return;
      // Closed means the answer was not a stream: the session has ended, or
      // the service is refusing new streams. Reloading the map tells which.
      // Logged out, it shows the login and this stays shut; otherwise it
      // announces the view again and the stream is reopened.
      close();
      retryTimer = setTimeout(() => app.reload(), GIVE_UP_RETRY_MS);
    };
  }

  // Brings the stream in line with what should be open now: the current
  // dimension's, and only while there is someone logged in and looking.
  function sync() {
    const available = app.live();
    el.filters.hidden = !available;
    el.readout.hidden = !available;
    const locked = document.body.classList.contains('locked');
    const want = available && settings.on && !locked && !document.hidden ? app.dimension() : null;
    if (!want) {
      close();
      clear();
    } else if (want !== streamDimension) {
      open(want);
      if (me === null && !locked) identify();
    }
    readout();
  }

  // Which marker is the visitor's own. Without a login there is no answer
  // and no marker is singled out.
  async function identify() {
    me = '';
    try {
      const res = await fetch('api/me', { cache: 'no-store' });
      if (!res.ok) return;
      me = String((await res.json()).gamertag || '').toLowerCase();
    } catch { /* the highlight is decoration */ }
    // Players drawn before the answer came are redrawn by the next frame,
    // this time with the visitor's own picked out.
    for (const [key, held] of [...entities]) if (held.category === 'players') forget(key);
  }

  function readout() {
    let state;
    if (!settings.on) {
      state = 'off';
    } else if (!source) {
      state = 'paused';
    } else if (Date.now() - frameSeen > ttlMs && entities.size) {
      // Nothing has arrived for as long as a position is good for, so what
      // is on screen is no longer where anyone is.
      clear();
      state = 'reconnecting';
    } else if (source.readyState !== EventSource.OPEN) {
      state = 'connecting';
    } else if (stale || frameAt === null) {
      state = 'no data';
    } else {
      const age = Math.max(0, (Date.now() + clockOffset - frameAt) / 1000);
      state = `${age.toFixed(1)} s`;
    }
    let line = `live · ${state}`;
    if (more > 0 && !stale) {
      const shown = [...entities.values()].filter((held) => held.category !== 'players').length;
      line += ` · showing ${fmt(shown)} of ${fmt(shown + more)} mobs`;
    }
    el.readout.textContent = line;
  }

  function save() {
    try { localStorage.setItem(SETTINGS_KEY, JSON.stringify(settings)); } catch { /* not kept, still applied */ }
  }

  function paint() {
    for (const [key, chip] of chips) {
      chip.setAttribute('aria-pressed', String(settings[key]));
      if (key !== 'on') chip.disabled = !settings.on;
    }
  }

  for (const [key, chip] of chips) {
    chip.addEventListener('click', () => {
      settings[key] = !settings[key];
      save();
      paint();
      if (key === 'on') {
        sync();
        return;
      }
      for (const held of entities.values()) {
        if (held.category !== key) continue;
        if (settings[key]) layerOf(key).addLayer(held.marker); else layerOf(key).removeLayer(held.marker);
      }
      if (settings[key]) playerLayer.eachLayer((marker) => marker.bringToFront());
    });
  }

  mobLayer.addTo(map);
  playerLayer.addTo(map);
  paint();
  document.addEventListener('mcmap:view', sync);
  document.addEventListener('visibilitychange', sync);
  setInterval(readout, READOUT_MS);
  sync();
})();
