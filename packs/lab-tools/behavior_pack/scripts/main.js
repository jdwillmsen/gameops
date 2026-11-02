import { world, system } from "@minecraft/server";
import { ActionFormData, ModalFormData, FormCancelationReason } from "@minecraft/server-ui";
import {
  STAND_Y, stationAt, clampCount, spawnPoint, spawnEventFor, resetAreas, SPAWNABLE, DRAGON_TIERS, TELEPORTS,
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
  await openGuidePage(player, GUIDE_PAGES[res.selection]);
  await guideMenu(player);
}

function spawnOne(player, id, tier, index) {
  const dir = player.getViewDirection();
  const p = spawnPoint(player.location, dir, SPAWN_DISTANCE, index);
  const entity = player.dimension.spawnEntity(`gameops:${id}`, p);
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
    .dropdown("Dragon health (dragon only)", DRAGON_TIERS.map((t) => `${t} hp`), { defaultValueIndex: 1 });
  const res = await show(form, player);
  if (res.canceled || !res.formValues) return;
  const [mobIndex, countRaw, tierIndex] = res.formValues;
  const id = SPAWNABLE[mobIndex];
  const count = clampCount(countRaw);
  for (let i = 0; i < count; i++) spawnOne(player, id, DRAGON_TIERS[tierIndex], i);
  say(player, `spawned ${count} ${id.replace(/_/g, " ")} in front of you`);
}

function resetArea(player, area) {
  const dim = player.dimension;
  let removed = 0;
  for (const type of area.types) {
    for (const e of dim.getEntities({ type })) {
      const { x, z } = e.location;
      if (x >= area.box.x1 && x <= area.box.x2 && z >= area.box.z1 && z <= area.box.z2) {
        e.remove();
        removed++;
      }
    }
  }
  for (const s of area.spawns) {
    const e = dim.spawnEntity(s.type, { x: s.x, y: STAND_Y, z: s.z });
    if (s.event) {
      try {
        e.triggerEvent(s.event);
      } catch {
        // The default health stays; the area still has its mob.
      }
    }
  }
  return removed;
}

async function resetMenu(player, only) {
  const areas = resetAreas().filter((a) => !only || a.id === only);
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
  let removed = 0;
  for (const a of chosen) removed += resetArea(player, a);
  say(player, `reset ${chosen.map((a) => a.name).join(", ")} (${removed} removed)`);
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
  const res = await show(form, player);
  if (res.canceled) return;
  const label = ["Main menu", "Wildlife guide", ...(spawnId ? ["Spawn one here"] : []),
    ...(station.kind === "pen" ? ["Reset this pen"] : []),
    ...(station.kind === "arena" ? ["Arena tag", "Reset the arena"] : []),
    ...(station.kind === "safety" ? ["Safety checklist", "Reset the rival cage"] : [])][res.selection];
  switch (label) {
    case "Main menu": return mainMenu(player);
    case "Wildlife guide": return openGuidePage(player, GUIDE_PAGES.find((p) => p.id === "wildlife"));
    case "Spawn one here": return spawnOne(player, spawnId, 300, 0);
    case "Reset this pen": return resetMenu(player, `pen_${station.species}`);
    case "Arena tag": return toggleWarriorTag(player);
    case "Reset the arena": return resetMenu(player, "arena");
    case "Safety checklist": return openGuidePage(player, GUIDE_PAGES.find((p) => p.id === "checklist"));
    case "Reset the rival cage": return resetMenu(player, "rival_cage");
    default: return undefined;
  }
}

// beforeEvents handlers run without permission to open forms or change the world, so hand off a tick later.
world.beforeEvents.playerInteractWithBlock.subscribe((ev) => {
  if (ev.block.typeId !== "minecraft:lectern") return;
  const { x, y, z } = ev.block.location;
  const station = stationAt(x, y, z);
  if (!station) return;
  ev.cancel = true;
  const player = ev.player;
  system.run(() => {
    stationMenu(player, station).catch((e) => console.warn(`gameops:tools menu failed ${e}`));
  });
});

world.afterEvents.itemUse.subscribe((ev) => {
  if (ev.itemStack.typeId !== "minecraft:book") return;
  mainMenu(ev.source).catch((e) => console.warn(`gameops:tools menu failed ${e}`));
});

// Builds every form shape without showing it, so a wrong argument shape for this server's UI module
// shows up in the log at startup and not when a player first right-clicks a lectern.
function selfCheck() {
  try {
    new ModalFormData().title("t")
      .dropdown("m", ["a", "b"], { defaultValueIndex: 0 })
      .slider("n", 1, 8, { valueStep: 1, defaultValue: 1 });
    new ActionFormData().title("t").body("b").button("x");
    console.warn("gameops:tools forms ok");
  } catch (e) {
    console.warn(`gameops:tools forms broken ${e}`);
  }
}

// Native constructors are refused during early execution, so this waits a tick.
system.run(selfCheck);
console.warn("gameops:tools script loaded");
