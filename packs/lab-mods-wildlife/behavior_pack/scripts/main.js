import { world, system, ScriptEventSource, EffectTypes } from "@minecraft/server";
import {
  sprintPhase, pruneStale, auraDue, auraRecipients, auraAmplifier, nightOnly,
  packStrengthAmplifier, packSize,
} from "./abilities.js";

const SPRINT = {
  "gameops:cheetah": { activeTicks: 160, tiredTicks: 160 },
  "gameops:tiger": { activeTicks: 60, tiredTicks: 200 },
};
// Only tamed, standing animals run their aura; recipients are chosen in abilities.js.
const AURAS = {
  "gameops:lion": {
    interval: 600, range: 16, effect: "strength", amplifier: 0, duration: 200, targets: ["owner", "pets"],
  },
  "gameops:black_leopard": {
    interval: 40, range: 0, effect: "strength", amplifier: 0, duration: 100, targets: ["self"], when: nightOnly,
  },
  "gameops:brown_bear": {
    interval: 20, range: 6, effect: "resistance", amplifier: 0, duration: 60, targets: ["owner"],
  },
  "gameops:hyena": {
    interval: 40, range: 0, effect: "strength", amplifier: (ctx) => packStrengthAmplifier(ctx.packSize),
    duration: 100, targets: ["self"], pack: { family: "gameops_hyena", range: 8 },
  },
};
// An unknown effect id only throws inside addEffect, where failures are swallowed; surface it once at load.
for (const [type, spec] of Object.entries(AURAS)) {
  if (!EffectTypes.get(spec.effect)) console.warn(`gameops:aura unknown effect ${spec.effect} for ${type}`);
}
const SITTING_PROPERTY = "gameops:sitting";
const DIMENSIONS = ["overworld", "nether", "the_end"];
const STEP = 5;
const TAME_SEARCH_RADIUS = 64;

const STATE_GRACE_TICKS = 6000;

let sprintState = new Map();
let auraState = new Map();

function setPhase(entity, phase) {
  try {
    entity.triggerEvent(`gameops:ability_${phase}`);
  } catch {
    // An entity without the event must not stop the others.
  }
}

system.runInterval(() => {
  const tick = system.currentTick;
  for (const dimId of DIMENSIONS) {
    for (const [type, cfg] of Object.entries(SPRINT)) {
      for (const entity of world.getDimension(dimId).getEntities({ type })) {
        if (!entity.isValid) continue;
        const prev = sprintState.get(entity.id);
        const next = sprintPhase(prev ?? { phase: "ready", until: 0 }, tick, cfg);
        sprintState.set(entity.id, { ...next, lastSeen: tick });
        if (!prev || next.phase !== prev.phase) setPhase(entity, next.phase);
      }
    }
  }
  sprintState = pruneStale(sprintState, tick, STATE_GRACE_TICKS);
}, STEP);

function wildlifeData(entity) {
  const tameable = entity.getComponent("minecraft:tameable");
  return {
    id: entity.id,
    pos: entity.location,
    owner: tameable?.tamedToPlayerId,
    tamed: tameable?.isTamed === true,
    sitting: entity.getProperty(SITTING_PROPERTY) === true,
    type: "wildlife",
    ref: entity,
  };
}

function nearbyWildlife(source, range, family = "gameops_wildlife") {
  return source.dimension
    .getEntities({ location: source.location, maxDistance: range, families: [family] })
    .map(wildlifeData);
}

function nearbyData(source, range) {
  const players = source.dimension.getPlayers({ location: source.location, maxDistance: range })
    .map((p) => ({ id: p.id, pos: p.location, type: "player", ref: p }));
  return [...players, ...nearbyWildlife(source, range)];
}

function applyAura(entity, spec) {
  const ctx = { timeOfDay: world.getTimeOfDay(), tick: system.currentTick };
  const data = wildlifeData(entity);
  if (!data.tamed || data.sitting) return;
  const prev = auraState.get(entity.id) ?? { nextAt: 0 };
  const { due, next } = auraDue(prev, ctx.tick, spec, ctx);
  auraState.set(entity.id, { ...next, lastSeen: ctx.tick });
  if (!due) return;
  if (spec.pack) ctx.packSize = packSize(data, nearbyWildlife(entity, spec.pack.range, spec.pack.family), spec.pack.range);
  const amplifier = auraAmplifier(spec, ctx);
  if (amplifier == null) return;
  const candidates = spec.range > 0 ? nearbyData(entity, spec.range) : [];
  const options = { amplifier, showParticles: false };
  for (const r of auraRecipients(data, candidates, spec)) {
    try {
      r.ref.addEffect(spec.effect, spec.duration, options);
    } catch {
      // A recipient that unloaded mid-scan must not stop the others.
    }
  }
}

system.runInterval(() => {
  for (const dimId of DIMENSIONS) {
    for (const [type, spec] of Object.entries(AURAS)) {
      for (const entity of world.getDimension(dimId).getEntities({ type })) {
        try {
          if (entity.isValid) applyAura(entity, spec);
        } catch {
          // An entity invalidated mid-scan must not stop the others.
        }
      }
    }
  }
  auraState = pruneStale(auraState, system.currentTick, STATE_GRACE_TICKS);
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
