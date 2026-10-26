import { initOps } from '/static/ops.js';
import { initSquads } from '/static/squad.js';
import { initAlerts } from '/static/alerts.js';
import { initOverview } from '/static/overview-ui.js';
import { initDetail } from '/static/detail-ui.js';
import { initContainers } from '/static/containers-ui.js';
import { initJobGraph } from '/static/graph/jobs-ui.js';
import { initExplorer } from '/static/explorer-ui.js';
import { initLedgerGraph } from '/static/graph/ledger-ui.js';
import { initTimeline } from '/static/timeline-ui.js';
import { step } from '/static/fleet.js';
import { MODES, parse, format, tileSrc, wallSrc, badge, nextMode, gotoBody, taskBody, startPoint, splitWork, describeItem, isTyping, canCancel, GATHER_ITEMS, PARALLEL_DEMO, stockBody, seenAgo, stepCount, jobCancellable, jobTitle, placeBody, renameBody, knownState } from '/static/state.js';

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
  history.replaceState(null, '', '?' + format(state) + overview.query() + explorer.query());
}

function renderBar() {
  const units = $('units');
  // Past nine units the roster is the picker; the bar keeps a stepper.
  if (order.length > 9) {
    const cur = statuses.get(state.unit);
    const prev = el('button', { type: 'button', textContent: '<', title: 'Previous unit (k)' });
    const next = el('button', { type: 'button', textContent: '>', title: 'Next unit (j)' });
    prev.addEventListener('click', () => pickUnit(step(order, state.unit, -1)));
    next.addEventListener('click', () => pickUnit(step(order, state.unit, 1)));
    units.replaceChildren(prev, el('span', { className: 'cur-unit', textContent: cur ? `${order.indexOf(cur.id) + 1}. ${cur.name || cur.id}` : '' }), next);
  } else units.replaceChildren(...order.map((id, i) => {
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
  $('fleet-toggle').setAttribute('aria-pressed', String(state.view === 'fleet'));
  $('detail-toggle').setAttribute('aria-pressed', String(state.view === 'detail'));
  $('jobgraph-toggle').setAttribute('aria-pressed', String(state.view === 'jobs'));
  $('explorer-toggle').setAttribute('aria-pressed', String(state.view === 'events'));
  $('ledger-toggle').setAttribute('aria-pressed', String(state.view === 'ledger'));
  $('timeline-toggle').setAttribute('aria-pressed', String(state.view === 'timeline'));
}

function renderStage() {
  overview.unmount();
  detail.unmount();
  jobGraph.unmount();
  explorer.unmount();
  ledgerGraph.unmount();
  timeline.unmount();
  // Frames are removed, not hidden, so a camera websocket closes as soon as
  // its tile leaves the screen.
  stage.replaceChildren();
  focusFrame = null;
  stage.className = state.view;
  if (state.view === 'jobs') {
    // The jobs view does not need a roster: it comes from the session and
    // the jobs stream, so it renders without a unit.
    jobGraph.mount(stage);
    return;
  }
  if (state.view === 'events') {
    // Like jobs, it needs no unit: it reads the history, or the public feed.
    explorer.mount(stage);
    return;
  }
  if (state.view === 'ledger') {
    ledgerGraph.mount(stage);
    return;
  }
  if (state.view === 'timeline') {
    timeline.mount(stage);
    return;
  }
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
  if (state.view === 'fleet') {
    overview.mount(stage);
    return;
  }
  if (state.view === 'detail') {
    // The operator question is answered by the page itself: signed-out
    // visitors see its sign-in state, not a console error.
    detail.mount(stage, state.unit);
    return;
  }
  ops.renderWall(stage);
}

function apply() {
  if (state.view !== 'jobs' && state.view !== 'timeline') state.job = '';
  sync();
  renderBar();
  renderStage();
  renderPanel();
  ops.onState();
}

function pickUnit(id) {
  if (!statuses.has(id)) return;
  // Rebuilding the frame would re-attach a viewer and replay the whole world.
  if (state.unit === id && state.view === 'focus') return;
  state.unit = id;
  state.view = 'focus';
  apply();
}

function openDetail(id) {
  if (!id || !statuses.has(id)) return;
  state.unit = id;
  state.view = 'detail';
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

function toggleFleet() {
  state.view = state.view === 'fleet' ? 'focus' : 'fleet';
  apply();
}

function toggleDetail() {
  state.view = state.view === 'detail' ? 'focus' : 'detail';
  apply();
}

function setView(view) {
  state.view = view;
  apply();
}

function toggleExplorer() {
  state.view = state.view === 'events' ? (state.unit ? 'focus' : 'fleet') : 'events';
  apply();
}

function toggleLedger() {
  state.view = state.view === 'ledger' ? (state.unit ? 'focus' : 'fleet') : 'ledger';
  apply();
}

function toggleTimeline() {
  state.view = state.view === 'timeline' ? (state.unit ? 'focus' : 'fleet') : 'timeline';
  apply();
}

function toggleJobs() {
  state.view = state.view === 'jobs' ? (state.unit ? 'focus' : 'fleet') : 'jobs';
  apply();
}

$('wall-toggle').addEventListener('click', toggleWall);
$('fleet-toggle').addEventListener('click', toggleFleet);
$('detail-toggle').addEventListener('click', toggleDetail);
$('jobgraph-toggle').addEventListener('click', toggleJobs);
$('explorer-toggle').addEventListener('click', toggleExplorer);
$('ledger-toggle').addEventListener('click', toggleLedger);
$('timeline-toggle').addEventListener('click', toggleTimeline);

function onKey(ev) {
  if ((ev.ctrlKey || ev.metaKey) && ev.key.toLowerCase() === 'k') { ops.key(ev); return; }
  if (isTyping(ev.target)) return;
  if (ev.ctrlKey || ev.metaKey || ev.altKey) return;
  if (overview.key(ev)) return;
  // 1-9 and 0 reach the first ten units; [ and ] step through all of them.
  if (/^[0-9]$/.test(ev.key)) pickUnit(order[(Number(ev.key) + 9) % 10]);
  else if (ev.key === '[' || ev.key === ']') {
    const at = order.indexOf(state.unit);
    pickUnit(order[(at + (ev.key === ']' ? 1 : order.length - 1)) % order.length]);
  }
  else if (ev.key === 'c') setMode(nextMode(state.mode));
  else if (ev.key === 'w') toggleWall();
  else if (ev.key === 'o') toggleFleet();
  else if (ev.key === 'd') toggleDetail();
  else if (ev.key === 'g') toggleJobs();
  else if (ev.key === 'x') toggleExplorer();
  else if (ev.key === 'l') toggleLedger();
  else if (ev.key === 't') toggleTimeline();
  // Shift+J, because plain j and k step through units.
  else if (ev.key === 'J' && operator) setJobsOpen(!jobsOpen);
  else if (ev.key === 'S') setSquadOpen(!squadOpen);
  else if (ev.key === 'A') alertsUI.toggle();
  // The jobs view keeps Esc for its own layers, but only when the ops shell
  // has no overlay open; ops still answers the overlay first.
  else if (ev.key === 'Escape' && state.view === 'jobs' && !document.getElementById('overlay') && jobGraph.key(ev)) return;
  else if (ev.key === 'Escape' && state.view === 'ledger' && !document.getElementById('overlay') && ledgerGraph.key(ev)) return;
  else if (ev.key === 'Escape' && state.view === 'timeline' && !document.getElementById('overlay') && timeline.key(ev)) return;
  else ops.key(ev);
}
addEventListener('keydown', onKey);

function onUnits(list) {
  const first = order.length === 0;
  order = list.map(s => s.id);
  statuses.clear();
  for (const s of list) statuses.set(s.id, s);
  overview.onUnits();
  if (first) {
    state = parse(location.search, order);
    apply();
    return;
  }
  if (!statuses.has(state.unit)) state = { ...parse('', order), mode: state.mode, view: state.view, job: state.job };
  renderBar();
  renderPanel();
  ops.onUnits();
  squads.onUnits();
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
  es.addEventListener('events', ev => {
    const list = JSON.parse(ev.data) || [];
    ops.onEvents(list, false);
    overview.onEvents(list, false);
    explorer.onEvents(list, false);
  });
  es.addEventListener('log', ev => {
    const e = JSON.parse(ev.data);
    ops.onEvents([e], true);
    overview.onEvents([e], true);
    explorer.onEvents([e], true);
  });
  es.addEventListener('positions', ev => {
    const list = JSON.parse(ev.data) || [];
    ops.onPositions(list);
    overview.onPositions(list);
  });
  es.addEventListener('work', ev => {
    const w = JSON.parse(ev.data);
    work.set(w.unit, w.items || []);
    if (w.unit === state.unit) renderWork();
  });
  es.addEventListener('jobs', ev => {
    jobs = JSON.parse(ev.data).jobs || [];
    renderJobs();
    jobGraph.onJobs();
    timeline.onJobs();
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
let jobs = [];
let places = [];
let jobsOpen = false;
let squadOpen = false;
let jobsUI = null;
let tab = 'jobs';
const expanded = new Set();

async function api(method, path, body, extra = {}) {
  const init = { method, headers: {}, ...extra };
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
    jobs = [];
    places = [];
    jobsUI = null;
    jobsOpen = false;
    state.job = '';
  }
  renderSession();
  renderPanel();
  ops.onOperator();
  overview.onOperator();
  detail.onOperator(op);
  jobGraph.onOperator();
  alertsUI.onSession();
  explorer.onOperator();
  ledgerGraph.onOperator();
  timeline.onOperator();
  renderJobs();
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

  const cancel = (button, id) => act(button, () => send(unitPath(`/items/${encodeURIComponent(id)}/cancel`), {}, () => 'Cancelled.'));

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

const ops = initOps({
  statuses,
  order: () => order,
  state: () => state,
  operator: () => operator,
  api,
  focus: pickUnit,
  detail: openDetail,
  setMode,
  setView,
  expired: () => setOperator(null),
});

const squads = initSquads({ statuses, focus: pickUnit });

const alertsUI = initAlerts({ operator: () => operator, focus: pickUnit, api, setOperator: next => setOperator(next) });

const overview = initOverview({
  statuses,
  order: () => order,
  state: () => state,
  operator: () => operator,
  focus: pickUnit,
  detail: openDetail,
  sync,
});

const detail = initDetail({
  statuses,
  state: () => state,
  operator: () => operator,
  api,
});

const explorer = initExplorer({
  statuses,
  order: () => order,
  state: () => state,
  operator: () => operator,
  api,
  sync,
});

const timeline = initTimeline({
  order: () => order,
  jobs: () => jobs,
  operator: () => operator,
  state: () => state,
  sync,
  api,
  setOperator: next => setOperator(next),
});

const jobGraph = initJobGraph({
  jobs: () => jobs,
  operator: () => operator,
  state: () => state,
  sync,
  api,
  // A sign-out mid-view has to flip the view's own state, not redo the
  // session logic here.
  setOperator: next => setOperator(next),
});

const ledgerGraph = initLedgerGraph({
  operator: () => operator,
  api,
  setOperator: next => setOperator(next),
});

const containers = initContainers({ pickUnit: id => pickUnit(id) });

function setSquadOpen(open) {
  squadOpen = open;
  squads.setOpen(open);
}

$('squad-toggle').addEventListener('click', () => setSquadOpen(!squadOpen));

// ---- jobs and places ----

function setJobsOpen(open) {
  jobsOpen = open;
  renderJobs();
  if (open) loadPlaces(true);
}

// A refresh rebuilds every row, so a timer-driven one waits while an operator
// is typing in the list; a refresh the operator asked for always runs.
async function loadPlaces(force = false) {
  if (!force && $('place-list') && $('place-list').contains(document.activeElement)) return;
  const r = await api('GET', '/v1/places');
  if (r.status === 401) { setOperator(null); return; }
  if (!r.ok) return;
  if (!force && $('place-list') && $('place-list').contains(document.activeElement)) return;
  places = r.data || [];
  renderPlaces();
  renderStockForm();
}

function buildJobs() {
  const msg = el('p', { id: 'jobs-msg', role: 'status' });
  const say = t => { msg.textContent = t; };
  // One path for every jobs/places write, so a refusal is worded the same way.
  const post = async (button, path, body, done) => {
    button.disabled = true;
    say('');
    try {
      const r = await api('POST', path, body);
      if (r.status === 401) { say('You were signed out.'); setOperator(null); return; }
      if (!r.ok) { say((r.data && r.data.error) || `Failed (${r.status}).`); return; }
      say(done);
      await loadPlaces(true);
    } finally { button.disabled = false; }
  };

  const tabs = el('nav', { id: 'jobs-tabs' });
  for (const [id, label] of [['jobs', 'Jobs'], ['places', 'Places'], ['containers', 'Chests']]) {
    const b = el('button', { type: 'button', textContent: label, id: `tab-${id}` });
    b.addEventListener('click', () => { tab = id; say(''); renderJobs(); loadPlaces(true); });
    tabs.append(b);
  }

  const demo = el('button', { type: 'button', id: 'start-demo', textContent: 'Parallel demo' });
  demo.addEventListener('click', () => post(demo, '/v1/jobs', PARALLEL_DEMO, 'Started.'));

  const placeSel = el('select', { id: 'stock-place' });
  placeSel.setAttribute('aria-label', 'Chest');
  const rows = el('div', { id: 'stock-rows' });
  const addRow = () => {
    const item = el('select', {}, ...GATHER_ITEMS.map(v => el('option', { value: v, textContent: v })));
    item.setAttribute('aria-label', 'Item');
    const qty = el('input', { type: 'number', step: '1', min: '1', max: '256', value: '32' });
    qty.setAttribute('aria-label', 'Quantity');
    const gone = el('button', { type: 'button', textContent: 'x', title: 'Remove' });
    const row = el('div', { className: 'stock-row' }, item, qty, gone);
    gone.addEventListener('click', () => row.remove());
    rows.append(row);
  };
  addRow();
  const more = el('button', { type: 'button', textContent: '+ item' });
  more.addEventListener('click', addRow);
  const goal = el('form', { id: 'stock-form', noValidate: true }, el('h3', { textContent: 'Stock goal' }), placeSel, rows, more,
    el('button', { type: 'submit', textContent: 'Start stock goal' }));
  goal.addEventListener('submit', ev => {
    ev.preventDefault();
    const r = stockBody(placeSel.value, [...rows.children].map(row => ({ item: row.children[0].value, qty: row.children[1].value })));
    if (r.error) { say(r.error); return; }
    post(goal.querySelector('[type=submit]'), '/v1/jobs', r.body, 'Started.');
  });

  const list = el('ul', { id: 'job-list' });
  const jobsView = el('section', { id: 'jobs-view' }, demo, goal, el('h3', { textContent: 'Jobs' }), list);

  const placeList = el('ul', { id: 'place-list' });
  const id = el('input', { id: 'new-id', placeholder: 'id' });
  id.setAttribute('aria-label', 'Place id');
  const name = el('input', { id: 'new-name', placeholder: 'name' });
  name.setAttribute('aria-label', 'Place name');
  const kind = el('select', { id: 'new-kind' }, el('option', { value: 'chest', textContent: 'chest' }), el('option', { value: 'area', textContent: 'area' }));
  kind.setAttribute('aria-label', 'Kind');
  const min = pointFields('Position');
  const max = pointFields('To');
  const syncKind = () => {
    max.row.hidden = kind.value !== 'area';
    min.row.firstChild.textContent = kind.value === 'area' ? 'From' : 'Position';
  };
  kind.addEventListener('change', syncKind);
  syncKind();
  const create = el('form', { id: 'place-form', noValidate: true }, el('h3', { textContent: 'New place' }), id, name, kind, min.row, max.row,
    el('button', { type: 'submit', textContent: 'Save place' }));
  create.addEventListener('submit', ev => {
    ev.preventDefault();
    const r = placeBody({ id: id.value, name: name.value, kind: kind.value, min: min.read(), max: max.read() });
    if (r.error) { say(r.error); return; }
    post(create.querySelector('[type=submit]'), '/v1/places', r.body, 'Saved.');
  });
  const placesView = el('section', { id: 'places-view' }, placeList, create);

  const root = el('div', { id: 'jobs-root' }, el('h2', { textContent: 'Jobs' }), tabs, msg, jobsView, placesView, containers.root);
  return { root, tabs, jobsView, placesView, containersView: containers.root, list, placeList, placeSel, rows, post, say };
}

function jobRow(job) {
  const { done, total } = stepCount(job);
  const open = expanded.has(job.id);
  const head = [jobTitle(job), job.state, `${done}/${total}`];
  if (job.reason) head.push(job.reason);
  const toggle = el('button', { type: 'button', className: 'link', textContent: head.join(' - ') });
  toggle.setAttribute('aria-expanded', String(open));
  toggle.addEventListener('click', () => {
    if (expanded.has(job.id)) expanded.delete(job.id); else expanded.add(job.id);
    renderJobs();
  });
  const li = el('li', { className: 'job ' + knownState(job.state) }, toggle);
  if (jobCancellable(job)) {
    const b = el('button', { type: 'button', textContent: 'Cancel' });
    b.addEventListener('click', () => jobsUI.post(b, `/v1/jobs/${encodeURIComponent(job.id)}/cancel`, {}, 'Cancelled.'));
    li.append(b);
  }
  const graph = el('button', { type: 'button', textContent: 'Graph', title: 'Open in the job graph (g)' });
  graph.addEventListener('click', () => {
    state.view = 'jobs';
    state.job = job.id;
    apply();
  });
  li.append(graph);
  if (open) {
    li.append(el('ul', { className: 'steps' }, ...(job.steps || []).map(s =>
      el('li', { className: 'step ' + knownState(s.state), textContent: [s.key, s.unit, s.state, s.reason].filter(Boolean).join(' - ') }))));
  }
  return li;
}

function renderJobs() {
  const aside = $('jobs');
  const show = !!operator && jobsOpen;
  aside.hidden = !show;
  $('jobs-toggle').hidden = !operator;
  $('jobs-toggle').setAttribute('aria-pressed', String(show));
  if (!show) return;
  if (!jobsUI) {
    jobsUI = buildJobs();
    aside.replaceChildren(jobsUI.root);
    renderStockForm();
  }
  jobsUI.jobsView.hidden = tab !== 'jobs';
  jobsUI.placesView.hidden = tab !== 'places';
  jobsUI.containersView.hidden = tab !== 'containers';
  for (const b of jobsUI.tabs.children) b.setAttribute('aria-pressed', String(b.id === `tab-${tab}`));
  jobsUI.list.replaceChildren(...(jobs.length ? jobs.map(jobRow) : [el('li', { className: 'empty', textContent: 'no jobs' })]));
}

function renderStockForm() {
  if (!jobsUI) return;
  const sel = jobsUI.placeSel;
  const keep = sel.value;
  sel.replaceChildren(...places.filter(p => p.kind === 'chest').map(p => el('option', { value: p.id, textContent: p.name || p.id })));
  if ([...sel.options].some(o => o.value === keep)) sel.value = keep;
}

function placeRow(p) {
  const head = el('div', { className: 'place-head' }, el('strong', { textContent: p.name || p.id }),
    el('span', { className: 'badge', textContent: `${p.kind}${p.seeded ? ' - config' : ''}` }));
  const li = el('li', { className: 'place' }, head);
  if (p.stock && p.stock.length) {
    li.append(el('ul', { className: 'stock' }, ...p.stock.map(s =>
      el('li', { textContent: `${s.item} x${s.count} - ${seenAgo(s.seen_at)}` }))));
  } else {
    li.append(el('div', { className: 'empty', textContent: 'no stock seen' }));
  }
  if (!p.seeded) {
    const name = el('input', { value: p.name || '' });
    name.setAttribute('aria-label', `Rename ${p.id}`);
    const save = el('button', { type: 'button', textContent: 'Rename' });
    save.addEventListener('click', () => {
      if (!name.value.trim()) { jobsUI.say('name is required'); return; }
      jobsUI.post(save, '/v1/places', renameBody(p, name.value), 'Renamed.');
    });
    const del = el('button', { type: 'button', className: 'danger', textContent: 'Delete' });
    del.addEventListener('click', () => {
      if (!confirm(`Delete ${p.name || p.id}?`)) return;
      jobsUI.post(del, '/v1/places/delete', { id: p.id }, 'Deleted.');
    });
    li.append(el('div', { className: 'point' }, name, save, del));
  }
  return li;
}

function renderPlaces() {
  if (!jobsUI) return;
  containers.paint(places);
  jobsUI.placeList.replaceChildren(...(places.length ? places.map(placeRow) : [el('li', { className: 'empty', textContent: 'no places' })]));
}

setInterval(() => { if (operator && jobsOpen && (tab === 'places' || tab === 'containers') && document.visibilityState === 'visible') loadPlaces(); }, 10000);

$('jobs-toggle').addEventListener('click', () => setJobsOpen(!jobsOpen));

// The stream does not wait for the session check; the check reconnects it if
// it finds an operator.
connect();
checkSession();
