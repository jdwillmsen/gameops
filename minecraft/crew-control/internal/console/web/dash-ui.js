// The dashboard screen: a filter row, a grid of panels and an edit mode.
// Layout rules live in dash.js, data in dash-data.js and the panel types in
// dash-panels.js; this file wires them to the page. Data is collected whether
// or not the screen is open, so the windows are full when it is first shown.
import { createDashData, pickUnits } from './dash-data.js';
import { PANELS, PANEL_TYPES, defaultLayout } from './dash-panels.js';
import { addPanel, removePanel, movePanel, resizePanel, stepWidth, snapRows, MAX_ROWS, MAX_PANELS, MAX_SAVED, NAME_MAX, loadStore, persist, saveNamed, deleteNamed, normalizeLayout } from './dash.js';

const MIN = 60_000;
const WINDOWS = [
  { label: '5m', ms: 5 * MIN },
  { label: '15m', ms: 15 * MIN },
  { label: '1h', ms: 60 * MIN },
  { label: 'All', ms: 0 },
];
const ROW_PX = 130;
const CHROME_PX = 96;
const REFRESH_MS = 1000;

function el(tag, props = {}, ...kids) {
  const e = Object.assign(document.createElement(tag), props);
  e.append(...kids);
  return e;
}

const btn = (text, label, onClick, extra = {}) => {
  const b = el('button', { type: 'button', className: 'cc-btn', textContent: text, ...extra });
  if (label) b.setAttribute('aria-label', label);
  b.addEventListener('click', onClick);
  return b;
};

