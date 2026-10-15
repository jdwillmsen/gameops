// Samples where players and mobs are and prints it to the server log, where
// the console bridge picks it up for the web map.
//
// Read-only, and that is a guardrail rather than a style choice: this runs
// inside a no-cheats survival world whose achievements depend on no script
// ever changing it. Nothing here runs a command, changes an entity or writes
// a property. The registrations are one timer and five after-events, which
// run once the game has already decided and so cannot cancel anything; each
// only adds to a counter in memory.
//
// Every line it prints is one of four kinds of record, because the map
// refuses any other: players, mobs, counts (what players did since the last
// counts record), and a heartbeat that also carries the throttle state and
// the last error.
import { system, world } from "@minecraft/server";
import { MOB_CAP, VERSION } from "./config.js";

const SENTINEL = "MCMAP1 ";

// The server starts the line after any console line of 4,093 bytes or more
// with a NUL byte, and that count includes its own timestamp prefix. 3,500
// for the record leaves the prefix and the sentinel far more than they need.
const RECORD_BYTES = 3500;

// Every interval below is a multiple of this, so one timer that is never
// re-armed serves all of them: a timer re-armed from inside its own callback
// stops for good the first time that callback throws.
const BASE_TICKS = 20;
const TICK_MS = 50;
const INTERVAL_TICKS = [20, 40, 80, 100];

// Milliseconds a whole sample may take. Over the first it backs off; it
// steps back only after a run of samples under the second, so one slow tick
// does not leave the map degraded and one fast tick does not undo a backoff.
const OVER_BUDGET_MS = 20;
const UNDER_BUDGET_MS = 10;
const SAMPLES_TO_RECOVER = 30;
const CAP_FLOOR = 100;

// Counting what the cap dropped means asking for every mob, which is the
// cost the cap exists to avoid, so it is done this seldom.
const COUNT_EVERY = 10;

// Anvils stop at 50 characters, commands do not.
const NAME_CHARS = 64;
const ERROR_CHARS = 200;

// What the map accepts: an id longer than this, or a list in more parts than
// this, costs the whole record and not only the entity that caused it.
const ID_BYTES = 64;
const MAX_PARTS = 256;

// Counts are deltas the map adds up, so what one record holds is bounded
// here: only the most-counted types of a sample are named, and a player who
// would be the 257th with something to report in one sample goes uncounted
// (and noted as an error). The block totals of those that are counted are
// exact. The map bounds its own side as well.
const MAX_TYPES = 12;
const MAX_PENDING_PLAYERS = 256;
const TYPE_CHARS = 48;

const DIMENSIONS = [
  ["overworld", "minecraft:overworld"],
  ["nether", "minecraft:nether"],
  ["end", "minecraft:the_end"],
];

// Mobs are picked by what they are not. The "mob" family would be the
// obvious filter and is wrong both ways: goats, pandas, piglin brutes and
// every fish lack it, armour stands and NPCs have it.
const NOT_MOBS = {
  excludeFamilies: ["inanimate", "projectile", "lightning"],
  excludeTypes: [
    "minecraft:player",
    "minecraft:item",
    "minecraft:xp_orb",
    "minecraft:armor_stand",
    "minecraft:npc",
    "minecraft:agent",
    "minecraft:ender_crystal",
    "minecraft:painting",
    "minecraft:leash_knot",
    "minecraft:falling_block",
    "minecraft:fishing_hook",
    "minecraft:ender_pearl",
    "minecraft:eye_of_ender_signal",
    "minecraft:fireball",
    "minecraft:small_fireball",
    "minecraft:dragon_fireball",
    "minecraft:wither_skull",
    "minecraft:wither_skull_dangerous",
    "minecraft:shulker_bullet",
    "minecraft:llama_spit",
    "minecraft:thrown_trident",
    "minecraft:lingering_potion",
    "minecraft:breeze_wind_charge_projectile",
    "minecraft:area_effect_cloud",
    "minecraft:evocation_fang",
    "minecraft:ominous_item_spawner",
    "minecraft:tripod_camera",
  ],
};

const NON_ASCII = /[^\x00-\x7f]/;
// Every id the server has been seen to give is a signed integer. One that is
// not is escaped and measured the slow way instead of being trusted.
const ASCII_ID = /^-?[0-9]+$/;

