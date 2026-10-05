// Samples where players and mobs are and prints it to the server log, where
// the console bridge picks it up for the web map.
//
// Read-only, and that is a guardrail rather than a style choice: this runs
// inside a no-cheats survival world whose achievements depend on no script
// ever changing it. Nothing here runs a command, changes an entity, writes a
// property or subscribes to an event, so nothing here can cancel one either.
// The one registration is a timer.
//
// Every line it prints is one of three kinds of record, because the map
// refuses any other: players, mobs, and a heartbeat that also carries the
// throttle state and the last error.
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
const dropped = new Map();

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
  const errorsBefore = errors;
  const started = Date.now();
  let totals = { players: 0, mobs: 0, more: 0 };
  // An exception that escaped would end the timer, and a pack whose timer
  // died is indistinguishable from a broken pipeline.
  try {
    totals = sample(tick, capAt(level));
  } catch (error) {
    noteError("sample", error);
  }
  const scanMs = Date.now() - started;
  throttle(scanMs);
  ticksUntilSample = intervalTicksAt(level) / BASE_TICKS;

  // Sent even with nobody online, so silence means the pipeline broke. It
  // reports the interval and cap the next sample will use, so a change of
  // throttle level shows on the sample that caused it.
  const failed = errors === errorsBefore ? "" : `,"error":${JSON.stringify(lastError)}`;
  print(`{"gen":${gen},"tick":${tick},"ts":${Date.now()},"kind":"tick","players":${totals.players},"mobs":${totals.mobs},"more":${totals.more},"scan":${scanMs},"interval":${intervalTicksAt(level) * TICK_MS},"cap":${capAt(level)},"level":${level},"errors":${errors}${failed}}`);
}

system.runInterval(() => {
  try {
    onTimer();
  } catch {
    // Reporting failed too; the next sample is the retry.
  }
}, BASE_TICKS);

console.log(`mcmap live pack ${VERSION} loaded, mob cap ${configuredCap}`);
