'use strict';

// The pack's counting, run against a stand-in for the game: events are
// fired by hand and what the script prints is read back. Every id, name and
// number is made up. Run by `go test ./internal/pack/` where there is a
// node, or by hand: `node internal/pack/testdata/counts.test.cjs`.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const source = fs.readFileSync(path.join(__dirname, '..', '..', '..', 'pack', 'scripts', 'main.js'), 'utf8')
  .replace(/^import .*$/gm, '');

function signal() {
  const subs = [];
  return { subscribe: (fn) => { subs.push(fn); return fn; }, fire: (e) => subs.forEach((fn) => fn(e)) };
}

function boot(players = []) {
  const lines = [];
  let timer = null;
  const events = { entityDie: signal(), playerBreakBlock: signal(), playerPlaceBlock: signal(), playerSpawn: signal(), playerLeave: signal() };
  const dimension = { id: '', getEntities: () => [] };
  const world = {
    afterEvents: events,
    getAllPlayers: () => players,
    getDimension: (id) => ({ ...dimension, id }),
  };
  const system = { currentTick: 100, runInterval: (fn) => { timer = fn; } };
  const console = { log: (line) => lines.push(line) };
  new Function('system', 'world', 'MOB_CAP', 'VERSION', 'console', source)(system, world, 1000, 'test', console);
  const records = () => lines.filter((l) => l.startsWith('MCMAP1 ')).map((l) => JSON.parse(l.slice(7)));
  return { events, lines, records, sample: () => { lines.length = 0; timer(); return records(); } };
}

const actor = (id, typeId = 'minecraft:player', extra = {}) => ({ id, typeId, ...extra });
const tests = [];
const test = (name, fn) => tests.push([name, fn]);

test('counts what players did and prints it once, as a delta', () => {
  const g = boot();
  g.sample();
  const zed = actor('-100');
  g.events.entityDie.fire({ deadEntity: actor('1', 'minecraft:zombie'), damageSource: { cause: 'entityAttack', damagingEntity: zed } });
  g.events.entityDie.fire({ deadEntity: actor('2', 'minecraft:zombie'), damageSource: { cause: 'entityAttack', damagingEntity: zed } });
  g.events.entityDie.fire({ deadEntity: zed, damageSource: { cause: 'fall' } });
  g.events.entityDie.fire({ deadEntity: actor('3', 'minecraft:armor_stand'), damageSource: { cause: 'entityAttack', damagingEntity: zed } });
  g.events.entityDie.fire({ deadEntity: actor('4', 'minecraft:cow'), damageSource: { cause: 'fall' } });
  g.events.playerBreakBlock.fire({ player: zed, brokenBlockPermutation: { type: { id: 'minecraft:stone' } } });
  g.events.playerBreakBlock.fire({ player: zed, brokenBlockPermutation: { type: { id: 'minecraft:stone' } } });
  g.events.playerPlaceBlock.fire({ player: zed, block: { typeId: 'minecraft:oak_planks' } });
  g.events.playerSpawn.fire({ player: zed, initialSpawn: true });
  g.events.playerSpawn.fire({ player: zed, initialSpawn: false });

  const counts = g.sample().filter((r) => r.kind === 'counts');
  assert.equal(counts.length, 1);
  assert.deepEqual(counts[0].items, [{ i: '-100', d: { fall: 1 }, k: { zombie: 2 }, b: 2, bt: { stone: 2 }, p: 1, pt: { oak_planks: 1 }, j: 1 }]);
  assert.equal(counts[0].boot > 0, true);
  assert.equal(counts[0].seq, 1);
  assert.deepEqual(g.sample().filter((r) => r.kind === 'counts'), [], 'counted again');
});