const configuredCap = Number.isInteger(MOB_CAP) && MOB_CAP > 0 ? MOB_CAP : 1000;

// Levels 0 to 3 lengthen the interval; each one after that halves the cap.
function capAt(level) {
  const halvings = Math.max(0, level - (INTERVAL_TICKS.length - 1));
  return Math.max(Math.min(CAP_FLOOR, configuredCap), Math.floor(configuredCap / 2 ** halvings));
}

function intervalTicksAt(level) {
  return INTERVAL_TICKS[Math.min(level, INTERVAL_TICKS.length - 1)];
}

let maxLevel = INTERVAL_TICKS.length - 1;
while (capAt(maxLevel + 1) < capAt(maxLevel)) maxLevel++;

let gen = 0;
let level = 0;
let samplesUnder = 0;
let ticksUntilSample = 1;
let errors = 0;
let lastError = "";
let errorsReported = 0;
const dropped = new Map();

// Names this run of the script among all of them. The map remembers the
// last count it took from each run, which is what lets it ignore a line the
// console bridge hands it twice; a restart of the script restarts seq.
const boot = Date.now();
let seq = 0;
// player id -> what happened since the last counts record
const pending = new Map();
// player id -> the moment up to which their time online has been counted
const onlineSince = new Map();

function print(record) {
  console.log(SENTINEL + record);
}

// An upper bound on the UTF-8 length: a surrogate pair is counted as six
// bytes where it takes four.
function byteLength(text) {
  if (!NON_ASCII.test(text)) return text.length;
  let n = 0;
  for (let i = 0; i < text.length; i++) {
    const c = text.charCodeAt(i);
    n += c < 0x80 ? 1 : c < 0x800 ? 2 : 3;
  }
  return n;
}

function clip(text, max) {
  if (text.length <= max) return text;
  const last = text.charCodeAt(max - 1);
  // Never cut a surrogate pair in half: the lone half is not valid UTF-8.
  return text.slice(0, last >= 0xd800 && last <= 0xdbff ? max - 1 : max);
}

function round(n) {
  return Math.round(n * 10) / 10;
}

// items are already JSON; sizes are their lengths in bytes. Returns how many
// more than `more` went unsent.
function emit(tick, dimension, kind, items, sizes, more) {
  const head = `{"gen":${gen},"tick":${tick},"ts":${Date.now()},"dim":"${dimension}","kind":"${kind}","part":`;
  // Sized for numbers that will never be reached, so the real ones fit.
  const room = RECORD_BYTES - (head.length + '999,"parts":999,"more":999999,"items":['.length + "]}".length);

  const parts = [];
  let current = "";
  let used = 0;
  let unsent = 0;
  for (let i = 0; i < items.length; i++) {
    const size = sizes[i] + 1;
    if (size > room) {
      unsent++;
      continue;
    }
    if (used + size > room) {
      if (parts.length === MAX_PARTS - 1) {
        unsent += items.length - i;
        break;
      }
      parts.push(current);
      current = "";
      used = 0;
    }
    current += (current ? "," : "") + items[i];
    used += size;
  }
  parts.push(current);

  for (let part = 0; part < parts.length; part++) {
    print(`${head}${part},"parts":${parts.length},"more":${more + unsent},"items":[${parts[part]}]}`);
  }
  return unsent;
}

// Failures ride on the heartbeat: the map takes only the three kinds of
// record it knows, and ignores fields it does not.
function noteError(where, error) {
  errors++;
  lastError = clip(`${where}: ${error}`, ERROR_CHARS);
}

// Health and experience level, left out whatever cannot be read: a missing
// field is how the map knows not to show it.
function vitalsOf(player) {
  const out = {};
  try {
    const health = player.getComponent("minecraft:health");
    if (health) {
      const current = round(health.currentValue);
      const max = round(health.effectiveMax);
      if (Number.isFinite(current) && Number.isFinite(max)) {
        out.h = current;
        out.hm = max;
      }
    }
  } catch {
    // No health component yet, as during a respawn.
  }
  try {
    const level = player.level;
    if (Number.isInteger(level) && level >= 0) out.xl = level;
  } catch {
    // Left between being listed and being read.
  }
  return out;
}

