// The fleet overview screen: every unit in one sortable, filterable,
// keyboard-driven table with a summary strip. overview.js holds the logic;
// this file only paints it. Rows are built once and patched in place, so a
// 2 s update writes the few cells that changed and never rebuilds the list.
import { Sparkline, StatTile } from './charts/spark.js';
import { isTyping } from './state.js';
import {
  SAMPLE_MS, MAX_HEALTH, STATES, STATE_LABEL, SORTS, SORT_LABEL, DEFAULT_DIR, LEVELS,
  createBoard, sortRows, filterRows, emptyFilter, isFiltered, parseParams, formatParams, sinceLabel, moveFocus, nextAttention, trailLabels,
} from './overview.js';

const SVG = 'http://www.w3.org/2000/svg';

function el(tag, props = {}, ...kids) {
  const e = Object.assign(document.createElement(tag), props);
  e.append(...kids);
  return e;
}

function icon(paths, fill = false) {
  const svg = document.createElementNS(SVG, 'svg');
  for (const [k, v] of Object.entries({ class: 'cc-icon', width: 12, height: 12, viewBox: '0 0 12 12', 'aria-hidden': 'true' })) svg.setAttribute(k, v);
  for (const d of [].concat(paths)) {
    const p = document.createElementNS(SVG, 'path');
    p.setAttribute('d', d);
    p.setAttribute('fill', fill ? 'currentColor' : 'none');
    p.setAttribute('stroke', 'currentColor');
    p.setAttribute('stroke-width', '1.6');
    p.setAttribute('stroke-linecap', 'round');
    p.setAttribute('stroke-linejoin', 'round');
    svg.append(p);
  }
  return svg;
}

const RING = 'M6 2.2a3.8 3.8 0 1 1 0 7.6a3.8 3.8 0 1 1 0-7.6z';
// Every state has its own shape as well as its own word and hue, so none of
// them leans on colour.
const STATE_VIEW = {
  working: { tone: 'good', paths: 'M3.5 2.5 L9.5 6 L3.5 9.5 Z', fill: true },
  idle: { tone: 'idle', paths: RING },
  stuck: { tone: 'warning', paths: 'M3 2 H9 L6 6 L9 10 H3 L6 6 Z' },
  dead: { tone: 'critical', paths: 'M3 3 L9 9 M9 3 L3 9' },
  offline: { tone: 'serious', paths: [RING, 'M2.8 9.2 L9.2 2.8'] },
};

function stateChip(state) {
  const v = STATE_VIEW[state];
  return el('span', { className: `cc-status cc-status-${v.tone} ov-chip` }, icon(v.paths, v.fill), el('span', { textContent: STATE_LABEL[state] }));
}

const KIND_ICON = {
  gather: ['M2 2.5 H10', 'M6 2.5 V9', 'M3.5 6.5 L6 9 L8.5 6.5'],
  haul: ['M1.5 4 H9', 'M7 2 L9.5 4 L7 6', 'M10.5 8.5 H3', 'M5 6.5 L2.5 8.5 L5 10.5'],
  goto: 'M6 10.5 C6 10.5 2.5 7 2.5 4.8 A3.5 3.5 0 0 1 9.5 4.8 C9.5 7 6 10.5 6 10.5 Z',
  none: null,
};
const taskIcon = kind => icon(KIND_ICON[kind] === undefined ? 'M6 4.5 a1.5 1.5 0 1 1 0 3 a1.5 1.5 0 1 1 0-3z' : KIND_ICON[kind]);

const WARN = 'M6 2 L11 10.5 H1 Z M6 5.5 V7.5';
const DIM = ['', 'nether', 'end'];

const CHIP_ORDER = ['working', 'idle', 'stuck', 'dead', 'offline'];
const LEVEL_LABEL = { 0: 'Any', 6: '6 or less (low)', 10: '10 or less', 15: '15 or less' };

// Every unit's trail ends on the same samples, so a label set is built once
// per length for a paint, not once per row.
function labelsAt(times, now) {
  const cache = new Map();
  return n => {
    let l = cache.get(n);
    if (!l) cache.set(n, (l = trailLabels(times, n, now)));
    return l;
  };
}

