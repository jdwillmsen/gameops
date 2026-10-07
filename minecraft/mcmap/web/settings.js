'use strict';

// Everything the page keeps in the browser, behind one door. The other
// scripts ask this for the part that is theirs and hand it back changed;
// none of them touches the browser's storage. What is kept is one record
// under one key, with a version, and every part of it is checked against a
// closed description on the way in: a value of the wrong type or out of
// range is put back to its default, and a key nobody described is dropped.
// Storage is the viewer's to edit, and a view may have come from a link or
// a file someone else made.
//
// This runs in the head, before the page is drawn, so that the theme is on
// the page from its first frame.
(() => {
  const KEY = 'mcmap.settings';
  const VERSION = 1;
  // The most the record may come to, in characters. Fifty views with every
  // layer named and long filters come to a fraction of this; anything
  // near it is not the page's own doing.
  const MAX_CHARS = 200_000;
  const MAX_VIEWS = 50;
  const NAME_LENGTH = 40;
  const WORLD_EDGE = 30_000_000;

  // Where each script kept its own part before there was one record. They
  // are read once, to start the record from, and are written still, in the
  // form they always had: the page and its scripts are cached apart for a
  // few minutes, and a script from before reads only these.
  const OLD = {
    layers: 'mcmap.layers',
    panel: 'mcmap.panel',
    live: 'mcmap.liveControl',
    trails: 'mcmap.trails',
    shortcuts: 'mcmap.shortcuts',
  };
  // And where the filters were kept before there was a panel, as
  // { <id>: boolean }. The structures also had one switch for the lot, and
  // a viewer who had that off gets the layers it hid switched off, and the
  // one added to them since. The live layer's one switch became its pause.
  const OLDER = {
    live: { key: 'mcmap.live', master: [] },
    markers: { key: 'mcmap.markers', master: [] },
    structures: { key: 'mcmap.structures', master: ['recorded', 'predicted', 'candidate'] },
  };

  // --- what a value may be --------------------------------------------------
  //
  // Each of these takes a value and gives back the value to keep, or BAD.
  // Read strictly, as a link or a file is, anything wrong anywhere is BAD
  // for the whole; read leniently, as the browser's own storage is, the
  // wrong part alone is dropped and the rest kept.

  const BAD = Symbol('bad');
  const plain = (v) => v !== null && typeof v === 'object' && !Array.isArray(v);
  const bool = (v) => (typeof v === 'boolean' ? v : BAD);
  const int = (min, max) => (v, strict) => {
    if (!Number.isFinite(v)) return BAD;
    if (strict) return Number.isInteger(v) && v >= min && v <= max ? v : BAD;
    return Math.min(max, Math.max(min, Math.round(v)));
  };
  const oneOf = (...known) => (v) => (known.includes(v) ? v : BAD);
  const text = (max, pattern) => (v) => (typeof v === 'string' && v.length > 0 && v.length <= max && pattern.test(v) ? v : BAD);
  const orNull = (kind) => (v, strict) => (v === null ? null : kind(v, strict));
  const list = (kind, max) => (v, strict) => {
    if (!Array.isArray(v) || (strict && v.length > max)) return BAD;
    const out = [];
    for (const item of v.slice(0, max)) {
      const kept = kind(item, strict);
      if (kept !== BAD) out.push(kept);
      else if (strict) return BAD;
    }
    return out;
  };
  // An object whose keys are not known ahead, only their form. No form
  // here matches a name every object already has.
  const record = (key, kind, max) => (v, strict) => {
    if (!plain(v)) return BAD;
    const out = {};
    let n = 0;
    for (const name of Object.keys(v)) {
      const kept = n < max && key.test(name) ? kind(v[name], strict, name) : BAD;
      if (kept !== BAD) {
        out[name] = kept;
        n += 1;
      } else if (strict) return BAD;
    }
    return out;
  };
  const shape = (fields, required = []) => (v, strict) => {
    if (!plain(v)) return BAD;
    const out = {};
    for (const name of Object.keys(v)) {
      const kept = Object.hasOwn(fields, name) ? fields[name](v[name], strict) : BAD;
      if (kept !== BAD) out[name] = kept;
      else if (strict) return BAD;
    }
    return required.every((name) => Object.hasOwn(out, name)) ? out : BAD;
  };

  // A name the viewer typed, or one that came in a link. It is only ever
  // shown as text; what is refused here is what would make that text lie
  // about itself: control characters, and the marks that turn the
  // direction of the writing round.
  const UNSAFE = '\\p{Cc}\\p{Cs}\\p{Co}\\p{Zl}\\p{Zp}\\u061c\\u200e\\u200f\\u202a-\\u202e\\u2066-\\u2069\\ufeff';
  const HAS_UNSAFE = new RegExp(`[${UNSAFE}]`, 'u');
  const ALL_UNSAFE = new RegExp(`[${UNSAFE}]`, 'gu');
  function name(v, strict) {
    if (typeof v !== 'string' || v.length > NAME_LENGTH * 8) return BAD;
    let said = v.normalize('NFC');
    if (strict) {
      if (HAS_UNSAFE.test(said) || said !== said.trim()) return BAD;
    } else {
      said = said.replace(ALL_UNSAFE, ' ').replace(/\s+/gu, ' ').trim();
    }
    const letters = [...said];
    if (letters.length === 0 || (strict && letters.length > NAME_LENGTH)) return BAD;
    return letters.slice(0, NAME_LENGTH).join('').trim() || BAD;
  }

  const MODE = oneOf('always', 'hover', 'never');
  const LOOK = {
    theme: oneOf('system', 'dark', 'light', 'contrast'),
    size: oneOf('small', 'normal', 'large'),
    text: oneOf('small', 'normal', 'large'),
    labelMobs: MODE,
    labelPlayers: MODE,
    labelWaypoints: MODE,
    picturesLive: bool,
    picturesMarkers: bool,
    // Percent. The biomes' is how much of the terrain the tint covers; the
    // other two are of how they have always been drawn.
    opacityBiomes: int(10, 100),
    opacityTrails: int(10, 100),
    opacitySlime: int(10, 100),
    density: oneOf('comfortable', 'compact'),
    motion: oneOf('system', 'reduce', 'full'),
    coords: oneOf('blocks', 'chunks'),
  };
  const LOOK_DEFAULTS = Object.freeze({
    theme: 'dark',
    size: 'normal',
    text: 'normal',
    labelMobs: 'always',
    labelPlayers: 'always',
    labelWaypoints: 'always',
    picturesLive: true,
    picturesMarkers: true,
    opacityBiomes: 60,
    opacityTrails: 100,
    opacitySlime: 100,
    density: 'comfortable',
    motion: 'system',
    coords: 'blocks',
  });

  const MAX_HIDDEN = 200;
  // A mob's type as the server reports it, and a gamertag as the game
  // compares it. Neither is ever used as anything but a key to look up.
  const TYPE = text(64, /^[a-z0-9_.:-]+$/);
  const TAG = (v) => (typeof v === 'string' && v.length > 0 && v.length <= 64 && !HAS_UNSAFE.test(v) ? v : BAD);
  const filter = (kind) => (v, strict) => {
    const kept = shape({ only: orNull(kind), hidden: list(kind, MAX_HIDDEN) })(v, strict);
    return kept === BAD ? BAD : { only: kept.only ?? null, hidden: kept.hidden || [] };
  };

  const LAYER = /^[a-z0-9_-]{1,32}\/[a-z0-9_-]{1,48}$/;
  const ID = text(24, /^[a-z0-9]+$/);
  const BUILT_IN = ['everything', 'exploring', 'base'];
  const VIEW_FIELDS = {
    id: ID,
    name,
    layers: record(LAYER, bool, 64),
    mobs: filter(TYPE),
    players: filter(TAG),
    biome: orNull(text(64, /^[a-z0-9_.:-]+$/)),
    trails: int(60, 7 * 86_400),
    interval: int(1, 86_400),
    grid: bool,
    place: shape({
      d: text(16, /^[a-z_]+$/),
      x: int(-WORLD_EDGE, WORLD_EDGE),
      z: int(-WORLD_EDGE, WORLD_EDGE),
      zoom: int(-12, 8),
      pin: shape({ u: oneOf('chunk', 'region'), x: int(-2_000_000, 2_000_000), z: int(-2_000_000, 2_000_000) }, ['u', 'x', 'z']),
    }, ['d', 'x', 'z', 'zoom']),
    look: shape(LOOK),
  };
  const VIEW = shape(VIEW_FIELDS, ['name', 'layers']);

  // What a layer keeps beside its switches is its own to describe, so here
  // it is only kept small: a few levels of plain values and short strings.
  function small(v, strict, depth = 0) {
    if (v === null || typeof v === 'boolean') return v;
    if (typeof v === 'number') return Number.isFinite(v) ? v : BAD;
    if (typeof v === 'string') return v.length <= 64 ? v : BAD;
    if (depth >= 3) return BAD;
    if (Array.isArray(v)) return list((item, s) => small(item, s, depth + 1), MAX_HIDDEN)(v, strict);
    return record(/^[a-zA-Z0-9_-]{1,32}$/, (item, s) => small(item, s, depth + 1), 16)(v, strict);
  }
  const SECTIONS = {
    // "<group>/<id>" is a row's switch, and "<group>#<name>" whatever else
    // its layer keeps.
    layers: record(/^[a-z0-9_-]{1,32}[/#][a-z0-9_-]{1,48}$/, (v, strict, key) => (key.includes('/') ? bool(v) : small(v, strict)), 256),
    panel: shape({ open: bool, folded: list(text(32, /^[a-z0-9_-]+$/), 32) }),
    live: shape({ paused: bool, interval: int(1, 86_400) }),
    trails: shape({ seconds: int(60, 7 * 86_400) }),
    shortcuts: shape({ on: bool }),
    grid: shape({ on: bool }),
    biome: shape({ only: orNull(text(64, /^[a-z0-9_.:-]+$/)) }),
    look: shape(LOOK),
    views: shape({
      list: list(VIEW, MAX_VIEWS),
      order: list(ID, MAX_VIEWS + BUILT_IN.length),
      start: orNull(ID),
      hidden: list(oneOf(...BUILT_IN), BUILT_IN.length),
      active: orNull(ID),
    }),
  };
  const DEFAULTS = {
    layers: () => ({}),
    panel: () => ({ folded: [] }),
    live: () => ({ paused: false, interval: 1 }),
    trails: () => ({ seconds: 3600 }),
    shortcuts: () => ({ on: true }),
    grid: () => ({ on: false }),
    biome: () => ({ only: null }),
    look: () => ({ ...LOOK_DEFAULTS }),
    views: () => ({ list: [], order: [...BUILT_IN], start: null, hidden: [], active: null }),
  };

  // The views that come with the page. They are not kept in the record, so
  // a release can change what they hold; the record keeps only where each
  // stands in the list and whether it is hidden.
  const rows = (group, on, off) => Object.fromEntries([...on.map((id) => [`${group}/${id}`, true]), ...off.map((id) => [`${group}/${id}`, false])]);
  const MOBS = ['hostile', 'passive', 'villager', 'other'];
  const MARKERS = ['waypoints', 'beds', 'containers', 'mobs'];
  const SORTS = ['recorded', 'predicted', 'candidate'];
  const KINDS = ['fortress', 'monument', 'outpost', 'witch_hut', 'village'];
  const unfiltered = () => ({ mobs: { only: null, hidden: [] }, players: { only: null, hidden: [] }, biome: null });
  const BUILT = {
    everything: {
      id: 'everything',
      name: 'Everything',
      says: 'Players, mobs, markers, structures and trails',
      layers: { ...rows('live', ['players', ...MOBS], []), ...rows('markers', MARKERS, []), ...rows('structures', [...SORTS, ...KINDS, 'spawn'], []), ...rows('biomes', [], ['overlay']), ...rows('overlays', ['trails'], ['slime']) },
      ...unfiltered(),
    },
    exploring: {
      id: 'exploring',
      name: 'Exploring',
      says: 'Terrain, structures, biomes and the world spawn; no mobs',
      layers: { ...rows('live', ['players'], MOBS), ...rows('markers', ['waypoints'], ['beds', 'containers', 'mobs']), ...rows('structures', [...SORTS, ...KINDS, 'spawn'], []), ...rows('biomes', ['overlay'], []), ...rows('overlays', [], ['trails', 'slime']) },
      ...unfiltered(),
    },
    base: {
      id: 'base',
      name: 'Base',
      says: 'Players, named mobs, beds, containers and waypoints',
      layers: { ...rows('live', ['players'], MOBS), ...rows('markers', MARKERS, []), ...rows('structures', [], [...SORTS, 'spawn']), ...rows('biomes', [], ['overlay']), ...rows('overlays', [], ['trails', 'slime']) },
      ...unfiltered(),
    },
  };

  // Brings the list of views in line with itself: one of each id, every
  // view somewhere in the order and nothing else in it, and a default and
  // a current view that exist.
  function tidy(views) {
    const seen = new Set(BUILT_IN);
    views.list = views.list.filter((view) => {
      if (!view.id || seen.has(view.id)) return false;
      seen.add(view.id);
      return true;
    });
    const order = [];
    for (const id of [...views.order, ...BUILT_IN, ...views.list.map((view) => view.id)]) {
      if (seen.has(id) && !order.includes(id)) order.push(id);
    }
    views.order = order;
    views.hidden = [...new Set(views.hidden)];
    if (!seen.has(views.start) || views.hidden.includes(views.start)) views.start = null;
    if (!seen.has(views.active)) views.active = null;
    return views;
  }

  // One part, checked, with whatever it lacked put back to its default.
  function whole(section, value) {
    const kept = SECTIONS[section](value, false);
    const out = { ...DEFAULTS[section](), ...(kept === BAD ? {} : kept) };
    return section === 'views' ? tidy(out) : out;
  }

  function wholeRecord(raw) {
    const out = { v: VERSION };
    for (const section of Object.keys(SECTIONS)) out[section] = whole(section, plain(raw) ? raw[section] : undefined);
    return out;
  }

  // --- the browser's storage ------------------------------------------------
  //
  // A browser may refuse storage altogether, or have no room left. Either
  // way the page works as it would have, on what it holds in memory, and
  // says so where the viewer saves things: kept is 'yes', 'no' where there
  // is nowhere to keep anything, 'full' where the last write was refused,
  // and 'newer' where the record was written by a later version of the
  // page, which this one reads what it can of and leaves as it is.
  let kept = 'yes';
  let store = null;
  try {
    store = window.localStorage;
    store.getItem(KEY);
  } catch {
    store = null;
    kept = 'no';
  }

  function stored(key) {
    if (!store) return null;
    try {
      const value = JSON.parse(store.getItem(key) || 'null');
      return plain(value) ? value : null;
    } catch {
      return null;
    }
  }

  // The record as the scripts before this one would have left it.
  function carriedOver() {
    const raw = { layers: stored(OLD.layers) || {}, panel: stored(OLD.panel) };
    for (const [group, old] of Object.entries(OLDER)) {
      const was = stored(old.key);
      if (!was) continue;
      for (const id of old.master) if (was.on === false && typeof raw.layers[`${group}/${id}`] !== 'boolean') raw.layers[`${group}/${id}`] = false;
      for (const [id, on] of Object.entries(was)) {
        if (id !== 'on' && typeof on === 'boolean' && typeof raw.layers[`${group}/${id}`] !== 'boolean') raw.layers[`${group}/${id}`] = on;
      }
    }
    const live = stored(OLD.live);
    const older = stored(OLDER.live.key);
    // Whoever had the live layer switched off still gets a page that
    // opens no stream.
    raw.live = live || { paused: Boolean(older) && older.on === false };
    const trails = stored(OLD.trails);
    // Kept as hours while the choice was one of three.
    if (trails) raw.trails = { seconds: Number.isFinite(trails.seconds) ? trails.seconds : trails.hours * 3600 };
    raw.shortcuts = stored(OLD.shortcuts);
    return raw;
  }

  let state;
  const written = {};
  {
    const raw = stored(KEY);
    if (raw && Number.isInteger(raw.v) && raw.v > VERSION) kept = 'newer';
    state = wholeRecord(raw || carriedOver());
    for (const section of Object.keys(OLD)) written[section] = JSON.stringify(state[section]);
    if (!raw) write();
  }

  function write() {
    if (!store || kept === 'newer') return;
    try {
      store.setItem(KEY, JSON.stringify(state));
      kept = 'yes';
    } catch {
      kept = 'full';
      return;
    }
    for (const [section, key] of Object.entries(OLD)) {
      const now = JSON.stringify(state[section]);
      if (now === written[section]) continue;
      written[section] = now;
      try { store.setItem(key, now); } catch { /* the record itself was kept */ }
    }
  }

  // Another tab of the same page has written: what this one holds of the
  // record is brought up to date, so that its next write does not undo
  // the other's. What is on this tab's screen stays as it is.
  addEventListener('storage', (e) => {
    if (e.storageArea !== store || e.key !== KEY) return;
    const raw = stored(KEY);
    if (raw) state = wholeRecord(raw);
  });

  const tell = (sections) => document.dispatchEvent(new CustomEvent('mcmap:settings', { detail: { sections } }));

  // Puts a changed record in place of the one there is, and says whether
  // it could: one too large to keep is refused whole, and nothing changes.
  function commit(next, quiet) {
    if (JSON.stringify(next).length > MAX_CHARS) return false;
    const changed = Object.keys(SECTIONS).filter((section) => JSON.stringify(next[section]) !== JSON.stringify(state[section]));
    if (changed.length === 0) return true;
    state = next;
    write();
    if (changed.includes('look')) paint();
    if (!quiet) tell(changed);
    return true;
  }

  const copy = (v) => JSON.parse(JSON.stringify(v));

  function set(section, value) {
    if (!Object.hasOwn(SECTIONS, section)) return false;
    return commit({ ...state, [section]: whole(section, value) });
  }

  // Makes the record say what a view says, all at once. What the view does
  // not speak of is left as it is. place is not part of the record: the
  // map is taken there by whoever asked.
  function adopt(view, quiet) {
    const next = copy(state);
    Object.assign(next.layers, view.layers);
    for (const domain of ['mobs', 'players']) {
      const f = view[domain];
      if (!f) continue;
      if (f.only === null && f.hidden.length === 0) delete next.layers[`live#${domain}`];
      else next.layers[`live#${domain}`] = copy(f);
    }
    if (view.biome !== undefined) next.biome.only = view.biome;
    if (view.trails !== undefined) next.trails.seconds = view.trails;
    if (view.interval !== undefined) next.live.interval = view.interval;
    if (view.grid !== undefined) next.grid.on = view.grid;
    if (view.look) next.look = { ...LOOK_DEFAULTS, ...view.look };
    next.views.active = view.id || null;
    for (const section of Object.keys(SECTIONS)) next[section] = whole(section, next[section]);
    return commit(next, quiet);
  }

  const find = (id) => (Object.hasOwn(BUILT, id) ? BUILT[id] : state.views.list.find((view) => view.id === id) || null);

  // --- how the page looks, as far as the stylesheet is told -----------------
  //
  // The choices are put on the page's root as attributes, and everything
  // else about a theme is the stylesheet's. A choice of "system" is
  // resolved here, and again when the system changes its mind.

  const prefers = (query) => {
    try { return matchMedia(query); } catch { return { matches: false, addEventListener() {} }; }
  };
  const media = {
    light: prefers('(prefers-color-scheme: light)'),
    contrast: prefers('(prefers-contrast: more)'),
    still: prefers('(prefers-reduced-motion: reduce)'),
  };

  function resolved() {
    const look = { ...state.look };
    if (look.theme === 'system') look.theme = media.contrast.matches ? 'contrast' : media.light.matches ? 'light' : 'dark';
    if (look.motion === 'system') look.motion = media.still.matches ? 'reduce' : 'full';
    return look;
  }

  let colours = new Map();
  function paint() {
    const look = resolved();
    const root = document.documentElement;
    root.dataset.theme = look.theme;
    root.dataset.density = look.density;
    root.dataset.motion = look.motion;
    root.dataset.text = look.text;
    colours = new Map();
  }

  // A colour of the theme in force, by the name the stylesheet gives it:
  // what is drawn on a canvas is drawn in the same colours as the page.
  function colour(named) {
    if (!colours.has(named)) colours.set(named, getComputedStyle(document.documentElement).getPropertyValue(`--${named}`).trim());
    return colours.get(named);
  }

  for (const query of Object.values(media)) {
    query.addEventListener('change', () => {
      const was = JSON.stringify(resolved());
      paint();
      if (state.look.theme === 'system' || state.look.motion === 'system' || was !== JSON.stringify(resolved())) tell(['look']);
    });
  }

  // The view marked as the default is what the page opens on. It is put
  // into the record before any other script has read its part, so nothing
  // is drawn one way and then another.
  let place = null;
  {
    const first = state.views.start ? find(state.views.start) : null;
    if (first) {
      adopt(first, true);
      place = first.place || null;
    }
  }
  paint();

  window.mcmap = window.mcmap || {};
  window.mcmap.settings = {
    VERSION,
    MAX_VIEWS,
    NAME_LENGTH,
    LOOK_DEFAULTS,
    BUILT_IN,
    // A part of the record, as a copy that is the caller's to change.
    get: (section) => (Object.hasOwn(SECTIONS, section) ? copy(state[section]) : null),
    set,
    adopt,
    // A view by its id, built in or saved, or null.
    view: (id) => {
      const found = find(id);
      return found ? copy(found) : null;
    },
    // Where the default view opens the map, if it says, for the map's own
    // script to start from.
    place: () => place,
    kept: () => kept,
    look: resolved,
    colour,
    // For what comes from outside: a view, a name, or a whole record from
    // a file, each read strictly and given back clean, or null.
    check: {
      view: (v) => {
        const read = VIEW(v, true);
        return read === BAD ? null : read;
      },
      name: (v) => {
        const read = name(v, false);
        return read === BAD ? null : read;
      },
      record: (v) => {
        if (!plain(v) || v.v !== VERSION) return null;
        const read = shape({ v: int(VERSION, VERSION), ...SECTIONS })(v, true);
        return read === BAD ? null : wholeRecord(read);
      },
    },
    // The whole record, for a file, and a whole record put in its place.
    all: () => copy(state),
    replace: (next) => commit(wholeRecord(next)),
  };
})();
