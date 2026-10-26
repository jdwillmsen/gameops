// Pure logic for the event explorer: no DOM, so node can test it. The same
// filter runs here for signed-out viewers (over the public feed they already
// hold) and on the server for operators (over the stored history), so the
// two answer alike.

export const KINDS = ['online', 'offline', 'died', 'respawned', 'task_started', 'task_done', 'task_failed', 'task_cancelled', 'task_ended', 'stuck', 'low_health', 'low_hunger'];

// Every level has an icon and a word, so none rides on colour alone.
export const SEVERITIES = [
  { id: 'ok', label: 'OK', icon: '✓' },
  { id: 'info', label: 'Info', icon: 'ℹ' },
  { id: 'warn', label: 'Warn', icon: '▲' },
  { id: 'crit', label: 'Critical', icon: '✖' },
];
const RANK = Object.fromEntries(SEVERITIES.map((s, i) => [s.id, i]));
export const severityOf = id => SEVERITIES.find(s => s.id === id) || { id, label: id, icon: '?' };

// The server's range parameter is a Go duration, hence 168h for a week.
export const RANGES = [
  { id: '', label: 'All' },
  { id: '15m', label: '15 min' },
  { id: '1h', label: '1 hour' },
  { id: '6h', label: '6 hours' },
  { id: '24h', label: '24 hours' },
  { id: '168h', label: '7 days' },
];
const RANGE_MS = { '15m': 9e5, '1h': 36e5, '6h': 216e5, '24h': 864e5, '168h': 6048e5 };

export const SORTS = ['at', 'unit', 'kind', 'severity'];
export const PREFIX = 'f.';
export const PAGE = 100;
export const ROW_CAP = 1000;

export const emptyFilter = () => ({ unit: [], kind: [], severity: [], task: '', q: '', range: '', since: '', until: '', sort: 'at', dir: 'desc' });

const listOf = (v, allowed) => [...new Set((v || '').split(',').map(s => s.trim()).filter(s => s && (!allowed || allowed.includes(s))))];
const timeOf = v => (v && !Number.isNaN(Date.parse(v)) ? new Date(v).toISOString().replace('.000Z', 'Z') : '');

// readFilter takes a URLSearchParams-ish; names carry PREFIX so they never
// collide with the console's own unit, mode and view.
export function readFilter(search, prefix = PREFIX) {
  const q = new URLSearchParams(search);
  const get = k => q.get(prefix + k) || '';
  const f = emptyFilter();
  f.unit = listOf(get('unit'));
  f.kind = listOf(get('kind'));
  f.severity = listOf(get('severity'), SEVERITIES.map(s => s.id));
  f.task = get('task').trim();
  f.q = get('q').trim().slice(0, 200);
  f.range = RANGES.some(r => r.id === get('range')) ? get('range') : '';
  f.since = timeOf(get('since'));
  f.until = timeOf(get('until'));
  f.sort = SORTS.includes(get('sort')) ? get('sort') : 'at';
  f.dir = get('dir') === 'asc' ? 'asc' : 'desc';
  return f;
}

// writeParams is the one place a filter becomes parameters; defaults are
// left out so an untouched explorer has a clean address.
export function writeParams(f, prefix = '') {
  const p = new URLSearchParams();
  const put = (k, v) => { if (v && v.length) p.set(prefix + k, Array.isArray(v) ? v.join(',') : v); };
  put('unit', f.unit);
  put('kind', f.kind);
  put('severity', f.severity);
  put('task', f.task);
  put('q', f.q);
  put('since', f.since);
  put('until', f.until);
  // A custom start replaces the relative range, as on the server.
  if (!f.since) put('range', f.range);
  if (f.sort !== 'at') p.set(prefix + 'sort', f.sort);
  if (f.dir === 'asc') p.set(prefix + 'dir', 'asc');
  return p;
}

// formatFilter is the leading-& suffix the address bar carries.
export function formatFilter(f) {
  const s = writeParams(f, PREFIX).toString();
  return s ? `&${s}` : '';
}

// toServerQuery is what the history route and the saved views take.
export const toServerQuery = (f, extra = {}) => {
  const p = writeParams(f);
  for (const [k, v] of Object.entries(extra)) if (v !== '' && v != null) p.set(k, String(v));
  return p.toString();
};

export const sameFilter = (a, b) => JSON.stringify(a) === JSON.stringify(b);
export const isDefault = f => sameFilter(f, emptyFilter());

// ---- chips ----

