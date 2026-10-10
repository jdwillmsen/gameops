'use strict';

// The one panel that every layer's switch lives in. A layer's own script
// registers a row here and is told when the viewer turns it on or off;
// nothing in this file knows what a layer draws, so a new one is added by
// its script and not by editing this file or the page.
(() => {
  const app = window.mcmap;
  if (!app || !app.layers) return;

  const el = {
    panel: document.getElementById('layers'),
    toggle: document.getElementById('layers-toggle'),
    body: document.getElementById('layers-body'),
  };
  if (!el.panel || !el.toggle || !el.body) return;

  // The viewer's choices are kept by the page's one record of them, in
  // its "layers" part, and which groups are folded in its "panel" part.
  // The page and its scripts are cached apart for a few minutes, so just
  // after a release this can meet a page from before the script that
  // keeps the record. The panel then reads and writes the keys it always
  // had, exactly as it did, so that nobody's choices reset for those
  // minutes; the record takes up whatever is written there when it is next
  // loaded.
  const settings = window.mcmapSettings || null;
  const CHOICES_KEY = 'mcmap.layers';
  const PANEL_KEY = 'mcmap.panel';
  // Where each layer's script kept its filters before there was a panel,
  // as { <id>: boolean }, with one switch for all the structures. The
  // record carries these over itself; without it they are read here.
  const LEGACY = {
    live: { key: 'mcmap.live', master: [] },
    markers: { key: 'mcmap.markers', master: [] },
    structures: { key: 'mcmap.structures', master: ['recorded', 'predicted', 'candidate'] },
  };

  function readOld(key) {
    try {
      const value = JSON.parse(localStorage.getItem(key) || '{}');
      return value && typeof value === 'object' && !Array.isArray(value) ? value : {};
    } catch {
      return {}; // a browser that refuses storage still gets the defaults
    }
  }

  function writeOld(key, value) {
    try { localStorage.setItem(key, JSON.stringify(value)); } catch { /* not kept, still applied */ }
  }

  const unkept = settings ? null : readOld(CHOICES_KEY);
  const legacy = new Map();
  const kept = () => (settings ? settings.get('layers') : unkept);

  // Choices are kept flat, as "<group>/<id>", so that no id a script picks
  // can be mistaken for a property every object has.
  function remember(changes) {
    const choices = kept();
    for (const [key, value] of Object.entries(changes)) {
      if (value === null || value === undefined) delete choices[key];
      else choices[key] = value;
    }
    if (settings) settings.set('layers', choices);
    else writeOld(CHOICES_KEY, choices);
  }

  const flag = (from, key) => (Object.hasOwn(from, key) && typeof from[key] === 'boolean' ? from[key] : null);

  function choice(group, id, fallback) {
    const was = flag(kept(), `${group}/${id}`);
    if (was !== null) return was;
    if (settings || !Object.hasOwn(LEGACY, group)) return fallback;
    if (!legacy.has(group)) legacy.set(group, readOld(LEGACY[group].key));
    const old = legacy.get(group);
    const before = flag(old, 'on') === false && LEGACY[group].master.includes(id) ? false : flag(old, id);
    if (before === null) return fallback;
    remember({ [`${group}/${id}`]: before });
    return before;
  }

  // What a layer keeps besides its switches, such as which kinds of a
  // thing it shows: under "<group>#<name>", beside the switches, so that
  // everything the viewer chose in the panel is in one place. The value is
  // whatever the layer's script gave, and that script's to check when it
  // reads it back: storage is the viewer's to edit.
  function recall(group, name) {
    const choices = kept();
    const key = `${group}#${name}`;
    return Object.hasOwn(choices, key) ? choices[key] : null;
  }

  const retain = (group, name, value) => remember({ [`${group}#${name}`]: value });

  const view = settings ? settings.get('panel') : readOld(PANEL_KEY);
  const folded = new Set(Array.isArray(view.folded) ? view.folded.filter((id) => typeof id === 'string') : []);
  // On a small screen the panel is a sheet over the map, which opens when
  // asked and is never found open on arriving: what was last chosen where
  // there was room beside the map is kept for there, and not used or
  // replaced here. The condition is the stylesheet's.
  const compact = matchMedia('(max-width: 720px), (max-height: 480px)');
  // Open to begin with where there is room for it beside the map.
  let open = !compact.matches && (typeof view.open === 'boolean' ? view.open : true);

  const keep = () => {
    if (!compact.matches) view.open = open;
    if (settings) settings.set('panel', { ...(typeof view.open === 'boolean' ? { open: view.open } : {}), folded: [...folded] });
    else writeOld(PANEL_KEY, { open: view.open, folded: [...folded] });
  };

  // --- what is in the panel ----------------------------------------------------
  //
  // Three depths and no more: a section, the layers in it, and what each
  // layer is made of. A layer's script says what it has as plain data, a
  // row for the layer and a list of items under it, and every line at
  // every depth is drawn by the one function below: nothing here knows a
  // mob from a biome. A section whose one layer is the whole of it, as
  // the biomes' overlay is, is headed by that layer's own line.

  // The sections there is a place for, in the order they are shown. A
  // section appears once it has a row. A layer that names none goes in the
  // section called what its group is, and any other after these, in the
  // order it was first used.
  const SECTIONS = [
    ['players', 'Players'],
    ['mobs', 'Mobs'],
    ['live', 'Live'],
    ['markers', 'Markers'],
    ['structures', 'Structures'],
    ['biomes', 'Biomes'],
    ['trails', 'Trails'],
    ['overlays', 'Overlays'],
  ];
  // The most items a layer lists before it is asked for the rest, and the
  // most a choice keeps, so that what a viewer hid over a year of visits
  // is still a few kilobytes in their browser.
  const LIST_CAP = 40;
  const MAX_HIDDEN = 200;
  // The most ids a row may say it lists at other times.
  const MAX_ELSEWHERE = 400;
  const TYPEAHEAD_MS = 600;
  const COLOUR = /^#[0-9a-f]{6}$/i;

  const sections = new Map();
  const facets = new Map();
  let made = 0;

  const make = (tag, className, text) => {
    const node = document.createElement(tag);
    if (className) node.className = className;
    // Always as text: a label or a note may carry a name from the world,
    // which a player chose.
    if (text !== undefined) node.textContent = text;
    return node;
  };
  const fmt = (n) => n.toLocaleString('en-US');
  const text = (v) => typeof v === 'string' && v.length > 0 && v.length <= 64;
  const call = (fn, ...args) => {
    // One layer failing to redraw must not leave the rest unswitched.
    try { return fn(...args); } catch (err) { console.error(err); return undefined; }
  };

  // A drawing, not a character: a character would be read out as part of
  // whatever it is on. Each is one path on a square of sixteen.
  const GLYPHS = {
    more: 'M3.5 8h.01M8 8h.01M12.5 8h.01',
    shut: 'M6 3l5 5-5 5',
    twist: 'M6 4l4 4-4 4',
    search: 'M7 12A5 5 0 1 0 7 2a5 5 0 0 0 0 10zM11 11l3 3',
    close: 'M4 4l8 8M12 4l-8 8',
    players: 'M8 8a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5zM3 14c0-2.8 2.2-4.5 5-4.5s5 1.7 5 4.5',
    mobs: 'M3 3h10v10H3zM5.5 6h1.5v1.5H5.5zM9 6h1.5v1.5H9zM7 9.5h2V12H7z',
    live: 'M2 8h3l2-4 3 8 2-4h2',
    markers: 'M8 14s4-4.2 4-7.5a4 4 0 0 0-8 0C4 9.8 8 14 8 14zM8 8a1.5 1.5 0 1 0 0-3 1.5 1.5 0 0 0 0 3z',
    structures: 'M3 14V7l5-4 5 4v7zM6.5 14v-4h3v4',
    biomes: 'M8 14V9M8 9C5 9 3.5 7 3.5 5S5.5 2 8 2s4.5 1 4.5 3S11 9 8 9z',
    trails: 'M3 13c0-4 4-2.5 5-5s4-1.5 5-5M3 13h.01M13 3h.01',
    overlays: 'M3 3h10v10H3zM3 8h10M8 3v10',
    other: 'M3 4h10M3 8h10M3 12h10',
  };
  const SVG = 'http://www.w3.org/2000/svg';
  function glyph(name) {
    const svg = document.createElementNS(SVG, 'svg');
    svg.setAttribute('viewBox', '0 0 16 16');
    svg.setAttribute('aria-hidden', 'true');
    svg.setAttribute('class', name === 'more' ? 'glyph heavy' : 'glyph');
    const path = document.createElementNS(SVG, 'path');
    path.setAttribute('d', Object.hasOwn(GLYPHS, name) ? GLYPHS[name] : GLYPHS.other);
    svg.append(path);
    return svg;
  }

  const button = (className, label, drawn) => {
    const node = make('button', className);
    node.type = 'button';
    if (drawn) {
      node.append(glyph(drawn));
      node.setAttribute('aria-label', label);
      node.title = label;
    } else {
      node.textContent = label;
    }
    return node;
  };

  // --- which items are shown ---------------------------------------------------
  //
  // What a layer is made of can each be hidden or shown alone, and the
  // choice is kept as what differs from everything showing. It is in one
  // of two forms. As { only, hidden, shown } everything is drawn but what
  // is in hidden, and shown is for the few items that are off until asked
  // for; an item that turns up later is drawn. As { mode: 'just', just }
  // nothing is drawn but what is in just, and an item that turns up later
  // is not: this is what "only this, and that one too" comes to, and what
  // a long list of hides is turned into when the other list is the
  // shorter one to keep. With only set, that one is drawn and nothing
  // else, and the rest of the choice is kept as it was underneath, which
  // is what going back from "only" returns to. One choice may be shared
  // by several layers, as the types of mob are across their four rows:
  // "only creepers" is no other mob, whichever row the others are in. It
  // is kept beside the switches as "<group>#<name>"; the first form is the
  // one the live layer always kept its own in.
  //
  // An item that has gone for good, a player who left or a waypoint that
  // was deleted, would otherwise be kept for ever. Each choice notes which
  // of its ids were nowhere in its lists, and when; an id that is still
  // nowhere a month later is dropped. One that comes back in between is
  // taken off the note.
  const DAY_MS = 86_400_000;
  const GRACE_DAYS = 30;
  // How long after a choice's lists first fill it is looked over, so that
  // a list still arriving is not taken for a short one.
  const LOOK_OVER_MS = 10_000;
  const sameSet = (a, b) => (a === null || b === null ? a === b : a.size === b.size && [...a].every((id) => b.has(id)));

  function readFacet(f) {
    const kept = recall(f.group, f.name);
    const was = kept !== null && typeof kept === 'object' && !Array.isArray(kept) ? kept : {};
    const list = (v) => new Set(Array.isArray(v) ? v.filter(text).slice(0, MAX_HIDDEN) : []);
    const next = {
      only: text(was.only) ? was.only : null,
      hidden: list(was.hidden),
      shown: list(was.shown),
      just: was.mode === 'just' ? list(was.just) : null,
      missing: list(was.missing),
      checked: Number.isFinite(was.checked) ? was.checked : null,
    };
    const same = next.only === f.only && sameSet(next.hidden, f.hidden) && sameSet(next.shown, f.shown) && sameSet(next.just, f.just);
    Object.assign(f, next, { over: false });
    return !same;
  }

  const plain = (f) => f.only === null && f.just === null && f.hidden.size === 0 && f.shown.size === 0;
  // The choice as it is kept and as a saved view carries it. Past the
  // most that is kept the list is cut at the end, so what is kept is the
  // same from one write to the next, and the panel says that it was cut.
  const stateOf = (f) => (f.just !== null
    ? { only: f.only, hidden: [], mode: 'just', just: [...f.just].slice(0, MAX_HIDDEN) }
    : { only: f.only, hidden: [...f.hidden].slice(0, MAX_HIDDEN), ...(f.shown.size > 0 ? { shown: [...f.shown].slice(0, MAX_HIDDEN) } : {}) });
  // And with the note of what has gone, which is this browser's own.
  const keptOf = (f) => (plain(f) ? null : { ...stateOf(f), ...(f.missing.size > 0 && f.checked !== null ? { missing: [...f.missing].slice(0, MAX_HIDDEN), checked: f.checked } : {}) });

  function facetOf(group, name, { off = [] } = {}) {
    const key = `${group}#${name}`;
    let f = facets.get(key);
    if (!f) {
      f = { key, group, name, only: null, hidden: new Set(), shown: new Set(), just: null, missing: new Set(), checked: null, over: false, off: new Set(), rows: new Set(), listeners: [], looked: false };
      readFacet(f);
      f.shows = (id) => {
        if (f.only !== null) return f.only === id;
        if (f.just !== null) return f.just.has(id);
        return !f.hidden.has(id) && (!f.off.has(id) || f.shown.has(id));
      };
      // What a layer's script holds of it: enough to ask and to be told.
      f.handle = {
        shows: f.shows,
        get only() { return f.only; },
        filtering: () => !plain(f),
        state: () => stateOf(f),
        onChange(fn) { if (typeof fn === 'function') f.listeners.push(fn); },
        // Shows one item and nothing else, as its "Only" would.
        solo(id) { changeFacet(f, () => { f.only = text(id) ? id : null; }); },
      };
      facets.set(key, f);
    }
    for (const id of off) if (text(id)) f.off.add(id);
    return f;
  }

  // Every item a choice is over, in whichever row it is listed.
  const known = (f) => [...f.rows].flatMap((row) => row.list.map((item) => item.id));

  function showItem(f, id, on) {
    if (f.just !== null) {
      if (on) f.just.add(id); else f.just.delete(id);
    } else if (f.off.has(id)) {
      if (on) f.shown.add(id); else f.shown.delete(id);
      f.hidden.delete(id);
    } else if (on) f.hidden.delete(id); else f.hidden.add(id);
  }

  // Brings a choice to the form that says the same in fewer ids, where
  // one form has run past the most that is kept; and to "everything" once
  // nothing listed is left out. Only when the viewer changes the choice:
  // the two forms differ in what becomes of an item that turns up later,
  // and that must not change under them because a list grew.
  function settle(f) {
    const all = known(f);
    if (f.just !== null) {
      if (all.length > 0 && all.every((id) => f.just.has(id))) {
        f.shown = new Set(all.filter((id) => f.off.has(id)));
        f.hidden = new Set();
        f.just = null;
      } else if (f.just.size > MAX_HIDDEN) {
        const out = all.filter((id) => !f.just.has(id));
        if (out.length <= MAX_HIDDEN) {
          f.hidden = new Set(out.filter((id) => !f.off.has(id)));
          f.shown = new Set(all.filter((id) => f.off.has(id) && f.just.has(id)));
          f.just = null;
        }
      }
    } else if (f.hidden.size > MAX_HIDDEN) {
      const drawn = all.filter((id) => f.shows(id));
      if (drawn.length < f.hidden.size) {
        f.just = new Set(drawn);
        f.hidden = new Set();
        f.shown = new Set();
      }
    }
    f.over = (f.just !== null ? f.just.size : Math.max(f.hidden.size, f.shown.size)) > MAX_HIDDEN;
  }

  // The whole of a choice as it is on this page, which past the most that
  // is kept is more than is written.
  const entire = (f) => JSON.stringify([f.only, [...f.hidden], [...f.shown], f.just === null ? null : [...f.just]]);

  function changeFacet(f, change) {
    const before = entire(f);
    change();
    settle(f);
    if (entire(f) === before) return;
    retain(f.group, f.name, keptOf(f));
    for (const fn of f.listeners) call(fn);
    schedule();
  }

  // Leaving "only this" by a switch: it is still that one and nothing
  // else, now as a list that the switch can add to.
  function leaveOnly(f) {
    if (f.only === null) return;
    f.just = new Set([f.only]);
    f.hidden = new Set();
    f.shown = new Set();
    f.only = null;
  }

  // Looks a choice over for ids that are in none of its lists any more.
  // Asked once a load, a while after its lists have something in them. An
  // id a row says is listed somewhere else, as a kind of structure is in
  // another dimension, has not gone.
  function lookOver(f) {
    const all = new Set([...known(f), ...[...f.rows].flatMap((row) => row.elsewhere)]);
    if (all.size === 0 || plain(f)) return;
    const chosen = f.just !== null ? [...f.just] : [...f.hidden, ...f.shown];
    const gone = new Set(chosen.filter((id) => !all.has(id)));
    const today = Math.floor(Date.now() / DAY_MS);
    const was = JSON.stringify(keptOf(f));
    if (f.checked === null || f.missing.size === 0) {
      f.missing = gone;
      f.checked = today;
    } else if (today - f.checked >= GRACE_DAYS) {
      for (const id of f.missing) {
        if (!gone.has(id)) continue;
        for (const set of [f.hidden, f.shown, f.just]) if (set !== null) set.delete(id);
        gone.delete(id);
      }
      // "Just these" with none of them left would hide whatever came next.
      if (f.just !== null && f.just.size === 0) f.just = null;
      if (f.only !== null && f.missing.has(f.only) && !all.has(f.only)) f.only = null;
      f.missing = gone;
      f.checked = today;
    } else {
      for (const id of [...f.missing]) if (!gone.has(id)) f.missing.delete(id);
    }
    if (JSON.stringify(keptOf(f)) === was) return;
    retain(f.group, f.name, keptOf(f));
    for (const fn of f.listeners) call(fn);
    schedule();
  }

  // --- rows ----------------------------------------------------------------------

  function set(row, on) {
    if (row.on === on) return false;
    row.on = on;
    for (const fn of row.listeners) call(fn, on);
    schedule();
    return true;
  }

  // Switches rows as the viewer does, by a checkbox or a menu, or as a
  // layer's script does on their behalf. A row that is greyed out is left
  // as it is. Whatever was shown alone no longer is once one of its rows
  // has been switched this way, so the way back from it is let go of.
  function switchRows(rows, on) {
    const changes = {};
    for (const row of [...rows]) {
      if (!row.available || !set(row, on)) continue;
      changes[row.key] = on;
      forget(row.section.id, 'all');
      unrest(row.section.id);
    }
    if (Object.keys(changes).length > 0) remember(changes);
  }
  const switchRow = (row, on) => switchRows([row], on);

  // Puts each row where it is wanted whether or not it is greyed out just
  // now, as a saved view does: for showing one thing alone and for going
  // back from that, neither of which a greyed row may be left out of.
  function place(rows, wanted) {
    const changes = {};
    for (const row of [...rows]) if (set(row, wanted(row))) changes[row.key] = row.on;
    if (Object.keys(changes).length > 0) remember(changes);
  }

  // Every row's switch as it stands, by the key its choice is kept under.
  function states() {
    const out = {};
    for (const section of sections.values()) for (const row of section.rows) out[row.key] = row.on;
    return out;
  }

  // Every choice over items as it stands, by the key it is kept under: for
  // a saved view, which has to say "everything" as well as what is hidden.
  function items() {
    const out = {};
    for (const f of facets.values()) out[f.key] = stateOf(f);
    return out;
  }

  // Brings every row and every choice over items in line with what is now
  // kept, for when something other than a switch has changed them, as a
  // saved view does. Everything is switched before any layer is told, and
  // a layer that listens with one function for all its rows is told once,
  // so the map goes from the one picture to the other with nothing drawn
  // between. wanted is the keys a view named, and what comes back is those
  // of them there is no row for here: a layer that has gone, or one this
  // server does not offer. A row that is only greyed out for now is
  // switched all the same, for when it is not.
  function adopt(wanted = []) {
    const choices = kept();
    const told = new Map();
    const found = new Set();
    for (const section of sections.values()) {
      for (const row of section.rows) {
        found.add(row.key);
        // With no choice kept for it, a row is as its layer first had it.
        const on = typeof choices[row.key] === 'boolean' ? choices[row.key] : row.first;
        if (row.on === on) continue;
        row.on = on;
        for (const fn of row.listeners) told.set(fn, on);
      }
    }
    // What was shown alone is as the record now has it, unless the rows
    // have just been put some other way: then there is no going back.
    readSolo();
    if (told.size > 0) forget(...Object.keys(solo));
    for (const f of facets.values()) {
      if (!readFacet(f)) continue;
      // A function that also listens to a row is told what the row said.
      for (const fn of f.listeners) if (!told.has(fn)) told.set(fn, undefined);
    }
    for (const [fn, on] of told) call(fn, on);
    schedule();
    return wanted.filter((key) => !found.has(key));
  }

  function sectionOf(id, label) {
    let section = sections.get(id);
    if (section) return section;
    const at = SECTIONS.findIndex(([name]) => name === id);
    made += 1;
    section = { kind: 'section', bare: null, path: `s:${id}`, id, label: at >= 0 ? SECTIONS[at][1] : String(label || id), rank: at >= 0 ? at : SECTIONS.length + made, rows: [], dom: null };
    sections.set(id, section);
    return section;
  }

  function drop(row) {
    const at = row.section.rows.indexOf(row);
    if (row.section.bare === row) row.section.bare = null;
    else if (at < 0) return;
    else row.section.rows.splice(at, 1);
    if (row.facet) row.facet.rows.delete(row);
    row.listeners.length = 0;
    schedule();
  }

  // Adds a layer's row and returns the handle its script keeps. group and
  // id are what the viewer's choice is saved under, and registering the
  // pair again replaces the row. section is where it is shown, which is
  // the group unless it says otherwise, and whole makes the row the
  // section's own heading. swatch is the class of the colour key drawn
  // beside the label and picture the game's picture of it, by its key or
  // as an element the script made. facet names the choice its items are
  // shown by, which several rows may share, and actions what can be done
  // with the row or one item of it: zoom(id) and go(id), with no id for
  // the row itself. bare makes it no row at all but a list: its items
  // stand directly under the section, with no switch of their own over
  // them, for a section whose switches are about something else. heading
  // is a word or two written over the row, to set it and the rows after
  // it apart from what is above them.
  function register({ group, id, label, enabled = true, order, groupLabel, swatch, picture, section, whole, facet, actions, bare, heading } = {}) {
    if (typeof group !== 'string' || !group || typeof id !== 'string' || !id) {
      throw new TypeError('a layer needs a group and an id');
    }
    const key = `${group}/${id}`;
    for (const other of sections.values()) {
      const again = other.bare && other.bare.key === key ? other.bare : other.rows.find((row) => row.key === key);
      if (again) drop(again);
    }
    const home = sectionOf(typeof section === 'string' && section ? section : group, groupLabel);
    made += 1;
    const row = {
      kind: 'row',
      path: `r:${key}`,
      group,
      id,
      key,
      section: home,
      whole: whole === true,
      label: String(label ?? id),
      order: Number.isFinite(order) ? order : Infinity,
      seq: made,
      bare: bare === true,
      heading: typeof heading === 'string' ? heading : '',
      // A list has no switch to keep: it is as on as its section is.
      on: bare === true || choice(group, id, Boolean(enabled)),
      first: bare === true || Boolean(enabled),
      available: true,
      listeners: [],
      count: null,
      note: '',
      swatch: typeof swatch === 'string' ? swatch : '',
      picture: picture instanceof Node || typeof picture === 'string' ? picture : null,
      facet: typeof facet === 'string' && facet ? facetOf(group, facet) : null,
      actions: actions && typeof actions === 'object' ? actions : {},
      items: new Map(),
      list: [],
      elsewhere: [],
      control: null,
      body: null,
      all: false,
      dom: null,
    };
    if (row.facet) row.facet.rows.add(row);
    if (row.bare) {
      home.bare = row;
    } else {
      // Lower orders first; rows given none go last, as they were registered.
      const next = home.rows.find((other) => row.order < other.order);
      home.rows.splice(next ? home.rows.indexOf(next) : home.rows.length, 0, row);
    }
    schedule();

    return {
      get enabled() { return row.on; },
      // Counts arrive once a second from the live layer; the panel is
      // drawn again at most once for all that changed in a turn, and
      // writes only what differs.
      setCount(n) {
        const count = Number.isFinite(n) ? n : null;
        if (row.count === count) return;
        row.count = count;
        schedule();
      },
      setNote(said) {
        const note = said ? String(said) : '';
        if (row.note === note) return;
        row.note = note;
        schedule();
      },
      // For a layer whose name is the world's and may arrive late.
      setLabel(said) {
        if (!said || row.label === String(said)) return;
        row.label = String(said);
        schedule();
      },
      onToggle(fn) { if (typeof fn === 'function') row.listeners.push(fn); },
      // Switches the row as the viewer would have, for a script that has
      // been asked for what the layer shows: the choice is kept, and the
      // listeners are told.
      setEnabled(on) { switchRow(row, Boolean(on)); },
      setAvailable(available) {
        if (row.available === Boolean(available)) return;
        row.available = Boolean(available);
        schedule();
      },
      // What the layer is made of, as a list in the order to show it.
      // Each is { id, label } with whichever of these it has: picture, a
      // picture's key or an element; swatch, a colour key's class; colour,
      // as #rrggbb, with shape 'line' for one drawn as a line; count;
      // detail, a few words beside the name; note, a line under it; off,
      // for an item that is hidden until asked for; disabled; and go or
      // zoom set to false where the row's action does not apply to it.
      // The list is diffed against the last one, so one given every
      // second costs what changed in it.
      setItems(list) {
        const seen = new Set();
        row.list = [];
        for (const given of Array.isArray(list) ? list : []) {
          if (!given || !text(given.id) || seen.has(given.id)) continue;
          seen.add(given.id);
          let item = row.items.get(given.id);
          if (!item) {
            item = { kind: 'item', path: `i:${key}:${given.id}`, id: given.id, row, dom: null };
            row.items.set(given.id, item);
          }
          item.label = String(given.label ?? given.id);
          item.picture = given.picture instanceof Node || typeof given.picture === 'string' ? given.picture : null;
          item.swatch = typeof given.swatch === 'string' ? given.swatch : '';
          item.colour = typeof given.colour === 'string' && COLOUR.test(given.colour) ? given.colour : '';
          item.shape = given.shape === 'line' ? 'line' : '';
          item.count = Number.isFinite(given.count) ? given.count : null;
          item.detail = given.detail ? String(given.detail) : '';
          item.note = given.note ? String(given.note) : '';
          item.disabled = given.disabled === true;
          item.go = given.go !== false;
          item.zoom = given.zoom !== false;
          if (given.off === true && row.facet) row.facet.off.add(given.id);
          row.list.push(item);
        }
        for (const id of [...row.items.keys()]) if (!seen.has(id)) row.items.delete(id);
        const f = row.facet;
        if (f && !f.looked && row.list.length > 0) {
          f.looked = true;
          setTimeout(() => lookOver(f), LOOK_OVER_MS);
        }
        schedule();
      },
      // The ids this row lists at other times and not now: a choice made
      // of one is kept though it is in no list on this page.
      setElsewhere(ids) {
        row.elsewhere = Array.isArray(ids) ? ids.filter(text).slice(0, MAX_ELSEWHERE) : [];
      },
      // Whether one of its items is on the map: the row is on and the
      // item is not hidden. Two set lookups.
      shows: (item) => row.on && (!row.facet || row.facet.shows(item)),
      // A choice among a few values, drawn under the row as buttons side
      // by side: { label, options: [{ value, label, disabled, title }],
      // value, onChange(value) }, and custom for a value that is typed:
      // { label, title, hint, placeholder, say(value), settle(typed) }
      // where settle gives { value, said, problem }. null takes it away.
      setControl(spec) {
        row.control = spec && typeof spec === 'object' && Array.isArray(spec.options) ? spec : null;
        schedule();
      },
      // For a script from before items: its own controls, shown under its
      // row as it built them. The node is the script's to fill; null
      // takes it away.
      setBody(node) {
        row.body = node instanceof Node ? node : null;
        schedule();
      },
      remove() { drop(row); },
    };
  }

  // --- "only", and the way back ---------------------------------------------------
  //
  // Showing one row alone switches the others in its section off and
  // remembers exactly which of them were on; showing one section alone
  // does the same over the whole panel. "Back to before" puts every row
  // of them as it was, on or off. What is remembered is kept, so the way
  // back is still there after a reload, and it is let go of only when the
  // viewer switches one of those rows themselves, never by looking at
  // which rows there are: they arrive one script at a time.
  let solo = {};
  function readSolo() {
    const kept = recall('panel', 'only');
    solo = {};
    if (kept === null || typeof kept !== 'object' || Array.isArray(kept)) return;
    for (const [scope, was] of Object.entries(kept)) {
      if (was && typeof was === 'object' && text(was.key) && Array.isArray(was.was)) solo[scope] = { key: was.key, was: was.was.filter(text).slice(0, MAX_HIDDEN) };
    }
  }
  readSolo();
  const keepSolo = () => retain('panel', 'only', Object.keys(solo).length > 0 ? solo : null);
  function unrest(id) {
    if (!Object.hasOwn(rested, id)) return;
    delete rested[id];
    keepRested();
  }
  function forget(...scopes) {
    const had = scopes.filter((scope) => Object.hasOwn(solo, scope));
    if (had.length === 0) return;
    for (const scope of had) delete solo[scope];
    keepSolo();
  }

  const rowsIn = (scope) => (scope === 'all' ? [...sections.values()].flatMap((section) => section.rows) : sections.get(scope) ? sections.get(scope).rows : []);
  // Whether this is what is shown alone in its scope.
  const alone = (scope, key) => Object.hasOwn(solo, scope) && solo[scope].key === key;

  function only(scope, key) {
    const rows = rowsIn(scope);
    const mine = (row) => (scope === 'all' ? row.section.id === key : row.key === key);
    // From one alone to another, what is gone back to is still what was
    // there before the first.
    const was = Object.hasOwn(solo, scope) ? solo[scope].was : rows.filter((row) => row.on).map((row) => row.key);
    // A section none of which was on is shown whole.
    const whole = !rows.some((row) => mine(row) && row.on);
    place(rows, (row) => (mine(row) ? whole || row.on : false));
    solo[scope] = { key, was: was.slice(0, MAX_HIDDEN) };
    keepSolo();
    schedule();
  }

  function restore(scope) {
    if (!Object.hasOwn(solo, scope)) return;
    const { was } = solo[scope];
    place(rowsIn(scope), (row) => was.includes(row.key));
    forget(scope);
    schedule();
  }

  // Everything as each layer first had it, and every item showing.
  function reset() {
    solo = {};
    keepSolo();
    rested = {};
    keepRested();
    const changes = {};
    for (const section of sections.values()) {
      for (const row of section.rows) {
        set(row, row.first);
        // With no choice kept for it, a row is as its layer first had it.
        changes[row.key] = null;
      }
    }
    remember(changes);
    for (const f of facets.values()) {
      changeFacet(f, () => {
        f.only = null;
        f.just = null;
        f.hidden.clear();
        f.shown.clear();
      });
    }
    schedule();
  }

  const changed = () => Object.keys(solo).length > 0 || [...facets.values()].some((f) => !plain(f))
    || [...sections.values()].some((section) => section.rows.some((row) => row.on !== row.first));

  // --- one line -------------------------------------------------------------------
  //
  // A line is a twist that opens what is under it, a checkbox that may be
  // half on, a picture or a colour key, a name, a count, "Only", and a
  // button for the rest of what can be done. A section, a layer and an
  // item are all this line; they differ in which of its parts they use.
  // These are a list of checkboxes that open, not a tree widget: every
  // control in a line is what a screen reader already knows it to be, and
  // the arrow keys are added on top for whoever has the focus in it.

  const nodes = new WeakMap();
  // The line the arrow keys are on, which is the one stop the Tab key
  // makes in the list.
  let current = '';
  let query = '';

  function line(node, depth) {
    made += 1;
    const li = make('li', `node d${depth}`);
    const row = make('div', 'row');
    const twist = button('twist', '', 'twist');
    twist.removeAttribute('title');
    twist.tabIndex = -1;
    const check = make('button', 'check');
    check.type = 'button';
    check.setAttribute('role', 'checkbox');
    const pic = make('span', 'pic');
    const name = make('span', 'name');
    name.id = `layers-name-${made}`;
    const detail = make('span', 'detail');
    detail.id = `layers-detail-${made}`;
    const count = make('span', 'count');
    count.id = `layers-count-${made}`;
    const onlyButton = button('only', 'Only');
    onlyButton.tabIndex = -1;
    const more = button('icon more', 'More', 'more');
    more.removeAttribute('title');
    more.setAttribute('aria-haspopup', 'menu');
    more.setAttribute('aria-expanded', 'false');
    const note = make('p', 'note');
    note.id = `layers-note-${made}`;
    const extra = make('div', 'extra');
    const kids = make('ul', 'kids');
    kids.id = `layers-kids-${made}`;
    // Said to be a list, since a list drawn without its bullets is not
    // taken for one by every browser; and named for the line it is under.
    kids.setAttribute('role', 'list');
    kids.setAttribute('aria-labelledby', name.id);
    twist.setAttribute('aria-controls', kids.id);
    twist.setAttribute('aria-labelledby', name.id);
    check.setAttribute('aria-labelledby', name.id);
    check.setAttribute('aria-describedby', `${detail.id} ${count.id} ${note.id}`);
    row.append(twist, check, pic, name, detail, count, onlyButton, more);
    li.append(row, note, extra, kids);
    nodes.set(li, node);
    return { li, row, twist, check, pic, name, detail, count, only: onlyButton, more, note, extra, kids, was: {} };
  }

  // Writes one thing about a line only if it is not what was written last.
  function put(dom, what, value, write) {
    if (dom.was[what] === value) return;
    dom.was[what] = value;
    write(value);
  }

  // A name with the part of it that was searched for marked.
  function named(dom, label) {
    put(dom, 'name', `${label}\n${query}`, () => {
      const at = query === '' ? -1 : label.toLowerCase().indexOf(query);
      if (at < 0) {
        dom.name.textContent = label;
        return;
      }
      dom.name.replaceChildren(label.slice(0, at), make('mark', '', label.slice(at, at + query.length)), label.slice(at + query.length));
    });
  }

  function pictured(dom, { picture, swatch, colour, shape, drawn }) {
    put(dom, 'pic', picture instanceof Node ? picture : `${picture || ''}|${swatch}|${colour}|${shape}|${drawn || ''}`, () => {
      const parts = [];
      if (drawn) parts.push(glyph(drawn));
      if (picture instanceof Node) parts.push(picture);
      else if (typeof picture === 'string' && picture && app.icons && app.icons.picture) parts.push(app.icons.picture(picture));
      if (swatch || colour) {
        const key = make('i', colour ? `swatch ${shape}` : swatch);
        // Only in the one form a colour is ever given in.
        if (colour) key.style.backgroundColor = colour;
        parts.push(key);
      }
      dom.pic.replaceChildren(...parts);
      dom.pic.hidden = parts.length === 0;
    });
  }

  // What a line shows, common to all three sorts of it.
  function show(dom, { label, state, disabled, masked, twisted, open, detail, count, note, soloed, menu: hasMenu }) {
    named(dom, label);
    put(dom, 'state', state, (v) => dom.check.setAttribute('aria-checked', v));
    put(dom, 'disabled', disabled, (v) => {
      if (v) dom.check.setAttribute('aria-disabled', 'true'); else dom.check.removeAttribute('aria-disabled');
      dom.li.classList.toggle('unavailable', v);
    });
    put(dom, 'masked', masked, (v) => dom.li.classList.toggle('masked', v));
    // A line with nothing under it keeps the room of what would open it,
    // so that its checkbox stands one step in from the checkbox of the
    // line it is under, and not level with that line's own opener.
    put(dom, 'twisted', twisted, (v) => {
      dom.twist.disabled = !v;
      dom.li.classList.toggle('leaf', !v);
    });
    put(dom, 'open', twisted && open, (v) => {
      dom.twist.setAttribute('aria-expanded', String(v));
      dom.kids.hidden = !v;
      dom.extra.hidden = !v;
    });
    put(dom, 'detail', detail || '', (v) => { dom.detail.textContent = v; });
    put(dom, 'count', count || '', (v) => { dom.count.textContent = v; });
    put(dom, 'note', note || '', (v) => { dom.note.textContent = v; });
    put(dom, 'solo', soloed, (v) => {
      dom.only.setAttribute('aria-pressed', String(v));
      dom.li.classList.toggle('alone', v);
    });
    put(dom, 'said', label, (v) => {
      dom.only.setAttribute('aria-label', `Only ${v}`);
      dom.more.setAttribute('aria-label', `More for ${v}`);
    });
    put(dom, 'menu', hasMenu !== false, (v) => { dom.more.hidden = !v; dom.only.hidden = !v; });
    const stop = dom.li === currentLine();
    put(dom, 'stop', stop, (v) => {
      dom.check.tabIndex = v ? 0 : -1;
      dom.more.tabIndex = v ? 0 : -1;
    });
  }

  let currentNode = null;
  const currentLine = () => (currentNode && currentNode.dom ? currentNode.dom.li : null);

  // Puts a list's lines in the page in the order wanted, moving only what
  // is out of place. While the viewer is pointing at the list or has the
  // focus in it, nothing in it moves: a line that is new goes on the end,
  // and is put in its place once they have gone.
  function arrange(list, wanted) {
    const want = new Set(wanted);
    for (const child of [...list.children]) {
      if (want.has(child)) continue;
      if (child.contains(document.activeElement)) refocus = list.closest('.node');
      child.remove();
    }
    const busy = list.children.length > 0 && (list.matches(':hover') || list.contains(document.activeElement));
    if (busy) {
      for (const node of wanted) if (node.parentNode !== list) list.append(node);
      return;
    }
    wanted.forEach((node, i) => {
      if (list.children[i] !== node) list.insertBefore(node, list.children[i] || null);
    });
  }
  let refocus = null;

  // --- the search ---------------------------------------------------------------
  //
  // Typing in the panel's box narrows the panel to the lines whose names
  // hold what was typed, and opens whatever a match is under. It changes
  // nothing on the map and nothing that is kept: clearing the box puts
  // the panel as it was.
  const hit = (label) => query !== '' && label.toLowerCase().includes(query);

  // --- drawing the panel ----------------------------------------------------------

  // The one section the live layers had is two now.
  if (folded.delete('live')) for (const id of ['players', 'mobs']) folded.add(id);
  // Which layers are open to their items, which none is until asked.
  const opened = new Set((() => {
    const was = recall('panel', 'open');
    return Array.isArray(was) ? was.filter(text) : [];
  })());

  const OVER = `More of these are chosen one by one than are kept: the first ${MAX_HIDDEN} will be as they are next time, the rest shown or hidden as they were at first. Only, or switching the whole group, keeps any number.`;
  const weight = (item) => (item.count === null ? 1 : item.count);
  const itemOn = (row, item) => row.on && (!row.facet || row.facet.shows(item.id));
  // An item that is off until asked for and has not been asked for is as
  // the page first has it: it is not shown, and it is not something the
  // viewer has hidden. It makes nothing read as filtered.
  const standing = (f, id) => f.only === null && f.just === null && f.off.has(id) && !f.shown.has(id) && !f.hidden.has(id);
  const hiddenIn = (row, item) => !row.facet.shows(item.id) && !standing(row.facet, item.id);
  // A row is on, off, or on with some of what it is made of hidden.
  function rowState(row) {
    if (!row.on) return 'false';
    return row.facet && (row.facet.only !== null && !row.list.some((item) => item.id === row.facet.only) || row.list.some((item) => hiddenIn(row, item))) ? 'mixed' : 'true';
  }

  function countOf(row) {
    if (row.count === null) return '';
    if (!row.on || !row.facet || row.list.length === 0) return fmt(row.count);
    let hidden = 0;
    for (const item of row.list) if (hiddenIn(row, item)) hidden += weight(item);
    return hidden > 0 ? `${fmt(Math.max(0, row.count - hidden))} / ${fmt(row.count)}` : fmt(row.count);
  }

  // The lines for a row's items, as many as are to be shown.
  function itemLines(row, depth, shownAll, tally) {
    const f = row.facet;
    const listed = query === '' || shownAll ? row.list : row.list.filter((item) => hit(item.label));
    const some = row.all ? listed : listed.slice(0, LIST_CAP);
    const lines = [];
    for (const item of some) {
      if (!item.dom) item.dom = line(item, depth);
      const on = itemOn(row, item);
      pictured(item.dom, item);
      show(item.dom, {
        label: item.label,
        state: String(on),
        disabled: item.disabled || !row.available || !f,
        masked: !row.on || row.masked === true,
        twisted: false,
        detail: item.detail,
        count: item.count === null ? '' : fmt(item.count),
        note: item.note,
        soloed: Boolean(f) && row.on && f.only === item.id,
        menu: Boolean(f),
      });
      lines.push(item.dom.li);
    }
    // A line that is no longer listed is let go of. A set, since a list
    // shown whole is gone through with every frame.
    const drawn = new Set(some);
    for (const item of row.list) if (item.dom && !drawn.has(item)) item.dom = null;
    if (listed.length > some.length) {
      if (!row.rest) {
        row.rest = make('li', `node d${depth} rest`);
        const all = button('link', '');
        all.addEventListener('click', () => {
          row.all = true;
          schedule();
        });
        row.rest.append(all);
      }
      const said = `Show all ${fmt(listed.length)}`;
      if (row.rest.firstChild.textContent !== said) row.rest.firstChild.textContent = said;
      lines.push(row.rest);
    }
    return lines;
  }

  // What stands between a row and its items: its choice among a few
  // values, and whatever a script from before items built for itself.
  function extras(row, dom) {
    put(dom, 'control', row.control !== null, (has) => {
      if (dom.control) dom.control.node.remove();
      dom.control = has ? control(row.control) : null;
      if (dom.control) dom.extra.prepend(dom.control.node);
    });
    if (dom.control) dom.control.paint(row.control);
  }

  function paintRow(row, tally) {
    const whole = row.whole;
    if (!row.dom) row.dom = whole ? row.section.dom : line(row, 2);
    const dom = row.dom;
    const matched = hit(row.section.label) || hit(row.label);
    const within = query !== '' && row.list.some((item) => hit(item.label));
    const twisted = row.list.length > 0 || row.control !== null;
    const open = whole ? (query === '' ? !folded.has(row.section.id) : true) : (query === '' ? opened.has(row.key) : within || (matched && opened.has(row.key)));
    const state = rowState(row);
    // What there is to show, and how much of it is: every item of a row
    // that is on, and a row that is off as one. What is off as the page
    // first has it, a row or an item, is not counted as hidden.
    const leaves = row.on && row.facet && row.list.length > 0 ? row.list.filter((item) => !standing(row.facet, item.id)) : null;
    if (leaves) {
      tally.all += leaves.length;
      tally.on += leaves.filter((item) => row.facet.shows(item.id)).length;
    } else if (row.on || row.first) {
      tally.all += 1;
      tally.on += row.on ? 1 : 0;
    }
    tally.rows += 1;
    tally.rowsOn += state === 'false' ? 0 : 1;
    tally.mixed = tally.mixed || state === 'mixed';
    if (!whole) pictured(dom, row);
    else pictured(dom, { drawn: row.section.id, swatch: '', colour: '', shape: '' });
    show(dom, {
      label: whole ? row.section.label : row.label,
      state,
      disabled: !row.available,
      masked: false,
      twisted: whole ? true : twisted,
      open,
      count: countOf(row),
      note: row.facet && row.facet.over ? `${row.note ? `${row.note} ` : ''}${OVER}` : row.note,
      soloed: whole ? alone('all', row.section.id) : alone(row.section.id, row.key),
    });
    put(dom, 'body', row.body, (node) => {
      if (dom.legacy) dom.legacy.remove();
      dom.legacy = node ? make('div', 'legacy') : null;
      if (dom.legacy) {
        dom.legacy.append(node);
        dom.note.after(dom.legacy);
      }
    });
    extras(row, dom);
    const visible = query === '' || matched || within;
    if (!whole) {
      arrange(dom.kids, open ? itemLines(row, 3, matched, tally) : []);
      if (!open) for (const item of row.list) item.dom = null;
    }
    return visible;
  }

  function paintSection(section, tally) {
    if (!section.dom) section.dom = line(section, 1);
    const dom = section.dom;
    const whole = section.rows.find((row) => row.whole) || null;
    const mine = { all: 0, on: 0, rows: 0, rowsOn: 0, mixed: false };
    const lines = [];
    let visible = hit(section.label);
    let headed = '';
    for (const row of section.rows) {
      const seen = paintRow(row, mine);
      visible = visible || seen;
      if (row === whole || !seen) continue;
      if (row.heading !== '' && row.heading !== headed) {
        headed = row.heading;
        if (!section.headings) section.headings = new Map();
        if (!section.headings.has(headed)) section.headings.set(headed, make('li', 'subhead', headed));
        lines.push(section.headings.get(headed));
      }
      lines.push(row.dom.li);
    }
    // A list with no switch of its own: its items stand first under the
    // section, counted and searched as any layer's are.
    const { bare } = section;
    if (bare) {
      const within = query !== '' && bare.list.some((item) => hit(item.label));
      visible = visible || within;
      const listed = bare.list.filter((item) => !standing(bare.facet, item.id));
      if (mine.rowsOn > 0) {
        mine.all += listed.length;
        mine.on += listed.filter((item) => bare.facet.shows(item.id)).length;
      }
      mine.mixed = mine.mixed || bare.facet.only !== null || bare.list.some((item) => hiddenIn(bare, item));
      bare.masked = mine.rowsOn === 0;
    }
    if (whole) {
      if (dom.was.open) lines.unshift(...itemLines(whole, 2, hit(section.label) || hit(whole.label), mine));
      else for (const item of whole.list) item.dom = null;
    } else {
      const open = query === '' ? !folded.has(section.id) : true;
      pictured(dom, { drawn: section.id, swatch: '', colour: '', shape: '' });
      show(dom, {
        label: section.label,
        state: mine.rowsOn === 0 ? 'false' : mine.rowsOn === mine.rows && !mine.mixed ? 'true' : 'mixed',
        disabled: false,
        masked: false,
        twisted: true,
        open,
        count: `${mine.rowsOn}/${mine.rows}`,
        note: '',
        soloed: alone('all', section.id),
      });
    }
    if (bare) {
      if (dom.was.open) lines.unshift(...itemLines(bare, 2, hit(section.label), mine));
      else for (const item of bare.list) item.dom = null;
    }
    arrange(dom.kids, dom.was.open ? lines : []);
    put(dom, 'seen', query === '' || visible, (v) => { dom.li.hidden = !v; });
    section.tally = mine;
    tally.all += mine.all;
    tally.on += mine.on;
    tally.seen = tally.seen || query === '' || visible;
  }

  let queued = false;
  // Everything that changes in one turn of the page is drawn once, after
  // it: a frame of the live layer changes five rows and sixty items, and
  // is one pass here.
  function schedule() {
    if (queued) return;
    queued = true;
    Promise.resolve().then(render);
  }

  function render() {
    queued = false;
    const shown = [...sections.values()].filter((section) => section.rows.length > 0 || section.bare).sort((a, b) => a.rank - b.rank);
    const tally = { all: 0, on: 0, seen: false };
    for (const section of sections.values()) {
      if (section.rows.length > 0 || section.bare) continue;
      if (section.dom) section.dom.li.remove();
    }
    for (const section of shown) paintSection(section, tally);
    arrange(tree, shown.map((section) => section.dom.li));
    // The one stop the Tab key makes is always on a line that is showing
    // and can be pressed: the line it was on may have gone, been put away
    // with its group, or been narrowed out by the search.
    if (!stoppable(currentNode)) {
      const showing = [...tree.querySelectorAll('.check')].filter(listed);
      const first = showing.find((check) => check.getAttribute('aria-disabled') !== 'true') || showing[0];
      moveStop(first ? nodeAt(first) : null);
    }
    if (refocus) {
      const node = nodes.get(refocus);
      refocus = null;
      if (node) focusOn(node);
    }
    const said = tally.on < tally.all ? `${fmt(tally.on)} of ${fmt(tally.all)} shown` : `All ${fmt(tally.all)} shown`;
    if (statusText.textContent !== said) statusText.textContent = said;
    resetButton.hidden = !changed();
    // Marked while it is the viewer's doing, and not for what is off until
    // asked for.
    status.classList.toggle('cut', tally.on < tally.all && changed());
    none.hidden = query === '' || tally.seen;
    if (!none.hidden) none.textContent = 'No layer has that in its name.';
    el.panel.hidden = shown.length === 0;
    paintRail(shown);
  }

  // The stop the Tab key makes is moved by writing the two lines it moves
  // between, and nothing else.
  // Whether a part of a line is showing in the list, whatever the panel
  // itself is doing: put away to its rail, it still has its one stop.
  const listed = (part) => {
    const within = part.closest('[hidden]');
    return within === null || !tree.contains(within);
  };
  const stoppable = (node) => Boolean(node) && Boolean(node.dom) && node.dom.li.isConnected && listed(node.dom.li)
    && node.dom.check.getAttribute('aria-disabled') !== 'true';

  function moveStop(node) {
    const was = currentNode;
    currentNode = node;
    for (const other of [was, node]) {
      if (!other || !other.dom) continue;
      const stop = other === node;
      put(other.dom, 'stop', stop, (v) => {
        other.dom.check.tabIndex = v ? 0 : -1;
        other.dom.more.tabIndex = v ? 0 : -1;
      });
    }
  }

  function focusOn(node) {
    if (!node || !node.dom) return;
    moveStop(node);
    node.dom.check.focus();
  }

  // --- what a line does ----------------------------------------------------------

  const wholeOf = (section) => section.rows.find((row) => row.whole) || null;

  function toggle(node) {
    if (node.kind === 'section') {
      const whole = wholeOf(node);
      if (whole) switchRow(whole, !whole.on);
      else rest(node);
    } else if (node.kind === 'row') {
      switchRow(node, !node.on);
    } else {
      const { row } = node;
      const f = row.facet;
      if (!f || node.disabled || !row.available) return;
      if (!row.on) {
        // Under a layer that is off, an item switched on brings the layer
        // on as it was last chosen, with this item shown as well.
        changeFacet(f, () => {
          if (f.only !== null && f.only !== node.id) leaveOnly(f);
          showItem(f, node.id, true);
        });
        switchRow(row, true);
        return;
      }
      changeFacet(f, () => {
        const on = f.shows(node.id);
        leaveOnly(f);
        showItem(f, node.id, !on);
      });
    }
  }

  // A section's own checkbox, where it is not one layer's: with anything
  // in it on, it switches the section off and remembers which rows were
  // on; with nothing on, it puts those back, or everything if there is
  // nothing remembered. A section that is partly on is never switched
  // wholly on by one press: that would lose what was chosen in it.
  let rested = (() => {
    const kept = recall('panel', 'rest');
    const out = {};
    if (kept === null || typeof kept !== 'object' || Array.isArray(kept)) return out;
    for (const [id, was] of Object.entries(kept)) if (Array.isArray(was)) out[id] = was.filter(text).slice(0, MAX_HIDDEN);
    return out;
  })();
  const keepRested = () => retain('panel', 'rest', Object.keys(rested).length > 0 ? rested : null);
  function rest(section) {
    const on = section.rows.filter((row) => row.on);
    if (on.length > 0) {
      place(section.rows, () => false);
      rested[section.id] = on.map((row) => row.key);
    } else {
      const was = Object.hasOwn(rested, section.id) ? rested[section.id].filter((key) => section.rows.some((row) => row.key === key)) : [];
      place(section.rows, (row) => was.length === 0 || was.includes(row.key));
      delete rested[section.id];
    }
    forget(section.id, 'all');
    keepRested();
    schedule();
  }

  const expandable = (node) => node.kind === 'section' || (node.kind === 'row' && (node.whole || node.list.length > 0 || node.control !== null));

  function twist(node, to) {
    if (!expandable(node)) return;
    const section = node.kind === 'section' ? node : node.whole ? node.section : null;
    if (section) {
      const shut = to === undefined ? !folded.has(section.id) : !to;
      if (shut) folded.add(section.id); else folded.delete(section.id);
      keep();
    } else {
      const open = to === undefined ? !opened.has(node.key) : to;
      if (open) opened.add(node.key); else opened.delete(node.key);
      // Seen whole the first time it is opened after being put away.
      if (!open) node.all = false;
      keepOpened();
    }
    schedule();
  }
  const keepOpened = () => retain('panel', 'open', opened.size > 0 ? [...opened].slice(0, MAX_HIDDEN) : null);
  const isOpen = (node) => Boolean(node.dom) && node.dom.was.open === true;

  function foldAll(shut) {
    for (const section of sections.values()) {
      if (shut) folded.add(section.id); else folded.delete(section.id);
      for (const row of section.rows) {
        if (row.whole || row.list.length === 0) continue;
        if (shut) opened.delete(row.key); else opened.add(row.key);
      }
    }
    keep();
    keepOpened();
    schedule();
  }

  function onlyThis(node) {
    if (node.kind === 'item') {
      const f = node.row.facet;
      if (!f) return;
      // Pressed on the one already alone, it lets the rest back.
      changeFacet(f, () => { f.only = f.only === node.id ? null : node.id; });
      if (!node.row.on) switchRow(node.row, true);
      return;
    }
    const scope = node.kind === 'section' || node.whole ? 'all' : node.section.id;
    const key = scope === 'all' ? (node.kind === 'section' ? node.id : node.section.id) : node.key;
    if (alone(scope, key)) restore(scope);
    else only(scope, key);
  }

  // Every item of a row showing, or the row and all of it off.
  function showAll(row, on) {
    if (!on) {
      switchRow(row, false);
      return;
    }
    const f = row.facet;
    if (f) {
      changeFacet(f, () => {
        f.only = null;
        for (const item of row.list) showItem(f, item.id, true);
      });
    }
    switchRow(row, true);
  }

  const act = (row, what, id) => {
    if (typeof row.actions[what] === 'function') call(row.actions[what], id);
  };

  function entriesFor(node) {
    const entries = [];
    if (node.kind === 'item') {
      const { row } = node;
      const f = row.facet;
      entries.push({ label: f.only === node.id && row.on ? 'Back to before' : 'Only this', act: () => onlyThis(node) });
      entries.push({ label: 'Show all in group', act: () => showAll(row, true) });
      // A list with no switch over it is hidden by its section's.
      if (!row.bare) entries.push({ label: 'Hide all in group', act: () => showAll(row, false) });
      if (typeof row.actions.zoom === 'function' && node.zoom) entries.push(null, { label: 'Zoom to', act: () => act(row, 'zoom', node.id) });
      if (typeof row.actions.go === 'function' && node.go) entries.push(null, { label: 'Go to', act: () => act(row, 'go', node.id) });
      return entries;
    }
    const section = node.kind === 'section' ? node : node.section;
    const whole = node.kind === 'section' ? wholeOf(node) : node.whole ? node : null;
    const row = node.kind === 'row' ? node : whole;
    const scope = node.kind === 'section' || whole ? 'all' : section.id;
    const key = scope === 'all' ? section.id : node.key;
    entries.push({ label: alone(scope, key) ? 'Back to before' : scope === 'all' ? 'Only this section' : 'Only this', act: () => onlyThis(node) });
    if (row && (whole || row.list.length > 0)) {
      entries.push({ label: 'Show all in group', act: () => showAll(row, true) }, { label: 'Hide all in group', act: () => showAll(row, false) });
    } else {
      entries.push({ label: 'Show all in group', act: () => switchRows(section.rows, true) }, { label: 'Hide all in group', act: () => switchRows(section.rows, false) });
    }
    if (row && typeof row.actions.zoom === 'function') entries.push(null, { label: 'Zoom to', act: () => act(row, 'zoom') });
    return entries;
  }

  // Enter does what a line is for: opens what is under it, goes to the
  // one thing it stands for, or failing both switches it.
  function activate(node) {
    if (expandable(node)) twist(node);
    else if (node.kind === 'item' && typeof node.row.actions.go === 'function' && node.go) act(node.row, 'go', node.id);
    else if (node.kind === 'item' && typeof node.row.actions.zoom === 'function' && node.zoom) act(node.row, 'zoom', node.id);
    else toggle(node);
  }

  const nodeAt = (target) => {
    const li = target instanceof Element ? target.closest('.node') : null;
    return li ? nodes.get(li) || null : null;
  };

  // --- a choice among a few values --------------------------------------------------
  //
  // Buttons side by side, one of them on, with a last one for a value
  // that is typed: that opens a small box of its own to type in, at the
  // panel's full width, and never a box in the row.
  function control(first) {
    const node = make('div', 'control');
    const group = make('div', 'segmented');
    group.setAttribute('role', 'radiogroup');
    node.append(group);
    let spec = first;
    let built = '';
    let custom = null;
    let chosenAt = null;

    const choose = (value) => {
      if (typeof spec.onChange === 'function') call(spec.onChange, value);
      schedule();
    };

    function paint(now) {
      spec = now;
      const sign = JSON.stringify([spec.label, spec.options.map((o) => [o.value, o.label, o.disabled === true, o.title || '']), Boolean(spec.custom)]);
      if (sign !== built) {
        built = sign;
        group.setAttribute('aria-label', String(spec.label || ''));
        const within = group.contains(document.activeElement);
        group.replaceChildren(...spec.options.map((option) => {
          const choice = button('segment', String(option.label));
          choice.setAttribute('role', 'radio');
          choice.disabled = option.disabled === true;
          if (option.title) choice.title = String(option.title);
          choice.addEventListener('click', () => choose(option.value));
          return choice;
        }));
        custom = null;
        if (spec.custom) {
          custom = button('segment custom', '');
          custom.setAttribute('aria-haspopup', 'dialog');
          custom.setAttribute('aria-expanded', 'false');
          custom.addEventListener('click', () => typed(custom, spec, choose));
          group.append(custom);
        }
        if (within) group.querySelector('button:not(:disabled)').focus();
        chosenAt = null;
      }
      const at = spec.options.findIndex((option) => option.value === spec.value);
      // Asked for with every frame of the live layer, and written only
      // when the choice has changed.
      if (at === chosenAt) return;
      chosenAt = at;
      [...group.children].forEach((choice, i) => {
        if (choice === custom) return;
        choice.setAttribute('aria-checked', String(i === at));
        choice.tabIndex = i === at || (at < 0 && i === 0) ? 0 : -1;
      });
      if (custom) {
        const said = at < 0 && typeof spec.custom.say === 'function' ? `${spec.custom.label}: ${spec.custom.say(spec.value)}` : `${spec.custom.label}…`;
        if (custom.textContent !== said) custom.textContent = said;
        custom.classList.toggle('on', at < 0);
      }
    }

    // The arrow keys move among the choices, as among radio buttons.
    group.addEventListener('keydown', (e) => {
      if (!['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'].includes(e.key)) return;
      const choices = [...group.querySelectorAll('button:not(:disabled)')];
      const at = choices.indexOf(document.activeElement);
      if (at < 0) return;
      e.preventDefault();
      e.stopPropagation();
      const by = e.key === 'ArrowLeft' || e.key === 'ArrowUp' ? -1 : 1;
      choices[(at + by + choices.length) % choices.length].focus();
    });
    return { node, paint };
  }

  // --- the shell ---------------------------------------------------------------
  //
  // The panel is one of three things by the room there is. With 960 pixels
  // or more across it is docked beside the map, which is resized by it and
  // never covered. Narrower than that it floats over the map's edge when
  // open, and only its rail takes room. On a small screen it is a sheet
  // from the bottom that stops at three heights, and below the tallest the
  // map above it is still the map. The page as it is sent has only the
  // panel's box and its button, so that a page from before this script and
  // this script from before the page both find what they look for;
  // everything else of the shell is made here.
  const DOCKED = '(min-width: 960px)';
  const MIN_WIDTH = 240;
  const MAX_WIDTH = 480;
  // The most of the window the panel may take, whatever was chosen.
  const MAX_SHARE = 0.45;
  const WIDTHS = [['Small', 280], ['Medium', 340], ['Large', 400]];
  const STEP = 16;
  const DETENTS = ['peek', 'half', 'full'];
  const roomy = matchMedia(DOCKED);

  // The width and the sheet's height are kept beside the layers' own
  // choices, under a name of the panel's: the script that keeps the record
  // may be one from before the panel had either, and would drop a part of
  // the record it has no description of.
  const shell = (() => {
    const kept = recall('panel', 'shell');
    const was = kept !== null && typeof kept === 'object' && !Array.isArray(kept) ? kept : {};
    return {
      width: Number.isFinite(was.width) ? Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, Math.round(was.width))) : null,
      detent: DETENTS.includes(was.detent) ? was.detent : 'half',
    };
  })();
  const keepShell = () => retain('panel', 'shell', { ...(shell.width === null ? {} : { width: shell.width }), detent: shell.detent });

  el.panel.classList.add('shell');
  el.body.classList.add('panel');
  const head = make('div', 'panel-head');
  const title = make('h2', 'panel-title', 'Layers');
  title.id = 'layers-title';
  const menuButton = button('icon panel-menu', 'Panel options', 'more');
  menuButton.setAttribute('aria-haspopup', 'menu');
  menuButton.setAttribute('aria-expanded', 'false');
  const shutButton = button('icon panel-shut', 'Collapse the layer panel', 'shut');
  head.append(title, menuButton, shutButton);
  const grab = button('panel-grab', 'Layers sheet');
  grab.textContent = '';

  const finder = make('div', 'panel-search');
  const box = make('input');
  box.type = 'search';
  box.id = 'layers-search';
  box.maxLength = 64;
  box.autocomplete = 'off';
  box.spellcheck = false;
  box.placeholder = 'Find a layer';
  box.setAttribute('aria-label', 'Find a layer in the panel by its name. This does not change what the map shows.');
  const clear = button('clear', '');
  clear.setAttribute('aria-label', 'Clear the box');
  clear.title = 'Clear the box (Esc)';
  clear.hidden = true;
  finder.append(glyph('search'), box, clear);

  const status = make('p', 'panel-status');
  const statusText = make('span');
  statusText.setAttribute('role', 'status');
  const resetButton = button('link', 'Reset');
  resetButton.title = 'Put every layer back to how the page first has it';
  resetButton.hidden = true;
  status.append(statusText, resetButton);

  const scroll = make('div', 'panel-scroll');
  const tree = make('ul', 'tree');
  tree.id = 'layers-list';
  tree.setAttribute('aria-labelledby', title.id);
  const none = make('p', 'panel-none');
  none.setAttribute('role', 'status');
  none.hidden = true;
  scroll.append(tree, none);
  el.body.append(grab, head, finder, status, scroll);
  const rail = make('nav', 'panel-rail');
  rail.setAttribute('aria-label', 'Layer sections');
  const sizer = make('div', 'panel-sizer');
  sizer.setAttribute('role', 'separator');
  sizer.setAttribute('aria-orientation', 'vertical');
  sizer.setAttribute('aria-controls', el.body.id);
  sizer.setAttribute('aria-label', 'Width of the layer panel');
  sizer.tabIndex = 0;
  sizer.title = 'Drag to resize. Double-click for the usual width.';
  el.panel.append(rail, sizer);
  el.toggle.title = 'Layers (L)';

  const stage = el.panel.closest('.stage') || el.panel.parentElement;
  const mode = () => (compact.matches ? 'sheeted' : roomy.matches ? 'docked' : 'floating');
  const widest = () => Math.max(MIN_WIDTH, Math.min(MAX_WIDTH, Math.floor(innerWidth * MAX_SHARE)));
  // The width the stylesheet gives a panel nobody has sized, which goes
  // with the density.
  // Both are the stylesheet's, and are asked for once and again only when
  // the density or the window changes: asking lays the page out, which a
  // drag of the panel's edge must not do with every move.
  let measured = null;
  const sizes = () => {
    if (measured === null) {
      const style = getComputedStyle(el.panel);
      measured = { usual: parseInt(style.getPropertyValue('--panel-default'), 10) || 340, rail: parseInt(style.getPropertyValue('--rail'), 10) || 40 };
    }
    return measured;
  };
  const usual = () => sizes().usual;
  const widthNow = () => Math.min(widest(), Math.max(MIN_WIDTH, shell.width === null ? usual() : shell.width));

  function setWidth(px, lasting) {
    shell.width = px === null ? null : Math.min(widest(), Math.max(MIN_WIDTH, Math.round(px)));
    paintShell();
    if (lasting) keepShell();
  }

  function setDetent(next) {
    if (!DETENTS.includes(next)) return;
    shell.detent = next;
    paintShell();
    keepShell();
  }

  function paintShell() {
    const now = mode();
    for (const name of ['docked', 'floating', 'sheeted']) el.panel.classList.toggle(name, name === now);
    for (const name of DETENTS) el.panel.classList.toggle(`detent-${name}`, now === 'sheeted' && name === shell.detent);
    const width = widthNow();
    // The stylesheet has no way to ask for a size, so this hands it one.
    el.panel.style.setProperty('--panel-width', `${width}px`);
    // What the panel takes of the map's width or lies over, for what is
    // centred over the map that can be seen and not over the stage.
    stage.style.setProperty('--dock', now === 'sheeted' ? '0px' : `${open ? width : sizes().rail}px`);
    // At its full height the sheet covers the map and whatever else the
    // stage holds, which is then out of reach of the keyboard and of a
    // screen reader as it is of a finger, and is given back when the
    // sheet is lower or gone.
    const covered = now === 'sheeted' && open && shell.detent === 'full';
    for (const under of stage.children) {
      if (under === el.panel || (!covered && under.dataset.covered !== 'yes')) continue;
      under.inert = covered;
      if (covered) under.dataset.covered = 'yes'; else delete under.dataset.covered;
    }
    sizer.hidden = now === 'sheeted' || !open;
    sizer.setAttribute('aria-valuemin', String(MIN_WIDTH));
    sizer.setAttribute('aria-valuemax', String(widest()));
    sizer.setAttribute('aria-valuenow', String(width));
    sizer.setAttribute('aria-valuetext', `${width} pixels`);
    rail.hidden = now === 'sheeted' || open;
    const small = now === 'sheeted';
    const said = small ? 'Close the layers' : 'Collapse the layer panel to a rail (L)';
    if (shutButton.title !== said) {
      shutButton.setAttribute('aria-label', said);
      shutButton.title = said;
      shutButton.replaceChildren(glyph(small ? 'close' : 'shut'));
    }
    const height = { peek: 'short', half: 'half height', full: 'full height' }[shell.detent];
    grab.setAttribute('aria-label', `Layers sheet, ${height}. Press to change its height, or use the arrow keys.`);
  }

  // The sections on the rail: a button each, which opens the panel at
  // that section, marked where the section has something hidden.
  function paintRail(shown) {
    for (const section of shown) {
      if (!section.railed) {
        section.railed = button('icon', section.label, section.id);
        section.railed.append(make('i', 'badge'));
        section.railed.addEventListener('click', () => {
          setOpen(true);
          folded.delete(section.id);
          keep();
          render();
          section.dom.li.scrollIntoView({ block: 'start' });
          focusOn(section);
        });
      }
      const { on, all, rowsOn } = section.tally;
      const state = rowsOn === 0 ? 'none' : on === all ? 'all' : 'some';
      const said = `${section.label}: ${state === 'all' ? 'all shown' : state === 'none' ? 'all hidden' : `${fmt(on)} of ${fmt(all)} shown`}`;
      if (section.railed.dataset.state !== state) section.railed.dataset.state = state;
      if (section.railed.title !== said) {
        section.railed.title = said;
        section.railed.setAttribute('aria-label', said);
      }
    }
    const order = shown.map((section) => section.railed);
    if (order.length !== rail.children.length || order.some((node, i) => rail.children[i] !== node)) rail.replaceChildren(...order);
  }

  // --- what hangs from a button ---------------------------------------------------
  //
  // One menu, opened under whichever button asked for it. entries are
  // { label, checked, disabled, act }, a string for a title, or null for
  // a rule between them; checked makes one a choice among several. It
  // shuts on a choice, on Escape, and on a press outside it, and gives
  // the focus back.
  const menu = make('div', 'panel-popup menu');
  menu.setAttribute('role', 'menu');
  menu.hidden = true;
  const popover = make('div', 'panel-popup popover');
  popover.setAttribute('role', 'dialog');
  popover.hidden = true;
  el.panel.append(menu, popover);
  let hungFrom = null;

  function shutPopup(back) {
    if (hungFrom === null) return;
    const anchor = hungFrom;
    hungFrom = null;
    for (const popup of [menu, popover]) {
      popup.hidden = true;
      popup.replaceChildren();
    }
    anchor.setAttribute('aria-expanded', 'false');
    if (back && anchor.isConnected) anchor.focus();
  }

  // Under the button and inside the window, whichever edge it is near.
  function hang(popup, anchor) {
    hungFrom = anchor;
    popup.hidden = false;
    anchor.setAttribute('aria-expanded', 'true');
    const at = anchor.getBoundingClientRect();
    const size = popup.getBoundingClientRect();
    popup.style.left = `${Math.max(8, Math.min(innerWidth - size.width - 8, at.right - size.width))}px`;
    popup.style.top = `${at.bottom + size.height + 8 > innerHeight ? Math.max(8, at.top - size.height - 4) : at.bottom + 4}px`;
  }

  function openMenu(anchor, label, entries) {
    shutPopup(false);
    menu.setAttribute('aria-label', label);
    let into = menu;
    for (const entry of entries) {
      if (entry === null) {
        menu.append(make('hr'));
        into = menu;
        continue;
      }
      // A title heads a group of the entries after it, which is named by
      // it; the words themselves are only for the eye.
      if (typeof entry === 'string') {
        into = make('div', 'menu-group');
        into.setAttribute('role', 'group');
        into.setAttribute('aria-label', entry);
        const words = make('p', 'menu-title', entry);
        words.setAttribute('aria-hidden', 'true');
        into.append(words);
        menu.append(into);
        continue;
      }
      const item = button('menu-item', entry.label);
      const chosen = typeof entry.checked === 'boolean';
      item.setAttribute('role', chosen ? 'menuitemradio' : 'menuitem');
      if (chosen) item.setAttribute('aria-checked', String(entry.checked));
      item.disabled = entry.disabled === true;
      item.tabIndex = -1;
      item.addEventListener('click', () => {
        shutPopup(true);
        entry.act();
      });
      into.append(item);
    }
    hang(menu, anchor);
    const first = menu.querySelector('button:not(:disabled)');
    if (first) first.focus();
  }

  menu.addEventListener('keydown', (e) => {
    const entries = [...menu.querySelectorAll('button:not(:disabled)')];
    const at = entries.indexOf(document.activeElement);
    let to = null;
    if (e.key === 'ArrowDown') to = entries[(at + 1) % entries.length];
    else if (e.key === 'ArrowUp') to = entries[(at - 1 + entries.length) % entries.length];
    else if (e.key === 'Home') to = entries[0];
    else if (e.key === 'End') to = entries[entries.length - 1];
    else if (e.key === 'Escape' || e.key === 'Tab') {
      // Not also an Escape for the card or the sheet under the menu.
      e.stopPropagation();
      if (e.key === 'Escape') e.preventDefault();
      shutPopup(true);
      return;
    } else return;
    e.preventDefault();
    if (to) to.focus();
  });
  document.addEventListener('pointerdown', (e) => {
    if (hungFrom !== null && e.target instanceof Node && !menu.contains(e.target) && !popover.contains(e.target) && !hungFrom.contains(e.target)) shutPopup(false);
  }, true);

  // The box a value is typed in. What was understood of it, or why it was
  // not, is said under the box as it is typed, and nothing is used until
  // it is applied.
  function typed(anchor, spec, choose) {
    if (hungFrom === anchor) {
      shutPopup(true);
      return;
    }
    shutPopup(false);
    made += 1;
    const { custom } = spec;
    popover.setAttribute('aria-label', String(custom.title || custom.label));
    const label = make('label', '', String(custom.title || custom.label));
    const field = make('input');
    field.type = 'text';
    field.id = `layers-typed-${made}`;
    field.maxLength = 32;
    field.autocomplete = 'off';
    field.spellcheck = false;
    if (custom.placeholder) field.placeholder = String(custom.placeholder);
    label.htmlFor = field.id;
    const said = make('p', 'said', String(custom.hint || ''));
    said.id = `layers-said-${made}`;
    said.setAttribute('role', 'status');
    field.setAttribute('aria-describedby', said.id);
    const apply = button('', 'Apply');
    const cancel = button('', 'Cancel');
    const ends = make('div', 'popover-ends');
    ends.append(apply, cancel);
    popover.append(label, field, said, ends);
    const read = () => (field.value.trim() === '' ? { value: null, said: String(custom.hint || ''), problem: true, empty: true } : call(custom.settle, field.value) || { value: null, said: '', problem: true });
    const tell = () => {
      const got = read();
      said.textContent = got.said;
      said.classList.toggle('problem', got.problem && !got.empty);
      apply.disabled = got.problem;
    };
    const take = () => {
      const got = read();
      if (got.problem) {
        tell();
        return;
      }
      shutPopup(true);
      choose(got.value);
    };
    field.addEventListener('input', tell);
    field.addEventListener('keydown', (e) => {
      if (e.key !== 'Enter') return;
      e.preventDefault();
      take();
    });
    apply.addEventListener('click', take);
    cancel.addEventListener('click', () => shutPopup(true));
    tell();
    hang(popover, anchor);
    field.focus();
  }
  popover.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape') return;
    // Not also an Escape for the card or the sheet under the box.
    e.stopPropagation();
    e.preventDefault();
    shutPopup(true);
  });

  const DENSITIES = [['Compact', 'compact'], ['Comfortable', 'comfortable'], ['Spacious', 'spacious']];
  function panelMenu() {
    const entries = [];
    if (mode() !== 'sheeted') {
      entries.push('Width');
      const width = widthNow();
      for (const [label, px] of WIDTHS) entries.push({ label, checked: width === Math.min(widest(), px), act: () => setWidth(px, true) });
      entries.push(null);
    }
    if (settings) {
      entries.push('Density');
      const now = settings.get('look').density;
      for (const [label, density] of DENSITIES) {
        entries.push({ label, checked: now === density, act: () => settings.set('look', { ...settings.get('look'), density }) });
      }
      entries.push(null);
    }
    entries.push({ label: 'Expand all', act: () => foldAll(false) }, { label: 'Collapse all', act: () => foldAll(true) });
    entries.push(null, { label: 'Reset layers to defaults', disabled: !changed(), act: reset });
    return entries;
  }

  function paint() {
    el.panel.classList.toggle('open', open);
    el.body.hidden = !open;
    el.toggle.setAttribute('aria-expanded', String(open));
    paintShell();
  }

  // Opens or shuts the panel: to its rail beside the map, and away
  // altogether on a small screen. What was chosen beside the map is kept.
  function setOpen(on) {
    if (open === on) return;
    shutPopup(false);
    const within = el.panel.contains(document.activeElement);
    open = on;
    keep();
    paint();
    // Focus left in what has just been hidden is focus lost, and a sheet
    // that has come up over the map is where the next thing to do is.
    if ((within || (on && compact.matches)) && !el.body.contains(document.activeElement)) (on ? shutButton : el.toggle).focus();
  }

  el.toggle.addEventListener('click', () => setOpen(!open));
  shutButton.addEventListener('click', () => setOpen(false));
  menuButton.addEventListener('click', () => {
    if (hungFrom === menuButton) shutPopup(true);
    else openMenu(menuButton, 'Panel options', panelMenu());
  });
  resetButton.addEventListener('click', () => {
    reset();
    box.focus();
  });

  // --- the list's own pointer and keys ----------------------------------------------

  tree.addEventListener('click', (e) => {
    const node = nodeAt(e.target);
    if (!node || !node.dom || !(e.target instanceof Element)) return;
    const { dom } = node;
    if (!dom.row.contains(e.target)) return;
    moveStop(node);
    if (e.target.closest('.twist')) twist(node);
    else if (e.target.closest('.check')) {
      if (dom.check.getAttribute('aria-disabled') !== 'true') toggle(node);
    } else if (e.target.closest('.only')) onlyThis(node);
    else if (e.target.closest('.more')) {
      if (hungFrom === dom.more) shutPopup(true);
      else openMenu(dom.more, `More for ${dom.was.said}`, entriesFor(node));
    } else if (expandable(node)) twist(node);
    else if (dom.check.getAttribute('aria-disabled') !== 'true') toggle(node);
  });
  tree.addEventListener('focusin', (e) => {
    const node = nodeAt(e.target);
    if (node && e.target instanceof Element && e.target.closest('.row')) moveStop(node);
  });

  // The lines there are to move among: every one that is showing.
  const stops = () => [...tree.querySelectorAll('.check')].filter((check) => check.offsetParent !== null);
  let typedSoFar = '';
  let typedAt = 0;

  tree.addEventListener('keydown', (e) => {
    if (e.ctrlKey || e.metaKey || e.altKey || e.isComposing || !(e.target instanceof Element) || !e.target.closest('.row')) return;
    const node = nodeAt(e.target);
    if (!node || !node.dom) return;
    const all = stops();
    const at = all.indexOf(node.dom.check);
    const to = (check) => {
      const next = check ? nodeAt(check) : null;
      if (next) focusOn(next);
    };
    if (e.key === 'ArrowDown') to(all[at + 1]);
    else if (e.key === 'ArrowUp') to(all[at - 1]);
    else if (e.key === 'Home') to(all[0]);
    else if (e.key === 'End') to(all[all.length - 1]);
    else if (e.key === 'ArrowRight') {
      if (!expandable(node)) return;
      if (!isOpen(node)) twist(node, true);
      else to(all[at + 1]);
    } else if (e.key === 'ArrowLeft') {
      if (expandable(node) && isOpen(node)) twist(node, false);
      else {
        const up = node.dom.li.parentElement.closest('.node');
        if (up) focusOn(nodes.get(up));
      }
    } else if (e.key === 'Enter') {
      // On the line's own checkbox it does what the line is for; on
      // another of its buttons it presses that.
      if (!e.target.closest('.check')) return;
      activate(node);
    } else if (e.key === 'ContextMenu' || (e.key === 'F10' && e.shiftKey)) {
      if (node.dom.more.hidden) return;
      openMenu(node.dom.more, `More for ${node.dom.was.said}`, entriesFor(node));
    } else if (/^[\p{L}\p{N}]$/u.test(e.key)) {
      // A letter goes to the next line that begins with what has been
      // typed. One that begins no line is left to be the page's
      // single-key shortcut it would be anywhere else.
      const now = Date.now();
      const letter = e.key.toLowerCase();
      // The same letter again is the next line that begins with it.
      const again = typedSoFar !== '' && [...typedSoFar].every((other) => other === letter);
      typedSoFar = now - typedAt > TYPEAHEAD_MS || again ? letter : typedSoFar + letter;
      typedAt = now;
      const begins = (check) => (nodeAt(check).dom.was.said || '').toLowerCase().startsWith(typedSoFar);
      const from = typedSoFar.length === 1 ? at + 1 : at;
      const found = [...all.slice(from), ...all.slice(0, from)].find(begins);
      if (!found) {
        typedSoFar = '';
        return;
      }
      to(found);
    } else return;
    e.preventDefault();
    e.stopPropagation();
  });

  box.addEventListener('input', () => {
    query = box.value.trim().toLowerCase();
    clear.hidden = box.value === '';
    render();
    scroll.scrollTop = 0;
  });
  const unsearch = () => {
    box.value = '';
    query = '';
    clear.hidden = true;
    render();
    box.focus();
  };
  clear.addEventListener('click', unsearch);
  box.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && box.value !== '') {
      // Not also an Escape for the card or the sheet.
      e.stopPropagation();
      e.preventDefault();
      unsearch();
    } else if (e.key === 'ArrowDown') {
      const [first] = stops();
      if (!first) return;
      e.preventDefault();
      focusOn(nodeAt(first));
    }
  });

  // The panel's edge is dragged, or moved by the arrow keys while it has
  // the focus; the menu's three widths are the way that needs neither. The
  // panel is on the right, so its edge moving left is the panel growing.
  // However fast the pointer moves, the panel is sized once a frame, and a
  // drag ends however the pointer is lost: let go, taken by the browser,
  // or with the window no longer the one in front.
  let dragging = null;
  sizer.addEventListener('pointerdown', (e) => {
    if (e.button !== 0) return;
    e.preventDefault();
    sizer.focus();
    sizer.setPointerCapture(e.pointerId);
    dragging = { id: e.pointerId, right: el.panel.getBoundingClientRect().right, x: e.clientX, frame: 0 };
    el.panel.classList.add('sizing');
  });
  sizer.addEventListener('pointermove', (e) => {
    if (!dragging || e.pointerId !== dragging.id) return;
    dragging.x = e.clientX;
    if (dragging.frame !== 0) return;
    dragging.frame = requestAnimationFrame(() => {
      if (!dragging) return;
      dragging.frame = 0;
      setWidth(dragging.right - dragging.x, false);
    });
  });
  const dropped = (e) => {
    if (!dragging || (e && e.pointerId !== undefined && e.pointerId !== dragging.id)) return;
    cancelAnimationFrame(dragging.frame);
    setWidth(dragging.right - dragging.x, false);
    dragging = null;
    el.panel.classList.remove('sizing');
    keepShell();
  };
  for (const end of ['pointerup', 'pointercancel', 'lostpointercapture']) sizer.addEventListener(end, dropped);
  sizer.addEventListener('dblclick', () => setWidth(null, true));
  sizer.addEventListener('keydown', (e) => {
    const width = widthNow();
    let to = null;
    if (e.key === 'ArrowLeft') to = width + STEP;
    else if (e.key === 'ArrowRight') to = width - STEP;
    else if (e.key === 'Home') to = MIN_WIDTH;
    else if (e.key === 'End') to = widest();
    else if (e.key === 'Enter') {
      e.preventDefault();
      setOpen(false);
      return;
    } else return;
    e.preventDefault();
    setWidth(to, true);
  });

  // The sheet's handle: pressed, it goes to the next height; dragged, the
  // sheet follows the finger and settles at the nearest of the three, or
  // shuts if it was let go below the shortest.
  const taller = (by) => {
    const at = DETENTS.indexOf(shell.detent) + by;
    if (at < 0) setOpen(false);
    else setDetent(DETENTS[Math.min(DETENTS.length - 1, at)]);
  };
  let pulling = null;
  let pulledAt = 0;
  grab.addEventListener('pointerdown', (e) => {
    if (e.button !== 0) return;
    grab.setPointerCapture(e.pointerId);
    pulling = { id: e.pointerId, y: e.clientY, now: e.clientY, from: el.body.getBoundingClientRect().height, moved: false, frame: 0 };
  });
  grab.addEventListener('pointermove', (e) => {
    if (!pulling || e.pointerId !== pulling.id) return;
    if (!pulling.moved && Math.abs(e.clientY - pulling.y) < 6) return;
    pulling.moved = true;
    pulling.now = e.clientY;
    if (pulling.frame) return;
    pulling.frame = requestAnimationFrame(() => {
      if (!pulling) return;
      pulling.frame = 0;
      el.panel.classList.add('sizing');
      el.body.style.height = `${Math.max(0, pulling.from + pulling.y - pulling.now)}px`;
    });
  });
  const released = (e) => {
    if (!pulling || (e && e.pointerId !== undefined && e.pointerId !== pulling.id)) return;
    const { moved, from, y, now, frame } = pulling;
    pulling = null;
    cancelAnimationFrame(frame);
    if (!moved) return;
    // Where the last move left it, which the frame may not have drawn.
    const height = Math.max(0, from + y - now);
    el.body.style.height = '';
    el.panel.classList.remove('sizing');
    const full = stage.clientHeight;
    const heights = { peek: grab.offsetHeight + head.offsetHeight, half: full / 2, full };
    let best = { name: null, far: height < heights.peek / 2 ? 0 : Infinity };
    for (const name of DETENTS) {
      const far = Math.abs(heights[name] - height);
      if (far < best.far) best = { name, far };
    }
    if (best.name === null) {
      paintShell();
      setOpen(false);
    } else {
      setDetent(best.name);
    }
    pulledAt = Date.now();
  };
  for (const end of ['pointerup', 'pointercancel', 'lostpointercapture']) grab.addEventListener(end, released);
  addEventListener('blur', () => {
    dropped();
    released();
  });
  grab.addEventListener('click', () => {
    // The click that ends a drag is not a press.
    if (Date.now() - pulledAt < 400) return;
    setDetent(DETENTS[(DETENTS.indexOf(shell.detent) + 1) % DETENTS.length]);
  });
  grab.addEventListener('keydown', (e) => {
    if (e.key !== 'ArrowUp' && e.key !== 'ArrowDown') return;
    e.preventDefault();
    taller(e.key === 'ArrowUp' ? 1 : -1);
  });

  // A window made small with the panel open beside the map would find it
  // lying over the map as a sheet, so it is shut on the way in, and put
  // back as it was kept on the way out.
  compact.addEventListener('change', () => {
    open = !compact.matches && (typeof view.open === 'boolean' ? view.open : true);
    paint();
  });
  roomy.addEventListener('change', paint);
  // The most the panel may take goes with the window.
  const remeasured = () => {
    measured = null;
    paintShell();
  };
  addEventListener('resize', remeasured);
  document.addEventListener('mcmap:settings', (e) => {
    if (e.detail && e.detail.sections.includes('look')) remeasured();
  });

  paint();
  render();
  app.layers.recall = recall;
  app.layers.retain = retain;
  app.layers.states = states;
  app.layers.items = items;
  app.layers.adopt = adopt;
  app.layers.reset = reset;
  app.layers.facet = (group, name, options) => facetOf(String(group), String(name), options).handle;
  app.layers.register = register;
  document.dispatchEvent(new CustomEvent('mcmap:layers'));
})();
