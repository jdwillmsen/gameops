# Placing any block correctly

**Status:** phases 0 and 0b done (findings below), phase 1 on
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

## Phase 0 findings (2026-10-28)

Phase 0 is done: `cmd/placecal` (kept, not throwaway), its first table, and the research note ([block placement quirks](../research/2026-10-28-block-placement-quirks.md)). Everything below was observed on a throwaway local lab, not read from docs.

### What was built

- `minecraft/crew/cmd/placecal`: joins a lab through the real dialer, belief tracker, block store and `act.Actuator`, puts its units in creative from the console, builds a stone platform per unit, and for each creative item places it from a pruned click matrix, reads the block and its states back from the unit's own store, breaks it (`BreakStart` only) and resets the platform. Lab-only: a compose service named `lab` or a repeated `--confirm-lab`, a loopback `--addr`, never a target naming fwb, never `kubectl`. Hard caps: `--max-duration` (default 60 min), `--max-placements`, interrupt-safe (breaks strays, removes its ticking areas, writes what it has, marked partial). `--merge-into` folds a later run into an earlier table; `--only` limits the items.
- `minecraft/crew/internal/place`: the table types, an embedded loader (`Default`, `Load`), `Lookup(block, want) (Recipe, bool)` (cheapest recipe for the wanted states, derived keys in `want` ignored, partial wants allowed, integer types compared by value), `Derived(block)`, `Table.Unreachable`. The table is `internal/place/table/1.26.60.6.json.gz` (57 KB, deterministic bytes).
- `act`: `Input.Sneak` (start, hold and stop flags), `Actuator.ClickBlock(against, face, hit, item)` (no choice of support or look, for recipes), and a fix found by the run: a creative take of 64 is refused with status 51 for items that stack to 16 or 1 (signs, banners, eggs, tools), so `TakeCreative` now steps 64, 16, 1.

### Method and what it ran on

- Server: Bedrock Dedicated Server 1.26.60.6, protocol 2225, image `itzg/minecraft-bedrock-server:2026.10.2`, offline, `VIEW_DISTANCE=6`, compose project `jdw297cal` on a loopback port, six units (`PlcA` to `PlcF`) each on its own platform at floor y=220. Torn down with `down -v`.
- Vocabulary: the repo has no per-block list of states other than the block registry the unit already decodes hashes with (dragonfly v0.11.5: 22,091 states over 1,477 block names), so that is what "a state the block can have" means, and what `unreachable` is measured against. The server's own palette was not read. One placed hash was not in the registry (the `redstone` item): the registry is one server version behind in places, and the header counts such hashes.
- Items: the 1,411 creative items that are not entities, liquids, projectiles, fire or spawners (denylist in `main.go`). A block is keyed by the block that appeared, not the item (`oak_sign` gives `standing_sign` and `wall_sign`; doors, beds and signs have items named unlike their blocks).
- Matrix, per item: first pass = each of six faces at centre, four edge hits on the top face, height and side hits on the north face, then yaw 90/180/270, pitch -60/+60 and sneak around the first click that placed. Full pass = every combination of the inputs that changed a state, cheapest first, capped at 360 (one block hit the cap). Items with a single state are placed once. A family (same first-pass responses, e.g. all stairs) is inferred from a sibling after 8 sampled clicks agree (557 blocks). A click that placed nothing is sent three times once the item is known to place (see surprises).
- Neighbour pass (once per family): the block is placed on bare floor with stone or itself on one or four horizontal sides, and, if its state follows yaw, with copies of itself turned 90 and 270 degrees at the front and back; keys that change are derived. Neighbours above and below are not varied.
- Sneak control: stone clicked onto a crafting table placed nothing without sneak and placed with sneak, so the sneak flags reach the server.

### Numbers

