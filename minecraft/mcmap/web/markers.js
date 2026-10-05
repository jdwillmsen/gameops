'use strict';

// What stays where it is: the player's own waypoints, and the beds,
// containers and named mobs read from the world at the last snapshot. They
// change at most every few minutes, so they are fetched, not streamed, and
// drawn as rings so that they are never mistaken for the live layer's dots.
(() => {
  const app = window.mcmap;
  if (!app) return;
  const { map } = app;

  const SETTINGS_KEY = 'mcmap.markers';
  // Several things announce a change of view at once; one fetch answers
  // them all.
  const SETTLE_MS = 50;

  const KINDS = {
    waypoints: { color: '#b48cf2', radius: 6 },
    beds: { color: '#f277b5', radius: 4 },
    containers: { color: '#f08a3c', radius: 4 },
    mobs: { color: '#4fd1c5', radius: 5 },
  };
  const WORLD_KINDS = ['beds', 'containers', 'mobs'];
  const CONTAINERS = { chest: 'Chest', barrel: 'Barrel', shulker: 'Shulker box' };

  const el = { filters: document.getElementById('marker-filters') };
  if (!el.filters) return;
  const chips = new Map([...el.filters.querySelectorAll('button[data-marker]')].map((b) => [b.dataset.marker, b]));

  const settings = { waypoints: true, beds: true, containers: true, mobs: true };
  try {
    const saved = JSON.parse(localStorage.getItem(SETTINGS_KEY) || '{}');
    for (const key of Object.keys(settings)) if (typeof saved[key] === 'boolean') settings[key] = saved[key];
  } catch { /* a browser that refuses storage still gets the defaults */ }

  // A pane of their own, above the live layer's canvas. A canvas takes
  // every pointer event over the map, so anything drawn under one cannot be
  // hovered; these are SVG, which takes them only on the shapes themselves,
  // and so leaves the live markers beneath as reachable as before.
  map.createPane('markers').style.zIndex = 450;
  const renderer = L.svg({ pane: 'markers', padding: 0.5 });

  // Names reach here from the game and from chat, where players choose
  // them, and Leaflet treats a string given to a tooltip as HTML. An
  // element holding text is the form it cannot interpret.
  const text = (s) => {
    const span = document.createElement('span');
    span.textContent = s;
    return span;
  };

  const layers = {};
  for (const kind of Object.keys(KINDS)) {
    layers[kind] = L.featureGroup();
    layers[kind].bindTooltip((marker) => text(marker.options.label), { sticky: true, direction: 'top', className: 'marker-tip' });
  }

  // Whether the service has each kind at all; null until it has answered.
  const available = { world: null, waypoints: null };
  const totals = { waypoints: null, beds: null, containers: null, mobs: null };
  const more = { waypoints: 0, beds: 0, containers: 0, mobs: 0 };
  // What is drawn now, so that an unchanged answer redraws nothing.
  let drawn = { dimension: null, etag: null };
  let waypoints = [];
  let timer = null;
  let asked = 0;

  const fmt = (n) => n.toLocaleString('en-US');
  const at = (m) => `${fmt(m.x)}, ${fmt(m.y)}, ${fmt(m.z)}`;
  const str = (v) => (typeof v === 'string' ? v : '');

  function label(kind, m) {
    if (kind === 'beds') return `Bed · ${at(m)}`;
    if (kind === 'containers') {
      const what = CONTAINERS[m.k] || 'Container';
      return str(m.n) ? `${str(m.n)} (${what.toLowerCase()}) · ${at(m)}` : `${what} · ${at(m)}`;
    }
    if (kind === 'mobs') return `${str(m.n)} (${str(m.k).replace(/_/g, ' ') || 'mob'}) · ${at(m)}`;
    return `${str(m.name)} · ${at(m)}`;
  }

  function ring(kind, m) {
    const style = KINDS[kind];
    // The middle of the block, not its north-west corner.
    return L.circleMarker([m.z + 0.5, m.x + 0.5], {
      renderer,
      pane: 'markers',
      radius: style.radius,
      color: style.color,
      weight: 2,
      fillColor: '#0b0c0e',
      fillOpacity: 0.75,
      label: label(kind, m),
    });
  }

  const placed = (m) => m && Number.isFinite(m.x) && Number.isFinite(m.y) && Number.isFinite(m.z);

  function fill(kind, list) {
    layers[kind].clearLayers();
    let n = 0;
    for (const m of list) {
      if (!placed(m)) continue;
      const marker = ring(kind, m);
      if (kind === 'waypoints') {
        // A waypoint is the one marker a player put there by name, so the
        // name is always showing.
        marker.bindTooltip(text(str(m.name)), { permanent: true, direction: 'right', offset: [8, 0], className: 'marker-name' });
      }
      layers[kind].addLayer(marker);
      n += 1;
    }
    totals[kind] = n;
  }

  function show(kind) {
    if (settings[kind]) layers[kind].addTo(map); else map.removeLayer(layers[kind]);
  }

  function paint() {
    const has = { waypoints: available.waypoints, beds: available.world, containers: available.world, mobs: available.world };
    let any = false;
    for (const [kind, chip] of chips) {
      chip.hidden = !has[kind];
      any = any || Boolean(has[kind]);
      chip.setAttribute('aria-pressed', String(settings[kind]));
      chip.querySelector('.count').textContent = totals[kind] === null ? '' : fmt(totals[kind]);
      chip.title = more[kind] > 0 ? `${fmt(more[kind])} more are not shown` : '';
    }
    el.filters.hidden = !any;
  }

  function clear() {
    for (const kind of Object.keys(KINDS)) {
      layers[kind].clearLayers();
      totals[kind] = null;
      more[kind] = 0;
    }
    drawn = { dimension: null, etag: null };
    waypoints = [];
    paint();
  }

  function drawWaypoints(dimension) {
    fill('waypoints', waypoints.filter((w) => w && w.dimension === dimension));
  }

  async function loadWorld(dimension, turn) {
    let res;
    try {
      // The browser keeps the last answer and asks whether it still
      // stands, so an unchanged world costs a 304.
      res = await fetch(`api/markers?dimension=${encodeURIComponent(dimension)}`);
    } catch {
      return; // what is drawn stays; the next announcement asks again
    }
    if (turn !== asked) return;
    if (res.status === 404) {
      available.world = false;
      return;
    }
    if (!res.ok) return;
    const etag = res.headers.get('ETag');
    let doc;
    try { doc = await res.json(); } catch { return; }
    if (turn !== asked) return;
    available.world = true;
    if (drawn.dimension === dimension && etag && drawn.etag === etag) return;
    drawn = { dimension, etag };
    for (const kind of WORLD_KINDS) {
      fill(kind, Array.isArray(doc[kind]) ? doc[kind] : []);
      more[kind] = (doc.more && Number.isFinite(doc.more[kind])) ? doc.more[kind] : 0;
    }
  }

  async function loadWaypoints(dimension, turn) {
    let res;
    try {
      res = await fetch('api/waypoints', { cache: 'no-store' });
    } catch {
      return;
    }
    if (turn !== asked) return;
    if (res.status === 404) {
      available.waypoints = false;
      return;
    }
    // Shown as a filter even while the agent cannot be reached: the layer
    // exists, it just has nothing to draw until the next try.
    available.waypoints = true;
    if (!res.ok) return;
    let doc;
    try { doc = await res.json(); } catch { return; }
    if (turn !== asked) return;
    waypoints = Array.isArray(doc.waypoints) ? doc.waypoints : [];
    more.waypoints = Number.isFinite(doc.more) ? doc.more : 0;
    drawWaypoints(dimension);
  }

  async function refresh() {
    const dimension = app.dimension();
    if (document.body.classList.contains('locked')) {
      // Logged out: nothing of the last player's stays on the page.
      asked += 1;
      clear();
      return;
    }
    if (!dimension || document.hidden) return;
    const turn = asked += 1;
    if (drawn.dimension !== null && drawn.dimension !== dimension) {
      // Another dimension's markers are wrong here, not merely old.
      for (const kind of WORLD_KINDS) layers[kind].clearLayers();
      drawn = { dimension: null, etag: null };
      drawWaypoints(dimension);
    }
    const jobs = [];
    if (available.world !== false) jobs.push(loadWorld(dimension, turn));
    if (available.waypoints !== false) jobs.push(loadWaypoints(dimension, turn));
    await Promise.all(jobs);
    if (turn === asked) paint();
  }

  function sync() {
    clearTimeout(timer);
    timer = setTimeout(refresh, SETTLE_MS);
  }

  function save() {
    try { localStorage.setItem(SETTINGS_KEY, JSON.stringify(settings)); } catch { /* not kept, still applied */ }
  }

  for (const [kind, chip] of chips) {
    chip.addEventListener('click', () => {
      settings[kind] = !settings[kind];
      save();
      show(kind);
      paint();
    });
    show(kind);
  }

  paint();
  document.addEventListener('mcmap:view', sync);
  document.addEventListener('visibilitychange', sync);
  sync();
})();
