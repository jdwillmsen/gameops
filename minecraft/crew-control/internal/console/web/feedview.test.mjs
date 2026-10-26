import test from 'node:test';
import assert from 'node:assert/strict';
import { createFeedView, perFrame } from './feedview.js';

const box = () => ({ calls: 0, kids: [], replaceChildren(...k) { this.calls++; this.kids = k; } });

test('an unchanged list does not touch the DOM', () => {
  const b = box(), made = [];
  const v = createFeedView(b, e => { made.push(e.seq); return { seq: e.seq }; });
  assert.equal(v.render([{ seq: 1 }, { seq: 2 }]), true);
  assert.equal(v.render([{ seq: 1 }, { seq: 2 }]), false);
  assert.equal(b.calls, 1);
  assert.deepEqual(made, [1, 2]);
});

test('rows that stay shown are reused, only new events build a row', () => {
  const b = box(), made = [];
  const v = createFeedView(b, e => { made.push(e.seq); return { seq: e.seq }; });
  v.render([{ seq: 1 }, { seq: 2 }]);
  const first = b.kids[1];
  v.render([{ seq: 2 }, { seq: 3 }]);
  assert.equal(b.kids[0], first);
  assert.deepEqual(made, [1, 2, 3]);
});

test('an empty list shows the placeholder once', () => {
  const b = box();
  const v = createFeedView(b, e => e);
  assert.equal(v.render([], () => 'none'), true);
  assert.equal(v.render([], () => 'none'), false);
  assert.deepEqual(b.kids, ['none']);
});

test('perFrame coalesces a burst into one call', () => {
  let queued = null, runs = 0;
  const f = perFrame(() => runs++, cb => { queued = cb; });
  f(); f(); f();
  assert.equal(runs, 0);
  queued();
  assert.equal(runs, 1);
  f();
  queued();
  assert.equal(runs, 2);
});
