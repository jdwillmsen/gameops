import test from 'node:test';
import assert from 'node:assert/strict';
import { createGovernor, lowerBudget } from './governor.js';

function run(g, ms, from = 1) {
  let t = from, tripped = false;
  for (let i = 0; i < 2000 && t < from + 12000; i++, t += ms) tripped = g.frame(t) || tripped;
  return tripped;
}

test('a healthy frame rate never trips', () => {
  assert.equal(run(createGovernor(), 16), false);
});

test('a sustained low frame rate trips once the hold has passed', () => {
  assert.equal(run(createGovernor(), 80), true);
});

test('a single stall is not a slow page', () => {
  const g = createGovernor();
  for (let t = 1; t < 3000; t += 16) g.frame(t);
  g.frame(9000);
  for (let t = 9016; t < 12000; t += 16) assert.equal(g.frame(t), false);
});

test('reset forgets earlier slow frames', () => {
  const g = createGovernor({ holdMs: 1000 });
  for (let t = 1; t < 900; t += 80) g.frame(t);
  g.reset();
  assert.equal(g.frame(1000), false);
});

test('lowerBudget steps down through the fixed budgets and stops at the smallest', () => {
  const all = 999;
  const bs = [0, 4, 6, 9, 12, all];
  assert.equal(lowerBudget(bs, all, all), 12);
  assert.equal(lowerBudget(bs, 12, all), 9);
  assert.equal(lowerBudget(bs, 4, all), 0);
  assert.equal(lowerBudget([0, 4, 6, 9, all], all, all), 9);
});
