# Crew squads, warriors and the Colosseum

**Status:** draft for owner review (owner said "make it happen", so decisions below marked *my call* are mine)
**Date:** 2026-10-23
**Epic:** to be filed under JDW-59 (Build Crew)
**Builds on:** crew 1.34.x, `life/fight.go` (reactive defence), crew-control (queue, SSE, units proxy), the lab spawn hub and the unit-kit behaviour pack.

## Why

Crew units work alone. The owner wants units that act as a group: a chain of command, talk between units, and a first unit type built for fighting. The proof is a Colosseum on the lab world where a squad of three warriors faces enhanced mobs and bosses, and the console shows it live.

## Goals

1. **Squad** — a named group of units with one leader, shared orders and call-outs. Data model allows squads to nest into a *battalion* (a leader of squad leaders); only squads are built now.
2. **Warriors** — three units whose combat is far beyond today's "hit what is near": focus fire, strafing, critical hits, shield and bow use, healing, regroup and retreat, and role split.
3. **Colosseum** — a large arena on the lab with gates, tiered stands, spawn pens, lighting and a reset button.
4. **Enhanced opposition** — waves of buffed, geared mobs ending in a boss, defined as data, tuned by tier.
5. **Watchable** — the public console shows the squad (members, leader, orders, health) and the wave.

## Non-goals / anti-goals

- No live LLM in the runtime (standing rule). "Talking" is a typed message protocol, not language.
- Nothing on FWB. Everything uses lab-only cheats (console `summon`, `effect`, kits), per the no-cheats rule.
- Warriors never hit players. Hostility to players would be a failure even if it wins fights.
- No battalion behaviour yet, no PvP, no ender dragon or Wither (Phase 3 candidates).
- No new memory quota: warriors reuse existing unit slots.

## Decisions (my call unless stated)

- **Warriors are crew-18, crew-19, crew-20** (existing slots; quota is full at 20 units, memory is the ceiling). They leave the WORK_PLAN idle rotation and get `role: warrior` in `crew.units`. Roles: **Vanguard** (front, shield, tank), **Striker** (melee damage, crits), **Ranger** (bow, kiting, picks off casters). Vanguard is leader by default; if the leader dies the next in chain (Striker, Ranger) takes over.
- **Coordination:** every unit sees the same entity packets, so *target choice is a pure function of shared rules* (focus the lowest-health threat in reach of the leader, tie-break by id). Messages carry only *intent*, so a 0.5 s delay is harmless. Transport is crew-control: units poll and post to `/v1/squads/{id}` with the existing `crew-work` token. Squad state lives in crew-control memory (rebuilt from units' hellos), not Postgres; fight telemetry goes to logs/metrics.
- **Messages:** `order(focus|hold|advance|regroup|retreat)`, `callout(low_health|need_heal|target_down|boss_phase)`, `status(hp, food, weapon, position)`.
- **Arena site:** circular colosseum, outer radius 70, floor radius 45, centred x 0, z -760 (flat zone south of the fields, already lit by the light-block grid, clear of plots x300+). Four gates, spawn pens under the stands, a squad start gate. Built by a generator (`cmd/labarena`, same method as `labcourses`), re-runnable with `--confirm-overwrite`.
- **Enhancement route (spike first):** Tier A uses only console commands: `summon` plus `effect` (resistance, strength, speed, regeneration), `replaceitem` gear, name tags. Tier B is a behaviour-pack entity override for true bosses (more health, custom attacks). Tier B restarts the lab and kicks players, so it needs a `list` check and the pack-bump routine in [[crew-lab-unit-gear]]. MVP ships Tier A; B only if the spike shows A is too weak.
- **Kit:** warriors get diamond (later netherite) gear, shield, bow, arrows, golden apples through the existing unit-kit pack function (lab only).
- **Director:** `lab/arena.sh <lab> run|reset|status <tier>` drives waves from a data file (`arena/waves.yaml`): per wave a mob list, buffs, gear, spacing, pause. It spawns via the server console, counts living mobs by `querytarget`/`testfor`, and ends on squad wipe or boss down. Scoring is the squad's time, deaths and damage taken, written to a log line.

## Phases and workstreams

Phase 1 is the first watchable demo. Streams marked ∥ are independent and run in parallel.

| # | Stream | Phase | Output |
|---|--------|-------|--------|
| S0 | Spike: which mob buffs and gear Bedrock accepts via console, boss options | 1 ∥ | findings doc, decides Tier A/B |
| S1 | Arena generator and build | 1 ∥ | `cmd/labarena`, `arena.sh build` |
| S2 | Squad core: model, API in crew-control, unit client, chain of command | 1 ∥ | `internal/squad`, crew-control endpoints |
| S3 | Warrior combat engine (`life/warrior*.go`), tested offline | 1 ∥ | focus fire, strafe, crits, shield, bow, heal, retreat |
| S4 | Deployment: warrior roles in chart, kit, idle-rotation exclusion | 1 | jdw-deployments PR |
| S5 | Arena director and wave data | 2 (needs S0, S1) | `arena.sh run`, `waves.yaml` |
| S6 | Console: squad panel and wave HUD | 2 | crew-control web |
| S7 | Live tuning on the lab, tiers 1–3, boss | 2 | evidence, tuning commits |
| S8 | Bosses via pack (if needed), battalion | 3 | |

S3 depends on S2 only for its interface; S3 codes against a small `Squad` interface and a fake.

## Success

Phase 1: the three warriors, started by one command, clear a wave of ten buffed zombies and skeletons in the Colosseum with no deaths, and the console shows the squad. Phase 2: they clear tier 3 and fell a boss in some of N runs; failures give tuning data, not a crash.

## Risks

- Bedrock has no attack cooldown, so "extreme combat" means positioning, crits (falling hits), shield timing and bow kiting, not sweep timing. S3 starts from the offline sim, then a lab pass.
- Lab CPU is 1000m shared with 20 units; large waves lag the server. The director caps living mobs (default 12).
- Free-model coding quality: every stream gets gofmt/vet/test review by me before merge ([[free-model-orchestration]]).
