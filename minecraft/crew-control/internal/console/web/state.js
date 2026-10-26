export const MODES = ['first', 'third', 'orbit', 'top'];
export const VIEWS = ['focus', 'wall', 'fleet', 'detail', 'jobs', 'events'];

const idOf = u => (typeof u === 'string' ? u : u.id);

// A job id is a uuid; anything else is a typo to ignore, not a link to fetch.
const jobParam = v => (/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(v) ? v : '');

export function parse(search, units) {
  const q = new URLSearchParams(search);
  const ids = units.map(idOf);
  const unit = ids.includes(q.get('unit')) ? q.get('unit') : (ids[0] ?? '');
  const mode = MODES.includes(q.get('mode')) ? q.get('mode') : MODES[0];
  const view = VIEWS.includes(q.get('view')) ? q.get('view') : VIEWS[0];
  return { unit, mode, view, job: jobParam(q.get('job') || '') };
}

export function format({ unit, mode, view, job }) {
  const q = new URLSearchParams({ unit, mode, view });
  // The job belongs to the jobs view; the address must not keep naming it
  // once the operator has left.
  if (job && view === 'jobs') q.set('job', job);
  return q.toString();
}

export const tileSrc = (id, mode) => `/units/${encodeURIComponent(id)}/camera?mode=${mode}`;
export const wallSrc = id => `/units/${encodeURIComponent(id)}/camera?mode=first&hud=0&r=2`;

export function badge(s) {
  if (!s.online) return 'offline';
  if (s.dead) return 'dead';
  const hp = typeof s.health === 'number' ? `hp ${Math.round(s.health)} ` : '';
  return hp + (s.task ? `${s.task.template} ${s.task.state}` : 'idle');
}

export function nextMode(mode) {
  return MODES[(MODES.indexOf(mode) + 1) % MODES.length];
}

// Mirrors the limits crew-control enforces; the server stays the authority,
// these only save a round trip for a typo.
const XZ = [-500, 499], Y = [-64, 320], QTY = [1, 256];
export const GATHER_ITEMS = ['cobblestone', 'oak_log'];

const whole = s => (/^-?\d+$/.test(String(s).trim()) ? Number(s) : NaN);
const within = (n, [lo, hi]) => n >= lo && n <= hi;

export function readPoint(raw) {
  const x = whole(raw.x), y = whole(raw.y), z = whole(raw.z);
  if (Number.isNaN(x) || Number.isNaN(y) || Number.isNaN(z)) return { error: 'x, y and z must be whole numbers' };
  if (!within(x, XZ) || !within(z, XZ)) return { error: `x and z must be ${XZ[0]} to ${XZ[1]}` };
  if (!within(y, Y)) return { error: `y must be ${Y[0]} to ${Y[1]}` };
  return { point: { x, y, z } };
}

export function gotoBody(raw) {
  const r = readPoint(raw);
  return r.error ? r : { body: r.point };
}

export function taskBody(form) {
  if (!GATHER_ITEMS.includes(form.item)) return { error: 'unknown item' };
  const qty = whole(form.qty);
  if (Number.isNaN(qty) || !within(qty, QTY)) return { error: `qty must be ${QTY[0]} to ${QTY[1]}` };
  const chest = readPoint(form.chest);
  if (chest.error) return chest;
  if (form.template === 'gather') {
    return { body: { template: 'gather_and_deposit', item: form.item, qty, chest: chest.point } };
  }
  if (form.template === 'haul') {
    const from = readPoint(form.from);
    if (from.error) return from;
    return { body: { template: 'haul', items: { [form.item]: qty }, from: [from.point], to: chest.point } };
  }
  return { error: 'unknown template' };
}

// Floors, because a unit standing in block -4 reports -3.2.
export function startPoint(resp) {
  const s = resp && resp.state;
  if (!s || !s.position_known || !Array.isArray(s.position)) return null;
  const [x, y, z] = s.position.map(Math.floor);
  return { x, y, z };
}

const OPEN = ['queued', 'delivered', 'running'];

export function splitWork(items) {
  return {
    open: items.filter(i => OPEN.includes(i.state)),
    history: items.filter(i => !OPEN.includes(i.state)),
  };
}

