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
  // An object whose keys are not known ahead, only their form. A name
  // every object already has, such as constructor or __proto__, is never
  // one of them whatever the form allows, and what is built has no such
  // names of its own to be mistaken for a key.
  const inherited = (name) => name in Object.prototype;
  const record = (key, kind, max) => (v, strict) => {
    if (!plain(v)) return BAD;
    const out = Object.create(null);
    let n = 0;
    for (const name of Object.keys(v)) {
      const kept = n < max && key.test(name) && !inherited(name) ? kind(v[name], strict, name) : BAD;
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
  // about itself: control characters, characters that take no room and
  // the marks that turn the direction of the writing round, which are
  // all of the format class, and a pile of accents on one letter, which
  // is drawn over the lines above and below it.
  const UNSAFE = '\\p{Cc}\\p{Cf}\\p{Cs}\\p{Co}\\p{Zl}\\p{Zp}';
  const HAS_UNSAFE = new RegExp(`[${UNSAFE}]`, 'u');
  const ALL_UNSAFE = new RegExp(`[${UNSAFE}]`, 'gu');
  const MARKS = 3;
  const PILED = new RegExp(`\\p{M}{${MARKS + 1},}`, 'u');
  const ALL_PILED = new RegExp(`(\\p{M}{${MARKS}})\\p{M}+`, 'gu');
  function name(v, strict) {
    if (typeof v !== 'string' || v.length > NAME_LENGTH * 8) return BAD;
    let said = v.normalize('NFC');
    if (strict) {
      if (HAS_UNSAFE.test(said) || PILED.test(said) || said !== said.trim()) return BAD;
    } else {
      said = said.replace(ALL_UNSAFE, '').replace(ALL_PILED, '$1').replace(/\s+/gu, ' ').trim();
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
    density: oneOf('compact', 'comfortable', 'spacious'),
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

  // Which of what a layer is made of are shown, as the panel keeps it:
  // what differs from everything showing. only is the one shown alone,
  // hidden those switched off, and shown those that are off until asked
  // for and have been; or, with mode "just", just is all that is shown.
  // An item is known by an id of the game's or the server's, or a
  // gamertag; none is ever used as anything but a key.
  const chosen = (v, strict) => {
    const kept = shape({ only: orNull(TAG), hidden: list(TAG, MAX_HIDDEN), shown: list(TAG, MAX_HIDDEN), mode: oneOf('just'), just: list(TAG, MAX_HIDDEN) })(v, strict);
    if (kept === BAD || (strict && (kept.mode === 'just') !== Array.isArray(kept.just))) return BAD;
    if (kept.mode === 'just' && kept.just) return { only: kept.only ?? null, hidden: [], mode: 'just', just: kept.just };
    return { only: kept.only ?? null, hidden: kept.hidden || [], ...(kept.shown && kept.shown.length > 0 ? { shown: kept.shown } : {}) };
  };
  const unchosen = (f) => !f || (f.only === null && f.mode !== 'just' && f.hidden.length === 0 && !(f.shown && f.shown.length > 0));
  const CHOICE = /^[a-z0-9_-]{1,32}#[a-z0-9_-]{1,48}$/;
  // The choices there are, by the key each is kept under beside the
  // switches. The two of the live layer are older than the rest and have
  // a place of their own in a view.
  const STRUCTURE_SORTS = ['recorded', 'predicted', 'candidate'];
  const CHOICES = [...STRUCTURE_SORTS.map((sort) => `structures#${sort}`), 'markers#containers', 'markers#beds', 'markers#mobs', 'markers#waypoints', 'biomes#items', 'overlays#trails'];
  // The kinds of structure that each had a switch over all three layers
  // before each layer listed its own, and the ones among them that are
  // off until asked for.
  const KIND_SWITCHES = ['fortress', 'monument', 'outpost', 'witch_hut', 'village', 'stronghold', 'trial_chamber'];
  const ASKED_FOR = ['stronghold'];

  const LAYER = /^[a-z0-9_-]{1,32}\/[a-z0-9_-]{1,48}$/;
  const ID = text(24, /^[a-z0-9]+$/);
  const BUILT_IN = ['everything', 'exploring', 'base'];
  const VIEW_FIELDS = {
    id: ID,
    name,
    layers: record(LAYER, bool, 64),
    mobs: filter(TYPE),
    players: filter(TAG),
    items: record(CHOICE, chosen, 32),
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
  // What each old key held when this script last looked, as a short
  // number made from its text. A script from before writes only the old
  // key, and a key that no longer matches its number is one such a script
  // has changed since.
  const STAMP = int(0, 0xffffffff);
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
    old: shape({ layers: STAMP, panel: STAMP, live: STAMP, trails: STAMP, shortcuts: STAMP }),
  };
  const PORTABLE = Object.fromEntries(Object.entries(SECTIONS).filter(([section]) => section !== 'old'));
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
    old: () => ({}),
  };

  // The views that come with the page. They are not kept in the record, so
  // a release can change what they hold; the record keeps only where each
  // stands in the list and whether it is hidden.
  const rows = (group, on, off) => Object.fromEntries([...on.map((id) => [`${group}/${id}`, true]), ...off.map((id) => [`${group}/${id}`, false])]);
  const MOBS = ['hostile', 'passive', 'villager', 'other'];
  const MARKERS = ['waypoints', 'beds', 'containers', 'mobs'];
  const SORTS = ['recorded', 'predicted', 'candidate'];
  // Every item of every layer showing: no type of mob, no player, no kind
  // of structure, container or bed and no biome is hidden.
  const unfiltered = () => ({
    mobs: { only: null, hidden: [] },
    players: { only: null, hidden: [] },
    biome: null,
    items: Object.fromEntries(CHOICES.map((key) => [key, { only: null, hidden: [] }])),
  });
  const BUILT = {
    everything: {
      id: 'everything',
      name: 'Everything',
      says: 'Players, mobs, markers, structures and trails',
      layers: { ...rows('live', ['players', ...MOBS], []), ...rows('markers', MARKERS, []), ...rows('structures', [...SORTS, 'spawn'], []), ...rows('biomes', [], ['overlay']), ...rows('overlays', ['trails'], ['slime']) },
      ...unfiltered(),
    },
    exploring: {
      id: 'exploring',
      name: 'Exploring',
      says: 'Terrain, structures, biomes and the world spawn; no mobs',
      layers: { ...rows('live', ['players'], MOBS), ...rows('markers', ['waypoints'], ['beds', 'containers', 'mobs']), ...rows('structures', [...SORTS, 'spawn'], []), ...rows('biomes', ['overlay'], []), ...rows('overlays', [], ['trails', 'slime']) },
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

  // What the switches over a kind of structure come to now that each of
  // the three layers lists its kinds: a kind that was off is hidden in all
  // three, and one that is off until asked for and was on is shown in all
  // three. Null where they say nothing but the defaults.
  function fromKindSwitches(layers) {
    const hidden = KIND_SWITCHES.filter((kind) => !ASKED_FOR.includes(kind) && layers[`structures/${kind}`] === false);
    const shown = ASKED_FOR.filter((kind) => layers[`structures/${kind}`] === true);
    return hidden.length + shown.length === 0 ? null : { only: null, hidden, ...(shown.length > 0 ? { shown } : {}) };
  }

  // A record from before the panel listed what each layer is made of is
  // brought to how that is kept now, once, and marked as brought: the one
  // switch per kind of structure becomes each layer's own list, and the
  // one biome picked out becomes the biomes'. The old switches are left
  // where they are, for a script from before to go on reading.
  const BROUGHT = 'panel#items';
  function brought(record) {
    const { layers } = record;
    if (layers[BROUGHT] === 1) return record;
    const kinds = fromKindSwitches(layers);
    for (const sort of STRUCTURE_SORTS) {
      if (kinds && !Object.hasOwn(layers, `structures#${sort}`)) layers[`structures#${sort}`] = { ...kinds };
    }
    if (record.biome.only !== null && !Object.hasOwn(layers, 'biomes#items')) layers['biomes#items'] = { only: record.biome.only, hidden: [] };
    layers[BROUGHT] = 1;
    return record;
  }

  function wholeRecord(raw) {
    const out = { v: VERSION };
    for (const section of Object.keys(SECTIONS)) out[section] = whole(section, plain(raw) ? raw[section] : undefined);
    return brought(out);
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
  // The record as it was when a preview began, while one is showing.
  let held = null;
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

  // FNV-1a over the text of an old key, or 0 for a key that is not there.
  function stamp(key) {
    let text = null;
    try { text = store ? store.getItem(key) : null; } catch { /* as if absent */ }
    if (text === null) return 0;
    let h = 0x811c9dc5;
    for (let i = 0; i < text.length; i++) h = Math.imul(h ^ text.charCodeAt(i), 0x01000193);
    return (h >>> 0) || 1;
  }

  // One old key's part of the record, as carriedOver reads it.
  const fromOld = {
    layers: () => stored(OLD.layers),
    panel: () => stored(OLD.panel),
    live: () => stored(OLD.live),
    trails: () => {
      const trails = stored(OLD.trails);
      return trails && { seconds: Number.isFinite(trails.seconds) ? trails.seconds : trails.hours * 3600 };
    },
    shortcuts: () => stored(OLD.shortcuts),
  };

  let state;
  const written = {};
  {
    let raw = stored(KEY);
    let text = null;
    try { text = store ? store.getItem(KEY) : null; } catch { /* as if absent */ }
    if (text !== null && text.length > MAX_CHARS) {
      // Larger than the page ever writes: not the page's own doing. It is
      // left exactly as it is, and the page works from its defaults.
      kept = 'large';
      raw = {};
    } else if (raw && Number.isInteger(raw.v) && raw.v > VERSION) {
      kept = 'newer';
    }
    const fresh = !raw;
    const unbrought = !fresh && !(plain(raw.layers) && raw.layers[BROUGHT] === 1);
    state = wholeRecord(raw || carriedOver());
    let changed = fresh || unbrought;
    if (kept === 'yes') {
      // The page and its scripts are cached apart, so for a few minutes
      // after a release a script from before may be the one writing. It
      // writes the old key alone: where an old key is not as this script
      // last saw it, what it holds now is the newer choice, and is taken.
      for (const section of Object.keys(OLD)) {
        const now = stamp(OLD[section]);
        if (!fresh && now !== 0 && now !== (state.old[section] || 0)) {
          const theirs = fromOld[section]();
          if (theirs) state[section] = whole(section, theirs);
        }
        changed = changed || state.old[section] !== now;
        state.old[section] = now;
      }
    }
    for (const section of Object.keys(OLD)) written[section] = JSON.stringify(state[section]);
    if (changed) write();
  }

  // Writes the record, and each old key whose part has changed in the form
  // that key always had. The old keys go first and are then read back, so
  // that the record says what each one really holds: if one could not be
  // written, its old value is not later mistaken for a newer choice.
  function write() {
    if (!store || kept === 'newer' || kept === 'large') return;
    const out = lasting();
    for (const [section, key] of Object.entries(OLD)) {
      const now = JSON.stringify(out[section]);
      if (now === written[section]) continue;
      try {
        store.setItem(key, now);
        written[section] = now;
      } catch { /* tried again with the next write */ }
      state.old[section] = stamp(key);
    }
    try {
      store.setItem(KEY, JSON.stringify({ ...out, old: state.old }));
      kept = 'yes';
    } catch {
      kept = 'full';
    }
  }

  // --- a preview ---------------------------------------------------------------
  //
  // A view that came in a link is shown before it is kept. While the
  // record is held, everything the page does is done to what is in memory
  // and what is written stays the record as it was when the hold began:
  // a reload, or letting go, is the viewer's own setup again. The list of
  // views alone is written through a hold, since saving, renaming or
  // deleting one is not part of what is being previewed.
  // The record as it is kept, which while one is held is not the record
  // the page is showing.
  function lasting() {
    return held ? { ...held, views: { ...state.views, active: held.views.active } } : state;
  }

  function hold() {
    if (!held) held = copy(state);
  }

  // Lets go of a hold and puts the page's record back as it was kept.
  function release() {
    if (!held) return false;
    const back = lasting();
    held = null;
    const changed = Object.keys(SECTIONS).filter((section) => JSON.stringify(back[section]) !== JSON.stringify(state[section]));
    state = { ...back, old: state.old };
    if (changed.includes('look')) paint();
    if (changed.length > 0) tell(changed);
    return true;
  }

  // Another tab of the same page has written: what this one holds of the
  // record is brought up to date, so that its next write does not undo
  // the other's. What is on this tab's screen stays as it is.
  addEventListener('storage', (e) => {
    if (e.storageArea !== store || e.key !== KEY || kept === 'large') return;
    const raw = stored(KEY);
    if (!raw) return;
    // The other tab's write went in, so there is room again; unless it is
    // a later version's, which this one must not write over.
    kept = Number.isInteger(raw.v) && raw.v > VERSION ? 'newer' : 'yes';
    for (const section of Object.keys(OLD)) written[section] = JSON.stringify(wholeRecord(raw)[section]);
    // While a preview is showing, the other tab's write is what there is
    // to go back to, and what is on this tab stays the preview.
    if (held) {
      held = wholeRecord(raw);
      return;
    }
    const looked = JSON.stringify(state.look);
    state = wholeRecord(raw);
    // The look is the one part taken up at once: left for later, this
    // tab's next unrelated change would bring the other's theme with it.
    if (looked === JSON.stringify(state.look)) return;
    paint();
    tell(['look']);
  });

  const tell = (sections) => document.dispatchEvent(new CustomEvent('mcmap:settings', { detail: { sections } }));

  // Puts a changed record in place of the one there is, and says whether
  // it could: one too large to keep is refused whole, and nothing changes.
  // later is for a value still being chosen, as a slider's is while it is
  // dragged: it is on the page at once and written when settled is called,
  // or with the next change that is written.
  let unwritten = false;
  function commit(next, quiet, later) {
    if (JSON.stringify(next).length > MAX_CHARS) return false;
    const changed = Object.keys(SECTIONS).filter((section) => JSON.stringify(next[section]) !== JSON.stringify(state[section]));
    if (changed.length === 0) return true;
    state = next;
    unwritten = later === true;
    if (!unwritten) write();
    if (changed.includes('look')) paint();
    if (!quiet) tell(changed);
    return true;
  }

  function settled() {
    if (!unwritten) return;
    unwritten = false;
    write();
  }
  // A page that is closed mid-drag still keeps where the slider was.
  addEventListener('pagehide', settled);

  const copy = (v) => JSON.parse(JSON.stringify(v));

  function set(section, value, later) {
    if (!Object.hasOwn(SECTIONS, section)) return false;
    return commit({ ...state, [section]: whole(section, value) }, false, later);
  }

  // Makes the record say what a view says, all at once. What the view does
  // not speak of is left as it is. place is not part of the record: the
  // map is taken there by whoever asked.
  function adopted(view) {
    const next = copy(state);
    Object.assign(next.layers, view.layers);
    const choose = (key, f) => {
      if (unchosen(f)) delete next.layers[key];
      else next.layers[key] = copy(f);
    };
    for (const domain of ['mobs', 'players']) if (view[domain]) choose(`live#${domain}`, view[domain]);
    if (view.biome !== undefined) next.biome.only = view.biome;
    if (view.items) {
      for (const [key, f] of Object.entries(view.items)) choose(key, f);
      const biomes = view.items['biomes#items'];
      if (biomes) next.biome.only = biomes.only;
    } else {
      // A view from before each layer listed what it is made of showed
      // all of every layer but what its own switches and its one biome
      // said, and that is what it puts back.
      for (const key of CHOICES) delete next.layers[key];
      const kinds = fromKindSwitches(view.layers);
      for (const sort of STRUCTURE_SORTS) choose(`structures#${sort}`, kinds);
      if (view.biome !== undefined) choose('biomes#items', view.biome === null ? null : { only: view.biome, hidden: [] });
    }
    if (view.trails !== undefined) next.trails.seconds = view.trails;
    if (view.interval !== undefined) next.live.interval = view.interval;
    if (view.grid !== undefined) next.grid.on = view.grid;
    // A view may speak of only some of how the page looks.
    if (view.look) next.look = { ...next.look, ...view.look };
    next.views.active = view.id || null;
    for (const section of Object.keys(SECTIONS)) next[section] = whole(section, next[section]);
    return next;
  }

  const adopt = (view, quiet) => commit(adopted(view), quiet);
  // Whether a view could be adopted, asked before anything else is done
  // for it: one that would make the record too large is refused whole.
  const fits = (view) => JSON.stringify(adopted(view)).length <= MAX_CHARS;

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
  // The look as it was last put on the page, to tell a change by.
  let painted = '';
  function paint() {
    const look = resolved();
    painted = JSON.stringify(look);
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
      const was = painted;
      paint();
      if (painted !== was) tell(['look']);
    });
  }

  // The view marked as the default is what the page opens on. It is put
  // into the record before any other script has read its part, so nothing
  // is drawn one way and then another. What it puts back is what is shown;
  // how the page looks is always as the viewer last set it, and a view's
  // own appearance comes with it only when the viewer switches to it.
  let place = null;
  {
    const first = state.views.start ? find(state.views.start) : null;
    if (first) {
      const { look, ...shown } = first;
      adopt(shown, true);
      place = first.place || null;
    }
  }
  paint();

  // Under a name of its own, and not on the page's object: the script that
  // makes that object may be one from before this, which would put its own
  // in the place of whatever was there.
  window.mcmapSettings = {
    VERSION,
    MAX_VIEWS,
    NAME_LENGTH,
    LOOK_DEFAULTS,
    BUILT_IN,
    // The switches a kind of structure had before each layer listed its
    // kinds, which a view saved then may still name.
    RETIRED: KIND_SWITCHES.map((kind) => `structures/${kind}`),
    // A part of the record, as a copy that is the caller's to change.
    get: (section) => (Object.hasOwn(SECTIONS, section) ? copy(state[section]) : null),
    set,
    settled,
    adopt,
    fits,
    // A hold on what is kept, for a preview, and letting go of it.
    hold,
    release,
    held: () => held !== null,
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
        const read = shape({ v: int(VERSION, VERSION), ...PORTABLE })(v, true);
        return read === BAD ? null : wholeRecord(read);
      },
    },
    // The whole record, for a file, and a whole record put in its place.
    // What it says of the old keys is this browser's own business, and
    // stays out of the one and is kept through the other.
    all: () => {
      const { old, ...rest } = copy(lasting());
      return rest;
    },
    replace: (next) => commit({ ...wholeRecord(next), old: state.old }),
  };
})();
