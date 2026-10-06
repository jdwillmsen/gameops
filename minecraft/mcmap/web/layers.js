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

  const CHOICES_KEY = 'mcmap.layers';
  const PANEL_KEY = 'mcmap.panel';

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

  // Where each layer's script kept its filters before there was a panel,
  // as { <id>: boolean }. A row with no choice saved here takes the one
  // saved there, so nobody's filters reset. The structures also had one
  // switch for the lot, and a viewer who had that off gets the layers it
  // hid, and the one added to them since, switched off.
  const LEGACY = {
    live: { key: 'mcmap.live', master: [] },
    markers: { key: 'mcmap.markers', master: [] },
    structures: { key: 'mcmap.structures', master: ['recorded', 'predicted', 'candidate'] },
  };

  function read(key) {
    try {
      const value = JSON.parse(localStorage.getItem(key) || '{}');
      return value && typeof value === 'object' && !Array.isArray(value) ? value : {};
    } catch {
      return {}; // a browser that refuses storage still gets the defaults
    }
  }

  function write(key, value) {
    try { localStorage.setItem(key, JSON.stringify(value)); } catch { /* not kept, still applied */ }
  }

  const flag = (from, key) => (Object.hasOwn(from, key) && typeof from[key] === 'boolean' ? from[key] : null);

  // Choices are kept flat, as "<group>/<id>", so that no id a script picks
  // can be mistaken for a property every object has.
  const choices = read(CHOICES_KEY);
  const legacy = new Map();

  function remember(group, id, on) {
    choices[`${group}/${id}`] = on;
    write(CHOICES_KEY, choices);
  }

  function choice(group, id, fallback) {
    const kept = flag(choices, `${group}/${id}`);
    if (kept !== null) return kept;
    if (!Object.hasOwn(LEGACY, group)) return fallback;
    if (!legacy.has(group)) legacy.set(group, read(LEGACY[group].key));
    const old = legacy.get(group);
    const was = flag(old, 'on') === false && LEGACY[group].master.includes(id) ? false : flag(old, id);
    if (was === null) return fallback;
    // Kept under the new key from here on, so the old one need never be
    // read for this row again.
    remember(group, id, was);
    return was;
  }

  const view = read(PANEL_KEY);
  const folded = new Set(Array.isArray(view.folded) ? view.folded.filter((id) => typeof id === 'string') : []);
  // Open to begin with where there is room for it beside the map.
  let open = typeof view.open === 'boolean' ? view.open : matchMedia('(min-width: 641px)').matches;

  const keep = () => write(PANEL_KEY, { open, folded: [...folded] });

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
    let changed = false;
    for (const row of [...g.rows]) {
      if (!row.available) continue;
      if (set(row, on)) {
        choices[`${g.id}/${row.id}`] = on;
        changed = true;
      }
    }
    if (changed) write(CHOICES_KEY, choices);
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
      if (set(row, box.checked)) remember(group, id, row.on);
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
        if (row.available && set(row, Boolean(on))) remember(group, id, row.on);
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

  paint();
  app.layers.register = register;
  document.dispatchEvent(new CustomEvent('mcmap:layers'));
})();
