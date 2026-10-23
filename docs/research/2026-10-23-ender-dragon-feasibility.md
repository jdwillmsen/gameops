# Ender Dragon for the crew bots — feasibility (2026-10-23)

Question: can vanguard / striker / ranger (Go + gophertunnel, packet-only) fight an Ender Dragon on the lab BDS?
Tags: **[V]** verified from a source named inline, **[L]** verified live in the arena-mob-buffs spike, **[A]** assumed or not found (needs a probe). Wiki figures come from minecraft.wiki's shared (both-edition) tables unless marked Bedrock.

## 1. Mechanics (Bedrock)

| Topic | Finding |
|---|---|
| HP | 200 [V wiki]. Hits outside the head take about 75% less damage on Java 1.21.4+, effective ~800 body HP; Bedrock "temporarily removes head reduction when a projectile hits the head" [V wiki]. Exact Bedrock body multiplier [A]. |
| Phases | Guarding (circles pillars; outside while crystals live, inside once gone), strafing (after a crystal dies, fireball at a player within 64 blocks), perching (chance 1/(3+crystals alive) per path end), breath (3 s, if a player is within 20 of the portal), charge (if no player near the portal 5 s after landing), take-off after 4 breaths. **Bedrock-only dive** at the player's position [V wiki]. |
| Charge / dive | Bedrock charge requires line of sight [V wiki]. |
| Damage | Wings 5 HP (10 on head); breath cloud 3 HP/s; fireball leaves a lingering Harming II cloud (6 HP/s, magic, armour does not reduce, Protection does) [V wiki]. Per-difficulty melee values not found [A]. |
| Crystals | Heal 1 HP per 0.5 s within 32 blocks; explosion power 6; `mobGriefing=false` stops crystal block damage on Bedrock [V wiki]. Each crystal destroyed hurts the dragon and triggers a fireball. |
| Perch | Bedrock lands on the highest block at X=0, Z=0, up to Y=101; with no block at 0,0 it **freezes** when perching [V wiki]. Arrows and tridents bounce off a perched dragon and catch fire; Bedrock dragons can also deflect projectiles while perched (Preview 26.40.26) [V wiki]. Melee/bed damage is the perch window. |
| Block destruction | Destroys most blocks it flies through; `mobGriefing=false` prevents it [V wiki]. Immune on Bedrock: End stone (natural), bedrock, barrier, command blocks, End portal, reinforced deepslate, crying obsidian, respawn anchors, light, border, jigsaw, allow/deny. Obsidian is *not* in the confirmed immune list, so the pillars are destructible [A]. |
| Overworld summon | Flies to X=0, Z=0, **never perches**, flies indefinitely, crystals (if any) still heal; no exit portal or egg on death [V wiki, Java-sourced]. Bedrock dragons do not use portals [V wiki]. Summon works from console but it leaves a ticking area in seconds [L spike]. |
| Flight height | Not found in the wiki text read; perch Y up to 101 only. Assume it cruises well above the End islands, roughly Y 70-100+ [A]. |
| Restriction to an area | No vanilla mechanism. Practical levers: `mobGriefing=false`, a ticking area large enough to hold its circuit, or a behaviour-pack override of `ender_dragon` (its movement targets are hard-coded around 0,0 and the 150/10-block retarget rule) [A]. |

## 2. Ways to run the fight

| Option | How | Pros | Cons |
|---|---|---|---|
| A. Real End | Bots go through a portal (or lab `execute in the_end run tp` / `/dimension`-style cheat; syntax not probed [A]); server sends `ChangeDimension{Dimension, Position, Respawn, LoadingScreenID}` [V gophertunnel source]. | Authentic phases, perch, crystals, pillars, portal, egg. | Bot client must survive a dimension switch: both `world/blocks/store.go` and `belief/belief.go` already handle `*packet.ChangeDimension` by resetting block state [V repo]. Still needed [A]: re-anchor on new `StartGame`-style position, ack the switch with `PlayerAction` DimensionChangeDone (Geyser clears its switching flag on `DIMENSION_CHANGE_SUCCESS`, a PocketMine thread notes a client stalls on the loading screen otherwise [V search]), clear stale entity table, expect a full chunk burst (End terrain is real, not flat). First visit generates the End and its crystals (CPU spike). |
| B. Overworld lab arena | `summon ender_dragon` over Colosseum (radius 45 floor, 70 walls) with `gamerule mobgriefing false`, ticking area covering the arena plus ring. | No dimension handling; arena already exists; cheat-only. | Dragon never perches and flees to 0,0 within seconds [L spike + V wiki], so it needs the arena centred at 0,0 or a pack tweak; no crystals so no heal and no fireball trigger; a single huge flying entity ignores walls (radius 70 is smaller than its circuit). Feels like a punching bag, not the fight. |
| C. Custom dragon (behaviour pack) | Full-file override or a new `gameops:` identifier with reduced HP, damage and a chosen phase set. | Tunable, restart-gated but proven for zombies [L spike Tier B]. | Whole vanilla `ender_dragon.json` must be copied; custom controller behaviours may not be moddable at all [A]; loses vanilla perch/crystal logic unless kept. Resource-pack/model concerns if a new identifier is used [A]. |