// host: { storage }; storage defaults to localStorage.
export function initDashboard(host = {}) {
  const data = createDashData();
  const storage = host.storage || (() => { try { return localStorage; } catch { return null; } })();
  const stored = storage ? loadStore(storage, PANEL_TYPES) : { current: null, saved: {} };
  let store = stored;
  let layout = store.current && store.current.panels.length ? store.current : defaultLayout();
  let editing = false;
  let mounted = false;
  let windowMs = WINDOWS[1].ms;
  let unit = '';
  let timer = 0;
  let unitSig = '';
  const cells = new Map();

  const root = el('section', { className: 'cc-root db' });
  root.setAttribute('data-theme', 'dark');
  root.setAttribute('aria-label', 'Dashboard');

  // ---- filter row: one place for the window and the unit ----

  const winBtns = WINDOWS.map(w => {
    const b = btn(w.label, w.ms ? `Last ${w.label}` : 'Everything this page has seen', () => {
      windowMs = w.ms;
      syncFilter();
      refresh(true);
    });
    return { b, w };
  });
  const winGroup = el('div', { className: 'db-group', role: 'group' }, ...winBtns.map(x => x.b));
  winGroup.setAttribute('aria-label', 'Time window');
  const unitSel = el('select', { className: 'db-sel' });
  unitSel.setAttribute('aria-label', 'Unit');
  unitSel.addEventListener('change', () => {
    unit = unitSel.value;
    refresh(true);
  });

  const layoutSel = el('select', { className: 'db-sel' });
  layoutSel.setAttribute('aria-label', 'Layout');
  layoutSel.addEventListener('change', () => {
    const v = layoutSel.value;
    if (v === '\0default') setLayout(defaultLayout(), 'Default layout');
    else if (v in store.saved) setLayout(normalizeLayout(store.saved[v], PANEL_TYPES), `Layout ${v} loaded`);
    else syncLayoutSel();
  });
  const editBtn = btn('Edit layout', '', () => setEditing(!editing));
  editBtn.setAttribute('aria-pressed', 'false');

  const bar = el('div', { className: 'db-bar' }, winGroup, unitSel, el('span', { className: 'db-spacer' }), layoutSel, editBtn);

  // ---- edit row ----

  const addSel = el('select', { className: 'db-sel' });
  addSel.setAttribute('aria-label', 'Panel to add');
  for (const [type, def] of Object.entries(PANELS)) addSel.append(el('option', { value: type, textContent: def.title }));
  const nameIn = el('input', { type: 'text', placeholder: 'Layout name', maxLength: NAME_MAX, className: 'db-name', autocomplete: 'off' });
  nameIn.setAttribute('aria-label', 'Layout name');
  const delBtn = btn('Delete saved', 'Delete the selected saved layout', () => {
    const name = layoutSel.value;
    if (!(name in store.saved)) return;
    store = deleteNamed(store, name);
    save();
    say(`Deleted layout ${name}`);
    syncLayoutSel();
  });
  const editRow = el('div', { className: 'db-bar db-edit', hidden: true },
    addSel,
    btn('Add panel', '', () => {
      if (layout.panels.length >= MAX_PANELS) return say(`At most ${MAX_PANELS} panels`);
      const def = PANELS[addSel.value];
      setLayout(addPanel(layout, addSel.value, def.size), `${def.title} added`);
    }),
    el('span', { className: 'db-spacer' }),
    nameIn,
    btn('Save as', 'Save the layout under this name', () => {
      const r = saveNamed(store, nameIn.value, layout);
      if (r.error) return say(r.error);
      store = r.store;
      save();
      nameIn.value = '';
      say(`Saved layout ${r.name}`);
      syncLayoutSel();
    }),
    delBtn,
    btn('Reset', 'Reset to the default layout', () => setLayout(defaultLayout(), 'Default layout restored')));

  const live = el('p', { className: 'cc-sr', role: 'status' });
  const note = el('p', { className: 'db-note' });
  const grid = el('div', { className: 'db-grid', role: 'list' });
  grid.setAttribute('aria-label', 'Panels');
  const empty = el('p', { className: 'db-empty', hidden: true, textContent: 'No panels. Use Edit layout to add one.' });
  root.append(bar, editRow, live, grid, empty, note);

  function say(text) {
    live.textContent = text;
    note.textContent = text;
  }

  // ---- state ----

  function save() {
    if (!storage) return;
    store = { ...store, current: layout };
    if (!persist(storage, store)) note.textContent = 'This browser would not save the layout; it will reset on reload.';
  }

  function setLayout(next, message) {
    layout = next;
    save();
    renderGrid();
    syncLayoutSel();
    refresh(true);
    if (message) say(message);
  }

  function setEditing(on) {
    editing = on;
    editBtn.setAttribute('aria-pressed', String(on));
    editRow.hidden = !on;
    root.classList.toggle('db-editing', on);
    for (const c of cells.values()) { c.tools.hidden = !on; c.cell.draggable = on; }
    note.textContent = on ? 'Editing: drag a panel, or use its buttons to move, resize or remove it. Changes save on their own.' : '';
  }

  function syncFilter() {
    for (const { b, w } of winBtns) b.setAttribute('aria-pressed', String(w.ms === windowMs));
  }

  const same = (a, b) => JSON.stringify(a.panels) === JSON.stringify(b.panels);

  function syncLayoutSel() {
    const opts = [el('option', { value: '\0default', textContent: 'Default' })];
    let active = same(layout, defaultLayout()) ? '\0default' : '';
    for (const name of Object.keys(store.saved)) {
      opts.push(el('option', { value: name, textContent: name }));
      if (!active && same(layout, store.saved[name])) active = name;
    }
    if (!active) opts.push(el('option', { value: '', textContent: 'Unsaved changes' }));
    layoutSel.replaceChildren(...opts);
    layoutSel.value = active;
    delBtn.disabled = !(active in store.saved);
    nameIn.placeholder = Object.keys(store.saved).length >= MAX_SAVED ? 'Saved layouts full' : 'Layout name';
  }

  function syncUnits() {
    const ids = data.units().map(s => [s.id, s.name || s.id]);
    const sig = JSON.stringify(ids);
    if (sig === unitSig) return;
    unitSig = sig;
    unitSel.replaceChildren(el('option', { value: '', textContent: 'All units' }), ...ids.map(([id, name]) => el('option', { value: id, textContent: name })));
    if (!ids.some(([id]) => id === unit)) unit = '';
    unitSel.value = unit;
  }

  // ---- grid ----

  const title = p => PANELS[p.type].title;

  function buildCell(p) {
    const def = PANELS[p.type];
    const cell = el('div', { className: 'db-cell', draggable: editing });
    cell.setAttribute('role', 'listitem');
    cell.dataset.id = p.id;
    const body = el('div', { className: 'db-body' });
    const tools = el('div', { className: 'db-tools', hidden: !editing });
    tools.setAttribute('role', 'group');
    tools.setAttribute('aria-label', `${def.title} layout`);
    const act = (text, label, fn) => tools.append(btn(text, `${label}: ${def.title}`, () => { fn(); keepFocus(p.id, label); }));
    const at = () => layout.panels.findIndex(x => x.id === p.id);
    const me = () => layout.panels.find(x => x.id === p.id);
    act('←', 'Move earlier', () => setLayout(movePanel(layout, p.id, at() - 1), `${def.title} moved to ${Math.max(0, at() - 1) + 1} of ${layout.panels.length}`));
    act('→', 'Move later', () => setLayout(movePanel(layout, p.id, at() + 1), `${def.title} moved to ${Math.min(layout.panels.length - 1, at() + 1) + 1} of ${layout.panels.length}`));
    act('W−', 'Narrower', () => setLayout(resizePanel(layout, p.id, { w: stepWidth(me().w, -1) }), `${def.title} is ${stepWidth(me().w, 0)} of 12 columns wide`));
    act('W+', 'Wider', () => setLayout(resizePanel(layout, p.id, { w: stepWidth(me().w, 1) }), `${def.title} is ${stepWidth(me().w, 0)} of 12 columns wide`));
    act('H−', 'Shorter', () => setLayout(resizePanel(layout, p.id, { h: snapRows(me().h - 1) }), `${def.title} is ${snapRows(me().h)} rows tall`));
    act('H+', 'Taller', () => setLayout(resizePanel(layout, p.id, { h: Math.min(MAX_ROWS, me().h + 1) }), `${def.title} is ${snapRows(me().h)} rows tall`));
    act('×', 'Remove', () => setLayout(removePanel(layout, p.id), `${def.title} removed`));
    cell.append(tools, body);

    cell.addEventListener('dragstart', ev => {
      if (!editing) return ev.preventDefault();
      ev.dataTransfer.setData('text/plain', p.id);
      ev.dataTransfer.effectAllowed = 'move';
    });
    cell.addEventListener('dragover', ev => {
      if (!editing) return;
      ev.preventDefault();
      cell.classList.add('db-over');
    });
    cell.addEventListener('dragleave', () => cell.classList.remove('db-over'));
    cell.addEventListener('drop', ev => {
      cell.classList.remove('db-over');
      const from = ev.dataTransfer.getData('text/plain');
      if (!editing || !from || from === p.id) return;
      ev.preventDefault();
      setLayout(movePanel(layout, from, layout.panels.findIndex(x => x.id === p.id)), 'Panel moved');
    });

    const height = Math.max(120, p.h * ROW_PX - CHROME_PX);
    body.style.setProperty('--db-h', `${p.h * ROW_PX}px`);
    let inst;
    try {
      inst = def.create(body, { height });
    } catch (err) {
      inst = { update() {}, destroy() {} };
      body.append(el('p', { className: 'db-empty', textContent: `${def.title} could not load.` }));
      console.error(err);
    }
    return { cell, body, tools, inst, p: { ...p }, sig: '' };
  }

  // A move re-renders the grid, which would drop focus from the button that
  // was pressed; this hands it back.
  function keepFocus(id, label) {
    const c = cells.get(id);
    const b = c && [...c.tools.children].find(x => x.getAttribute('aria-label').startsWith(label));
    b?.focus();
  }

  function renderGrid() {
    const want = new Set(layout.panels.map(p => p.id));
    for (const [id, c] of cells) {
      const p = layout.panels.find(x => x.id === id);
      // A different size means a different chart height, so the chart is
      // rebuilt; a plain move keeps it.
      if (!want.has(id) || p.type !== c.p.type || p.h !== c.p.h) {
        c.inst.destroy();
        c.cell.remove();
        cells.delete(id);
      }
    }
    for (const p of layout.panels) {
      let c = cells.get(p.id);
      if (!c) {
        c = buildCell(p);
        cells.set(p.id, c);
      }
      c.p = { ...p };
      c.cell.style.gridColumn = `span ${p.w}`;
      c.cell.style.gridRow = `span ${p.h}`;
      grid.append(c.cell);
    }
    empty.hidden = layout.panels.length > 0;
  }

  // ---- data ----

  function filter(now) {
    const first = Math.min(data.samples()[0]?.t ?? now, ...data.feed().slice(0, 1).map(e => Date.parse(e.at)).filter(Number.isFinite));
    return { unit, from: windowMs ? now - windowMs : Math.min(first, now - MIN), to: now };
  }

  function refresh(force = false) {
    if (!mounted) return;
    if (!force && document.visibilityState !== 'visible') return;
    const now = Date.now();
    syncUnits();
    const f = filter(now);
    const ctx = { data, units: pickUnits(data.units(), f), filter: f, now };
    for (const c of cells.values()) {
      let model;
      try {
        model = PANELS[c.p.type].query(ctx);
      } catch (err) {
        console.error(err);
        continue;
      }
      // Relative ages in a model change with the clock, so they are part of
      // the signature on purpose.
      const sig = JSON.stringify(model);
      if (sig === c.sig) continue;
      c.sig = sig;
      c.inst.update(model);
    }
  }

  function schedule() {
    if (!mounted || timer) return;
    timer = setTimeout(() => {
      timer = 0;
      refresh();
    }, REFRESH_MS);
  }

  syncFilter();
  syncLayoutSel();

  return {
    mount(into) {
      into.append(root);
      mounted = true;
      document.body.classList.add('view-dash');
      renderGrid();
      setEditing(editing);
      refresh(true);
    },
    unmount() {
      if (!mounted) return;
      mounted = false;
      clearTimeout(timer);
      timer = 0;
      for (const c of cells.values()) c.inst.destroy();
      cells.clear();
      root.remove();
      document.body.classList.remove('view-dash');
    },
    onUnits(list) {
      data.onUnits(list, Date.now());
      schedule();
    },
    onEvents(list) {
      data.onEvents(list);
      schedule();
    },
    key(ev) {
      if (mounted && editing && ev.key === 'Escape') {
        setEditing(false);
        editBtn.focus();
        return true;
      }
      return false;
    },
    // For tests and the page's own checks.
    layout: () => layout,
    data,
  };
}
