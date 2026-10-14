'use strict';

// Height bands, as an indoor map has floors: every marker that has a Y is
// in one of a few bands, and the panel shows or hides a band, counts what
// is in it and, if asked, rings each marker in the colour of its band.
//
// What a band is depends on the dimension. In the overworld and the end it
// is relative to the surface under the marker, which the server sends with
// it (the surface is the game's own heightmap, the top of a tree or of the
// sea, not the ground): a marker is in the sky above it, at the surface
// within a margin of it, or under it. The overworld's underground is cut
// at y 0, where the deepslate begins, whatever the surface is. The
// nether's heightmap is its bedrock roof over everything, so its bands are
// fixed heights instead: the lava sea, the middle and the upper levels.
//
// A marker whose height is not known is never hidden by a band, and is
// counted apart: a predicted structure has no Y at all, and a column the
// world has not generated has no surface. The nether's bands need no
// surface, and a marker in the overworld under y 0 needs none either.
//
// This is only the sorting and the panel's rows; the layers that draw
// markers ask it which of theirs to show and what ring to give them.
(() => {
  const app = window.mcmap;
  if (!app || !app.layers || !app.layers.register || !app.layers.facet) return;

  // How far from the surface, in blocks, a marker still counts as at it:
  // a tree's crown and a player on the ground under a small one, a cellar
  // under a house and the roof of one.
  const MARGIN = 8;
  // How far under the surface the shallow underground goes. Past it, down
  // to y 0, is the deep underground.
  const SHALLOW = 32;
  // The nether's lava sea fills up to y 31, and its upper levels begin
  // where the fortresses' roofs and the bastions' towers do.
  const LAVA_TOP = 32;
  const UPPER_FROM = 96;
  // How far, in blocks of height, is near the viewer's own.
  const NEAR = 16;

  // The bands of each dimension, from the highest down. Colours go from
  // light at the top to dark at the bottom so that a ring's height can be
  // read from how dark it is as well as which colour.
  const BANDS = {
    overworld: [
      { id: 'sky', label: 'Sky', detail: `more than ${MARGIN} above the surface`, colour: '#bde0ff' },
      { id: 'surface', label: 'Surface', detail: `within ${MARGIN} of the surface`, colour: '#7fd7c4' },
      { id: 'shallow', label: 'Shallow underground', detail: `${MARGIN + 1} to ${SHALLOW} under the surface`, colour: '#e8c458' },
      { id: 'deep', label: 'Deep underground', detail: 'deeper, down to y 0', colour: '#d9774a' },
      { id: 'deepslate', label: 'Deepslate', detail: 'below y 0', colour: '#8a5cc9' },
    ],
    nether: [
      { id: 'upper', label: 'Upper', detail: `y ${UPPER_FROM} and up`, colour: '#bde0ff' },
      { id: 'middle', label: 'Middle', detail: `y ${LAVA_TOP} to ${UPPER_FROM - 1}`, colour: '#e8c458' },
      { id: 'lava', label: 'Lava sea', detail: `below y ${LAVA_TOP}`, colour: '#d9774a' },
    ],
    end: [
      { id: 'sky', label: 'Sky', detail: `more than ${MARGIN} above the islands`, colour: '#bde0ff' },
      { id: 'surface', label: 'Surface', detail: `within ${MARGIN} of an island’s top`, colour: '#7fd7c4' },
      { id: 'below', label: 'Below the islands', detail: `more than ${MARGIN} under the surface`, colour: '#8a5cc9' },
    ],
  };
  const OWN = Object.keys(BANDS);
  // Every id there is, whichever dimension has it: a choice made of one
  // is kept in the dimensions that do not list it.
  const EVERY = [...new Set(OWN.flatMap((d) => BANDS[d].map((b) => b.id)))];

  const fin = Number.isFinite;

  // The band of a block y over a surface of s, as an index into the
  // dimension's list, or -1 where it cannot be said. Called for every
  // entity of every frame, so it allocates nothing.
  function pointBand(dimension, y, s) {
    if (!fin(y)) return -1;
    if (dimension === 'nether') return y >= UPPER_FROM ? 0 : y >= LAVA_TOP ? 1 : 2;
    if (dimension === 'end') {
      if (!fin(s)) return -1;
      return y - s > MARGIN ? 0 : y - s >= -MARGIN ? 1 : 2;
    }
    if (dimension !== 'overworld') return -1;
    // Under y 0 is deepslate whatever the surface above it is.
    if (!fin(s)) return y < 0 ? 4 : -1;
    if (y - s > MARGIN) return 0;
    if (y - s >= -MARGIN) return 1;
    if (y < 0) return 4;
    return s - y <= SHALLOW ? 2 : 3;
  }

  // The same, by ranges, for a box that may span several bands: each
  // band's lowest and highest y as [low, high] with the ends inclusive and
  // an empty one with low above high, or null where the surface it needs
  // is not known.
  function ranges(dimension, s) {
    if (dimension === 'nether') return [[UPPER_FROM, Infinity], [LAVA_TOP, UPPER_FROM - 1], [-Infinity, LAVA_TOP - 1]];
    if (dimension === 'end') return fin(s) ? [[s + MARGIN + 1, Infinity], [s - MARGIN, s + MARGIN], [-Infinity, s - MARGIN - 1]] : [null, null, null];
    if (dimension !== 'overworld') return [];
    if (!fin(s)) return [null, null, null, null, [-Infinity, -1]];
    const under = s - MARGIN - 1;
    return [
      [s + MARGIN + 1, Infinity],
      [s - MARGIN, s + MARGIN],
      [Math.max(s - SHALLOW, 0), under],
      [0, s - SHALLOW - 1],
      [-Infinity, Math.min(-1, under)],
    ];
  }

  // The indexes of the bands a stretch of y from low to high is in, as
  // many as it overlaps. Empty where nothing of it can be placed.
  function boxBands(dimension, low, high, s) {
    if (!fin(low) || !fin(high)) return [];
    const [a, b] = low <= high ? [low, high] : [high, low];
    const out = [];
    ranges(dimension, s).forEach((range, at) => {
      if (range !== null && range[0] <= range[1] && range[0] <= b && range[1] >= a) out.push(at);
    });
    return out;
  }

  // --- what is chosen -----------------------------------------------------------

  const listeners = [];
  const viewerListeners = [];
  const tell = (list) => {
    for (const fn of [...list]) {
      try { fn(); } catch (err) { console.error(err); }
    }
  };

  let rows = null;
  let bandsChoice = null;
  const dimensionNow = () => (typeof app.dimension === 'function' ? app.dimension() : null);

  // Whether the band filter is in force, and which bands are shown, as
  // flags by index for the dimension last looked at.
  let filtering = false;
  let shownFor = null;
  let shownFlags = [];
  function readChoice(dimension) {
    shownFor = dimension;
    shownFlags = (BANDS[dimension] || []).map((band) => bandsChoice.shows(band.id));
  }
  const flagsFor = (dimension) => {
    if (shownFor !== dimension) readChoice(dimension);
    return shownFlags;
  };

  // The viewer's own height, from the live layer, with the dimension they
  // are in; null while they are not in the picture.
  let viewerAt = null;
  // Their block y while they are on the map's dimension, which is the only
  // time it says anything about what is on the map.
  const nearY = () => (viewerAt !== null && viewerAt.dimension === dimensionNow() ? viewerAt.y : null);
  const nearOn = () => rows !== null && rows.near.enabled && nearY() !== null;
  const colourOn = () => rows !== null && rows.colour.enabled;

  // Whether a marker at block y over a surface of s is to be drawn.
  function shows(dimension, y, s) {
    if (rows === null) return true;
    if (nearOn() && fin(y) && Math.abs(Math.floor(y) - viewerAt.y) > NEAR) return false;
    if (!filtering) return true;
    const at = pointBand(dimension, fin(y) ? Math.floor(y) : y, s);
    return at < 0 || flagsFor(dimension)[at] !== false;
  }

  // And a structure, drawn if any band its box reaches is shown, or if
  // its height is not known.
  function showsBox(dimension, low, high, s) {
    if (rows === null) return true;
    if (nearOn() && fin(low) && fin(high) && (Math.max(low, high) < viewerAt.y - NEAR || Math.min(low, high) > viewerAt.y + NEAR)) return false;
    if (!filtering) return true;
    const at = boxBands(dimension, low, high, s);
    if (at.length === 0) return true;
    const flags = flagsFor(dimension);
    return at.some((i) => flags[i] !== false);
  }

  // The colour of the ring a marker is given, or '' for none: colouring is
  // off, or the band is not known.
  function tint(dimension, y, s) {
    if (!colourOn()) return '';
    const at = pointBand(dimension, fin(y) ? Math.floor(y) : y, s);
    return at < 0 ? '' : BANDS[dimension][at].colour;
  }

  // --- counts -------------------------------------------------------------------
  //
  // Each layer that draws markers offers a function that is handed a way to
  // count them; the panel's counts are made again a moment after any of
  // them says it has changed, and only for the dimension on the map.

  const COUNT_MS = 250;
  const NEAR_WAITS = 'Needs you online and on the map';
  const sources = new Map();
  let counting = 0;
  const fmt = (n) => n.toLocaleString('en-US');

  function recount() {
    counting = 0;
    if (rows === null) return;
    const dimension = dimensionNow();
    const bands = BANDS[dimension] || [];
    const counts = bands.map(() => 0);
    let unknown = 0;
    const add = (y, s, n = 1) => {
      const at = pointBand(dimension, fin(y) ? Math.floor(y) : y, s);
      if (at < 0) unknown += n; else counts[at] += n;
    };
    const addBox = (low, high, s) => {
      const at = boxBands(dimension, low, high, s);
      if (at.length === 0) unknown += 1;
      for (const i of at) counts[i] += 1;
    };
    for (const fn of sources.values()) {
      try { fn(add, addBox, dimension); } catch (err) { console.error(err); }
    }
    const coloured = colourOn();
    rows.bands.setItems(bands.map((band, at) => ({ id: band.id, label: band.label, note: band.detail, count: counts[at], ...(coloured ? { colour: band.colour } : {}) })));
    rows.bands.setNote(unknown > 0 ? `${fmt(unknown)} with no known height, always shown` : '');
  }

  function changed() {
    if (rows !== null && counting === 0) counting = setTimeout(recount, COUNT_MS);
  }

  // --- the panel ----------------------------------------------------------------

  function chosen() {
    filtering = rows !== null && rows.bands.enabled;
    shownFor = null;
    recount();
    tell(listeners);
  }

  function panel() {
    bandsChoice = app.layers.facet('heights', 'bands');
    rows = {
      bands: app.layers.register({ group: 'heights', id: 'bands', label: 'Filter by height', order: 10, facet: 'bands', swatch: 'ring heights' }),
      near: app.layers.register({ group: 'heights', id: 'near', label: `Near my height (±${NEAR})`, enabled: false, order: 20 }),
      colour: app.layers.register({ group: 'heights', id: 'colour', label: 'Colour by height', enabled: false, order: 30 }),
    };
    rows.bands.setElsewhere(EVERY);
    refreshNear();
    rows.colour.setNote('A ring round each marker in its band’s colour');
    rows.bands.onToggle(chosen);
    bandsChoice.onChange(chosen);
    rows.near.onToggle(() => tell(listeners));
    rows.colour.onToggle(chosen);
    filtering = rows.bands.enabled;
    recount();
  }

  // The live layer says where the viewer is on every frame it has them in,
  // and that it has not on those it has not.
  function viewer(dimension, y) {
    if (rows === null) return;
    const before = nearY();
    viewerAt = fin(y) ? { dimension, y: Math.floor(y) } : null;
    refreshNear();
    // What is near moves with them, and is only worth redrawing for while
    // it is asked for.
    if (rows.near.enabled && before !== nearY()) tell(viewerListeners);
  }

  function refreshNear() {
    const here = nearY() !== null;
    rows.near.setAvailable(here);
    rows.near.setNote(here ? '' : NEAR_WAITS);
  }

  app.heights = {
    MARGIN,
    SHALLOW,
    NEAR,
    UPPER_FROM,
    LAVA_TOP,
    bands: (dimension) => (BANDS[dimension] || []).map((band) => ({ ...band })),
    // The id of the band a marker at block y over a surface of s is in,
    // or null where that cannot be said.
    bandOf(dimension, y, s) {
      const at = pointBand(dimension, fin(y) ? Math.floor(y) : y, s);
      return at < 0 ? null : BANDS[dimension][at].id;
    },
    // The ids of every band a box from low to high reaches.
    bandsOf: (dimension, low, high, s) => boxBands(dimension, low, high, s).map((at) => BANDS[dimension][at].id),
    shows,
    showsBox,
    tint,
    colouring: colourOn,
    viewer,
    onChange(fn) { if (typeof fn === 'function') listeners.push(fn); },
    onViewer(fn) { if (typeof fn === 'function') viewerListeners.push(fn); },
    // A layer that draws markers offers how to count them: fn(add, addBox,
    // dimension), where add(y, surface, n) counts a marker, or n alike, and addBox(minY,
    // maxY, surface) a structure, which counts in every band it reaches.
    // A marker with no y is passed as NaN and counts as unknown.
    count(name, fn) {
      sources.set(name, fn);
      changed();
    },
    changed,
  };

  panel();
  document.addEventListener('mcmap:view', () => {
    shownFor = null;
    if (rows !== null) refreshNear();
    changed();
    tell(listeners);
  });
})();
