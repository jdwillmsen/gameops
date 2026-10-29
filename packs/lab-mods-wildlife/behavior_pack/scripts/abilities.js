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
