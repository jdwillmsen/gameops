import { world, system, BlockPermutation, SignSide, ItemStack } from "@minecraft/server";
import { ActionFormData, ModalFormData, FormCancelationReason } from "@minecraft/server-ui";
import {
  STAND_Y, stationAt, clampCount, spawnPoint, spawnEventFor, resetAreas, SPAWNABLE, DRAGON_TIERS, TELEPORTS,
  inLab, penCenter, padAt, signs, hasCubForm, ARMOR_ITEMS,
} from "./logic.js";
import { GUIDE_PAGES, PEN_NOTES, STATION_NOTES } from "./guide.js";

const WARRIOR_TAG = "arena_warrior";
const SPAWN_DISTANCE = 4;
// A form is refused while the player has a screen open; retry for a few seconds.
const BUSY_RETRIES = 12;
const BUSY_WAIT_TICKS = 10;

async function show(form, player) {
  for (let i = 0; i < BUSY_RETRIES; i++) {
    const res = await form.show(player);
    if (res.cancelationReason !== FormCancelationReason.UserBusy) return res;
    await new Promise((resolve) => system.runTimeout(resolve, BUSY_WAIT_TICKS));
  }
  return { canceled: true };
}

function say(player, text) {
  try {
    player.sendMessage(`§a[lab]§r ${text}`);
  } catch {
    // The player left; nothing to tell.
  }
}

async function openGuidePage(player, page) {
  const res = await show(new ActionFormData().title(page.title).body(page.body).button("Back"), player);
  return !res.canceled;
}

async function guideMenu(player) {
  const form = new ActionFormData().title("Guide").body("Pick a topic.");
  for (const p of GUIDE_PAGES) form.button(p.title);
  form.button("Back");
  const res = await show(form, player);
  if (res.canceled || res.selection >= GUIDE_PAGES.length) return;
  const back = await openGuidePage(player, GUIDE_PAGES[res.selection]);
  if (back) await guideMenu(player);
}

function spawnOne(player, id, tier, index, baby = false) {
  const dir = player.getViewDirection();
  const p = spawnPoint(player.location, dir, SPAWN_DISTANCE, index);
  const entity = player.dimension.spawnEntity(`gameops:${id}`, p);
  if (baby && hasCubForm(id)) {
    try {
      entity.triggerEvent("gameops:as_baby");
    } catch {
      say(player, "this pack version has no cub form yet");
    }
  }
  const event = spawnEventFor(id, tier);
  if (event) {
    try {
      entity.triggerEvent(event);
    } catch {
      say(player, `could not set the health tier ${event}`);
    }
  }
  return entity;
}

async function spawnMenu(player, preset) {
  const names = SPAWNABLE.map((s) => s.replace(/_/g, " "));
  const form = new ModalFormData()
    .title("Spawn")
    .dropdown("Mob", names, { defaultValueIndex: Math.max(0, SPAWNABLE.indexOf(preset ?? SPAWNABLE[0])) })
    .slider("How many", 1, 8, { valueStep: 1, defaultValue: 1 })
    .dropdown("Dragon health (dragon only)", DRAGON_TIERS.map((t) => `${t} hp`), { defaultValueIndex: 1 })
    .toggle("Cub (wildlife animals only)", { defaultValue: false });
  const res = await show(form, player);
  if (res.canceled || !res.formValues) return;
  const [mobIndex, countRaw, tierIndex, baby] = res.formValues;
  const id = SPAWNABLE[mobIndex];
  const count = clampCount(countRaw);
  for (let i = 0; i < count; i++) spawnOne(player, id, DRAGON_TIERS[tierIndex], i, baby === true);
  say(player, `spawned ${count} ${baby && hasCubForm(id) ? "cub " : ""}${id.replace(/_/g, " ")} in front of you`);
}

// A reset must not eat a fitted armor item: drop it where the animal stood.
function returnArmor(dim, entity) {
  let tier = 0;
  try {
    tier = Number(entity.getProperty("gameops:armor") ?? 0);
  } catch {
    return;
  }
  const id = Object.keys(ARMOR_ITEMS).find((k) => ARMOR_ITEMS[k] === tier);
  if (id) dim.spawnItem(new ItemStack(id, 1), entity.location);
}

