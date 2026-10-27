import test from 'node:test';
import assert from 'node:assert/strict';
import { buildRequest, confirmText, outcome, MAX_UNITS } from './bulk.js';

const pt = { x: '10', y: '64', z: '-20' };

test('recall builds a confirmed request for exactly the selected units', () => {
  const r = buildRequest('recall', ['a', 'b'], { to: pt });
  assert.deepEqual(r.body, { units: ['a', 'b'], confirm: 2, action: 'recall', to: { x: 10, y: 64, z: -20 } });
  assert.equal(r.what, 'Walk to 10 64 -20');
});

test('assign gather and haul reuse the task rules', () => {
  const g = buildRequest('assign', ['a'], { template: 'gather', item: 'cobblestone', qty: '8', chest: pt });
  assert.equal(g.body.task.template, 'gather_and_deposit');
  assert.match(g.what, /Gather 8 cobblestone/);
  const h = buildRequest('assign', ['a'], { template: 'haul', item: 'oak_log', qty: '4', chest: pt, from: pt });
  assert.match(h.what, /Haul 4 oak_log/);
});

test('bad input is refused before anything is sent', () => {
  assert.match(buildRequest('recall', [], { to: pt }).error, /at least one/);
  const many = Array.from({ length: MAX_UNITS + 1 }, (_, i) => `u${i}`);
  assert.match(buildRequest('recall', many, { to: pt }).error, /at most 16/);
  assert.ok(buildRequest('recall', ['a'], { to: { x: '9999', y: '64', z: '0' } }).error);
  assert.ok(buildRequest('recall', ['a'], {}).error);
  assert.ok(buildRequest('assign', ['a'], { template: 'gather', item: 'diamond', qty: '1', chest: pt }).error);
  assert.equal(buildRequest('explode', ['a'], {}).error, 'unknown action');
});

test('the confirmation says what happens to current work', () => {
  assert.match(confirmText('recall', ['Agate'], 'Walk to 1 2 3'), /Agate\. Their current and queued work is cancelled/);
  assert.match(confirmText('assign', ['a', 'b', 'c', 'd'], 'Gather'), /4 units\. It joins their queue/);
});

test('the outcome names the units that failed', () => {
  assert.equal(outcome([{ unit: 'a', ok: true }, { unit: 'b', ok: true }]).text, 'sent to 2 units');
  const o = outcome([{ unit: 'a', ok: true }, { unit: 'b', ok: false, error: 'queue unavailable' }], { b: 'Basalt' });
  assert.deepEqual([o.ok, o.failed], [1, 1]);
  assert.match(o.text, /1 sent, 1 failed: Basalt \(queue unavailable\)/);
  assert.equal(outcome(null).text, 'sent to 0 units');
});
