import test from 'node:test';
import assert from 'node:assert/strict';
import { whyRows, countText } from './why.js';

test('no failures gives one plain row', () => {
  const rows = whyRows({ total: 0, findings: [], unexplained: { count: 0 } });
  assert.equal(rows.length, 1);
  assert.equal(rows[0].kind, 'none');
});

test('findings keep their rule and chain, and the unexplained bucket follows as a row', () => {
  const rows = whyRows({
    total: 5,
    findings: [{ rule: 'missing_tool.kit_failed', sentence: 'tool missing', chain: [3, 4], count: 4 }],
    unexplained: { count: 1, failures: [9], sentence: '1 failed task(s) have no cause in the window; no cause is guessed' },
  });
  assert.deepEqual(rows.map(r => r.kind), ['finding', 'unexplained']);
  assert.deepEqual(rows[0].chain, [3, 4]);
  assert.equal(rows[0].rule, 'missing_tool.kit_failed');
  assert.equal(rows[1].rule, '');
  assert.equal(rows[1].count, 1);
});

test('a window with everything explained has no unexplained row', () => {
  const rows = whyRows({ total: 2, findings: [{ rule: 'r', sentence: 's', chain: [], count: 2 }], unexplained: { count: 0 } });
  assert.deepEqual(rows.map(r => r.kind), ['finding']);
});

test('countText', () => {
  assert.equal(countText(1), 'once');
  assert.equal(countText(3), '3 times');
});