function playerItems(players) {
  const items = [];
  const sizes = [];
  for (const player of players) {
    try {
      const id = player.id;
      if (!id || id.length > ID_BYTES) continue;
      const at = player.location;
      const x = round(at.x);
      const y = round(at.y);
      const z = round(at.z);
      if (!Number.isFinite(x + y + z)) continue;
      const item = JSON.stringify({
        i: id,
        n: clip(player.name, NAME_CHARS),
        x,
        y,
        z,
        r: Math.round(player.getRotation().y),
        ...vitalsOf(player),
      });
      items.push(item);
      sizes.push(byteLength(item));
    } catch {
      // Left the world between being listed and being read.
    }
  }
  return { items, sizes };
}

// Type ids repeat for every mob of a kind, so each is shortened and escaped
// once.
const typeNames = new Map();

function typeName(typeId) {
  let known = typeNames.get(typeId);
  if (known === undefined) {
    const json = JSON.stringify(typeId.startsWith("minecraft:") ? typeId.slice(10) : typeId);
    known = { json, ascii: !NON_ASCII.test(json) };
    typeNames.set(typeId, known);
  }
  return known;
}

// Written out by hand because this loop is the whole cost of a sample: on
// the rig it took 2.5 ms for 1,000 mobs against 4.0 ms building an object
// for JSON.stringify, and the server reads the result either way.
function mobItems(mobs) {
  const items = [];
  const sizes = [];
  for (const mob of mobs) {
    try {
      const id = mob.id;
      if (!id || id.length > ID_BYTES) continue;
      const at = mob.location;
      const x = round(at.x);
      const y = round(at.y);
      const z = round(at.z);
      if (!Number.isFinite(x + y + z)) continue;
      const type = typeName(mob.typeId);
      const plain = ASCII_ID.test(id);
      const quotedId = plain ? `"${id}"` : JSON.stringify(id);
      const name = mob.nameTag;
      if (name) {
        const item = `{"i":${quotedId},"t":${type.json},"n":${JSON.stringify(clip(name, NAME_CHARS))},"x":${x},"y":${y},"z":${z}}`;
        items.push(item);
        sizes.push(byteLength(item));
      } else {
        const item = `{"i":${quotedId},"t":${type.json},"x":${x},"y":${y},"z":${z}}`;
        items.push(item);
        sizes.push(plain && type.ascii ? item.length : byteLength(item));
      }
    } catch {
      // Unloaded or removed between the query and the read.
    }
  }
  return { items, sizes };
}

// How many mobs the cap left out. The capped query cannot say, so a full
// count is taken now and then while the cap is being reached and remembered
// in between.
function countDropped(name, dimension, returned, cap) {
  if (returned < cap) {
    dropped.delete(name);
    return 0;
  }
  let known = dropped.get(name);
  if (!known || known.cap !== cap || gen - known.gen >= COUNT_EVERY) {
    known = { gen, cap, more: Math.max(0, dimension.getEntities(NOT_MOBS).length - cap) };
    dropped.set(name, known);
  }
  return known.more;
}

function sampleDimension(tick, name, id, players, cap) {
  const here = players.filter((player) => player.dimension.id === id);
  const playerList = playerItems(here);
  const playersUnsent = emit(tick, name, "players", playerList.items, playerList.sizes, 0);

  const dimension = world.getDimension(id);
  // `closest` is the one thing that limits the query where it runs, so the
  // server never builds more than `cap` entities for the script. It needs a
  // point to measure from; the mobs kept are the ones nearest a player.
  const mobs = dimension.getEntities({
    ...NOT_MOBS,
    location: here.length > 0 ? here[0].location : { x: 0, y: 0, z: 0 },
    closest: cap,
  });
  const more = countDropped(name, dimension, mobs.length, cap);
  const mobList = mobItems(mobs);
  const mobsUnsent = emit(tick, name, "mobs", mobList.items, mobList.sizes, more);
  return {
    players: playerList.items.length - playersUnsent,
    mobs: mobList.items.length - mobsUnsent,
    more: more + mobsUnsent,
  };
}

