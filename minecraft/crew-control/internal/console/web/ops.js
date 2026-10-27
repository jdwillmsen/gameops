// The ops-console shell around the camera: roster, wall, event feed, radar,
// palette, toasts and status bar. console.js owns the stream, the session and
// the focused camera; this file owns everything that scales with the fleet.
import { MODES, wallSrc, badge, nextMode } from '/static/state.js';
import {
  alertsOf, worst, counts, taskLine, sortUnits, step, nextAlerted, gridFor, pageOf, ALL, budgetLabel, budgetsFor,
  mergeEvents, filterEvents, isToast, ago, project, nearest, rankItems, KEYS,
} from '/static/fleet.js';
import { createFeedView, perFrame, timeText } from '/static/feedview.js';
import { openBulk } from '/static/bulk-ui.js';
import { createGovernor, lowerBudget } from '/static/governor.js';

const $ = id => document.getElementById(id);

function el(tag, props = {}, ...kids) {
  const e = Object.assign(document.createElement(tag), props);
  e.append(...kids);
  return e;
}

const PREFS = 'crew-console-prefs';
function loadPrefs() {
  const d = { side: true, dock: true, budget: 6, sort: 'unit', desc: false, level: 'all' };
  try { return { ...d, ...JSON.parse(localStorage.getItem(PREFS) || '{}') }; } catch { return d; }
}

