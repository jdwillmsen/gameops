'use strict';

// The inside of the world: a cut through the dimension at a chosen height,
// in the colours of its blocks, laid over the terrain. The server draws
// each tile from the world's own files when it is asked for, so it is only
// ever drawn for what is in view, and addresses it as the terrain's are, so
// that the two line up to the block. It is off until the viewer turns it on.
//
// Two rows, in one of which the cut is the world with everything above the
// height taken away, and in the other the ground of the caves and rooms in
// the band under it with the rock between them dimmed; at most one is on.
// Under each is the same choice of height. In the nether that choice is the
// nether's own levels, the row of buttons over the map, so that the two are
// one thing and not two that can disagree.
(() => {
  const app = window.mcmap;
  // The page and its scripts are cached apart for a few minutes, so just
  // after a release this can meet a page that has no panel yet.
  if (!app || !app.layers || !app.layers.register) return;
  const { map } = app;
  const settings = window.mcmapSettings || null;

  // The listing is read once per snapshot, and says when a new one is there.
  const REFRESH_MS = 60_000;
  const RETRY_MS = 30_000;
  const STYLES = [
    { id: 'floor', label: 'Cave spaces', order: 10 },
    { id: 'cut', label: 'Cut view', order: 20 },
  ];
  // Where the overworld's cut is until the viewer chooses, if the server
  // offers it: down in the stone, where there is something to see.
  const FIRST_OVERWORLD_LEVEL = '0';
  const LEVEL = /^-?[0-9]{1,3}$/;

  // Whether the service has the views at all; null until it has answered.
  let available = null;
  const rows = new Map();
  // The current dimension's listing: { dimension, offered, ready, version,
  // tiles, styles, levels }, or null before the answer.
  let listing = null;
  let fetchedAt = 0;
  let pending = null;
  let layer = null;
  // The nether's height where the page has no levels of its own to follow.
  let ownNether = '';

  const str = (v) => (typeof v === 'string' ? v : '');
  const on = (id) => rows.has(id) && rows.get(id).enabled;
  const styleOn = () => STYLES.find((style) => on(style.id)) || null;

  const pastNow = () => Boolean(app.past && app.past.get && app.past.get());
  function drawable() {
    return styleOn() !== null && listing !== null && listing.offered && listing.ready && listing.version !== ''
      && listing.tiles.size === 256 && app.dimension() === listing.dimension && !pastNow() && !document.body.classList.contains('locked');
  }

  const known = (id) => listing !== null && listing.levels.some((l) => l.id === id);

  // The height drawn: the nether's own level where the map has them, the
  // viewer's choice kept in the record for the overworld, and the one there
  // is for the end.
  function levelId() {
    if (listing === null || listing.levels.length === 0) return '';
    if (listing.dimension === 'nether') {
      const own = app.level && app.level.list ? app.level.list() : [];
      const chosen = app.level && app.level.get ? app.level.get() : '';
      const hit = own.find((l) => l.id === chosen);
      if (hit && known(String(hit.y))) return String(hit.y);
      return known(ownNether) ? ownNether : listing.levels[0].id;
    }
    if (listing.dimension === 'overworld') {
      const kept = settings ? settings.get('underground').level : null;
      if (kept && known(kept)) return kept;
      return known(FIRST_OVERWORLD_LEVEL) ? FIRST_OVERWORLD_LEVEL : listing.levels[0].id;
    }
    return listing.levels[0].id;
  }

  function choose(id) {
    if (!known(id)) return false;
    if (listing.dimension === 'nether') {
      const own = app.level && app.level.list ? app.level.list() : [];
      const hit = own.find((l) => String(l.y) === id);
      if (hit) return app.level.set(hit.id);
      ownNether = id;
    } else if (listing.dimension === 'overworld' && settings) {
      settings.set('underground', { level: id });
    }
    paint();
    return true;
  }

  const Tiles = L.TileLayer.extend({
    getTileUrl(c) {
      const o = this.options;
      return `api/layers/tiles/${o.dimension}/${c.z}/${c.x}/${c.y}.png?level=${encodeURIComponent(o.level)}&style=${o.style}&v=${encodeURIComponent(o.version)}`;
    },
  });

  // Brings the layer in line with the listing and the rows. Another height
  // or style or a new reading redraws the layer there is; only another
  // dimension, whose world is other ground, replaces it.
  function overlay() {
    if (!drawable()) {
      if (layer) map.removeLayer(layer);
      layer = null;
      return;
    }
    if (layer && layer.options.dimension !== listing.dimension) {
      map.removeLayer(layer);
      layer = null;
    }
    const level = levelId();
    const style = styleOn().id;
    if (!layer) {
      layer = new Tiles('', {
        dimension: listing.dimension,
        level,
        style,
        version: listing.version,
        tileSize: listing.tiles.size,
        minNativeZoom: listing.tiles.minZoom,
        maxNativeZoom: listing.tiles.maxZoom,
        // Out past the widest tile the terrain is what is shown.
        minZoom: listing.tiles.minZoom,
        maxZoom: 8,
        bounds: app.extent ? app.extent() || undefined : undefined,
        noWrap: true,
        keepBuffer: 1,
        // Over the terrain, under the overlays.
        zIndex: 2,
        className: 'underground',
      }).addTo(map);
      return;
    }
    const o = layer.options;
    if (o.level === level && o.style === style && o.version === listing.version) return;
    o.level = level;
    o.style = style;
    o.version = listing.version;
    layer.redraw();
  }

  const noteOf = (style) => {
    if (listing === null) return '';
    if (!listing.offered) return 'Not offered in this dimension.';
    if (!listing.ready) return 'The world has not been opened for drawing yet.';
    if (Math.round(map.getZoom()) < listing.tiles.minZoom) return 'Zoom in to see it.';
    const said = listing.styles.find((s) => s.id === style.id);
    return said ? said.says : '';
  };

  function control() {
    if (listing === null || !listing.offered || listing.levels.length < 2) return null;
    const label = listing.dimension === 'overworld' ? 'Cut at y' : 'Level';
    return {
      label,
      options: listing.levels.map((l) => ({ value: l.id, label: l.label, title: `Cut at y ${l.y}` })),
      value: levelId(),
      onChange: (id) => { choose(id); },
    };
  }

  function paint() {
    if (available !== true) {
      for (const row of rows.values()) row.remove();
      rows.clear();
    } else if (rows.size === 0) {
      for (const style of STYLES) {
        const row = app.layers.register({
          group: 'underground', id: style.id, label: style.label, enabled: false, order: style.order, section: 'underground', groupLabel: 'Underground',
        });
        row.onToggle(() => {
          // At most one: the other was the one before.
          if (row.enabled) for (const other of STYLES) if (other.id !== style.id && on(other.id)) rows.get(other.id).setEnabled(false);
          paint();
          sync();
        });
        rows.set(style.id, row);
      }
    }
    overlay();
    if (rows.size === 0) return;
    const offered = listing === null || listing.offered;
    for (const style of STYLES) {
      const row = rows.get(style.id);
      row.setAvailable(offered);
      row.setNote(on(style.id) || listing !== null ? noteOf(style) : '');
      row.setControl(control());
    }
  }

  function clear() {
    listing = null;
    fetchedAt = 0;
  }

  async function load(dimension) {
    if (pending === dimension) return;
    pending = dimension;
    try {
      const res = await fetch(`api/layers?dimension=${encodeURIComponent(dimension)}`, { cache: 'no-store' });
      if (pending !== dimension) return; // the view moved on while this was out
      if (res.status === 404) {
        // The service is running without it, and will be until it restarts.
        available = false;
        clear();
        paint();
        return;
      }
      // Logged out: the map's own check shows the login, and the view is
      // announced again once there is a session.
      if (!res.ok) throw new Error(String(res.status));
      const data = await res.json();
      if (pending !== dimension) return;
      available = true;
      const tiles = data.tiles && typeof data.tiles === 'object' ? data.tiles : {};
      listing = {
        dimension,
        offered: Boolean(data.offered),
        ready: Boolean(data.ready),
        version: str(data.version),
        tiles: { minZoom: Number(tiles.minZoom), maxZoom: Number(tiles.maxZoom), size: Number(tiles.size) },
        styles: (Array.isArray(data.styles) ? data.styles : []).filter((s) => s && typeof s === 'object').map((s) => ({ id: str(s.id), says: str(s.says) })),
        levels: (Array.isArray(data.levels) ? data.levels : []).filter((l) => l && typeof l === 'object' && LEVEL.test(str(l.id)))
          .map((l) => ({ id: str(l.id), y: Number(l.y), label: str(l.label) || str(l.id) })),
      };
      // Before the world is open, ask again soon rather than in a minute.
      fetchedAt = listing.ready || !listing.offered ? Date.now() : Date.now() - REFRESH_MS + RETRY_MS;
      paint();
    } catch {
      fetchedAt = Date.now() - REFRESH_MS + RETRY_MS;
    } finally {
      if (pending === dimension) pending = null;
    }
  }

  // Brings the listing in line with the view: the current dimension's, and
  // none at all while logged out.
  function sync() {
    const locked = document.body.classList.contains('locked');
    const dimension = app.dimension();
    if (available === false) return;
    if (locked || !dimension) {
      // An answer still on its way belongs to the view that asked for it.
      pending = null;
      clear();
      paint();
      return;
    }
    if (!listing || listing.dimension !== dimension) {
      clear();
      paint();
      load(dimension);
    } else if (styleOn() !== null && Date.now() - fetchedAt > REFRESH_MS && !document.hidden) {
      // Only while one is on: off, there is no tile to be stale.
      load(dimension);
    } else {
      // The nether's level or the day may have changed under the page.
      paint();
    }
  }

  // The height rides with a saved view's place, in the field the nether's
  // level is in, for the overworld: a place that names a height this page
  // offers puts it back, and one that does not leaves it as it is.
  if (app.place && typeof app.place.get === 'function' && typeof app.place.set === 'function') {
    const get = app.place.get;
    const set = app.place.set;
    app.place.get = () => {
      const where = get.call(app.place);
      if (where && where.d === 'overworld' && settings) {
        const kept = settings.get('underground').level;
        if (kept) where.level = kept;
      }
      return where;
    };
    app.place.set = (at) => {
      if (at && at.d === 'overworld' && typeof at.level === 'string' && LEVEL.test(at.level) && settings) settings.set('underground', { level: at.level });
      return set.call(app.place, at);
    };
  }

  document.addEventListener('mcmap:view', sync);
  document.addEventListener('mcmap:settings', (e) => {
    if (e.detail && Array.isArray(e.detail.sections) && e.detail.sections.includes('underground')) paint();
  });
  document.addEventListener('visibilitychange', sync);
  map.on('zoomend', paint);
  setInterval(sync, RETRY_MS);
  sync();
})();
