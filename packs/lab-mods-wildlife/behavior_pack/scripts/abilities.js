// Pure helpers; no @minecraft/server import so node --test can load them.

// A burst starts only when a fight triggers it; on a timer alone the rest phase made the animal slower than its base speed.
export function sprintPhase(state, tick, cfg, triggered = false) {
  if (tick < state.until) return state;
  switch (state.phase) {
    case "ready":
      return triggered ? { phase: "active", until: tick + cfg.activeTicks } : state;
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

// Strength I from two hyenas, II from four: the multiplier tops out at 1.75 so a bigger pack is no stronger.
const PACK = { perPet: 0.25, max: 1.75 };
const STRENGTH_I = 1.25;
const STRENGTH_II = 1.75;

// Returns null when the pack is too small for any effect; the aura skips a null amplifier.
export function packStrengthAmplifier(size) {
  const m = packDamageMultiplier(size, PACK);
  if (m >= STRENGTH_II) return 1;
  return m >= STRENGTH_I ? 0 : null;
}

// Standing tamed hyenas within range of the source, the source included; a sitting pet does not fight.
export function packSize(source, candidates, range) {
  let n = 1;
  for (const c of candidates) {
    if (c.id !== source.id && c.tamed && !c.sitting && inRoarRange(source.pos, c.pos, range)) n++;
  }
  return n;
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

export const CENSUS_SPECIES = ["tiger", "cheetah", "lion", "black_leopard", "brown_bear", "hyena"];

// Fixed species order so a smoke test can parse the line without caring which types exist in the world.
export function formatCensus(counts) {
  return `gameops:census ${CENSUS_SPECIES.map((s) => `${s}=${counts[`gameops:${s}`] ?? 0}`).join(" ")}`;
}

// Test hook output; an amplifier of 0 is Strength I, so absence must read "none", not 0.
export function formatEffects(name, amplifiers) {
  const show = (id) => amplifiers[id] ?? "none";
  return `gameops:effects ${name} strength=${show("strength")} resistance=${show("resistance")}`;
}

export const EVENT_PREFIX = "gameops:event ";
export const MAX_LISTED_TARGETS = 8;
const LEVELS = { off: 0, info: 1, debug: 2 };

// An unknown configured level behaves as info, so a typo cannot silence the log.
export function levelAllows(configured, eventLevel) {
  return (LEVELS[configured] ?? LEVELS.info) >= LEVELS[eventLevel];
}

// One line, one JSON object; the leading keys are the contract (ev, type, id, owner) and the rest may grow.
// A target list is capped so a big pack cannot make a line unbounded; target_count keeps the true size.
export function formatEvent(ev, fields, tick) {
  const { type, id, owner, targets, ...rest } = fields;
  const out = { ev, type, id, owner: owner ?? null, ...rest, tick };
  if (targets) {
    out.targets = targets.slice(0, MAX_LISTED_TARGETS);
    out.target_count = targets.length;
  }
  return EVENT_PREFIX + JSON.stringify(out);
}

// Per-key rate limit. Mutates the map for cost; the caller bounds its size.
export function eventDue(state, key, tick, intervalTicks) {
  if (tick < (state.get(key) ?? 0)) return { due: false, state };
  state.set(key, tick + intervalTicks);
  return { due: true, state };
}
