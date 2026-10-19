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
    const frame = focusFrame = el('iframe', { src: tileSrc(state.unit, state.mode), title: state.unit });
    // A message posted before the page has registered its listener is lost,
    // so the mode is re-sent once it has loaded. The same-origin window also
    // gets the console's keys, which a focused tile would otherwise swallow.
    frame.addEventListener('load', () => {
      if (focusFrame !== frame) return;
      frame.contentWindow.postMessage({ type: 'crew-camera', mode: state.mode }, location.origin);
      frame.contentWindow.addEventListener('keydown', onKey);
    });
    stage.append(frame);
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
  // Rebuilding the frame would re-attach a viewer and replay the whole world.
  if (state.unit === id && state.view === 'focus') return;
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

function onKey(ev) {
  if (ev.ctrlKey || ev.metaKey || ev.altKey) return;
  if (/^[1-9]$/.test(ev.key)) pickUnit(order[Number(ev.key) - 1]);
  else if (ev.key === 'c') setMode(nextMode(state.mode));
  else if (ev.key === 'w') toggleWall();
}
addEventListener('keydown', onKey);

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

const BACKOFF_MIN = 2000, BACKOFF_MAX = 30000;
let backoff = BACKOFF_MIN;

function connect() {
  const es = new EventSource('/v1/units/stream');
  es.addEventListener('units', ev => {
    backoff = BACKOFF_MIN;
    $('link').textContent = '';
    onUnits(JSON.parse(ev.data));
  });
  es.onerror = () => {
    $('link').textContent = 'status offline';
    // The browser retries a dropped stream itself, but gives up for good after
    // a non-200 answer (client cap, gateway restart), so those are rebuilt.
    if (es.readyState !== EventSource.CLOSED) return;
    es.close();
    setTimeout(connect, backoff);
    backoff = Math.min(backoff * 2, BACKOFF_MAX);
  };
}
connect();
