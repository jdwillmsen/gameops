import { MODES, parse, format, tileSrc, wallSrc, badge, nextMode, gotoBody, taskBody, startPoint, splitWork, describeItem, isTyping, canCancel } from '/static/state.js';

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
    const b = el('button', { type: 'button', title: i < 10 ? `Key ${(i + 1) % 10}` : 'Keys [ and ]' }, s.name || id, el('span', { className: 'badge ' + (!s.online ? 'offline' : s.dead ? 'dead' : ''), textContent: badge(s) }));
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
  renderPanel();
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
  if (isTyping(ev.target)) return;
  if (ev.ctrlKey || ev.metaKey || ev.altKey) return;
  // 1-9 and 0 reach the first ten units; [ and ] step through all of them.
  if (/^[0-9]$/.test(ev.key)) pickUnit(order[(Number(ev.key) + 9) % 10]);
  else if (ev.key === '[' || ev.key === ']') {
    const at = order.indexOf(state.unit);
    pickUnit(order[(at + (ev.key === ']' ? 1 : order.length - 1)) % order.length]);
  }
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
  renderPanel();
}

const BACKOFF_MIN = 2000, BACKOFF_MAX = 30000;
let backoff = BACKOFF_MIN;
let source = null;
let retryTimer = null;

function connect() {
  const es = source = new EventSource('/v1/units/stream');
  es.addEventListener('units', ev => {
    backoff = BACKOFF_MIN;
    $('link').textContent = '';
    onUnits(JSON.parse(ev.data));
  });
  es.addEventListener('work', ev => {
    const w = JSON.parse(ev.data);
    work.set(w.unit, w.items || []);
    if (w.unit === state.unit) renderWork();
  });
  es.onerror = () => {
    if (source !== es) return;
    $('link').textContent = 'status offline';
    if (operator) checkSession();
    // The browser retries a dropped stream itself, but gives up for good after
    // a non-200 answer (client cap, gateway restart), so those are rebuilt.
    if (es.readyState !== EventSource.CLOSED) return;
    es.close();
    retryTimer = setTimeout(connect, backoff);
    backoff = Math.min(backoff * 2, BACKOFF_MAX);
  };
}

// A stream is signed in or not for its whole life, so a change of session
// needs a new one.
function reconnect() {
  clearTimeout(retryTimer);
  if (source) source.close();
  backoff = BACKOFF_MIN;
  connect();
}

// ---- operator session and command panel ----

const work = new Map();
let operator = null;
let authAvailable = true;
let panel = null;
let panelUnit = '';

async function api(method, path, body) {
  const init = { method, headers: {} };
  if (method === 'POST') {
    init.headers = { 'Content-Type': 'application/json', 'X-Crew-Request': '1' };
    init.body = JSON.stringify(body ?? {});
  }
  let res;
  try {
    res = await fetch(path, init);
  } catch {
    return { ok: false, status: 0, data: { error: 'could not reach the server' } };
  }
  let data = null;
  try { data = await res.json(); } catch { /* an error page has no JSON */ }
  return { ok: res.ok, status: res.status, data };
}

function setOperator(op) {
  const changed = (operator && operator.id) !== (op && op.id);
  operator = op;
  if (!op) {
    work.clear();
    // The next sign-in builds a fresh panel instead of reusing one whose
    // lists and message belong to the previous session.
    panel = null;
    panelUnit = '';
    $('panel').replaceChildren();
  }
  renderSession();
  renderPanel();
  if (changed) reconnect();
}

function renderSession() {
  const box = $('session');
  if (operator) {
    const out = el('button', { type: 'button', textContent: 'Sign out' });
    const fail = el('span', { className: 'session-error', role: 'alert' });
    out.addEventListener('click', async () => {
      fail.textContent = '';
      const r = await api('POST', '/auth/logout');
      // A refused sign-out leaves the session alive, so the panel must stay.
      if (!r.ok) {
        fail.textContent = 'Sign out failed.';
        return;
      }
      setOperator(null);
    });
    box.replaceChildren(el('span', { className: 'who', textContent: operator.name || operator.id }), out, fail);
  } else if (authAvailable) {
    box.replaceChildren(el('a', { href: '/signin', textContent: 'Sign in' }));
  } else {
    box.replaceChildren();
  }
}

// Only a definite answer changes the session: an unreachable server says
// nothing about whether the cookie is still good.
async function checkSession() {
  const r = await api('GET', '/auth/me');
  authAvailable = r.status !== 404;
  if (r.ok && r.data && r.data.operator) setOperator(r.data.operator);
  else if (r.status === 401) setOperator(null);
  else {
    renderSession();
    renderPanel();
  }
}

// A session can lapse or be revoked while the page sits open, and the stream
// just stops sending work without saying why.
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible' && operator) checkSession();
});
setInterval(() => { if (operator) checkSession(); }, 5 * 60 * 1000);

function numberField(label, value) {
  const input = el('input', { type: 'number', step: '1', value: value ?? '' });
  input.setAttribute('aria-label', label);
  return input;
}

function pointFields(title) {
  const x = numberField(title + ' x'), y = numberField(title + ' y'), z = numberField(title + ' z');
  const row = el('div', { className: 'point' }, el('span', { textContent: title }), x, y, z);
  return {
    row,
    read: () => ({ x: x.value, y: y.value, z: z.value }),
    set(p) { x.value = p.x; y.value = p.y; z.value = p.z; },
  };
}

