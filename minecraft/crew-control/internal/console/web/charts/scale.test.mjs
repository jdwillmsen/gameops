import test from 'node:test';
import assert from 'node:assert/strict';
import { compact, dateTime, full, linear, lowerBound, nearest, niceTicks, stack, tickLabel, timeStep, timeTicks } from './scale.js';

test('niceTicks: round steps that cover the data', () => {
  assert.deepEqual(niceTicks(0, 412, 5).ticks, [0, 100, 200, 300, 400, 500]);
  assert.deepEqual(niceTicks(0, 93, 5).ticks, [0, 20, 40, 60, 80, 100]);
  const t = niceTicks(113, 207, 5);
  assert.ok(t.lo <= 113 && t.hi >= 207);
  assert.deepEqual([niceTicks(0, 0).lo, niceTicks(0, 0).hi], [0, 1]);
  const flat = niceTicks(5, 5);
  assert.ok(flat.lo < 5 && flat.hi > 5);
  assert.deepEqual(niceTicks(0.1, 0.3, 3).ticks, [0.1, 0.2, 0.3]);
});

test('compact: no integer zero is ever stripped, thousands go K', () => {
  assert.equal(compact(0), '0');
  assert.equal(compact(100), '100');
  assert.equal(compact(600), '600');
  assert.equal(compact(1000), '1K');
  assert.equal(compact(2500), '2.5K');
  assert.equal(compact(1284), '1.3K');
  assert.equal(compact(1200000), '1.2M');
  assert.equal(compact(0.5), '0.5');
  assert.equal(compact(12.5), '12.5');
  assert.equal(compact(-3000), '-3K');
  assert.equal(compact(null), '–');
  assert.equal(full(12345.6), '12,346');
  assert.equal(full(12.345), '12.35');
});

test('linear maps and inverts, and survives a flat domain', () => {
  const f = linear(0, 10, 100, 200);
  assert.equal(f(5), 150);
  assert.equal(f.invert(150), 5);
  const flat = linear(3, 3, 0, 10);
  assert.equal(flat(3), 0);
  assert.equal(flat.invert(5), 3);
});

test('bisect and nearest snap to the closest x', () => {
  const xs = [0, 10, 20, 40];
  assert.equal(lowerBound(xs, 15), 2);
  assert.equal(nearest(xs, 14), 1);
  assert.equal(nearest(xs, 16), 2);
  assert.equal(nearest(xs, 31), 3);
  assert.equal(nearest(xs, -5), 0);
  assert.equal(nearest(xs, 99), 3);
  assert.equal(nearest(xs, 99, 0, 2), 2);
  assert.equal(nearest([], 1), -1);
});

test('stack treats gaps as zero so layers above do not tear', () => {
  const s = stack([[1, null, 3], [2, 2, 2]]);
  assert.deepEqual(s[0].hi, [1, 0, 3]);
  assert.deepEqual(s[1].lo, [1, 0, 3]);
  assert.deepEqual(s[1].hi, [3, 2, 5]);
});

test('timeTicks: UTC ticks land on round clock times', () => {
  const t0 = Date.UTC(2026, 9, 23, 0, 3);
  const t1 = Date.UTC(2026, 9, 23, 12, 0);
  const { ticks, step } = timeTicks(t0, t1, 6, 'UTC');
  assert.equal(step, 2 * 3600000);
  assert.equal(new Date(ticks[0]).toISOString(), '2026-10-23T02:00:00.000Z');
  assert.ok(ticks.every(t => t >= t0 && t <= t1));
  assert.equal(tickLabel(ticks[0], step, 'UTC'), '02:00');
  assert.equal(tickLabel(Date.UTC(2026, 9, 24), 86400000, 'UTC'), 'Oct 24');
  assert.equal(tickLabel(Date.UTC(2026, 9, 24, 1, 2, 3), 10000, 'UTC'), '01:02:03');
  assert.equal(dateTime(Date.UTC(2026, 9, 23, 14, 5, 9), 'UTC'), 'Oct 23, 14:05:09');
  assert.equal(timeStep(1000, 6), 1000);
});
