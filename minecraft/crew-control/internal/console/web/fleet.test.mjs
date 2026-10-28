import test from 'node:test';
import assert from 'node:assert/strict';
import { alertsOf, counts, taskLine, sortUnits, step, nextAlerted, gridFor, pageOf, mergeEvents, filterEvents, ago, project, nearest, fuzzy, rankItems, worst, budgetsFor, budgetLabel } from './fleet.js';

const u = (id, extra = {}) => ({ id, name: id, online: true, dead: false, health: 20, hunger: 20, task: null, ...extra });

test('alertsOf: offline hides everything else, dead and low stats are flagged', () => {
  assert.deepEqual(alertsOf(u('a', { online: false, health: 1 })).map(a => a.kind), ['offline']);
  assert.deepEqual(alertsOf(u('a', { dead: true, health: 0 })).map(a => a.kind), ['dead']);
  assert.deepEqual(alertsOf(u('a', { health: 6, hunger: 3 })).map(a => a.kind), ['low_health', 'low_hunger']);
  assert.deepEqual(alertsOf(u('a')), []);
  assert.equal(worst(alertsOf(u('a', { dead: true }))), 'crit');
  assert.equal(worst(alertsOf(u('a', { hunger: 1 }))), 'warn');
  assert.equal(worst([]), '');
});

test('counts splits the fleet', () => {
  const list = [u('a'), u('b', { task: { template: 'haul', progress: 1, qty: 4 } }), u('c', { dead: true }), u('d', { online: false }), u('e', { health: 2 })];
  assert.deepEqual(counts(list), { total: 5, online: 4, working: 1, idle: 2, dead: 1, offline: 1, alerts: 3 });
});

test('taskLine', () => {
  assert.equal(taskLine(u('a')), 'idle');
  assert.equal(taskLine(u('a', { online: false })), '-');
  assert.equal(taskLine(u('a', { task: { template: 'gather_and_deposit', progress: 3, qty: 8 } })), 'gather 3/8');
  assert.equal(taskLine(u('a', { task: { template: 'goto', progress: 0, qty: 0 } })), 'goto');
  assert.equal(taskLine(u('a', { task: { template: 'repair_build', progress: 0, qty: 0 } })), 'repair');
  assert.equal(taskLine(u('a', { task: { template: 'build_blueprint', progress: 12, qty: 80 } })), 'build 12/80');
});

test('sortUnits is stable, unknown numbers sort last, desc reverses', () => {
  const list = [u('a', { health: 10 }), u('b', { health: null }), u('c', { health: 4 })];
  assert.deepEqual(sortUnits(list, 'health').map(s => s.id), ['c', 'a', 'b']);
  assert.deepEqual(sortUnits(list, 'health', true).map(s => s.id), ['b', 'a', 'c']);
  assert.deepEqual(sortUnits(list, 'unit').map(s => s.id), ['a', 'b', 'c']);
  assert.deepEqual(sortUnits([u('a'), u('b', { dead: true }), u('c', { hunger: 2 })], 'alerts').map(s => s.id), ['b', 'c', 'a']);
});

test('step wraps both ways and survives an unknown or empty fleet', () => {
  const o = ['a', 'b', 'c'];
  assert.equal(step(o, 'c', 1), 'a');
  assert.equal(step(o, 'a', -1), 'c');
  assert.equal(step(o, 'zzz', 1), 'a');
  assert.equal(step([], 'a', 1), '');
});

test('nextAlerted goes worst first, then rotates', () => {
  const list = [u('a'), u('b', { hunger: 1 }), u('c', { dead: true }), u('d', { online: false })];
  assert.equal(nextAlerted(list, 'a'), 'c');
  assert.equal(nextAlerted(list, 'c'), 'd');
  assert.equal(nextAlerted(list, 'd'), 'b');
  assert.equal(nextAlerted(list, 'b'), 'c');
  assert.equal(nextAlerted([u('a')], 'a'), '');
});

test('gridFor picks the grid that gives the biggest tiles', () => {
  assert.deepEqual(gridFor(1, 1600, 900), { cols: 1, rows: 1 });
  assert.deepEqual(gridFor(4, 1600, 900), { cols: 2, rows: 2 });
  const g = gridFor(20, 1920, 1000);
  assert.ok(g.cols * g.rows >= 20);
  assert.ok(g.cols >= 5);
  // A tall, narrow window stacks the tiles.
  assert.equal(gridFor(3, 400, 1200).cols, 1);
  assert.deepEqual(gridFor(0, 100, 100), { cols: 1, rows: 1 });
});