export function chips(f) {
  const out = [];
  for (const k of ['unit', 'kind', 'severity']) {
    for (const v of f[k]) out.push({ key: k, value: v, label: `${k}: ${k === 'severity' ? severityOf(v).label : v}` });
  }
  if (f.task) out.push({ key: 'task', value: f.task, label: `task: ${f.task.slice(0, 8)}` });
  if (f.q) out.push({ key: 'q', value: f.q, label: `text: ${f.q}` });
  if (f.since || f.until) out.push({ key: 'time', value: '', label: `time: ${f.since ? f.since.slice(5, 16) : '...'} to ${f.until ? f.until.slice(5, 16) : 'now'}` });
  else if (f.range) out.push({ key: 'range', value: f.range, label: `last ${RANGES.find(r => r.id === f.range).label}` });
  return out;
}

export function dropChip(f, chip) {
  const n = { ...f, unit: [...f.unit], kind: [...f.kind], severity: [...f.severity] };
  if (['unit', 'kind', 'severity'].includes(chip.key)) n[chip.key] = n[chip.key].filter(v => v !== chip.value);
  else if (chip.key === 'time') { n.since = ''; n.until = ''; }
  else n[chip.key] = '';
  return n;
}

export const toggleIn = (list, v) => (list.includes(v) ? list.filter(x => x !== v) : [...list, v]);

// ---- rows ----

// A public feed event has no id of its own beyond its sequence and no
// payload; it becomes a row of the same shape as a stored one.
export const fromPublic = e => ({ id: e.seq, unit: e.unit, at: e.at, kind: e.kind, severity: e.level, payload: { text: e.text } });

export function detailText(row) {
  const p = row.payload;
  if (p && typeof p.text === 'string' && p.text) return p.text;
  const s = p && Object.keys(p).length ? JSON.stringify(p) : '';
  return s.length > 140 ? s.slice(0, 139) + '…' : s;
}

export function matchRow(row, f, now = Date.now()) {
  if (f.unit.length && !f.unit.includes(row.unit)) return false;
  if (f.kind.length && !f.kind.includes(row.kind)) return false;
  if (f.severity.length && !f.severity.includes(row.severity)) return false;
  if (f.task && row.task !== f.task) return false;
  const at = Date.parse(row.at);
  const from = f.since ? Date.parse(f.since) : f.range ? now - RANGE_MS[f.range] : 0;
  if (from && at < from) return false;
  if (f.until && at > Date.parse(f.until)) return false;
  if (f.q) {
    const needle = f.q.toLowerCase();
    const hay = `${row.unit} ${row.kind} ${row.task || ''} ${JSON.stringify(row.payload || {})}`.toLowerCase();
    if (!hay.includes(needle)) return false;
  }
  return true;
}

// sortRows mirrors the server: the key, then time, then id, all in the one
// direction, so a row never jumps between a local and a server answer.
export function sortRows(rows, f) {
  const key = r => (f.sort === 'severity' ? RANK[r.severity] ?? -1 : f.sort === 'unit' ? r.unit : f.sort === 'kind' ? r.kind : 0);
  const sign = f.dir === 'asc' ? 1 : -1;
  const cmp = (a, b) => (a < b ? -1 : a > b ? 1 : 0);
  return [...rows].sort((a, b) => sign * (cmp(key(a), key(b)) || cmp(Date.parse(a.at), Date.parse(b.at)) || cmp(a.id, b.id)));
}

export const filterAndSort = (rows, f, now) => sortRows(rows.filter(r => matchRow(r, f, now)), f);

// A live row can go straight to the top only when the list is newest-first
// by time; any other order would need a re-sort the reader did not ask for.
export const tailsToTop = f => f.sort === 'at' && f.dir === 'desc';

// mergeRows adds rows not already present (a reconnected tail repeats some),
// newest first by id, capped.
export function mergeRows(rows, incoming, cap = ROW_CAP) {
  const have = new Set(rows.map(r => r.id));
  const add = incoming.filter(r => !have.has(r.id));
  if (!add.length) return { rows, added: [] };
  return { rows: [...add, ...rows].slice(0, cap), added: add };
}

// Clicking a header sorts by it; clicking it again flips the direction. A
// text column starts ascending, time starts newest-first.
export function nextSort(f, col) {
  if (f.sort === col) return { ...f, dir: f.dir === 'asc' ? 'desc' : 'asc' };
  return { ...f, sort: col, dir: col === 'at' || col === 'severity' ? 'desc' : 'asc' };
}
