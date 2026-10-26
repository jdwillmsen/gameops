// DOM-level checks for the timeline screen against the fake DOM from the
// graph tests: what it asks the API for, what it draws, and that it stays
// empty without a session.
import test from 'node:test';
import assert from 'node:assert/strict';
import { installDom, settle, byClass, one, all } from './graph/fake-dom.mjs';

const dom = installDom();
const { doc } = dom;
globalThis.location = { search: '' };
const { initTimeline } = await import('./timeline-ui.js');

const NOW = Date.now();
const iso = ms => new Date(NOW - ms).toISOString();
let id = 0;
const ev = (unit, ago, kind, extra = {}) => ({ id: ++id, unit, at: iso(ago), kind, severity: 'info', payload: {}, ...extra });
const ok = data => ({ ok: true, status: 200, data });

const JOB = '11111111-1111-4111-8111-111111111111';

function page({ operator = { id: 'owner' }, answer, jobs = [], job = '' } = {}) {
  const calls = [];
  const st = { job };
  const live = { operator, setTo: null };
  const host = {
    order: () => ['crew-01', 'crew-02'],
    jobs: () => jobs,
    operator: () => live.operator,
    state: () => st,
    sync: () => {},
    setOperator: op => { live.operator = op; },
    api: async (method, path) => { calls.push(path); return answer(path); },
  };
  const ui = initTimeline(host);
  const stage = doc.createElement('div');
  doc.body.append(stage);
  return { ui, calls, stage, live, st, mount: async () => { ui.mount(stage); await settle(); await settle(); } };
}

const history = path => {
  const u = new URL(path, 'http://x');
  const unit = u.searchParams.get('unit');
  const evs = unit === 'crew-01'
    ? [ev('crew-01', 50000, 'task_started', { task: 'a', payload: { template: 'haul' } }), ev('crew-01', 20000, 'task_failed', { task: 'a', payload: { template: 'haul' } })]
    : [ev('crew-02', 90000, 'died')];
  return ok({ events: evs.reverse() });
};

test.afterEach(() => { doc.body.children.length = 0; });

test('signed out: shows the sign-in note and asks the API for nothing', async () => {
  const p = page({ operator: null, answer: history });
  await p.mount();
  assert.equal(p.calls.length, 0);
  assert.match(one(p.stage, 'tl-empty').textContent, /Sign in/);
  p.ui.unmount();
});

test('asks for each unit over the window, newest first', async () => {
  const p = page({ answer: history });
  await p.mount();
  assert.equal(p.calls.length, 2);
  assert.ok(p.calls.every(c => c.startsWith('/v1/events?') && c.includes('sort=desc') && c.includes('since=') && c.includes('until=')));
  assert.deepEqual(p.calls.map(c => new URL(c, 'http://x').searchParams.get('unit')).sort(), ['crew-01', 'crew-02']);
  p.ui.unmount();
});

test('draws a lane per unit, a bar, a failure marker and a death marker', async () => {
  const p = page({ answer: history });
  await p.mount();
  assert.equal(byClass(p.stage, 'tl-lane').length, 2);
  assert.equal(byClass(p.stage, 'tl-bar-failed').length, 1);
  assert.equal(byClass(p.stage, 'tl-mark-failed').length, 1);
  assert.equal(byClass(p.stage, 'tl-mark-died').length, 1);
  assert.match(one(p.stage, 'tl-legend').textContent, /task failed/);
  p.ui.unmount();
});

test('clicking a bar opens its events', async () => {
  const p = page({ answer: history });
  await p.mount();
  const bar = byClass(p.stage, 'tl-bar-failed')[0];
  bar.dispatch('pointerup', { clientX: 0 });
  const side = one(p.stage, 'tl-side');
  assert.equal(side.hidden, false);
  assert.equal(byClass(side, 'tl-ev').length, 2);
  p.ui.key({ key: 'Escape', preventDefault() {} });
  assert.equal(side.hidden, true);
  p.ui.unmount();
});

test('repeated drag steps keep panning and the release does not open the side panel', async () => {
  const p = page({ answer: history });
  await p.mount();
  one(p.stage, 'tl-svg').dispatch('keydown', { key: '+' });
  one(p.stage, 'tl-svg').dispatch('keydown', { key: '+' });
  const first = one(p.stage, 'tl-svg');
  first.dispatch('pointerdown', { clientX: 400 });
  const ticks = () => byClass(p.stage, 'tl-grid').map(t => t.getAttribute('x1')).join();
  const before = ticks();
  first.dispatch('pointermove', { clientX: 330 });
  const second = one(p.stage, 'tl-svg');
  assert.notEqual(second, first);
  const mid = ticks();
  second.dispatch('pointermove', { clientX: 250 });
  const third = one(p.stage, 'tl-svg');
  assert.notEqual(third, second);
  assert.notEqual(ticks(), mid);
  assert.notEqual(mid, before);
  third.dispatch('pointerup', { clientX: 300 });
  assert.equal(one(p.stage, 'tl-side').hidden, true);
  p.ui.unmount();
});

