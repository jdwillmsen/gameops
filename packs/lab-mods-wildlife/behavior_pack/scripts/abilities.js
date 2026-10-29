// Pure helpers; no @minecraft/server import so node --test can load them.

export function sprintPhase(state, tick, cfg) {
  if (tick < state.until) return state;
  switch (state.phase) {
    case "ready":
      return { phase: "active", until: tick + cfg.activeTicks };
    case "active":
      return { phase: "tired", until: tick + cfg.tiredTicks };
    default:
      return { phase: "ready", until: tick };
  }
}

export function packDamageMultiplier(nearbyPets, cfg) {
  if (nearbyPets <= 1) return 1;
  return Math.min(cfg.max, 1 + cfg.perPet * (nearbyPets - 1));
}

export function isNight(timeOfDay) {
  return timeOfDay >= 13000 && timeOfDay <= 23000;
}

export function inRoarRange(a, b, range) {
  const dx = a.x - b.x;
  const dy = a.y - b.y;
  const dz = a.z - b.z;
  return dx * dx + dy * dy + dz * dz <= range * range;
}

export function nightOnly(ctx) {
  return isNight(ctx.timeOfDay);
}

// A blocked when-condition keeps the schedule, so the aura fires as soon as it opens.
export function auraDue(state, tick, spec, ctx = {}) {
  if (tick < state.nextAt) return { due: false, next: state };
  if (spec.when && !spec.when(ctx)) return { due: false, next: state };
  return { due: true, next: { nextAt: tick + spec.interval } };
}

export function auraAmplifier(spec, ctx) {
  const a = spec.amplifier ?? 0;
  return typeof a === "function" ? a(ctx) : a;
}

// source and candidates are plain {id, pos, owner, tamed, sitting, type: "player" | "wildlife"}.
export function auraRecipients(source, candidates, spec) {
  if (!source.tamed || source.sitting) return [];
  const out = new Map();
  if (spec.targets.includes("self")) out.set(source.id, source);
  for (const c of candidates) {
    if (c.id === source.id || !inRoarRange(source.pos, c.pos, spec.range)) continue;
    const owner = c.type === "player" && c.id === source.owner && spec.targets.includes("owner");
    const pet = c.type === "wildlife" && c.tamed && spec.targets.includes("pets");
    if (owner || pet) out.set(c.id, c);
  }
  return [...out.values()];
}

// Entities in unloaded chunks vanish from scans; keeping their state for a
// grace period lets them resume instead of restarting, while bounding the map.
export function pruneStale(states, tick, graceTicks) {
  const kept = new Map();
  for (const [id, state] of states) {
    if (tick - state.lastSeen <= graceTicks) kept.set(id, state);
  }
  return kept;
}
