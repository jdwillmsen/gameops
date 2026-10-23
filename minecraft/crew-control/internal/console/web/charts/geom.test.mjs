import test from 'node:test';
import assert from 'node:assert/strict';
import { areaPath, arcPath, barPath, labelsFit, linePath, textWidth, truncate } from './geom.js';

test('linePath lifts the pen over gaps', () => {
  assert.equal(linePath([0, 10, 20, 30], [5, null, 7, 8]), 'M0 5M20 7L30 8');
  assert.equal(linePath([], []), '');
});

test('areaPath closes one shape per unbroken run', () => {
  const d = areaPath([0, 10, 20], [1, null, 3], [9, 9, 9]);
  assert.equal(d.match(/Z/g).length, 2);
  assert.equal(areaPath([0, 10], [1, 2], [9, 9]), 'M0 1L10 2L10 9L0 9Z');
});

test('barPath rounds only the data end', () => {
  assert.equal(barPath(0, 0, 10, 10, 4, null), 'M0 0H10V10H0Z');
  const top = barPath(0, 0, 10, 20, 4, 'top');
  assert.match(top, /^M0 20V4A4 4 0 0 1 4 0H6A4 4 0 0 1 10 4V20Z$/);
  assert.match(barPath(0, 0, 20, 10, 4, 'right'), /^M0 0H16A4 4 0 0 1 20 4V6A4 4 0 0 1 16 10H0Z$/);
  // The radius never exceeds half the thickness or the length.
  assert.match(barPath(0, 0, 4, 20, 4, 'top'), /A2 2 /);
  assert.match(barPath(0, 0, 10, 2, 4, 'top'), /A2 2 /);
  assert.equal(barPath(0, 0, 0, 5, 4, 'top'), '');
});

test('arcPath flags the long way round past 180 degrees', () => {
  assert.match(arcPath(50, 50, 40, 135, 405), / 0 1 1 /);
  assert.match(arcPath(50, 50, 40, 135, 200), / 0 0 1 /);
});

test('end labels are dropped, not nudged, when they collide', () => {
  assert.equal(labelsFit([10, 40, 80], 14), true);
  assert.equal(labelsFit([10, 18, 80], 14), false);
  assert.equal(labelsFit([], 14), true);
});

test('truncate and width estimate', () => {
  assert.equal(truncate('abcdefghij', 5), 'abcd…');
  assert.equal(truncate('abc', 5), 'abc');
  assert.equal(textWidth('abcd'), 24.8);
});
