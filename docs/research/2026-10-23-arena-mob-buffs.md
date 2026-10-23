# Arena mob buffs — Bedrock console-command spike (2026-10-23)

Answers whether the arena/Colosseum design (crew-squads design doc) can run its
enhanced opposition on **console commands alone** (Tier A), and where the ladder
must fall back to a behaviour pack (Tier B). Everything marked **live** was
probed on a throwaway BDS 1.26.52.3 container (`itzg/minecraft-bedrock-server`)
driven with `docker exec <c> send-command '...'`, responses read from
`docker logs`; HP claims were proven with the `damage` command, not by looking.

Probe harness: `docs/research/arena-mob-buffs-probe.sh` (same battery, scripted).

## Method notes

- No chunks tick/load near spawn until you add one:
  `tickingarea add -16 60 -16 16 60 16 arena` → `Added ticking area from -16, 0, -16 to 31, 0, 31.`
  Ticking areas are chunk-column scoped (the y args clamp; mobs at y125 inside a
  y60 area still load). `tickingarea list` works; `listtickingareas` is unknown.
- Spawn terrain at 0,0 is y≈110–117; the probe built a stone pad at y124 and
  summoned at y126–140. Console output is the only observability channel.
- `say` with no players online produces no console log line — the director
  cannot use `say` as a signal; rely on `testfor` / `tag` / `querytarget` /
  `execute … test failed` ERROR lines instead.

## Capability table (Tier A = console-only)

