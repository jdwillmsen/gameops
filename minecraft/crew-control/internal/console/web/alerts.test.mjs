import test from 'node:test';
import assert from 'node:assert/strict';
import { isMuted, isLoud, bellCount, ageText, newlyLoud, visible, ruleLabel } from './alerts.js';

const now = Date.parse('2026-10-26T12:00:00Z');
const al = (o = {}) => ({ id: 'unit_died.a', rule: 'unit_died', unit: 'a', severity: 'crit', state: 'firing', since: '2026-10-26T11:59:00Z', acked: false, ...o });

test('muted only while the period runs', () => {
  assert.equal(isMuted(al({ muted_until: '2026-10-26T12:05:00Z' }), now), true);
  assert.equal(isMuted(al({ muted_until: '2026-10-26T11:59:00Z' }), now), false);
  assert.equal(isMuted(al(), now), false);
});

test('bell counts only loud alerts', () => {
  const list = [al(), al({ id: 'b', acked: true }), al({ id: 'c', state: 'resolved' }), al({ id: 'd', muted_until: '2026-10-26T13:00:00Z' })];
  assert.equal(isLoud(list[0], now), true);
  assert.equal(bellCount(list, now), 1);
});

test('age reads in the largest unit', () => {
  assert.equal(ageText('2026-10-26T11:59:30Z', now), '30s');
  assert.equal(ageText('2026-10-26T11:55:00Z', now), '5m');
  assert.equal(ageText('2026-10-26T10:30:00Z', now), '1h 30m');
  assert.equal(ageText('junk', now), '');
});

test('a toast fires once per firing, again after a re-fire', () => {
  const seen = new Set();
  assert.equal(newlyLoud(seen, [al()], now).length, 1);
  assert.equal(newlyLoud(seen, [al()], now).length, 0);
  assert.equal(newlyLoud(seen, [al({ since: '2026-10-26T11:59:59Z' })], now).length, 1);
});

test('an acked or muted alert is not toasted but is remembered', () => {
  const seen = new Set();
  assert.equal(newlyLoud(seen, [al({ acked: true })], now).length, 0);
  assert.equal(newlyLoud(seen, [al()], now).length, 0);
});

test('visible filters and puts firing critical first', () => {
  const list = [al({ id: 'w', severity: 'warn' }), al({ id: 'r', state: 'resolved' }), al({ id: 'c' })];
  assert.deepEqual(visible(list, { show: 'active', severity: 'all' }, now).map(a => a.id), ['c', 'w']);
  assert.deepEqual(visible(list, { show: 'all', severity: 'all' }, now).map(a => a.id), ['c', 'w', 'r']);
  assert.deepEqual(visible(list, { show: 'all', severity: 'warn' }, now).map(a => a.id), ['w']);
});

test('unknown rules show their id', () => {
  assert.equal(ruleLabel('unit_died'), 'Unit died');
  assert.equal(ruleLabel('x'), 'x');
});