function sample(tick, cap) {
  const totals = { players: 0, mobs: 0, more: 0 };
  let players = [];
  try {
    players = world.getAllPlayers();
  } catch (error) {
    noteError("players", error);
  }
  for (const [name, id] of DIMENSIONS) {
    // One dimension failing must not blank the other two.
    try {
      const counted = sampleDimension(tick, name, id, players, cap);
      totals.players += counted.players;
      totals.mobs += counted.mobs;
      totals.more += counted.more;
    } catch (error) {
      noteError(name, error);
    }
  }
  return totals;
}

// --- counts --------------------------------------------------------------

function deltaFor(id) {
  if (typeof id !== "string" || !id || id.length > ID_BYTES) return null;
  let delta = pending.get(id);
  if (!delta) {
    if (pending.size >= MAX_PENDING_PLAYERS) {
      noteError("counts", "too many players with counts in one sample");
      return null;
    }
    delta = { d: new Map(), k: new Map(), bt: new Map(), pt: new Map(), b: 0, p: 0, j: 0, l: 0, t: 0 };
    pending.set(id, delta);
  }
  return delta;
}

function bump(map, name) {
  const key = clip(name.startsWith("minecraft:") ? name.slice(10) : name, TYPE_CHARS);
  map.set(key, (map.get(key) || 0) + 1);
}

function guarded(where, fn) {
  return (event) => {
    try {
      fn(event);
    } catch (error) {
      noteError(where, error);
    }
  };
}

function onDeath(event) {
  const dead = event.deadEntity;
  const source = event.damageSource;
  if (dead.typeId === "minecraft:player") {
    const delta = deltaFor(dead.id);
    if (delta) bump(delta.d, String(source.cause));
    return;
  }
  const killer = source.damagingEntity;
  if (!killer || killer.typeId !== "minecraft:player" || NOT_MOBS.excludeTypes.includes(dead.typeId)) return;
  const delta = deltaFor(killer.id);
  if (delta) bump(delta.k, dead.typeId);
}

function onBreak(event) {
  const delta = deltaFor(event.player.id);
  if (!delta) return;
  delta.b++;
  bump(delta.bt, event.brokenBlockPermutation.type.id);
}

function onPlace(event) {
  const delta = deltaFor(event.player.id);
  if (!delta) return;
  delta.p++;
  bump(delta.pt, event.block.typeId);
}

function onSpawn(event) {
  if (!event.initialSpawn) return;
  const id = event.player.id;
  onlineSince.set(id, Date.now());
  const delta = deltaFor(id);
  if (delta) delta.j++;
}

// Time online is added as it passes, at every counts record, and not when a
// player leaves: the server restarts daily and a stop ends sessions without
// any leave event, so what is added only at the end would be lost with it.
// Only whole seconds are taken and the rest waits, so none is counted twice
// or dropped between records.
function accrue(id, now) {
  const mark = onlineSince.get(id);
  if (mark === undefined) return;
  const seconds = Math.floor((now - mark) / 1000);
  if (seconds <= 0) return;
  onlineSince.set(id, mark + seconds * 1000);
  const delta = deltaFor(id);
  if (delta) delta.t += seconds;
}

function onLeave(event) {
  const id = event.playerId;
  accrue(id, Date.now());
  onlineSince.delete(id);
  const delta = deltaFor(id);
  if (delta) delta.l++;
}

// The most-counted names up to the limit as a JSON object, or null if none.
function topTypes(map) {
  if (map.size === 0) return null;
  const sorted = [...map].sort((a, b) => b[1] - a[1]).slice(0, MAX_TYPES);
  return Object.fromEntries(sorted);
}

function deltaItem(id, delta) {
  const item = { i: id };
  const deaths = topTypes(delta.d);
  const kills = topTypes(delta.k);
  const broken = topTypes(delta.bt);
  const placed = topTypes(delta.pt);
  if (deaths) item.d = deaths;
  if (kills) item.k = kills;
  if (delta.b) item.b = delta.b;
  if (broken) item.bt = broken;
  if (delta.p) item.p = delta.p;
  if (placed) item.pt = placed;
  if (delta.j) item.j = delta.j;
  if (delta.l) item.l = delta.l;
  if (delta.t) item.t = delta.t;
  return JSON.stringify(item);
}

