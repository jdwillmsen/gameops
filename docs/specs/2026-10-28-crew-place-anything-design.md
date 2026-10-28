# Placing any block correctly

**Status:** draft, calibration first
**Date:** 2026-10-28
**Builds on:** [creative menders and builders](2026-10-28-crew-creative-menders-and-builders-design.md) (`Build`, `Repair`, `act.PlaceBlock`, `internal/blueprint`, `internal/repair`)

## Why

A creative unit can place a block only in the state a plain click gives it. The builder reports every stair, slab, door, trapdoor, log axis, furnace or piston as `skipped_state`, and the mender leaves them "still damaged". Nothing in the placer understands gravity (sand over air falls), support (a torch, rail or redstone wire needs a block to hold it), two-part blocks (doors, beds, tall plants), liquids, or blocks the server reshapes from their neighbours (fences, panes, walls, stair corners). The owner wants units that can place everything a plan or blueprint contains, correctly, and know the quirks.

## Decisions

Calls made without the owner's review; change any of them.

- **Learn the rules from the server, do not hand-write them.** Bedrock derives a placed block's state from the click: the clicked face, the hit point on it, the player's yaw and pitch, and sneaking. The rules differ per block family and several have exceptions. A calibration tool, `cmd/placecal`, joins a throwaway lab in creative and, for every placeable block that has states, places it from a matrix of clicks (face x hit-height x yaw x pitch, plus sneaking where it matters), reads the placed block's full state back, and records which click produced which state. Output is a checked-in table (`internal/place/table`): per block and wanted state, the cheapest click recipe, or `unreachable`.
- **A recipe is data.** `(support side, face, hit point, yaw, pitch, sneak)`. The actuator already aims yaw and pitch and clicks a face; it grows hit-point, sneak and an explicit support choice. `Build` and `Repair` ask the table for the recipe that yields the wanted state instead of using `clickDerivable`.
- **Derived states are not damage.** Calibration also finds the states that change with the neighbours, not the click (fence and wall connections, pane connections, stair shape, rail shape, redstone wire connections and power, leaf distance, double-slab merges, waterlogging by adjacent water). They go in a `derived` list that the matcher in `internal/repair` and `Build.matches` ignore, as `open_bit` and lit twins already are.
- **Unreachable stays visible.** A state no recipe yields is reported (`skipped_state`, with the block and state names, never coordinates), exactly as today. Never place a block that cannot be verified by reading it back.
- **A planner orders the work.** Between the plan and the actuator, a placement planner knows these classes and applies the rules:
  - *Gravity* (sand, red sand, gravel, concrete powder, anvils, dragon egg): place only onto a supported cell, bottom-up; clear a column top-down so nothing falls; a gravity cell the plan leaves floating is reported `unstable`, not placed.
  - *Needs support* (torches, ladders, signs, rails, redstone wire and components, carpets, pressure plates, buttons, levers, doors, plants): placed after their support exists and cleared before it.
  - *Two-part* (doors, beds, tall flowers, double plants, double slabs): one click places both halves; the planner must not place or clear the second half separately, and verifies both.
  - *Liquids*: placed with the matching bucket item through a use-item action; flowing state is derived, not a target.
  - *Interactive state* (repeater delay, comparator mode, door open, lever on): a later phase, by clicking the placed block.
  - *Multi-count* (candles, sea pickles, snow layers): placed by repeated clicks on the same cell.
  - *Block entities* (chest contents, sign text, banners): out of scope.
- **Support aids.** If a recipe needs a neighbouring face that the plan does not have yet (a wall torch, a hanging lantern), the planner may place a temporary scaffold block, then remove it after the click. The scaffold must never be a gravity block, is always removed, and a failed removal fails the task loudly.
- **Reuse the verification.** A cell is counted placed only when the world shows the exact wanted state (minus derived states), as `Build` does now.

## Goals

- A live placement conformance run: the calibration table, replayed on a fresh lab, yields the same states (a regression test for server updates).
- A quirks blueprint (stairs in all four facings and halves, slabs, a door, a trapdoor, logs on three axes, a furnace, a piston, a hanging lantern, sand over a support, a bed, a tall plant, a fence line, a glass-pane wall, torches, a lever) is stamped by a builder and every cell either matches or is reported with a reason.
- The mender repairs Colosseum cells containing stairs and slabs to their exact states.

## Non-goals

- Block entities, entities, redstone power state, farmland moisture, growth stages.
- Survival item gathering.

## Risks and unknowns

1. Calibration size. 300 or so stateful blocks x ~50 click combinations x ~150 ms is about an hour of server time; it is split across units and run once per server version. The matrix is pruned per family after a first pass shows which inputs matter.
2. Some states may be unreachable by any click (e.g. a stair facing a direction that needs the player to stand where a wall is). Scaffold or standing position may fix some; the rest are reported.
3. The server version changes rules. The conformance run exists to catch that; the table carries the server version it was built against.
4. Placement from awkward angles needs the unit to stand in positions the pathfinder must find; recipes include a required standing side.

## Phases

| # | Output |
|---|--------|
| 0 | `cmd/placecal`, its first table, a findings addendum here (which inputs matter per family, derived states, unreachable list), and a research note on block-placement quirks |
| 1 | Actuator click recipes (hit point, sneak, pitch, support choice); `Build` places stateful blocks from the table |
| 2 | Placement planner: gravity, support, two-part, liquids, multi-count, scaffolds, clearing order |
| 3 | `Repair` and the matcher use the table and the derived list; mender repairs exact states |
| 4 | Interactive states (repeater delay, door open, lever) |
| 5 | Quirks blueprint, conformance run, live trial |

## Left to the builder

Table file format and size, recipe search order, matrix pruning, the scaffold block choice, names of skills and report fields.
