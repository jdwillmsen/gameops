# Crew collision conformance

Do crew units clip through blocks? A table of scenarios, a reference sweep written
a different way, and seeded random worlds (`minecraft/crew/internal/act/conformance_test.go`)
compare one tick of the unit's collision with per-box offsets over the same
shape data.

## Result against the code before the fix

Every shape family in the table matched the reference: full blocks, slabs,
stairs (all corners), fences, walls and gates at 1.5 high, doors and trapdoors
open and closed, chests, beds, snow layers 0-7, carpets, soul sand, honey,
ladders, step-up under and without a lintel, falling onto edges, ceilings,
inside and outside corners and diagonal moves. Nine approach angles with and
without jumping never left the body inside a box, and 400 random worlds with
knockback agreed tick for tick.

Six cases clipped, all the same cause: a body that sat 5 mm inside a face (a
pose the server accepted, or a float32 landing) was treated as "leaving" the
box, so the next step walked, rose or fell on through it. The sweep now holds a
body within 1 cm of a face.

Blocks that are knowingly kept a cube (`web`, `sweet_berry_bush`, panes, bars,
scaffolding) never clip; they only keep the unit out of space the server would
let it into.

## What the table cannot say

The table checks the prediction against the shape data, not the data against
the Bedrock server. Speed effects (soul sand, honey, cobweb) are not modelled
and need lab measurement. The follow-up is filed as a ticket.

## Baseline for the correction count

Image 1.50.1, 20 lab units, 10.2 minutes from 04:37 UTC on 2026-10-27, summing
each `movement_corrected` line and its `suppressed` count: 2106 corrections,
about 10 per unit per minute. By skill: mine_shaft 844, fight 392, idle 290,
deposit_chest 213, tend 124, fell_tree 122, mine_block 117. 26 lines were
corrections of 2 blocks or more (respawn-like). A mine_shaft pattern stands out:
axis y-, 0.75-0.81, airborne, vy 0.16-0.25, over air.

The after count has to wait for the rollout: repeat the same sum over the first
10 minutes of the new image, and read `crew_movement_clips_total` by reason to
see how many were clips (`corrected_inside_block`) and how many were not
(`corrected_in_free_space`).
