import test from 'node:test';
import assert from 'node:assert/strict';
import { MAX_SERIES } from './palette.js';
import { OTHER_KEY, createRegistry, foldOther, slotClass } from './series.js';

const mk = (n, len = 3) => Array.from({ length: n }, (_, i) => ({ key: `u${i}`, name: `Unit ${i}`, values: Array.from({ length: len }, (_, j) => i + j) }));

test('20 series fold past the eighth into one grey Other, no generated hue', () => {
  const reg = createRegistry();
  const out = foldOther(mk(20), reg);
  assert.equal(out.length, MAX_SERIES + 1);
  assert.deepEqual(out.slice(0, 8).map(s => s.slot), [0, 1, 2, 3, 4, 5, 6, 7]);
  const other = out[8];
  assert.equal(other.key, OTHER_KEY);
  assert.equal(other.name, 'Other (12)');
  assert.equal(slotClass(other.slot), 'cc-other');
  // u8..u19 at index 0 sum to 8+9+...+19
  assert.equal(other.values[0], 12 * 8 + 66);
  assert.ok(out.every(s => /^cc-(s[1-8]|other)$/.test(slotClass(s.slot))));
});

test('colour follows the entity: dropping a series does not repaint the rest', () => {
  const reg = createRegistry();
  const all = mk(5);
  const before = Object.fromEntries(foldOther(all, reg).map(s => [s.key, s.slot]));
  const after = Object.fromEntries(foldOther(all.filter(s => s.key !== 'u1'), reg).map(s => [s.key, s.slot]));
  for (const k of Object.keys(after)) assert.equal(after[k], before[k], k);
  assert.equal(after.u4, 4);
});

test('fewer than nine series leave no Other', () => {
  const out = foldOther(mk(8), createRegistry());
  assert.equal(out.length, 8);
  assert.ok(!out.some(s => s.other));
});

test('combine mean and max, and gaps are skipped', () => {
  const reg = createRegistry(1);
  const s = [{ key: 'a', name: 'A', values: [1, 1] }, { key: 'b', name: 'B', values: [4, null] }, { key: 'c', name: 'C', values: [8, 6] }];
  assert.deepEqual(foldOther(s, reg, { combine: 'mean' })[1].values, [6, 6]);
  assert.deepEqual(foldOther(s, createRegistry(1), { combine: 'max' })[1].values, [8, 6]);
  assert.deepEqual(foldOther([{ key: 'a', name: 'A', values: [1] }, { key: 'b', name: 'B', values: [null] }], createRegistry(1))[1].values, [null]);
});
