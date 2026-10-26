import test from 'node:test';
import assert from 'node:assert/strict';
import { ledgerModel, mapPlace, staleState, staleChip, ago, FRESH_MS, STALE_MS } from './ledger.js';
import { layout } from './layout.js';

const NOW = Date.parse('2026-10-26T12:00:00Z');
const iso = msAgo => new Date(NOW - msAgo).toISOString();
const box = (x, y, z) => ({ min: { x, y, z }, max: { x, y, z } });

const places = () => [
  { id: 'quarry-1', name: 'Quarry', kind: 'area', seeded: true, box: { min: { x: 10, y: 60, z: 10 }, max: { x: 30, y: 70, z: 30 } }, items: ['cobblestone'], stock: [] },
  { id: 'chest-1-2-3', name: 'Stone chest', kind: 'chest', box: box(100, 64, 20), stock: [
    { item: 'cobblestone', count: 64, seen_at: iso(30_000), unit: 'agate' },
    { item: 'coal', count: 8, seen_at: iso(20 * 60_000), unit: 'cobble' },
  ] },
  { id: 'grove-1', name: 'Grove', kind: 'area', box: { min: { x: -40, y: 64, z: 60 }, max: { x: -20, y: 70, z: 80 } }, items: ['oak_log'], stock: [] },
];

test('staleness: bands and words, never colour alone', () => {
  assert.equal(staleState(iso(FRESH_MS), NOW), 'fresh');
  assert.equal(staleState(iso(FRESH_MS + 1), NOW), 'aging');
  assert.equal(staleState(iso(STALE_MS + 1), NOW), 'stale');
  assert.equal(staleState('', NOW), 'unseen');
  assert.equal(staleChip('', NOW).word, 'never seen');
  assert.equal(staleChip(iso(20 * 60_000), NOW).word, 'stale 20m');
  assert.equal(ago(90_000), '1m');
  assert.equal(ago(3 * 3600_000), '3h');
  assert.equal(ago(-1), '');
});

test('model: nodes, edge kinds and what each says', () => {
  const m = ledgerModel(places(), NOW);
  const ids = m.nodes.map(n => n.id).sort();
  assert.deepEqual(ids, ['p:chest-1-2-3', 'p:grove-1', 'p:quarry-1', 'r:coal', 'r:cobblestone', 'r:oak_log', 'u:agate', 'u:cobble']);
  const has = (from, to, kind) => m.edges.some(e => e.from === from && e.to === to && e.kind === kind);
  assert.ok(has('r:cobblestone', 'p:chest-1-2-3', 'stored'));
  assert.ok(has('p:quarry-1', 'r:cobblestone', 'supplies'));
  assert.ok(has('p:chest-1-2-3', 'u:agate', 'used'));
  assert.ok(m.edges.find(e => e.kind === 'used').title.includes('used by'));
  assert.ok(m.edges.find(e => e.kind === 'supplies').title.includes('supplied by'));
  const chest = m.nodes.find(n => n.id === 'p:chest-1-2-3');
  assert.equal(chest.chip.state, 'fresh', 'a place is as fresh as its newest reading');
  assert.match(chest.sub, /72 items/);
  assert.equal(m.nodes.find(n => n.id === 'r:coal').chip.state, 'stale');
  assert.equal(m.nodes.find(n => n.id === 'p:quarry-1').chip.word, 'never seen');
  assert.equal(m.facts.get('p:chest-1-2-3').stock.length, 2);
});

test('model: an area holding stock of its own item makes no two-way edge', () => {
  const p = places();
  p[0].stock = [{ item: 'cobblestone', count: 1, seen_at: iso(1000), unit: 'agate' }];
  const m = ledgerModel(p, NOW);
  assert.equal(m.edges.filter(e => e.from === 'p:quarry-1' && e.to === 'r:cobblestone').length, 0);
  assert.equal(layout(m.nodes.map(n => ({ id: n.id })), m.edges).cycle.length, 0, 'the ledger graph is acyclic');
});

test('model: an empty ledger is an empty graph', () => {
  const m = ledgerModel([], NOW);
  assert.deepEqual([m.nodes, m.edges], [[], []]);
});

test('map layout: places follow world coordinates, nothing overlaps, others stand to the right', () => {
  const ps = places();
  const m = ledgerModel(ps, NOW);
  const pos = mapPlace(m, ps);
  assert.equal(pos.size, m.nodes.length);
  const g = pos.get('p:grove-1'), q = pos.get('p:quarry-1'), c = pos.get('p:chest-1-2-3');
  assert.ok(g.x < q.x && q.x < c.x, 'west to east');
  assert.ok(g.y > q.y, 'south is down');
  const placeX = Math.max(...['p:grove-1', 'p:quarry-1', 'p:chest-1-2-3'].map(id => pos.get(id).x));
  for (const n of m.nodes.filter(n => n.kind !== 'place')) assert.ok(pos.get(n.id).x > placeX + 0.99, `${n.id} is right of the map`);
  const all = [...pos.values()];
  for (let i = 0; i < all.length; i++) for (let j = i + 1; j < all.length; j++) {
    const a = all[i], b = all[j];
    assert.ok(Math.abs(a.x - b.x) >= 0.75 || Math.abs(a.y - b.y) >= 0.77, 'no two nodes overlap');
  }
});

test('map layout: chests on the same spot do not stack', () => {
  const ps = [
    { id: 'aa', name: 'A', kind: 'chest', box: box(0, 64, 0), stock: [] },
    { id: 'bb', name: 'B', kind: 'chest', box: box(0, 65, 0), stock: [] },
  ];
  const pos = mapPlace(ledgerModel(ps, NOW), ps);
  assert.notDeepEqual(pos.get('p:aa'), pos.get('p:bb'));
});