function resetArea(player, area) {
  const dim = player.dimension;
  if (dim.id !== "minecraft:overworld") return { removed: 0, spawned: 0, failed: area.spawns.length };
  let removed = 0;
  let spawned = 0;
  for (const type of area.types) {
    for (const e of dim.getEntities({ type })) {
      const { x, z } = e.location;
      if (inLab(e.location) && x >= area.box.x1 && x <= area.box.x2 && z >= area.box.z1 && z <= area.box.z2) {
        returnArmor(dim, e);
        e.remove();
        removed++;
      }
    }
  }
  for (const s of area.spawns) {
    try {
      const e = dim.spawnEntity(s.type, { x: s.x, y: STAND_Y, z: s.z });
      spawned++;
      if (s.event) e.triggerEvent(s.event);
    } catch {
      // A chunk that is not loaded refuses the spawn; the rest of the area still resets.
    }
  }
  return { removed, spawned, failed: area.spawns.length - spawned };
}

async function resetMenu(player, only) {
  const areas = resetAreas().filter((a) => !only || a.id.startsWith(only));
  let chosen = areas;
  if (!only) {
    const form = new ActionFormData().title("Reset").body("Clears the mobs in an area and puts the starting ones back.");
    for (const a of areas) form.button(a.name);
    form.button("Everything");
    form.button("Back");
    const res = await show(form, player);
    if (res.canceled || res.selection > areas.length) return;
    chosen = res.selection === areas.length ? areas : [areas[res.selection]];
  }
  for (const a of chosen) {
    const r = resetArea(player, a);
    const problem = r.failed ? `, ${r.failed} could not spawn (go closer so the area is loaded)` : "";
    say(player, `${a.name}: ${r.removed} removed, ${r.spawned} placed${problem}`);
  }
}

async function teleportMenu(player) {
  const form = new ActionFormData().title("Teleport").body("Where to?");
  for (const t of TELEPORTS) form.button(t.name);
  const res = await show(form, player);
  if (res.canceled) return;
  const t = TELEPORTS[res.selection];
  player.teleport({ x: t.x + 0.5, y: STAND_Y, z: t.z + 0.5 });
}

function toggleWarriorTag(player) {
  if (player.hasTag(WARRIOR_TAG)) {
    player.removeTag(WARRIOR_TAG);
    say(player, "arena tag off: stage 0 mobs leave you alone");
  } else {
    player.addTag(WARRIOR_TAG);
    say(player, "arena tag ON: brutes, werewolves and dragons will hunt you");
  }
}

async function mainMenu(player) {
  const tagged = player.hasTag(WARRIOR_TAG);
  const form = new ActionFormData()
    .title("Proving Grounds")
    .body("Test menus. None of these need operator rights.")
    .button("Guide")
    .button("Spawn mobs")
    .button("Reset an area")
    .button("Teleport")
    .button(tagged ? "Arena tag: ON (turn off)" : "Arena tag: off (turn on)");
  const res = await show(form, player);
  if (res.canceled) return;
  switch (res.selection) {
    case 0: return guideMenu(player);
    case 1: return spawnMenu(player);
    case 2: return resetMenu(player);
    case 3: return teleportMenu(player);
    default: return toggleWarriorTag(player);
  }
}

async function stationMenu(player, station) {
  if (station.kind === "hub") return mainMenu(player);
  const title = station.kind === "pen" ? station.species.replace(/_/g, " ") : station.kind;
  const note = station.kind === "pen" ? PEN_NOTES[station.species] : STATION_NOTES[station.kind];
  const form = new ActionFormData().title(title).body(note).button("Main menu").button("Wildlife guide");
  const spawnId = station.kind === "pen" ? station.species : null;
  if (spawnId) form.button("Spawn one here");
  if (station.kind === "pen") form.button("Reset this pen");
  if (station.kind === "arena") form.button("Arena tag").button("Reset the arena");
  if (station.kind === "safety") form.button("Safety checklist").button("Reset the rival cage");
  if (station.kind === "breeding") form.button("Breeding guide").button("Reset the stalls");
  if (station.kind === "armor") form.button("Armor guide").button("Reset the tigers");
  const res = await show(form, player);
  if (res.canceled) return;
  const label = ["Main menu", "Wildlife guide", ...(spawnId ? ["Spawn one here"] : []),
    ...(station.kind === "pen" ? ["Reset this pen"] : []),
    ...(station.kind === "arena" ? ["Arena tag", "Reset the arena"] : []),
    ...(station.kind === "safety" ? ["Safety checklist", "Reset the rival cage"] : []),
    ...(station.kind === "breeding" ? ["Breeding guide", "Reset the stalls"] : []),
    ...(station.kind === "armor" ? ["Armor guide", "Reset the tigers"] : [])][res.selection];
  switch (label) {
    case "Main menu": return mainMenu(player);
    case "Wildlife guide": return openGuidePage(player, GUIDE_PAGES.find((p) => p.id === "wildlife"));
    case "Spawn one here": {
      const c = penCenter(spawnId);
      player.dimension.spawnEntity(`gameops:${spawnId}`, c);
      say(player, `added a ${spawnId.replace(/_/g, " ")} to its pen`);
      return undefined;
    }
    case "Reset this pen": return resetMenu(player, `pen_${station.species}`);
    case "Arena tag": return toggleWarriorTag(player);
    case "Reset the arena": return resetMenu(player, "arena");
    case "Safety checklist": return openGuidePage(player, GUIDE_PAGES.find((p) => p.id === "checklist"));
    case "Reset the rival cage": return resetMenu(player, "rival_cage");
    case "Breeding guide": return openGuidePage(player, GUIDE_PAGES.find((p) => p.id === "breeding"));
    case "Reset the stalls": return resetMenu(player, "stall_");
    case "Armor guide": return openGuidePage(player, GUIDE_PAGES.find((p) => p.id === "armor"));
    case "Reset the tigers": return resetMenu(player, "armor_tigers");
    default: return undefined;
  }
}