// host: { statuses, order(), state(), focus(id), detail(id), setMode(m), setView(v),
//         operator(), api(method, path, body) }
export function initOps(host) {
  const prefs = loadPrefs();
  const save = () => { try { localStorage.setItem(PREFS, JSON.stringify(prefs)); } catch { /* private mode */ } };

  let feed = [];
  let unread = 0;
  let positions = [];
  let wallPage = 0;
  // The budget the frame-rate governor stepped down to; never saved, so the
  // viewer's own choice comes back on the next load.
  let eased = 0;
  const budget = () => eased || prefs.budget;
  let pin = '';
  let kiosk = false;
  const picked = new Set();
  const feedFilter = { unit: false };

  const list = () => host.order().map(id => host.statuses.get(id)).filter(Boolean);

  // ---- layout toggles ----

  function applyLayout() {
    document.body.classList.toggle('kiosk', kiosk);
    $('side').hidden = !prefs.side;
    $('dock').hidden = !prefs.dock;
    $('radar-box').hidden = !host.operator();
    for (const [id, on] of [['t-roster', prefs.side], ['t-events', prefs.dock]]) {
      $(id).setAttribute('aria-pressed', String(on));
    }
    $('t-radar').hidden = !host.operator();
    if (prefs.dock) { unread = 0; renderDockHead(); }
  }

  function toggle(key) {
    prefs[key] = !prefs[key];
    save();
    applyLayout();
    if (key === 'side') renderRoster();
    if (key === 'dock') renderFeed();
    if (host.state().view === 'wall') layoutWall();
  }

  $('t-roster').addEventListener('click', () => toggle('side'));
  $('t-events').addEventListener('click', () => toggle('dock'));
  $('t-radar').addEventListener('click', () => { $('radar-box').classList.toggle('collapsed'); drawRadar(); });
  $('t-palette').addEventListener('click', () => openPalette(''));
  $('t-help').addEventListener('click', openHelp);

  // ---- roster ----

  function bar(value, max, kind) {
    const known = typeof value === 'number';
    const pct = known ? Math.max(0, Math.min(100, (value / max) * 100)) : 0;
    const b = el('span', { className: `meter ${kind}` + (known && value <= 6 ? ' low' : '') });
    b.title = known ? `${Math.round(value)} / ${max}` : 'unknown';
    b.append(el('i'));
    b.firstChild.style.width = pct + '%';
    return b;
  }

  function renderRoster() {
    const box = $('roster');
    // The fleet overview replaces the roster, which would otherwise rebuild unseen every tick.
    if ($('side').hidden || document.body.classList.contains('view-fleet')) return;
    const rows = sortUnits(list(), prefs.sort, prefs.desc);
    const cols = [['unit', '#'], ['name', 'Unit'], ['health', 'HP'], ['hunger', 'Food'], ['task', 'Task'], ['alerts', '!']];
    const op = host.operator();
    const head = el('tr', {}, ...(op ? [el('th')] : []), ...cols.map(([key, label]) => {
      const th = el('th', { textContent: label + (prefs.sort === key ? (prefs.desc ? ' v' : ' ^') : ''), tabIndex: 0 });
      th.dataset.sort = key;
      th.setAttribute('aria-sort', prefs.sort === key ? (prefs.desc ? 'descending' : 'ascending') : 'none');
      const resort = () => {
        prefs.desc = prefs.sort === key ? !prefs.desc : false;
        prefs.sort = key;
        save();
        renderRoster();
      };
      th.addEventListener('click', resort);
      th.addEventListener('keydown', ev => {
        if (ev.key !== 'Enter' && ev.key !== ' ') return;
        ev.preventDefault();
        ev.stopPropagation();
        resort();
      });
      return th;
    }));
    const idx = new Map(host.order().map((id, i) => [id, i + 1]));
    const body = rows.map(s => {
      const al = alertsOf(s);
      const tr = el('tr', { className: [worst(al), !s.online ? 'offline' : '', s.id === host.state().unit ? 'cur' : ''].join(' ') });
      tr.dataset.unit = s.id;
      if (op) {
        const cb = el('input', { type: 'checkbox', checked: picked.has(s.id) });
        cb.setAttribute('aria-label', `Select ${s.name || s.id}`);
        cb.addEventListener('click', ev => ev.stopPropagation());
        cb.addEventListener('change', () => { if (cb.checked) picked.add(s.id); else picked.delete(s.id); renderRoster(); });
        tr.append(el('td', {}, cb));
      }
      const nameCell = el('td', { className: 'nm', textContent: s.name || s.id });
      if (op && host.detail) {
        // A row click focuses the camera, so the second destination gets its
        // own explicit affordance rather than a hidden double-click.
        const b = el('button', { type: 'button', className: 'dt-open', textContent: 'detail', title: `Open detail for ${s.name || s.id}` });
        b.addEventListener('click', ev => { ev.stopPropagation(); host.detail(s.id); });
        nameCell.append(' ', b);
      }
      tr.append(
        el('td', { className: 'num', textContent: String(idx.get(s.id)) }),
        nameCell,
        el('td', {}, bar(s.health, 20, 'hp')),
        el('td', {}, bar(s.hunger, 20, 'food')),
        el('td', { className: 'task', textContent: taskLine(s) }),
        el('td', { className: 'al', textContent: al.length ? al.map(a => a.text).join(', ') : '' }),
      );
      tr.addEventListener('click', () => host.focus(s.id));
      tr.tabIndex = 0;
      tr.addEventListener('keydown', ev => {
        if (ev.target !== tr || ev.key !== 'Enter') return;
        ev.stopPropagation();
        host.focus(s.id);
      });
      return tr;
    });
    const had = box.contains(document.activeElement) ? document.activeElement : null;
    const keep = had && (had.dataset.sort ? `th[data-sort="${had.dataset.sort}"]` : had.dataset.unit ? `tr[data-unit="${had.dataset.unit}"]` : '');
    box.replaceChildren(el('thead', {}, head), el('tbody', {}, ...body));
    // Rebuilding drops keyboard focus; put it back on the equivalent header or row.
    if (keep) box.querySelector(keep)?.focus();
    const sel = $('roster-sel');
    sel.hidden = !op || picked.size === 0;
    sel.textContent = `Stop ${picked.size} selected`;
    for (const id of ['roster-recall', 'roster-assign']) $(id).hidden = sel.hidden;
  }

  $('roster-sel').addEventListener('click', () => stopUnits([...picked]));
  $('roster-recall').addEventListener('click', () => bulk('recall'));
  $('roster-assign').addEventListener('click', () => bulk('assign'));

  // ---- wall ----

  function renderWall(stage) {
    stage.replaceChildren();
    stage.className = 'wall';
    const order = host.order();
    const pg = pageOf(order, budget(), wallPage);
    wallPage = pg.page;

    const bar = el('div', { className: 'wallbar' }, el('span', { className: 'lbl', textContent: 'Live feeds' }));
    for (const b of budgetsFor(order.length)) {
      const btn = el('button', { type: 'button', textContent: budgetLabel(b), title: b === 0 ? 'Status cards only, no cameras' : `${b} cameras at a time` });
      btn.setAttribute('aria-pressed', String(budget() === b));
      btn.addEventListener('click', () => { prefs.budget = b; eased = 0; wallPage = 0; save(); host.setView('wall'); });
      bar.append(btn);
    }
    if (eased) bar.append(el('span', { className: 'pg', textContent: `Reduced to ${eased} live: low frame rate`, title: 'The page could not keep up, pick a budget to try again' }));
    if (pg.pages > 1) {
      const prev = el('button', { type: 'button', textContent: '<', title: 'Previous page (,)' });
      const next = el('button', { type: 'button', textContent: '>', title: 'Next page (.)' });
      prev.addEventListener('click', () => wallStep(-1));
      next.addEventListener('click', () => wallStep(1));
      bar.append(prev, el('span', { className: 'pg', textContent: `${pg.page + 1} / ${pg.pages}` }), next);
    }
    const grid = el('div', { className: 'wallgrid' });
    for (const id of pg.ids) {
      const tile = el('div', { className: 'tile' + (pg.live ? '' : ' card') });
      tile.dataset.unit = id;
      if (pg.live) tile.append(el('iframe', { src: wallSrc(id), title: id }));
      const cover = el('button', { type: 'button', className: 'cover', title: 'Focus this unit' });
      cover.setAttribute('aria-label', `Focus ${id}`);
      cover.addEventListener('click', () => host.focus(id));
      tile.append(cover, el('div', { className: 'label' }), el('div', { className: 'info' }));
      grid.append(tile);
    }
    stage.append(bar, grid);
    layoutWall();
    refreshTiles();
  }

  function wallStep(d) {
    const pg = pageOf(host.order(), budget(), wallPage);
    if (pg.pages < 2) return;
    wallPage = (pg.page + d + pg.pages) % pg.pages;
    host.setView('wall');
  }

  function layoutWall() {
    const grid = document.querySelector('.wallgrid');
    if (!grid) return;
    const n = grid.children.length;
    const card = grid.firstChild && grid.firstChild.classList.contains('card');
    const r = grid.getBoundingClientRect();
    const g = gridFor(n, r.width, r.height, card ? 2.2 : 16 / 9);
    grid.style.gridTemplateColumns = `repeat(${g.cols}, minmax(0, 1fr))`;
    grid.style.gridTemplateRows = `repeat(${g.rows}, minmax(0, 1fr))`;
  }
  new ResizeObserver(() => layoutWall()).observe($('stage'));

  function refreshTiles() {
    for (const tile of document.querySelectorAll('.tile')) {
      const s = host.statuses.get(tile.dataset.unit);
      if (!s) continue;
      const al = alertsOf(s);
      tile.classList.toggle('offline', !s.online);
      tile.classList.toggle('crit', worst(al) === 'crit');
      tile.classList.toggle('warn', worst(al) === 'warn');
      tile.querySelector('.label').textContent = `${s.name || s.id}  ${badge(s)}`;
      const info = tile.querySelector('.info');
      if (tile.classList.contains('card')) {
        info.replaceChildren(
          el('div', { className: 'big', textContent: s.name || s.id }),
          el('div', {}, bar(s.health, 20, 'hp'), bar(s.hunger, 20, 'food')),
          el('div', { className: 'task', textContent: taskLine(s) }),
          el('div', { className: 'al', textContent: al.map(a => a.text).join(', ') }),
        );
      }
    }
  }

  // ---- picture-in-picture ----

  function renderPip() {
    const box = $('pip');
    const show = pin && host.statuses.has(pin) && host.state().view === 'focus' && pin !== host.state().unit;
    if (!show) {
      if (!box.hidden) { box.hidden = true; box.replaceChildren(); delete box.dataset.unit; }
      return;
    }
    box.hidden = false;
    const s = host.statuses.get(pin);
    if (box.dataset.unit !== pin) {
      box.dataset.unit = pin;
      const swap = el('button', { type: 'button', className: 'cover', title: 'Swap with the main view' });
      swap.addEventListener('click', () => { const was = host.state().unit; host.focus(pin); pin = was; renderPip(); });
      const close = el('button', { type: 'button', className: 'x', textContent: 'x', title: 'Unpin (p)' });
      close.addEventListener('click', () => { pin = ''; renderPip(); });
      box.replaceChildren(el('iframe', { src: wallSrc(pin), title: 'pinned ' + pin }), swap, el('div', { className: 'label' }), close);
    }
    box.querySelector('.label').textContent = `pin ${s.name || s.id}  ${badge(s)}`;
    box.classList.toggle('crit', worst(alertsOf(s)) === 'crit');
  }

  function togglePin() {
    const cur = host.state().unit;
    pin = pin === cur ? '' : cur;
    if (pin === host.state().unit) {
      // The pinned unit is the main view; the pip shows once focus moves on.
      toast({ level: 'info', name: host.statuses.get(cur)?.name || cur, text: 'pinned: stays in view when you switch units', unit: cur });
    }
    renderPip();
  }

  // ---- event feed and toasts ----

  function renderDockHead() {
    const u = $('dock-unread');
    u.hidden = unread === 0 || prefs.dock === false;
    u.textContent = String(unread);
  }

  function renderFeed() {
    if ($('dock').hidden) return;
    const box = $('feed');
    const rows = filterEvents(feed, { unit: feedFilter.unit ? host.state().unit : '', level: prefs.level }).slice(-120);
    const atEnd = box.scrollHeight - box.scrollTop - box.clientHeight < 24;
    if (!feedView.render(rows, () => el('li', { className: 'empty', textContent: 'no events yet' }))) return;
    if (atEnd) box.scrollTop = box.scrollHeight;
  }
  const feedView = createFeedView($('feed'), e => {
    const li = el('li', { className: `ev ${e.level}` },
      el('time', { textContent: timeText(e.at) }),
      el('b', { textContent: e.name || e.unit }),
      el('span', { textContent: e.text }));
    li.addEventListener('click', () => host.focus(e.unit));
    return li;
  });
  // Events arrive in bursts; one render per frame is all anyone can see.
  const renderFeedSoon = perFrame(() => renderFeed());

  function onEvents(incoming, live) {
    const r = mergeEvents(live ? feed : [], incoming);
    feed = r.feed;
    if (live) {
      for (const e of r.added) {
        if (isToast(e)) toast(e);
        if (!prefs.dock) unread++;
      }
    }
    renderDockHead();
    renderFeedSoon();
  }

  function toast(e) {
    const box = $('toasts');
    const t = el('button', { type: 'button', className: `toast ${e.level}` }, el('b', { textContent: e.name || e.unit }), ' ', e.text);
    t.addEventListener('click', () => { t.remove(); if (e.unit) host.focus(e.unit); });
    box.append(t);
    while (box.children.length > 4) box.firstChild.remove();
    setTimeout(() => t.remove(), e.level === 'crit' ? 10000 : 6000);
  }

  $('feed-level').addEventListener('change', ev => { prefs.level = ev.target.value; save(); renderFeed(); });
  $('feed-unit').addEventListener('change', ev => { feedFilter.unit = ev.target.checked; renderFeed(); });
  $('feed-level').value = prefs.level;

  // ---- radar (signed-in only: positions are never sent to anyone else) ----

  let radarPts = [];
  const canvas = $('radar');

  function drawRadar() {
    const box = $('radar-box');
    if (box.hidden || box.classList.contains('collapsed')) return;
    const ctx = canvas.getContext('2d');
    const w = canvas.width, h = canvas.height;
    ctx.clearRect(0, 0, w, h);
    ctx.fillStyle = '#0b100c';
    ctx.fillRect(0, 0, w, h);
    ctx.strokeStyle = '#1d2a1f';
    ctx.lineWidth = 1;
    for (let r = 40; r < w / 2; r += 40) { ctx.beginPath(); ctx.arc(w / 2, h / 2, r, 0, Math.PI * 2); ctx.stroke(); }
    ctx.beginPath(); ctx.moveTo(w / 2, 0); ctx.lineTo(w / 2, h); ctx.moveTo(0, h / 2); ctx.lineTo(w, h / 2); ctx.stroke();
    const dim = positions.find(p => p.id === host.state().unit && p.known)?.dim ?? 0;
    radarPts = project(positions.filter(p => p.dim === dim), w, h, 22);
    $('radar-empty').hidden = radarPts.length > 0;
    ctx.font = '10px ui-monospace, monospace';
    for (const p of radarPts) {
      const s = host.statuses.get(p.id);
      if (!s) continue;
      const lvl = worst(alertsOf(s));
      const cur = p.id === host.state().unit;
      ctx.fillStyle = lvl === 'crit' ? '#ff5d5d' : lvl === 'warn' ? '#f0b040' : s.task ? '#5fd0ff' : '#8fe08f';
      ctx.beginPath(); ctx.arc(p.x, p.y, cur ? 6 : 4, 0, Math.PI * 2); ctx.fill();
      if (cur) { ctx.strokeStyle = '#fff'; ctx.lineWidth = 1.5; ctx.stroke(); }
      // Bedrock yaw 0 faces +z, 90 faces -x.
      const a = (p.yaw * Math.PI) / 180;
      ctx.strokeStyle = ctx.fillStyle; ctx.lineWidth = 1.5;
      ctx.beginPath(); ctx.moveTo(p.x, p.y); ctx.lineTo(p.x - Math.sin(a) * 10, p.y + Math.cos(a) * 10); ctx.stroke();
      ctx.fillStyle = '#c8d4c8';
      ctx.fillText(s.name || s.id, p.x + 8, p.y - 6);
    }
  }

  canvas.addEventListener('click', ev => {
    const r = canvas.getBoundingClientRect();
    const id = nearest(radarPts, ((ev.clientX - r.left) / r.width) * canvas.width, ((ev.clientY - r.top) / r.height) * canvas.height);
    if (id) host.focus(id);
  });

  function onPositions(list) {
    positions = list;
    drawRadar();
  }

  // ---- status bar ----

  function renderStatus() {
    const c = counts(list());
    const sel = host.statuses.get(host.state().unit);
    const bits = [
      `${c.online}/${c.total} online`,
      `${c.working} working`,
      `${c.idle} idle`,
    ];
    if (c.dead) bits.push(`${c.dead} dead`);
    if (c.offline) bits.push(`${c.offline} offline`);
    const sb = $('sb-fleet');
    sb.replaceChildren(...bits.map(t => el('span', { textContent: t })));
    const a = $('sb-alerts');
    a.textContent = c.alerts ? `${c.alerts} alert${c.alerts === 1 ? '' : 's'}` : 'all clear';
    a.className = c.alerts ? 'bad' : 'good';
    $('sb-unit').textContent = sel ? `${sel.name || sel.id} / ${host.state().view === 'wall' ? 'wall' : host.state().mode}` : '';
    const newest = Math.max(0, ...list().map(s => (s.seen ? Date.parse(s.seen) : 0)));
    $('sb-seen').textContent = newest ? `data ${ago(Date.now() - newest)} old` : '';
    $('sb-clock').textContent = new Date().toLocaleTimeString([], { hour12: false });
    document.title = (c.alerts ? `(${c.alerts}) ` : '') + 'Crew control';
  }
  setInterval(renderStatus, 1000);

  // ---- command palette and help ----

  let paletteEl = null;
  function closeOverlay() {
    if (!paletteEl) return false;
    paletteEl.remove();
    paletteEl = null;
    return true;
  }

  function paletteItems() {
    const items = [];
    const st = host.state();
    const ord = host.order();
    ord.forEach((id, i) => {
      const s = host.statuses.get(id);
      items.push({ label: `Focus ${s.name || id}`, keywords: `${id} unit ${i + 1}`, hint: badge(s), run: () => host.focus(id) });
    });
    items.push({ label: 'Show wall', keywords: 'view grid', hint: 'w', run: () => host.setView('wall') });
    items.push({ label: 'Show fleet overview', keywords: 'view table units list', hint: 'o', run: () => host.setView('fleet') });
    items.push({ label: 'Show dashboard', keywords: 'view panels charts metrics', hint: 'b', run: () => host.setView('dash') });
    items.push({ label: 'Show job graph', keywords: 'view dag steps jobs', hint: 'g', run: () => host.setView('jobs') });
    items.push({ label: 'Show event explorer', keywords: 'view events filter search history log', hint: 'x', run: () => host.setView('events') });
    items.push({ label: 'Show replay', keywords: 'view history scrub positions past map', hint: 'y', run: () => host.setView('replay') });
    items.push({ label: 'Show timeline', keywords: 'view history swim lanes tasks jobs', hint: 't', run: () => host.setView('timeline') });
    items.push({ label: 'Auto-director: toggle', keywords: 'follow camera interesting', hint: 'Shift-D', run: () => host.director() });
    items.push({ label: 'Show focus view', keywords: 'view single', run: () => host.setView('focus') });
    for (const m of MODES) items.push({ label: `Camera: ${m}`, keywords: 'mode view', hint: m === st.mode ? 'current' : '', run: () => host.setMode(m) });
    items.push({ label: 'Next camera mode', hint: 'c', run: () => host.setMode(nextMode(st.mode)) });
    items.push({ label: 'Next unit', hint: 'j', run: () => host.focus(step(ord, st.unit, 1)) });
    items.push({ label: 'Previous unit', hint: 'k', run: () => host.focus(step(ord, st.unit, -1)) });
    items.push({ label: 'Jump to next alert', keywords: 'problem', hint: 'a', run: jumpAlert });
    for (const b of budgetsFor(ord.length)) {
      items.push({ label: b === 0 ? 'Wall: status cards only' : b >= ALL ? 'Wall: all feeds live' : `Wall: ${b} live feeds`, keywords: 'budget cameras', run: () => { prefs.budget = b; eased = 0; wallPage = 0; save(); host.setView('wall'); } });
    }
    items.push({ label: 'Toggle roster', hint: 'r', run: () => toggle('side') });
    items.push({ label: 'Toggle event feed', hint: 'e', run: () => toggle('dock') });
    if (host.operator()) items.push({ label: 'Toggle radar', hint: 'm', run: () => $('t-radar').click() });
    items.push({ label: pin === st.unit ? 'Unpin picture-in-picture' : 'Pin this unit (picture-in-picture)', hint: 'p', run: togglePin });
    items.push({ label: 'Kiosk mode (hide chrome)', hint: 'f', run: () => { kiosk = true; applyLayout(); } });
    items.push({ label: 'Keyboard help', hint: '?', run: openHelp });
    if (host.operator()) {
      const cur = host.statuses.get(st.unit);
      if (cur) items.push({ label: `Stop ${cur.name || cur.id}`, keywords: 'halt cancel operator', hint: 'operator', run: () => stopUnits([st.unit]) });
      if (picked.size) items.push({ label: `Stop ${picked.size} selected units`, keywords: 'halt bulk operator', hint: 'operator', run: () => stopUnits([...picked]) });
      if (picked.size) {
        items.push({ label: `Recall ${picked.size} selected units`, keywords: 'bulk walk goto operator', hint: 'operator', run: () => bulk('recall') });
        items.push({ label: `Assign a task to ${picked.size} selected units`, keywords: 'bulk gather haul operator', hint: 'operator', run: () => bulk('assign') });
      }
      items.push({ label: 'Stop all units', keywords: 'halt everything emergency operator', hint: 'operator', run: () => stopUnits(ord) });
      items.push({ label: 'Select all in roster', keywords: 'operator', run: () => { ord.forEach(id => picked.add(id)); renderRoster(); } });
      items.push({ label: 'Clear roster selection', keywords: 'operator', run: () => { picked.clear(); renderRoster(); } });
    }
    return items;
  }

  function openPalette(seed) {
    closeOverlay();
    const input = el('input', { type: 'text', placeholder: 'Type a command or a unit name', value: seed, autocomplete: 'off', spellcheck: false });
    input.setAttribute('aria-label', 'Command palette');
    const ul = el('ul', { role: 'listbox' });
    const items = paletteItems();
    let shown = [];
    let at = 0;
    const draw = () => {
      shown = rankItems(items, input.value);
      at = Math.min(at, Math.max(0, shown.length - 1));
      ul.replaceChildren(...(shown.length ? shown.map((it, i) => {
        const li = el('li', { className: i === at ? 'on' : '' }, el('span', { textContent: it.label }), el('em', { textContent: it.hint || '' }));
        li.setAttribute('role', 'option');
        li.addEventListener('mousedown', ev => { ev.preventDefault(); run(it); });
        return li;
      }) : [el('li', { className: 'empty', textContent: 'no match' })]));
    };
    const run = it => { closeOverlay(); it.run(); };
    input.addEventListener('input', () => { at = 0; draw(); });
    input.addEventListener('keydown', ev => {
      if (ev.key === 'Escape') { ev.preventDefault(); closeOverlay(); }
      else if (ev.key === 'ArrowDown' || (ev.ctrlKey && ev.key === 'n')) { ev.preventDefault(); at = Math.min(at + 1, shown.length - 1); draw(); }
      else if (ev.key === 'ArrowUp' || (ev.ctrlKey && ev.key === 'p')) { ev.preventDefault(); at = Math.max(at - 1, 0); draw(); }
      else if (ev.key === 'Enter' && shown[at]) { ev.preventDefault(); run(shown[at]); }
    });
    paletteEl = el('div', { id: 'overlay' }, el('div', { className: 'palette' }, input, ul));
    paletteEl.addEventListener('mousedown', ev => { if (ev.target === paletteEl) closeOverlay(); });
    document.body.append(paletteEl);
    draw();
    input.focus();
  }

  function openHelp() {
    closeOverlay();
    const dl = el('dl', {});
    for (const [k, d] of KEYS) dl.append(el('dt', {}, el('kbd', { textContent: k })), el('dd', { textContent: d }));
    const card = el('div', { className: 'palette help', role: 'dialog' }, el('h2', { textContent: 'Keyboard' }), dl);
    paletteEl = el('div', { id: 'overlay' }, card);
    paletteEl.addEventListener('mousedown', ev => { if (ev.target === paletteEl) closeOverlay(); });
    document.body.append(paletteEl);
  }

  function jumpAlert() {
    const id = nextAlerted(list(), host.state().unit);
    if (id) host.focus(id);
    else toast({ level: 'ok', name: 'Fleet', text: 'no unit has an alert' });
  }

  // ---- operator actions ----

  function bulk(action) {
    if (!host.operator() || !picked.size) return;
    closeOverlay();
    paletteEl = openBulk({
      api: host.api,
      name: id => host.statuses.get(id)?.name || id,
      expired: () => {
        toast({ level: 'warn', name: 'Fleet', text: 'you were signed out' });
        host.expired();
      },
      done: r => {
        toast({ level: r.failed ? 'crit' : 'ok', name: action === 'recall' ? 'Recall' : 'Assign', text: r.text });
        picked.clear();
        renderRoster();
      },
    }, action, [...picked], closeOverlay);
  }

  async function stopUnits(ids) {
    if (!host.operator() || !ids.length) return;
    const names = ids.map(id => host.statuses.get(id)?.name || id);
    const what = ids.length > 3 ? `${ids.length} units` : names.join(', ');
    if (!confirm(`Stop ${what}? Their queued work is cancelled.`)) return;
    const results = await Promise.all(ids.map(id => host.api('POST', `/v1/work/${encodeURIComponent(id)}/stop`, {})));
    if (results.some(r => r.status === 401)) {
      toast({ level: 'warn', name: 'Fleet', text: 'you were signed out' });
      host.expired();
      return;
    }
    const bad = results.filter(r => !r.ok).length;
    toast(bad
      ? { level: 'crit', name: 'Stop', text: `${bad} of ${ids.length} failed` }
      : { level: 'ok', name: 'Stop', text: `sent to ${what}` });
    picked.clear();
    renderRoster();
  }

  // ---- keys ----

  // Returns true when the key was ours.
  function key(ev) {
    if ((ev.ctrlKey || ev.metaKey) && ev.key.toLowerCase() === 'k') {
      ev.preventDefault();
      openPalette('');
      return true;
    }
    if (ev.ctrlKey || ev.metaKey || ev.altKey) return false;
    if (ev.key === 'Escape') {
      if (closeOverlay()) return true;
      if (kiosk) { kiosk = false; applyLayout(); return true; }
      return false;
    }
    const st = host.state();
    const ord = host.order();
    switch (ev.key) {
      case ':': case '/': ev.preventDefault(); openPalette(''); return true;
      case '?': openHelp(); return true;
      case 'j': host.focus(step(ord, st.unit, 1)); return true;
      case 'k': host.focus(step(ord, st.unit, -1)); return true;
      case 'a': jumpAlert(); return true;
      case 'r': toggle('side'); return true;
      case 'e': toggle('dock'); return true;
      case 'm': if (host.operator()) $('t-radar').click(); return true;
      case 'p': togglePin(); return true;
      case 'f': kiosk = !kiosk; applyLayout(); return true;
      case ',': wallStep(-1); return true;
      case '.': wallStep(1); return true;
      default: return false;
    }
  }

  // ---- hooks for console.js ----

  function onUnits() {
    renderRoster();
    refreshTiles();
    renderStatus();
    renderPip();
    drawRadar();
    if (!host.operator()) picked.clear();
  }

  const governor = createGovernor();
  function watchFrames(t) {
    requestAnimationFrame(watchFrames);
    const order = host.order();
    const pg = host.state().view === 'wall' && document.visibilityState === 'visible' ? pageOf(order, budget(), wallPage) : null;
    // Only live tiles cost enough to shed, and only the focused wall counts.
    if (!pg || !pg.live || !governor.frame(t)) { if (!pg || !pg.live) governor.reset(); return; }
    const next = lowerBudget(budgetsFor(order.length), budget(), ALL);
    if (!next) return;
    eased = next;
    wallPage = 0;
    governor.reset();
    host.setView('wall');
  }
  requestAnimationFrame(watchFrames);

  applyLayout();
  renderFeed();

  return {
    key,
    renderWall,
    onUnits,
    onEvents,
    onPositions,
    // Called after the focused unit, mode or view changed.
    onState() { renderRoster(); renderPip(); renderStatus(); renderFeed(); drawRadar(); applyLayout(); },
    onOperator() {
      if (!host.operator()) { positions = []; picked.clear(); }
      applyLayout();
      renderRoster();
      drawRadar();
    },
  };
}