| Capability | Exact command | Verified | Caveats |
|---|---|---|---|
| Load chunks (spawn area) | `tickingarea add -16 60 -16 16 60 16 arena` | live | Full chunk columns; required before any `summon`/`fill` near spawn, else `Unable to summon object` / `Cannot place blocks outside of the world`. Remove: `tickingarea remove arena`. |
| Summon mob | `summon zombie 0 126 0` | live | Bare `summon <type> <x y z>`. |
| Summon variant (spawn event) | `summon <type> <x y z> <yaw> <pitch> <spawnEvent> "<nameTag>"` | live | Rotations are **mandatory** before the event token. Verified events: `minecraft:charged_creeper` (creeper), `minecraft:become_drowned` (zombie), `minecraft:as_baby_jockey` (chicken), `minecraft:promote_to_patrol_captain` (pillager, vindicator), `minecraft:spawn_farmer` (villager), `minecraft:spawn_emerging` (warden). Bad token → `Syntax error: Unexpected "<tok>"`. There is no discovery command; per-mob valid events live in the vanilla entity JSON. |
| Named summon | `summon zombie "Hero" 0 126 0` | live | Name overload only works if the entity definition has `minecraft:nameable` (vanilla mobs do; a minimal pack override drops it — see Tier B). Quotes are parsed for string args. |
| Count survivors (list form) | `testfor @e[tag=arena_wave]` | live | `Found Zombie, Zombie, Skeleton` — duplicates preserved → parseable count. Zero → ERROR `No targets matched selector`. Drops from dead mobs appear as item names (`Found Iron Helmet`); filter with `type=!item` (negation selector verified) or rely on tags (items never inherit tags). |
| Count survivors (numeric form) | `tag @e[tag=arena_wave] add _c` then read log, or `tag @e[tag=arena_wave] list` | live | `Added tag 'arena_wave' to 3 entities`; list form → `The 3 entities have 2 total tags: §awave2§r, §aarena_wave§r` (N = entity count, tags listed unique). Multi-tag AND selector `@e[tag=a,tag=b]` verified. |
| Structured state | `querytarget @e[tag=arena_wave]` | live | Pretty JSON per entity: `dimension`, `id`, `position{x,y,z}`, `uniqueId`, `yRot`. Best signal for the director (position drift, count). |
| Buff: damage reduction | `effect give @e[tag=arena_wave] resistance 600 4` | live | Amplifier 0–255 (over → `too big, it must be at most 255`); duration silently clamps at 1000000s. Resistance-4 zombie survived `damage` 30 (baseline dies at 20hp/30). |
| Buff: extra hearts | `effect give @e[tag=arena_wave] absorption 600 4` | live | Absorption-4 zombie survived `damage` 25. |
| Buff: raised max HP | `effect give @e[tag=arena_wave] health_boost 600 9` + `effect give … regeneration 600 7` | live | health_boost raises the cap; regeneration tops the mob up (verification recipe: control dies, buffed survives). Regen heals living mobs only — it does **not** work on undead (zombie/skeleton), and does not harm them. +4 max HP per amplifier level (docs; live-proven cap >40 at amp 9). |
| Other effects | `effect give @e[tag=x] <id> <sec> [amp] [hideParticles]` | live | Unknown id → syntax error. Speed/strength/weakness/slowness/fire_resistance are the wave levers. |
| Gear a mob | `replaceitem entity @e[tag=x] slot.armor.head 0 diamond_helmet 1` | live | Works on `slot.armor.head/chest/legs/feet`, `slot.weapon.mainhand`, `slot.weapon.offhand`. `slot.hotbar`/`slot.inventory` on a mob → `An unknown error occurred…`. Bad item id → syntax error. |
| Enchant held item | `/enchant @e[tag=x] sharpness 5` | live | Works on **mobs directly** and on armor stands; targets **mainhand only** (never inspects armour slots). Level bounds per enchant (sharpness 1..5); incompatible pair → `The selected enchantment can't be added to the target item`; re-running on an already-enchanted item is refused (i.e. it persists). Recipe: `replaceitem … slot.weapon.mainhand iron_sword` then `/enchant`. |
| Re-enchantable gear pool | armor stand: `replaceitem entity @e[name=Anvil] slot.weapon.mainhand 0 iron_sword` → `/enchant @e[name=Anvil] sharpness 5` → `kill @e[name=Anvil]` → pickup drop | live | Drops are `@e[type=item]` (testfor: `Found Iron Sword`). Matches the kit-lab memory trick; stands survive `/kill @e[type=!player]` unless included. |
| Kill / cleanup | `kill @e[tag=arena_wave]`, `kill @e[type=!player]` | live | Broad kill also sweeps items and XP orbs — wave-reset friendly. |
| Conditional branching | `execute if entity @e[tag=alive] run <cmd>` / `execute unless entity …` | live | Failure logs ERROR `Execute subcommand if entity test failed.`; success runs the inner command and logs **its** output (verified with `run summon` → `Object successfully summoned`). |
| Position-relative spawn | `execute positioned 12 130 12 run summon zombie ~ ~ ~` | live | Lets the director spawn at pen positions instead of hard-coded coords. |
| Boss-grade mobs | `summon ravager\|warden\|vindicator\|piglin_brute\|wither_skeleton\|iron_golem\|elder_guardian\|wither\|ender_dragon <x y z>` | live | All summon+testfor+kill verified. **Entity id `evoker` does not exist — use `evocation_illager`.** `ender_dragon` roams out of the ticking area within seconds (becomes unselectable by name/type once gone; count immediately or pin it). `wither` drops a nether star on death. |
| Captain-style elite | `summon pillager 0 130 0 0 0 minecraft:promote_to_patrol_captain "Boss" ` | live | Same event also works on vindicator (banner captain). |

## Tier B — behaviour pack override (proven, but restart-gated)

Live-proven mechanics (full detail in the caveats list below):

1. Write a pack under `<world>/behavior_packs/<pack>/` with `manifest.json`
   (format_version 2, header + data module) and `entities/<name>.entity.json`,
   register it in `<world>/world_behavior_packs.json` as
   `[{"pack_id": "<header uuid>", "version": [1, 0, 1]}]`, then **restart the
   server** (`Pack Stack - [00] …` in the log confirms load; restart kicks all
   players — same cost class as the kit-lab `UNIT_KIT_REV` bump).
2. Entity files **replace** the vanilla definition wholesale — no merge. A
   minimal file (`identifier` + `minecraft:health` only) produced zombies that
   float at spawn height (no `minecraft:physics`) and ignore `summon` nameTags
   (no `minecraft:nameable`). Always copy the full vanilla file and edit it.
3. Full vanilla entity JSONs ship inside the BDS install at
   `behavior_packs/vanilla/__brarchive/entities.brarchive`: a 24-entry name
   index (stride 0x400, u32 at +0x3fc is a checksum) followed by a raw
   concatenated JSON stream — parse by walking `{"` tokens; 96 entities
   extracted, including zombie, skeleton, husk, stray, spider, cave_spider,
   pillager, vindicator, evocation_illager, ravager, wither, wither_skeleton,
   ender_dragon, elder_guardian, iron_golem, ghast, guardian.
