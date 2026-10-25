// DOM-level checks for the jobs view, against the fake DOM in fake-dom.mjs:
// what the view puts on screen, what it asks the API for, and what survives a
// data update (focus, the selected node, open details, pan and zoom).
import test from 'node:test';
import assert from 'node:assert/strict';
import { installDom, settle, byClass, one, all, classesOf } from './fake-dom.mjs';

const dom = installDom();
const { doc, win, observers, timers } = dom;
const { initJobGraph } = await import('./jobs-ui.js');

const A = '00000000-0000-4000-8000-00000000000a';
const B = '00000000-0000-4000-8000-00000000000b';
const C = '00000000-0000-4000-8000-00000000000c';

// a -> b, a -> c, b -> d, c -> d: a fan-out and fan-in with a known layout.
function job(id, over = {}, prefix = '') {
  const k = key => prefix + key;
  const step = (key, deps, state = 'waiting') => ({ key: k(key), state, depends_on: deps.map(k), template: { template: 'inspect', chest: { x: 1, y: 2, z: 3 } } });
  return {
    id, kind: 'plan', spec: { template: `plan-${id.slice(-1)}` }, state: 'running',
    created: '2026-10-24T10:00:00Z', updated: '2026-10-24T10:01:00Z', deadline: '2026-10-24T10:30:00Z',
    steps: [step('a', [], 'done'), step('b', ['a'], 'running'), step('c', ['a'], 'queued'), step('d', ['b', 'c'])],
    ...over,
  };
}

const ok = data => ({ ok: true, status: 200, data });
const fail = (status, error = 'nope') => ({ ok: false, status, data: { error } });

// A view against a scripted host. `answer(path)` returns a response or a
// promise of one; every call is recorded.
function page({ feed = [], link = '', answer = () => fail(404), operator = { id: 'owner' } } = {}) {
  const calls = [];
  const st = { job: link };
  const live = { feed, operator, syncs: 0 };
  const host = {
    jobs: () => live.feed,
    operator: () => live.operator,
    state: () => st,
    sync: () => { live.syncs++; },
    api: async (method, path) => {
      calls.push(`${method} ${path}`);
      return answer(path, method);
    },
    setOperator: op => { live.operator = op; ui.onOperator(); },
  };
  const ui = initJobGraph(host);
  const stage = doc.createElement('div');
  doc.body.append(stage);
  const root = () => stage.children[0];
  pages.push(ui);
  return {
    ui, calls, st, live, stage, root,
    mount: async () => { ui.mount(stage); await settle(); },
    feed: next => { live.feed = next; ui.onJobs(); },
    node: id => all(stage, n => n.getAttribute('data-id') === id)[0],
    nodes: () => all(stage, n => n.getAttribute('data-id') !== null).map(n => n.getAttribute('data-id')),
    say: () => one(stage, 'dg-empty'),
    rows: () => byClass(stage, 'jv-row'),
    title: () => one(stage, 'jv-title').textContent,
    side: () => one(stage, 'dg-side'),
    zoomLayer: () => all(stage, n => classesOf(n).includes('dg-zoom'))[0],
    key: (target, key) => target.dispatch('keydown', { key }),
  };
}

const pages = [];
const active = () => doc.activeElement;
const idOf = el => el && el.getAttribute('data-id');

test.afterEach(() => {
  for (const ui of pages.splice(0)) ui.unmount();
  doc.activeElement = null;
  doc.body.children.length = 0;
});

// ---- the deep link ----

