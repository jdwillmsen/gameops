# Lab wildlife: tamable big cats, bear and hyena

Status: draft for owner approval. Scope: crew lab only, never the FWB world.

## Intent

The owner wants new animals that Bedrock does not have, that a player can
tame the way they tame a wolf, but each with its own look and a special
ability. They asked for a tiger, cheetah and lion "and a few other common
examples", and then for the real-life ecosystem to be taken into account.

The point is play: a tamed tiger and a tamed cheetah should feel different
to have beside you, and the wild ones should behave like animals in one
shared ecosystem rather than six unrelated mobs. Real animal facts are the
source of the differences, so an ability should be explainable by "that is
what that animal does".

## Roster

Six animals, all new entities in the lab add-on. Ids are `gameops:<name>`,
families `gameops_<name>`, plus the shared family `gameops_wildlife`.

| Id | Real-life basis | Role as a pet | Ability (owner's brief and research; numbers are starting points) |
|---|---|---|---|
| `tiger` | Largest cat, about 220 kg male; powerful, short dashes, tires quickly | Heavy hitter | **Owner's words: slightly bigger and stronger than the cheetah, lower top speed.** Health 60, damage 8, speed 0.28. A short charge, then it needs to rest |
| `cheetah` | Fastest land animal; light build, weaker bite; sprints then rests | Sprinter | Health 30, damage 5, speed 0.45. A sprint burst that is followed by a tired period; low health |
| `lion` | Social cat living in prides; males have a mane, females do not | Leader | Health 50, damage 7, speed 0.30. A roar gives the owner and nearby tamed pets a short strength buff. Male (mane) and female (no mane) variants |
| `black_leopard` | A melanistic leopard, not a separate species; solitary, nocturnal ambusher, strong climber | Stalker | Health 40, damage 6, speed 0.32. Bonus damage on a first strike or at night; pounce leap |
| `brown_bear` | Mostly solitary, about 30 mph, heavy and strong | Tank | Health 80, damage 9, speed 0.25. Gives its owner Resistance while the bear is close (hostile mobs normally target players, so "drawing aggro" is not used) |
| `hyena` | Spotted hyena: clans led by a female, bite about 1,100 lb (about double a lion's) | Pack fighter | Health 30, damage 7, speed 0.32. Outsized bite for its size; bonus damage when two or more tamed pets fight the same target. Female alpha variant |

Sources for the real-life column are web search results of mixed quality and
are rough guides only; they are listed in the conversation that produced
this document. The `black_leopard` name is deliberate (a "black panther" is
not one species).

## Taming and handling

Modelled on the vanilla wolf, which is entirely data (no code): a taming
item with a probability, then `follow_owner`, sit and stay, defend the owner,
a dyeable collar, heal by feeding.

- Taming foods (my call): tiger raw beef, lion raw mutton, cheetah raw
  rabbit, black leopard raw chicken, brown bear raw salmon, hyena rotten
  flesh (a scavenger).
- A tamed pet follows, sits on interaction, and attacks what hurts or is hit
  by its owner. It never attacks a player, including one who hits its owner.
- No breeding, no armour, no riding (non-goals, see below).

## Ecosystem (my calls; the owner asked for it without detail)

Wild behaviour follows the real food web, and stays among animals:

- **Prey:** wild cats hunt small animals matched to their size: the cheetah
  and black leopard take rabbits and chickens; the lion and tiger also take
  sheep, pigs and cows. The bear scavenges and does not hunt.
- **Rivalry:** hyenas and lions fight on sight, and hyenas harass cheetahs
  and leopards. A tamed lion therefore defends against a wild hyena, which
  is the intended play.
- **Players:** wild animals never attack a player and flee from one who
  approaches without taming food. This keeps the lab rule that mobs do not
  target players.
- **Tamed pets** hunt neither livestock nor other players' pets. They
  fight only the owner's enemies and rival wildlife.
- **Where they live:** the lab world is a flat square hub, so biome spawn
  rules would do nothing. Wild groups are placed by summon, spawn egg or a
  command function. Biome spawn rules are left for a later pass.

## Look

Each animal needs its own model, texture and animations, built in
Blockbench's Bedrock entity format (`.geo.json`, PNG, `.animation.json`).

- One shared four-legged cat skeleton and one shared animation set (walk,
  sit, idle, attack) for the tiger, cheetah, lion and black leopard; each
  differs in proportions, texture and extras (mane, spots, stripes).
- The bear and hyena get their own skeletons: the hyena has a sloping back,
  the bear a heavy shoulder hump.
- Generate models and textures by script, as for the werewolf and dragon
  (`assets-src/`), so they stay reviewable in git. Check each one by loading
  it into Blockbench (web.blockbench.net, driven by Playwright through its
  JavaScript API) and taking screenshots. The owner can open the same files
  in Blockbench for final touches. A spike showed that loading a `.geo.json`
  and a texture works; the export round trip and animations are untested.
- Reusing the vanilla cat model is not acceptable: the owner asked for
  distinct animals.

## Decisions made while writing (my calls, to be checked)

- **Script API:** the lab already runs a script pack (the unit-kit pack
  installs `scripts/main.js`), so scripting is available and no experiment
  flag shows in the chart. JSON is still preferred; scripts for the lion
  roar aura, the hyena pack bonus and the cooldowns if JSON cannot.
- **Existing mobs:** the werewolf, brute and dragon target only the
  `arena_warrior` tag, so they ignore wildlife and pets, and the reverse.
  Nothing is added to make them fight.
- **Ecosystem is approved in outline** (owner: "take into account the whole
  ecosystem"); the details in the Ecosystem section are my calls and open
  to change. Phasing is a suggestion, not a gate: ship one release when it
  is ready.
- **Getting animals:** spawn eggs (the client entity already defines one)
  handed out with `give`, plus a command function per animal for placing a
  wild group. Wild groups are persistent and capped by hand.
- **Livestock:** wild predators skip animals that carry a name tag. Rivalry
  is mutual: a wild hyena or lion also attacks a tamed rival, so a tamed
  pet can be hurt or killed like a vanilla wolf.
- **Bots:** the new ids are left unlisted in `kinds.go`, so they are
  `other`: never a threat and never a suspect. Wildlife never attacks units,
  and listing it as `provoked` would let a hurt unit blame a nearby pet.
  Units never attack a tamed pet.
- **Variants** (male and female lion, alpha hyena, white tiger) are chosen
  at random on spawn and are cosmetic, apart from the alpha hyena having a
  little more health.

## Goals

- All six animals tamable, each visibly different and with its ability.
- The ecosystem relations above, working without player-facing hostility.
- Tamed pets and the existing mobs coexist.
- Passes `tools/check-packs.sh`, loads on the lab's Bedrock version with a
  clean content log, and a Go bot stays connected.

## Non-goals

- Breeding, riding, armour, baby animals, hibernation, biome spawning.
- New AI the engine has no component for. Prefer pure entity JSON; use the
  Script API only where JSON cannot do it (for example counting nearby
  pack-mates), and say so.
- Any change on FWB.

## Anti-goals

- A tiger that is a retextured wolf.
- A pet or wild animal that can hurt a player, directly or by retaliation.
- A change that makes the lab restart repeatedly: draft in `lab-preview`
  first so each release is one restart.

## Constraints from the repo (read `packs/README.md`)

- One pack per theme. The existing `lab-mods-stage0` holds the arena mobs.
  The owner said to put the animals "in the current add-on"; the
  conventions say a pack is one theme, so a new folder in the same add-on
  family, `lab-mods-wildlife`, is the intended reading. **Confirmed by the
  owner (2026-10-28).** The owner also confirmed that wild rivals may hurt
  tamed pets, and asked for the delivery and testing approach to be chosen
  for scale: a `lab-packs` image with a generic installer, and layered
  tests (checker, generators, `node --test`, preview smoke and scale run).
- The chart ships packs as a ConfigMap of about 780 KB raw, and the README
  says to move textured packs to an image or volume built by CI before the
  second themed pack lands. This is that second pack. Six small PNGs and
  JSON may still fit; the builder must measure and decide.
- Mobs there must target only the `arena_warrior` tag and never the player
  family, enforced by `tools/check-packs.sh`. The wildlife pack legitimately
  targets animal families, so the checker needs a second rule for this pack:
  attack and target filters may name only animal families and never
  `player`. The change to the checker is part of this work.
- Pack versions in both manifests, the dependency, and the chart's
  world pack lists are bumped on every shipped change.
- Bots: crew units' tactics are keyed by entity id in
  `minecraft/crew/internal/life/kinds.go`. The new ids need an entry so a
  unit does not attack a tamed pet or a neutral wild animal.

## Risk and rollout

Lab only. Shipping restarts the lab Bedrock server. The owner has allowed
restarts with players online on the lab (2026-10-26); the unit-kit gate
holds a changed pack until nobody is online, and forcing past it needs the
owner's go-ahead each time. Classify with `change-class` before merge; the
chart and checker changes are not docs.

## Verification

- `tools/check-packs.sh` and `tools/packs/test_check_packs.py`.
- Blockbench screenshots of each model.
- `tools/lab-preview` on the lab's Bedrock version (1.26.60.6 at the time of
  writing, not the older default): tame each animal, check follow, sit,
  defend, each ability, the ecosystem fights, and that no animal attacks a
  player.
- Boot content log has no pack errors; a Go bot stays connected.

## Left to the builder

Exact numbers (all stats above are starting values to tune in play), the
shared skeleton's exact boxes, texture art, sound choices, the delivery
mechanism if the ConfigMap is too small, and whether to ship all six in one
release or stage them. Phasing suggestion: tiger and cheetah first to prove
the taming framework, then lion and black leopard on the same skeleton, then
bear, hyena and the ecosystem relations.
