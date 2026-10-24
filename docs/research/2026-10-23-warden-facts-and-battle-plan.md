# Warden fact sheet and three-bot battle plan (2026-10-23)

Tags: **[FILE]** read from the BDS entity definition, **[DOC]** minecraft.wiki or learn.microsoft.com, **[INF]** my inference or arithmetic, not tested.

## Source
- Lab BDS is `bedrock_server-1.26.52.3`, `difficulty=normal` (`/data/server.properties`).
- The warden definition is in `/data/behavior_packs/vanilla_1.20.80/__brarchive/entities.brarchive`. It is the newest pack that defines `minecraft:warden`; no 1.26.x overlay or base `vanilla` pack redefines it. [FILE]
- Parsed with `python3 -I` (index, then a `{"` JSON stream walk). The extracted JSON is at `/tmp/warden-src/warden.json`.
- The file has no `damage_sensor`, no `minecraft:armor`, no `minecraft:damage_sensor` and no invulnerability entries. [FILE] Spawn invulnerability while emerging is engine behaviour (wiki: only `/kill` hurts it while emerging). [DOC]

## Key numbers

| Item | Value | Tag |
|---|---|---|
| Max health | 500 (value 500) | FILE |
| Melee damage | `minecraft:attack` damage 30. Wiki: 16 / 30 / 45 on Easy / Normal / Hard, so 30 on the lab. Melee cooldown about 1.8 s. | FILE + DOC |
| Melee side effect | Disables a shield for 5 s | DOC |
| Sonic boom damage | 10 (wiki: 6 / 10 / 15 by difficulty) | FILE + DOC |
| Sonic boom timing | goal `duration` 3 s; sound at 1.7 s (the hit lands about 1.7 s after the charge starts); `attack_cooldown` 2 s | FILE + DOC |
| Sonic boom range | MS default 15 horizontal and 20 vertical (the warden file does not override them). Wiki trigger: 14 horizontal, only after 10 s of tracking and 5 s without another attack. | DOC |
| Sonic boom knockback | horizontal 2.5, vertical 0.5, height cap 0.5 | FILE |
| Sonic boom defences | Ignores armor, shield, most enchantments (Thorns reflects it on Bedrock), and passes through blocks and entities. The Resistance effect does reduce it. | DOC |
| Warden knockback resistance | 1.0 (cannot be pushed) | FILE |
| Speed | `movement` 0.3; melee box attack speed multiplier 1.2, sonic boom multiplier 1.2; stroll 0.5, investigate 0.7 | FILE |
| Hitbox | 0.9 wide x 2.9 tall | FILE |
| Follow range | 30 | FILE |
| Fire / lava | `fire_immune`; paths over lava and water | FILE |
| Anger | Hostile at 80, max 150, +20 on reaching hostile, decays 1 per second. Default annoyance 35, projectile 10. Wiki: direct attack +100; contact +80 on Bedrock (+35 on Java). Targets that are `warden` or `inanimate` family are ignored. | FILE + DOC |
| Sensing | Vibration listener (about 15 blocks, wiki). Sniff: 24 radius, 4.16 s, 5-10 s cooldown; suspicion radius 6 horizontal and 20 vertical. | FILE + DOC |
| Darkness | `mob_effect` darkness: players within 20 blocks, every 6 s (`cooldown_time`), lasting 13 s. Skips players with the invulnerable ability. | FILE |
| Emerge / dig | Emerge 7 s (group `emerging`, event `minecraft:spawn_emerging`). Dig 5.5 s with `idle_time` 60: after 60 s with nothing detected it digs away (named wardens do not despawn). Dig fires `on_digging_event`. | FILE + DOC |
| Loot / XP | `loot_tables/entities/warden.json` (sculk catalyst, wiki); 5 XP only if the last hit was by a player | FILE + DOC |
| Armor | None (so Sharpness and arrows apply in full) | FILE |

Java vs Bedrock differences: contact anger is +80 on Bedrock and +35 on Java. Bedrock lets Thorns work on the boom, and the warden cannot path through fire. The Java emerging hitbox shrink is Java-only. [DOC]

## Why the fight goes wrong (reading the lab result)
- Melee is the killer. Each hit is 30 raw. Full unenchanted diamond armor (20 points, toughness 8) cuts a 30-damage hit by about 50%, so about 15 per hit, and a 20 HP bot dies in 2 hits (about 3.6 s). [INF, Bedrock armor formula]
- The sonic boom (10 HP, ignores armor but not Resistance) comes at most every 2 s, so at most about 5 DPS (3 with Resistance II) on whoever is in its 15-block range. It is the dominant damage on anyone who cannot be reached by melee but stands inside that range. [INF]
- Lab figures: 50 s with melee zeroed means about 10 squad DPS against the 500 HP pool. [INF] The unbuffed 190 s run was mostly bots dying and returning.
- Warden target choice is "most angry". A melee hit is +100 anger and a projectile only +10, so the striker's sword hits pull aggro off the tank and onto the striker. Expect it to re-target whoever last hit it in melee. [INF from DOC]
- Shields are weak against it. The first melee hit blocks, then the shield is disabled for 5 s, so a shield stops about one hit in three at a 1.8 s attack cadence. The boom ignores shields entirely. [INF from DOC]

## Battle plan