test('deep link: a job outside the feed is fetched by id, with a loading state, and is the job shown', async () => {
  let release;
  const gate = new Promise(r => { release = r; });
  const p = page({ feed: [job(B, {}, 'x-')], link: A, answer: async () => { await gate; return ok(job(A)); } });
  await p.mount();
  assert.deepEqual(p.calls, [`GET /v1/jobs/${A}`], 'the job is asked for by id');
  assert.match(p.say().textContent, /loading that job/);
  assert.equal(p.say().hidden, false);
  assert.deepEqual(p.nodes(), [], 'another job is not drawn under the linked id');
  assert.equal(p.title(), '', 'no other job title either');
  release();
  await settle();
  assert.deepEqual(p.nodes().sort(), ['a', 'b', 'c', 'd']);
  assert.equal(p.title(), `plan-a`);
  assert.equal(p.say().hidden, true);
  assert.equal(p.calls.length, 1, 'fetched once');
  p.feed([job(B, {}, 'x-')]);
  assert.equal(p.calls.length, 1, 'a feed update does not fetch it again');
  assert.equal(p.title(), `plan-a`, 'and the linked job stays on screen');
});

test('deep link: a 404 says the job was not found and is not asked for again', async () => {
  const p = page({ feed: [job(B, {}, 'x-')], link: A, answer: () => fail(404, 'no such job') });
  await p.mount();
  assert.match(p.say().textContent, /job not found/);
  assert.deepEqual(p.nodes(), []);
  assert.equal(timers.pending().length, 0, 'a refusal is final: no retry');
  p.feed([job(B, {}, 'x-')]);
  p.feed([job(B, {}, 'x-')]);
  assert.equal(p.calls.length, 1);
  assert.ok(p.rows().some(r => /not available/.test(r.textContent)), 'the list row says so as well');
});

test('deep link: a 503 or a dropped connection retries with growing delays, never reports not found', async () => {
  let n = 0;
  const p = page({
    link: A,
    answer: () => (++n === 1 ? fail(503, 'unavailable') : n === 2 ? { ok: false, status: 0, data: { error: 'could not reach the server' } } : ok(job(A))),
  });
  await p.mount();
  assert.doesNotMatch(p.say().textContent, /not found/);
  assert.match(p.say().textContent, /trying again/);
  assert.equal(timers.pending().length, 1);
  assert.equal(await timers.fire(), 1000);
  assert.doesNotMatch(p.say().textContent, /not found/);
  assert.equal(timers.pending().length, 1, 'a network error retries too');
  assert.equal(await timers.fire(), 2000, 'each wait is longer');
  assert.deepEqual(p.nodes().sort(), ['a', 'b', 'c', 'd'], 'the job shows once it answers');
  assert.equal(timers.pending().length, 0);
  assert.equal(p.calls.length, 3);
});

test('deep link: switching to another job forgets the fetched and the refused ones', async () => {
  const p = page({
    feed: [job(B, {}, 'x-')], link: A,
    answer: path => (path.endsWith(A) ? ok(job(A)) : fail(404)),
  });
  await p.mount();
  assert.equal(p.title(), 'plan-a');
  assert.equal(p.rows().length, 2, 'the fetched job is listed beside the feed');
  p.rows().find(r => /plan-b/.test(r.textContent)).click();
  assert.equal(p.st.job, B);
  assert.equal(p.rows().length, 1, 'the fetched job leaves the list when another is picked');
  p.st.job = A;
  p.ui.onJobs();
  await settle();
  assert.equal(p.calls.length, 2, 'linking to it again asks again');
  // A refusal does not outlive the switch either.
  p.st.job = C;
  p.ui.onJobs();
  await settle();
  assert.match(p.say().textContent, /not found/);
  p.st.job = B;
  p.ui.onJobs();
  p.st.job = C;
  p.ui.onJobs();
  await settle();
  assert.equal(p.calls.filter(c => c.endsWith(C)).length, 2, 'the refusal was forgotten when the view moved off it');
});

test('deep link: an answer that lands after the view moved on is ignored', async () => {
  let release;
  const gate = new Promise(r => { release = r; });
  const p = page({ feed: [job(B, {}, 'x-')], link: A, answer: async () => { await gate; return ok(job(A)); } });
  await p.mount();
  p.st.job = B;
  p.ui.onJobs();
  release();
  await settle();
  assert.deepEqual(p.nodes().sort(), ['x-a', 'x-b', 'x-c', 'x-d']);
  assert.equal(p.rows().length, 1, 'the late job was not spliced into the list');
});

