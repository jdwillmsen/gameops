// Pure fleet logic for the ops console: no DOM, so node can test it.

export const LOW = 6;
export const FEED_CAP = 300;
// 0 is status cards only; ALL runs a camera in every tile.
export const ALL = 999;
export const BUDGETS = [0, 4, 6, 9, 12, ALL];

export const budgetLabel = b => (b === 0 ? 'Cards' : b >= ALL ? 'All' : String(b));
export const budgetsFor = n => BUDGETS.filter(b => b === 0 || b >= ALL || b < n);

// ---- alerts and counts ----

// Alerts are derived from the status alone, so every viewer sees the same.
export function alertsOf(s) {
  if (!s.online) return [{ kind: 'offline', level: 'crit', text: 'offline' }];
  const out = [];
  if (s.dead) out.push({ kind: 'dead', level: 'crit', text: 'dead' });
  if (!s.dead && typeof s.health === 'number' && s.health <= LOW) out.push({ kind: 'low_health', level: 'warn', text: 'low health' });
  if (typeof s.hunger === 'number' && s.hunger <= LOW) out.push({ kind: 'low_hunger', level: 'warn', text: 'hungry' });
  return out;
}

export const worst = alerts => (alerts.some(a => a.level === 'crit') ? 'crit' : alerts.length ? 'warn' : '');

export function counts(list) {
  const c = { total: list.length, online: 0, working: 0, idle: 0, dead: 0, offline: 0, alerts: 0 };
  for (const s of list) {
    if (!s.online) c.offline++;
    else {
      c.online++;
      if (s.dead) c.dead++;
      else if (s.task) c.working++;
      else c.idle++;
    }
    if (alertsOf(s).length) c.alerts++;
  }
  return c;
}

export function taskLine(s) {
  if (!s.online) return '-';
  if (!s.task) return 'idle';
  const t = s.task;
  const what = t.template.replace('gather_and_deposit', 'gather');
  return t.qty ? `${what} ${t.progress}/${t.qty}` : what;
}

// ---- sorting and stepping ----

export const SORTS = ['unit', 'name', 'health', 'hunger', 'task', 'alerts'];

const rank = s => (alertsOf(s).some(a => a.level === 'crit') ? 0 : alertsOf(s).length ? 1 : 2);

export function sortUnits(list, key, desc = false) {
  const idx = new Map(list.map((s, i) => [s.id, i]));
  const num = v => (typeof v === 'number' ? v : Infinity);
  const by = {
    unit: s => idx.get(s.id),
    name: s => (s.name || s.id).toLowerCase(),
    health: s => num(s.health),
    hunger: s => num(s.hunger),
    task: s => taskLine(s),
    alerts: s => rank(s),
  }[key] || (s => idx.get(s.id));
  const out = [...list].sort((a, b) => {
    const x = by(a), y = by(b);
    return x < y ? -1 : x > y ? 1 : idx.get(a.id) - idx.get(b.id);
  });
  return desc ? out.reverse() : out;
}

// Wraps, so the last unit steps to the first.
export function step(order, cur, delta) {
  if (!order.length) return '';
  const i = order.indexOf(cur);
  return order[(((i < 0 ? 0 : i + delta) % order.length) + order.length) % order.length];
}

// The next unit after cur that has an alert, worst first; '' when none.
export function nextAlerted(list, cur) {
  const bad = list.filter(s => alertsOf(s).length);
  if (!bad.length) return '';
  const ids = bad.sort((a, b) => rank(a) - rank(b)).map(s => s.id);
  const i = ids.indexOf(cur);
  return ids[(i + 1) % ids.length];
}

// ---- the wall ----

// Fits n tiles of the given aspect into w x h with the least wasted area.
export function gridFor(n, w, h, aspect = 16 / 9) {
  if (n <= 0) return { cols: 1, rows: 1 };
  let best = { cols: 1, rows: n, tile: 0 };
  for (let cols = 1; cols <= n; cols++) {
    const rows = Math.ceil(n / cols);
    const tw = Math.min(w / cols, (h / rows) * aspect);
    if (tw > best.tile) best = { cols, rows, tile: tw };
  }
  return { cols: best.cols, rows: best.rows };
}

