import test from "node:test";
import assert from "node:assert/strict";
import {
  STAND_Y, STATIONS, PEN_SPECIES, ARENA_MOBS, SPAWNABLE, stationAt, clampCount, spawnPoint, spawnEventFor,
  resetAreas, inBox, TELEPORTS,
} from "./logic.js";

test("stationAt finds a lectern only at its own spot and level", () => {
  assert.equal(stationAt(0, STAND_Y, -5).kind, "hub");
  assert.equal(stationAt(0.9, STAND_Y, -4.2).kind, "hub");
  assert.equal(stationAt(0, STAND_Y + 1, -5), null);
  assert.equal(stationAt(1, STAND_Y, -5), null);
  assert.equal(stationAt(PEN_SPECIES[0].cx + 3, STAND_Y, PEN_SPECIES[0].cz + 9).species, "tiger");
});

test("every station has a distinct position", () => {
  const keys = STATIONS.map((s) => `${s.x},${s.z}`);
  assert.equal(new Set(keys).size, keys.length);
});

test("clampCount stays within bounds and survives junk", () => {
  assert.equal(clampCount(0), 1);
  assert.equal(clampCount(99), 8);
  assert.equal(clampCount("3.7"), 3);
  assert.equal(clampCount(NaN), 1);
  assert.equal(clampCount(undefined), 1);
});

test("spawnPoint lands ahead of the player at their own height and fans out", () => {
  const p = spawnPoint({ x: 10, y: -60, z: 5 }, { x: 0, y: -0.5, z: 1 }, 4);
  assert.deepEqual(p, { x: 10, y: -60, z: 9 });
  const a = spawnPoint({ x: 0, y: -60, z: 0 }, { x: 0, y: 0, z: 1 }, 4, 1);
  const b = spawnPoint({ x: 0, y: -60, z: 0 }, { x: 0, y: 0, z: 1 }, 4, 2);
  assert.notEqual(a.x, b.x);
  assert.equal(a.z, 4);
  const straightUp = spawnPoint({ x: 0, y: -60, z: 0 }, { x: 0, y: 1, z: 0 }, 4);
  assert.ok(Number.isFinite(straightUp.x) && Number.isFinite(straightUp.z));
});

test("only the dragon takes a health event, and unknown tiers fall back", () => {
  assert.equal(spawnEventFor("ground_dragon", 500), "gameops:hp_500");
  assert.equal(spawnEventFor("ground_dragon", 7), "gameops:hp_300");
  assert.equal(spawnEventFor("tiger", 500), null);
});

test("reset areas cover every pen, the arena and the cage, and spawns sit inside their box", () => {
  const areas = resetAreas();
  assert.equal(areas.length, PEN_SPECIES.length + 2);
  for (const a of areas) {
    assert.ok(a.spawns.length > 0, a.id);
    for (const s of a.spawns) assert.ok(inBox(a.box, s.x, s.z), `${a.id} spawn ${s.x},${s.z}`);
    for (const s of a.spawns) assert.ok(a.types.includes(s.type), `${a.id} ${s.type}`);
  }
});

test("pens are distinct and the pen counts match the lab builder", () => {
  assert.equal(new Set(PEN_SPECIES.map((p) => `${p.cx},${p.cz}`)).size, PEN_SPECIES.length);
  assert.equal(PEN_SPECIES.find((p) => p.id === "hyena").count, 4);
  assert.deepEqual(ARENA_MOBS.map((m) => m.id), ["training_dummy_brute", "werewolf", "ground_dragon"]);
  assert.equal(SPAWNABLE.length, 9);
  assert.ok(TELEPORTS.length >= 5);
});
