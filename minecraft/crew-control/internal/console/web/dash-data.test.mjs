import test from 'node:test';
import assert from 'node:assert/strict';
import { createDashData, throughput, outcomes, taskDurations, durationHistogram, bucketMs, healthShare, median, pickUnits, windowSamples, stateOf } from './dash-data.js';

const T0 = Date.parse('2026-10-26T12:00:00Z');
const iso = s => new Date(T0 + s * 1000).toISOString();
let seq = 0;
const ev = (s, unit, kind, extra = {}) => ({ seq: ++seq, at: iso(s), unit, kind, level: 'info', text: '', ...extra });
const unit = (id, o = {}) => ({ id, name: id, online: true, dead: false, health: 20, hunger: 20, task: null, ...o });
const F = (o = {}) => ({ unit: '', from: T0, to: T0 + 600_000, ...o });

test('stateOf: offline beats dead beats working', () => {
  assert.equal(stateOf(unit('a', { online: false, dead: true })), 'offline');
  assert.equal(stateOf(unit('a', { dead: true, task: {} })), 'dead');
  assert.equal(stateOf(unit('a', { task: {} })), 'working');
  assert.equal(stateOf(unit('a')), 'idle');
});

test('samples are taken every few seconds and the newest one refreshes in place', () => {
  const d = createDashData();
  d.onUnits([unit('a'), unit('b', { task: {} })], T0);
  d.onUnits([unit('a'), unit('b')], T0 + 1000);
  assert.equal(d.samples().length, 1);
  assert.equal(d.samples()[0].idle, 2);
  d.onUnits([unit('a', { online: false }), unit('b')], T0 + 6000);
  assert.equal(d.samples().length, 2);
  assert.equal(d.samples()[1].offline, 1);
  assert.equal(d.samples()[1].alerts, 1);
});

test('idle time credits each interval to the state that lasted through it', () => {
  const d = createDashData();
  d.onUnits([unit('a'), unit('b', { task: {} })], T0);
  d.onUnits([unit('a', { task: {} }), unit('b', { task: {} })], T0 + 1000);
  d.onUnits([unit('a', { task: {} }), unit('b', { online: false })], T0 + 3000);
  const idle = Object.fromEntries(d.idle(['a', 'b']).map(r => [r.id, r]));
  assert.equal(idle.a.idleMs, 1000);
  assert.equal(idle.a.onlineMs, 3000);
  assert.equal(idle.b.idleMs, 0);
});

test('a stream gap is not counted as idle', () => {
  const d = createDashData();
  d.onUnits([unit('a')], T0);
  d.onUnits([unit('a')], T0 + 60_000);
  assert.equal(d.idle(['a'])[0].idleMs, 0);
});

test('events merge by seq and bump the version only when new', () => {
  const d = createDashData();
  const e = ev(1, 'a', 'task_done');
  d.onEvents([e]);
  const v = d.version;
  d.onEvents([e]);
  assert.equal(d.version, v);
  assert.equal(d.feed().length, 1);
});

test('throughput buckets done and failed within the window and unit', () => {
  const feed = [ev(10, 'a', 'task_done'), ev(20, 'a', 'task_done'), ev(70, 'b', 'task_failed'), ev(75, 'b', 'task_done'), ev(5000, 'a', 'task_done')];
  const all = throughput(feed, F());
  assert.equal(all.size, 60_000);
  assert.equal(all.done.reduce((a, b) => a + b, 0), 3);
  assert.deepEqual([all.done[0], all.done[1], all.failed[1]], [2, 1, 1]);
  const onlyA = throughput(feed, F({ unit: 'a' }));
  assert.equal(onlyA.done.reduce((a, b) => a + b, 0), 2);
  assert.ok(onlyA.x.length >= 2);
});

test('bucketMs keeps about forty points and never goes under a minute', () => {
  assert.equal(bucketMs(5 * 60_000), 60_000);
  assert.equal(bucketMs(60 * 60_000), 120_000);
  for (const span of [60_000, 900_000, 3_600_000, 86_400_000]) assert.ok(bucketMs(span) >= 60_000);
});

test('outcomes and success rate', () => {
  const feed = [ev(1, 'a', 'task_done'), ev(2, 'a', 'task_done'), ev(3, 'b', 'task_failed'), ev(4, 'b', 'task_cancelled'), ev(5, 'a', 'stuck')];
  const o = outcomes(feed, F());
  assert.deepEqual([o.done, o.failed, o.cancelled], [2, 1, 1]);
  assert.equal(Math.round(o.rate * 100), 67);
  assert.deepEqual(o.perUnit.get('b'), { done: 0, failed: 1 });
  assert.equal(outcomes([], F()).rate, null);
});

test('durations pair starts with ends by task id and skip unseen starts', () => {
  const feed = [
    ev(0, 'a', 'task_started', { task: 't1' }), ev(0, 'b', 'task_started', { task: 't2' }),
    ev(8, 'a', 'task_done', { task: 't1' }), ev(200, 'b', 'task_failed', { task: 't2' }),
    ev(300, 'a', 'task_done', { task: 'unseen' }),
  ];
  const d = taskDurations(feed, F());
  assert.deepEqual(d.map(x => [x.unit, x.secs, x.outcome]), [['a', 8, 'done'], ['b', 200, 'failed']]);
  const h = durationHistogram(d);
  assert.equal(h.done[0], 1);
  assert.equal(h.failed[4], 1);
  assert.equal(h.categories.length, h.done.length);
  assert.equal(median([3, 1, 2]), 2);
  assert.equal(median([]), null);
});

test('health share and filters', () => {
  const list = [unit('a'), unit('b', { hunger: 2 }), unit('c', { online: false }), unit('d')];
  assert.equal(healthShare(list), 50);
  assert.equal(healthShare([]), null);
  assert.deepEqual(pickUnits(list, F({ unit: 'b' })).map(u => u.id), ['b']);
  assert.equal(pickUnits(list, F()).length, 4);
  assert.deepEqual(windowSamples([{ t: T0 - 1 }, { t: T0 + 5 }], F()).length, 1);
});
