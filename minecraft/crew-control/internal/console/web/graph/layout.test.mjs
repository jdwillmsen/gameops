import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { buildGraph, splitCycles, layerOf, crossings, reduceCrossings, criticalPath, layout } from './layout.js';

const nodes = ids => ids.map(id => ({ id }));
const edges = pairs => pairs.map(([from, to]) => ({ from, to }));

test('layerOf: longest path puts a dependency at least one layer under its dependent', () => {
  const g = buildGraph(nodes(['a', 'b', 'c']), edges([['a', 'b'], ['b', 'c']]));
  const { layer } = layerOf(g, splitCycles(g));
  assert.equal(layer.get('a'), 0);
  assert.equal(layer.get('b'), 1);
  assert.equal(layer.get('c'), 2);
});

test('layerOf: the longest branch decides the layer, not the first dependency', () => {
  // a -> b -> d, and c -> d: d sits at 2 whatever order the dependencies
  // arrive in, because b already sits at 1.
  const g = buildGraph(nodes(['a', 'b', 'c', 'd']), edges([['a', 'b'], ['b', 'd'], ['c', 'd']]));
  const { layer } = layerOf(g, splitCycles(g));
  assert.equal(layer.get('d'), 2);
  assert.equal(layer.get('c'), 0);
});

test('splitCycles: a cycle is reported and the loop ends', () => {
  const g = buildGraph(nodes(['a', 'b', 'c']), edges([['a', 'b'], ['b', 'c'], ['c', 'a']]));
  const { order, cyclic } = splitCycles(g);
  assert.deepEqual(order, []);
  assert.deepEqual(cyclic.sort(), ['a', 'b', 'c']);
});

test('layout does not hang on a cycle and reports it (child process with a timeout)', () => {
  // A synchronous function that hangs would take the whole suite down, so the
  // hang check runs in a child with its own clock.
  const mod = pathToFileURL(path.join(path.dirname(fileURLToPath(import.meta.url)), 'layout.js')).href;
  const out = spawnSync(process.execPath, ['--input-type=module', '--eval', `
    import { layout } from ${JSON.stringify(mod)};
    const r = layout([{ id: 'a' }, { id: 'b' }], [{ from: 'a', to: 'b' }, { from: 'b', to: 'a' }]);
    console.log(JSON.stringify({ cycle: r.cycle.length, drawn: r.pos.size }));
  `], { timeout: 2000, encoding: 'utf8' });
  assert.equal(out.status, 0, out.stderr);
  const result = JSON.parse(out.stdout.trim().split('\n').at(-1));
  assert.equal(result.cycle, 2);
  assert.equal(result.drawn, 2, 'every node still gets a position');
});

test('layout: a cycle node is drawn rather than dropped', () => {
  const r = layout(nodes(['a', 'b', 'c']), edges([['a', 'b'], ['b', 'c'], ['c', 'a']]));
  const xs = [...r.pos.values()].map(p => p.x);
  assert.equal(r.cycle.length, 3);
  assert.equal(new Set(xs).size, 1, 'the cycle nodes share one layer');
  assert.deepEqual(r.layers.at(-1).sort(), ['a', 'b', 'c']);
});

test('layout: only the nodes on a cycle are reported, not what hangs off it', () => {
  // a -> b -> a is the cycle; c waits on b and d waits on c, so they can never
  // start but are not part of the loop. e is unrelated.
  const r = layout(nodes(['a', 'b', 'c', 'd', 'e']),
    edges([['a', 'b'], ['b', 'a'], ['b', 'c'], ['c', 'd']]));
  assert.deepEqual([...r.cycle].sort(), ['a', 'b']);
  assert.deepEqual(r.blocked.sort(), ['c', 'd']);
});

test('layout: nodes downstream of a cycle are layered after it, in their own columns', () => {
  const r = layout(nodes(['x', 'a', 'b', 'c', 'd']),
    edges([['x', 'a'], ['a', 'b'], ['b', 'a'], ['b', 'c'], ['c', 'd']]));
  const col = id => r.pos.get(id).x;
  assert.equal(col('x'), 0);
  assert.equal(col('a'), 1);
  assert.equal(col('b'), 1, 'the cycle members share a column');
  assert.equal(col('c'), 2);
  assert.equal(col('d'), 3);
  assert.deepEqual(r.criticalIds, ['x'], 'the critical path stays on what can actually run');
});

test('splitCycles: separates the cycle from the nodes it blocks', () => {
  const g = buildGraph(nodes(['a', 'b', 'c', 'z']), edges([['a', 'b'], ['b', 'a'], ['b', 'c']]));
  const split = splitCycles(g);
  assert.deepEqual(split.order, ['z']);
  assert.deepEqual(split.cyclic.sort(), ['a', 'b']);
  assert.deepEqual(split.blocked, ['c']);
});

