import test from "node:test";
import assert from "node:assert/strict";
import { sprintPhase, packDamageMultiplier, isNight, inRoarRange, pruneStale } from "./abilities.js";

test("sprintPhase goes ready to active to tired to ready", () => {
  const cfg = { activeTicks: 160, tiredTicks: 160 };
  let s = sprintPhase({ phase: "ready", until: 0 }, 0, cfg);
  assert.deepEqual(s, { phase: "active", until: 160 });
  assert.deepEqual(sprintPhase(s, 100, cfg), s);
  s = sprintPhase(s, 160, cfg);
  assert.deepEqual(s, { phase: "tired", until: 320 });
  assert.deepEqual(sprintPhase(s, 319, cfg), s);
  s = sprintPhase(s, 320, cfg);
  assert.equal(s.phase, "ready");
});

test("packDamageMultiplier is 1 for one pet and caps at max", () => {
  const cfg = { perPet: 0.25, max: 2 };
  assert.equal(packDamageMultiplier(0, cfg), 1);
  assert.equal(packDamageMultiplier(1, cfg), 1);
  assert.equal(packDamageMultiplier(3, cfg), 1.5);
  assert.equal(packDamageMultiplier(50, cfg), 2);
});

test("isNight is false at 6000 and true at 18000", () => {
  assert.equal(isNight(6000), false);
  assert.equal(isNight(18000), true);
  assert.equal(isNight(13000), true);
  assert.equal(isNight(23000), true);
  assert.equal(isNight(23001), false);
});

test("inRoarRange is inclusive at the boundary", () => {
  const o = { x: 0, y: 0, z: 0 };
  assert.equal(inRoarRange(o, { x: 3, y: 4, z: 0 }, 5), true);
  assert.equal(inRoarRange(o, { x: 3, y: 4, z: 1 }, 5), false);
  assert.equal(inRoarRange(o, { x: 0, y: 0, z: 2 }, 5), true);
});

test("pruneStale keeps states seen within the grace period and drops older ones", () => {
  const states = new Map([
    ["fresh", { phase: "active", until: 10, lastSeen: 5000 }],
    ["edge", { phase: "tired", until: 10, lastSeen: 4000 }],
    ["old", { phase: "ready", until: 0, lastSeen: 3999 }],
  ]);
  const kept = pruneStale(states, 10000, 6000);
  assert.deepEqual([...kept.keys()], ["fresh", "edge"]);
  assert.equal(states.size, 3);
});
