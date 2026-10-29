import { world, system, ScriptEventSource } from "@minecraft/server";
import { sprintPhase } from "./abilities.js";

const SPRINT = {
  "gameops:cheetah": { activeTicks: 160, tiredTicks: 160 },
  "gameops:tiger": { activeTicks: 60, tiredTicks: 200 },
};
const DIMENSIONS = ["overworld", "nether", "the_end"];
const STEP = 5;
const TAME_SEARCH_RADIUS = 64;

const sprintState = new Map();

function setPhase(entity, phase) {
  try {
    entity.triggerEvent(`gameops:ability_${phase}`);
  } catch {
    // An entity without the event must not stop the others.
  }
}

system.runInterval(() => {
  const tick = system.currentTick;
  const seen = new Set();
  for (const dimId of DIMENSIONS) {
    for (const [type, cfg] of Object.entries(SPRINT)) {
      for (const entity of world.getDimension(dimId).getEntities({ type })) {
        if (!entity.isValid) continue;
        seen.add(entity.id);
        const prev = sprintState.get(entity.id);
        const next = sprintPhase(prev ?? { phase: "ready", until: 0 }, tick, cfg);
        sprintState.set(entity.id, next);
        if (!prev || next.phase !== prev.phase) setPhase(entity, next.phase);
      }
    }
  }
  for (const key of sprintState.keys()) {
    if (!seen.has(key)) sprintState.delete(key);
  }
}, STEP);

function tameNearest(player) {
  const candidates = player.dimension.getEntities({
    location: player.location,
    maxDistance: TAME_SEARCH_RADIUS,
    closest: 20,
  });
  for (const entity of candidates) {
    if (!entity.typeId.startsWith("gameops:")) continue;
    const tameable = entity.getComponent("minecraft:tameable");
    if (!tameable) continue;
    return tameable.tame(player);
  }
  return false;
}

system.afterEvents.scriptEventReceive.subscribe((event) => {
  if (event.id === "gameops:tame") {
    if (event.sourceType !== ScriptEventSource.Entity || event.sourceEntity?.typeId !== "minecraft:player") return;
    event.sourceEntity.sendMessage(tameNearest(event.sourceEntity) ? "Tamed." : "No wildlife nearby to tame.");
  } else if (event.id === "gameops:perf") {
    const start = system.currentTick;
    const t0 = Date.now();
    system.runTimeout(() => {
      const ticks = system.currentTick - start;
      console.warn(`gameops:perf mean_tick_ms=${((Date.now() - t0) / ticks).toFixed(2)} ticks=${ticks}`);
    }, 200);
  }
}, { namespaces: ["gameops"] });