// ---- sign-out and empty states ----

test('states: signed out says so and shows no jobs; an empty feed says there are none', async () => {
  const out = page({ feed: [job(B)], operator: null });
  await out.mount();
  assert.equal(out.say().textContent, 'Sign in to see jobs.');
  assert.equal(out.say().hidden, false);
  assert.deepEqual(out.nodes(), []);
  assert.equal(out.rows().length, 0);
  assert.equal(out.calls.length, 0, 'nothing is fetched without a session');

  const none = page();
  await none.mount();
  assert.match(none.say().textContent, /no jobs yet/);
  assert.deepEqual(none.nodes(), []);
});

test('sign-out clears the fetched job, the refusal, a pending retry and the selection', async () => {
  let n = 0;
  const p = page({ link: A, answer: () => (++n === 1 ? ok(job(A)) : fail(503)) });
  await p.mount();
  p.node('b').click();
  assert.ok(p.side() && !p.side().hidden, 'details are open');
  p.st.job = C;
  p.ui.onJobs();
  await settle();
  assert.equal(timers.pending().length, 1, 'a retry is waiting for the 503');
  p.live.operator = { id: 'owner' };
  p.st.job = A;
  p.ui.onJobs();
  await settle();

  p.ui.onJobs();
  p.live.operator = null;
  p.ui.onOperator();
  assert.equal(p.say().textContent, 'Sign in to see jobs.');
  assert.deepEqual(p.nodes(), []);
  assert.ok(p.side().hidden, 'the details are gone');
  assert.equal(p.rows().length, 0);
  assert.equal(timers.pending().length, 0, 'no retry outlives the session');

  // Signing in again starts from nothing: the linked job is fetched afresh.
  const before = p.calls.length;
  p.live.operator = { id: 'owner' };
  p.ui.onOperator();
  await settle();
  assert.equal(p.calls.length, before + 1);
  assert.ok(!p.side() || p.side().hidden, 'nothing selected after signing back in');
});

test('sign-out while a fetch is in flight drops the late answer', async () => {
  let release;
  const gate = new Promise(r => { release = r; });
  const p = page({ link: A, answer: async () => { await gate; return ok(job(A)); } });
  await p.mount();
  p.live.operator = null;
  p.ui.onOperator();
  release();
  await settle();
  assert.equal(p.say().textContent, 'Sign in to see jobs.');
  assert.deepEqual(p.nodes(), []);
});

test('a 401 on the fetch signs the view out', async () => {
  const p = page({ link: A, answer: () => fail(401) });
  await p.mount();
  assert.equal(p.say().textContent, 'Sign in to see jobs.');
  assert.equal(p.live.operator, null);
});

// ---- focus and persistence across updates ----

const running = (over = {}) => job(B, { updated: '2026-10-24T10:02:00Z', ...over });
const withState = (j, key, state) => ({ ...j, steps: j.steps.map(s => (s.key === key ? { ...s, state } : s)) });

test('update: a node keeps focus through a jobs event', async () => {
  const p = page({ feed: [running()] });
  await p.mount();
  const a = p.node('a');
  a.focus();
  p.feed([withState(running(), 'b', 'done')]);
  assert.equal(active(), a, 'the same node element still has focus');
  assert.equal(p.node('b').getAttribute('aria-label').includes('done'), true, 'and its data was patched');
});

test('update: an unchanged list row keeps focus, and so does one that moved', async () => {
  const other = job(C, { state: 'done' }, 'z-');
  const p = page({ feed: [running(), other] });
  await p.mount();
  const row = p.rows()[1];
  row.focus();
  p.feed([running({ updated: '2026-10-24T10:05:00Z' }), other]);
  assert.equal(active(), row, 'a tick with the same rows leaves focus alone');
  // The second job starts running and moves to the top: the row moves, the
  // operator's place does not.
  p.feed([running(), { ...other, state: 'running' }]);
  assert.equal(active(), row, 'focus follows the row when the order changes');
});

