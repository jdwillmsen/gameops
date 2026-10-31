import { world, system, ScriptEventSource, EffectTypes } from "@minecraft/server";
import {
  sprintPhase, pruneStale, auraDue, auraRecipients, auraAmplifier, nightOnly,
  packStrengthAmplifier, packSize, formatCensus, formatEffects, CENSUS_SPECIES,
  formatEvent, levelAllows, eventDue,
} from "./abilities.js";

// "off" | "info" | "debug". info logs one line per taming, sit, stand, burst, ability and hit, and an
// aura at most every AURA_LOG_TICKS per animal; debug logs every aura application. Edit and release to change it.
const LOG_LEVEL = "info";
const AURA_LOG_TICKS = 600;
const HIT_LOG_TICKS = 10;
const MAX_LOG_KEYS = 2000;

let auraLogState = new Map();
let hitLogState = new Map();

function logEvent(level, ev, fields) {
  if (!levelAllows(LOG_LEVEL, level)) return;
  try {
    console.warn(formatEvent(ev, fields, system.currentTick));
  } catch {
    // Logging must never stop the behaviour it describes.
  }
}

function ownerOf(entity) {
  try {
    return entity.getDynamicProperty("gameops:owner") ?? null;
  } catch {
    return null;
  }
}

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
// Deferred because BDS rejects native calls such as EffectTypes.get during early execution and aborts the whole script.
system.run(() => {
  for (const [type, spec] of Object.entries(AURAS)) {
    if (!EffectTypes.get(spec.effect)) console.warn(`gameops:aura unknown effect ${spec.effect} for ${type}`);
  }
});
// Lets a headless smoke test tell "script never loaded" from "script loaded, nothing to do".
console.warn("gameops:wildlife script loaded");
const SITTING_PROPERTY = "gameops:sitting";
// The owner is saved on the entity when taming happens because the tameable component is removed with the wild group.
const OWNER_PROPERTY = "gameops:owner";
const DIMENSIONS = ["overworld", "nether", "the_end"];
const STEP = 5;
const TAME_SEARCH_RADIUS = 64;

const STATE_GRACE_TICKS = 6000;

let sprintState = new Map();
// Ids of burst animals that landed a hit since the last scan; collected here so the scan stays the only writer of sprintState.
let hitters = new Set();

world.afterEvents.entityHitEntity.subscribe((event) => {
  const attacker = event.damagingEntity;
  if (attacker.isValid && SPRINT[attacker.typeId]) hitters.add(attacker.id);
  logHit(attacker, event.hitEntity, "attacker");
  logHit(event.hitEntity, attacker, "victim");
});

function logHit(animal, other, role) {
  try {
    if (!animal.isValid || !animal.typeId.startsWith("gameops:") || !animal.hasComponent("minecraft:health")) return;
    if (!levelAllows(LOG_LEVEL, "info")) return;
    if (hitLogState.size > MAX_LOG_KEYS) hitLogState = new Map();
    if (!eventDue(hitLogState, `${animal.id}:${role}:${other.id}`, system.currentTick, HIT_LOG_TICKS).due) return;
    logEvent("info", "hit", {
      type: animal.typeId, id: animal.id, owner: ownerOf(animal), role,
      other_type: other.typeId, other_id: other.id,
    });
  } catch {
    // An entity that unloaded between the hit and this callback has nothing to log.
  }
}
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
  const triggers = hitters;
  hitters = new Set();
  for (const dimId of DIMENSIONS) {
    for (const [type, cfg] of Object.entries(SPRINT)) {
      for (const entity of world.getDimension(dimId).getEntities({ type })) {
        if (!entity.isValid) continue;
        const prev = sprintState.get(entity.id);
        const next = sprintPhase(prev ?? { phase: "ready", until: 0 }, tick, cfg, triggers.has(entity.id));
        sprintState.set(entity.id, { ...next, lastSeen: tick });
        if (!prev || next.phase !== prev.phase) {
          setPhase(entity, next.phase);
          logPhase(entity, prev?.phase, next.phase);
        }
      }
    }
  }
  sprintState = pruneStale(sprintState, tick, STATE_GRACE_TICKS);
}, STEP);

function logPhase(entity, from, to) {
  const base = { type: entity.typeId, id: entity.id, owner: ownerOf(entity) };
  const ability = entity.typeId === "gameops:tiger" ? "charge" : "sprint";
  if (to === "active") logEvent("info", "burst", { ...base, ability, phase: "start" });
  else if (to === "tired") logEvent("info", "burst", { ...base, ability, phase: "end" });
  else if (from) logEvent("info", "ability", { ...base, ability, phase: "ready" });
}

