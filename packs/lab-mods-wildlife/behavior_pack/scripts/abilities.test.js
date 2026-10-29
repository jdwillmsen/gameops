import test from "node:test";
import assert from "node:assert/strict";
import {
  sprintPhase, packDamageMultiplier, isNight, inRoarRange, pruneStale,
  auraDue, auraRecipients, auraAmplifier, nightOnly, packStrengthAmplifier, packSize,
} from "./abilities.js";

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

const ROAR = { interval: 600, range: 16, targets: ["owner", "pets"], amplifier: 0 };
const AMBUSH = { interval: 40, range: 0, targets: ["self"], amplifier: 0, when: nightOnly };

test("auraDue fires once per interval and schedules the next", () => {
  let r = auraDue({ nextAt: 0 }, 100, ROAR);
  assert.equal(r.due, true);
  assert.deepEqual(r.next, { nextAt: 700 });
  r = auraDue(r.next, 699, ROAR);
  assert.equal(r.due, false);
  assert.deepEqual(r.next, { nextAt: 700 });
  assert.equal(auraDue(r.next, 700, ROAR).due, true);
});

test("auraDue holds the interval while the when-condition is false", () => {
  const day = { timeOfDay: 6000 };
  const night = { timeOfDay: 18000 };
  let r = auraDue({ nextAt: 0 }, 10, AMBUSH, day);
  assert.equal(r.due, false);
  assert.deepEqual(r.next, { nextAt: 0 });
  r = auraDue(r.next, 15, AMBUSH, night);
  assert.equal(r.due, true);
  assert.deepEqual(r.next, { nextAt: 55 });
});

test("nightOnly follows isNight", () => {
  assert.equal(nightOnly({ timeOfDay: 6000 }), false);
  assert.equal(nightOnly({ timeOfDay: 18000 }), true);
});

test("auraAmplifier accepts a number or a function of the context", () => {
  assert.equal(auraAmplifier({ amplifier: 2 }, {}), 2);
  assert.equal(auraAmplifier({ amplifier: (c) => c.pets }, { pets: 3 }), 3);
  assert.equal(auraAmplifier({}, {}), 0);
});

const lion = { id: "l1", pos: { x: 0, y: 64, z: 0 }, owner: "p1", tamed: true, sitting: false, type: "wildlife" };
const owner = { id: "p1", pos: { x: 10, y: 64, z: 0 }, type: "player" };
const stranger = { id: "p2", pos: { x: 3, y: 64, z: 0 }, type: "player" };
const pet = { id: "c1", pos: { x: 0, y: 64, z: 16 }, owner: "p1", tamed: true, sitting: true, type: "wildlife" };
const farPet = { id: "c2", pos: { x: 0, y: 64, z: 17 }, owner: "p1", tamed: true, sitting: false, type: "wildlife" };
const wild = { id: "w1", pos: { x: 1, y: 64, z: 0 }, owner: undefined, tamed: false, sitting: false, type: "wildlife" };
const ids = (list) => list.map((e) => e.id).sort();

test("roar reaches the owner and tamed wildlife in range, never strangers or wild animals", () => {
  const got = auraRecipients(lion, [owner, stranger, pet, farPet, wild, lion], ROAR);
  assert.deepEqual(ids(got), ["c1", "p1"]);
});

test("roar skips an owner out of range", () => {
  const far = { ...owner, pos: { x: 16.1, y: 64, z: 0 } };
  assert.deepEqual(auraRecipients(lion, [far], ROAR), []);
});

test("a wild or sitting source has no recipients", () => {
  assert.deepEqual(auraRecipients({ ...lion, tamed: false }, [owner], ROAR), []);
  assert.deepEqual(auraRecipients({ ...lion, sitting: true }, [owner, pet], ROAR), []);
  assert.deepEqual(auraRecipients({ ...lion, sitting: true }, [], AMBUSH), []);
});

test("self target returns the source once, whatever the range", () => {
  const got = auraRecipients(lion, [owner, lion], AMBUSH);
  assert.deepEqual(ids(got), ["l1"]);
  const both = auraRecipients(lion, [lion], { ...ROAR, targets: ["owner", "pets", "self"] });
  assert.deepEqual(ids(both), ["l1"]);
});

test("packStrengthAmplifier maps pack size to no effect, Strength I or Strength II", () => {
  assert.equal(packStrengthAmplifier(0), null);
  assert.equal(packStrengthAmplifier(1), null);
  assert.equal(packStrengthAmplifier(2), 0);
  assert.equal(packStrengthAmplifier(3), 0);
  assert.equal(packStrengthAmplifier(4), 1);
  assert.equal(packStrengthAmplifier(9), 1);
});

test("an amplifier function may return null to skip the effect", () => {
  assert.equal(auraAmplifier({ amplifier: (c) => packStrengthAmplifier(c.packSize) }, { packSize: 1 }), null);
  assert.equal(auraAmplifier({ amplifier: (c) => packStrengthAmplifier(c.packSize) }, { packSize: 4 }), 1);
});

const GUARD = { interval: 20, range: 6, targets: ["owner"], amplifier: 0 };
const bear = { id: "b1", pos: { x: 0, y: 64, z: 0 }, owner: "p1", tamed: true, sitting: false, type: "wildlife" };

test("bear guard reaches only its owner within 6 blocks", () => {
  const near = { ...owner, pos: { x: 6, y: 64, z: 0 } };
  const far = { ...owner, pos: { x: 6.1, y: 64, z: 0 } };
  const friend = { ...pet, sitting: false, pos: { x: 1, y: 64, z: 0 } };
  assert.deepEqual(ids(auraRecipients(bear, [near, stranger, friend, wild], GUARD)), ["p1"]);
  assert.deepEqual(auraRecipients(bear, [far], GUARD), []);
});

test("packSize counts the source and tamed hyenas within range, not wild, far or itself twice", () => {
  const h = (id, x, tamed = true) => ({ id, pos: { x, y: 64, z: 0 }, tamed, sitting: false, type: "wildlife" });
  const self = h("h1", 0);
  const all = [self, h("h2", 8), h("h3", 8.1), h("h4", 2, false), h("h5", -3)];
  assert.equal(packSize(self, all, 8), 3);
  assert.equal(packSize(self, [self], 8), 1);
});

test("packSize ignores sitting hyenas, which do not fight", () => {
  const h = (id, x, sitting) => ({ id, pos: { x, y: 64, z: 0 }, tamed: true, sitting, type: "wildlife" });
  const standing = h("h1", 0, false);
  assert.equal(packSize(standing, [standing, h("h2", 1, true), h("h3", 2, true)], 8), 1);
  assert.equal(packSize(standing, [standing, h("h2", 1, true), h("h3", 2, false)], 8), 2);
});