// beforeEvents handlers run without permission to open forms or change the world, so hand off a tick later.
// Using a book on a lectern fires both events; the lectern wins.
const lastLectern = new Map();

world.beforeEvents.playerInteractWithBlock.subscribe((ev) => {
  if (ev.block.typeId !== "minecraft:lectern") return;
  if (!inLab(ev.block.location)) return;
  const { x, y, z } = ev.block.location;
  const station = stationAt(x, y, z);
  if (!station) return;
  ev.cancel = true;
  const player = ev.player;
  lastLectern.set(player.id, system.currentTick);
  // Holding the button repeats the event; one menu per click.
  if (!ev.isFirstEvent) return;
  system.run(() => {
    stationMenu(player, station).catch((e) => console.warn(`gameops:tools menu failed ${e}`));
  });
});

// The vanilla book and the lab's own Guide book both open the main menu.
const BOOKS = new Set(["minecraft:book", "gameops:guide_book"]);

world.afterEvents.itemUse.subscribe((ev) => {
  if (!BOOKS.has(ev.itemStack.typeId)) return;
  if (!inLab(ev.source.location)) return;
  if (system.currentTick - (lastLectern.get(ev.source.id) ?? -100) <= 4) return;
  mainMenu(ev.source).catch((e) => console.warn(`gameops:tools menu failed ${e}`));
});

function placeSigns() {
  const dim = world.getDimension("overworld");
  let placed = 0;
  let missing = 0;
  for (const s of signs()) {
    if (!inLab({ x: s.x, y: STAND_Y, z: s.z })) continue;
    try {
      const block = dim.getBlock({ x: s.x, y: STAND_Y, z: s.z });
      if (!block) {
        missing++;
        continue;
      }
      block.setPermutation(BlockPermutation.resolve("minecraft:standing_sign", { ground_sign_direction: s.rot }));
      const sign = dim.getBlock({ x: s.x, y: STAND_Y, z: s.z }).getComponent("minecraft:sign");
      sign.setText(s.lines.join("\n"), SignSide.Front);
      sign.setWaxed(true);
      placed++;
    } catch {
      missing++;
    }
  }
  console.warn(`gameops:tools signs placed ${placed} missing ${missing}`);
}

system.afterEvents.scriptEventReceive.subscribe((ev) => {
  if (ev.id === "gameops:signs") placeSigns();
});

// Pads carry players through the sealed arena dome. A cooldown stops a player bouncing between them.
const padCooldown = new Map();
system.runInterval(() => {
  for (const player of world.getAllPlayers()) {
    const pad = padAt(player.location);
    if (!pad || player.dimension.id !== "minecraft:overworld") continue;
    if (system.currentTick < (padCooldown.get(player.id) ?? 0)) continue;
    padCooldown.set(player.id, system.currentTick + 60);
    player.teleport(pad.to);
    say(player, pad.name);
  }
}, 10);

world.afterEvents.playerLeave.subscribe((ev) => padCooldown.delete(ev.playerId));

// Builds every form shape without showing it, so a wrong argument shape for this server's UI module
// shows up in the log at startup and not when a player first right-clicks a lectern.
function selfCheck() {
  try {
    new ModalFormData().title("t")
      .dropdown("m", ["a", "b"], { defaultValueIndex: 0 })
      .slider("n", 1, 8, { valueStep: 1, defaultValue: 1 })
      .toggle("b", { defaultValue: false });
    new ActionFormData().title("t").body("b").button("x");
    console.warn("gameops:tools forms ok");
  } catch (e) {
    console.warn(`gameops:tools forms broken ${e}`);
  }
}

// Native constructors are refused during early execution, so this waits a tick.
system.run(selfCheck);
console.warn("gameops:tools script loaded");