// Each line is complete in itself and has a seq of its own, so the map can
// take or refuse it without waiting for its neighbours: there is no list to
// assemble, which is why none of the part/parts bookkeeping of a sample's
// lists is repeated here.
function emitCounts(tick) {
  const now = Date.now();
  for (const id of [...onlineSince.keys()]) accrue(id, now);
  if (pending.size === 0) return;
  const items = [...pending].map(([id, delta]) => deltaItem(id, delta));
  pending.clear();

  const head = () => `{"gen":${gen},"tick":${tick},"ts":${Date.now()},"kind":"counts","boot":${boot},"seq":${seq},"items":[`;
  // Sized for a seq that will never be reached, so the real one fits.
  const room = RECORD_BYTES - (head().length + 10 + "]}".length);
  let current = "";
  let used = 0;
  const flush = () => {
    if (!current) return;
    seq++;
    print(head() + current + "]}");
    current = "";
    used = 0;
  };
  for (const item of items) {
    const size = byteLength(item) + 1;
    if (size > room) {
      noteError("counts", "a player's counts did not fit one record");
      continue;
    }
    if (used + size > room) flush();
    current += (current ? "," : "") + item;
    used += size;
  }
  flush();
}

function subscribeCounts() {
  world.afterEvents.entityDie.subscribe(guarded("die", onDeath));
  world.afterEvents.playerBreakBlock.subscribe(guarded("break", onBreak));
  world.afterEvents.playerPlaceBlock.subscribe(guarded("place", onPlace));
  world.afterEvents.playerSpawn.subscribe(guarded("spawn", onSpawn));
  world.afterEvents.playerLeave.subscribe(guarded("leave", onLeave));
}

// Players already here when the script loads have no spawn event to wait
// for, so their time counts from the first sample. Done there and not at
// load because the world's players can be unreadable that early.
let seeded = false;
function seedOnline() {
  if (seeded) return;
  seeded = true;
  for (const player of world.getAllPlayers()) {
    if (!onlineSince.has(player.id)) onlineSince.set(player.id, Date.now());
  }
}

// The tick rate always wins over the map's freshness: a slow sample makes
// the next one later, and once it is as late as it gets, smaller.
function throttle(scanMs) {
  if (scanMs > OVER_BUDGET_MS) {
    samplesUnder = 0;
    level = Math.min(level + 1, maxLevel);
  } else if (scanMs < UNDER_BUDGET_MS) {
    samplesUnder++;
    if (samplesUnder >= SAMPLES_TO_RECOVER && level > 0) {
      samplesUnder = 0;
      level--;
    }
  } else {
    samplesUnder = 0;
  }
}

function onTimer() {
  if (--ticksUntilSample > 0) return;

  gen++;
  const tick = system.currentTick;
  const started = Date.now();
  let totals = { players: 0, mobs: 0, more: 0 };
  try {
    seedOnline();
  } catch (error) {
    noteError("seed", error);
  }
  // An exception that escaped would end the timer, and a pack whose timer
  // died is indistinguishable from a broken pipeline.
  try {
    totals = sample(tick, capAt(level));
  } catch (error) {
    noteError("sample", error);
  }
  const scanMs = Date.now() - started;
  try {
    emitCounts(tick);
  } catch (error) {
    noteError("counts", error);
  }
  throttle(scanMs);
  ticksUntilSample = intervalTicksAt(level) / BASE_TICKS;

  // Sent even with nobody online, so silence means the pipeline broke. It
  // reports the interval and cap the next sample will use, so a change of
  // throttle level shows on the sample that caused it.
  // Errors from event handlers happen between samples, so the heartbeat
  // reports any that have not been reported yet, not only this sample's.
  const failed = errors === errorsReported ? "" : `,"error":${JSON.stringify(lastError)}`;
  errorsReported = errors;
  print(`{"gen":${gen},"tick":${tick},"ts":${Date.now()},"kind":"tick","players":${totals.players},"mobs":${totals.mobs},"more":${totals.more},"scan":${scanMs},"interval":${intervalTicksAt(level) * TICK_MS},"cap":${capAt(level)},"level":${level},"errors":${errors}${failed}}`);
}

try {
  subscribeCounts();
} catch (error) {
  // Positions are worth having without the counts.
  noteError("subscribe", error);
}

system.runInterval(() => {
  try {
    onTimer();
  } catch {
    // Reporting failed too; the next sample is the retry.
  }
}, BASE_TICKS);

console.log(`mcmap live pack ${VERSION} loaded, mob cap ${configuredCap}`);