test('update: open details stay the same element, hold focus, and show the new values', async () => {
  const p = page({ feed: [running()] });
  await p.mount();
  p.node('b').click();
  const dl = one(p.side(), 'dg-details').children[1];
  assert.equal(active(), dl, 'the operator opened the details, so focus moved there');
  const before = dl.children.length;
  p.feed([withState(running(), 'b', 'done')]);
  assert.equal(active(), dl, 'a data update does not steal or move focus');
  assert.equal(one(p.side(), 'dg-details').children[1], dl, 'the panel was not rebuilt');
  assert.equal(dl.children.length, before);
  assert.match(dl.textContent, /Statedone/, 'the new state is shown');
});

test('update: a focused node stays focused while the details open elsewhere are untouched', async () => {
  const p = page({ feed: [running()] });
  await p.mount();
  p.node('b').click();
  p.node('c').focus();
  p.feed([withState(running(), 'c', 'running')]);
  assert.equal(idOf(active()), 'c', 'focus stays where the operator put it');
  assert.equal(p.node('b').getAttribute('aria-current'), 'true');
  assert.ok(!p.side().hidden);
});

test('update: the selected node and its details persist; pan and zoom persist', async () => {
  const p = page({ feed: [running()] });
  await p.mount();
  p.node('b').click();
  const zoomIn = all(p.stage, n => n.getAttribute('aria-label') === 'Zoom in')[0];
  zoomIn.click();
  const transform = p.zoomLayer().getAttribute('transform');
  assert.match(transform, /scale\(1\.25\)/);
  for (let i = 0; i < 3; i++) p.feed([withState(running({ updated: `2026-10-24T10:0${3 + i}:00Z` }), 'c', 'done')]);
  assert.equal(p.node('b').getAttribute('aria-current'), 'true', 'b is still the selected node');
  assert.equal(p.node('c').getAttribute('aria-current'), null, 'and only b');
  assert.ok(classesOf(p.node('b')).includes('dg-on'));
  assert.ok(!p.side().hidden, 'the details are still open');
  assert.match(one(p.side(), 'dg-details').textContent, /Stepb/);
  assert.equal(p.zoomLayer().getAttribute('transform'), transform, 'the viewport did not move');
});

test('update: a step added to the job keeps the focused node focused', async () => {
  const p = page({ feed: [running()] });
  await p.mount();
  p.node('c').focus();
  const grown = running({ steps: [...running().steps, { key: 'e', state: 'waiting', depends_on: ['d'] }] });
  p.feed([grown]);
  assert.deepEqual(p.nodes().sort(), ['a', 'b', 'c', 'd', 'e']);
  assert.equal(idOf(active()), 'c', 'the rebuilt node got the focus back');
});

test('update: a step that disappears closes its details', async () => {
  const p = page({ feed: [running()] });
  await p.mount();
  p.node('d').click();
  p.feed([running({ steps: running().steps.filter(s => s.key !== 'd') })]);
  assert.ok(p.side().hidden);
});

// ---- keyboard ----

test('keys: arrows follow edges, Enter opens the details', async () => {
  const p = page({ feed: [running()] });
  await p.mount();
  p.node('a').focus();
  const right = p.key(p.node('a'), 'ArrowRight');
  assert.equal(idOf(active()), 'b', 'right follows an edge to what the step unlocks');
  assert.ok(right.defaultPrevented);
  p.key(active(), 'ArrowDown');
  assert.equal(idOf(active()), 'c', 'down steps through the same layer');
  p.key(active(), 'ArrowUp');
  assert.equal(idOf(active()), 'b');
  p.key(active(), 'ArrowRight');
  assert.equal(idOf(active()), 'd');
  p.key(active(), 'ArrowLeft');
  assert.equal(idOf(active()), 'b', 'left goes back to what it waited for');
  const edge = p.key(p.node('a'), 'ArrowLeft');
  assert.equal(idOf(active()), 'b', 'a step with nothing before it keeps focus where it is');
  assert.ok(edge.defaultPrevented);

  p.node('c').focus();
  p.key(p.node('c'), 'Enter');
  assert.ok(!p.side().hidden, 'Enter opens the details');
  assert.match(one(p.side(), 'dg-details').textContent, /Stepc/);
  assert.equal(active().tag, 'dl', 'and moves focus into them');
  assert.equal(p.node('c').getAttribute('aria-current'), 'true');
});

