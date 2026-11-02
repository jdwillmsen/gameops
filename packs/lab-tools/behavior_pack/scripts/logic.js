// Pure helpers for the lectern menus. No game imports, so node can test them.

// Keep in step with `tools/proving-grounds-lab.py` in jdw-deployments, which
// builds the world these coordinates describe. Floor top is y -61; players stand at y -60.
export const STAND_Y = -60;

export const PEN_SPECIES = [
  { id: "tiger", food: "beef", count: 2, cx: -24, cz: -42 },
  { id: "cheetah", food: "rabbit", count: 2, cx: 0, cz: -42 },
  { id: "lion", food: "mutton", count: 2, cx: 24, cz: -42 },
  { id: "black_leopard", food: "chicken", count: 2, cx: -24, cz: -66 },
  { id: "brown_bear", food: "salmon", count: 2, cx: 0, cz: -66 },
  { id: "hyena", food: "rotten_flesh", count: 4, cx: 24, cz: -66 },
];

export const ARENA_MOBS = [
  { id: "training_dummy_brute", x: 42 },
  { id: "werewolf", x: 58 },
  { id: "ground_dragon", x: 74, event: "gameops:hp_300" },
];

export const DRAGON_TIERS = [150, 300, 500, 1000];

export const SPAWNABLE = [
  ...PEN_SPECIES.map((p) => p.id),
  ...ARENA_MOBS.map((m) => m.id),
];

// Where each lectern opens what. Key is "x,y,z" of the lectern block.
export const STATIONS = [
  { x: 0, z: -5, kind: "hub" },
  { x: 0, z: 17, kind: "supply" },
  { x: -36, z: -9, kind: "safety" },
  { x: 34, z: -4, kind: "arena" },
  ...PEN_SPECIES.map((p) => ({ x: p.cx + 3, z: p.cz + 9, kind: "pen", species: p.id })),
];

export const TELEPORTS = [
  { name: "Spawn plaza", x: 0, z: 0 },
  { name: "Wildlife preserve", x: 0, z: -29 },
  { name: "Safety lab", x: -36, z: 0 },
  { name: "Boss arena", x: 38, z: 0 },
  { name: "Supply room", x: 0, z: 19 },
];

export function stationAt(x, y, z) {
  if (y !== STAND_Y) return null;
  return STATIONS.find((s) => s.x === Math.floor(x) && s.z === Math.floor(z)) ?? null;
}

export function clampCount(n, min = 1, max = 8) {
  const v = Math.floor(Number(n));
  if (!Number.isFinite(v)) return min;
  return Math.min(max, Math.max(min, v));
}

// A point `distance` blocks along the view direction, flattened onto the player's own level so
// an animal never appears in the air or in the ground. `index` fans a group out sideways.
export function spawnPoint(loc, dir, distance, index = 0) {
  const len = Math.hypot(dir.x, dir.z) || 1;
  const fx = dir.x / len;
  const fz = dir.z / len;
  const side = (index % 2 === 0 ? 1 : -1) * Math.ceil(index / 2) * 1.5;
  return { x: loc.x + fx * distance - fz * side, y: loc.y, z: loc.z + fz * distance + fx * side };
}

export function spawnEventFor(id, tier) {
  if (id !== "ground_dragon") return null;
  return DRAGON_TIERS.includes(tier) ? `gameops:hp_${tier}` : "gameops:hp_300";
}

export function inBox(box, x, z) {
  return x >= box.x1 && x <= box.x2 && z >= box.z1 && z <= box.z2;
}

// Areas that the reset menu can clear and refill. Pens and the arena are walled, so a box test
// finds exactly what belongs to them.
export function resetAreas() {
  const areas = PEN_SPECIES.map((p) => ({
    id: `pen_${p.id}`,
    name: `${p.id.replace(/_/g, " ")} pen`,
    box: { x1: p.cx - 8, x2: p.cx + 8, z1: p.cz - 8, z2: p.cz + 8 },
    types: [`gameops:${p.id}`],
    spawns: Array.from({ length: p.count }, (_, i) => ({
      type: `gameops:${p.id}`, x: p.cx - 2 + 3 * (i % 2) + 0.5, z: p.cz - 2 + 2 * Math.floor(i / 2) + 0.5,
    })),
  }));
  areas.push({
    id: "arena",
    name: "boss arena",
    box: { x1: 33, x2: 81, z1: -25, z2: 25 },
    types: ARENA_MOBS.map((m) => `gameops:${m.id}`),
    spawns: ARENA_MOBS.map((m) => ({ type: `gameops:${m.id}`, x: m.x + 0.5, z: -11.5, event: m.event })),
  });
  areas.push({
    id: "rival_cage",
    name: "rival cage (lion and hyena)",
    box: { x1: -47, x2: -41, z1: -3, z2: 3 },
    types: ["gameops:lion", "gameops:hyena"],
    spawns: [
      { type: "gameops:lion", x: -45.5, z: 0.5 },
      { type: "gameops:hyena", x: -41.5, z: 0.5 },
    ],
  });
  return areas;
}

// The menus act only inside the world the pack was built for: a book in hand elsewhere (another
// world that registers the pack by mistake, or the far side of a flat map) does nothing.
export const LAB_BOUNDS = { x1: -70, x2: 90, z1: -80, z2: 35, y1: -64, y2: -20 };

export function inLab(loc, bounds = LAB_BOUNDS) {
  return loc.x >= bounds.x1 && loc.x <= bounds.x2 && loc.z >= bounds.z1 && loc.z <= bounds.z2
    && loc.y >= bounds.y1 && loc.y <= bounds.y2;
}

export function penCenter(id) {
  const p = PEN_SPECIES.find((x) => x.id === id);
  return p ? { x: p.cx + 0.5, y: STAND_Y, z: p.cz + 0.5 } : null;
}
