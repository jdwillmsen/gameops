import { MODES, parse, format, tileSrc, wallSrc, badge, nextMode } from '/static/state.js';

const statuses = new Map();
let order = [];
let state = parse(location.search, []);
let focusFrame = null;

const $ = id => document.getElementById(id);
const stage = $('stage');

function el(tag, props = {}, ...kids) {
  const e = Object.assign(document.createElement(tag), props);
  e.append(...kids);
  return e;
}

function sync() {
  history.replaceState(null, '', '?' + format(state));
}

function renderBar() {
  const units = $('units');
  units.replaceChildren(...order.map((id, i) => {
    const s = statuses.get(id);
    const b = el('button', { type: 'button', title: `Key ${i + 1}` }, s.name || id, el('span', { className: 'badge ' + (!s.online ? 'offline' : s.dead ? 'dead' : ''), textContent: badge(s) }));
    b.setAttribute('aria-pressed', String(state.unit === id && state.view === 'focus'));
    b.addEventListener('click', () => pickUnit(id));
    return b;
  }));
  $('modes').replaceChildren(...MODES.map(m => {
    const b = el('button', { type: 'button', textContent: m[0].toUpperCase() + m.slice(1) });
    b.setAttribute('aria-pressed', String(state.mode === m));
    b.addEventListener('click', () => setMode(m));
    return b;
  }));
  $('wall-toggle').setAttribute('aria-pressed', String(state.view === 'wall'));
}

function renderStage() {
  // Frames are removed, not hidden, so a camera websocket closes as soon as
  // its tile leaves the screen.
  stage.replaceChildren();
  focusFrame = null;
  stage.className = state.view;
  if (!state.unit) return;
  if (state.view === 'focus') {
    focusFrame = el('iframe', { src: tileSrc(state.unit, state.mode), title: state.unit });
    stage.append(focusFrame);
    return;
  }
  stage.append(...order.map(id => {
    const tile = el('div', { className: 'tile' });
    tile.dataset.unit = id;
    const cover = el('button', { type: 'button', className: 'cover', title: 'Focus this unit' });
    cover.addEventListener('click', () => { state.unit = id; state.view = 'focus'; apply(); });
    tile.append(el('iframe', { src: wallSrc(id), title: id, loading: 'lazy' }), cover, el('div', { className: 'label' }));
    return tile;
  }));
  renderLabels();
}

function renderLabels() {
  for (const tile of stage.querySelectorAll('.tile')) {
    const s = statuses.get(tile.dataset.unit);
    if (!s) continue;
    tile.classList.toggle('offline', !s.online);
    tile.querySelector('.label').textContent = `${s.name || s.id}  ${badge(s)}`;
  }
}

function apply() {
  sync();
  renderBar();
  renderStage();
}

function pickUnit(id) {
  if (!statuses.has(id)) return;
  state.unit = id;
  state.view = 'focus';
  apply();
}

function setMode(mode) {
  state.mode = mode;
  sync();
  renderBar();
  if (focusFrame && focusFrame.contentWindow) {
    focusFrame.contentWindow.postMessage({ type: 'crew-camera', mode }, location.origin);
  }
}

function toggleWall() {
  state.view = state.view === 'wall' ? 'focus' : 'wall';
  apply();
}

$('wall-toggle').addEventListener('click', toggleWall);

addEventListener('keydown', ev => {
  if (ev.ctrlKey || ev.metaKey || ev.altKey) return;
  if (/^[1-9]$/.test(ev.key)) pickUnit(order[Number(ev.key) - 1]);
  else if (ev.key === 'c') setMode(nextMode(state.mode));
  else if (ev.key === 'w') toggleWall();
});

function onUnits(list) {
  const first = order.length === 0;
  order = list.map(s => s.id);
  statuses.clear();
  for (const s of list) statuses.set(s.id, s);
  if (first) {
    state = parse(location.search, order);
    apply();
    return;
  }
  if (!statuses.has(state.unit)) state = { ...parse('', order), mode: state.mode, view: state.view };
  renderBar();
  renderLabels();
}

const es = new EventSource('/v1/units/stream');
es.addEventListener('units', ev => {
  $('link').textContent = '';
  onUnits(JSON.parse(ev.data));
});
es.onerror = () => { $('link').textContent = 'status offline'; };