Recommendation: **A in a throwaway lab End** for the real fight; use **B** only as a stage-0 spectacle; use **C** only to nerf (HP, wing damage), not to rebuild behaviour.

## 3. What the bots need

Current capability: melee, shield (vanguard), bow-kiting (ranger) [per brief].

| Need | Work | Rough size |
|---|---|---|
| Aim at a moving 3-D target | Lead calculation from entity position/velocity (MoveActor / SetActorMotion deltas), arrow gravity and draw-time curve, pitch up to ~60-80 deg. The ranger's kiting aim is 2-D ground-oriented. | M |
| Arrow supply | Many hundreds; crossbow/bow with Infinity (mending optional). Stock via lab `give` or `replaceitem`, same as the existing gear top-up behaviour pack. | S |
| Crystal destruction | Identify `ender_crystal` entities on pillars (some are caged, tallest ones need a ranged shot or tower); crystal explosion hurts nearby players (power 6), so shoot from >12 blocks. | M |
| Breath / fireball avoidance | Detect AreaEffectCloud entities (breath 3 HP/s, Harming II cloud) and path out; shield does nothing against magic clouds. | M |
| Charge / dive response | Vanguard shield for the wing hit (5 HP, knockback); dodge side-step on line-of-sight charge. | M |
| Perch window | Melee at the portal fountain (Bedrock arrows bounce); striker and vanguard close in, ranger holds off. Needs pathing around the fountain and onto Y<=101 stack. | M |
| Healing | Golden apples / health: eat logic, absorption; mostly present if food handling exists [A]. | S |
| Fall / void safety | End has void; knockback near islands edge is lethal. Use Resistance/Slow Falling from console (lab only), and keep bots off outer islands. Elytra/flight mode **not needed** for a ground fight; only for chasing, which we avoid. | S |
| Dimension handling | See option A table. | M |

Feasibility rating: **vanilla full fight (stage 2): low-medium, 3/5 hard**; **bow-only nerfed fight (stage 1): medium, achievable** with the ranger aim work; **spectate (stage 0): trivial**.

## Staged plan

- **Stage 0, spectate:** lab ticking area, `mobgriefing false`, summon at arena centre (with the arena at or near 0,0), bots observe via belief tracker; confirm they survive a dragon flyby by staying inside the walls or out of reach. Engineering: none beyond ensuring the director does not engage; log entity tracking of a Y>100 target.
- **Stage 1, bow-only kill of a nerfed dragon:** option B or C; pack override to ~40-80 HP, wing damage halved, arrow stock by `give`. Engineering: 3-D lead-aim for ranger (and striker with a bow), flying-target selection, retreat from the dragon's circuit, spectacle-safe arena. Gate on measured hits/min.
- **Stage 2, full fight in a lab End:** option A. Engineering: dimension-change ack and re-anchor, crystal targeting, cloud avoidance, perch melee phase, void safety, repeatable reset (kill, re-summon crystals via the 604-tick respawn, or restore the world).

## 4. Risks

- **CPU:** lab is limited to ~1 core; the dragon is a heavy entity and the End generation plus a large ticking area to hold its circuit multiplies chunk work. Probe tick time with the dragon live before putting bots in [A].
- **Chunk loading:** dragon leaves small ticking areas in seconds [L]; a ring of areas or a big area is needed; unloaded dragon freezes or becomes unselectable.
- **Griefing:** with `mobgriefing true` it destroys the Colosseum (non-immune blocks) and drops container contents [V wiki]; set false for the whole lab session, and re-set after. Note `mobgriefing false` also disables other mobs' griefing, including bot-relevant creepers, which is fine.
- **Players' safety:** lab only, cheats on, never FWB. Keep human players out of the world during tests; `/kill` on Bedrock leaves the dragon animating [V wiki], so use `kill` plus ticking-area removal for cleanup. Dragon egg drops twice on Bedrock (known bug MCPE-51345) [V wiki], harmless.
- **Unprobed:** body-damage multiplier on Bedrock, cruise altitude, whether the End is reachable on the lab BDS by console, and whether an `ender_dragon` pack override survives the controller.

Sources: minecraft.wiki Ender Dragon and End Crystal pages; gophertunnel `change_dimension.go`; Geyser and PocketMine threads on dimension-change acks; repo `minecraft/crew/internal/{belief,world/blocks}`; `2026-10-23-arena-mob-buffs.md`.