### What blocks what
- Sonic boom: not a shield, not a wall (it goes through blocks, so line-of-sight breaking does nothing). The only counters are distance (more than about 15 blocks horizontally and 20 vertically, from the MS defaults), the Resistance effect, Absorption and healing. [DOC + INF]
- Melee: Protection IV armor, Resistance, and the shield for one hit per 5 s. A melee hit is on the target only when the warden is within roughly 2 blocks (melee box), so kiting works if the warden can be slowed. [INF]
- Darkness: stand more than 20 blocks away and it never applies. It is only a vision effect, no damage. Bots that read entity positions from the server are probably unaffected. Milk clears it, and it reapplies every 6 s, so milk is not worth it. [INF]

### Roles and geometry (arena is a 90-block circle, so radius 45)
- Vanguard: holds melee range in the arena centre half, shield up, and is the permanent aggro holder. Place the fight so the warden's back faces the open arena (more kite room).
- Striker: stays behind the warden, outside its rotation, and trades sword hits with it. The melee_box_attack has `melee_fov` 360, so there is no true "behind". Striker pulls aggro when it hits, so give it the same Protection IV kit and tell it to disengage for 5 s after one swing when it is the target. [INF]
- Ranger: stands 21-28 blocks away. That is outside the darkness radius (20) and outside the boom range (15 horizontal), and inside bow range and under follow range 30. Bow hits add only +10 anger so the ranger is rarely targeted. [INF]
- Do not fight near walls or pillars, because the warden paths 1x3 gaps and the squad needs retreat room.

### DPS and effective HP budget (inference)
- Warden has 500 HP and no armor. Bot DPS needs to be at least 8-10 for a 50-60 s fight. Sharpness V diamond sword: 7 + 1.25 x 5 = about 13 per full hit. Bow with Power V: about 9-12 per full draw.
- Incoming: a warden hit chain on one target is 30 per 1.8 s, about 16.7 raw DPS. Over a 60-100 s fight that is 1000-1700 raw damage.
- Mitigation factor per melee hit on a bot with full kit:
  - Diamond only: x0.50
  - plus Protection IV on all four pieces (16 EPF, minus 64%): x0.18 overall (30 -> 5.4 per hit)
  - plus Resistance II (minus 40%): x0.108 (30 -> 3.2 per hit)
- Budget for a 100 s fight, vanguard holding aggro (inference, to be checked with the telemetry in the boss tests):
  - Melee on the vanguard: 100 / 1.8 s is about 55 hits at 3.2 each with Protection IV and Resistance II, so about 180 HP.
  - Sonic boom: 10 x 0.6 = 6 per boom (Resistance applies, armor does not), one per 2 s on whoever is inside 15 blocks, so up to about 300 HP over the fight if the vanguard and striker stay in range the whole time. The ranger at 21-28 blocks takes none.
  - Supply for the two bots in range: 2 x (20 + 8 Absorption II) = 56 HP, one Totem of Undying for the striker (about one full health bar), plus regular golden apples, each worth a few HP of absorption and regeneration and a 1.6 s eating pause.
  - Conclusion: the supply is well below roughly 480 HP of incoming damage, so a 100 s fight is not winnable by mitigation alone. It needs a shorter fight (more damage per second), line-of-sight breaks against the boom, and healing between bursts. This agrees with the live baseline: unbuffed kit, median 14 deaths in about 123 s.

## Recommended kit (all three bots)
- Diamond armor with Protection IV on all four pieces (Blast Protection IV does nothing against melee or the boom). Unbreaking III and Mending.
- Sword: Sharpness V. Bow: Power V, Infinity (no arrows to lose).
- Offhand: vanguard keeps the shield. Striker and ranger carry a Totem of Undying instead of a shield (Bedrock totems must be in the offhand, so it is one or the other). [INF]
- Consumables: enchanted golden apples (Absorption plus Regeneration plus Resistance); regular golden apples when an enchanted one is not available.
- Director-applied effects (via `effect give`): Resistance II 600 s and Absorption II on all bots before the fight; re-apply Absorption between phases. Vanguard also gets Regeneration I.
- Potions are not needed if the director gives the effects directly.

## Lab director tier-5 boss recipe (fight hard but winnable)
- Keep the Warden at 500 HP. Do not add Health Boost, Resistance or Strength: the melee-zeroed test showed 50 s is already a short kill, so the squad is losing to damage intake, not to HP.
- Spawn with `minecraft:spawn_emerging` (7 s emerge; invulnerable then) and let it roar before bots engage.
- To make it hard but beatable: bots get the kit above plus Resistance II; warden stays at its native 30 melee and 10 boom.
- Optional easing lever if bots still die more than about 3 times: `effect give <warden> slowness 600 1` (about -15% speed) or `weakness` (reduces melee, amplifier tuned by testing). Both are effects, so no restart and no pack override. [INF]
- Pass criterion to tune against: squad win in 60-100 s with 3 or fewer deaths. Judge damage output and deaths separately: record damage dealt, melee exposure and deaths per run before choosing a change. A fight over 150 s can come from low damage output as well as from too much melee exposure, so do not assume either from the duration alone.
- Darkness (20 blocks, every 6 s) cannot be disabled without a pack override; check bots' sight does not depend on the screen.

## Open items to verify live
1. Actual bot kit enchant levels today (this sheet assumes unenchanted diamond).
2. Whether the warden re-targets the striker when it hits (the INF aggro claim).
3. Whether the boom range matches the MS default 15 or the wiki 14 in-game.
4. Slowness and Weakness effects on the warden (not tested).