test('arrow keys keep working after a repaint because focus returns to the chart', async () => {
  const p = page({ answer: history });
  await p.mount();
  let svg = one(p.stage, 'tl-svg');
  svg.focus();
  svg.dispatch('keydown', { key: '+' });
  one(p.stage, 'tl-svg').dispatch('keydown', { key: '+' });
  svg = one(p.stage, 'tl-svg');
  assert.equal(doc.activeElement, svg);
  const seen = new Set();
  for (let i = 0; i < 3; i++) {
    const e = svg.dispatch('keydown', { key: 'ArrowLeft' });
    assert.equal(e.defaultPrevented, true);
    const next = one(p.stage, 'tl-svg');
    assert.notEqual(next, svg);
    assert.equal(doc.activeElement, next);
    svg = next;
    seen.add(byClass(p.stage, 'tl-grid').map(t => t.getAttribute('x1')).join());
  }
  assert.ok(seen.size >= 2);
  p.ui.unmount();
});

test('a 401 from the history ends the session instead of drawing', async () => {
  const p = page({ answer: () => ({ ok: false, status: 401, data: { error: 'signed_out' } }) });
  await p.mount();
  assert.equal(p.live.operator, null);
  p.ui.unmount();
});

test('job view lists the steps in the order they ran, read from the job and its units', async () => {
  const job = { id: JOB, kind: 'plan', spec: { template: 'demo' }, state: 'done', created: iso(300000), updated: iso(10000), steps: [
    { key: 'b', unit: 'crew-02', state: 'done', work_id: 'w2', template: { template: 'haul' } },
    { key: 'a', unit: 'crew-01', state: 'failed', reason: 'boom', work_id: 'w1', template: { template: 'goto' } },
  ] };
  const answer = path => {
    if (path.startsWith(`/v1/jobs/${JOB}`)) return ok(job);
    const unit = new URL(path, 'http://x').searchParams.get('unit');
    return ok({ events: unit === 'crew-01'
      ? [ev('crew-01', 100000, 'task_started', { task: 'w1' }), ev('crew-01', 90000, 'task_failed', { task: 'w1' })].reverse()
      : [ev('crew-02', 80000, 'task_started', { task: 'w2' }), ev('crew-02', 20000, 'task_done', { task: 'w2' }), ev('crew-02', 5000, 'task_done', { task: 'other' })].reverse() });
  };
  const p = page({ answer, jobs: [job], job: JOB });
  await p.mount();
  const rows = byClass(p.stage, 'tl-job-row');
  assert.equal(rows.length, 2);
  assert.match(rows[0].textContent, /a.*crew-01.*boom/);
  assert.match(rows[1].textContent, /b.*crew-02.*1m 0s/);
  assert.ok(p.calls.some(c => c.includes('unit=crew-01') && c.includes('until=')));
  assert.equal(p.calls.filter(c => c.includes('unit=crew-03')).length, 0);
  p.ui.unmount();
});

test('job view says so when the job does not exist', async () => {
  const p = page({ answer: () => ({ ok: false, status: 404, data: { error: 'no such job' } }), job: JOB });
  await p.mount();
  assert.match(one(p.stage, 'tl-empty').textContent, /not found/);
  p.ui.unmount();
});

test('20 lanes over 24 h build and paint well inside a second', async () => {
  const units = Array.from({ length: 20 }, (_, i) => `crew-${String(i + 1).padStart(2, '0')}`);
  // A task every 90 s per unit plus an idle roll-up per minute: a busy day.
  const day = 86400000;
  const answer = path => {
    const unit = new URL(path, 'http://x').searchParams.get('unit');
    const out = [];
    for (let t = day; t > 0; t -= 90000) {
      out.push(ev(unit, t, 'task_started', { task: `${unit}-${t}`, payload: { template: ['haul', 'goto', 'mine'][t % 3] } }));
      out.push(ev(unit, t - 60000, t % 7 === 0 ? 'task_failed' : 'task_done', { task: `${unit}-${t}`, payload: { template: 'haul' } }));
    }
    return ok({ events: out.reverse().slice(0, 200) });
  };
  const p = page({ answer });
  p.ui.unmount();
  const host = { order: () => units, jobs: () => [], operator: () => ({ id: 'o' }), state: () => ({ job: '' }), sync() {}, setOperator() {}, api: async (m, path) => answer(path) };
  const ui = initTimeline(host);
  const stage = doc.createElement('div');
  doc.body.append(stage);
  const t0 = performance.now();
  ui.mount(stage);
  await settle(); await settle(); await settle();
  const took = performance.now() - t0;
  assert.equal(byClass(stage, 'tl-lane').length, 20);
  assert.ok(byClass(stage, 'tl-bar-done').length > 100);
  assert.ok(took < 1000, `took ${took}ms`);
  ui.unmount();
});
