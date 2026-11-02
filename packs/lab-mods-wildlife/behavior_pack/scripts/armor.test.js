import test from "node:test";
import assert from "node:assert/strict";
import { tierOfItem, itemOfTier, armorAction } from "./armor.js";
import { ARMOR_ITEMS } from "./armor_tiers.js";

const base = { tamed: true, ownerId: "p1", playerId: "p1", sneaking: false, heldItem: undefined, current: 0 };

test("every armor item maps to its tier and back", () => {
  assert.equal(Object.keys(ARMOR_ITEMS).length, 3);
  for (const [id, v] of Object.entries(ARMOR_ITEMS)) {
    assert.equal(tierOfItem(id).tier, v.tier);
    assert.equal(itemOfTier(v.tier), id);
  }
  assert.equal(tierOfItem("minecraft:apple"), null);
  assert.equal(itemOfTier(0), null);
});

test("the owner equips an unarmored pet", () => {
  const a = armorAction({ ...base, heldItem: "minecraft:iron_horse_armor" });
  assert.equal(a.kind, "equip");
  assert.equal(a.tier, 2);
});

test("a stranger is ignored silently; a wild animal and an armored pet refuse armor", () => {
  const item = "minecraft:diamond_horse_armor";
  assert.equal(armorAction({ ...base, heldItem: item, playerId: "p2" }).kind, "ignore");
  assert.equal(armorAction({ ...base, heldItem: item, ownerId: null }).kind, "ignore");
  assert.equal(armorAction({ ...base, heldItem: item, tamed: false }).kind, "deny");
  assert.equal(armorAction({ ...base, heldItem: item, current: 1 }).kind, "deny");
});

test("sneaking with an empty hand removes the worn tier", () => {
  assert.deepEqual(armorAction({ ...base, sneaking: true, current: 3 }), { kind: "remove", tier: 3 });
});

test("ordinary interactions are left alone", () => {
  assert.equal(armorAction({ ...base, current: 2 }).kind, "ignore");
  assert.equal(armorAction({ ...base, sneaking: true }).kind, "ignore");
  assert.equal(armorAction({ ...base, sneaking: true, current: 2, playerId: "p2" }).kind, "ignore");
  assert.equal(armorAction({ ...base, sneaking: true, current: 2, heldItem: "minecraft:beef" }).kind, "ignore");
  assert.equal(armorAction({ ...base, tamed: false, heldItem: "minecraft:beef" }).kind, "ignore");
});
