import test from 'node:test';
import assert from 'node:assert/strict';
import { deltaText, changeText, netRows, entryRows, summary } from './invhistory.js';

test('a delta spells its sign', () => {
  assert.equal(deltaText(12), '+12');
  assert.equal(deltaText(-3), '−3');
  assert.equal(deltaText(0), '0');
});

test('an item that appeared or vanished says so', () => {
  assert.equal(changeText({ item: 'minecraft:iron_ingot', from: 0, to: 4 }), 'iron ingot +4 (new)');
  assert.equal(changeText({ item: 'minecraft:coal', from: 3, to: 0 }), 'coal −3 (gone)');
  assert.equal(changeText({ item: 'stone', from: 10, to: 25 }), 'stone +15 (10 → 25)');
});

test('net rows keep the server order and class by sign', () => {
  const rows = netRows({ net: [{ item: 'minecraft:stone', delta: 15 }, { item: 'coal', delta: -2 }] });
  assert.deepEqual(rows.map(r => [r.item, r.text, r.cls]), [['stone', '+15', 'gain'], ['coal', '−2', 'loss']]);
  assert.deepEqual(netRows(null), []);
});

test('entries carry their time and worded changes', () => {
  const rows = entryRows({ entries: [{ at: '2026-10-26T12:00:00Z', changes: [{ item: 'dirt', from: 0, to: 1 }] }] });
  assert.deepEqual(rows, [{ at: '2026-10-26T12:00:00Z', changes: ['dirt +1 (new)'] }]);
});

test('an empty window, a truncated one and a failed read read differently', () => {
  assert.match(summary({ entries: [], net: [] }, 7), /No inventory changes recorded in the last 7 days/);
  assert.match(summary({ entries: [{}], truncated: false }, 1), /^1 change recorded in the last day\.$/);
  assert.match(summary({ entries: [{}, {}], truncated: true }, 30), /2 changes.*only the newest part/);
  assert.match(summary(null, 7), /could not be read/);
});
