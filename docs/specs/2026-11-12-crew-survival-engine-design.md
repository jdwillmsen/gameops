# Crew survival engine

A unit that plays Minecraft the way a careful player does: it stays alive, keeps
its gear, eats, sleeps, stores what it gathers, grows what it needs and moves
up the tech ladder, all by explicit rules that can be tested. No game rule is
changed to help it (no keepinventory, no peaceful): the unit has to cope with
the world as players get it.

## Why

crew-23 on `fwb-seed` died 449 times in 14 hours (skeletons, zombies, spiders).
Each death dropped its axe, so 345 tasks failed `missing_tool`. It respawned at
world spawn, 300 blocks from its work, where a walk cannot reach (only loaded
chunks are walkable). Search and planting skills do not help a unit that cannot
stay alive or keep a tool. The missing layer is survival basics, not wood.

## What exists

`internal/life` (reflexes): fight or flee, swim, eat, rearm from a kit chest
(`KIT_CHEST`, weapon and food only, within 96 blocks), idle walks. `internal/belief`:
health, hunger, inventory, armour, effects, day phase, biome. `internal/skill`:
walk, mine, fell, explore, plant, place, craft (planks, sticks, wooden tools),
chests (deposit, withdraw, haul). `internal/task`: a runner that queues task
templates and lets the reflex pre-empt them.

## Shape: a priority ladder, one owner per tick

Every tick the first rung that applies owns the unit. Rungs 0-2 exist; the work
below fills 3-6. Each rung is a pure function of belief plus remembered facts,
so each is testable in the simulator without a server.

0. **Hazard**: drowning, lava, fire, void, fall. (swim exists; extend.)
1. **Threat**: fight or flee. (exists.) Add: do not idle underground or in the open at night.
2. **Body**: eat when hungry; when health is low and safe, wait to heal sheltered. (eat exists.)
3. **Gear**: keep a weapon, a tool for the current job, armour and food. Rearm
   from the base chest. After a death, recover the drops within five minutes
   (corpse run) before they despawn. Craft a missing tool if materials allow.
4. **Night**: from dusk, go to the bed and sleep; with no bed, dig in (enclosed
   2-high cell, light) and wait until dawn. Never work in the dark away from home.
5. **Stock**: when the inventory is nearly full, deposit surplus at the base
   chest, keeping a protected essentials set (tools, weapon, armour, food,
   saplings, building blocks). Craft chests when storage runs out.
6. **Work**: the goal ledger below.
7. **Idle**.

## Memory

A small file on the unit's volume that survives restarts: home (bed, base
chest, kit chest), forest, farm and mine sites, last death point and time. It
replaces coordinates that today live only in task templates. Facts carry a
timestamp and are re-verified by sight before use.

## Goal ledger (work)

Declarative tech ladder with stock targets, evaluated in order, first unmet goal
becomes the next task:

wood (logs 32, saplings 8, replant) -> planks, sticks, crafting table, chests ->
wooden then stone tools (cobblestone) -> furnace, coal/charcoal, smelt -> iron
tools and armour -> food (crops: wheat, carrots, potatoes; animal pens; cook) ->
tree and crop farms kept replanted.

Cooperation: units share chests by role through the world ledger (crew-control):
gatherers deposit, crafters withdraw, builders take. A unit never takes another
unit's protected stock.

## Phases, each shippable on its own

- **P0, stop dying** (first, because nothing else works without it): bed and
  sleep skill; respawn awareness (the respawn point moves to the base); kit chest
  for units outside the lab with armour and a spare tool; night rule (do not
  work or idle exposed after dusk; shelter); corpse run.
- **P1, inventory and crafting chain**: protected-set inventory manager, deposit
  surplus, craft table/chests/tools, wood-to-stone tool upgrade.
- **P2, wood economy**: search and planting hardening (survives deaths, resumes),
  tree-farm upkeep.
- **P3, food and metal**: crops, animals, furnace/smelting, iron tier. Furnace,
  smelting, torch-lit ore dives and the stone-to-iron chain (`iron_age`) are
  built (JDW-351; README "Safe mining and progression"); crops and animals are
  not. The enchanting table (4 obsidian, 2 diamonds, 1 book) is the ladder's
  long-range goal, craftable but not yet fetchable.
- **P4, memory and ledger**: persisted memory, the goal ledger, multi-unit roles.

## Status

- P0 night rule (rung 4): shipped behind `NIGHT_SHELTER` (JDW-353): bed when the
  respawn point has one next to it, else a shelter, resumed at dawn.
- P0 respawn awareness: the bed tap now works on the live server (the server
  answers only from about 2.6 blocks), and `HOME_POS` walks a unit home after a
  join or a death.

## Verification

Every rung and skill has simulator tests. Add a survival scorecard to the
existing scenario harness: deaths per hour, tools lost, items stored per hour,
nights survived. A phase is done when the scorecard improves on a live unit,
not when its tests pass.

## Out of scope

Changing game rules or difficulty, teleporting units, editing the world to help
a unit. Operator console use is for diagnosis and setup only.