// budget is how many tiles run a live camera; 0 shows status cards only.
export function pageOf(order, budget, page) {
  if (budget <= 0 || budget >= order.length) return { ids: order, pages: 1, page: 0, live: budget !== 0 };
  const pages = Math.ceil(order.length / budget);
  const p = Math.min(Math.max(page, 0), pages - 1);
  return { ids: order.slice(p * budget, p * budget + budget), pages, page: p, live: true };
}

// ---- the event feed ----

// The backlog and the live line can both deliver an event; seq tells them apart.
export function mergeEvents(feed, incoming, cap = FEED_CAP) {
  const have = new Set(feed.map(e => e.seq));
  const add = incoming.filter(e => !have.has(e.seq));
  if (!add.length) return { feed, added: [] };
  const next = [...feed, ...add].sort((a, b) => a.seq - b.seq);
  return { feed: next.slice(-cap), added: add };
}

export function filterEvents(feed, { unit = '', level = 'all' } = {}) {
  return feed.filter(e => (!unit || e.unit === unit) && (level === 'all' || (level === 'warn' ? e.level === 'warn' || e.level === 'crit' : e.level === level)));
}

export const isToast = e => e.level === 'crit' || e.level === 'warn';

export function ago(ms) {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  return `${Math.floor(s / 3600)}h`;
}

// ---- the radar ----

// Fits the known positions of one dimension into a w x h box, keeping the
// scale equal on both axes so distances read true. North (-z) is up.
export function project(positions, w, h, pad = 16, min = 32) {
  const pts = positions.filter(p => p.known);
  if (!pts.length) return [];
  const xs = pts.map(p => p.x), zs = pts.map(p => p.z);
  const minX = Math.min(...xs), maxX = Math.max(...xs), minZ = Math.min(...zs), maxZ = Math.max(...zs);
  const span = Math.max(maxX - minX, maxZ - minZ, min);
  const scale = (Math.min(w, h) - pad * 2) / span;
  const cx = (minX + maxX) / 2, cz = (minZ + maxZ) / 2;
  return pts.map(p => ({ id: p.id, x: w / 2 + (p.x - cx) * scale, y: h / 2 + (p.z - cz) * scale, yaw: p.yaw }));
}

export function nearest(points, x, y, within = 14) {
  let best = null, bd = within * within;
  for (const p of points) {
    const d = (p.x - x) ** 2 + (p.y - y) ** 2;
    if (d <= bd) { best = p; bd = d; }
  }
  return best ? best.id : '';
}

// ---- the command palette ----

// Subsequence match: every query letter must appear in order. Returns -1 for
// no match, otherwise a score where earlier and tighter matches are higher.
export function fuzzy(query, text) {
  const q = query.toLowerCase().replace(/\s+/g, ''), t = text.toLowerCase();
  if (!q) return 0;
  let score = 0, ti = 0, last = -2;
  for (const ch of q) {
    const at = t.indexOf(ch, ti);
    if (at < 0) return -1;
    score += at === last + 1 ? 3 : 1;
    if (at === 0 || ' -_:/'.includes(t[at - 1])) score += 2;
    last = at;
    ti = at + 1;
  }
  return score - t.length * 0.01;
}

export function rankItems(items, query, limit = 12) {
  const scored = [];
  for (const it of items) {
    const sc = fuzzy(query, `${it.label} ${it.keywords || ''}`);
    if (sc >= 0) scored.push({ it, sc });
  }
  scored.sort((a, b) => b.sc - a.sc);
  return scored.slice(0, limit).map(x => x.it);
}

export const KEYS = [
  ['1-9', 'focus unit by number'],
  ['j / k', 'next / previous unit'],
  ['a', 'jump to the next unit with an alert'],
  ['c', 'cycle camera mode'],
  ['w', 'toggle wall'],
  [', / .', 'wall: previous / next page'],
  ['r', 'roster'],
  ['e', 'event feed'],
  ['m', 'radar (signed in)'],
  ['Shift-J', 'jobs and places (signed in)'],
  ['Shift-S', 'squad panel'],
  ['p', 'pin / unpin picture-in-picture'],
  ['f', 'kiosk: hide the chrome'],
  ['Ctrl-K or :', 'command palette'],
  ['?', 'this help'],
  ['Esc', 'close overlay'],
];