test('buildGraph: a self-dependency is dropped as degenerate, not crash food', () => {
  const g = buildGraph(nodes(['a', 'b']), edges([['a', 'a']]));
  assert.deepEqual(g.pred.get('a'), []);
  assert.deepEqual(g.succ.get('a'), []);
  // The layout still answers, with a ignored and b laid out.
  assert.ok(layout(nodes(['a', 'b']), edges([['a', 'a']])).pos.has('b'));
});

test('reduceCrossings: the barycentre pass removes a crossing that an order-first pass keeps', () => {
  // Layer 0: [a, b]; layer 1 fed so that rank order crosses: a->c2, b->c1.
  // Original (insertion) order of layer 1 is [c1, c2]; barycentre sorts c2
  // above c1, so the initial order crosses and the reduced one does not.
  const g = buildGraph(nodes(['a', 'b', 'c1', 'c2']), edges([['a', 'c2'], ['b', 'c1']]));
  const initial = [['a', 'b'], ['c1', 'c2']];
  assert.ok(crossings(g, initial) === 1);
  const fixed = reduceCrossings(g, initial);
  assert.equal(crossings(g, fixed), 0);
});

test('reduceCrossings: two layouts of one input are identical', () => {
  const ids = ['a', 'b', 'c', 'd', 'e'];
  const pairs = edges([['a', 'c'], ['b', 'c'], ['b', 'd'], ['c', 'e'], ['d', 'e']]);
  const r1 = layout(nodes(ids), pairs);
  const r2 = layout(nodes(ids), pairs);
  assert.deepEqual(r1.layers, r2.layers);
  assert.equal(crossings(buildGraph(nodes(ids), pairs), r1.layers), 0);
});

test('criticalPath: with all steps equal, the gating chain is the longest path', () => {
  // a -> b -> c -> e and a -> d -> e: the left chain is longer.
  const g = buildGraph(nodes(['a', 'b', 'c', 'd', 'e']),
    edges([['a', 'b'], ['b', 'c'], ['c', 'e'], ['a', 'd'], ['d', 'e']]));
  const best = criticalPath(g, splitCycles(g));
  assert.deepEqual(best.ids, ['a', 'b', 'c', 'e']);
  assert.equal(best.cost, 4);
});

test('criticalPath: weights pick the shorter hop count when heavy steps earn it', () => {
  const g = buildGraph(nodes(['a', 'b', 'c', 'd']),
    edges([['a', 'b'], ['b', 'd'], ['a', 'c'], ['c', 'd']]));
  const heavy = criticalPath(g, splitCycles(g), id => (id === 'c' ? 10 : 1));
  assert.deepEqual(heavy.ids, ['a', 'c', 'd']);
  assert.equal(heavy.cost, 12);
});

test('criticalPath: ties resolve to the first (rank-sorted) dependency, not luck', () => {
  const g = buildGraph(nodes(['a', 'b', 'c']), edges([['a', 'c'], ['b', 'c']]));
  const best = criticalPath(g, splitCycles(g));
  assert.deepEqual(best.ids, ['a', 'c']);
});

test('criticalPath: the acyclic part is still layed out when cycle nodes exist', () => {
  const r = layout(nodes(['a', 'b', 'x', 'y']), edges([['a', 'b'], ['x', 'y'], ['y', 'x']]));
  assert.deepEqual(r.criticalIds, ['a', 'b']);
});

test('layout: diamond edge set keeps every node and reports the sinks', () => {
  const r = layout(nodes(['a', 'b', 'c', 'd']), edges([['a', 'b'], ['a', 'c'], ['b', 'd'], ['c', 'd']]));
  assert.equal(r.pos.size, 4);
  assert.equal(r.height, 2);
  assert.equal(r.width, 3);
});

test('layout: an empty graph answers empty columns and no critical path', () => {
  const r = layout([], []);
  assert.deepEqual(r.layers, []);
  assert.equal(r.criticalIds.length, 0);
});

test('60 nodes lay out in well under 50 ms', () => {
  const ids = Array.from({ length: 60 }, (_, i) => `s${i}`);
  const pairs = [];
  for (let i = 0; i < 59; i++) pairs.push({ from: `s${i}`, to: `s${i + 1}` });
  // A few parallel branches fan out of the same chain.
  for (let i = 3; i < 57; i += 7) pairs.push({ from: `s${i}`, to: `s${i + 3}` });
  const start = performance.now();
  const r = layout(nodes(ids), pairs);
  const ms = performance.now() - start;
  assert.equal(r.pos.size, 60);
  assert.ok(ms < 50, `${ms.toFixed(1)} ms to lay out 60 nodes`);
});
