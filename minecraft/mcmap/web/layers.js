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

  // A drawing, not a character: a character would be read out as part of
  // whatever it is on. Each is one path on a square of sixteen.
  const GLYPHS = {
    layers: 'M8 2l6 3-6 3-6-3zM2 8l6 3 6-3M2 11l6 3 6-3',
    more: 'M3.5 8h.01M8 8h.01M12.5 8h.01',
    shut: 'M6 3l5 5-5 5',
    close: 'M4 4l8 8M12 4l-8 8',
    live: 'M2 8h3l2-4 3 8 2-4h2',
    markers: 'M8 14s4-4.2 4-7.5a4 4 0 0 0-8 0C4 9.8 8 14 8 14zM8 8a1.5 1.5 0 1 0 0-3 1.5 1.5 0 0 0 0 3z',
    structures: 'M3 14V7l5-4 5 4v7zM6.5 14v-4h3v4',
    biomes: 'M8 14V9M8 9C5 9 3.5 7 3.5 5S5.5 2 8 2s4.5 1 4.5 3S11 9 8 9z',
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
  const scroll = make('div', 'panel-scroll');
  scroll.id = 'layers-list';
  el.body.append(grab, head, scroll);
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
  const usual = () => parseInt(getComputedStyle(el.panel).getPropertyValue('--panel-default'), 10) || 340;
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
    // What the panel takes from the map's width, for what is centred over
    // the map and not over the stage.
    stage.style.setProperty('--dock', now === 'sheeted' ? '0px' : `${el.panel.offsetWidth}px`);
    sizer.hidden = now === 'sheeted' || !open;
    sizer.setAttribute('aria-valuemin', String(MIN_WIDTH));
    sizer.setAttribute('aria-valuemax', String(widest()));
    sizer.setAttribute('aria-valuenow', String(width));
    sizer.setAttribute('aria-valuetext', `${width} pixels`);
    rail.hidden = now === 'sheeted' || open;
    const small = now === 'sheeted';
    const said = small ? 'Close the layers' : 'Collapse the layer panel to a rail (L)';
    shutButton.setAttribute('aria-label', said);
    shutButton.title = said;
    const height = { peek: 'short', half: 'half height', full: 'full height' }[shell.detent];
    grab.setAttribute('aria-label', `Layers sheet, ${height}. Press to change its height, or use the arrow keys.`);
  }

  // The sections on the rail: a button each, which opens the panel at
  // that section, marked where the section has something hidden.
  function paintRail() {
    const wanted = [...groups.values()].filter((g) => g.rows.length > 0).sort((a, b) => a.rank - b.rank);
    for (const g of wanted) {
      if (!g.railed) {
        g.railed = button('icon', g.title, g.id);
        g.railed.append(make('i', 'badge'));
        g.railed.addEventListener('click', () => {
          setOpen(true);
          g.section.scrollIntoView({ block: 'start' });
          g.fold.focus();
        });
      }
      const on = g.rows.filter((row) => row.on).length;
      const state = on === g.rows.length ? 'all' : on === 0 ? 'none' : 'some';
      const said = `${g.title}: ${state === 'all' ? 'all shown' : state === 'none' ? 'all hidden' : `${on} of ${g.rows.length} shown`}`;
      if (g.railed.dataset.state !== state) g.railed.dataset.state = state;
      if (g.railed.title !== said) {
        g.railed.title = said;
        g.railed.setAttribute('aria-label', said);
      }
    }
    const order = wanted.map((g) => g.railed);
    if (order.length !== rail.children.length || order.some((node, i) => rail.children[i] !== node)) rail.replaceChildren(...order);
  }

  // --- a menu -------------------------------------------------------------------
  //
  // One menu, opened under whichever button asked for it. entries are
  // { label, checked, disabled, act } or null for a rule between them;
  // checked makes one a choice among several. It shuts on a choice, on
  // Escape, and on a press outside it, and gives the focus back.
  const menu = make('div', 'panel-popup menu');
  menu.setAttribute('role', 'menu');
  menu.hidden = true;
  el.panel.append(menu);
  let menuFor = null;

  function shutMenu(refocus) {
    if (menuFor === null) return;
    const anchor = menuFor;
    menuFor = null;
    menu.hidden = true;
    menu.replaceChildren();
    anchor.setAttribute('aria-expanded', 'false');
    if (refocus) anchor.focus();
  }

  function openMenu(anchor, label, entries) {
    shutMenu(false);
    menuFor = anchor;
    menu.setAttribute('aria-label', label);
    for (const entry of entries) {
      if (entry === null) {
        menu.append(make('hr'));
        continue;
      }
      if (typeof entry === 'string') {
        menu.append(make('p', 'menu-title', entry));
        continue;
      }
      const item = button('menu-item', entry.label);
      const choice = typeof entry.checked === 'boolean';
      item.setAttribute('role', choice ? 'menuitemradio' : 'menuitem');
      if (choice) item.setAttribute('aria-checked', String(entry.checked));
      item.disabled = entry.disabled === true;
      item.tabIndex = -1;
      item.addEventListener('click', () => {
        shutMenu(true);
        entry.act();
      });
      menu.append(item);
    }
    menu.hidden = false;
    anchor.setAttribute('aria-expanded', 'true');
    // Under the button and inside the window, whichever edge it is near.
    const at = anchor.getBoundingClientRect();
    const box = menu.getBoundingClientRect();
    menu.style.left = `${Math.max(8, Math.min(innerWidth - box.width - 8, at.right - box.width))}px`;
    menu.style.top = `${at.bottom + box.height + 8 > innerHeight ? Math.max(8, at.top - box.height - 4) : at.bottom + 4}px`;
    const first = menu.querySelector('button:not(:disabled)');
    if (first) first.focus();
  }

  menu.addEventListener('keydown', (e) => {
    const items = [...menu.querySelectorAll('button:not(:disabled)')];
    const at = items.indexOf(document.activeElement);
    let to = null;
    if (e.key === 'ArrowDown') to = items[(at + 1) % items.length];
    else if (e.key === 'ArrowUp') to = items[(at - 1 + items.length) % items.length];
    else if (e.key === 'Home') to = items[0];
    else if (e.key === 'End') to = items[items.length - 1];
    else if (e.key === 'Escape' || e.key === 'Tab') {
      // Not also an Escape for the card or the sheet under the menu.
      e.stopPropagation();
      if (e.key === 'Escape') e.preventDefault();
      shutMenu(true);
      return;
    } else return;
    e.preventDefault();
    if (to) to.focus();
  });
  document.addEventListener('pointerdown', (e) => {
    if (menuFor !== null && e.target instanceof Node && !menu.contains(e.target) && !menuFor.contains(e.target)) shutMenu(false);
  }, true);

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
        entries.push({ label, checked: now === density, act: () => {
          settings.set('look', { ...settings.get('look'), density });
          // The usual width goes with the density.
          paintShell();
        } });
      }
      entries.push(null);
    }
    entries.push({ label: 'Expand all', act: () => foldAll(false) }, { label: 'Collapse all', act: () => foldAll(true) });
    return entries;
  }

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
    paintShell();
    paintRail();
  }

  // Opens or shuts the panel: to its rail beside the map, and away
  // altogether on a small screen. What was chosen beside the map is kept.
  function setOpen(on) {
    if (open === on) return;
    shutMenu(false);
    const within = el.panel.contains(document.activeElement);
    open = on;
    keep();
    paint();
    // Focus left in what has just been hidden is focus lost, and a sheet
    // that has come up over the map is where the next thing to do is.
    if ((within || (on && compact.matches)) && !el.body.contains(document.activeElement)) (on ? shutButton : el.toggle).focus();
  }

  function foldAll(shut) {
    for (const g of groups.values()) {
      if (shut) folded.add(g.id); else folded.delete(g.id);
      fold(g);
    }
    keep();
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
    paintRail();
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
        // With no choice kept for it, a row is as its layer first had it.
        const on = typeof choices[key] === 'boolean' ? choices[key] : row.first;
        if (row.on === on) continue;
        row.on = on;
        row.box.checked = on;
        for (const fn of row.listeners) told.set(fn, on);
      }
    }
    for (const [fn, on] of told) {
      try { fn(on); } catch (err) { console.error(err); }
    }
    paintRail();
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

    g = { id, title, rank: known >= 0 ? known : GROUPS.length + made, section, list, fold: foldButton, rows: [] };
    foldButton.addEventListener('click', () => {
      if (!folded.delete(id)) folded.add(id);
      keep();
      fold(g);
    });
    all.addEventListener('click', () => setAll(g, true));
    none.addEventListener('click', () => setAll(g, false));
    fold(g);

    const next = [...groups.values()].filter((other) => other.rank > g.rank).sort((a, b) => a.rank - b.rank)[0];
    scroll.insertBefore(section, next ? next.section : null);
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
      first: Boolean(enabled),
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

  el.toggle.addEventListener('click', () => setOpen(!open));
  shutButton.addEventListener('click', () => setOpen(false));
  menuButton.addEventListener('click', () => {
    if (menuFor === menuButton) shutMenu(true);
    else openMenu(menuButton, 'Panel options', panelMenu());
  });

  // The panel's edge is dragged, or moved by the arrow keys while it has
  // the focus; the menu's three widths are the way that needs neither. The
  // panel is on the right, so its edge moving left is the panel growing.
  let dragging = null;
  sizer.addEventListener('pointerdown', (e) => {
    if (e.button !== 0) return;
    e.preventDefault();
    sizer.focus();
    sizer.setPointerCapture(e.pointerId);
    dragging = { id: e.pointerId, right: el.panel.getBoundingClientRect().right };
    el.panel.classList.add('sizing');
  });
  sizer.addEventListener('pointermove', (e) => {
    if (dragging && e.pointerId === dragging.id) setWidth(dragging.right - e.clientX, false);
  });
  const dropped = (e) => {
    if (!dragging || e.pointerId !== dragging.id) return;
    dragging = null;
    el.panel.classList.remove('sizing');
    keepShell();
  };
  sizer.addEventListener('pointerup', dropped);
  sizer.addEventListener('pointercancel', dropped);
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
  grab.addEventListener('pointerdown', (e) => {
    if (e.button !== 0) return;
    grab.setPointerCapture(e.pointerId);
    pulling = { id: e.pointerId, y: e.clientY, from: el.body.getBoundingClientRect().height, moved: false };
  });
  grab.addEventListener('pointermove', (e) => {
    if (!pulling || e.pointerId !== pulling.id) return;
    if (!pulling.moved && Math.abs(e.clientY - pulling.y) < 6) return;
    pulling.moved = true;
    el.panel.classList.add('sizing');
    el.body.style.height = `${Math.max(0, pulling.from + pulling.y - e.clientY)}px`;
  });
  const released = (e) => {
    if (!pulling || e.pointerId !== pulling.id) return;
    const { moved } = pulling;
    pulling = null;
    if (!moved) return;
    const height = el.body.getBoundingClientRect().height;
    el.body.style.height = '';
    el.panel.classList.remove('sizing');
    const full = stage.clientHeight;
    const stops = { peek: grab.offsetHeight + head.offsetHeight, half: full / 2, full };
    let best = { name: null, far: height < stops.peek / 2 ? 0 : Infinity };
    for (const name of DETENTS) {
      const far = Math.abs(stops[name] - height);
      if (far < best.far) best = { name, far };
    }
    if (best.name === null) {
      paintShell();
      setOpen(false);
    } else {
      setDetent(best.name);
    }
  };
  grab.addEventListener('pointerup', released);
  grab.addEventListener('pointercancel', released);
  grab.addEventListener('click', () => {
    // The click that ends a drag is not a press.
    if (el.body.style.height !== '') return;
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
  addEventListener('resize', paintShell);
  document.addEventListener('mcmap:settings', (e) => {
    if (e.detail && e.detail.sections.includes('look')) paintShell();
  });

  paint();
  app.layers.recall = recall;
  app.layers.retain = retain;
  app.layers.states = states;
  app.layers.adopt = adopt;
  app.layers.register = register;
  document.dispatchEvent(new CustomEvent('mcmap:layers'));
})();
