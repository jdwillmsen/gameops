import { ARMOR_ITEMS } from "./armor_tiers.js";

export function tierOfItem(typeId) {
  return ARMOR_ITEMS[typeId] ?? null;
}

export function itemOfTier(tier) {
  const entry = Object.entries(ARMOR_ITEMS).find(([, v]) => v.tier === tier);
  return entry ? entry[0] : null;
}

// What an interaction with a wildlife animal means for its armor. Anything other than "ignore" is also
// cancelled by the caller so the same click does not make the animal sit or stand.
//   equip:  an armor item on an unarmored pet of the player
//   remove: sneaking with an empty hand on an armored pet of the player
//   deny:   the owner's armor item the pet cannot take, or one used on a wild animal; reason is shown
export function armorAction({ tamed, ownerId, playerId, sneaking, heldItem, current }) {
  const item = heldItem ? tierOfItem(heldItem) : null;
  if (item && !tamed) return { kind: "deny", reason: "Tame it first." };
  if (!tamed) return { kind: "ignore" };
  if (item) {
    // Someone else's pet (or one with no recorded owner) is left to the game, silently.
    if (!ownerId || ownerId !== playerId) return { kind: "ignore" };
    if (current > 0) return { kind: "deny", reason: "Already armored. Sneak with an empty hand to take it off." };
    return { kind: "equip", item: heldItem, tier: item.tier, name: item.name, sound: item.sound };
  }
  if (!heldItem && sneaking && current > 0 && ownerId === playerId) {
    return { kind: "remove", tier: current };
  }
  return { kind: "ignore" };
}
