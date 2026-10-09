'use strict';

// The chunk under the pointer, while the grid is on: which chunk it is,
// the blocks it covers and whether slimes spawn in it, with an outline on
// the map. A click pins one, so that it can be read off and found again
// after panning, and a chunk can be gone to by its own coordinates. From
// too far out to tell one chunk from the next, the same is said of the
// region, the 32 chunks square the grid falls back to.
(() => {
  const app = window.mcmap;
  if (!app || !app.map) return;
  const { map } = app;

  const el = {
    grid: document.getElementById('grid'),
    box: document.getElementById('chunk'),
    title: document.getElementById('chunk-title'),
    state: document.getElementById('chunk-state'),
    blocks: document.getElementById('chunk-blocks'),
    slime: document.getElementById('chunk-slime'),
    pointer: document.getElementById('chunk-pointer'),
    form: document.getElementById('chunk-go'),
    x: document.getElementById('chunk-x'),
    z: document.getElementById('chunk-z'),
    unpin: document.getElementById('chunk-unpin'),
  };
  if (Object.values(el).some((node) => !node)) return;

  const CHUNK = 16;
  const REGION = 32;
  // A chunk narrower than this many pixels cannot be pointed at, and its
  // outline would be a dot.
  const MIN_PIXELS = 4;
  // No closer than this is needed to see a chunk whole.
  const GO_ZOOM = 1;
  // Past the edge of any Bedrock world, in chunks.
  const WORLD_EDGE = 2_000_000;

  const fmt = (n) => n.toLocaleString('en-US');
  const say = (node, text) => {
    if (node.textContent !== text) node.textContent = text;
  };

  // A cell is a chunk or a region: { unit, x, z }, in its own coordinates.
  // Math.floor, so that the chunk west of the origin is -1 and not 0.
  const sizeOf = (unit) => (unit === 'region' ? CHUNK * REGION : CHUNK);
  const unitNow = () => (CHUNK * 2 ** map.getZoom() >= MIN_PIXELS ? 'chunk' : 'region');
  const cellAt = (latlng, unit) => ({ unit, x: Math.floor(latlng.lng / sizeOf(unit)), z: Math.floor(latlng.lat / sizeOf(unit)) });
  const same = (a, b) => Boolean(a) && Boolean(b) && a.unit === b.unit && a.x === b.x && a.z === b.z;
  const boundsOf = (cell) => {
    const size = sizeOf(cell.unit);
    return [[cell.z * size, cell.x * size], [(cell.z + 1) * size, (cell.x + 1) * size]];
  };

  const outline = (style) => L.rectangle([[0, 0], [CHUNK, CHUNK]], { interactive: false, ...style });
  // The theme's colours for the two outlines, where the page has themes.
  const settings = window.mcmapSettings || null;
  const themed = (name, fallback) => (settings && settings.colour(name)) || fallback;
  const hoverStyle = () => ({ color: themed('chunk-hover', '#ffffff'), fillColor: themed('chunk-hover', '#ffffff') });
  const pinStyle = () => ({ color: themed('chunk-pin', '#6ecf7a'), fillColor: themed('chunk-pin', '#6ecf7a') });
  const hoverBox = outline({ weight: 1, fillOpacity: 0.14, ...hoverStyle() });
  const pinBox = outline({ weight: 2.5, fillOpacity: 0.1, ...pinStyle() });

  let hovered = null;
  let pinned = null;
  // The dimension a pin belongs to: chunk 3, -2 of the Nether is not the
  // chunk 3, -2 that was pinned in the Overworld.
  let pinnedIn = null;

  // The grid and the chunk focus each have a row among the panel's
  // overlays. The grid's is the switch in the bar by another door, and the
  // two are kept as one; the focus is on whenever the grid is unless its
  // own row is off. A page with no panel has the switch in the bar alone.
  const rows = { grid: null, focus: null };
  if (app.layers && app.layers.register) {
    rows.grid = app.layers.register({ group: 'overlays', id: 'grid', label: 'Grid', enabled: false, order: 30, swatch: 'key grid' });
    rows.focus = app.layers.register({ group: 'overlays', id: 'chunk', label: 'Chunk focus', enabled: true, order: 35, swatch: 'key focus' });
    rows.grid.setEnabled(el.grid.checked);
    rows.grid.onToggle((want) => {
      if (el.grid.checked !== want) el.grid.click();
    });
    el.grid.addEventListener('change', () => rows.grid.setEnabled(el.grid.checked));
  }

  const on = () => el.grid.checked && (!rows.focus || rows.focus.enabled) && !document.body.classList.contains('locked') && Boolean(app.dimension());

  function slimeOf(cell) {
    const dimension = app.dimension();
    if (dimension !== 'overworld') return `No slime chunks in ${app.label(dimension)}.`;
    if (!app.isSlimeChunk) return '';
    if (cell.unit === 'chunk') return app.isSlimeChunk(cell.x, cell.z) ? 'Slime chunk.' : 'Not a slime chunk.';
    let n = 0;
    for (let dz = 0; dz < REGION; dz++) {
      for (let dx = 0; dx < REGION; dx++) if (app.isSlimeChunk(cell.x * REGION + dx, cell.z * REGION + dz)) n += 1;
    }
    return `${fmt(n)} of its ${fmt(REGION * REGION)} chunks are slime chunks.`;
  }

  const titleOf = (cell) => `${cell.unit === 'region' ? 'Region' : 'Chunk'} ${fmt(cell.x)}, ${fmt(cell.z)}`;

  function rangeOf(cell) {
    const size = sizeOf(cell.unit);
    const blocks = `Blocks X ${fmt(cell.x * size)} to ${fmt(cell.x * size + size - 1)}, Z ${fmt(cell.z * size)} to ${fmt(cell.z * size + size - 1)}`;
    if (cell.unit === 'chunk') return blocks;
    return `Chunks X ${fmt(cell.x * REGION)} to ${fmt(cell.x * REGION + REGION - 1)}, Z ${fmt(cell.z * REGION)} to ${fmt(cell.z * REGION + REGION - 1)}. ${blocks}`;
  }

  // What the slime line was last worked out for: a region's is a thousand
  // chunks' worth, and the pointer moves many times within one cell.
  let slimeFor = '';

  function paint() {
    if (rows.focus) {
      rows.focus.setAvailable(el.grid.checked);
      rows.focus.setNote(el.grid.checked ? '' : 'Shown while the grid is on.');
    }
    const showing = on();
    el.box.hidden = !showing;
    document.body.classList.toggle('chunking', showing);
    if (!showing) {
      hoverBox.remove();
      pinBox.remove();
      return;
    }
    if (pinned) {
      pinBox.setBounds(boundsOf(pinned));
      if (!map.hasLayer(pinBox)) pinBox.addTo(map);
    } else {
      pinBox.remove();
    }
    if (hovered && !same(hovered, pinned)) {
      hoverBox.setBounds(boundsOf(hovered));
      if (!map.hasLayer(hoverBox)) hoverBox.addTo(map);
    } else {
      hoverBox.remove();
    }
    const shown = pinned || hovered;
    el.unpin.hidden = !pinned;
    if (!shown) {
      say(el.title, 'No chunk chosen');
      say(el.state, '');
      say(el.blocks, matchMedia('(hover: none)').matches ? 'Tap the map to pin a chunk.' : 'Point at the map, or click it to pin a chunk.');
      say(el.slime, '');
      say(el.pointer, '');
      slimeFor = '';
      return;
    }
    say(el.title, titleOf(shown));
    say(el.state, pinned ? 'pinned' : 'under the pointer');
    say(el.blocks, `${rangeOf(shown)}.`);
    const key = `${app.dimension()}|${shown.unit}|${shown.x}|${shown.z}`;
    if (key !== slimeFor) {
      slimeFor = key;
      say(el.slime, slimeOf(shown));
    }
    say(el.pointer, pinned && hovered && !same(hovered, pinned) ? `Pointer: ${titleOf(hovered).toLowerCase()}.` : '');
  }

  function pin(cell) {
    pinned = cell;
    pinnedIn = cell ? app.dimension() : null;
    paint();
  }

  // Takes the map to a chunk by its own coordinates and pins it, with the
  // grid switched on, since that is what shows it.
  function go(cx, cz) {
    if (!Number.isInteger(cx) || !Number.isInteger(cz) || Math.abs(cx) > WORLD_EDGE || Math.abs(cz) > WORLD_EDGE) return false;
    const dimension = app.dimension();
    if (!dimension) return false;
    if (!el.grid.checked) {
      el.grid.checked = true;
      el.grid.dispatchEvent(new Event('change'));
    }
    if (rows.focus) rows.focus.setEnabled(true);
    const cell = { unit: 'chunk', x: cx, z: cz };
    map.setView([(cz + 0.5) * CHUNK, (cx + 0.5) * CHUNK], Math.max(map.getZoom(), GO_ZOOM));
    pin(cell);
    return true;
  }

  map.on('mousemove', (e) => {
    if (!on()) return;
    const cell = cellAt(e.latlng, unitNow());
    if (same(cell, hovered)) return;
    hovered = cell;
    paint();
  });
  map.on('mouseout', () => {
    if (!hovered) return;
    hovered = null;
    if (on()) paint();
  });
  // A click on a marker is the marker's and does not reach here; a click
  // on the pinned cell lets it go.
  map.on('click', (e) => {
    if (!on()) return;
    const cell = cellAt(e.latlng, unitNow());
    pin(same(cell, pinned) ? null : cell);
  });
  // A hovered cell is of the unit the zoom had then.
  map.on('zoomend', () => {
    hovered = null;
    if (on()) paint();
  });

  el.form.addEventListener('submit', (e) => {
    e.preventDefault();
    if (el.x.value === '' || el.z.value === '') return;
    go(Number(el.x.value), Number(el.z.value));
  });
  el.unpin.addEventListener('click', () => pin(null));
  el.grid.addEventListener('change', paint);
  document.addEventListener('mcmap:view', () => {
    if (pinned && pinnedIn !== app.dimension()) {
      pinned = null;
      pinnedIn = null;
    }
    hovered = null;
    slimeFor = '';
    paint();
  });

  document.addEventListener('mcmap:settings', (e) => {
    if (!e.detail || !e.detail.sections.includes('look')) return;
    hoverBox.setStyle(hoverStyle());
    pinBox.setStyle(pinStyle());
  });

  if (rows.focus) rows.focus.onToggle(paint);

  app.chunk = {
    go,
    pinned: () => pinned,
    // Pins a chunk or a region where the map already is, or with null lets
    // the pin go, for a saved view that was saved with one.
    pin(cell) {
      const fine = cell && (cell.unit === 'chunk' || cell.unit === 'region') && Number.isInteger(cell.x) && Number.isInteger(cell.z)
        && Math.abs(cell.x) <= WORLD_EDGE && Math.abs(cell.z) <= WORLD_EDGE;
      pin(fine ? { unit: cell.unit, x: cell.x, z: cell.z } : null);
    },
  };
  paint();
})();
