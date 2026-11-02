// Text shown by the lectern guide. Plain strings so a test can check them; "§" codes are
// Minecraft formatting (l bold, 7 grey, e yellow, a green).
const B = "§l";
const R = "§r";
const Y = "§e";

import { SPECIES } from "./logic.js";

const BASE_PAGES = [
  {
    id: "overview",
    title: "How this server works",
    body: [
      `${B}Proving Grounds${R} is a test world for Bedrock add-on packs. Nothing here is permanent.`,
      "",
      `${Y}Three packs are installed:${R}`,
      "- lab wildlife: six tamable animals",
      "- lab mods stage 0: brute, werewolf, ground dragon",
      "- lab tools: these menus",
      "",
      `${Y}Areas${R} (signposted from the plaza):`,
      "North: wildlife preserve, one glass pen per species",
      "West: safety lab for the two-player pet tests",
      "East: boss arena for the three stage 0 mobs",
      "South: supply room with food, gear and eggs",
      "",
      "Every area has a lectern. Right-click it for that area's guide, spawn and reset options.",
      "A book in your hand opens the main menu anywhere.",
    ].join("\n"),
  },
  {
    id: "wildlife",
    title: "Wildlife pack",
    body: [
      `${B}Rule one:${R} no wild or tamed animal ever attacks a player, even if you hit it or its owner.`,
      "",
      `${Y}Taming:${R} feed it its food. The first feeder becomes the owner.`,
      "Tiger beef, cheetah raw rabbit, lion mutton, black leopard chicken, brown bear salmon, hyena rotten flesh.",
      "",
      `${Y}A tamed pet${R} follows you, defends you, and sits or stands when its owner interacts. Nobody else can sit it.`,
      "Pets tamed before the owner was recorded keep their auras but have no owner for roar or guard.",
      "",
      `${Y}Rivalries:${R} hyenas and lions fight on sight; cheetahs and black leopards fight hyenas too.`,
      "",
      "Tiger 60 hp, cheetah 30, lion 50, black leopard 40, brown bear 80, hyena 30 (alpha 40).",
    ].join("\n"),
  },
  {
    id: "abilities",
    title: "Wildlife abilities",
    body: [
      `${Y}Tiger charge and cheetah sprint${R} start when the animal lands a hit, never on a timer. They run fast, then tire (slow) before they are ready again.`,
      "",
      `${Y}Lion roar:${R} a tamed lion gives its owner and the owner's pets Strength about every 30 seconds within 16 blocks.`,
      "",
      `${Y}Black leopard ambush:${R} pounces at its target, and its Strength aura works at night only. Use /time set night to test.`,
      "",
      `${Y}Brown bear guard:${R} a tamed bear gives its owner Resistance within 6 blocks.`,
      "",
      `${Y}Hyena pack:${R} hyenas get stronger the more of them are close by. A group of four is the full effect.`,
      "",
      "Sitting animals stop their auras.",
    ].join("\n"),
  },
  {
    id: "stage0",
    title: "Stage 0 mobs",
    body: [
      `${B}They only hunt the arena_warrior tag.${R} Players are never targets unless tagged, and hitting them does not make them retaliate.`,
      "",
      `${Y}Training dummy brute:${R} 40 hp, melee. The simplest target.`,
      `${Y}Werewolf:${R} 3x3 blocks, 500 hp, 20 damage, leaps, knockback, fire immune.`,
      `${Y}Ground dragon:${R} a boss that stays on the ground. Bite, tail swipe (knocks back tagged targets) and a fireball you can dodge. Health is chosen when it spawns: 150, 300, 500 or 1000. Never despawns.`,
      "",
      "Turn the tag on from the main menu (Arena tag) to be hunted, and off again to be left alone.",
    ].join("\n"),
  },
  {
    id: "checklist",
    title: "Safety checklist (two players)",
    body: [
      `${B}Condition for putting animals near real players. Use the safety lab (west).${R}`,
      "",
      "1. Tame a pet with player A. Player B hits A: the pet ignores B.",
      "2. A hits B: the pet ignores B.",
      "3. A hits their own pet: it does not fight back.",
      "4. Sit the pet; a wild rival nearby does not make it attack.",
      "5. B cannot sit A's pet.",
      "6. Hit any wild animal: it never attacks you.",
      "",
      "Then the abilities: tiger charge, cheetah sprint, lion roar, leopard at night, bear guard, hyena pack of four.",
      "Spawn eggs exist for each animal; models and textures should look right; the female lion has no mane.",
    ].join("\n"),
  },
  {
    id: "commands",
    title: "Commands (operators)",
    body: [
      "/tag @s add arena_warrior   get hunted by stage 0 mobs",
      "/tag @s remove arena_warrior",
      "/time set night | day",
      "/effect @s strength 60 1",
      "/gamemode survival | creative",
      "/summon gameops:<mob> ~ ~ ~",
      "/summon gameops:ground_dragon \"Boss\" ~ ~ ~ gameops:hp_500",
      "/kill @e[type=gameops:tiger]",
      "/scriptevent gameops:census   logs how many of each animal are loaded",
      "",
      "Mob ids: tiger, cheetah, lion, black_leopard, brown_bear, hyena, training_dummy_brute, werewolf, ground_dragon.",
      "The menus here work without operator rights.",
    ].join("\n"),
  },
];