test('pageOf pages the live tiles and clamps', () => {
  const ids = Array.from({ length: 20 }, (_, i) => `u${i}`);
  assert.deepEqual(pageOf(ids, 9, 0).ids, ids.slice(0, 9));
  assert.equal(pageOf(ids, 9, 0).pages, 3);
  assert.deepEqual(pageOf(ids, 9, 2).ids, ids.slice(18));
  assert.equal(pageOf(ids, 9, 99).page, 2);
  assert.equal(pageOf(ids, 9, -3).page, 0);
  const all = pageOf(ids, 20, 0);
  assert.equal(all.pages, 1);
  assert.equal(all.live, true);
  const cards = pageOf(ids, 0, 4);
  assert.deepEqual(cards.ids, ids);
  assert.equal(cards.live, false);
});

test('mergeEvents drops repeats, orders by seq and caps', () => {
  const e = seq => ({ seq, unit: 'a', level: 'info', kind: 'x', text: '' });
  const r1 = mergeEvents([], [e(2), e(1)]);
  assert.deepEqual(r1.feed.map(x => x.seq), [1, 2]);
  const r2 = mergeEvents(r1.feed, [e(2), e(3)]);
  assert.deepEqual(r2.added.map(x => x.seq), [3]);
  const r3 = mergeEvents(r2.feed, [e(2)]);
  assert.equal(r3.feed, r2.feed);
  assert.deepEqual(mergeEvents(r2.feed, [e(4)], 2).feed.map(x => x.seq), [3, 4]);
});

test('filterEvents by unit and level', () => {
  const feed = [{ unit: 'a', level: 'info' }, { unit: 'b', level: 'warn' }, { unit: 'a', level: 'crit' }, { unit: 'a', level: 'ok' }];
  assert.equal(filterEvents(feed, { unit: 'a' }).length, 3);
  assert.equal(filterEvents(feed, { level: 'warn' }).length, 2);
  assert.equal(filterEvents(feed, { level: 'crit' }).length, 1);
  assert.equal(filterEvents(feed).length, 4);
});

test('ago', () => {
  assert.equal(ago(-5), '0s');
  assert.equal(ago(59_000), '59s');
  assert.equal(ago(61_000), '1m');
  assert.equal(ago(3 * 3600_000), '3h');
});

test('project keeps equal scale, flips nothing, ignores unknown positions', () => {
  const pts = project([
    { id: 'a', x: 0, z: 0, known: true, yaw: 0 },
    { id: 'b', x: 100, z: 50, known: true, yaw: 0 },
    { id: 'c', x: 9999, z: 9999, known: false },
  ], 200, 200, 0);
  assert.equal(pts.length, 2);
  const [a, b] = pts;
  assert.equal(b.x - a.x, 200);
  assert.equal(b.y - a.y, 100);
  assert.deepEqual(project([], 100, 100), []);
  // A lone unit lands in the middle, and a tiny spread does not blow up the scale.
  assert.deepEqual(project([{ id: 'a', x: 5, z: 5, known: true }], 100, 100).map(p => [p.x, p.y]), [[50, 50]]);
});

test('nearest finds the closest dot within reach', () => {
  const pts = [{ id: 'a', x: 10, y: 10 }, { id: 'b', x: 30, y: 10 }];
  assert.equal(nearest(pts, 28, 12), 'b');
  assert.equal(nearest(pts, 100, 100), '');
});

test('fuzzy: subsequence only, prefix and word starts win', () => {
  assert.equal(fuzzy('xyz', 'focus crew-01'), -1);
  assert.ok(fuzzy('fc', 'focus crew') >= 0);
  assert.ok(fuzzy('foc', 'focus ada') > fuzzy('foc', 'a far off cat'));
  assert.equal(fuzzy('', 'anything'), 0);
});

test('rankItems orders by score and honours the limit', () => {
  const items = [{ label: 'Open roster' }, { label: 'Toggle wall' }, { label: 'Wall page next', keywords: 'pager' }];
  assert.equal(rankItems(items, 'wall')[0].label, 'Toggle wall');
  assert.equal(rankItems(items, 'pager')[0].label, 'Wall page next');
  assert.equal(rankItems(items, '', 2).length, 2);
  assert.equal(rankItems(items, 'qqq').length, 0);
});

test('budgets offered fit the fleet', () => {
  assert.deepEqual(budgetsFor(3), [0, 999]);
  assert.deepEqual(budgetsFor(20), [0, 4, 6, 9, 12, 999]);
  assert.deepEqual(['Cards', '6', 'All'], [0, 6, 999].map(budgetLabel));
});
