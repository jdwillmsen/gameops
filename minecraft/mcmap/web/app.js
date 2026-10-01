'use strict';

(() => {
  // A region is the renderer's unit of extent: 512 blocks square.
  const REGION = 512;
  const TILE = 256;
  const POLL_MS = 60_000;
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
    grid: document.getElementById('grid'),
    copy: document.getElementById('copy'),
    goto: document.getElementById('goto'),
    gotoX: document.getElementById('goto-x'),
    gotoZ: document.getElementById('goto-z'),
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
  let current = null;
  let layer = null;
  // Leaflet's grid layers default to zoom 0 and up; this map lives below it.
  const grid = new Grid({ tileSize: TILE, zIndex: 5, minZoom: -12, maxZoom: 8 });

  const dimension = (id) => info && info.dimensions.find((d) => d.id === id);
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
      const every = Math.round(info.refreshSeconds / 60);
      const when = d.renderedAt ? `Updated ${ago(d.renderedAt)}` : 'Rendered before the last restart';
      el.status.textContent = info.problem
        ? `${when}. The last refresh failed, so this is the previous map.`
        : `${when} · refreshes every ${every} min`;
    }
  }

  async function load() {
    let next;
    try {
      const res = await fetch('api/map', { cache: 'no-store' });
      if (!res.ok) throw new Error(String(res.status));
      next = await res.json();
    } catch {
      el.status.classList.add('problem');
      el.status.textContent = 'Cannot reach the map service. Retrying.';
      return;
    }
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
  }

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

  load();
  setInterval(load, POLL_MS);
  setInterval(describe, 30_000);
})();