test('a leave adds the time since the join, and a sample before it takes its share once', () => {
  const g = boot();
  const realNow = Date.now;
  let rest;
  try {
    Date.now = () => 1_000_000;
    g.events.playerSpawn.fire({ player: actor('-7'), initialSpawn: true });
    Date.now = () => 1_030_500;
    const first = g.sample().find((r) => r.kind === 'counts').items[0];
    assert.equal(first.t, 30);
    assert.equal(first.j, 1);
    Date.now = () => 1_090_000;
    g.events.playerLeave.fire({ playerId: '-7' });
    Date.now = () => 1_200_000;
    rest = g.sample().find((r) => r.kind === 'counts').items[0];
  } finally {
    Date.now = realNow;
  }
  // 30 s were counted at the sample, 60 s more at the leave, and nothing
  // after it: 90 s in all, none twice.
  assert.equal(rest.t, 60);
  assert.equal(rest.l, 1);
});

test('time online is added at every sample, so a stop without a leave loses at most one sample', () => {
  const g = boot([{ id: '-8', name: 'Bo', location: { x: 0, y: 0, z: 0 }, dimension: { id: 'minecraft:overworld' }, getRotation: () => ({ y: 0 }), getComponent: () => undefined }]);
  const realNow = Date.now;
  let sum = 0;
  try {
    Date.now = () => 5_000_000;
    g.sample();
    for (let at = 5_001_000; at <= 5_007_000; at += 1000) {
      Date.now = () => at;
      const counts = g.sample().find((r) => r.kind === 'counts');
      sum += counts.items[0].t;
    }
  } finally {
    Date.now = realNow;
  }
  assert.equal(sum, 7);
});

test('sequence numbers rise by one per line and every line stays under the limit', () => {
  const g = boot();
  g.sample();
  for (let p = 0; p < 120; p++) {
    for (let m = 0; m < 20; m++) {
      g.events.playerBreakBlock.fire({ player: actor(String(-1000 - p)), brokenBlockPermutation: { type: { id: `minecraft:invented_block_${m}` } } });
    }
  }
  const out = g.sample();
  const counts = out.filter((r) => r.kind === 'counts');
  assert.ok(counts.length > 1, 'expected the sample to be split');
  counts.forEach((r, i) => assert.equal(r.seq, i + 1, 'seq'));
  for (const line of g.lines) assert.ok(Buffer.byteLength(line) + 43 < 4093, 'line too long');
  assert.equal(counts.reduce((n, r) => n + r.items.length, 0), 120);
  for (const r of counts) for (const item of r.items) {
    assert.equal(item.b, 20);
    assert.ok(Object.keys(item.bt).length <= 12);
  }
});

test('players carry health and level when they can be read', () => {
  const alex = { id: '-5', name: 'Alex', location: { x: 1, y: 2, z: 3 }, dimension: { id: 'minecraft:overworld' }, getRotation: () => ({ y: 9 }), level: 12,
    getComponent: (id) => (id === 'minecraft:health' ? { currentValue: 17.04, effectiveMax: 20 } : undefined) };
  const bare = { id: '-6', name: 'Bo', location: { x: 1, y: 2, z: 3 }, dimension: { id: 'minecraft:overworld' }, getRotation: () => ({ y: 0 }),
    getComponent: () => { throw new Error('gone'); } };
  const g = boot([alex, bare]);
  const list = g.sample().find((r) => r.kind === 'players' && r.dim === 'overworld');
  assert.deepEqual(list.items[0], { i: '-5', n: 'Alex', x: 1, y: 2, z: 3, r: 9, h: 17, hm: 20, xl: 12 });
  assert.deepEqual(list.items[1], { i: '-6', n: 'Bo', x: 1, y: 2, z: 3, r: 0 });
});

test('an event that throws is counted as an error and does not stop the rest', () => {
  const g = boot();
  g.sample();
  g.events.playerBreakBlock.fire({ player: null });
  g.events.playerPlaceBlock.fire({ player: actor('-2'), block: { typeId: 'minecraft:dirt' } });
  const out = g.sample();
  assert.equal(out.find((r) => r.kind === 'tick').errors, 1);
  assert.equal(out.find((r) => r.kind === 'counts').items[0].p, 1);
});

let failed = 0;
for (const [name, fn] of tests) {
  try { fn(); console.log(`ok ${name}`); } catch (e) { failed++; console.log(`not ok ${name}\n${e.stack}`); }
}
process.exit(failed ? 1 : 0);
