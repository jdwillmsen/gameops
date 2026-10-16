'use strict';

// Chunks the world has lost. A chunk that was never generated and one that
// was and has gone look the same on the terrain: nothing. The service keeps
// a record of every chunk the world has held, and this marks, in red, the
// ones that record says were there at the last count and are not now, so
// that the two can be told apart. Off until the viewer turns it on; the
// tiles are drawn by the server, as the terrain's addresses are.
(() => {
  const app = window.mcmap;
  // The page and its scripts are cached apart for a few minutes, so just
  // after a release this can meet a page that has no panel yet.
  if (!app || !app.layers || !app.layers.register) return;
  const { map } = app;

  // The count is made once a cycle; asking more often finds nothing new.
  const REFRESH_MS = 60_000;
  const RETRY_MS = 30_000;

  // Whether the service keeps the record at all; null until it has answered.
  let available = null;
  let row = null;
  // The current dimension's listing: { dimension, offered, version, missing,
  // tiles }, or null before the answer.
  let listing = null;
  let fetchedAt = 0;
  let pending = null;
  let layer = null;

  const str = (v) => (typeof v === 'string' ? v : '');
  const count = (v) => (Number.isInteger(v) && v >= 0 ? v : 0);
  const pastNow = () => Boolean(app.past && app.past.get && app.past.get());
  const drawable = () => row !== null && row.enabled && listing !== null && listing.offered && listing.version !== ''
    && listing.tiles.size === 256 && app.dimension() === listing.dimension && !pastNow() && !document.body.classList.contains('locked');

  const Tiles = L.TileLayer.extend({
    getTileUrl(c) {
      const o = this.options;
      return `api/lost/tiles/${o.dimension}/${c.z}/${c.x}/${c.y}.png?v=${encodeURIComponent(o.version)}`;
    },
  });

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
        tileSize: listing.tiles.size,
        minNativeZoom: listing.tiles.minZoom,
        maxNativeZoom: listing.tiles.maxZoom,
        minZoom: listing.tiles.minZoom,
        maxZoom: 8,
        bounds: app.extent ? app.extent() || undefined : undefined,
        noWrap: true,
        keepBuffer: 2,
        // Over the terrain and the other overlays, under the grid.
        zIndex: 4,
        className: 'lost',
      }).addTo(map);
      return;
    }
    if (layer.options.version === listing.version) return;
    layer.options.version = listing.version;
    layer.redraw();
  }

  function paint() {
    if (available !== true) {
      if (row) row.remove();
      row = null;
    } else if (row === null) {
      row = app.layers.register({ group: 'overlays', id: 'lost', label: 'Lost chunks', enabled: false, order: 50, section: 'overlays', swatch: 'key lost' });
      row.onToggle(() => {
        paint();
        // Switched on, the listing may be as old as the page.
        sync();
      });
    }
    overlay();
    if (row === null) return;
    let note = '';
    if (listing !== null && !listing.offered) note = 'No record of what the world has held yet.';
    else if (listing !== null && listing.missing === 0) note = 'No chunk of this dimension is missing.';
    else if (listing !== null) note = `${listing.missing.toLocaleString('en-US')} chunks that were in the world are not now, marked in red. Ground in none is just not generated yet.`;
    row.setNote(note);
    row.setAvailable(listing === null || listing.offered);
  }

  function clear() {
    listing = null;
    fetchedAt = 0;
  }

  async function load(dimension) {
    if (pending === dimension) return;
    pending = dimension;
    try {
      const res = await fetch(`api/lost?dimension=${encodeURIComponent(dimension)}`, { cache: 'no-store' });
      if (pending !== dimension) return;
      if (res.status === 404) {
        available = false;
        clear();
        paint();
        return;
      }
      if (!res.ok) throw new Error(String(res.status));
      const data = await res.json();
      if (pending !== dimension) return;
      available = true;
      const tiles = data.tiles && typeof data.tiles === 'object' ? data.tiles : {};
      listing = {
        dimension,
        offered: Boolean(data.offered),
        version: str(data.version),
        missing: count(data.missing),
        tiles: { minZoom: Number(tiles.minZoom), maxZoom: Number(tiles.maxZoom), size: Number(tiles.size) },
      };
      fetchedAt = Date.now();
      paint();
    } catch {
      fetchedAt = Date.now() - REFRESH_MS + RETRY_MS;
    } finally {
      if (pending === dimension) pending = null;
    }
  }

  function sync() {
    const locked = document.body.classList.contains('locked');
    const dimension = app.dimension();
    if (available === false) return;
    if (locked || !dimension) {
      pending = null;
      clear();
      paint();
      return;
    }
    if (!listing || listing.dimension !== dimension) {
      clear();
      paint();
      load(dimension);
    } else if (row !== null && row.enabled && Date.now() - fetchedAt > REFRESH_MS && !document.hidden) {
      load(dimension);
    } else {
      // A past day or the login may have changed under the page.
      paint();
    }
  }

  document.addEventListener('mcmap:view', sync);
  document.addEventListener('visibilitychange', sync);
  setInterval(sync, RETRY_MS);
  sync();
})();
