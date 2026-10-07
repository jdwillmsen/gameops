'use strict';

// Slime chunks. On Bedrock a chunk is one or not by its coordinates alone,
// the same in every world, so nothing is asked of the server: each tile of
// the layer works out the chunks it covers as it is drawn, and a tile is
// only ever drawn for what is in view.
(() => {
  const app = window.mcmap;
  // The page and its scripts are cached apart for a few minutes, so just
  // after a release this can meet a page that has no panel yet.
  if (!app || !app.layers || !app.layers.register) return;
  const { map } = app;

  const TILE = 256;
  const CHUNK = 16;
  // A chunk is four pixels across here and two at the next zoom out, where
  // a tenth of them shaded says nothing about any one of them.
  const MIN_ZOOM = -2;
  // Below this many pixels a chunk has no room for an outline inside it.
  const OUTLINED_FROM = 8;
  // The theme's colours for them, and how much of their usual strength
  // the viewer has them drawn at, where the page keeps such choices.
  const settings = app.settings || null;
  const themed = (name, fallback) => (settings && settings.colour(name)) || fallback;
  const strength = () => {
    const chosen = settings ? settings.look().opacitySlime : NaN;
    return Number.isFinite(chosen) ? chosen / 100 : 1;
  };

  // The game seeds a Mersenne Twister with the chunk's coordinates and
  // takes its first number, which is made from words 0, 1 and 397 of the
  // seeded state alone. This is the function the service's own Go is
  // tested against, kept letter for letter.
  function isSlimeChunk(cx, cz) {
    const state = new Uint32Array(398);
    state[0] = Math.imul(cx, 0x1f1f1f1f) ^ cz;
    for (let i = 1; i < 398; i++) {
      state[i] = Math.imul(1812433253, state[i - 1] ^ (state[i - 1] >>> 30)) + i;
    }
    const y = (state[0] & 0x80000000) | (state[1] & 0x7fffffff);
    let v = state[397] ^ (y >>> 1) ^ (y & 1 ? 0x9908b0df : 0);
    v ^= v >>> 11;
    v ^= (v << 7) & 0x9d2c5680;
    v ^= (v << 15) & 0xefc60000;
    v ^= v >>> 18;
    return (v >>> 0) % 10 === 0;
  }

  const Slime = L.GridLayer.extend({
    createTile(c) {
      const canvas = L.DomUtil.create('canvas', 'leaflet-tile');
      canvas.width = canvas.height = TILE;
      // Pixels to a chunk, and where this tile starts, in pixels.
      const side = CHUNK * 2 ** c.z;
      const x0 = c.x * TILE;
      const z0 = c.y * TILE;
      const ctx = canvas.getContext('2d');
      ctx.fillStyle = side >= OUTLINED_FROM ? themed('slime-fill', 'rgba(110, 207, 122, 0.22)') : themed('slime-far', 'rgba(110, 207, 122, 0.5)');
      ctx.strokeStyle = themed('slime-line', 'rgba(110, 207, 122, 0.95)');
      for (let cz = Math.floor(z0 / side); cz * side < z0 + TILE; cz++) {
        for (let cx = Math.floor(x0 / side); cx * side < x0 + TILE; cx++) {
          if (!isSlimeChunk(cx, cz)) continue;
          const px = cx * side - x0;
          const pz = cz * side - z0;
          ctx.fillRect(px, pz, side, side);
          if (side >= OUTLINED_FROM) ctx.strokeRect(px + 0.5, pz + 0.5, side - 1, side - 1);
        }
      }
      return canvas;
    },
  });

  // Over the terrain and the biomes, under the grid.
  const layer = new Slime({ tileSize: TILE, minZoom: MIN_ZOOM, maxZoom: 8, zIndex: 4, opacity: strength() });
  const row = app.layers.register({ group: 'overlays', id: 'slime', label: 'Slime chunks', enabled: false, order: 10, swatch: 'key slime' });

  function sync() {
    const locked = document.body.classList.contains('locked');
    const dimension = app.dimension();
    // The rule is the overworld's: the other dimensions have no slime
    // chunks to show.
    const here = dimension === 'overworld';
    row.setAvailable(here);
    const want = here && !locked && row.enabled;
    if (want && !map.hasLayer(layer)) layer.addTo(map);
    if (!want && map.hasLayer(layer)) map.removeLayer(layer);
    let note = '';
    if (dimension && !here) note = 'Only the Overworld has slime chunks.';
    else if (want && map.getZoom() < MIN_ZOOM) note = 'Zoom in to see them.';
    row.setNote(note);
  }

  // For anything else that wants to know, and for checking this against
  // the vectors the service's own implementation is tested with.
  app.isSlimeChunk = isSlimeChunk;

  let theme = settings ? settings.look().theme : '';
  document.addEventListener('mcmap:settings', (e) => {
    if (!e.detail || !e.detail.sections.includes('look')) return;
    layer.setOpacity(strength());
    if (theme === settings.look().theme) return;
    theme = settings.look().theme;
    if (map.hasLayer(layer)) layer.redraw();
  });

  row.onToggle(sync);
  map.on('zoomend', sync);
  document.addEventListener('mcmap:view', sync);
  sync();
})();
