import test from 'node:test';
import assert from 'node:assert/strict';
import { buildLane, buildLanes, colorFor, clampRange, zoomRange, panRange, ticks, cullBars, fetchUnit, fetchWindow, jobRows, duration } from './timeline.js';

const T0 = Date.parse('2026-10-26T10:00:00Z');
const at = s => new Date(T0 + s * 1000).toISOString();
let id = 0;
const ev = (s, kind, extra = {}) => ({ id: ++id, unit: 'crew-01', at: at(s), kind, severity: 'info', payload: {}, ...extra });
const range = { from: T0, to: T0 + 600000 };

test('a start and its finish make one bar with the verdict', () => {
  const { bars } = buildLane([
    ev(0, 'task_started', { task: 'a', payload: { template: 'haul' } }),
    ev(30, 'task_done', { task: 'a', payload: { template: 'haul', duration_ms: 30000 } }),
  ], range);
  assert.equal(bars.length, 1);
  assert.deepEqual([bars[0].start, bars[0].end, bars[0].outcome, bars[0].template], [T0, T0 + 30000, 'done', 'haul']);
  assert.equal(bars[0].events.length, 2);
});

test('events arriving newest first still pair up', () => {
  const { bars } = buildLane([
    ev(30, 'task_failed', { task: 'a' }),
    ev(0, 'task_started', { task: 'a', payload: { template: 'goto' } }),
  ], range);
  assert.equal(bars[0].outcome, 'failed');
});

test('a failure leaves a marker', () => {
  const { marks } = buildLane([ev(0, 'task_started', { task: 'a' }), ev(5, 'task_failed', { task: 'a' })], range);
  assert.deepEqual(marks.map(m => m.kind), ['failed']);
});

test('a task never finished runs to the end of the window as running', () => {
  const { bars } = buildLane([ev(60, 'task_started', { task: 'a' })], range);
  assert.deepEqual([bars[0].outcome, bars[0].end], ['running', range.to]);
});

test('a finish whose start predates the window uses the recorded duration, else the window edge', () => {
  const withTook = buildLane([ev(100, 'task_done', { task: 'a', payload: { duration_ms: 40000 } })], range).bars[0];
  assert.equal(withTook.start, T0 + 60000);
  const without = buildLane([ev(100, 'task_done', { task: 'a' })], range).bars[0];
  assert.deepEqual([without.start, without.clipped], [T0, true]);
});

test('a new start closes the previous task as ended, not done', () => {
  const { bars } = buildLane([ev(0, 'task_started', { task: 'a' }), ev(10, 'task_started', { task: 'b' })], range);
  assert.deepEqual([bars[0].outcome, bars[0].end], ['ended', T0 + 10000]);
  assert.equal(bars[1].outcome, 'running');
});

test('died and offline open spans that respawn and online close; death is a marker', () => {
  const { spans, marks, bars } = buildLane([
    ev(0, 'task_started', { task: 'a' }),
    ev(10, 'died'),
    ev(20, 'respawned'),
    ev(30, 'offline'),
    ev(50, 'online'),
  ], range);
  assert.equal(bars[0].outcome, 'ended');
  assert.deepEqual(marks.map(m => m.kind), ['died']);
  assert.deepEqual(spans.map(s => [s.kind, s.start - T0, s.end - T0]), [['dead', 10000, 20000], ['offline', 30000, 50000]]);
});

test('an idle roll-up is a bar over the polls it stands for', () => {
  const { bars } = buildLane([ev(0, 'idle_rollup', { payload: { template: 'haul', count: 12, last: at(55) } })], range);
  assert.deepEqual([bars[0].outcome, bars[0].end - bars[0].start, bars[0].count], ['idle', 55000, 12]);
});

test('a stuck period is a marker', () => {
  const { marks } = buildLane([ev(0, 'task_started', { task: 'a' }), ev(20, 'stuck', { task: 'a' })], range);
  assert.equal(marks[0].kind, 'stuck');
});

test('buildLanes keeps every requested unit, even one without events', () => {
  const lanes = buildLanes([ev(0, 'task_started', { task: 'a' })], ['crew-01', 'crew-02'], range);
  assert.deepEqual([...lanes.keys()], ['crew-01', 'crew-02']);
  assert.equal(lanes.get('crew-02').bars.length, 0);
});

test('colorFor is stable per template and falls back to grey past the palette', () => {
  const seen = [];
  const a = colorFor('haul', seen);
  colorFor('goto', seen);
  assert.equal(colorFor('haul', seen), a);
  for (let i = 0; i < 12; i++) colorFor(`t${i}`, seen);
  assert.equal(colorFor('t11', seen), colorFor('t10', seen));
  assert.equal(colorFor('', seen), colorFor('t11', seen));
});