export const PEN_NOTES = {
  tiger: "Orange or white variant at random. Charge starts when it lands a hit, then it tires. 60 hp, tames with raw beef.",
  cheetah: "Fastest animal, 30 hp. Sprint starts when it lands a hit. Tames with raw rabbit. Hunts rabbits and fights hyenas.",
  lion: "Male (maned) or female at random, 50 hp. Roar gives its owner and pets Strength. Tames with raw mutton. Fights hyenas.",
  black_leopard: "40 hp, pounces. Its Strength aura works at night only. Tames with raw chicken.",
  brown_bear: "80 hp, hits hardest (9). Guard gives its owner Resistance within 6 blocks. Tames with raw salmon.",
  hyena: "Pack of four, one may be an alpha (40 hp). Gets stronger near its pack. Tames with rotten flesh. Fights lions, cheetahs and leopards.",
};

export const STATION_NOTES = {
  safety: "Two-player safety room. Player A stands on the blue pad, player B on the red pad. The glass cage in the middle holds a wild lion and a hyena that fight each other. Food for taming is in the chest. Follow the safety checklist page.",
  arena: "Boss arena. The brute, werewolf and ground dragon stand on their pedestals and only hunt players with the arena_warrior tag. Turn the tag on from the main menu, fight, then turn it off. Reset puts all three back.",
  supply: "Supply room. Chests hold every taming food and a set of diamond gear, a bow and golden apples. Spawn eggs for the animals and mobs are in the egg chest.",
};

const MOBS = {
  training_dummy_brute: ["Training dummy brute", "40 hp, melee attack 4.\nThe simplest target: a hostile that only hunts players carrying the arena_warrior tag.\nHitting it does not make it fight back."],
  werewolf: ["Werewolf", "A hunched 3x3 block wolf-man.\n500 hp, 20 damage, speed 0.38.\nLeaps at its target, knocks back, immune to fire.\nHunts only the arena_warrior tag."],
  ground_dragon: ["Ground dragon", "The arena boss, kept on the ground.\nHitbox 4x3, never despawns, immune to fire and knockback.\nBite (long reach), tail swipe that knocks back tagged targets, and a fireball you can dodge.\nHealth is picked when it spawns: 150, 300, 500 or 1000.\nHunts only the arena_warrior tag."],
};

function speciesCard(id, f) {
  const lines = [
    `${B}${f.name}${R}`,
    "",
    `Health ${f.hp}, attack ${f.dmg}, speed ${f.speed}`,
    `${Y}Tames with${R} ${f.food}`,
    `${Y}Ability${R} ${f.ability}: ${f.short}`,
    `${Y}Hunts${R} ${f.prey}`,
    `${Y}Fights on sight${R} ${f.rivals}`,
    `${Y}Variants${R} ${f.variants}`,
    "",
    "Never attacks a player, wild or tamed.",
  ];
  return { id: `card_${id}`, title: `${f.name} card`, body: lines.join("\n") };
}

export const GUIDE_PAGES = [
  ...BASE_PAGES,
  ...Object.entries(SPECIES).map(([id, f]) => speciesCard(id, f)),
  ...Object.entries(MOBS).map(([id, [title, body]]) => ({ id: `card_${id}`, title: `${title} card`, body: `${B}${title}${R}\n\n${body}` })),
];