const TILES = [
  { key: 'online', label: 'Online', trend: 'online', value: s => `${s.online}/${s.total}` },
  { key: 'working', label: 'Working', trend: 'working' },
  { key: 'idle', label: 'Idle', trend: 'idle' },
  { key: 'stuck', label: 'Stuck', status: ['warning', 'Stuck'] },
  { key: 'dead', label: 'Dead', status: ['critical', 'Dead'] },
  { key: 'offline', label: 'Offline', status: ['serious', 'Offline'] },
  { key: 'done', label: 'Tasks done', window: true },
  { key: 'failed', label: 'Tasks failed', status: ['warning', 'Failed'], window: true },
];

// host: { statuses, order(), state(), operator(), focus(id), sync() }
export function initOverview(host) {
  const board = createBoard();
  const initial = parseParams(location.search);
  // User-timing entries are never discarded by the browser, so they are only
  // written when a perf run asks, not on a wall display left open for days.
  const perf = new URLSearchParams(location.search).get('perf') === '1';
  const measure = (name, start) => { if (perf) performance.measure(name, { start, end: performance.now() }); };
  const prefs = { sort: initial.sort, dir: initial.dir, filter: initial.filter };

  const root = el('section', { className: 'cc-root ov' });
  root.setAttribute('data-theme', 'dark');
  root.setAttribute('aria-label', 'Fleet overview');

  // ---- summary strip ----

  const tilesBox = el('div', { className: 'ov-tiles', role: 'group' });
  tilesBox.setAttribute('aria-label', 'Fleet summary');
  const tiles = TILES.map(def => {
    const box = el('div', { className: 'ov-tile' });
    tilesBox.append(box);
    const tile = new StatTile(box, { label: def.label, value: '-', trend: def.trend ? { values: [], labels: [] } : undefined, status: def.status ? 'good' : undefined, statusLabel: 'None', format: n => String(n), trendFormat: n => (n == null ? 'unknown' : String(Math.round(n))) });
    if (tile.spark) {
      tile.spark.svg.setAttribute('tabindex', '-1');
      // The trend is redrawn every tick; a 90-row screen-reader table per tile
      // per tick is the most expensive part of it, and the value is in the text.
      tile.spark.opts.table = false;
    }
    return { def, tile, box, sig: '' };
  });

  // ---- toolbar ----

  const q = el('input', { type: 'search', placeholder: 'Filter (/)', autocomplete: 'off', spellcheck: false, value: prefs.filter.q });
  q.setAttribute('aria-label', 'Filter units');
  const chips = new Map();
  const chipBox = el('div', { className: 'ov-chips', role: 'group' });
  chipBox.setAttribute('aria-label', 'Filter by state');
  for (const s of CHIP_ORDER) {
    const count = el('span', { className: 'ov-count-n', textContent: '0' });
    const b = el('button', { type: 'button', className: 'ov-fchip' }, icon(STATE_VIEW[s].paths, STATE_VIEW[s].fill), el('span', { textContent: STATE_LABEL[s] }), count);
    b.dataset.state = s;
    b.addEventListener('click', () => {
      const f = prefs.filter;
      f.states = f.states.includes(s) ? f.states.filter(x => x !== s) : [...f.states, s];
      changed();
    });
    chips.set(s, { b, count, n: -1 });
    chipBox.append(b);
  }

  const select = (label, onPick) => {
    const s = el('select');
    s.setAttribute('aria-label', label);
    s.addEventListener('change', () => onPick(s.value));
    return s;
  };
  const kindSel = select('Task', v => { prefs.filter.kind = v; changed(); });
  const hpSel = select('Health at most', v => { prefs.filter.hp = Number(v); changed(); });
  const foodSel = select('Hunger at most', v => { prefs.filter.food = Number(v); changed(); });
  for (const [sel, name] of [[hpSel, 'Health'], [foodSel, 'Hunger']]) {
    sel.replaceChildren(...LEVELS.map(n => el('option', { value: String(n), textContent: `${name}: ${LEVEL_LABEL[n]}` })));
  }
  const sortSel = select('Sort by', v => setSort(v, true));
  sortSel.className = 'ov-sortsel';
  sortSel.replaceChildren(...SORTS.map(k => el('option', { value: k, textContent: `Sort: ${SORT_LABEL[k]}` })));
  const dirBtn = el('button', { type: 'button', className: 'ov-dir' });
  dirBtn.addEventListener('click', () => { prefs.dir = prefs.dir === 'asc' ? 'desc' : 'asc'; changed(true); });
  const moreBtn = el('button', { type: 'button', className: 'ov-more', textContent: 'Filters' });
  moreBtn.setAttribute('aria-expanded', 'false');
  const more = el('div', { className: 'ov-selects' }, kindSel, hpSel, foodSel);
  moreBtn.addEventListener('click', () => {
    const open = !more.classList.contains('open');
    more.classList.toggle('open', open);
    moreBtn.setAttribute('aria-expanded', String(open));
  });
  const clear = el('button', { type: 'button', className: 'ov-clear', textContent: 'Clear' });
  clear.addEventListener('click', clearFilters);
  const shownText = el('span');
  const windowText = el('span', { className: 'ov-window' });
  const count = el('span', { className: 'ov-count', role: 'status' }, shownText, windowText);
  const bar = el('div', { className: 'ov-bar' },
    el('div', { className: 'ov-bar-top' }, q, sortSel, dirBtn, moreBtn),
    chipBox, more,
    el('div', { className: 'ov-bar-end' }, count, clear));

  // ---- table ----

  const COLS = [
    ['unit', '#'], ['name', 'Unit'], ['state', 'State'], ['since', 'In state'], ['health', 'Health'], ['hunger', 'Hunger'], ['task', 'Task'], [null, 'Health, 3 min'], ['pos', 'Position'],
  ];
  const head = el('div', { className: 'ov-head', role: 'row' });
  const heads = new Map();
  for (const [key, label] of COLS) {
    const cell = el('div', { className: `ov-h ov-h-${key || 'trend'}`, role: 'columnheader' });
    if (key && key !== 'pos') {
      const b = el('button', { type: 'button' }, el('span', { textContent: label }), el('span', { className: 'ov-arrow' }));
      b.addEventListener('click', () => setSort(key, false));
      cell.append(b);
      heads.set(key, cell);
    } else cell.textContent = label;
    head.append(cell);
  }
  const body = el('div', { className: 'ov-body', role: 'rowgroup' });
  const empty = el('p', { className: 'ov-empty', hidden: true });
  const grid = el('div', { className: 'ov-grid', role: 'grid' }, head, body);
  grid.setAttribute('aria-label', 'Crew units');
  const wrap = el('div', { className: 'ov-wrap' }, grid, empty);
  const emptyText = el('span');
  const emptyClear = el('button', { type: 'button', textContent: 'Clear filters' });
  emptyClear.addEventListener('click', clearFilters);
  empty.append(emptyText, ' ', emptyClear);

  root.append(tilesBox, bar, wrap);

  // ---- rows ----

  const entries = new Map();
  let roving = '';

  function meter(kind) {
    const fill = el('i');
    const track = el('span', { className: 'ov-track' }, fill);
    const val = el('span', { className: 'ov-val' });
    const warn = icon(WARN);
    warn.classList.add('ov-low');
    return { cell: el('div', { className: `ov-c ov-c-${kind} ov-meter ${kind}`, role: 'gridcell' }, track, val, warn), fill, val };
  }

  function buildRow(r) {
    const num = el('div', { className: 'ov-c ov-c-unit', role: 'gridcell' });
    const name = el('div', { className: 'ov-c ov-c-name', role: 'gridcell' });
    const chipHost = el('div', { className: 'ov-c ov-c-state', role: 'gridcell' });
    const sinceCell = el('div', { className: 'ov-c ov-c-since', role: 'gridcell' });
    const hp = meter('hp');
    const food = meter('food');
    const taskIconHost = el('span', { className: 'ov-ti' });
    const taskText = el('span', { className: 'ov-tt' });
    const taskCell = el('div', { className: 'ov-c ov-c-task', role: 'gridcell' }, taskIconHost, taskText);
    const trendHost = el('div', { className: 'ov-c ov-c-trend', role: 'gridcell' });
    const posCell = el('div', { className: 'ov-c ov-c-pos', role: 'gridcell' });
    const row = el('div', { className: 'ov-row', role: 'row', tabIndex: -1 }, num, name, chipHost, sinceCell, hp.cell, food.cell, taskCell, trendHost, posCell);
    row.dataset.unit = r.id;
    const spark = new Sparkline(trendHost, { height: 22, pad: 3, table: false, domain: [0, MAX_HEALTH], name: 'Health', format: n => (n == null ? 'unknown' : String(Math.round(n))) });
    spark.svg.setAttribute('tabindex', '-1');
    return { id: r.id, row, num, name, chipHost, sinceCell, hp, food, taskIconHost, taskText, spark, posCell, c: {} };
  }

  // Writes only what differs from the last paint.
  const put = (e, key, value, write) => {
    if (e.c[key] === value) return;
    e.c[key] = value;
    write(value);
  };

  function paintMeter(e, key, m, value, low, label) {
    put(e, key, `${value}|${low}`, () => {
      const known = value !== null;
      m.fill.style.transform = `scaleX(${known ? Math.max(0, Math.min(1, value / MAX_HEALTH)) : 0})`;
      m.val.textContent = known ? String(Math.round(value)) : '-';
      m.cell.classList.toggle('is-low', low);
      const text = known ? `${label} ${Math.round(value)} of ${MAX_HEALTH}${low ? ', low' : ''}` : `${label} unknown`;
      m.cell.title = text;
      m.cell.setAttribute('aria-label', text);
    });
  }

  function paintRow(e, r, now, cur) {
    put(e, 'num', r.index, v => { e.num.textContent = String(v); });
    put(e, 'name', r.name, v => { e.name.textContent = v; });
    // A focused row is read by its label, so it carries the whole story.
    put(e, 'label', `${r.name}, ${STATE_LABEL[r.state].toLowerCase()}, health ${r.health === null ? 'unknown' : `${Math.round(r.health)} of ${MAX_HEALTH}`}, hunger ${r.hunger === null ? 'unknown' : `${Math.round(r.hunger)} of ${MAX_HEALTH}`}, ${r.taskText}`, v => e.row.setAttribute('aria-label', v));
    put(e, 'state', r.state, v => { e.chipHost.replaceChildren(stateChip(v)); e.row.dataset.state = v; });
    put(e, 'since', sinceLabel(r.since, now), v => { e.sinceCell.textContent = v; });
    paintMeter(e, 'hp', e.hp, r.health, r.lowHealth, 'Health');
    paintMeter(e, 'food', e.food, r.hunger, r.lowHunger, 'Hunger');
    put(e, 'kind', r.kind, v => { e.taskIconHost.replaceChildren(...(KIND_ICON[v] === null ? [] : [taskIcon(v)])); });
    put(e, 'task', r.taskText, v => { e.taskText.textContent = v; e.taskText.parentNode.title = v; });
    put(e, 'pos', r.pos ? `${r.pos.x} ${r.pos.y} ${r.pos.z}${DIM[r.pos.dim] ? ` ${DIM[r.pos.dim]}` : ''}` : '', v => { e.posCell.textContent = v || '-'; });
    put(e, 'cur', r.id === cur, v => {
      e.row.classList.toggle('cur', v);
      if (v) e.row.setAttribute('aria-current', 'true');
      else e.row.removeAttribute('aria-current');
    });
  }

  // ---- paint ----

  const FRAME_BUDGET_MS = 5;
  let pending = null;
  let pump = 0;

  function paintSlice() {
    pump = 0;
    const t0 = performance.now();
    const labels = labelsAt(board.times(), pending ? pending.now : 0);
    while (pending && pending.i < pending.work.length) {
      const [e, r] = pending.work[pending.i++];
      paintRow(e, r, pending.now, pending.cur);
      e.spark.update(r.trail, labels(r.trail.length));
      if (performance.now() - t0 > FRAME_BUDGET_MS) break;
    }
    if (pending && pending.i < pending.work.length) pump = requestAnimationFrame(paintSlice);
    else pending = null;
    measure('fleet-paint', t0);
  }

  function paintRows(work, now, cur, immediate) {
    cancelAnimationFrame(pump);
    pending = { work, now, cur, i: 0 };
    if (immediate) {
      const labels = labelsAt(board.times(), now);
      for (const [e, r] of work) {
        paintRow(e, r, now, cur);
        e.spark.update(r.trail, labels(r.trail.length));
      }
      pending = null;
      pump = 0;
    } else paintSlice();
  }

  let mounted = false;
  let painted = false;
  let hovering = false;

  const visibleIds = () => [...body.children].filter(c => !c.hidden).map(c => c.dataset.unit);

  function render(reorder = false) {
    if (!mounted) return;
    const t0 = performance.now();
    const now = Date.now();
    const all = board.rows();
    const sum = board.summary();
    const filtered = filterRows(all, prefs.filter);
    const shown = sortRows(filtered, prefs.sort, prefs.dir);
    const shownIds = new Set(shown.map(r => r.id));
    const cur = host.state().unit;
    const trendLabels = labelsAt(board.times(), now);
    const op = !!host.operator();
    root.classList.toggle('has-pos', op);

    for (const t of tiles) {
      const { def } = t;
      const n = sum[def.key];
      const value = def.value ? def.value(sum) : n;
      const sig = `${value}|${n > 0}`;
      const trend = def.trend ? board.fleetTrail(def.trend) : null;
      // A count of units reads from zero to the fleet size, so a one-unit wobble
      // does not fill the whole sparkline.
      if (t.tile.spark) t.tile.spark.opts.domain = [0, Math.max(1, sum.total)];
      if (def.window) {
        const tip = sum.events ? `Counted over the last ${sum.events} events in the live feed` : 'No events in the live feed yet';
        if (t.box.title !== tip) t.box.title = tip;
      }
      if (sig !== t.sig) {
        t.sig = sig;
        const next = { value };
        if (def.status) Object.assign(next, { status: n > 0 ? def.status[0] : 'good', statusLabel: n > 0 ? def.status[1] : 'None' });
        if (trend) next.trend = { values: trend, labels: trendLabels(trend.length) };
        t.tile.update(next);
      } else if (trend) t.tile.spark.update(trend, trendLabels(trend.length));
    }
    tilesBox.dataset.since = sum.since;

    for (const [state, c] of chips) {
      const n = sum[state];
      if (c.n !== n) { c.n = n; c.count.textContent = String(n); }
      c.b.setAttribute('aria-pressed', String(prefs.filter.states.includes(state)));
    }
    syncSelects();

    // By id, not by count: a roster swap of the same size leaves as many old
    // rows as new units.
    const live = new Set(all.map(r => r.id));
    for (const [id, e] of entries) {
      if (live.has(id)) continue;
      e.spark.destroy();
      e.row.remove();
      entries.delete(id);
      if (roving === id) roving = '';
    }
    const work = [];
    for (const r of all) {
      let e = entries.get(r.id);
      if (!e) {
        e = buildRow(r);
        entries.set(r.id, e);
        body.append(e.row);
        if (!roving) setRoving(r.id, false);
      }
      const show = shownIds.has(r.id);
      if (e.row.hidden === show) e.row.hidden = !show;
      if (show) work.push([e, r]);
    }
    // A change the user just made paints at once; a timer tick spreads its
    // twenty rows over as many frames as the machine needs.
    paintRows(work, now, cur, reorder || !painted);
    const rv = entries.get(roving);
    if (!rv || rv.row.hidden) {
      const first = shown[0];
      if (first) setRoving(first.id, false);
    }
    // The row under the pointer would move away mid-click if a changing
    // value re-sorted the list, so order waits for the pointer to leave.
    if (reorder || !hovering || !painted) orderRows(shown);
    painted = true;

    for (const [key, cell] of heads) cell.setAttribute('aria-sort', key === prefs.sort ? (prefs.dir === 'asc' ? 'ascending' : 'descending') : 'none');
    sortSel.value = prefs.sort;
    dirBtn.textContent = prefs.dir === 'asc' ? '↑' : '↓';
    dirBtn.setAttribute('aria-label', prefs.dir === 'asc' ? 'Ascending' : 'Descending');
    for (const [key, cell] of heads) cell.querySelector('.ov-arrow').textContent = key === prefs.sort ? (prefs.dir === 'asc' ? '↑' : '↓') : '';

    const filteredOn = isFiltered(prefs.filter);
    const sub = sum.events ? ` · done and failed count the last ${sum.events} events${sum.since ? ` since ${new Date(sum.since).toLocaleTimeString([], { hour12: false })}` : ''}` : '';
    const line = all.length ? `${shown.length} of ${all.length} units` : 'waiting for units';
    if (shownText.textContent !== line) shownText.textContent = line;
    if (windowText.textContent !== sub) windowText.textContent = sub;
    clear.hidden = !filteredOn;
    empty.hidden = !(all.length && !shown.length);
    emptyText.textContent = 'No unit matches the filters.';
    measure('fleet-render', t0);
  }

  // Moves only rows that are out of place; a list already in order costs no
  // DOM writes, and a focused row keeps focus through a move.
  function orderRows(shown) {
    const active = document.activeElement;
    let ref = body.firstElementChild;
    for (const r of shown) {
      const node = entries.get(r.id).row;
      while (ref && ref !== node && ref.hidden) ref = ref.nextElementSibling;
      if (ref === node) ref = ref.nextElementSibling;
      else body.insertBefore(node, ref);
    }
    if (active && active !== document.activeElement && body.contains(active)) active.focus({ preventScroll: true });
  }

  let kindSig = '';
  function syncSelects() {
    const kinds = [...new Set([...board.kinds(), ...(prefs.filter.kind ? [prefs.filter.kind] : [])])].sort();
    const sig = kinds.join('|');
    if (sig !== kindSig) {
      kindSig = sig;
      kindSel.replaceChildren(el('option', { value: '', textContent: 'Task: any' }), el('option', { value: 'none', textContent: 'Task: none' }), ...kinds.filter(k => k !== 'none').map(k => el('option', { value: k, textContent: `Task: ${k}` })));
    }
    kindSel.value = prefs.filter.kind;
    hpSel.value = String(prefs.filter.hp);
    foodSel.value = String(prefs.filter.food);
  }

  function changed(reorder = true) {
    render(reorder);
    host.sync();
  }

  function setSort(key, fromSelect) {
    if (!SORTS.includes(key)) return;
    if (key === prefs.sort && !fromSelect) prefs.dir = prefs.dir === 'asc' ? 'desc' : 'asc';
    else if (key !== prefs.sort) prefs.dir = DEFAULT_DIR[key];
    prefs.sort = key;
    changed(true);
  }

  function clearFilters() {
    prefs.filter = emptyFilter();
    q.value = '';
    changed();
  }

  q.addEventListener('input', () => { prefs.filter.q = q.value.slice(0, 40); changed(); });
  q.addEventListener('keydown', ev => {
    if (ev.key === 'Escape') {
      ev.preventDefault();
      ev.stopPropagation();
      if (q.value) { q.value = ''; prefs.filter.q = ''; changed(); } else focusRow(roving);
    } else if (ev.key === 'ArrowDown' || ev.key === 'Enter') {
      ev.preventDefault();
      focusRow(roving && !entries.get(roving)?.row.hidden ? roving : visibleIds()[0]);
    }
  });

  // ---- rows: pointer and keyboard ----

  function setRoving(id, takeFocus) {
    const e = entries.get(id);
    if (!e) return;
    const prev = entries.get(roving);
    if (prev && prev !== e) prev.row.tabIndex = -1;
    roving = id;
    e.row.tabIndex = 0;
    if (takeFocus) e.row.focus();
  }

  function focusRow(id) {
    if (id) setRoving(id, true);
  }

  body.addEventListener('focusin', ev => {
    const row = ev.target.closest('.ov-row');
    if (row && row.dataset.unit !== roving) setRoving(row.dataset.unit, false);
  });
  body.addEventListener('click', ev => {
    const row = ev.target.closest('.ov-row');
    if (row) host.focus(row.dataset.unit);
  });
  body.addEventListener('pointerenter', ev => { hovering = ev.pointerType === 'mouse'; });
  body.addEventListener('pointerleave', () => {
    if (!hovering) return;
    hovering = false;
    render(true);
  });

  const pageSize = () => {
    const first = body.querySelector('.ov-row:not([hidden])');
    return first ? Math.max(1, Math.floor(wrap.clientHeight / first.offsetHeight) - 1) : 8;
  };

  const MOVES = ['ArrowDown', 'ArrowUp', 'PageDown', 'PageUp', 'Home', 'End', 'j', 'k'];

  // Returns true when the key was ours. console.js asks first, so while the
  // fleet is up its own j/k/a/digit/bracket keys do not run, wherever focus
  // sits (a sort header, a chip, a button): they would pick a unit and leave
  // the table.
  function key(ev) {
    if (!mounted || document.getElementById('overlay')) return false;
    if (isTyping(ev.target)) return false;
    if (ev.key === '/') {
      ev.preventDefault();
      q.focus();
      q.select();
      return true;
    }
    const row = ev.target.closest && ev.target.closest('.ov-row');
    const cur = row ? row.dataset.unit : roving;
    if (MOVES.includes(ev.key)) {
      ev.preventDefault();
      focusRow(moveFocus(visibleIds(), cur, ev.key, pageSize()));
      return true;
    }
    // Enter and Space already mean something on a button.
    if (row && (ev.key === 'Enter' || ev.key === ' ')) {
      ev.preventDefault();
      host.focus(row.dataset.unit);
      return true;
    }
    // The detail page of the row; d is free at the table, and the row you
    // are about to open is also where the pointer may sit.
    if (ev.key === 'd' && host.detail) {
      ev.preventDefault();
      host.detail(row ? row.dataset.unit : roving);
      return true;
    }
    if (ev.key === 'a') {
      const shown = new Set(visibleIds());
      focusRow(nextAttention(board.rows().filter(r => shown.has(r.id)), cur));
      return true;
    }
    if (ev.key === 'Escape' && isFiltered(prefs.filter)) {
      clearFilters();
      return true;
    }
    return /^[0-9[\]]$/.test(ev.key);
  }

  // ---- lifecycle ----

  setInterval(() => {
    board.sample();
    if (mounted && document.visibilityState === 'visible') render();
  }, SAMPLE_MS);
  document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'visible') render(); });

  const list = () => host.order().map(id => host.statuses.get(id)).filter(Boolean);

  return {
    mount(into) {
      into.append(root);
      mounted = true;
      painted = false;
      hovering = false;
      document.body.classList.add('view-fleet');
      render(true);
    },
    unmount() {
      if (!mounted) return;
      mounted = false;
      // A removed table never gets its pointerleave.
      hovering = false;
      root.remove();
      document.body.classList.remove('view-fleet');
    },
    onUnits() {
      board.ingest(list(), Date.now());
      if (mounted && !painted) render(true);
    },
    onEvents(incoming, live) {
      board.events(incoming, live);
    },
    onPositions(positions) {
      board.positions(positions);
    },
    onOperator() {
      if (!host.operator()) board.clearPositions();
      render();
    },
    key,
    // What the address bar carries for this screen, as a leading-& suffix.
    query() {
      if (host.state().view !== 'fleet') return '';
      const s = formatParams(prefs);
      return s ? `&${s}` : '';
    },
  };
}
