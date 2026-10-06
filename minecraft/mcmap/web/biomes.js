'use strict';

// The world's biomes, as a tint over the terrain. The server cuts the
// overlay into tiles addressed exactly as the terrain's are, so the two are
// laid one over the other by the same Leaflet machinery and line up to the
// block. The legend lists what the current dimension holds, and choosing
// one entry asks for tiles in which only that biome keeps its colour.
(() => {
  const app = window.mcmap;
  // The page and its scripts are cached apart for a few minutes, so just
  // after a release this can meet a page that has no panel yet.
  if (!app || !app.layers || !app.layers.register) return;
  const { map } = app;

  // The biomes are read once per snapshot, and the listing is what says a
  // new reading is there; asking more often than this finds nothing new.
  const REFRESH_MS = 60_000;
  const RETRY_MS = 30_000;
  // How long the pointer rests before the biome under it is asked for.
  const AT_SETTLE_MS = 150;
  const OPACITY = 0.6;
  // The terrain is cut at one block to a pixel at most, and so is this: a
  // finer tile would say nothing more and cost eight times the requests.
  const FINEST_ZOOM = 0;
  const COLOUR = /^#[0-9a-f]{6}$/i;

  const el = { at: document.getElementById('biome') };

  // A tile carries the reading's version, which lets the browser keep it
  // for good: a new reading is a new address.
  const Tiles = L.TileLayer.extend({
    getTileUrl(c) {
      const o = this.options;
      const only = o.only ? `biome=${encodeURIComponent(o.only)}&` : '';
      return `api/biomes/tiles/${o.dimension}/${c.z}/${c.x}/${c.y}.png?${only}v=${encodeURIComponent(o.version)}`;
    },
  });

  // Whether the service has biomes at all; null until it has answered.
  let available = null;
  let row = null;
  // The current dimension's listing: { dimension, extracted, version,
  // biomes, tiles }, or null before the answer.
  let listing = null;
  let fetchedAt = 0;
  let pending = null;
  // The one biome picked out, by the game's identifier, or null for all.
  let only = null;
  // Whether the overlay was asked for before there was a row to switch.
  let queued = false;
  let layer = null;
  // What the legend was last built from, so that an unchanged listing does
  // not rebuild what the viewer may be scrolling.
  let legendOf = '';
  const legend = document.createElement('div');
  legend.className = 'legend';

  let atTimer = null;
  let atRequest = null;
  let atShown = '';

  const on = () => row !== null && row.enabled;
  const str = (v) => (typeof v === 'string' ? v : '');
  const held = (name) => Boolean(listing) && listing.biomes.some((b) => b.name === name);
  const labelOf = (name) => {
    const found = listing ? listing.biomes.find((b) => b.name === name) : null;
    return found ? found.label : name;
  };

  function share(area, total) {
    if (!(total > 0)) return '';
    const percent = (area / total) * 100;
    return percent < 1 ? '<1%' : `${Math.round(percent)}%`;
  }

  // An entry of the legend. The name is the server's and is set as text;
  // the colour is taken only in the one form the server gives it.
  function entry(label, colour, detail, pressed, choose) {
    const button = document.createElement('button');
    button.type = 'button';
    button.setAttribute('aria-pressed', String(pressed));
    const swatch = document.createElement('i');
    if (COLOUR.test(colour)) swatch.style.backgroundColor = colour;
    else swatch.className = 'every';
    const name = document.createElement('span');
    name.className = 'name';
    name.textContent = label;
    const size = document.createElement('span');
    size.className = 'share';
    size.textContent = detail;
    button.append(swatch, name, size);
    button.addEventListener('click', choose);
    const item = document.createElement('li');
    item.append(button);
    return item;
  }

  function buildLegend() {
    const key = `${listing.dimension}|${listing.version}|${only || ''}`;
    if (key === legendOf) return;
    legendOf = key;
    const total = listing.biomes.reduce((sum, b) => sum + b.area, 0);
    const list = document.createElement('ul');
    list.setAttribute('aria-label', 'Biomes in this dimension, largest first');
    list.append(entry('All biomes', '', '', only === null, () => pick(null)));
    for (const b of listing.biomes) {
      // Choosing the one already picked out is the way back as well.
      list.append(entry(b.label, b.color, share(b.area, total), only === b.name, () => pick(only === b.name ? null : b.name)));
    }
    legend.replaceChildren(list);
  }

  function drawable() {
    return on() && listing !== null && listing.extracted && listing.version !== ''
      && listing.biomes.length > 0 && listing.tiles.size === 256 && app.dimension() === listing.dimension;
  }

  // Brings the overlay in line with the listing, the switch and the biome
  // picked out. A new reading or another biome redraws the layer there is;
  // only another dimension, whose terrain covers other ground, replaces it.
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
    if (!layer) {
      layer = new Tiles('', {
        dimension: listing.dimension,
        version: listing.version,
        only,
        tileSize: listing.tiles.size,
        minNativeZoom: listing.tiles.minZoom,
        maxNativeZoom: Math.min(listing.tiles.maxZoom, FINEST_ZOOM),
        minZoom: listing.tiles.minZoom,
        maxZoom: 8,
        bounds: app.extent ? app.extent() || undefined : undefined,
        noWrap: true,
        keepBuffer: 2,
        opacity: OPACITY,
        // Over the terrain and under the grid.
        zIndex: 2,
      }).addTo(map);
      return;
    }
    if (layer.options.version === listing.version && layer.options.only === only) return;
    layer.options.version = listing.version;
    layer.options.only = only;
    layer.redraw();
  }

  function paint() {
    if (available !== true) {
      if (row) row.remove();
      row = null;
    } else if (!row) {
      row = app.layers.register({ group: 'biomes', id: 'overlay', label: 'Biome overlay', enabled: false, order: 10 });
      if (queued) row.setEnabled(true);
      queued = false;
      row.onToggle(() => {
        paint();
        // Switched on, the listing may be as old as the page.
        sync();
      });
    }
    overlay();
    const showing = layer !== null;
    if (el.at) el.at.hidden = !showing;
    if (!showing) forget();
    if (!row) return;
    const known = listing !== null && listing.extracted;
    row.setCount(known ? listing.biomes.length : null);
    let note = '';
    if (listing !== null && !listing.extracted) note = 'The biomes have not been read yet.';
    else if (known && listing.biomes.length === 0) note = 'The world holds no biomes for this dimension.';
    else if (on() && only !== null) note = `Showing only ${labelOf(only)}.`;
    row.setNote(note);
    if (on() && known && listing.biomes.length > 0) {
      buildLegend();
      row.setBody(legend);
    } else {
      row.setBody(null);
    }
  }

  // Picks one biome out, or with null goes back to all of them.
  function pick(name) {
    only = name;
    paint();
  }

  function clear() {
    listing = null;
    fetchedAt = 0;
    legendOf = '';
  }

  async function load(dimension) {
    if (pending === dimension) return;
    pending = dimension;
    try {
      const res = await fetch(`api/biomes?dimension=${encodeURIComponent(dimension)}`, { cache: 'no-store' });
      if (pending !== dimension) return; // the view moved on while this was out
      if (res.status === 404) {
        // The service is running without biomes, and will be until it
        // restarts.
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
        extracted: Boolean(data.extracted),
        version: str(data.version),
        biomes: (Array.isArray(data.biomes) ? data.biomes : [])
          .filter((b) => b && str(b.name) !== '')
          .map((b) => ({ name: b.name, label: str(b.label) || b.name, color: str(b.color), area: Number.isFinite(b.area) ? b.area : 0 })),
        tiles: { minZoom: Number(tiles.minZoom), maxZoom: Number(tiles.maxZoom), size: Number(tiles.size) },
      };
      // Before the first reading, ask again soon rather than in a minute.
      fetchedAt = listing.extracted ? Date.now() : Date.now() - REFRESH_MS + RETRY_MS;
      // A biome picked out that this dimension does not hold would dim
      // the whole of it.
      if (only !== null && !held(only)) only = null;
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
      only = null;
      queued = false;
      paint();
      return;
    }
    if (!listing || listing.dimension !== dimension) {
      if (listing) only = null;
      clear();
      paint();
      load(dimension);
    } else if (on() && Date.now() - fetchedAt > REFRESH_MS && !document.hidden) {
      // Only while the overlay is on: off, there is no tile to be stale.
      load(dimension);
    }
  }

  // --- the biome under the pointer -----------------------------------------

  function say(text) {
    atShown = text;
    if (el.at && el.at.textContent !== text) el.at.textContent = text;
  }

  function forget() {
    clearTimeout(atTimer);
    if (atRequest) atRequest.abort();
    atRequest = null;
    if (atShown !== '') say('');
  }

  async function ask(x, z) {
    if (atRequest) atRequest.abort();
    const request = new AbortController();
    atRequest = request;
    const dimension = listing.dimension;
    let data;
    try {
      const res = await fetch(`api/biomes/at?dimension=${encodeURIComponent(dimension)}&x=${x}&z=${z}`, { cache: 'no-store', signal: request.signal });
      if (!res.ok) return;
      data = await res.json();
    } catch {
      return; // superseded, or a dropped request: the next move asks again
    }
    if (atRequest !== request || layer === null) return;
    atRequest = null;
    const label = data && data.generated && data.biome ? str(data.biome.label) || str(data.biome.name) : '';
    say(label ? `Biome: ${label}` : 'Biome: not generated here');
  }

  let asked = '';
  function at(latlng, settle) {
    if (layer === null || !el.at) return;
    const x = Math.floor(latlng.lng);
    const z = Math.floor(latlng.lat);
    const key = `${listing.dimension}|${listing.version}|${x}|${z}`;
    if (key === asked) return;
    clearTimeout(atTimer);
    atTimer = setTimeout(() => {
      asked = key;
      ask(x, z);
    }, settle);
  }

  map.on('mousemove', (e) => at(e.latlng, AT_SETTLE_MS));
  // A tap has no hover before it, and a click is a plain request.
  map.on('click', (e) => at(e.latlng, 0));

  // For the search: shows the overlay with one biome picked out, in
  // whatever dimension the map is on by the time the listing arrives.
  app.biomes = {
    show(name) {
      if (available === false || typeof name !== 'string' || !name) return false;
      only = name;
      // A result chosen before the first listing has answered: the row is
      // switched on when the answer makes it.
      if (row) row.setEnabled(true);
      else queued = true;
      paint();
      return true;
    },
  };

  document.addEventListener('mcmap:view', sync);
  document.addEventListener('visibilitychange', sync);
  setInterval(sync, RETRY_MS);
  sync();
})();
