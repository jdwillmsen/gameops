import test from 'node:test';
import assert from 'node:assert/strict';
import { capacityTiles, templateRows, insightRows, percent } from './insights.js';

const report = (over = {}) => ({
  busy_units: 10.2, working_units: 16, utilization: 0.6375, non_stationary: false, unknown_durations: 0,
  templates: [{ template: 'gather', lambda_per_min: 24.4, mean_duration_s: 16.9, l: 6.87, failed_time_s: 12.4, non_stationary: false }],
  ...over,
});

test('tiles show busy units and utilization', () => {
  const t = capacityTiles(report());
  assert.deepEqual(t.map(x => x.value), ['10.2', '64%']);
});

test('an unsteady window is called out', () => {
  const t = capacityTiles(report({ non_stationary: true }));
  assert.equal(t[2].value, 'Unsteady');
});

test('missing durations are called out when the window is steady', () => {
  const t = capacityTiles(report({ unknown_durations: 3 }));
  assert.equal(t[2].value, 'Partial');
});

test('no working units shows no utilization', () => {
  assert.equal(capacityTiles(report({ working_units: 0, utilization: 0 }))[1].value, '-');
  assert.deepEqual(capacityTiles(null), []);
});

test('template rows are formatted', () => {
  assert.deepEqual(templateRows(report())[0], { template: 'gather', lambda: '24.4', mean: '16.9', l: '6.9', failedTime: 12, unsteady: false });
});

test('firing insights come first and resolved ones are capped', () => {
  const list = [
    { id: 5, rule: 'R1', unit: 'crew-16', severity: 'crit', state: 'firing', first_seen: 'x', detail: 'd', evidence: [1] },
    { id: 4, rule: 'R4', fleet: true, severity: 'crit', state: 'resolved', first_seen: 'x', detail: 'd' },
    { id: 3, rule: 'R2', unit: 'a', severity: 'warn', state: 'resolved', first_seen: 'x', detail: 'd' },
    { id: 2, rule: 'R2', unit: 'b', severity: 'warn', state: 'resolved', first_seen: 'x', detail: 'd' },
    { id: 1, rule: 'R2', unit: 'c', severity: 'warn', state: 'resolved', first_seen: 'x', detail: 'd' },
  ];
  const rows = insightRows(list);
  assert.equal(rows.length, 4);
  assert.equal(rows[0].state, 'firing');
  assert.equal(rows[0].ruleName, 'Zero success');
  assert.equal(rows[1].who, 'Fleet');
});

test('percent rounds', () => assert.equal(percent(0.505), '51%'));
