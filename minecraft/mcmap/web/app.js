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
  // The longest the tiles being replaced stay under the ones replacing them.
  const SWAP_MS = 4000;
  // How long Leaflet takes to fade a tile in, and a little: what it waits
  // itself before it takes away the tiles a new one covers.
  const FADE_MS = 250;
  const LABELS = { overworld: 'Overworld', nether: 'Nether', end: 'The End' };
  // What the viewer keeps, where the page has a script for it. The page
  // and its scripts are cached apart for a few minutes, so just after a
  // release this can be a page from before there was one: everything then
  // works as it would with nothing kept.
  const settings = window.mcmapSettings || null;
  const look = () => (settings ? settings.look() : {});
  const colour = (name, fallback) => (settings && settings.colour(name)) || fallback;

  // Leaflet's simple CRS has +Y pointing up; a Minecraft map has +Z pointing
  // down. With this transformation lat is Z and lng is X, in blocks, and
  // zoom 0 is one block per pixel, which is exactly how the tiles are cut.
  const crs = L.extend({}, L.CRS.Simple, { transformation: new L.Transformation(1, 0, 1, 0) });
  // Bounds no tile is in. Leaflet draws a tile only where it overlaps the
  // layer's bounds by more than an edge, and a box of no size, on a corner
  // every tile at every zoom shares, overlaps none.
  const NOWHERE = L.latLngBounds([0, 0], [0, 0]);

  // One block is 2^zoom screen pixels. 7 is 128: a single block fills a
  // third of a phone's width and about a seventh of a desktop's height,
  // enough to count the blocks of a build or place a marker on one. The
  // terrain's own tiles stop at a block to the pixel, so everything past
  // that is the same tiles enlarged.
  const MAX_ZOOM = 7;
  // As far out as a world of any size is fitted to a window; each
  // dimension's own farthest is worked out from its extent.
  const MIN_ZOOM = -12;
  // Hundredths of a level are well under a pixel at any scale the map
  // has, and keep an address short.
  const tidy = (zoom) => Math.round(zoom * 100) / 100;

  // Zoom is any fraction, so that a pinch, a trackpad and the wheel rest
  // where they were left. The wheel is zoom.js's own: Leaflet's drops a
  // wheel turn that comes while its last zoom is still animating, and
  // zooms in whole levels however little was scrolled.
  const map = L.map('map', {
    crs,
    attributionControl: false,
    zoomSnap: 0,
    zoomDelta: 1,
    minZoom: MIN_ZOOM,
    maxZoom: MAX_ZOOM,
    scrollWheelZoom: false,
    maxBoundsViscosity: 0.8,
  });

  // Over the terrain, the biome tint and the slime chunks, which are in the
  // tile pane at 200, and under everything drawn as a shape or a marker.
  map.createPane('grid').style.zIndex = 250;
  map.getPane('grid').style.pointerEvents = 'none';
  // For the terrain drawn beside the map's own while two days are
  // compared. Inside the tile pane, over the terrain there and under the
  // biome tint and the slime chunks, so that what is laid over the map is
  // laid over both, and a pane of its own so that it can be cut at the
  // divider whichever layers are in it.
  const besidePane = map.createPane('beside', map.getPane('tilePane'));
  besidePane.style.zIndex = 1;

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
    gotoClear: document.getElementById('goto-clear'),
    who: document.getElementById('who'),
    logout: document.getElementById('logout'),
    login: document.getElementById('login'),
    loginCommand: document.getElementById('login-command'),
    loginCopy: document.getElementById('login-copy'),
    loginCopied: document.getElementById('login-copied'),
    loginNote: document.getElementById('login-note'),
    loginNew: document.getElementById('login-new'),
  };

  // No cache-busting parameter: tiles are served with a short max-age and a
  // Last-Modified, so after a render the browser revalidates and only the
  // tiles that actually changed are downloaded again.
  // A layer given a day draws the terrain as it was kept on that day, at
  // the same addresses. A layer given a level draws that cut of the nether
  // from the same address with the level after it; the default cut has no
  // level, so its addresses are the ones there have always been.
  const Tiles = L.TileLayer.extend({
    getTileUrl(c) {
      if (this.options.day) return `api/history/tiles/${this.options.day}/${this.options.dimension}/${c.z}/${c.x}/${c.y}.${this.options.format}`;
      const level = this.options.level ? `?level=${this.options.level}` : '';
      return `tiles/${this.options.dimension}/${c.z}/${c.x}/${c.y}.${this.options.format}${level}`;
    },
  });

  // The grid is one canvas the size of the view and drawn again whenever
  // the view moves or zooms, and not a layer of tiles: tiles are cut at
  // whole zoom levels and stretched in between, which blurs a one pixel
  // line and makes it a pixel and a half. Here a line is a pixel at every
  // zoom, and a block that is large enough has lines of its own.
  // _initContainer, _update and the rest are the Renderer's own hooks,
  // which is safe only because Leaflet is vendored at a fixed version.
  const BLOCK_LINES_FROM = 12;
  const LINE_GAP = 32;
  const Grid = L.Renderer.extend({
    options: { padding: 0.5, pane: 'grid' },
    _initContainer() {
      this._container = document.createElement('canvas');
      this._ctx = this._container.getContext('2d');
      this._container.setAttribute('aria-hidden', 'true');
    },
    _destroyContainer() {
      L.DomUtil.remove(this._container);
      delete this._container;
      delete this._ctx;
    },
    // Lines at this spacing, in blocks, are far enough apart to read.
    spacing(scale) {
      if (scale >= BLOCK_LINES_FROM) return 1;
      return [16, 128, REGION, 2048, 8192].find((s) => s * scale >= LINE_GAP) || 32768;
    },
    _update() {
      L.Renderer.prototype._update.call(this);
      const b = this._bounds;
      const size = b.getSize();
      const ratio = window.devicePixelRatio || 1;
      const canvas = this._container;
      L.DomUtil.setPosition(canvas, b.min);
      if (canvas.width !== Math.round(size.x * ratio) || canvas.height !== Math.round(size.y * ratio)) {
        canvas.width = Math.round(size.x * ratio);
        canvas.height = Math.round(size.y * ratio);
      }
      canvas.style.width = `${size.x}px`;
      canvas.style.height = `${size.y}px`;
      this._draw(ratio);
    },
    _draw(ratio) {
      const ctx = this._ctx;
      const b = this._bounds;
      ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
      ctx.clearRect(0, 0, b.getSize().x, b.getSize().y);
      const scale = 2 ** this._map.getZoom();
      const step = this.spacing(scale);
      const nw = this._map.layerPointToLatLng(b.min);
      const se = this._map.layerPointToLatLng(b.max);
      // A line is on the device pixel nearest its block's edge.
      const at = (px) => (Math.round(px * ratio) + 0.5) / ratio;
      const draw = (block, vertical) => {
        const to = this._map.latLngToLayerPoint(vertical ? [0, block] : [block, 0]);
        const p = at(vertical ? to.x - b.min.x : to.y - b.min.y);
        const chunk = block % 16 === 0;
        ctx.globalAlpha = step === 1 && !chunk ? 0.5 : 1;
        ctx.strokeStyle = block === 0 ? colour('grid-origin', 'rgba(110,207,122,0.9)')
          : block % REGION === 0 ? colour('grid-region', 'rgba(255,255,255,0.45)') : colour('grid-line', 'rgba(255,255,255,0.18)');
        // A dark line beside the light one, so that it reads on pale
        // terrain as well as dark.
        const dark = ctx.globalAlpha * 0.35;
        const w = b.getSize().x;
        const h = b.getSize().y;
        ctx.save();
        ctx.globalAlpha = dark;
        ctx.strokeStyle = '#000';
        ctx.beginPath();
        if (vertical) { ctx.moveTo(p + 1 / ratio, 0); ctx.lineTo(p + 1 / ratio, h); } else { ctx.moveTo(0, p + 1 / ratio); ctx.lineTo(w, p + 1 / ratio); }
        ctx.stroke();
        ctx.restore();
        ctx.beginPath();
        if (vertical) { ctx.moveTo(p, 0); ctx.lineTo(p, h); } else { ctx.moveTo(0, p); ctx.lineTo(w, p); }
        ctx.stroke();
      };
      for (let v = Math.ceil(nw.lng / step) * step; v < se.lng; v += step) draw(v, true);
      for (let h = Math.ceil(nw.lat / step) * step; h < se.lat; h += step) draw(h, false);
      ctx.globalAlpha = 1;
    },
    // A zoom that is being driven by hand, a step at a time, is drawn at
    // each step; the browser's own stretching of the last drawing is for
    // the animated kind only.
    _onZoom() {
      if (this._map._animatingZoom) return;
      this._update();
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
  // A terrain on the map: the layer shown, the one layer kept under it
  // while that one's tiles come, and the wait that takes it away if they
  // never do. There is the map's own, and the one beside it while two
  // days are compared.
  const side = (pane) => ({ pane, layer: null, under: null, swap: 0 });
  const main = side('tilePane');
  const other = side('beside');
  let extent = null;
  // The day whose terrain is drawn in place of today's, as the server
  // lists it, or null for the map as it is.
  let past = null;
  // While two are compared, what is drawn right of the divider: { day },
  // a day as the server lists it or null for the map as it is. The map's
  // own terrain is then the left of the two. cut is where the divider
  // stands, as a share of the map's width.
  let compared = null;
  let cut = 0.5;
  // The nether's cut the viewer chose, as the server names it, or '' for
  // the default one. What is drawn is this only while the server still
  // offers it: a deployment can take a level away.
  let netherCut = settings ? settings.get('levels').nether || '' : '';
  const grid = new Grid();

  const dimension = (id) => info && info.dimensions.find((d) => d.id === id);

  // The cuts of a dimension the viewer can choose between, the default
  // first, or none where there is no choice to make.
  const DEFAULT_LEVEL = 'default';
  const levelsOf = (id) => {
    const d = id === 'nether' ? dimension(id) : null;
    return d && d.rendered && Array.isArray(d.levels) && d.levels.length > 1 ? d.levels : [];
  };
  // The level asked of the server for a dimension: none for the default,
  // for every other dimension and for a day that is past, which was kept
  // as the default cut only.
  const levelOf = (id) => (!past && netherCut !== '' && levelsOf(id).some((l) => l.id === netherCut && l.id !== DEFAULT_LEVEL) ? netherCut : '');

  // Tells the live layer, which is a file of its own, that what it should
  // be showing may have changed: the dimension, the login, or the map data.
  const announce = () => document.dispatchEvent(new CustomEvent('mcmap:view'));
  const version = (d) => (d.renderedAt ? Date.parse(d.renderedAt) : 0);
  // What says when the tiles drawn for a dimension were rendered: the level
  // drawn, which is rendered after its dimension and so has a time of its
  // own, or the dimension itself.
  const drawnOf = (id) => {
    const level = levelOf(id);
    return level === '' ? dimension(id) : levelsOf(id).find((l) => l.id === level);
  };
  // Which tiles a dimension's layer holds: a layer is out of date when
  // this is no longer what it was built for, whether by a new render or
  // by its level being taken away.
  const tilesOf = (id) => {
    const drawn = drawnOf(id);
    return drawn ? `${levelOf(id)}@${version(drawn)}` : '';
  };

  // What the address carries after the view: one more part, which names
  // something open over the map, such as a structure's details. It is kept
  // through every rewrite of the address until whatever set it clears it.
  const EXTRA = /^[a-z0-9_~.-]{1,96}$/;
  let extra = '';
  const extraOf = (hash) => {
    const part = hash.slice(1).split('/')[4] || '';
    return EXTRA.test(part) ? part : '';
  };
  // After those, a seventh part: the day whose terrain is drawn. Everything
  // that reads an address takes its parts by their place, so one past the
  // last any of them reads changes what none of them reads, and an address
  // without a day is as it always was. The day in the address is the one
  // drawn once the timeline has found it among the days kept; until then
  // it is only what a link asks for, kept through every rewrite as the
  // part before it is. Never read beside a view, which moves nothing.
  const DAY = /^\d{4}-\d{2}-\d{2}$/;
  let wanted = '';
  const dayOf = (hash) => {
    const parts = hash.slice(1).split('/');
    return !parts[5] && DAY.test(parts[6] || '') ? parts[6] : '';
  };
  // And an eighth while that day is compared with another: the day right
  // of the divider, or "now". It is past the last part anything else
  // reads, as the day is, means nothing without a day before it, and is
  // kept and handed over the way the day is.
  const NOW = 'now';
  let wantedBeside = '';
  const besideOf = (hash) => {
    const part = hash.slice(1).split('/')[7] || '';
    return dayOf(hash) && (part === NOW || DAY.test(part)) ? part : '';
  };

  function parseHash() {
    const [id, x, z, zoom, , view] = location.hash.slice(1).split('/');
    // An address with a sixth part carries a saved view, which is only
    // ever an offer: its place comes with it if the viewer takes it up,
    // and until then the address moves the map nowhere.
    if (view) return null;
    const n = [x, z, zoom].map(Number);
    if (!Object.hasOwn(LABELS, id) || n.some((v) => !Number.isFinite(v))) return null;
    return { id, x: n[0], z: n[1], zoom: n[2] };
  }

  function writeHash() {
    if (!current) return;
    const c = map.getCenter();
    const place = `#${current}/${Math.round(c.lng)}/${Math.round(c.lat)}/${tidy(map.getZoom())}${extra ? `/${extra}` : ''}`;
    // A day is the seventh part whatever is before it, so the two it
    // follows are written even when empty.
    const hash = wanted ? `${place}${extra ? '' : '/'}//${wanted}${wantedBeside ? `/${wantedBeside}` : ''}` : place;
    // replaceState so panning does not fill the back button with positions.
    if (hash !== location.hash) history.replaceState(null, '', hash);
  }

  // The coarsest zoom the map goes to: the one the renderer cut tiles
  // for, or further out if the whole world still does not fit the window
  // then, as on a tall narrow phone. Worked out from the box and not asked
  // of Leaflet, whose answer is held to the limit it is being set.
  let wholeWorld = MIN_ZOOM;
  const fitZoom = (bounds, size) => Math.log2(Math.min(size.x / (bounds.getEast() - bounds.getWest()), size.y / (bounds.getNorth() - bounds.getSouth())));
  function limits() {
    if (!extent) return;
    const fit = fitZoom(extent, map.getSize());
    const floor = Number.isFinite(fit) ? Math.min(wholeWorld, Math.floor((fit - 0.1) * 100) / 100) : wholeWorld;
    map.setMinZoom(Math.max(MIN_ZOOM, floor));
  }
  map.on('resize', limits);

  function settle(s) {
    clearTimeout(s.swap);
    if (s.under) map.removeLayer(s.under);
    s.under = null;
  }

  const reach = (o) => L.latLngBounds(
    [o.minRegionZ * REGION, o.minRegionX * REGION],
    [(o.maxRegionZ + 1) * REGION, (o.maxRegionX + 1) * REGION],
  );

  // Puts a dimension's terrain on one side, as it is or as a day kept it,
  // and gives back the layer that was there. stays is whether that one
  // drew the same dimension, and may be left under the new one.
  function lay(s, id, day, stays) {
    const today = dimension(id);
    // A day kept has its own extent and format: the world was smaller
    // then, and may have been drawn another way. A day that does not hold
    // this dimension is drawn as nothing, not as today: the layer is made
    // as today's would be and given nowhere to draw, so it asks for none
    // of the tiles the day does not have.
    const kept = day ? day.dimensions.find((o) => o && o.id === id) : today;
    const d = kept || today;
    const old = s.layer;
    if (!stays) {
      settle(s);
      if (old) map.removeLayer(old);
    }
    s.layer = new Tiles('', {
      dimension: id,
      day: day ? day.day : '',
      level: day ? '' : levelOf(id),
      format: d.format,
      tileSize: TILE,
      minNativeZoom: d.minZoom,
      maxNativeZoom: d.maxZoom,
      // Tiles are enlarged past the finest the server has, and shrunk
      // below the coarsest, which is what lets the zoom be any fraction.
      minZoom: -12,
      maxZoom: MAX_ZOOM,
      bounds: kept ? reach(kept) : NOWHERE,
      noWrap: true,
      keepBuffer: 2,
      pane: s.pane,
      // Under the pane of the terrain beside it, which is in this one.
      zIndex: 0,
    }).addTo(map);
    return old;
  }

  // The same place drawn again, a day apart or a render apart: what was
  // there stays under the new tiles until they have come, so stepping
  // through days is one picture turning into the next and not a blink
  // of nothing between them. Not for ever, for tiles that never come,
  // and not at all where the new layer has no tile in view to wait for:
  // a place the day did not hold is shown as nothing at once. Only ever
  // one layer is kept under: a layer replaced before its own tiles came
  // goes at once, its requests with it, and what was under it stays for
  // what is left of its wait.
  function turn(s, old) {
    if (s.under && old.isLoading()) {
      map.removeLayer(old);
    } else {
      settle(s);
      s.under = old;
      s.swap = setTimeout(() => settle(s), SWAP_MS);
    }
    const mine = s.layer;
    const done = () => { if (s.layer === mine) settle(s); };
    if (!mine.isLoading()) settle(s);
    else mine.once('load', () => { if (map._fadeAnimated) setTimeout(done, FADE_MS); else done(); });
  }

  // Cuts the terrain beside the map's own at the divider, so that it is
  // seen right of it and the map's own left of it. The pane moves with
  // the map and has no size of its own, so the cut is said in the pane's
  // own measure, again whenever the map has moved under it, and reaches
  // a view's worth past the other three edges.
  function clip() {
    if (!compared) return;
    const size = map.getSize();
    const from = map.containerPointToLayerPoint([Math.round(cut * size.x), -size.y]);
    const to = map.containerPointToLayerPoint([size.x * 2, size.y * 2]);
    besidePane.style.clipPath = `polygon(${from.x}px ${from.y}px, ${to.x}px ${from.y}px, ${to.x}px ${to.y}px, ${from.x}px ${to.y}px)`;
  }
  map.on('move zoom viewreset resize', clip);

  // Draws what is right of the divider. fresh is for a dimension just
  // come to, which is drawn alone; otherwise what was there turns into
  // it as the map's own terrain does.
  function showBeside(fresh) {
    const stays = !fresh && Boolean(other.layer);
    const old = lay(other, current, compared.day, stays);
    if (stays) turn(other, old);
    clip();
  }

  function stopComparing() {
    if (!compared) return;
    compared = null;
    settle(other);
    if (other.layer) map.removeLayer(other.layer);
    other.layer = null;
    besidePane.style.clipPath = '';
  }

  function show(id, view) {
    const today = dimension(id);
    if (!today || !today.rendered) return;
    const stays = Boolean(main.layer) && current === id;
    current = id;
    // Only the tiles are of the day. Where the map can be taken, and what
    // is laid over it, are of the world as it is, so choosing a day never
    // moves the view.
    extent = reach(today);
    const old = lay(main, id, past, stays);
    map.setMaxBounds(extent.pad(0.25));
    wholeWorld = today.minZoom;
    limits();

    if (view) map.setView([view.z, view.x], view.zoom);
    else if (extent.contains([0, 0])) map.setView([0, 0], Math.max(today.minZoom, -2));
    else map.fitBounds(extent);

    if (stays) turn(main, old);
    // What is beside it is of a day of its own, and is drawn again only
    // for another dimension.
    if (compared && (!stays || !other.layer)) showBeside(true);

    for (const b of el.tabs.children) b.setAttribute('aria-pressed', String(b.dataset.id === id));
    writeHash();
    describe();
    announce();
  }

  // Takes up a cut of the nether, given as the server names it, and says
  // whether it is one on offer. It is kept for next time even while another
  // dimension or a day is shown; it is drawn only for the nether as it is,
  // from the place the map is at, through the one swap of layers the days
  // use, so a viewer who steps through the cuts fast is shown one picture
  // turning into the next.
  function choose(id) {
    if (typeof id !== 'string' || !levelsOf('nether').some((l) => l.id === id)) return false;
    const next = id === DEFAULT_LEVEL ? '' : id;
    if (next === netherCut) return true;
    netherCut = next;
    if (settings) settings.set('levels', { nether: netherCut || null });
    recut();
    return true;
  }

  function recut() {
    if (current !== 'nether' || past) return;
    const c = map.getCenter();
    show(current, { x: c.lng, z: c.lat, zoom: map.getZoom() });
  }

  // The record's cut has changed other than by a choice made here, as when
  // a preview is let go of and what was kept is put back: the cut drawn
  // follows it, or the page and its record would disagree until a reload.
  document.addEventListener('mcmap:settings', (e) => {
    if (!settings || !e.detail || !e.detail.sections.includes('levels')) return;
    const kept = settings.get('levels').nether || '';
    if (kept === netherCut) return;
    netherCut = kept;
    recut();
  });

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
    } else if (past) {
      el.status.textContent = `as it was on ${past.day}`;
    } else if (!d || !d.rendered) {
      el.status.textContent = 'the first render is still running; this page updates when it is done';
    } else {
      const at = drawnOf(current).renderedAt;
      const when = at ? `updated ${ago(at)}` : 'rendered before the last restart';
      el.status.textContent = info.problem ? `${when}; the last refresh failed, so this is the previous map` : when;
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
    // A day that is past is not about to be refreshed.
    el.refresh.hidden = locked || !info || Boolean(past);
    if (locked || !info) return;
    const now = Date.now() + serverOffset;
    const snapshot = info.snapshotAt ? Date.parse(info.snapshotAt) : NaN;
    const every = info.refreshSeconds * 1000;
    let text;
    let overdue = false;
    let ask = true;
    if (!Number.isFinite(snapshot) || !(every > 0)) {
      // A service that has just started has taken no snapshot yet.
      text = 'waiting for the first refresh';
    } else {
      const late = now - snapshot - every;
      if (late < 0) {
        // Never more than the interval, whatever the two clocks make of
        // a snapshot taken this instant.
        text = `next refresh in ${clock(Math.min(-late, every), Math.ceil)}`;
        // The tiles of a snapshot follow it by as long as they take to draw.
        const d = current && drawnOf(current);
        ask = Boolean(d && d.renderedAt) && Date.parse(d.renderedAt) < snapshot && now - snapshot < DRAWING_MS;
        if (!ask) dueSince = null;
      } else if (late < DUE_GRACE_MS && !info.problem) {
        text = 'refresh due now';
      } else {
        overdue = true;
        text = `refresh overdue by ${clock(late, Math.floor)}, ${info.problem ? 'retrying' : 'may be in a quiet window'}`;
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
    offerCopy(false);
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
    offerCopy(true);
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
    offerCopy(false);
    note('That code expired before it was used.', true);
    el.loginNew.hidden = false;
  }

  // Offered only while there is a whole command to copy: "!map" alone is
  // not one the game accepts.
  let copiedTimer = null;
  function offerCopy(on) {
    if (!el.loginCopy) return;
    el.loginCopy.hidden = !on;
    clearTimeout(copiedTimer);
    el.loginCopied.textContent = '';
  }

  // Copies the command exactly as the page shows it, which is exactly as
  // it has to be typed.
  async function copyCommand() {
    let said = 'Copied';
    try {
      await navigator.clipboard.writeText(el.loginCommand.textContent);
    } catch {
      // No clipboard to write to, as on a page not served securely. The
      // command on the page is the same text, so it is selected and
      // copied the old way, or left selected for the viewer to copy.
      getSelection().selectAllChildren(el.loginCommand);
      let done = false;
      try { done = document.execCommand('copy'); } catch { /* left selected */ }
      if (!done) said = 'Selected: copy it';
    }
    el.loginCopied.textContent = said;
    clearTimeout(copiedTimer);
    copiedTimer = setTimeout(() => { el.loginCopied.textContent = ''; }, 4000);
  }

  if (el.loginCopy) el.loginCopy.addEventListener('click', copyCommand);
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
      el.status.textContent = 'cannot reach the map service; retrying';
      return;
    }
    if (checking === true && body === answer) return;
    answer = body;
    const before = current && tilesOf(current);
    info = next;
    el.world.textContent = `${info.world} map`;
    document.title = `${info.world} map`;
    tabs();

    if (!current) {
      // An address that names a place is where the page opens; failing
      // that, the place the viewer's default view was saved with.
      const kept = settings ? settings.place() : null;
      const want = parseHash() || (kept && { id: kept.d, x: kept.x, z: kept.z, zoom: kept.zoom });
      // The cut a default view was saved with comes with its place, and is
      // kept as the viewer's choice as one from the picker is: the cut drawn
      // and the cut kept are then never two, so whatever puts the record
      // back puts back what was drawn.
      if (!parseHash() && kept && kept.d === 'nether') {
        netherCut = kept.level || '';
        settings.set('levels', { nether: netherCut || null });
      }
      const first = info.dimensions.find((d) => d.rendered);
      if (want && dimension(want.id) && dimension(want.id).rendered) show(want.id, want);
      else if (first) show(first.id);
    } else {
      const now = tilesOf(current);
      // A new render changes nothing about a day that is past.
      const drawnAgain = main.layer && now && before && now !== before;
      if (!past && drawnAgain) {
        // A render can extend the map, so the layer is rebuilt with the new
        // extent rather than redrawn inside the old one. The view stays put.
        const c = map.getCenter();
        show(current, { x: c.lng, z: c.lat, zoom: map.getZoom() });
      } else if (compared && !compared.day && drawnAgain) {
        // Beside a day, the map as it is has been drawn again all the same.
        showBeside(false);
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
  // Coordinates as a player writes or copies them: "10 -20", "10, -20",
  // "10 64 -20", with or without the axes' letters. Two numbers are x and
  // z, three are x, y and z. Anything else is not coordinates.
  const WORLD_EDGE = 30_000_000;
  function coordinates(text) {
    if (typeof text !== 'string' || text.length > 96) return null;
    const parts = text.trim().split(/[\s,;]+/).map((p) => p.replace(/^[xyz][:=]?/i, '')).filter(Boolean);
    if (parts.length < 2 || parts.length > 3 || !parts.every((p) => /^[+-]?\d+(\.\d+)?$/.test(p))) return null;
    const n = parts.map(Number);
    if (n.some((v) => Math.abs(v) > WORLD_EDGE)) return null;
    return n.length === 3 ? { x: n[0], y: n[1], z: n[2] } : { x: n[0], z: n[1] };
  }

  window.mcmap = {
    map,
    dimension: () => current,
    dimensions: () => (info ? info.dimensions.map((d) => d.id) : []),
    label: (id) => (Object.hasOwn(LABELS, id) ? LABELS[id] : id),
    live: () => Boolean(info && info.live),
    // Never passes an argument on: load reads one as "only checking".
    reload: () => load(),
    // The blocks the current dimension's terrain covers, or null with no
    // dimension shown: where another tile layer has anything to lay over.
    extent: () => extent,
    // Takes the view to a block, in another dimension if need be, and says
    // whether it could: a dimension not rendered yet cannot be shown.
    go(id, x, z) {
      const d = dimension(id);
      if (!d || !d.rendered || !Number.isFinite(x) || !Number.isFinite(z)) return false;
      const view = { x, z, zoom: Math.max(map.getZoom(), -1) };
      if (id !== current) show(id, view);
      else map.setView([view.z, view.x], view.zoom);
      return true;
    },
    coordinates,
    // The zoom at which a box of blocks just fits the map as it is now.
    fitZoom: (bounds) => fitZoom(bounds, map.getSize()),
    limits: () => ({ min: map.getMinZoom(), max: MAX_ZOOM }),
    // The part of the address that names what is open over the map: read
    // by whoever can open it, and set or cleared by them as it opens and
    // shuts. href is the whole address as it would be copied.
    link: {
      get: () => extra,
      set(part) {
        extra = typeof part === 'string' && EXTRA.test(part) ? part : '';
        writeHash();
      },
      href() {
        writeHash();
        return location.href;
      },
    },
    // Where the map is, and a way to put it somewhere exactly, zoom and
    // all, for a view that was saved with its place.
    place: {
      get() {
        if (!current) return null;
        const c = map.getCenter();
        const where = { d: current, x: Math.round(c.lng), z: Math.round(c.lat), zoom: tidy(map.getZoom()) };
        // Said only for a cut other than the default one, which is what a
        // place with no level means.
        if (levelOf(current) !== '') where.level = levelOf(current);
        return where;
      },
      set(at) {
        const d = at && dimension(at.d);
        if (!d || !d.rendered || ![at.x, at.z, at.zoom].every(Number.isFinite)) return false;
        if (at.d === 'nether') choose(at.level || DEFAULT_LEVEL);
        const view = { x: at.x, z: at.z, zoom: at.zoom };
        if (at.d !== current) show(at.d, view);
        else map.setView([view.z, view.x], view.zoom, { animate: false });
        return true;
      },
    },
    // The day whose terrain is drawn: read as its date, or '' for now, and
    // set to a day as GET /api/history lists it, or to null for now. The
    // view stays where it is. wanted is the day the address names, which
    // is the day drawn except where the address has only just said it:
    // whoever knows the days kept then sets that day, or null if it is
    // not one of them, and either way the address says what is drawn.
    // markers is whether the day drawn kept a dimension's markers, as its
    // listing says, for the layer that draws them in place of today's.
    past: {
      get: () => (past ? past.day : ''),
      wanted: () => wanted,
      markers: (id) => Boolean(past) && past.dimensions.some((o) => o && o.id === id && o.markers === true),
      set(day) {
        const next = day && typeof day.day === 'string' && DAY.test(day.day) && Array.isArray(day.dimensions) ? day : null;
        wanted = next ? next.day : '';
        // Now has nothing beside it, and a day set by whoever compares
        // none is beside nothing either, whatever the address had asked.
        if (!next) stopComparing();
        if (!compared) wantedBeside = '';
        if ((next ? next.day : '') === (past ? past.day : '')) {
          writeHash();
          return true;
        }
        past = next;
        if (!current) return true;
        const c = map.getCenter();
        show(current, { x: c.lng, z: c.lat, zoom: map.getZoom() });
        return true;
      },
      // The day drawn, compared with another: the map's own terrain is
      // seen left of a divider and the other's right of it, at one place
      // and zoom. start takes the day for the right as the server lists
      // it, or null for the map as it is, and is refused while no day is
      // drawn, which leaves nothing to compare; stop puts the map back as
      // one. get is the date on the right, or '' for now, and on whether
      // two are drawn at all. wanted is what the address asks for beside
      // its day, a date or "now", handed over as the day is. cut is where
      // the divider stands as a share of the map's width, and moves it
      // when given one.
      compare: {
        on: () => compared !== null,
        get: () => (compared && compared.day ? compared.day.day : ''),
        wanted: () => wantedBeside,
        start(day) {
          if (!past) return false;
          const next = day && typeof day.day === 'string' && DAY.test(day.day) && Array.isArray(day.dimensions) ? day : null;
          wantedBeside = next ? next.day : NOW;
          const same = compared && (compared.day ? compared.day.day : '') === (next ? next.day : '');
          if (!same) {
            const fresh = !compared;
            compared = { day: next };
            if (current) showBeside(fresh);
          }
          writeHash();
          return true;
        },
        stop() {
          stopComparing();
          wantedBeside = '';
          writeHash();
        },
        cut(to) {
          if (Number.isFinite(to)) {
            cut = Math.min(1, Math.max(0, to));
            clip();
          }
          return cut;
        },
      },
    },
    // The cut of the nether that is drawn. It is offered only while the
    // nether is what is shown, with more than one cut to choose between and
    // no day past; choosing is remembered by the page, and drawn at once.
    level: {
      list: () => (current === 'nether' && !past ? levelsOf('nether').map((l) => ({ ...l })) : []),
      get: () => levelOf('nether') || DEFAULT_LEVEL,
      set: (id) => choose(id),
    },
    layers,
  };
  extra = extraOf(location.hash);
  wanted = dayOf(location.hash);
  wantedBeside = besideOf(location.hash);

  const fmt = (n) => Math.floor(n).toLocaleString('en-US');

  // From this many pixels to a block the position under the pointer is
  // told to a tenth of a block as well: one pixel is then well under one.
  const TENTHS_FROM = 16;
  const tenth = (n) => (Math.floor(n * 10) / 10).toLocaleString('en-US', { minimumFractionDigits: 1 });

  function point(latlng) {
    const x = Math.floor(latlng.lng);
    const z = Math.floor(latlng.lat);
    let text = `X ${fmt(x)}, Z ${fmt(z)}`;
    if (2 ** map.getZoom() >= TENTHS_FROM) text += ` (${tenth(latlng.lng)}, ${tenth(latlng.lat)})`;
    if (look().coords === 'chunks') text += `  ·  chunk ${fmt(x / 16)}, ${fmt(z / 16)}`;
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
    const named = extraOf(location.hash);
    if (named !== extra) {
      extra = named;
      document.dispatchEvent(new CustomEvent('mcmap:link'));
    }
    // Before the view is moved: setting a day draws the map again where
    // it is, which would cut short a move still on its way.
    const day = dayOf(location.hash);
    const beside = besideOf(location.hash);
    if (day !== wanted || beside !== wantedBeside) {
      wanted = day;
      wantedBeside = beside;
      document.dispatchEvent(new CustomEvent('mcmap:day'));
    }
    if (want.id !== current) show(want.id, want);
    else map.setView([want.z, want.x], want.zoom);
  });

  el.grid.addEventListener('change', () => {
    if (el.grid.checked) grid.addTo(map); else map.removeLayer(grid);
    if (settings) settings.set('grid', { on: el.grid.checked });
  });
  if (settings && settings.get('grid').on) {
    el.grid.checked = true;
    grid.addTo(map);
  }

  // The map's room changes without the window's doing so: the bar wraps
  // to a second row when the live controls arrive, and a bar comes and
  // goes above the map while a shared view is previewed. Leaflet measures
  // only when the window is resized, and a map that has the wrong size
  // has the wrong centre.
  // At most once a frame: the map's box changes with every move of a drag
  // of the layer panel's edge, and each time the map is told it lays out
  // and draws again.
  let resized = 0;
  if (typeof ResizeObserver === 'function') {
    new ResizeObserver(() => {
      if (resized !== 0) return;
      resized = requestAnimationFrame(() => {
        resized = 0;
        map.invalidateSize();
      });
    }).observe(map.getContainer());
  }

  // Whoever has asked for less motion gets a map that goes where it is
  // sent without sliding or fading there. Leaflet takes its animation
  // options once, when the map is made, so these are its own fields:
  // _zoomAnimated and _fadeAnimated are internals, which is safe only
  // because Leaflet is vendored at a fixed version.
  const animated = { zoom: map._zoomAnimated, fade: map._fadeAnimated, inertia: map.options.inertia };
  const panBy = map.panBy;
  map.panBy = (offset, options) => panBy.call(map, offset, look().motion === 'reduce' ? { ...options, animate: false } : options);
  function motion() {
    const still = look().motion === 'reduce';
    map._zoomAnimated = animated.zoom && !still;
    map._fadeAnimated = animated.fade && !still;
    map.options.inertia = animated.inertia && !still;
  }
  motion();

  // What the page looks like has changed: the grid is drawn in the
  // theme's lines, and the footer says coordinates the way now chosen.
  let drawnAs = JSON.stringify(look());
  document.addEventListener('mcmap:settings', (e) => {
    if (!e.detail || !e.detail.sections.includes('look') || drawnAs === JSON.stringify(look())) return;
    drawnAs = JSON.stringify(look());
    motion();
    if (map.hasLayer(grid)) grid.redraw();
    if (matchMedia('(hover: none)').matches && current) point(map.getCenter());
  });

  // --- going to coordinates ------------------------------------------------
  //
  // Go takes the view to a block and leaves a mark on it, since a view
  // that has moved does not say which of its blocks was asked for. Clear
  // takes the numbers and the mark away together.

  // { layer, dimension } for the mark Go left, or null.
  let went = null;

  function unmark() {
    if (went) went.layer.remove();
    went = null;
  }

  function offerClear() {
    if (el.gotoClear) el.gotoClear.hidden = el.gotoX.value === '' && el.gotoZ.value === '' && went === null;
  }

  el.goto.addEventListener('submit', (e) => {
    e.preventDefault();
    if (el.gotoX.value === '' || el.gotoZ.value === '') return;
    const x = Number(el.gotoX.value);
    const z = Number(el.gotoZ.value);
    if (!Number.isFinite(x) || !Number.isFinite(z) || !current) return;
    unmark();
    // The middle of the block, not its north-west corner.
    const at = [Math.floor(z) + 0.5, Math.floor(x) + 0.5];
    map.setView(at, Math.max(map.getZoom(), -1));
    const label = document.createElement('span');
    label.textContent = `X ${fmt(x)}, Z ${fmt(z)}`;
    const layer = L.marker(at, {
      icon: L.divIcon({ className: 'found', iconSize: [28, 28], iconAnchor: [14, 14] }),
      interactive: false,
      keyboard: false,
      zIndexOffset: 1000,
    }).bindTooltip(label, { permanent: true, direction: 'top', offset: [0, -14], className: 'marker-tip' });
    layer.addTo(map);
    went = { layer, dimension: current };
    offerClear();
  });

  // A pair or a triple pasted into the first box is split between the two:
  // the game, a chat message and this page's own card all give them whole.
  el.gotoX.addEventListener('paste', (e) => {
    const pasted = coordinates((e.clipboardData || window.clipboardData)?.getData('text') ?? '');
    if (!pasted) return;
    e.preventDefault();
    el.gotoX.value = String(Math.floor(pasted.x));
    el.gotoZ.value = String(Math.floor(pasted.z));
    offerClear();
    el.goto.querySelector('button[type="submit"]').focus();
  });

  for (const box of [el.gotoX, el.gotoZ]) box.addEventListener('input', offerClear);
  if (el.gotoClear) {
    el.gotoClear.addEventListener('click', () => {
      el.gotoX.value = '';
      el.gotoZ.value = '';
      unmark();
      offerClear();
      el.gotoX.focus();
    });
  }
  // The mark is of a block in one dimension, and of whoever asked for it.
  document.addEventListener('mcmap:view', () => {
    if (went && (went.dimension !== current || !el.login.hidden)) {
      unmark();
      offerClear();
    }
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
