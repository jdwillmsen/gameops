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

// Facts behind the guide cards and the pen signs. Source of truth is the wildlife pack's
// assets-src/species.py; tests keep the taming foods in step with PEN_SPECIES.
export const SPECIES = {
  tiger: { name: "Tiger", hp: 60, dmg: 8, speed: 0.28, food: "raw beef", prey: "sheep, pig, cow", rivals: "none",
    ability: "Charge", short: "Charge, then tires", sign: "Charge + tires", variants: "orange or white" },
  cheetah: { name: "Cheetah", hp: 30, dmg: 5, speed: 0.45, food: "raw rabbit", prey: "rabbit, chicken", rivals: "hyena",
    ability: "Sprint", short: "Sprint after a hit", sign: "Sprint on a hit", variants: "none" },
  lion: { name: "Lion", hp: 50, dmg: 7, speed: 0.30, food: "raw mutton", prey: "sheep, pig, cow", rivals: "hyena",
    ability: "Roar", short: "Roar: Strength aura", sign: "Roar: Strength", variants: "male (mane) or female" },
  black_leopard: { name: "Black Leopard", hp: 40, dmg: 6, speed: 0.32, food: "raw chicken", prey: "rabbit, chicken", rivals: "hyena",
    ability: "Ambush", short: "Pounce, night aura", sign: "Night pounce", variants: "none" },
  brown_bear: { name: "Brown Bear", hp: 80, dmg: 9, speed: 0.25, food: "raw salmon", prey: "none", rivals: "none",
    ability: "Guard", short: "Guard: Resistance", sign: "Guard: Resist", variants: "none" },
  hyena: { name: "Hyena", hp: 30, dmg: 7, speed: 0.32, food: "rotten flesh", prey: "none", rivals: "lion, cheetah, black leopard",
    ability: "Pack", short: "Stronger in a pack", sign: "Pack strength", variants: "regular or alpha (40 hp)" },
};

export const ARENA_MOBS = [
  { id: "training_dummy_brute", x: 42 },
  { id: "werewolf", x: 58 },
  { id: "ground_dragon", x: 74, event: "gameops:hp_300" },
];

// Only the wildlife animals have a cub form; the stage 0 mobs do not.
export function hasCubForm(id) {
  return PEN_SPECIES.some((p) => p.id === id);
}

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
  { name: "Boss arena (inside the dome)", x: 38, z: 0 },
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
    box: { x1: 29, x2: 85, z1: -28, z2: 28 },
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

// The arena dome is sealed, so a lit pad outside carries a player in and one inside carries them out.
// `to` lies off the pad it leads to, or the player would be sent straight back.
export const PADS = [
  { name: "into the arena", x1: 24, x2: 26, z1: -1, z2: 1, to: { x: 38.5, y: STAND_Y, z: 0.5 } },
  { name: "out of the arena", x1: 33, x2: 35, z1: -1, z2: 1, to: { x: 21.5, y: STAND_Y, z: 0.5 } },
];

export function padAt(loc) {
  if (Math.abs(loc.y - STAND_Y) > 1.5 || !inLab(loc)) return null;
  const x = Math.floor(loc.x);
  const z = Math.floor(loc.z);
  return PADS.find((p) => x >= p.x1 && x <= p.x2 && z >= p.z1 && z <= p.z2) ?? null;
}

// Standing signs the script writes on request (scriptevent gameops:signs). rot is the sign's facing:
// 0 south, 4 west, 8 north, 12 east. Four lines of at most 15 characters each fit a sign face.
export const SIGN_LINE_MAX = 15;

export function signs() {
  const out = [
    { x: -3, z: 4, rot: 8, lines: ["PROVING", "GROUNDS", "Add-on test", "world"] },
    { x: 3, z: 4, rot: 8, lines: ["HOW TO TEST", "Right-click a", "lectern or hold", "the Guide book"] },
    { x: -4, z: -12, rot: 0, lines: ["NORTH", "Wildlife", "preserve", "6 species"] },
    { x: 4, z: 12, rot: 8, lines: ["SOUTH", "Supply room", "food, gear,", "eggs, books"] },
    { x: -12, z: 4, rot: 12, lines: ["WEST", "Safety lab", "two-player", "pet tests"] },
    { x: 12, z: -4, rot: 4, lines: ["EAST", "Boss arena", "glass dome", "3 bosses"] },
    { x: 23, z: -3, rot: 4, lines: ["BOSS ARENA", "Step on the", "lit pad to", "enter"] },
    { x: 33, z: 3, rot: 12, lines: ["EXIT", "Lit pad leaves", "the dome", ""] },
    { x: 36, z: 4, rot: 12, lines: ["TARGET TAG", "arena_warrior", "on = hunted", "Book: toggle"] },
    { x: -38, z: -9, rot: 12, lines: ["SAFETY LAB", "Blue pad: A", "Red pad: B", "Cage:lion+hyena"] },
    { x: -10, z: 27, rot: 8, lines: ["FOOD", "taming foods", "for all six", "animals"] },
    { x: -8, z: 27, rot: 8, lines: ["GEAR", "diamond set", "bow, apples", "totem"] },
    { x: 4, z: 27, rot: 8, lines: ["EGGS + BOOKS", "spawn eggs", "guide books", "creative only"] },
  ];
  for (const p of PEN_SPECIES) {
    const f = SPECIES[p.id];
    out.push({
      x: p.cx - 3, z: p.cz + 9, rot: 0,
      lines: [f.name.toUpperCase(), `HP ${f.hp} DMG ${f.dmg}`, `Eats ${f.food === "rotten flesh" ? "rot. flesh" : f.food.replace("raw ", "")}`, f.sign],
    });
  }
  return out;
}
