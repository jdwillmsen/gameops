import test from 'node:test';
import assert from 'node:assert/strict';
import { levelOf, legendSteps, boundsOf, fit, cellIndex, cellAt, hottest, summary, heatQuery, LAB, LEVELS } from './heat.js';

test('levelOf: empty is 0, the maximum is the top class, and it never decreases', () => {
  assert.equal(levelOf(0, 10), 0);
  assert.equal(levelOf(10, 10), LEVELS);
  assert.equal(levelOf(1, 1000), 1);
  let prev = 0;
  for (let n = 1; n <= 100; n++) { const l = levelOf(n, 100); assert.ok(l >= prev); prev = l; }
});

test('legendSteps start at 1, rise, and agree with levelOf', () => {
  const steps = legendSteps(100);
  assert.equal(steps[0].from, 1);
  for (const s of steps) assert.ok(levelOf(s.from, 100) >= s.level - 1);
  for (let i = 1; i < steps.length; i++) assert.ok(steps[i].from > steps[i - 1].from);
});

test('boundsOf always covers the lab and grows to fit far cells', () => {
  const b = boundsOf({ cell: 4, cells: [] });
  assert.ok(b.minX < LAB.minX && b.maxX > LAB.maxX && b.minZ < LAB.minZ && b.maxZ > LAB.maxZ);
  const far = boundsOf({ cell: 4, cells: [[200, 10, 1]] });
  assert.ok(far.maxX >= 803);
});

test('fit round-trips between world and pixel space and keeps aspect', () => {
  const b = { minX: 0, maxX: 99, minZ: 0, maxZ: 49 };
  const p = fit(b, 200, 200);
  assert.equal(p.scale, 2);
  assert.equal(Math.round(p.wx(p.x(40))), 40);
  assert.equal(Math.round(p.wz(p.y(10))), 10);
});

test('cellAt floors negative coordinates into the right cell', () => {
  const g = { cell: 4, cells: [[-1, -125, 7]] };
  const c = cellAt(g, cellIndex(g), -1, -497);
  assert.deepEqual([c.cx, c.cz, c.n, c.x0, c.x1], [-1, -125, 7, -4, -1]);
});

test('hottest orders by count then position; summary and query are plain', () => {
  const g = { cell: 4, unit: '', samples: 9, max: 5, cells: [[1, 1, 2], [0, 0, 5], [2, 2, 5]] };
  assert.deepEqual(hottest(g, 2).map(c => c[0]), [0, 2]);
  assert.match(summary(g), /3 cells of 4x4 blocks, 9 samples from the fleet/);
  assert.equal(summary({ cells: [] }), 'No samples in this window yet.');
  assert.equal(heatQuery({ layer: 'travel', window: '1h', unit: 'crew-02' }), '/v1/heatmap?layer=travel&window=1h&unit=crew-02');
  assert.equal(heatQuery({ layer: 'position', window: '6h', unit: '' }), '/v1/heatmap?layer=position&window=6h');
});
