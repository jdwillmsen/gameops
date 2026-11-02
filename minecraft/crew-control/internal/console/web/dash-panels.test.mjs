import test from 'node:test';
import assert from 'node:assert/strict';
import { PANELS, PANEL_TYPES, defaultLayout, twoPoints } from './dash-panels.js';
import { normalizeLayout } from './dash.js';
import { createDashData } from './dash-data.js';

const T0 = Date.parse('2026-10-26T12:00:00Z');
const unit = (id, o = {}) => ({ id, name: id.toUpperCase(), online: true, dead: false, health: 20, hunger: 20, task: null, ...o });
const ev = (seq, s, unit, kind, extra = {}) => ({ seq, at: new Date(T0 + s * 1000).toISOString(), unit, kind, level: 'info', text: kind, ...extra });

function ctx() {
  const data = createDashData();
  data.onUnits([unit('a'), unit('b', { task: { template: 'gather_and_deposit' } }), unit('c', { hunger: 3 })], T0);
  data.onUnits([unit('a'), unit('b', { task: { template: 'gather_and_deposit' } }), unit('c', { hunger: 3 })], T0 + 6000);
  data.onEvents([ev(1, 0, 'a', 'task_started', { task: 't' }), ev(2, 9, 'a', 'task_done', { task: 't' }), ev(3, 10, 'c', 'low_hunger', { level: 'warn' })]);
  const filter = { unit: '', from: T0 - 60_000, to: T0 + 60_000 };
  return { data, units: data.units(), filter, now: T0 + 12_000 };
}

test('the default layout is made of known panels and survives normalizing', () => {
  const l = defaultLayout();
  assert.ok(l.panels.length >= 8, 'ships at least eight panels');
  assert.deepEqual(normalizeLayout(l, PANEL_TYPES), l);
});

test('every panel query returns a JSON-safe model', () => {
  const c = ctx();
  for (const [type, def] of Object.entries(PANELS)) {
    const m = def.query(c);
    assert.doesNotThrow(() => JSON.stringify(m), type);
    assert.equal(typeof def.create, 'function', type);
  }
});

test('queries read the filter', () => {
  const c = ctx();
  assert.equal(PANELS['success-rate'].query(c).rate, 100);
  assert.deepEqual(PANELS['hunger'].query(c).categories, ['C', 'A', 'B']);
  assert.equal(PANELS['alert-count'].query(c).alerts, 1);
  assert.deepEqual(PANELS['unit-status'].query(c).rows.map(r => r.state), ['idle', 'working', 'idle']);
  assert.equal(PANELS['task-durations'].query(c).n, 1);
  assert.equal(PANELS['recent-events'].query(c).rows.length, 1);
  const none = { ...c, filter: { ...c.filter, unit: 'b' } };
  assert.equal(PANELS['success-rate'].query(none).rate, null);
  assert.equal(PANELS['recent-events'].query(none).rows.length, 0);
});

test('a single point is padded so a line has a width', () => {
  const m = twoPoints({ x: [5000], series: [{ key: 'k', name: 'K', values: [3] }] });
  assert.deepEqual(m.x, [4000, 5000]);
  assert.deepEqual(m.series[0].values, [null, 3]);
  const keep = { x: [1, 2], series: [] };
  assert.equal(twoPoints(keep), keep);
});

test('the world panel is on the Add menu, reads the filter, and is JSON-safe in every state', () => {
  assert.ok(PANEL_TYPES.has('world-compare'));
  assert.ok(!defaultLayout().panels.some(p => p.type === 'world-compare'), 'one lab has nothing to compare, so it is added on request');
  assert.deepEqual(normalizeLayout({ panels: [{ id: 'p1', type: 'world-compare', w: 12, h: 4 }] }, PANEL_TYPES).panels.map(p => p.type), ['world-compare']);

  const c = ctx();
  const m = PANELS['world-compare'].query(c);
  assert.equal(m.state, 'ready');
  assert.deepEqual(m.worlds.map(w => w.world), ['lab'], 'units from a server that names no world are in the lab');
  assert.equal(m.worlds[0].online, 3);
  assert.ok(m.worlds[0].cells.every(x => x.delta === null), 'a lone world has no median to differ from');
  assert.deepEqual(m.chart.categories, ['other'], 'a finished task with no template is counted as other');
  assert.doesNotThrow(() => JSON.stringify(m));

  const empty = createDashData();
  assert.equal(PANELS['world-compare'].query({ data: empty, units: [], filter: c.filter, now: c.now }).state, 'loading');
});

test('a failure inside the comparison becomes an error state, not a blank panel', () => {
  const broken = { data: { feed: () => { throw new Error('x'); }, samples: () => [] }, units: [unit('a')], filter: { unit: '', from: 0, to: 1 }, now: 1 };
  const silence = console.error;
  console.error = () => {};
  try {
    const m = PANELS['world-compare'].query(broken);
    assert.equal(m.state, 'error');
    assert.match(m.message, /^Could not compare the worlds\./);
  } finally {
    console.error = silence;
  }
});
