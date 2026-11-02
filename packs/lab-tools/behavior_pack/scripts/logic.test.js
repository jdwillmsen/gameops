import test from "node:test";
import assert from "node:assert/strict";
import {
  STAND_Y, STATIONS, PEN_SPECIES, ARENA_MOBS, SPAWNABLE, stationAt, clampCount, spawnPoint, spawnEventFor,
  resetAreas, inBox, TELEPORTS, inLab, penCenter, LAB_BOUNDS, SPECIES, PADS, padAt, signs, SIGN_LINE_MAX,
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

test("inLab accepts the plaza and the far corners and rejects other places", () => {
  assert.ok(inLab({ x: 0, y: -60, z: 0 }));
  assert.ok(inLab({ x: 82, y: -60, z: 25 }));
  assert.ok(!inLab({ x: 5000, y: -60, z: 0 }));
  assert.ok(!inLab({ x: 0, y: 70, z: 0 }));
  assert.ok(!inLab({ x: 0, y: -60, z: -200 }));
});

test("every area and station lies inside the lab bounds", () => {
  for (const s of STATIONS) assert.ok(inLab({ x: s.x, y: STAND_Y, z: s.z }), `${s.kind} ${s.x},${s.z}`);
  for (const t of TELEPORTS) assert.ok(inLab({ x: t.x, y: STAND_Y, z: t.z }), t.name);
  for (const a of resetAreas()) {
    assert.ok(inLab({ x: a.box.x1, y: STAND_Y, z: a.box.z1 }) && inLab({ x: a.box.x2, y: STAND_Y, z: a.box.z2 }), a.id);
  }
  assert.ok(LAB_BOUNDS.x2 > LAB_BOUNDS.x1);
});

test("penCenter returns the pen's middle or null", () => {
  assert.deepEqual(penCenter("tiger"), { x: -23.5, y: STAND_Y, z: -41.5 });
  assert.equal(penCenter("werewolf"), null);
});

test("pads send a player to a spot that is not on a pad", () => {
  assert.equal(padAt({ x: 25.2, y: STAND_Y, z: 0.4 }).name, "into the arena");
  assert.equal(padAt({ x: 34, y: STAND_Y, z: -0.6 }).name, "out of the arena");
  assert.equal(padAt({ x: 25, y: 10, z: 0 }), null);
  for (const p of PADS) {
    assert.equal(padAt(p.to), null, p.name);
    assert.ok(inLab(p.to), p.name);
  }
});

test("every sign fits a sign face and stands inside the lab", () => {
  const all = signs();
  assert.ok(all.length >= 19);
  const spots = all.map((s) => `${s.x},${s.z}`);
  assert.equal(new Set(spots).size, spots.length, "two signs on one spot");
  for (const s of all) {
    assert.equal(s.lines.length, 4);
    for (const l of s.lines) assert.ok(l.length <= SIGN_LINE_MAX, `${s.x},${s.z} "${l}"`);
    assert.ok([0, 4, 8, 12].includes(s.rot));
    assert.ok(inLab({ x: s.x, y: STAND_Y, z: s.z }));
  }
});

test("species facts exist for every pen and name its taming food", () => {
  for (const p of PEN_SPECIES) {
    assert.ok(SPECIES[p.id], p.id);
    assert.ok(SPECIES[p.id].food.endsWith(p.food.replace("_", " ")), p.id);
  }
});