| | |
|---|---|
| Items calibrated | 1,411 (all of them reached; 56 `waxed_*` copper items keep the results of an earlier pass and are listed `not_covered`, so the table is marked partial) |
| Blocks in the table | 1,088 (635 with more than one state) |
| States reachable by a click | 2,916 |
| States unreachable by this matrix | 8,996 (see below; most are not meant to be placed) |
| Blocks with derived keys | 165 (union: the fence/pane `minecraft:connection_*`, wall `wall_connection_type_*` and `wall_post_bit`, stair `minecraft:corner`, `rail_direction`, `redstone_signal` of plates, bed `head_piece_bit`, `pale_moss_carpet_side_*`, `growth`, `stability_check`, ...) |
| Blocks inferred from a sibling after sampling | 557 |
| Items that placed no block | 337 (food, tools, dyes, sherds and similar; plants and rafts that need a surface or water the platform lacks), listed in the header |
| Hashes the registry did not know | 1 (the `redstone` item) |
| Clicks | about 41,000 over four passes |
| Throughput | six units placed 18.5 clicks per second together, about 3 per unit, so a click including its cleanup costs about 320 ms |
| Wall clock | first full pass 9 min 44 s (814 items before a unit's hotbar bookkeeping stalled), the remaining 597 items 8 min 36 s, a neighbour re-run 5 min 37 s, a re-run with dropped clicks retried 15 min; startup is 30 to 40 s per run |

### Which inputs matter, by family

Read from the cheapest recipe of each reachable state (an input shows up only where nothing cheaper gives the state).

| Family | Blocks | What gave the states |
|---|---|---|
| stairs | 97 | `weirdo_direction` follows yaw only (0 from yaw 270, 1 from 90, 2 from 0, 3 from 180); `upside_down_bit` follows the face (bottom face) and the hit height on a side face; pitch and sneak do nothing; `minecraft:corner` is derived |
| slabs | 100 | `minecraft:vertical_half`: top face gives bottom, bottom face gives top; a side click depends on hit height. Yaw does nothing. No double-slab state came out of one click |
| doors | 22 | yaw gives `minecraft:cardinal_direction`; only the lower half is read (the upper is placed by the same click and broken with it); `door_hinge_bit` was never true; `open_bit` never true |
| trapdoors | 22 | yaw gives `direction`; face or side hit height gives `upside_down_bit`; `open_bit` unreachable (a use, phase 4) |
| logs, wood, stems, pillars | 73 | the clicked face alone: top and bottom give `pillar_axis` y, east and west x, north and south z. Yaw, pitch, sneak do nothing |
| glazed terracotta | 16 | yaw gives `facing_direction` 2 to 5; 0 and 1 unreachable |
| furnaces, smokers, chests, barrels, lecterns, looms, anvils | 30 | yaw alone, from the top face (`minecraft:cardinal_direction`, or `facing_direction` 2 to 5 for barrels); barrels never gave facing 0 or 1 |
| pistons, observers, droppers, dispensers, crafters | 7 | yaw gives the four horizontal facings; up and down never came, at pitch -60, 60, and (one extra run) -89 and 89 |
| buttons, levers, pressure plates | 33 | the clicked face gives `facing_direction` (buttons) and `lever_direction`; levers on top and bottom also follow yaw (`up_east_west` etc.); pressed and open states unreachable; plate `redstone_signal` is flagged derived (unexplained) |
| torches, lanterns, ladders | 17 | the clicked face: torch `torch_facing_direction`, ladder `facing_direction` 2 to 5 (never from top or bottom), lantern `hanging` from the bottom face |
| signs | 39 | standing sign: yaw gives `ground_sign_direction` 0, 4, 8, 12 only; wall sign: the clicked side face; hanging signs: both |
| beds | 1 | yaw gives `direction`; needs flat ground beside it (the lane); `head_piece_bit` is flagged derived because a neighbour decides which half the cell is |
| rails | 4 | a plain click; `rail_direction` derived |
| fences, panes, walls, bars | 77 | a plain top click; every connection key is derived; walls also `wall_post_bit` |
| candles, pickles, eggs, snow | 23 | a plain click gives the first count only; the others need repeated clicks (planner) |
| repeaters, comparators | 2 | yaw gives `minecraft:cardinal_direction`; delay and mode unreachable (phase 4) |
| skulls, heads, banners | 4 | the clicked face; standing ones follow yaw |

### Derived states

Found by placing the block on bare floor with other blocks around it and comparing states (165 blocks). The matcher and `Build.matches` should ignore these keys, as they do `open_bit` and the lit twins.

- Fences, glass panes, iron bars: `minecraft:connection_north/south/east/west`. The 1.26.60 server stores them in the state, which the older wiki pages did not say.
- Walls: `wall_connection_type_*` and `wall_post_bit`.
- Stairs: `minecraft:corner` (none, inner and outer left and right). It showed only when a copy of the stair stood in front or behind, turned 90 degrees.
- Rails: `rail_direction`. Redstone pressure plates: `redstone_signal` (unexplained, see doubts). Pale moss carpet side keys, `scaffolding` stability, some plant `growth`, `minecraft:block_face` on a few blocks.
- Beds: `head_piece_bit`, because the clicked cell was sometimes the head when a bed stood beside it.

### Unreachable states

8,996 states over the table's blocks are not reached by the matrix. By cause:

- Not placeable by a click at all, by design: interactive state (`open_bit`, `lit`, `powered_bit`, `triggered_bit`, `button_pressed_bit`, `toggle_bit`, repeater delay, comparator mode), growth, fill and age, and the upper half of doors and tall plants (`upper_block_bit` true; the click places both and only the lower is read).
- Multi-count: candles, sea pickles, turtle eggs, snow layers beyond the first (`candles`, `cluster_count`, `turtle_egg_count`, `height`).
- Vocabulary noise: states the registry lists that the game never places, for instance `facing_direction` 0 and 1 on ladders and wall signs, `ground_sign_direction` values not a multiple of 4 (the matrix only varies yaw by 90 degrees), `cardinal_direction` values on blocks that only face four ways; hanging signs alone are 376 each (nine woods) because of the attached and direction cross product.
- Up and down facing of pistons, droppers, dispensers, barrels, observers: not reached with pitch up to 89 degrees from the fixed stand position; possibly decided by where the player stands (untested).
- Door `door_hinge_bit` true, and stairs, slabs and other blocks listed in the doubts as holes.

The per-block lists are in the table (`unreachable`).

### Surprises

1. **Fence, pane and stair-corner connections are in the block state in 1.26.60.** Derived, but they exist; a builder that reads states back sees them.
2. **Sneak reaches the server.** Verified with a control, not assumed: a crafting table as the support only accepts the block when the unit sneaks.
3. **A creative take of 64 is refused with status 51 for items that stack to 16 or 1** (signs, banners, eggs, buckets, tools). Fixed in `TakeCreative`.
4. **The server never names a taken item**, so belief cannot count hotbar slots after a take; a unit that takes items it does not then place fills its hotbar and every later take fails with `ErrHotbarFull`. placecal counts its own takes and clears through the console. A real mender has the same problem with items it takes and does not use.
5. **A dropped click hides a whole face.** On a loaded machine (six units, other work running) a placement was sometimes not answered inside 350 ms and the first pass then lacked, say, the bottom face, so a stair family lost its upside-down states and siblings inferred from it. Fixed by sending a miss again twice once the item is known to place; 11 of 97 stairs and 12 of 100 slabs still came out short (the doubts).
6. **The neighbour pass mistook an unplaced block for a changed one** (a bed with no room for its head reads as "every key differs"). Fixed: unplaced configs say nothing.
7. **Beds and plants need flat ground and dirt around the click.** The platform grew a "lane" of bare floor with a dirt block; items that placed nowhere on the fixtures are tried there.
8. **Yaw 0 faces south**, and `weirdo_direction` 0 (east-ascending) comes from yaw 270; furnaces and chests face back toward the unit (cardinal north from yaw 0).
9. **Doors and beds place their second half with the first click** (the scan after each trial breaks both).
10. **Lab setup:** the lab's world is terrain, so the platform sits at y=220 with a ticking area and mob spawning off; a fresh offline player's first `fill` needs a retry until the chunks load.

### Doubts

Phase 0b answered all of these but the registry-version and inference ones; see *Phase 0b findings* below.

- **Residual holes from dropped clicks:** 11 of 97 stair blocks and 12 of 100 slab blocks lack states their siblings have (for example `bamboo_mosaic_stairs`, `granite_stairs`, `cherry_slab`). The table says "unreachable" for them; it should say "unknown". Next step: after a run, compare each block to its family signature and re-run the ones that disagree. Treat `unreachable` as "not seen", never as proof.
- **56 `waxed_*` copper items** were cut by the 15 minute cap of the last pass and keep their results from the earlier pass, made before the drop-retry fix.
- **Door hinge** never came out true, neither from hit position nor from a stone beside the door, and the neighbour pass's copies of the door beside it did not flag it either. Java-style hinge rules may need a door in a particular position; untested.
- **Pressure plate `redstone_signal` as derived** is unexplained (possibly the unit's own proximity, or a neighbour update on placement).
- **Bed `head_piece_bit`**: flagged derived, but it more likely marks which cell the unit read; beds are placed from the foot cell and the head sits beyond it.
- **Stand position** is fixed (south of the platform, one cell from the nearest target). Facings that follow where the player stands relative to the block (pistons up and down, possibly hoppers and observers) are not varied.
- **Only 90 degree yaw steps**: 16-way rotations (standing signs, banners, skulls) reach four of sixteen.
- **Neighbours above and below** were not varied (redstone wire power, vines, leaf distance), and liquid, waterlogging and falling-block behaviour were not exercised: the platform has no water and gravity blocks stood on a support.
- **The registry is one version behind**: the `redstone` item placed a block whose hash is not in dragonfly v0.11.5.
- **Inference** copies a sibling's sweep after 8 matching clicks; a rare family member with a different rule would pass if the sample missed it.
- The table is a snapshot of one server version; the conformance run (phase 5) is what keeps it honest.

## Phase 0b findings (2026-10-28)

Phase 0b made "unreachable" mean "tried in full and never seen", answered the Phase 0 doubts by experiment, and measured the world rules Phase 2 depends on. Everything below was observed on throwaway local labs (compose projects `jdw297b`, `jdw297b2`, `jdw297b3`, server 1.26.60.6, offline, view distance 6), torn down with `down -v`. The probes are kept: `placecal --probe hinge,pistons,yaws,plates,beds,gravity,support,twopart,multicount,liquids,waterlog,neighbours,occupied` (or `all`) reruns them and prints the same report.

### What changed in the table and the tool

- **Gap check** (`--verify`): each block is compared with the majority of its family (same last name word and same state property names: stairs, slabs, doors, trapdoors, logs, plates, signs, ...). Blocks that disagree, and sibling-inferred blocks in a split family, are run again in full (no inference, reply wait 1.5 s instead of 350 ms, 5 retries, 3 to 4 units instead of 6). The 56 `waxed_*` copper items were run in that pass. What still disagrees is written to the header (`family_gaps`) with the reason.
- **Second halves are read.** A door's hinge is stored on the upper half; calibration now reads it. A door's `upper_block_bit`, a bed's `head_piece_bit` and `occupied_bit` and a plate's `redstone_signal` are derived by construction (the other half, or something standing on it), never unreachable.
- **Not seen is not unreachable.** A sweep cut at the 360 click cap lists its remaining states as `not_seen` and marks the block `truncated`; `unreachable` is only for blocks swept in full. Items no click placed are retried over sand, farmland, end stone, soul sand and grass (`soil` in the recipe) before they count as unplaceable. Items whose placement hit a hash the vocabulary lacks are listed (`unmapped_items`).
- **Sixteen-way rotation**: blocks with `ground_sign_direction` are also tried at 16 yaws (0, 23, 45, 68, ... 338).
- **Stand position** is part of a recipe (`stand`: `above` or `below`) for blocks that face up or down.
- **Derived keys** are measured like against like: the same click on the same floor, read the same way, with and without neighbours. A "derived" key that still changes when only the look changes is `mixed`, not derived.
- Builds are deterministic (ties between equal-cost clicks are ordered by the click's text, then item).

### Numbers

| | Phase 0 | Phase 0b |
|---|---|---|
| Blocks in the table | 1,088 | 1,098 |
| States reachable by a click | 2,916 | 3,264 (14 of them stand-position recipes taken from the probes) |
| States unreachable (tried in full, never seen) | 8,996 | 8,773 |
| States not seen (sweep cut at the cap) | not recorded | 1 (`twisting_vines`) |
| Blocks with derived keys | 165 | 190 |
| Blocks inferred from a sibling | 557 | 431, none of them in a split family |
| Items not covered (`not_covered`) | 56 `waxed_*` | 0 |
| Family disagreements after the re-run | 44 blocks + the stair/slab holes | 2, explained (below) |
| Items no click placed | 337 | 330 (7 plants placed once given sand, farmland, end stone or soul sand) |
| Table size | 57 KB | 61 KB |

Timings: gap check 7 min 6 s (95 items, 3 units, 3,049 clicks); re-run of the derived, door, bed, plate, sign and unplaceable items with the fixed tool 18 min 6 s (566 items, 5 units, 8,341 clicks); gap items and waxed copper in full, no inference, 5 to 6 min (95 items, 3 units); second gap pass about 4 min; signs at 16 yaws about 7 min; probes 6 to 8 min each group on three labs at the same time. Total wall clock about 55 minutes of the 100 allowed.

### The doubts, answered

- **Door hinge.** It is stored on the **upper** half of the door and follows the neighbours, not the click: 140 placements (4 yaws x 7 neighbour setups x 5 hit points on the top face) gave `door_hinge_bit` true in 30, always and only when a solid block, or another door, stood on the matching side (door facing west, from yaw 0: stone on -x, or a door on +x; yaw 90: stone on -z; yaw 180: stone on +x, door on -x; yaw 270: stone on +z). Hit point never mattered. So hinge is derived: a planner reaches it by placing the neighbour first.
- **Piston, sticky piston, dropper, dispenser, barrel, observer, crafter up/down.** Pitch never matters (0, -60, +-89, +-90 all gave the same). Standing position does, as the old Java rule: with the unit within one cell horizontally and its eye at least three cells above the placed cell the block faces up (piston, dropper, dispenser, barrel `facing_direction` 1; observer `down`; crafter `up_north`); with the placed cell above the unit's eye it faces down (`facing_direction` 0; observer `up`; crafter `down_north`). Otherwise it faces the horizontal side from yaw. Hoppers ignore all of it (`facing_direction` 0 in every setup). Recipes now carry `stand`. Only yaw 0 was run for the vertical stands; the crafter's other orientations are not mapped.
- **Sixteen-way rotation.** Standing signs and banners reach all 16 `ground_sign_direction` values with yaw in 22.5 degree steps (rounded to whole degrees): yaw 0 gives 8, yaw 180 gives 0, yaw 79 to 101 give 12, and each 22.5 degrees is one step. Skulls and heads have no rotation state at all (`facing_direction` 1 whatever the yaw; the rotation lives in the block entity), so they are out of reach for state matching. A hanging sign is not placed from the floor top (it needs a ceiling).
- **Pressure plate `redstone_signal`** is real, not placement noise: 0 untouched for 2.5 s, 1 while the unit stood on the plate, 0 again after it stepped off. Derived (by standing entities); the matcher should ignore it.
- **Bed `head_piece_bit`** is 0 on the clicked cell (the foot) and 1 on the head cell, which the same click places one cell away in the facing direction: `direction` 0, 1, 2, 3 puts the head at +z, -x, -z, +x, from yaws 0, 90, 180, 270 with the unit standing opposite. Derived by construction, not a read artifact. `occupied_bit` is 0 (it follows a sleeper).
- **Stairs/slabs/trapdoors short of states**: after the full re-run the family gaps were dropped clicks, as suspected; the `family_gaps` that remain are listed under *Remaining holes*.

### World rules for Phase 2

All times are from the server's clock or the probe's click, so they are good to about 100 ms; "the unit" is a joined client.

**Gravity** (sand, red sand, gravel, white concrete powder, anvil, dragon egg)
- Placed over air they fall: the placed cell was empty about 0.5 s after the click and the block was in the landing cell (5 blocks down) at about 1.0 to 1.15 s. Dragon eggs fall too. Placed on a one-block scaffold they stay; when the scaffold is broken the block drops one cell and is in the scaffold's cell about 1.5 s later.
- **The unit's block store never sees it.** The cell the block left stays "sand" and the landing cell stays "air" in the unit's own view for at least 8 s (no UpdateBlock for the fall); only the server's own `testforblock` shows the truth. A planner cannot verify gravity cells from the world model; it must avoid creating them (support first, bottom-up) and re-read the area after a chunk refresh.
- Concrete powder placed beside a water source turned into concrete within 0.8 s.
- No item drops were seen from any of these landing.

**Support loss** (tile drops on; the support was broken by the unit)
- These popped off as one item each, the dependent block gone about 150 ms after the break (the support cell itself about 70 to 100 ms after): torch (top or side), redstone wire (drops `redstone`), rail, carpet, stone pressure plate, standing sign and wall sign (drop `oak_sign`), ladder, hanging lantern (support broken above it), lever, stone button, repeater, sapling (on dirt), flower pot. A door dropped one door item: the lower half was gone at +145 ms and the upper at +203 ms.
- A **snow layer and a candle stay** when the block under them is broken (nothing changed in 2.5 s). A bed could not be tested this way: it is not placed unless both halves have a block under them.

**Two-part blocks**
- A door is one click: lower and upper both appear. With the upper cell occupied (stone or glass) **nothing is placed**. A bed is one click: foot on the clicked cell, head one cell on in the facing direction; with the head cell occupied by stone nothing is placed, and with no block under the head cell nothing is placed (needs a solid cell below both halves).
- Tall grass, large fern, sunflower, rose bush, peony, lilac and pitcher plant are one click from a grass block: both halves appear; with the upper cell occupied nothing is placed. The `double_plant` item name does not exist.
- A second slab clicked on a bottom slab's top face makes `oak_double_slab`; clicked on the north face of the bottom slab at the low hit it places a new bottom slab in the cell beside, at the high hit a top slab beside. A birch slab on an oak slab does not merge: it goes in the cell above. (Clicking the slab's own half merges only through the top face of a bottom slab.)

**Multi-count** (clicking the same cell again, no sneak, the clicked block's top or side face)
- Candles (`candles` 0 to 3), sea pickles (`cluster_count` 0 to 3), turtle eggs (`turtle_egg_count` one to four) and snow layers (`height` 0 to 7; six clicks gave 0 to 5) all increment on each click of the existing block's face. The fifth candle spills into the next cell. Sneaking does not change candles, pickles or snow; with sneak a turtle egg stays at one egg. Leaf litter's `growth` also increments; pink petals and wildflowers placed nothing on the platform floor.

**Liquids**
- A water or lava bucket works with the same use-item-on-block action as any block (`ClickBlock`, `ClientPrediction` success, face of the block clicked): the source appears in the cell the face points at, as `water[liquid_depth=0]` / `lava[liquid_depth=0]`, at the first click about 150 ms and about 1 s when the take of the bucket from the creative inventory is first. A use-item "click air" with the look pointing at the cell did nothing. There was no flow in the closed pit used.
- Water or lava onto a cell holding a plain replaceable (short grass, a snow layer, a torch, the other liquid) replaces it; onto a stone block, a slab or a fence nothing happens. Lava onto water gives lava, water onto lava gives water (no obsidian or cobblestone in the source-onto-source case).

**Waterlogging**
- Observable only partially: the unit's store reads layer 0 only. Slabs, stairs, fences, panes, trapdoors, doors, chests, signs, levers, rails, carpets, stone and sea pickles can be placed into a water source cell, and the cell then reads the block; breaking it leaves air, not water, so the water was displaced (no second layer visible). Torches, candles, ladders and redstone wire cannot be placed in water (the cell still reads water).

**Neighbour-derived shapes with neighbours above and below**
- Fences, glass panes and iron bars: the four `connection_*` bits follow only horizontal neighbours; a block above or below (stone, or the same block) changes nothing, and placing one on top of or under another gives the same bits. Walls: `wall_connection_type_*` is `tall` toward a side neighbour when a block sits above the wall, `short` without it; `wall_post_bit` stays 1 in every case tried. Stairs were not tried with neighbours above or below.

**Occupied target cells**
- Placing a block into a cell holding water, lava, short grass, a snow layer (1 or 3), fire, a vine, a dead bush or seagrass **replaces it**; into torch, carpet, slab, candle or redstone wire it **does nothing** (the item is not placed and the old block stays).

### Remaining holes

- **Four plants and 326 non-block items still place nothing**: cactus, sugar cane, kelp and cocoa beans are refused on every soil the lane offered (cause not found; cocoa needs a log side and kelp water, which the lane lacks). The rest are food, tools, dyes, sherds and the like. They are listed in the header, not called unreachable.
- **Hanging signs**: 8 of 384 states per wood are reached; the rest need a ceiling with chain support, attachment between two blocks or a wall side, which the lane and fixtures do not offer. They are `unreachable` only relative to this geometry.
- **Stand recipes** (`above`, `below`) were observed at yaw 0 for one setup each; the horizontal facings in those setups were not mapped, and the crafter's other orientations are not. They were added to the table by hand from the probe report, not by the matrix, and the header says so.
- **Door hinge, wall height and stair corners** need a neighbour, and **candle, pickle, egg and snow counts and double slabs** a repeated click, not a different single click. The table marks the first group derived and the second unreachable-by-one-click; it cannot say "this state needs a neighbour on the left", so Phase 2 reads that rule from this document.
- **The unit's world model is blind to falling blocks and to a second water layer**, so Phase 2 cannot verify gravity cells or waterlogging from belief; it needs a chunk refresh or server-side reads.
- **Two family disagreements are kept, on purpose**: `bamboo_block` and `stripped_bamboo_block` reach three axes while the quartz and purpur blocks of their name group reach one; the one-axis blocks were run in full too. `twisting_vines` is cut by the click cap (1 state not seen).
- **Still sibling-inferred**: 431 blocks copy a family sweep after 8 agreeing clicks. The gap check re-runs those that sit in a split family; a rare member with its own rule in a unanimous family would still pass.
- **Registry one version behind**: redstone wire and fire read as unmapped hashes (listed as `unmapped_items`: `redstone`).
- Not probed: stairs with neighbours above or below, reach distance, doors on a slab or stairs, the item drops of landing gravity blocks onto a replaceable cell, liquid flow, observing waterlogging through the item drops.
