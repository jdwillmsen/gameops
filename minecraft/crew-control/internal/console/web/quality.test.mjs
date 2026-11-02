import test from 'node:test';
import assert from 'node:assert/strict';
import { QUALITIES, DEFAULT_QUALITY, WALL_QUALITY_CHOICES, qualityOf, wallQualityLabel } from './quality.js';

test('levels are cheapest first and default to high', () => {
  assert.deepEqual(QUALITIES, ['low', 'med', 'high']);
  assert.equal(DEFAULT_QUALITY, 'high');
  assert.deepEqual(WALL_QUALITY_CHOICES, ['auto', 'low', 'med', 'high']);
});

test('only the exact level names are recognised', () => {
  for (const q of QUALITIES) assert.equal(qualityOf(q), q);
  for (const q of ['auto', 'LOW', 'ultra', '', null, undefined]) assert.equal(qualityOf(q), 'high', String(q));
});

test('every wall choice has a label', () => {
  for (const q of WALL_QUALITY_CHOICES) assert.match(wallQualityLabel(q), /^(Auto|Low|Med|High)$/);
  assert.equal(wallQualityLabel('bogus'), 'Auto');
});
