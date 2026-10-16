'use strict';

// The shape of the land, as light and shadow, contour lines and a tint by
// height laid over the terrain. The server draws each tile from the
// surface heights it keeps, in the one picture for however many of the
// three are on, and addresses it exactly as the terrain's are, so that
// the two are laid one over the other by the same Leaflet machinery and
// line up to the block. Turning a second drawing on changes the address of
// the tiles there are and asks for no more of them.
(() => {
  const app = window.mcmap;
  // The page and its scripts are cached apart for a few minutes, so just
  // after a release this can meet a page that has no panel yet.
  if (!app || !app.layers || !app.layers.register) return;
  const { map } = app;

  // The heights are read once per snapshot, and the listing is what says a
  // new reading is there; asking more often than this finds nothing new.
  const REFRESH_MS = 60_000;
  const RETRY_MS = 30_000;
  // A line is as far apart in blocks as it is at a block to a pixel from
  // there in, where the tiles are drawn smooth and not in squares.
  const ONE_BLOCK_ZOOM = 0;
  const COLOUR = /^#[0-9a-f]{6}$/i;
  // The rows, in the order they are listed and the order the server's
  // styles go in the address, each by the name the server knows it as.
  const STYLES = [
    { id: 'hillshade', label: 'Hillshade', order: 10, swatch: 'key hillshade' },
    { id: 'contours', label: 'Contour lines', order: 20, swatch: 'key contours' },
    { id: 'tint', label: 'Height tint', order: 30, swatch: 'key height' },
  ];

  // Whether the service has the overlay at all; null until it has answered.
  let available = null;
  const rows = new Map();
  // The current dimension's listing: { dimension, offered, ready, version,
  // tiles, intervals, heavyEvery, bands }, or null before the answer.
  let listing = null;
  // What each dimension's tint is made of, as ids this panel knows them by,
  // kept for the choices of the dimensions that are not shown.
  const bandIds = new Map();
  let fetchedAt = 0;
  let pending = null;
  let layer = null;

  const str = (v) => (typeof v === 'string' ? v : '');
  const count = (v) => (Number.isInteger(v) && v >= 0 ? v : 0);
  const bandId = (dimension, at) => `${dimension}:${at}`;
  // Which bands of the tint are drawn, which the panel keeps: any of them
  // hidden, or one shown alone.
  const choice = app.layers.facet ? app.layers.facet('topography', 'tint') : { shows: () => true, only: null, onChange() {} };

  // The nether has no land of its own to draw: its heightmap is the roof.
  // What is drawn is the floors under the level the map is cut at, so the
  // overlay follows the picker of levels; '' for any other dimension.
  const levelNow = () => {
    if (app.dimension() !== 'nether' || !app.level || typeof app.level.get !== 'function') return '';
    const id = app.level.get();
    return typeof id === 'string' && id !== 'default' ? id : '';
  };
  const levelQuery = (id) => (id ? `&level=${encodeURIComponent(id)}` : '');

  const on = (id) => rows.has(id) && rows.get(id).enabled;
  const anyOn = () => STYLES.some((style) => on(style.id));
  // Whether there is anything to draw here: the dimension has land, the
  // heights have been read, and the page is not waiting at the login.
  function drawable() {
    return anyOn() && listing !== null && listing.offered && listing.ready && listing.version !== ''
      && listing.tiles.size === 256 && app.dimension() === listing.dimension && !document.body.classList.contains('locked');
  }

  // The styles asked for as the server names them, and the bands of the
  // tint left out: the part of a tile's address that says what it is.
  function pickOf() {
    const styles = STYLES.filter((style) => on(style.id)).map((style) => style.id).join(',');
    let hide = '';
    if (on('tint')) {
      const hidden = listing.bands.map((_, at) => at).filter((at) => !choice.shows(bandId(listing.dimension, at)));
      // A list of just the bands to show that names some and none of this
      // dimension's was made over another's, and leaves none out here.
      const elsewhere = hidden.length === listing.bands.length && choice.state().mode === 'just' && choice.state().just.length > 0;
      if (hidden.length > 0 && !elsewhere) hide = `&hide=${hidden.join(',')}`;
    }
    return `styles=${styles}${hide}`;
  }

  const Tiles = L.TileLayer.extend({
    getTileUrl(c) {
      const o = this.options;
      return `api/topography/tiles/${o.dimension}/${c.z}/${c.x}/${c.y}.png?${o.pick}${levelQuery(o.level)}&v=${encodeURIComponent(o.version)}`;
    },
  });

  // Brings the overlay in line with the listing and the rows. A new
  // reading or another set of drawings redraws the layer there is; only
  // another dimension, whose terrain is other ground, replaces it.
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
    const pick = pickOf();
    if (!layer) {
      layer = new Tiles('', {
        dimension: listing.dimension,
        version: listing.version,
        pick,
        level: listing.level,
        tileSize: listing.tiles.size,
        minNativeZoom: listing.tiles.minZoom,
        maxNativeZoom: listing.tiles.maxZoom,
        minZoom: listing.tiles.minZoom,
        maxZoom: 8,
        bounds: app.extent ? app.extent() || undefined : undefined,
        noWrap: true,
        keepBuffer: 2,
        // Over the terrain and the biomes, under the grid.
        zIndex: 3,
        // Enlarged past the finest tile it is smoothed, not made into
        // squares, as the terrain's own are.
        className: 'topography',
      }).addTo(map);
      return;
    }
    if (layer.options.version === listing.version && layer.options.pick === pick && layer.options.level === listing.level) return;
    layer.options.version = listing.version;
    layer.options.pick = pick;
    layer.options.level = listing.level;
    layer.redraw();
  }

  // The height between two lines at the zoom the map is at, in blocks.
  function interval() {
    if (listing === null || listing.intervals.length === 0) return 0;
    const out = Math.round(Math.min(Math.max(map.getZoom(), listing.tiles.minZoom), ONE_BLOCK_ZOOM));
    return listing.intervals[Math.min(-out, listing.intervals.length - 1)] || 0;
  }

  const rangeOf = (b) => {
    if (b.to >= 32767) return `${b.from} and up`;
    if (b.from <= -32768) return `Below ${b.to + 1}`;
    return b.from === b.to ? `${b.from}` : `${b.from} to ${b.to}`;
  };

  function paint() {
    if (available !== true) {
      for (const row of rows.values()) row.remove();
      rows.clear();
    } else if (rows.size === 0) {
      for (const style of STYLES) {
        const row = app.layers.register({
          group: 'topography', id: style.id, label: style.label, enabled: false, order: style.order, section: 'topography',
          swatch: style.swatch, facet: style.id === 'tint' && app.layers.facet ? 'tint' : undefined,
        });
        row.onToggle(() => {
          paint();
          // Switched on, the listing may be as old as the page.
          sync();
        });
        rows.set(style.id, row);
      }
    }
    overlay();
    // Where the footer is told the height of the surface under the pointer.
    if (app.biomes && app.biomes.surface) app.biomes.surface(layer !== null);
    if (rows.size === 0) return;
    const known = listing !== null;
    const offered = !known || listing.offered;
    for (const style of STYLES) {
      const row = rows.get(style.id);
      row.setAvailable(offered);
      let note = '';
      if (known && !listing.offered) note = 'Not drawn in the Nether, where the surface is the roof of bedrock everywhere.';
      else if (known && !listing.ready) note = 'The heights have not been read yet.';
      else if (known && row.enabled && Math.round(map.getZoom()) < listing.tiles.minZoom) note = 'Zoom in to see it.';
      else if (style.id === 'contours' && row.enabled && interval() > 0) note = `A line every ${interval()} blocks at this zoom, every ${listing.heavyEvery}th heavier.`;
      row.setNote(note);
    }
    if (rows.get('tint').setItems) {
      // The legend: what height each colour is, lowest first. A band is
      // told apart by its dimension too, so a choice made over one
      // dimension's bands is not read as another's.
      const bands = known && listing.offered ? listing.bands : [];
      rows.get('tint').setItems(bands.map((b, at) => ({
        id: bandId(listing.dimension, at),
        label: b.label ? `${rangeOf(b)} · ${b.label}` : rangeOf(b),
        colour: COLOUR.test(b.colour) ? b.colour : '',
        swatch: COLOUR.test(b.colour) ? '' : 'key clear',
      })));
      if (rows.get('tint').setElsewhere) {
        rows.get('tint').setElsewhere([...bandIds].filter(([dimension]) => !known || dimension !== listing.dimension).flatMap(([, ids]) => ids));
      }
    }
  }

  // A band shown alone that this dimension's tint does not have would
  // leave all of it out, so it is let go of: the bands are a dimension's
  // own, and the one choice is over them all.
  function settle() {
    if (listing !== null && choice.only !== null && !bandIds.get(listing.dimension).includes(choice.only)) choice.solo(null);
    else paint();
  }

  function clear() {
    listing = null;
    fetchedAt = 0;
  }

  async function load(dimension) {
    if (pending === dimension) return;
    pending = dimension;
    // A level chosen while this was out was turned away by the check above,
    // so it is asked for once this answer is in; only after an answer, so a
    // failing request does not ask again at once.
    let moved = false;
    try {
      const level = dimension === 'nether' ? levelNow() : '';
      const res = await fetch(`api/topography?dimension=${encodeURIComponent(dimension)}${levelQuery(level)}`, { cache: 'no-store' });
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
        level,
        offered: Boolean(data.offered),
        ready: Boolean(data.ready),
        version: str(data.version),
        tiles: { minZoom: Number(tiles.minZoom), maxZoom: Number(tiles.maxZoom), size: Number(tiles.size) },
        intervals: (Array.isArray(data.intervals) ? data.intervals : []).map(count),
        heavyEvery: count(data.heavyEvery) || 5,
        bands: (Array.isArray(data.bands) ? data.bands : []).filter((b) => b && typeof b === 'object')
          .map((b) => ({ from: Number(b.from), to: Number(b.to), colour: str(b.colour), label: str(b.label) })),
      };
      bandIds.set(dimension, listing.bands.map((_, at) => bandId(dimension, at)));
      // Before the first reading, ask again soon rather than in a minute.
      fetchedAt = listing.ready || !listing.offered ? Date.now() : Date.now() - REFRESH_MS + RETRY_MS;
      settle();
      moved = dimension === 'nether' && level !== levelNow();
    } catch {
      fetchedAt = Date.now() - REFRESH_MS + RETRY_MS;
    } finally {
      if (pending === dimension) pending = null;
    }
    if (moved && pending === null && app.dimension() === 'nether') load('nether');
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
    } else if (dimension === 'nether' && listing.level !== levelNow()) {
      // Another level's floors are another reading to ask about; the layer
      // there is stays until the answer, and is drawn again from it.
      load(dimension);
    } else if (anyOn() && Date.now() - fetchedAt > REFRESH_MS && !document.hidden) {
      // Only while something is on: off, there is no tile to be stale.
      load(dimension);
    }
  }

  choice.onChange(settle);
  map.on('zoomend', paint);
  document.addEventListener('mcmap:view', sync);
  document.addEventListener('visibilitychange', sync);
  setInterval(sync, RETRY_MS);
  sync();
})();