export function describeItem(it) {
  const b = it.body;
  if (it.kind === 'command') return (b && b.name) || 'command';
  if (!b) return 'task';
  if (b.template === 'gather_and_deposit') return `gather ${b.item} x${b.qty}`;
  if (b.template === 'haul') return 'haul ' + Object.entries(b.items || {}).map(([k, n]) => `${k} x${n}`).join(', ');
  if (b.template === 'goto' && b.to) return `go to ${b.to.x} ${b.to.y} ${b.to.z}`;
  return b.template || 'task';
}

// A command (stop) is not undone from the queue: it is delivered at once and
// the unit reports it done.
export const canCancel = it => it.kind !== 'command';

// The invite is a credential, so it travels in the fragment, which browsers
// never send to a server or proxy.
export function signinParams(hash) {
  const q = new URLSearchParams(String(hash).replace(/^#/, ''));
  return { invite: q.get('invite') || '', operator: q.get('operator') || '' };
}

// The console's shortcut keys are plain letters and digits, so they must not
// fire while an operator types into the panel.
export function isTyping(t) {
  return !!t && (['INPUT', 'SELECT', 'TEXTAREA'].includes(t.tagName) || t.isContentEditable === true);
}

// Goal rows are (item, qty) pairs; the server sums nothing, so a repeated item
// is a typo to surface rather than a total to guess.
export function stockBody(place, rows) {
  if (!place) return { error: 'pick a chest' };
  const want = {};
  for (const r of rows) {
    if (!GATHER_ITEMS.includes(r.item)) return { error: 'unknown item' };
    const qty = whole(r.qty);
    if (Number.isNaN(qty) || !within(qty, QTY)) return { error: `qty must be ${QTY[0]} to ${QTY[1]}` };
    if (r.item in want) return { error: `${r.item} is listed twice` };
    want[r.item] = qty;
  }
  if (!Object.keys(want).length) return { error: 'add an item' };
  return { body: { kind: 'stock', place, want } };
}

export const PARALLEL_DEMO = { kind: 'plan', template: 'parallel-demo' };

export function seenAgo(iso, now = Date.now()) {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const s = Math.max(0, Math.floor((now - t) / 1000));
  if (s < 10) return 'seen just now';
  if (s < 60) return `seen ${s}s ago`;
  if (s < 3600) return `seen ${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `seen ${Math.floor(s / 3600)}h ago`;
  return `seen ${Math.floor(s / 86400)}d ago`;
}

export function stepCount(job) {
  const steps = job.steps || [];
  return { done: steps.filter(s => s.state === 'done').length, total: steps.length };
}

export const jobCancellable = job => job.state === 'running';

export function jobTitle(job) {
  const spec = job.spec || {};
  if (job.kind === 'plan') return spec.template || 'plan';
  if (job.kind === 'stock') {
    return `stock ${spec.place || ''}: ` + Object.entries(spec.want || {}).map(([k, n]) => `${k} x${n}`).join(', ');
  }
  return job.kind;
}

// A place keeps its kind and items on rename, so only the name travels from
// the form; a new place also needs an id and where it is.
export function placeBody(form) {
  const id = String(form.id || '').trim();
  const name = String(form.name || '').trim();
  if (!id) return { error: 'id is required' };
  if (!name) return { error: 'name is required' };
  const min = readPoint(form.min);
  if (min.error) return min;
  let max = min;
  if (form.kind === 'area') {
    max = readPoint(form.max);
    if (max.error) return max;
  } else if (form.kind !== 'chest') {
    return { error: 'kind must be chest or area' };
  }
  return { body: { id, name, kind: form.kind, box: { min: min.point, max: max.point } } };
}

export const renameBody = (place, name) => ({
  id: place.id, name: String(name).trim(), kind: place.kind, box: place.box, ...(place.items ? { items: place.items } : {}),
});

const STATES = ['waiting', 'queued', 'running', 'done', 'failed', 'cancelled'];

// States come from the server and end up in a class name, so only known ones
// are passed through.
export const knownState = s => (STATES.includes(s) ? s : 'unknown');
