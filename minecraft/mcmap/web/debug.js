'use strict';

// The debug panel, its visual aids, the report for a bug and the ten
// second recording. All of it is the viewer's to switch on, from the help
// list, by D, or by ?debug=1 in a link, and while it is off this file holds
// one listener, for the setting itself, and nothing else: no timer, no
// frame loop, no listener on the map or the page. What the report holds is
// told in the help list; nothing in it is sent anywhere.
(() => {
  const app = window.mcmap;
  if (!app || !app.map) return;
  const { map } = app;
  const settings = window.mcmapSettings || null;
  const keep = window.mcmapDiag && window.mcmapDiag.keep;
  const tools = window.mcmapDiag || null;
  const zoomModel = window.mcmapZoom || null;

  const el = {
    panel: document.getElementById('debug'),
    fold: document.getElementById('debug-fold'),
    close: document.getElementById('debug-close'),
    body: document.getElementById('debug-body'),
    rows: document.getElementById('debug-rows'),
    report: document.getElementById('debug-report'),
    copy: document.getElementById('debug-copy'),
    download: document.getElementById('debug-download'),
    record: document.getElementById('debug-record'),
    position: document.getElementById('debug-position'),
    said: document.getElementById('debug-said'),
    on: document.getElementById('debug-on'),
    reportOn: document.getElementById('debug-report-on'),
    helpReport: document.getElementById('debug-help-report'),
    helpCopy: document.getElementById('debug-help-copy'),
    helpDownload: document.getElementById('debug-help-download'),
    helpPosition: document.getElementById('debug-help-position'),
  };
  if (!settings || !keep || !tools || Object.values(el).some((node) => !node)) return;

  const UPDATE_MS = 250;
  const SECOND_MS = 1000;
  const RECORD_MS = 10_000;
  const RECORD_MAX = 1200;
  const SCRIPTS_MS = 60_000;
  const BYTES = new TextEncoder();

  const read = () => settings.get('debug');
  const write = (patch) => settings.set('debug', { ...read(), ...patch });
  // Never kept: asked for each time the report is made.
  let includePosition = false;

  // --- what there is to say ----------------------------------------------------------

  const fixed = (n, places = 1) => (Number.isFinite(n) ? n.toFixed(places) : '—');
  const age = (ms) => {
    if (!Number.isFinite(ms) || ms < 0) return '—';
    if (ms < 1000) return `${Math.round(ms)} ms`;
    if (ms < 90_000) return `${(ms / 1000).toFixed(1)} s`;
    if (ms < 5_400_000) return `${Math.round(ms / 60_000)} min`;
    return `${(ms / 3_600_000).toFixed(1)} h`;
  };
  const since = (iso) => (iso ? age(Date.now() - Date.parse(iso)) : '—');

  let pointer = null;
  let frameTimes = [];
  let served = null;
  let servedAt = 0;
  let scripts = null;
  let scriptsAt = 0;
  let recording = null;
  let lastRecording = null;
  const failedTiles = new Map();
  const watched = new Set();

  const gridLayers = () => {
    const out = [];
    map.eachLayer((layer) => { if (typeof L.GridLayer === 'function' && layer instanceof L.GridLayer) out.push(layer); });
    return out;
  };
  const tileName = (layer) => (layer.options.pick !== undefined ? 'biome overlay' : layer.options.format ? 'terrain' : 'canvas overlay');

  function tileCounts(layer) {
    let loading = 0;
    let total = 0;
    for (const tile of Object.values(layer._tiles || {})) {
      total += 1;
      if (!tile.loaded) loading += 1;
    }
    return { loading, total };
  }

  const frameStats = () => {
    const now = performance.now();
    const recent = frameTimes.filter((f) => now - f.at <= SECOND_MS).map((f) => f.dt).sort((a, b) => a - b);
    if (recent.length === 0) return { median: NaN, worst: NaN, frames: 0 };
    return { median: recent[Math.floor(recent.length / 2)], worst: recent[recent.length - 1], frames: recent.length };
  };

  const box = (b) => ({ left: b.left, top: b.top, right: b.right, bottom: b.bottom });
  // The part of the map the follow code keeps its target in: the map's own
  // box less whatever lies over it.
  function visibleArea() {
    if (!app.room || !app.room.free) return null;
    const container = map.getContainer().getBoundingClientRect();
    const covers = [];
    for (const selector of ['#layers .panel', '#inspect', '#chunk']) {
      const node = document.querySelector(selector);
      if (node && node.getClientRects().length > 0) covers.push(box(node.getBoundingClientRect()));
    }
    return { container: box(container), free: app.room.free(box(container), covers) };
  }

  function marks() {
    const renderer = app.liveRenderer;
    const layers = renderer && renderer._layers ? Object.values(renderer._layers) : [];
    return { count: layers.length, layers };
  }

  function groups(withPosition) {
    const zoom = map.getZoom();
    const size = map.getSize();
    const look = settings.look();
    const stats = frameStats();
    const stream = keep.stream;
    const out = [];
    const px = Number.isFinite(zoom) ? 2 ** zoom : NaN;
    out.push({
      title: 'Zoom',
      rows: [
        ['zoom', Number.isFinite(zoom) ? zoom.toFixed(4) : '—'],
        ['scale', zoomModel && Number.isFinite(zoom) ? zoomModel.scale(zoom) : '—'],
        ['pixels a block', fixed(px, 3)],
        ['range', `${fixed(map.getMinZoom(), 2)} to ${fixed(map.getMaxZoom(), 2)}`],
        ['moving to', app.zoom && app.zoom.running() ? fixed(app.zoom.running().target, 3) : 'at rest'],
      ],
    });
    if (withPosition) {
      const c = map.getCenter();
      const b = map.getBounds();
      const rows = [
        ['centre', `${fixed(c.lng)}, ${fixed(c.lat)} (x, z)`],
        ['bounds', `x ${fixed(b.getWest(), 0)} to ${fixed(b.getEast(), 0)}, z ${fixed(b.getNorth(), 0)} to ${fixed(b.getSouth(), 0)}`],
      ];
      if (pointer) {
        const x = Math.floor(pointer.lng);
        const z = Math.floor(pointer.lat);
        const dim = app.dimension && app.dimension();
        rows.push(['pointer block', `${x}, ${z}`], ['pointer chunk', `${Math.floor(x / 16)}, ${Math.floor(z / 16)}`], ['pointer region', `${Math.floor(x / 512)}, ${Math.floor(z / 512)}`]);
        if (dim === 'overworld') rows.push(['in the nether', `${Math.floor(x / 8)}, ${Math.floor(z / 8)}`]);
        if (dim === 'nether') rows.push(['in the overworld', `${x * 8}, ${z * 8}`]);
      } else rows.push(['pointer', 'not over the map']);
      const room = visibleArea();
      if (room) rows.push(['follow area', `${Math.round(room.free.left - room.container.left)},${Math.round(room.free.top - room.container.top)} to ${Math.round(room.free.right - room.container.left)},${Math.round(room.free.bottom - room.container.top)} px of the map`]);
      out.push({ title: 'Place', rows });
    }
    out.push({
      title: 'Screen',
      rows: [
        ['device pixel ratio', fixed(window.devicePixelRatio, 3)],
        ['sprite density', app.icons && app.icons.density ? String(app.icons.density()) : '—'],
        ['window', `${innerWidth} × ${innerHeight}`],
        ['map', `${size.x} × ${size.y}`],
      ],
    });
    out.push({
      title: 'Markers',
      rows: [['style', look.style], ['size', look.size], ['label size', look.text], ['mob pictures', look.mobPicture], ['theme', look.theme], ['density', look.density], ['motion', look.motion]],
    });
    const tiles = gridLayers().map((layer) => {
      const o = layer.options;
      const n = tileCounts(layer);
      const failed = failedTiles.get(layer) || 0;
      return [tileName(layer), `native ${o.minNativeZoom ?? '—'} to ${o.maxNativeZoom ?? '—'}, tiles at ${layer._tileZoom ?? '—'}, ${n.total} held, ${n.loading} loading, ${failed} failed`];
    });
    out.push({ title: 'Tile layers', rows: tiles.length > 0 ? tiles : [['none', '']] });
    const sent = stream.entities;
    const drawn = marks().count;
    const frames = stream.frames.filter((t) => Date.now() - t <= 60_000).length;
    out.push({
      title: 'Live stream',
      rows: [
        ['connected', stream.opened === 0 ? 'no stream opened' : stream.connected ? 'yes' : 'no'],
        ['last frame', stream.lastFrame ? `${age(Date.now() - stream.lastFrame)} ago` : '—'],
        ['frames a minute', String(frames)],
        ['in the frame', sent ? `${sent.players} players, ${sent.mobs} mobs` : 'waiting for a frame'],
        ['held back by the server', sent ? `${sent.more + sent.morePlayers}` : '—'],
        ['canvas marks drawn', String(drawn)],
        ['reconnects', String(Math.max(0, stream.opened - 1))],
        ['errors', String(stream.errors)],
      ],
    });
    const seen = keep.snapshots();
    const diagnostics = served;
    out.push({
      title: 'Snapshots',
      rows: [
        ['terrain', diagnostics ? since(Object.values(diagnostics.renderedAt || {}).sort().at(-1)) : '—'],
        ['markers', seen.markers ? age(Date.now() - Date.parse(seen.markers.at)) : 'not seen since this was on'],
        ['structures', seen.structures ? age(Date.now() - Date.parse(seen.structures.at)) : 'not seen since this was on'],
        ['biomes', seen.biomes ? age(Date.now() - Date.parse(seen.biomes.at)) : 'not seen since this was on'],
      ],
    });
    const long = keep.longTasks();
    out.push({
      title: 'Drawing',
      rows: [
        ['frame time, last second', stats.frames ? `median ${fixed(stats.median)} ms, worst ${fixed(stats.worst)} ms (${stats.frames} frames)` : '—'],
        ['long tasks', `${long.count}, longest ${long.longest} ms`],
      ],
    });
    out.push({
      title: 'Page',
      rows: [
        ['scripts', scripts ? `${scripts.verdict}${scripts.stale.length ? `: ${scripts.stale.join(', ')}` : ''}` : 'checking…'],
        ['settings', `version ${settings.VERSION}, ${settings.size()} characters, storage ${settings.kept()}`],
        ['errors', keep.errors().length === 0 ? 'none' : keep.errors().map((e) => e.text).join(' | ')],
      ],
    });
    return out;
  }

  // --- the panel -----------------------------------------------------------------------

  // The panel is made once and its figures written in place, and only where
  // they have changed, so that it costs no layout while it is read.
  let built = [];
  function paint() {
    // Before the map has been given a view there is nothing to say of it.
    if (!map._loaded) return;
    const now = groups(true);
    const shape = now.map((g) => `${g.title}:${g.rows.map((r) => r[0]).join(',')}`).join('|');
    if (built.shape !== shape) {
      el.rows.replaceChildren();
      built = now.map((g) => {
        const section = document.createElement('section');
        const h = document.createElement('h3');
        h.textContent = g.title;
        const dl = document.createElement('dl');
        const cells = g.rows.map((r) => {
          const dt = document.createElement('dt');
          const dd = document.createElement('dd');
          dt.textContent = r[0];
          dl.append(dt, dd);
          return dd;
        });
        section.append(h, dl);
        el.rows.append(section);
        return cells;
      });
      built.shape = shape;
    }
    now.forEach((g, i) => g.rows.forEach((r, j) => {
      if (built[i][j].textContent !== r[1]) built[i][j].textContent = r[1];
    }));
  }

  // --- what is watched while it is on ---------------------------------------------------

  let timer = 0;
  let frame = 0;
  let last = 0;
  const listening = [];
  const onMap = (events, fn) => {
    map.on(events, fn);
    listening.push([events, fn]);
  };

  function tick(now) {
    frame = requestAnimationFrame(tick);
    if (last) frameTimes.push({ at: now, dt: now - last });
    last = now;
    if (frameTimes.length > 240) frameTimes = frameTimes.slice(-180);
    if (recording) sample(now);
  }

  function watch(layer) {
    if (watched.has(layer)) return;
    watched.add(layer);
    layer.on('tileerror', countFailure, layer);
  }
  function countFailure() {
    failedTiles.set(this, (failedTiles.get(this) || 0) + 1);
    keep.log('tiles', `a tile failed to load (${tileName(this)})`);
  }
  function unwatchAll() {
    for (const layer of watched) layer.off('tileerror', countFailure, layer);
    watched.clear();
    failedTiles.clear();
  }

  // One question at a time, and its answer kept for a minute.
  let asking = null;
  function fetchServed() {
    if (asking && Date.now() - servedAt < SCRIPTS_MS) return asking;
    servedAt = Date.now();
    asking = (async () => {
      try {
        const res = await fetch('api/diagnostics', { cache: 'no-store' });
        served = res.ok ? await res.json() : { unavailable: res.status };
      } catch {
        served = { unavailable: 0 };
      }
      return served;
    })();
    return asking;
  }

  // Which scripts the page ran are the ones the server has now: each is
  // read from the browser's own cache and fingerprinted as the server does.
  async function checkScripts() {
    if (scripts && Date.now() - scriptsAt < SCRIPTS_MS) return scripts;
    scriptsAt = Date.now();
    const server = await fetchServed();
    if (!server || !server.assets) {
      scripts = { verdict: 'not checked: the server did not say what it has', stale: [], current: 0, unknown: [] };
      return scripts;
    }
    const names = [...document.scripts].map((s) => s.getAttribute('src')).filter((src) => src && !src.includes('/') && !src.includes(':'));
    const loaded = {};
    for (const name of [...new Set([...names, 'index.html'])]) {
      try {
        const res = await fetch(name === 'index.html' ? (location.pathname || '/') : name, { cache: 'force-cache' });
        if (res.ok) loaded[name] = tools.fingerprint(new Uint8Array(await res.arrayBuffer()));
      } catch { /* left out, and so not called old */ }
    }
    scripts = tools.mixed(loaded, server.assets);
    if (scripts.verdict !== 'current') keep.log('versions', scripts.verdict);
    return scripts;
  }

  // --- visual aids ------------------------------------------------------------------------

  const aids = { canvas: null, ctx: null, pending: 0 };
  const anyAid = () => Object.values(read().aids || {}).some(Boolean);

  function drawAids() {
    aids.pending = 0;
    if (!aids.canvas) return;
    const on = read().aids || {};
    const rect = map.getContainer().getBoundingClientRect();
    const ratio = window.devicePixelRatio || 1;
    const w = Math.round(rect.width * ratio);
    const h = Math.round(rect.height * ratio);
    if (aids.canvas.width !== w || aids.canvas.height !== h) {
      aids.canvas.width = w;
      aids.canvas.height = h;
    }
    const ctx = aids.ctx;
    ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
    ctx.clearRect(0, 0, rect.width, rect.height);
    ctx.font = '11px ui-monospace, monospace';
    ctx.lineWidth = 1;
    const label = (text, x, y, colour) => {
      const width = ctx.measureText(text).width + 6;
      ctx.fillStyle = 'rgba(0,0,0,0.75)';
      ctx.fillRect(x, y, width, 14);
      ctx.fillStyle = colour;
      ctx.fillText(text, x + 3, y + 11);
    };
    if (on.lines) {
      const b = map.getBounds();
      const scale = 2 ** map.getZoom();
      for (const [step, colour, width] of [[16, 'rgba(255,0,200,0.7)', 1], [512, 'rgba(0,220,255,0.9)', 2]]) {
        if (step * scale < 4) continue;
        ctx.strokeStyle = colour;
        ctx.lineWidth = width;
        ctx.beginPath();
        for (let x = Math.ceil(b.getWest() / step) * step; x < b.getEast(); x += step) {
          const p = map.latLngToContainerPoint([0, x]).x + 0.5;
          ctx.moveTo(p, 0);
          ctx.lineTo(p, rect.height);
        }
        for (let z = Math.ceil(b.getNorth() / step) * step; z < b.getSouth(); z += step) {
          const p = map.latLngToContainerPoint([z, 0]).y + 0.5;
          ctx.moveTo(0, p);
          ctx.lineTo(rect.width, p);
        }
        ctx.stroke();
      }
      ctx.lineWidth = 1;
    }
    if (on.tiles) {
      ctx.strokeStyle = 'rgba(255,220,0,0.9)';
      for (const layer of gridLayers()) {
        for (const tile of Object.values(layer._tiles || {})) {
          if (!tile.el || !tile.coords) continue;
          const r = tile.el.getBoundingClientRect();
          ctx.strokeRect(r.left - rect.left + 0.5, r.top - rect.top + 0.5, r.width, r.height);
          const x = Math.max(r.left - rect.left, 0) + 4;
          const y = Math.max(r.top - rect.top, 0) + 4;
          label(`${tileName(layer)[0]} ${tile.coords.z}/${tile.coords.x}/${tile.coords.y}${tile.loaded ? '' : ' …'}`, Math.min(x, rect.width - 90), Math.min(y, rect.height - 18), '#ffdc00');
        }
      }
    }
    if (on.hits) {
      for (const m of marks().layers) {
        if (!m._point) continue;
        const p = map.layerPointToContainerPoint(m._point);
        if (p.x < -50 || p.y < -50 || p.x > rect.width + 50 || p.y > rect.height + 50) continue;
        const r = (m._radius || 0) + (typeof m._clickTolerance === 'function' ? m._clickTolerance() : 0);
        ctx.strokeStyle = 'rgba(255,255,0,0.9)';
        ctx.beginPath();
        ctx.arc(p.x, p.y, r, 0, Math.PI * 2);
        ctx.stroke();
        ctx.fillStyle = '#ff2d2d';
        ctx.fillRect(p.x - 1.5, p.y - 1.5, 3, 3);
      }
    }
    if (on.room) {
      const area = visibleArea();
      if (area) {
        const f = area.free;
        ctx.strokeStyle = 'rgba(80,255,120,0.95)';
        ctx.setLineDash([6, 4]);
        ctx.strokeRect(f.left - rect.left + 0.5, f.top - rect.top + 0.5, f.right - f.left, f.bottom - f.top);
        ctx.setLineDash([]);
        const cx = (f.left + f.right) / 2 - rect.left;
        const cy = (f.top + f.bottom) / 2 - rect.top;
        ctx.beginPath();
        ctx.moveTo(cx - 10, cy);
        ctx.lineTo(cx + 10, cy);
        ctx.moveTo(cx, cy - 10);
        ctx.lineTo(cx, cy + 10);
        ctx.stroke();
        label('visible area: where a followed mob is kept', Math.max(f.left - rect.left + 4, 4), Math.max(f.top - rect.top + 4, 4), '#50ff78');
      }
    }
  }

  function scheduleAids() {
    if (aids.pending === 0 && aids.canvas) aids.pending = requestAnimationFrame(drawAids);
  }

  function aidsOn() {
    if (!anyAid()) {
      aidsOff();
      return;
    }
    if (!aids.canvas) {
      aids.canvas = document.createElement('canvas');
      aids.canvas.className = 'debug-aids-canvas';
      aids.canvas.setAttribute('aria-hidden', 'true');
      map.getContainer().append(aids.canvas);
      aids.ctx = aids.canvas.getContext('2d');
      onMap('move zoom resize layeradd layerremove', scheduleAids);
    }
    scheduleAids();
  }

  function aidsOff() {
    if (!aids.canvas) return;
    cancelAnimationFrame(aids.pending);
    aids.pending = 0;
    aids.canvas.remove();
    aids.canvas = null;
    aids.ctx = null;
  }

  // --- the recording ------------------------------------------------------------------------

  // What the map and the followed marker did each frame for ten seconds.
  // Positions are kept as how far from where the recording began, so that
  // the samples say how the map moved and not where it was.
  function sample(now) {
    const r = recording;
    if (r.samples.length >= RECORD_MAX || now - r.start > RECORD_MS) {
      finishRecording();
      return;
    }
    const c = map.getCenter();
    if (!r.origin) r.origin = { x: c.lng, z: c.lat };
    const key = app.inspect && app.inspect.following ? app.inspect.following() : null;
    const truth = key ? keep.stream.latest.get(key) : null;
    let drift = null;
    let size = null;
    const { layers } = marks();
    let nearest = null;
    if (truth) {
      let best = 4;
      for (const m of layers) {
        if (!m._latlng) continue;
        const d = Math.hypot(m._latlng.lng - truth.x, m._latlng.lat - truth.z);
        if (d < best) { best = d; nearest = m; }
      }
      if (nearest && nearest._point) {
        const drawn = map.layerPointToContainerPoint(nearest._point);
        const want = map.latLngToContainerPoint([truth.z, truth.x]);
        drift = Math.hypot(drawn.x - want.x, drawn.y - want.y);
      }
    }
    const marker = nearest || layers.find((m) => m._point && m.options && m.options.sprite) || layers[0];
    if (marker) {
      const k = marker._renderer && marker._renderer._steady ? marker._renderer._steady : 1;
      const sprite = marker.options && marker.options.sprite;
      const density = app.icons && app.icons.density ? app.icons.density() : 1;
      // On the screen: what is drawn on the canvas, which is itself
      // stretched by as much as the map has zoomed since it was drawn.
      const stretch = Number.isFinite(marker._renderer && marker._renderer._zoom) ? map.getZoomScale(map.getZoom(), marker._renderer._zoom) : 1;
      size = (sprite ? sprite.width / density : (marker._radius || 0) * 2) * k * stretch;
    }
    r.samples.push([Math.round(now - r.start), Math.round((now - (r.last || now)) * 10) / 10, Math.round(map.getZoom() * 1000) / 1000, Math.round((c.lng - r.origin.x) * 100) / 100, Math.round((c.lat - r.origin.z) * 100) / 100, drift === null ? null : Math.round(drift * 10) / 10, size === null ? null : Math.round(size * 10) / 10, key ? 1 : 0]);
    r.last = now;
  }

  function summarise(r) {
    const col = (i) => r.samples.map((s) => s[i]).filter((v) => v !== null);
    const stat = (list) => (list.length ? { min: Math.min(...list), max: Math.max(...list), mean: Math.round((list.reduce((a, b) => a + b, 0) / list.length) * 100) / 100 } : null);
    const dts = col(1).slice(1).sort((a, b) => a - b);
    const travel = Math.max(0, ...r.samples.map((s) => Math.hypot(s[3], s[4])));
    return {
      frames: r.samples.length,
      seconds: r.samples.length ? r.samples.at(-1)[0] / 1000 : 0,
      frameMs: dts.length ? { median: dts[Math.floor(dts.length / 2)], p95: dts[Math.floor(dts.length * 0.95)], worst: dts.at(-1), over33: dts.filter((d) => d > 33).length } : null,
      zoom: stat(col(2)),
      mapTravelBlocks: Math.round(travel * 100) / 100,
      followed: col(7).some((v) => v === 1),
      followedDriftPx: stat(col(5)),
      sampleMarkerPx: stat(col(6)),
    };
  }

  function startRecording() {
    if (recording) return;
    recording = { start: performance.now(), samples: [], origin: null, last: 0 };
    keep.stream.parse = true;
    el.record.disabled = true;
    el.record.textContent = 'Recording…';
    keep.log('recording', 'started');
  }

  function finishRecording() {
    const r = recording;
    recording = null;
    el.record.disabled = false;
    el.record.textContent = 'Record 10 seconds';
    lastRecording = { columns: ['ms', 'frameMs', 'zoom', 'dx', 'dz', 'followedDriftPx', 'markerPx', 'followed'], summary: summarise(r), samples: r.samples };
    keep.log('recording', `done, ${r.samples.length} frames`);
    say(`Recorded ${r.samples.length} frames. They are in the next report.`);
  }

  // --- the report ---------------------------------------------------------------------------

  const media = (q) => { try { return matchMedia(q).matches; } catch { return null; } };

  async function build(withPosition) {
    const [server, versions] = await Promise.all([fetchServed(), checkScripts()]);
    const stream = keep.stream;
    const zoom = map.getZoom();
    const layersOn = settings.get('layers');
    const live = groups(withPosition);
    const panel = Object.fromEntries(live.map((g) => [g.title, Object.fromEntries(g.rows)]));
    const out = {
      report: 'mcmap diagnostics',
      madeAt: new Date().toISOString(),
      includesPosition: withPosition,
      link: tools.scrubLink(location.href, withPosition),
      panel,
      versions,
      server,
      browser: {
        userAgent: navigator.userAgent,
        platform: navigator.platform || '',
        language: navigator.language,
        touchPoints: navigator.maxTouchPoints || 0,
        pointer: media('(pointer: coarse)') ? 'coarse' : media('(pointer: fine)') ? 'fine' : 'unknown',
        hover: media('(hover: hover)'),
        reducedMotion: media('(prefers-reduced-motion: reduce)'),
        colourScheme: media('(prefers-color-scheme: dark)') ? 'dark' : media('(prefers-color-scheme: light)') ? 'light' : 'no preference',
        moreContrast: media('(prefers-contrast: more)'),
        pageZoom: window.visualViewport ? window.visualViewport.scale : null,
        online: navigator.onLine,
      },
      viewport: { inner: [innerWidth, innerHeight], outer: [outerWidth, outerHeight], screen: [screen.width, screen.height], dpr: window.devicePixelRatio, map: [map.getSize().x, map.getSize().y] },
      map: {
        dimension: app.dimension && app.dimension(),
        zoom: Number.isFinite(zoom) ? Math.round(zoom * 1000) / 1000 : null,
        zoomRange: [map.getMinZoom(), map.getMaxZoom()],
        following: Boolean(app.inspect && app.inspect.following && app.inspect.following()),
      },
      layers: {
        switches: { on: Object.entries(layersOn).filter(([k, v]) => k.includes('/') && v === true).length, off: Object.entries(layersOn).filter(([k, v]) => k.includes('/') && v === false).length },
        marksDrawn: marks().count,
        tileLayers: gridLayers().map((l) => ({ name: tileName(l), ...tileCounts(l), failed: failedTiles.get(l) || 0 })),
        liveEntitiesInFrame: stream.entities,
      },
      settings: { version: settings.VERSION, characters: settings.size(), storage: settings.kept(), values: tools.scrubSettings(settings.all(), withPosition) },
      network: { endpoints: keep.endpoints(), recentFailures: keep.failures() },
      events: keep.events(),
      errors: keep.errors(),
    };
    if (lastRecording) out.recording = lastRecording;
    return JSON.stringify(out, null, 2);
  }

  const said = (node, text) => { node.textContent = text; };
  let saidTimer = 0;
  function say(text) {
    said(el.said, text);
    clearTimeout(saidTimer);
    saidTimer = setTimeout(() => said(el.said, ''), 6000);
  }

  async function copy() {
    const text = await build(includePosition);
    try {
      await navigator.clipboard.writeText(text);
      say(`Copied ${text.length.toLocaleString('en-US')} characters${includePosition ? ', with your position' : ''}.`);
    } catch {
      // No clipboard, as on a page not served securely: offered as a file.
      download(text);
      say('The clipboard is not available here, so the report was saved as a file.');
    }
  }

  function download(text) {
    const url = URL.createObjectURL(new Blob([text], { type: 'application/json' }));
    const a = document.createElement('a');
    a.href = url;
    a.download = `mcmap-diagnostics-${new Date().toISOString().replace(/[:.]/g, '-')}.json`;
    document.body.append(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 10_000);
  }

  const doCopy = () => copy();
  const doDownload = async () => {
    download(await build(includePosition));
    say(`Saved${includePosition ? ', with your position' : ''}.`);
  };
  const setPosition = (on) => {
    includePosition = on;
    el.position.checked = on;
    el.helpPosition.checked = on;
  };

  for (const [node, fn] of [[el.copy, doCopy], [el.helpCopy, doCopy], [el.download, doDownload], [el.helpDownload, doDownload], [el.record, startRecording]]) node.addEventListener('click', fn);
  el.position.addEventListener('change', () => setPosition(el.position.checked));
  el.helpPosition.addEventListener('change', () => setPosition(el.helpPosition.checked));
  el.fold.addEventListener('click', () => {
    const open = el.fold.getAttribute('aria-expanded') !== 'true';
    el.fold.setAttribute('aria-expanded', String(open));
    el.fold.textContent = open ? 'Fold' : 'Open';
    el.body.hidden = !open;
  });
  el.close.addEventListener('click', () => write({ on: false }));
  el.on.addEventListener('change', () => write({ on: el.on.checked }));
  el.reportOn.addEventListener('change', () => write({ report: el.reportOn.checked }));
  for (const box of el.panel.querySelectorAll('input[data-aid]')) {
    box.addEventListener('change', () => write({ aids: { ...read().aids, [box.dataset.aid]: box.checked } }));
  }

  // --- on and off ----------------------------------------------------------------------------

  let active = false;
  function start() {
    if (active) return;
    active = true;
    keep.stream.parse = true;
    el.panel.hidden = false;
    frameTimes = [];
    last = 0;
    frame = requestAnimationFrame(tick);
    timer = setInterval(paint, UPDATE_MS);
    onMap('mousemove', (e) => { pointer = e.latlng; });
    onMap('mouseout', () => { pointer = null; });
    onMap('layeradd', (e) => { if (typeof L.GridLayer === 'function' && e.layer instanceof L.GridLayer) watch(e.layer); });
    for (const layer of gridLayers()) watch(layer);
    fetchServed().then(paint);
    checkScripts().then(paint);
    keep.log('debug', 'panel on');
    paint();
    aidsOn();
  }

  function stop() {
    if (!active) return;
    active = false;
    keep.stream.parse = recording !== null;
    el.panel.hidden = true;
    clearInterval(timer);
    cancelAnimationFrame(frame);
    timer = 0;
    frame = 0;
    for (const [events, fn] of listening.splice(0)) map.off(events, fn);
    unwatchAll();
    aidsOff();
    pointer = null;
    built = [];
    keep.log('debug', 'panel off');
  }

  function apply() {
    const s = read();
    el.on.checked = s.on;
    el.reportOn.checked = s.report;
    el.report.hidden = !s.report;
    el.helpReport.hidden = !s.report;
    for (const box of el.panel.querySelectorAll('input[data-aid]')) box.checked = Boolean((s.aids || {})[box.dataset.aid]);
    if (s.on) {
      start();
      aidsOn();
    } else stop();
  }

  document.addEventListener('mcmap:settings', (e) => {
    if (e.detail && e.detail.sections.includes('debug')) apply();
  });

  // A link may carry the choice, and is then remembered like any other.
  const asked = (name) => {
    const v = new URLSearchParams(location.search).get(name);
    return v === null ? null : /^(1|true|on|yes)$/i.test(v) ? true : /^(0|false|off|no)$/i.test(v) ? false : null;
  };
  const wantDebug = asked('debug');
  const wantReport = asked('diagnostics');
  if (wantDebug !== null || wantReport !== null) {
    write({ ...(wantDebug !== null ? { on: wantDebug } : {}), ...(wantReport !== null ? { report: wantReport } : {}) });
  }

  app.debug = {
    toggle: () => write({ on: !read().on }),
    on: () => active,
    report: build,
    // For the tests and for whoever reports on the page.
    watching: () => ({ timer: timer !== 0, frame: frame !== 0, listeners: listening.length }),
  };
  apply();
})();