function isTamed(entity) {
  return entity.hasComponent("minecraft:is_tamed");
}

// minecraft:tameable only exists in the wild group, so a tamed animal answers undefined to it; the
// is_tamed marker is in the tamed group, which every pet tamed before the owner was stored also has.
// Such an older pet has no stored owner, so only its self and pet auras work until it is tamed again.
function wildlifeData(entity) {
  return {
    id: entity.id,
    pos: entity.location,
    owner: entity.getDynamicProperty(OWNER_PROPERTY),
    tamed: isTamed(entity),
    sitting: entity.getProperty(SITTING_PROPERTY) === true,
    type: "wildlife",
    ref: entity,
  };
}

// The tameable component still answers during the tame event, before the wild group is removed.
world.afterEvents.dataDrivenEntityTrigger.subscribe((event) => {
  try {
    const ownerId = event.entity.getComponent("minecraft:tameable")?.tamedToPlayerId;
    if (ownerId) event.entity.setDynamicProperty(OWNER_PROPERTY, ownerId);
    logEvent("info", "tamed", { type: event.entity.typeId, id: event.entity.id, owner: ownerId ?? null });
  } catch {
    // An entity that unloaded between the event and this callback has nothing to record.
  }
}, { eventTypes: ["gameops:on_tamed"] });

for (const [eventId, ev] of [["gameops:on_sit", "sit"], ["gameops:on_stand", "stand"]]) {
  world.afterEvents.dataDrivenEntityTrigger.subscribe((event) => {
    try {
      logEvent("info", ev, { type: event.entity.typeId, id: event.entity.id, owner: ownerOf(event.entity) });
    } catch {
      // An entity that unloaded between the event and this callback has nothing to log.
    }
  }, { eventTypes: [eventId] });
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

function logAura(entity, data, spec, amplifier, recipients) {
  if (!levelAllows(LOG_LEVEL, "info")) return;
  if (!levelAllows(LOG_LEVEL, "debug")) {
    if (auraLogState.size > MAX_LOG_KEYS) auraLogState = new Map();
    if (!eventDue(auraLogState, entity.id, system.currentTick, AURA_LOG_TICKS).due) return;
  }
  logEvent("info", "aura", {
    type: entity.typeId, id: entity.id, owner: data.owner ?? null, effect: spec.effect, amplifier,
    duration: spec.duration, kinds: spec.targets, targets: recipients.map((r) => r.id),
  });
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
  const recipients = auraRecipients(data, candidates, spec);
  logAura(entity, data, spec, amplifier, recipients);
  for (const r of recipients) {
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
  // Filtered before ranking so unrelated entities cannot crowd the animal out of a fixed-size result.
  const candidates = player.dimension.getEntities({
    location: player.location,
    maxDistance: TAME_SEARCH_RADIUS,
    families: ["gameops_wildlife"],
  });
  const distance = (e) => {
    const l = e.location;
    const p = player.location;
    return (l.x - p.x) ** 2 + (l.y - p.y) ** 2 + (l.z - p.z) ** 2;
  };
  candidates.sort((a, b) => distance(a) - distance(b));
  for (const entity of candidates) {
    if (isTamed(entity)) continue;
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
  } else if (event.id === "gameops:census") {
    const counts = {};
    for (const dimId of DIMENSIONS) {
      for (const species of CENSUS_SPECIES) {
        const type = `gameops:${species}`;
        counts[type] = (counts[type] ?? 0) + world.getDimension(dimId).getEntities({ type }).length;
      }
    }
    console.warn(formatCensus(counts));
  } else if (event.id === "gameops:effects") {
    const player = world.getPlayers({ name: event.message.trim() })[0];
    const amplifiers = {};
    for (const id of ["strength", "resistance"]) {
      const effect = player?.getEffect(id);
      if (effect) amplifiers[id] = effect.amplifier;
    }
    console.warn(formatEffects(event.message.trim(), amplifiers));
  } else if (event.id === "gameops:perf") {
    const start = system.currentTick;
    const t0 = Date.now();
    system.runTimeout(() => {
      const ticks = system.currentTick - start;
      console.warn(`gameops:perf mean_tick_ms=${((Date.now() - t0) / ticks).toFixed(2)} ticks=${ticks}`);
    }, 200);
  }
}, { namespaces: ["gameops"] });
