import test from 'node:test';
import assert from 'node:assert/strict';
import { summarizeFrames, check, longestTask } from './budget-lib.mjs';

test('summarizeFrames reports fps from elapsed time', () => {
  const s = summarizeFrames([16, 16, 16, 16]);
  assert.equal(Math.round(s.fps), 63);
  assert.equal(s.over50, 0);
});

test('a stall drags fps down and is counted', () => {
  const s = summarizeFrames([16, 16, 500, 16]);
  assert.ok(s.fps < 10);
  assert.equal(s.over50, 1);
  assert.equal(s.maxFrameMs, 500);
});

test('no frames is zero fps, not NaN', () => {
  assert.equal(summarizeFrames([]).fps, 0);
});

test('check passes inside the limits and names each breach', () => {
  const limits = { fpsMin: 30, heapMbMax: 100 };
  assert.deepEqual(check({ fps: 45, heapMb: 80 }, limits), []);
  const bad = check({ fps: 20, heapMb: 120 }, limits);
  assert.equal(bad.length, 2);
  assert.match(bad[0], /fps 20 breaks fpsMin 30/);
});

test('a limit with no measurement fails rather than passing silently', () => {
  assert.deepEqual(check({}, { fpsMin: 30 }), ['fps: not measured']);
});

test('longestTask', () => {
  assert.equal(longestTask([{ duration: 10 }, { duration: 70 }]), 70);
  assert.equal(longestTask([]), 0);
});
