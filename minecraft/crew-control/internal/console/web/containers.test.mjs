import test from 'node:test';
import assert from 'node:assert/strict';
import { containerList, cellLabel, STALE_MS } from './containers.js';

const NOW = Date.parse('2026-10-26T12:00:00Z');
const ago = s => new Date(NOW - s * 1000).toISOString();
const chest = (id, extra = {}) => ({ id, name: id, kind: 'chest', box: { min: { x: 3, y: 64, z: -403 }, max: { x: 3, y: 64, z: -403 } }, seeded: false, stock: [], ...extra });

test('only chests are listed, seeded ones first then by name', () => {
  const out = containerList([
    chest('zeta'), { ...chest('area-1'), kind: 'area' }, chest('alpha'), chest('kit', { seeded: true, name: 'Kit chest' }),
  ], NOW);
  assert.deepEqual(out.map(c => c.id), ['kit', 'alpha', 'zeta']);
});

test('cells are the items seen, biggest first, with zero counts dropped', () => {
  const [c] = containerList([chest('a', { stock: [
    { item: 'coal', count: 0, seen_at: ago(5), unit: 'crew-01' },
    { item: 'oak_log', count: 12, seen_at: ago(5), unit: 'crew-01' },
    { item: 'cobblestone', count: 90, seen_at: ago(5), unit: 'crew-01' },
  ] })], NOW);
  assert.deepEqual(c.cells.map(x => [x.item, x.count]), [['cobblestone', 90], ['oak_log', 12]]);
  assert.equal(c.state, 'fresh');
});

test('units are those that observed it, latest first, once each', () => {
  const [c] = containerList([chest('a', { stock: [
    { item: 'coal', count: 1, seen_at: ago(100), unit: 'crew-02' },
    { item: 'oak_log', count: 1, seen_at: ago(10), unit: 'crew-01' },
    { item: 'dirt', count: 1, seen_at: ago(50), unit: 'crew-02' },
  ] })], NOW);
  assert.deepEqual(c.units, ['crew-01', 'crew-02']);
  assert.equal(c.seenAt, ago(10));
});

test('stock last seen long ago is stale, never guessed fresh', () => {
  const [c] = containerList([chest('a', { stock: [{ item: 'coal', count: 3, seen_at: ago(STALE_MS / 1000 + 1), unit: 'u' }] })], NOW);
  assert.equal(c.state, 'stale');
});

test('a chest seen empty is empty, one never seen is unseen', () => {
  const [seen, none] = containerList([
    chest('a', { stock: [{ item: 'coal', count: 0, seen_at: ago(5), unit: 'u' }] }),
    chest('b', { stock: [] }),
  ], NOW);
  assert.equal(seen.state, 'empty');
  assert.equal(none.state, 'unseen');
  assert.deepEqual(none.units, []);
});

test('a missing stock list does not throw', () => {
  const [c] = containerList([{ ...chest('a'), stock: undefined }], NOW);
  assert.equal(c.state, 'unseen');
  assert.deepEqual(containerList(null, NOW), []);
});

test('position is spelled out from the box', () => {
  const [c] = containerList([chest('a')], NOW);
  assert.equal(c.pos, '3, 64, -403');
});

test('a cell speaks its item and count', () => {
  assert.equal(cellLabel({ item: 'oak_log', count: 12 }), 'oak log x12');
});
