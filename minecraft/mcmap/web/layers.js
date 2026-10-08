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

  // The sections there is a place for, in the order they are shown. A
  // section appears once it has a row. Any other group is shown after
  // these, in the order it was first used.
  const GROUPS = [
    ['live', 'Live'],
    ['markers', 'Markers'],
    ['structures', 'Structures'],
    ['biomes', 'Biomes'],
    ['overlays', 'Overlays'],
  ];

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

  const groups = new Map();
  let made = 0;

  const make = (tag, className, text) => {
    const node = document.createElement(tag);
    if (className) node.className = className;
    // Always as text: a label or a note may carry a name from the world,
    // which a player chose.
    if (text !== undefined) node.textContent = text;
    return node;
  };

  function paint() {
    let any = false;
    for (const g of groups.values()) {
      g.section.hidden = g.rows.length === 0;
      any = any || g.rows.length > 0;
    }
    el.panel.hidden = !any;
    el.panel.classList.toggle('open', open);
    el.body.hidden = !open;
    el.toggle.setAttribute('aria-expanded', String(open));
  }

  function fold(g) {
    const shut = folded.has(g.id);
    g.list.hidden = shut;
    g.fold.setAttribute('aria-expanded', String(!shut));
  }

  function set(row, on) {
    if (row.on === on) return false;
    row.on = on;
    row.box.checked = on;
    for (const fn of row.listeners) {
      // One layer failing to redraw must not leave the rest unswitched.
      try { fn(on); } catch (err) { console.error(err); }
    }
    return true;
  }

  function setAll(g, on) {
    const changes = {};
    for (const row of [...g.rows]) {
      if (row.available && set(row, on)) changes[`${g.id}/${row.id}`] = on;
    }
    if (Object.keys(changes).length > 0) remember(changes);
  }

  // Every row's switch as it stands, by the key its choice is kept under.
  function states() {
    const out = {};
    for (const g of groups.values()) for (const row of g.rows) out[`${g.id}/${row.id}`] = row.on;
    return out;
  }

  // Brings every row in line with the choices as they are now kept, for
  // when something other than a switch has changed them, as a saved view
  // does. Every row is switched before any layer is told, and a layer
  // that listens with one function for all its rows is told once, so the
  // map goes from the one picture to the other with nothing drawn between.
  // wanted is the keys a view named, and what comes back is those of them
  // there is no row for here: a layer that has gone, or one this server
  // does not offer. A row that is only greyed out for now is switched all
  // the same, for when it is not.
  function adopt(wanted = []) {
    const choices = kept();
    const told = new Map();
    const found = new Set();
    for (const g of groups.values()) {
      for (const row of g.rows) {
        const key = `${g.id}/${row.id}`;
        found.add(key);
        const on = typeof choices[key] === 'boolean' ? choices[key] : row.on;
        if (row.on === on) continue;
        row.on = on;
        row.box.checked = on;
        for (const fn of row.listeners) told.set(fn, on);
      }
    }
    for (const [fn, on] of told) {
      try { fn(on); } catch (err) { console.error(err); }
    }
    return wanted.filter((key) => !found.has(key));
  }

  function groupOf(id, label) {
    let g = groups.get(id);
    if (g) return g;
    const known = GROUPS.findIndex(([name]) => name === id);
    const title = known >= 0 ? GROUPS[known][1] : String(label || id);
    made += 1;
    const section = make('section', 'layer-group');
    const head = make('div', 'layer-group-head');
    const list = make('ul');
    list.id = `layers-list-${made}`;
    const foldButton = make('button', 'fold', title);
    foldButton.type = 'button';
    foldButton.setAttribute('aria-controls', list.id);
    const all = make('button', 'mini', 'All');
    all.type = 'button';
    all.setAttribute('aria-label', `All: show every ${title} layer`);
    const none = make('button', 'mini', 'None');
    none.type = 'button';
    none.setAttribute('aria-label', `None: hide every ${title} layer`);
    head.append(foldButton, all, none);
    section.append(head, list);

    g = { id, rank: known >= 0 ? known : GROUPS.length + made, section, list, fold: foldButton, rows: [] };
    foldButton.addEventListener('click', () => {
      if (!folded.delete(id)) folded.add(id);
      keep();
      fold(g);
    });
    all.addEventListener('click', () => setAll(g, true));
    none.addEventListener('click', () => setAll(g, false));
    fold(g);

    const next = [...groups.values()].filter((other) => other.rank > g.rank).sort((a, b) => a.rank - b.rank)[0];
    el.body.insertBefore(section, next ? next.section : null);
    groups.set(id, g);
    return g;
  }

  function drop(g, row) {
    const at = g.rows.indexOf(row);
    if (at < 0) return;
    g.rows.splice(at, 1);
    row.item.remove();
    row.listeners.length = 0;
    paint();
  }

  // Adds a layer's row and returns the handle its script keeps. group is
  // one of the sections above, or a new one, titled by groupLabel. id is
  // unique within the group and is what the viewer's choice is saved
  // under; registering one again replaces the row. swatch is the class of
  // the colour key drawn beside the label, for a layer that has one, and
  // picture an element the script made to stand in the key's place for as
  // long as it is showing.
  function register({ group, id, label, enabled = true, order, groupLabel, swatch, picture } = {}) {
    if (typeof group !== 'string' || !group || typeof id !== 'string' || !id) {
      throw new TypeError('a layer needs a group and an id');
    }
    const g = groupOf(group, groupLabel);
    const again = g.rows.find((other) => other.id === id);
    if (again) drop(g, again);

    made += 1;
    const item = make('li', 'layer');
    const name = make('label');
    const box = make('input');
    box.type = 'checkbox';
    const note = make('span', 'note');
    note.id = `layers-note-${made}`;
    box.setAttribute('aria-describedby', note.id);
    const count = make('span', 'count');
    const body = make('div', 'body');
    body.hidden = true;
    name.append(box);
    if (picture instanceof Node) name.append(picture);
    if (typeof swatch === 'string' && swatch) name.append(make('i', swatch));
    const title = make('span', 'name', String(label ?? id));
    name.append(title);
    item.append(name, count, note, body);

    const row = {
      id,
      order: Number.isFinite(order) ? order : Infinity,
      seq: made,
      on: choice(group, id, Boolean(enabled)),
      available: true,
      listeners: [],
      item,
      box,
    };
    box.checked = row.on;
    box.addEventListener('change', () => {
      if (set(row, box.checked)) remember({ [`${group}/${id}`]: row.on });
    });

    // Lower orders first; rows given none go last, as they were registered.
    const next = g.rows.find((other) => row.order < other.order);
    g.rows.splice(next ? g.rows.indexOf(next) : g.rows.length, 0, row);
    g.list.insertBefore(item, next ? next.item : null);
    paint();

    // Counts arrive once a second from the live layer, so a value that has
    // not changed is not written again.
    const put = (node, value) => { if (node.textContent !== value) node.textContent = value; };
    return {
      get enabled() { return row.on; },
      setCount(n) { put(count, Number.isFinite(n) ? n.toLocaleString('en-US') : ''); },
      setNote(text) { put(note, text ? String(text) : ''); },
      // For a layer whose name is the world's and may arrive late.
      setLabel(text) { if (text) put(title, String(text)); },
      onToggle(fn) { if (typeof fn === 'function') row.listeners.push(fn); },
      // Switches the row as the viewer would have, for a script that has
      // been asked for what the layer shows: the choice is kept, and the
      // listeners are told.
      setEnabled(on) {
        if (row.available && set(row, Boolean(on))) remember({ [`${group}/${id}`]: row.on });
      },
      // A layer's own controls, such as a legend, shown under its row.
      // The node is the script's to fill; null takes it away.
      setBody(node) {
        body.replaceChildren(...(node instanceof Node ? [node] : []));
        body.hidden = !(node instanceof Node);
      },
      setAvailable(available) {
        row.available = Boolean(available);
        box.disabled = !row.available;
        item.classList.toggle('unavailable', !row.available);
      },
      remove() { drop(g, row); },
    };
  }

  el.toggle.addEventListener('click', () => {
    open = !open;
    keep();
    paint();
  });

  // A window made small with the panel open beside the map would find it
  // lying over the map as a sheet, so it is shut on the way in, and put
  // back as it was kept on the way out.
  compact.addEventListener('change', () => {
    open = !compact.matches && (typeof view.open === 'boolean' ? view.open : true);
    paint();
  });

  paint();
  app.layers.recall = recall;
  app.layers.retain = retain;
  app.layers.states = states;
  app.layers.adopt = adopt;
  app.layers.register = register;
  document.dispatchEvent(new CustomEvent('mcmap:layers'));
})();
