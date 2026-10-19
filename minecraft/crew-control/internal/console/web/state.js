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
