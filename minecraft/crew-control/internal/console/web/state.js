export const MODES = ['first', 'third', 'orbit', 'top'];
export const VIEWS = ['focus', 'wall'];

const idOf = u => (typeof u === 'string' ? u : u.id);

export function parse(search, units) {
  const q = new URLSearchParams(search);
  const ids = units.map(idOf);
  const unit = ids.includes(q.get('unit')) ? q.get('unit') : (ids[0] ?? '');
  const mode = MODES.includes(q.get('mode')) ? q.get('mode') : MODES[0];
  const view = VIEWS.includes(q.get('view')) ? q.get('view') : VIEWS[0];
  return { unit, mode, view };
}

export function format({ unit, mode, view }) {
  return new URLSearchParams({ unit, mode, view }).toString();
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