test('clamp, zoom and pan keep the window inside the bounds', () => {
  const b = [0, 1000000];
  assert.deepEqual(clampRange([-5, 100000], b), [0, 100005]);
  assert.deepEqual(clampRange([990000, 1100000], b), [890000, 1000000]);
  const z = zoomRange([0, 600000], 0.5, 0.5, b);
  assert.deepEqual(z, [150000, 450000]);
  assert.equal(zoomRange([0, 40000], 0.01, 0.5, b)[1] - zoomRange([0, 40000], 0.01, 0.5, b)[0], 30000);
  assert.deepEqual(panRange([0, 100000], -50000, b), [0, 100000]);
});

test('ticks land on round times inside the window', () => {
  const { step, times } = ticks([T0, T0 + 24 * 3600000], 8);
  assert.equal(step, 3600000 * 6);
  assert.ok(times.length >= 3 && times.length <= 9);
  assert.ok(times.every(t => t >= T0 && t <= T0 + 24 * 3600000));
  assert.ok(times.every(t => (t - times[0]) % step === 0));
});

test('cullBars drops sub-pixel repeats but never a failure', () => {
  const x = t => (t - T0) / 1000;
  const mk = (s, outcome) => ({ start: T0 + s * 1000, end: T0 + s * 1000 + 100, template: 'haul', outcome });
  const out = cullBars([mk(1, 'done'), mk(1.2, 'done'), mk(1.4, 'failed'), mk(5, 'done')], x);
  assert.equal(out.length, 3);
});

test('fetchUnit pages newest first until the cursor runs out and returns oldest first', async () => {
  const calls = [];
  const api = async (m, path) => {
    calls.push(path);
    return calls.length === 1
      ? { ok: true, data: { events: [{ id: 3 }, { id: 2 }], next: 'c1' } }
      : { ok: true, data: { events: [{ id: 1 }] } };
  };
  const r = await fetchUnit(api, 'crew-01', [T0, T0 + 1000]);
  assert.deepEqual(r.events.map(e => e.id), [1, 2, 3]);
  assert.equal(r.truncated, false);
  assert.match(calls[0], /unit=crew-01/);
  assert.match(calls[0], /sort=desc/);
  assert.match(calls[1], /cursor=c1/);
});

test('fetchUnit flags a page cap as truncated and reports a failure', async () => {
  const more = async () => ({ ok: true, data: { events: [{ id: 1 }], next: 'n' } });
  assert.equal((await fetchUnit(more, 'u', [0, 1], { pages: 3 })).truncated, true);
  const denied = async () => ({ ok: false, status: 401, data: { error: 'signed_out' } });
  assert.equal((await fetchUnit(denied, 'u', [0, 1])).status, 401);
});

test('fetchWindow gathers every unit with bounded concurrency and surfaces a failure', async () => {
  let live = 0, peak = 0;
  const api = async (m, path) => {
    live++; peak = Math.max(peak, live);
    await new Promise(r => setTimeout(r, 2));
    live--;
    return { ok: true, data: { events: [{ id: 1, unit: new URL(path, 'http://x').searchParams.get('unit') }] } };
  };
  const units = Array.from({ length: 20 }, (_, i) => `crew-${i}`);
  const r = await fetchWindow(api, units, [0, 1], { concurrency: 4 });
  assert.equal(r.events.length, 20);
  assert.ok(peak <= 4);
  const bad = await fetchWindow(async () => ({ ok: false, status: 503, data: { error: 'event store unavailable' } }), ['a', 'b'], [0, 1]);
  assert.equal(bad.failure.status, 503);
});

test('jobRows orders steps by when they ran and carries duration and outcome', () => {
  const job = { updated: at(300), steps: [
    { key: 'late', unit: 'crew-02', state: 'done', work_id: 'w2', template: { template: 'haul' } },
    { key: 'early', unit: 'crew-01', state: 'failed', reason: 'no pickup', work_id: 'w1', template: { template: 'gather_and_deposit' } },
    { key: 'wait', unit: 'crew-03', state: 'waiting', work_id: null },
  ] };
  const events = [
    ev(0, 'task_started', { task: 'w1' }), ev(20, 'task_failed', { task: 'w1' }),
    ev(30, 'task_started', { task: 'w2', unit: 'crew-02' }), ev(90, 'task_done', { task: 'w2', unit: 'crew-02' }),
  ];
  const rows = jobRows(job, events);
  assert.deepEqual(rows.map(r => r.key), ['early', 'late', 'wait']);
  assert.deepEqual([rows[0].outcome, rows[0].end - rows[0].start, rows[0].reason], ['failed', 20000, 'no pickup']);
  assert.equal(rows[2].start, null);
});

test('duration reads in the largest sensible unit', () => {
  assert.deepEqual([duration(4000), duration(125000), duration(3720000), duration(null)], ['4s', '2m 5s', '1h 2m', '']);
});