test('keys: Escape closes the details first and returns focus to the node, then the job selection', async () => {
  const p = page({ feed: [running(), job(C, {}, 'z-')], link: B });
  await p.mount();
  const reached = [];
  p.stage.addEventListener('keydown', ev => reached.push(ev.key));

  p.node('b').focus();
  p.key(p.node('b'), 'Enter');
  assert.equal(active().tag, 'dl');

  // From inside the details the view's own key handler takes the press.
  const ev = { key: 'Escape', preventDefault() { this.defaultPrevented = true; } };
  assert.equal(p.ui.key(ev), true);
  assert.ok(p.side().hidden, 'the details closed');
  assert.equal(idOf(active()), 'b', 'focus is back on the node');
  assert.equal(p.node('b').getAttribute('aria-current'), null, 'and it is no longer marked selected');
  assert.equal(p.st.job, B, 'the job selection is untouched by the first press');

  // On the node with nothing open the press is not consumed there: it bubbles
  // to the console, whose handler asks the view, which drops the selection.
  const bare = p.key(p.node('b'), 'Escape');
  assert.equal(bare.defaultPrevented, false);
  assert.deepEqual(reached, ['Escape'], 'it reached the console');
  assert.equal(p.ui.key({ key: 'Escape', preventDefault() {} }), true);
  assert.equal(p.st.job, '', 'the second layer cleared the job from the address');
  assert.equal(p.ui.key({ key: 'Escape', preventDefault() {} }), false, 'nothing left to close');
});

test('keys: Escape on a node with details open is consumed there and closes them', async () => {
  const p = page({ feed: [running()] });
  await p.mount();
  const reached = [];
  p.stage.addEventListener('keydown', ev => reached.push(ev.key));
  p.node('b').click();
  p.node('b').focus();
  const ev = p.key(p.node('b'), 'Escape');
  assert.ok(ev.defaultPrevented);
  assert.deepEqual(reached, [], 'it did not leak to the console');
  assert.ok(p.side().hidden);
  assert.equal(idOf(active()), 'b');
});

test('keys: the close button also returns focus to the node', async () => {
  const p = page({ feed: [running()] });
  await p.mount();
  p.node('c').click();
  const close = all(p.side(), n => n.getAttribute('aria-label') === 'Close details')[0];
  close.click();
  assert.ok(p.side().hidden);
  assert.equal(idOf(active()), 'c');
});

// ---- mount and unmount ----

test('unmount: leaves no window listener, observer, timer or late render behind', async () => {
  let release;
  const gate = new Promise(r => { release = r; });
  let n = 0;
  const p = page({ link: A, answer: async () => { n++; if (n === 1) return fail(503); await gate; return ok(job(A)); } });
  await p.mount();
  assert.equal(win.count('blur'), 1, 'mounted: the blur listener is on');
  assert.equal(observers.size, 1);
  assert.equal(timers.pending().length, 1, 'a retry is pending');
  await timers.fire();
  p.ui.unmount();
  assert.equal(p.root(), undefined, 'the view left the stage');
  assert.equal(win.count('blur'), 0);
  assert.equal(observers.size, 0);
  assert.equal(timers.pending().length, 0);
  release();
  await settle();
  assert.equal(p.root(), undefined, 'a late answer rendered nothing');
  assert.equal(win.count('blur'), 0);

  await p.mount();
  assert.equal(win.count('blur'), 1, 'mounting again re-attaches exactly once');
  assert.equal(observers.size, 1);
  p.ui.unmount();
});