function buildPanel() {
  const msg = el('p', { id: 'panel-msg', role: 'status' });
  const say = text => { msg.textContent = text; };

  const act = async (button, run) => {
    button.disabled = true;
    say('');
    try { await run(); } finally { button.disabled = false; }
  };

  // Every command answers the same way, so one place reports it.
  const send = async (path, body, done) => {
    const r = await api('POST', path, body);
    if (r.status === 401) {
      say('You were signed out.');
      setOperator(null);
      return;
    }
    if (!r.ok) {
      say((r.data && r.data.error) || `Failed (${r.status}).`);
      return;
    }
    say(done(r.data || {}));
  };

  const unitPath = suffix => `/v1/work/${encodeURIComponent(state.unit)}${suffix}`;

  const stop = el('button', { type: 'button', id: 'stop', className: 'danger', textContent: 'Stop' });
  stop.addEventListener('click', () => act(stop, () => send(unitPath('/stop'), {}, d =>
    d.cancelled ? `Stopped, cancelled ${d.cancelled} open.` : 'Stop sent.')));

  const go = pointFields('Go to');
  const goForm = el('form', { id: 'goto-form', noValidate: true }, go.row, el('button', { type: 'submit', textContent: 'Go' }));
  const here = el('button', { type: 'button', textContent: 'Here', title: 'Fill in the unit\'s current position' });
  here.addEventListener('click', () => prefill());
  goForm.append(here);
  goForm.addEventListener('submit', ev => {
    ev.preventDefault();
    const b = gotoBody(go.read());
    if (b.error) { say(b.error); return; }
    act(goForm.querySelector('[type=submit]'), () => send(unitPath('/goto'), b.body, () => 'Go to queued.'));
  });

  const kind = el('select', { id: 'template' },
    ...[['gather:cobblestone', 'Gather cobblestone'], ['gather:oak_log', 'Chop oak_log'], ['haul:cobblestone', 'Haul cobblestone'], ['haul:oak_log', 'Haul oak_log']]
      .map(([value, textContent]) => el('option', { value, textContent })));
  kind.setAttribute('aria-label', 'Template');
  const qty = el('input', { type: 'number', step: '1', min: '1', max: '256', value: '8' });
  qty.setAttribute('aria-label', 'Quantity');
  const from = pointFields('From chest');
  const chest = pointFields('Chest');
  const syncKind = () => {
    const hauling = kind.value.startsWith('haul');
    from.row.hidden = !hauling;
    chest.row.firstChild.textContent = hauling ? 'To chest' : 'Chest';
  };
  kind.addEventListener('change', syncKind);
  syncKind();
  const queue = el('form', { id: 'queue-form', noValidate: true }, kind, el('label', {}, 'Qty ', qty), from.row, chest.row,
    el('button', { type: 'submit', textContent: 'Queue' }));
  queue.addEventListener('submit', ev => {
    ev.preventDefault();
    const [template, item] = kind.value.split(':');
    const b = taskBody({ template, item, qty: qty.value, chest: chest.read(), from: from.read() });
    if (b.error) { say(b.error); return; }
    act(queue.querySelector('[type=submit]'), () => send(unitPath('/tasks'), b.body, () => 'Queued.'));
  });

  const openList = el('ul', { id: 'open-items' });
  const historyList = el('ul', { id: 'history-items' });

  const cancel = (button, id) => act(button, () => send(unitPath(`/items/${encodeURIComponent(id)}/cancel`), {}, d =>
    d.note || 'Cancelled.'));

  const title = el('h2', { id: 'panel-title' });

  return {
    root: [
      title,
      stop, goForm, queue, msg,
      el('h3', { textContent: 'Queue' }), openList,
      el('h3', { textContent: 'History' }), historyList,
    ],
    title,
    go, openList, historyList, cancel,
  };
}

async function prefill() {
  const unit = state.unit;
  const r = await api('GET', `/units/${encodeURIComponent(unit)}/v1/state`);
  if (!r.ok || unit !== state.unit || !panel) return;
  const p = startPoint(r.data);
  if (p) panel.go.set(p);
}

function renderPanel() {
  const aside = $('panel');
  const show = !!operator && state.view === 'focus' && !!state.unit && statuses.has(state.unit);
  aside.hidden = !show;
  if (!show) return;
  if (!panel) {
    panel = buildPanel();
    aside.replaceChildren(...panel.root);
  }
  panel.title.textContent = (statuses.get(state.unit).name || state.unit);
  if (panelUnit !== state.unit) {
    panelUnit = state.unit;
    prefill();
  }
  renderWork();
}

function itemRow(it, withCancel) {
  const bits = [describeItem(it), it.state];
  if (it.state === 'running' && it.progress) bits.push(`${it.progress}`);
  if (it.reason) bits.push(it.reason);
  const li = el('li', { className: 'item ' + it.state }, el('span', { textContent: bits.join(' - ') }));
  if (withCancel && canCancel(it)) {
    const b = el('button', { type: 'button', textContent: 'Cancel' });
    b.addEventListener('click', () => panel.cancel(b, it.id));
    li.append(b);
  }
  return li;
}

function renderWork() {
  if (!panel || $('panel').hidden) return;
  const { open, history } = splitWork(work.get(state.unit) || []);
  panel.openList.replaceChildren(...(open.length ? open.map(i => itemRow(i, true)) : [el('li', { className: 'empty', textContent: 'nothing queued' })]));
  panel.historyList.replaceChildren(...(history.length ? history.map(i => itemRow(i, false)) : [el('li', { className: 'empty', textContent: 'no history' })]));
}

// The stream does not wait for the session check; the check reconnects it if
// it finds an operator.
connect();
checkSession();