4. Verified with a full vanilla zombie file patched to `health 400/400`:
   falls from y140 to the pad (physics intact), accepts the `summon` nameTag,
   AI wanders (position + yRot drift over 10s), survives `damage 30` (vanilla
   dies), dies by cumulative ~399 (vanilla dies at 30) — i.e. the override is
   live and tunable.
5. Costs: server restart (kicks players), a pack version bump per change, and
   the full-file copy burden. Prefer Tier A effects/replaceitem; reach for this
   only for stat curves console cannot express (custom max HP without the
   regeneration crutch, custom attack damage, custom movement speed on a
   persistent breed of unit).

## Tier ladder (arena opposition)

Stays Tier A (console-only) through Tier 5; the pack route is optional polish,
not a dependency.

| Tier | Composition (all console) | Design role |
|---|---|---|
| 1 — Grunt | `summon zombie\|skeleton\|husk\|stray "<Name>" <pen>` (or bare form) + `tag @e[…] add arena_wave_t1` + `effect give … strength 300 0` + `replaceitem` stone/iron kit | Numbers, fodder, wave pacing |
| 2 — Veteran | Tier 1 + `tag … add arena_wave_t2` + `effect give … resistance 600 1` + `effect give … health_boost 600 2` + iron kit + `/enchant` weapon (sharpness 3) | Survives initial burst; teaches target priority |
| 3 — Elite | Tier 2 + amp step (`health_boost 600 4`, `resistance 600 2`, `strength 600 1`) + diamond `replaceitem` + `/enchant` 4–5 + variant events (`become_drowned`, `charged_creeper`) + a display name | Requires coordinated focus fire |
| 4 — Mini-boss | Captain spawn (`minecraft:promote_to_patrol_captain`, named) or a single `ravager`/`iron_golem` + stacked effects (`health_boost 600 7`, `resistance 600 3`, `regeneration 600 5` on living bodies) + full enchanted diamond kit | Wave gate; arena spectacle |
| 5 — Boss | `summon wither\|warden\|ender_dragon` at arena centre + effect stack + kit. Wither/warden are stable; dragon leaves the ticking area in seconds — treat as a phase boss or pin with more `tickingarea`s. Wither drops a nether star (economy note). | Round finale |
| Tier B (optional) | Full-file pack override of a wave mob's `minecraft:health`/`minecraft:movement`/`minecraft:attack` on a custom identifier | Only if console effect stacking proves insufficient in playtest; costs a restart per revision |

Wave bookkeeping pattern: tag at spawn (`arena_wave` + tier tag), count with
`testfor @e[tag=arena_wave]` or `tag … list` between waves, reset with
`kill @e[tag=arena_wave]`, branch with `execute if entity … run`.

## Caveats collected (all live unless noted)

- No ticking area → no spawns/fills anywhere (`Unable to summon object`).
- Fall damage: mobs summoned above the pad take fall damage on landing (a
  140→125 drop measurably ate into a 400 HP test mob). Summon at pad+1, or
  accept the chip damage as a hidden nerf.
- Summon rotations are required when using the spawn-event form; the event
  token is positional (cannot skip it to pass only a nameTag in that form).
- `summon` reports success even if the entity definition is broken; broken
  (minimal-pack) mobs are unselectable by name and float forever.
- `testfor` may still match a just-killed mob for ~1s (death animation);
  overkill damage removed corpses before the next poll in every clean test.
- `damage N` is the only console HP probe; small (±1) rounding at the death
  boundary made a 400 HP mob die at cumulative 399 in one run — treat damage
  thresholds as approximate.
- `effect` duration clamps at 1000000s; amplifier bounds 0..255.
- `/enchant` never touches armour slots — enchant via mainhand + stand trick.
- `evoker` is not an entity id (`evocation_illager` is).
- Broad `kill @e[type=!player]` sweeps items, XP orbs, and armor stands too
  (anything that is not a player); tag-based kills are the surgical reset.

## Open questions

- Full per-mob spawn-event catalogue (only the six verified tokens above were
  probed; extract from the brarchive entity files when a wave needs one).
- Whether a pack-defined **new** identifier (e.g. `gameops:arena_brute` with
  `runtime_identifier`) can bypass the full-file copy burden — not probed.
- Client-side resource pack implications of custom entity models — out of
  scope for this spike (console-only tiers never need it).
