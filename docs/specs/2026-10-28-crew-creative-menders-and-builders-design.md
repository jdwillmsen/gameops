# Creative menders and a builder class

**Status:** draft, spike first
**Date:** 2026-10-28
**Builds on:** [Colosseum repair bot](2026-10-27-crew-colosseum-repair-bot-design.md) (`internal/repair`, `cmd/arenarepair`), [crew squads and arena](2026-10-23-crew-squads-and-arena-design.md), `internal/blueprint`

## Why

Repair works today only as an offline tool: `arenarepair` plans from a world copy and applies console `fill` commands. Nobody sees a bot do it, and it covers only the larger Colosseum. The owner wants crew units that walk up to damaged builds, triage them and fix them in the lab, and a second class that builds new things. Items are not the point yet, so these units run in creative.

## Decisions

Everything below is a call made without the owner's review; change any of it.

- **Two new roles, not one.** `CREW_ROLE` today means a warrior role. It grows two builder roles, parsed by the same setting and refused in the same way when unknown:
  - `mender`: triages a build against its plan and repairs the difference.
  - `builder`: raises a new structure from a blueprint (`internal/blueprint`), the in-world twin of `labcourses blueprint`.
  A unit has one role; a warrior role and a builder role never combine. Builder roles do not join a squad.
- **Creative, lab only.** A builder unit is put in creative by the lab's console (`gamemode creative <name>`), never by the unit. The unit refuses to start work on any server that is not the crew lab (the check `arenarepair` already applies to its `--lab` target), and the gamemode is set per unit, so warriors and the FWB-bound units stay in survival.
- **Looks like it needs items.** Creative means unlimited stock, but a mender takes each block from the creative inventory just before placing it and visibly holds it, instead of placing from nowhere. No kit-chest trips in v1.
- **Reuse the planner, change the executor.** `internal/repair` already produces ordered rectangles of absolute block states with zones and damage kinds. A mender takes that plan, walks to each rectangle and places or breaks through the actuator. Console `fill` stays the bulk fallback when a rectangle is larger than a unit can cover in a task timeout.
- **Triage first.** A mender's first output is a report (zones, damage kinds, counts, worst first) posted to crew-control as task progress, the same shape `arenarepair plan` prints. Repair starts only on a repair task, so an operator can read the triage and decline.
- **Reads blocks as a client.** The scan reader is the unit's own loaded chunks (the phase 2 note in the repair spec), so a mender needs no world-db copy and no save hold. Cells outside loaded chunks stay `Unknown` and are never repaired.
- **Tasks, not new endpoints.** Two templates, `repair_build` (plan source, area box, `dry_run`) and `build_blueprint` (name, origin, rotation), through the existing Task API and crew-control work queue. Both are bounded to the lab area box like other terrain-changing templates.
- **Fight guard unchanged.** A mender stops while tagged mobs or non-crew players are present, as `repair.Apply` does.

## Goals

- A mender repairs a deliberately damaged part of the larger Colosseum on the lab with a player watching the camera.
- A builder stamps `small_house` at a given spot.
- Both are visible in the console with their role.

## Non-goals

- Survival materials, chests, hauling, or any use on FWB.
- Entities, container contents, sign text, levers (same limits as the repair spec).
- Replacing `arenarepair`; it stays the whole-arena batch path.

## Risks and unknowns (the spike answers these)

1. **Creative placement on the wire.** crew has no creative handling. Whether taking a stack takes an item-stack request with a creative-craft action, and whether placement then needs the stack in the hotbar, is unverified against BDS 1.26. Spike: one unit, creative by console, takes one `stone` and places it.
2. **Gamemode visibility.** The unit may not learn its own gamemode from the start-game packet it already handles; belief may need a field.
3. **Breaking.** Creative breaking is instant and uses a different action than survival `act/break.go` assumes.
4. **Scale.** A unit places far slower than `fill`. The executor chooses per rectangle between walking and console fill, and the threshold is a tuning call.

## Phases

| # | Output |
|---|--------|
| 0 | Spike: one creative unit takes and places one block on the lab. Findings recorded here |
| 1 | `mender` and `builder` roles in config, belief and console label; creative set by the lab tooling |
| 2 | Creative actuator: take from creative inventory, place, instant break |
| 3 | `repair_build` template with triage report; live trial on a damaged Colosseum section |
| 4 | `build_blueprint` template and builder trial |

## Left to the builder

Names of the skills, the exact progress report fields, the console badge colour, and the walk-or-fill threshold.
