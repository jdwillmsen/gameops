# Bedrock block placement quirks

Date: 2026-10-28. Written for the JDW-297 placement planner (a bot that clicks a face of a support block with a held item and must end with an exact block state). Research only; nothing here was run on a server.

**Java-vs-Bedrock sensitive:** state names and encodings (everywhere), rail shape on placement, chest pairing, bed/door/sea-pickle support, snow (falls, layers into plants), fence/pane/wall/stair connection storage, waterlogging (layer system, not a state), leaves flags, reach distance.

Status key: facts come from minecraft.wiki Bedrock sections and Mojang docs, read 2026-10-28. Wiki pages are community-edited and several state tables mark Bedrock states "Unsupported" (meaning no aux value, not "does not exist"). Anything not confirmed is tagged (unverified). Version-dependent items mention the version because Bedrock changed several of these in 26.x previews.

## 1. Gravity-affected blocks [1]

| Blocks | Rule |
|---|---|
| sand, red sand, gravel, dragon egg | Fall when support is lost; become a block again on landing |
| concrete powder | Becomes concrete if it touches water while falling or after landing |
| suspicious sand/gravel | Break on landing; contents lost |
| anvils (anvil, chipped, damaged) | Fall; become a block again; landing on a non-replaceable block breaks them |
| snow layer | Bedrock only: falls and adds layers to existing snow (Java drops it as an item) |
| pointed dripstone | Only stalactites (hanging) fall |
| scaffolding | Falls when 7+ blocks from the nearest supported scaffolding |

General rule: a falling block returns to block form only if its bottom centre is in a replaceable block and the block below supports it; otherwise it drops as an item [1]. The page gives no Bedrock-specific support rules beyond this (unverified for Bedrock details of replaceable-block landing).

Effect on a placer: placing one of these on an air-supported face may place it and then move it one tick later; verify position after settling, support it from below first (or place bottom-up), and expect concrete powder next to water to change id.

## 2. Blocks that need support

| Block | Rule as sourced |
|---|---|
| torch | Single `torch` block, state `torch_facing_direction` 1..5 = west, east, north, south, top (face attached to); 0 unused; breaks if attachment is removed [2]. Water flowing in removes it; "some blocks require sneaking". |
| redstone torch | Not read separately (unverified) |
| ladder | Any full solid block face except top/bottom; `facing_direction` 2..5 = north, south, west, east; value is direction from attached block to ladder [3]. Non-full-block attachment: not stated (unverified). |
| signs | Standing sign on top face (`ground_sign_direction` 0-15); wall sign on side (`facing_direction` 2..5). A side-attached sign "floats" even without contact; breaks if the attached block is removed [4]. |
| rails | Must sit on a block with a rimmed top (full block, hopper, top slab, upside-down stairs, top trapdoor); not on sides/bottoms; can be placed underwater; broken by flowing water [5]. |
| redstone dust | Only `redstone_signal` 0-15 stored. The page gives no Bedrock support rule; Java's "solid top or glowstone" check is not confirmed for Bedrock (unverified). The prior-claim in the 2026-10-27 colosseum note says it needs a block below [14]. |
| repeater | Full blocks, upside-down slabs/stairs, furnaces, glass, and (Bedrock only) fences and walls [6]. |
| lever | Top, side, or bottom of full opaque block; top of upside-down slab/stairs; also fences, walls, hoppers; allowed underwater [7]. |
| button | Side, top or bottom of "any full opaque block" [8]. |
| door | Needs a full solid face beneath; both spaces must be free or nothing is placed [9]. |
| bed | Bedrock needs solid blocks below both halves at placement (Java does not); not on transparent blocks; stays if support is later removed [10]. |
| sea pickle | Bedrock: only on complete blocks (stricter than turtle eggs) [11]. |
| turtle egg | No support requirement; not gravity-affected [12]. |
| snow layer | Solid non-ice block; falls if support lost [13]. |
| pointed dripstone | Stalactite breaks/falls when its chain support is removed [15]. |
| seagrass | Opaque block only, not magma [16]. |
| vine | Side of a full block, or bottom of a block [17]. |
| carpet, pressure plate, sapling/flower, sugar cane, cactus, kelp, hanging roots, candle (support), bell, chain, amethyst cluster, banner, lantern | Not sourced (unverified). |

Mojang's custom-block `minecraft:placement_filter` expresses this model: `conditions[]` of `allowed_faces` (down, up, north, south, west, east) plus `block_filter`; if no condition holds the block is not placed, or pops off as an item if already placed [18]. That is the documented shape of a vanilla "needs support" rule, not a list of vanilla values.

Effect on a placer: order builds so the support exists first; remove-support-later is block-specific (beds stay, torches/signs/ladders pop).

## 3. Two-part blocks

| Block | Rule |
|---|---|
| door | Lower half where aimed, upper in the block above. States: `minecraft:cardinal_direction` (lower half only), `door_hinge_bit` (false = left, true = right), `open_bit` (0x4), `upper_block_bit` (0x8). Faces the side closest to the player [9]. |
| bed | Foot on the clicked block, head on the block farther from the player; needs 2 blocks in the facing direction. `direction` 0-3 = south, west, north, east (head direction); `head_piece_bit`; `occupied_bit` [10]. |
| tall grass / double plants | Bedrock `double_plant` split into separate ids in 1.21.0.22; `upper_block_bit`; whether free space above is required is not stated (unverified) [19]. Placing any block on tall grass breaks it with no drop (replaceable) [19]. |
| pitcher plant | State `upper_block_bit` only; two-block placement process not described (unverified) [20]. Torchflower: not sourced. |
| tall seagrass | One block `seagrass` with `sea_grass_type` = default, double_bot, double_top. Not obtainable as an item; formed by bone meal on seagrass with 2 water blocks above, the lower non-flowing [16]. |
| double slab | A merge, not a two-cell block: a top and a bottom slab of the same type in one space; a matching slab onto a lower slab also merges. Bedrock slab state is `minecraft:vertical_half` bottom/top; `double` is Java-only [21]. |
| piston head | Technical block; the piston stores `facing_direction` and a `PistonArm` block entity; head and moving piston cannot be obtained as items [22]. |

Mojang's `minecraft:multi_block` trait sets `minecraft:multi_block_part` for "blocks composed of multiple block parts that act as a single block, like beds or doors" [23].

Effect on a placer: aim at the lower/foot cell only; pre-clear and pre-support the second cell; verify both cells afterwards. Double plants: do not assume the upper half is created automatically for every type (unverified).

## 4. Liquids and waterlogging

- Bucket places fluid in the empty block beside the targeted face, or replaces the targeted block for some replaceable blocks [24]. Since 1.20.80 there is no delay before re-collecting; a 10-tick delay existed in 1.20.40 Preview [24]. Source-vs-flowing result of a bucket: not stated on the page (unverified).
- Bedrock waterlogging is the layers system: a block occupies layer 0 and water layer 1; there is no `waterlogged` state in the Bedrock tables (the slab, fence, pane, wall, sign, candle pages list none) [21][25].
- Generally non-cube blocks can be waterlogged; "none can be used to displace water source blocks" (Java doors/pistons can) [25]. Doors in a source block become waterlogged and do not displace it [9].
- Placement in water fails for many plants and for torches, redstone dust, snow, bamboo, carrots, beetroots; cactus cannot be placed in water; candles cannot be placed underwater but can be waterlogged by other means; extended piston heads do not waterlog [25].
- Levers, repeaters and similar are placeable in flowing and source water and remove the flowing water they replace [25]. Rails can be placed underwater [5].
- Slabs: placing into a source or pouring water onto a slab waterlogs the empty half [21].
- Sea pickle uses `dead_bit` (true when no water) instead of waterlogged [11]. Waterlogged ladders cannot be climbed [3].
- Bubble column: forms 20 game ticks after soul sand (up) or magma block (down) is placed under a water source; only through water sources, stops at flowing or waterlogged blocks. Bedrock allows the item via inventory editing/add-ons; no normal placement [26].

Effect on a placer: the "exact state" for water-adjacent blocks lives in a second layer; verify with a water probe, not the state string. Do not expect to displace sources with doors or other non-cube blocks.

## 5. Multi-count blocks

| Block | State (Bedrock) | Increment by clicking |
|---|---|---|
| candle | `candles` 0-3 = 1-4 (Java 1-4); up to 4 of the same colour per space [27] | Not described (unverified) |
| sea pickle | `cluster_count` 0-3 = 1-4; `dead_bit` [11] | Not described (unverified) |
| turtle egg | `turtle_egg_count` one_egg..four_egg; `cracked_state` [12] | Eggs can be added to a cluster, keeping hatch progress; mechanism not described (unverified) |
| snow layer | `height` 0-7 = 1-8 layers; `covered_bit` [13] | Stacking works by placing; exact click mechanics not described (unverified) |
| slab | `minecraft:vertical_half`; double merge as in section 3 [21] | Matching slab merges as in section 3 |

A single snow layer is replaced when you place a block on it; two or more are not [13]. Snow layers can coexist with one-block plants but not two-block plants [13].

Effect on a placer: the target value is stored as count-minus-one; click the same block with the same item, then re-read the state to count clicks. Sneaking effect on increments: unverified.

## 6. Neighbour-derived shapes

Storage changed across 26.x versions; the pages say states were added in 26.40, removed in 26.40.30 and restored in Preview 26.50.22, and that from the restore on, shape changes trigger neighbour updates "for parity with Java"; worlds whose template `base_game_version` is below 1.26.50 keep the older client-only behaviour [28][29][30]. Check the server version before relying on any of these.

| Block | What Bedrock stores |
|---|---|
| fence | `minecraft:connection_north/south/east/west` booleans [28] |
| glass pane | Same four `minecraft:connection_*` booleans, marked "in development" for 26.50 [29]. Panes shown with fences as state-bearing in newer builds; older builds: client-side only (unverified). |
| iron bars | Not read (unverified) |
| wall | `wall_connection_type_east/north/south/west` = none, short, tall (Java `low`); `wall_post_bit` (Java `up`). Marked Unsupported (no aux) [31] |
| stairs | `weirdo_direction` 0=east, 1=west, 2=south, 3=north; `upside_down_bit`; corner state `minecraft:corner` (none, inner/outer left/right) removed in 26.40.30, restored 26.50.22; older worlds keep client-only corner updates [32] |
| chest | Only `minecraft:cardinal_direction` (before 1.20.40 Preview 20: `facing_direction`); no left/right state. Adjacent chests join into a large chest unless one is pushed in by a piston or faces a different direction. First-placed half owns the top three rows. Java lets sneaking stop pairing; this is not in the Bedrock list [33] |
| redstone dust | Only `redstone_signal`; no stored connection state is listed (likely client-side, not confirmed) [34] |
| rail | `rail_direction` 0-9 (0 N-S, 1 E-W, 2-5 ascending east/west/north/south, 6-9 curves). A rail with no neighbours is N-S (Java uses player facing); it reshapes to join neighbours and can reorient them [5] |
| vine | `vine_direction_bits` 0-15 (south 1, west 2, north 4, east 8; up not stored) [17] |
| glow lichen `multi_face_direction_bits`, chorus plant, tripwire | Not sourced (unverified) |
| leaves | `persistent_bit`, `update_bit`; no `distance` state; player-placed leaves never decay but the bit value is not stated [35] |
| double slab merge | See section 3 [21] |

Effect on a placer: do not assume any connection or corner state is directly settable; place neighbours first, then read the result back. A target state for a fence/pane/wall/stair corner depends on build order and server version.

## 7. Player-direction and click-derived states

Mojang traits (custom-block docs, but they describe the engine inputs) [18][23][36][37]:
- `minecraft:placement_direction` reads player rotation: `cardinal_direction` (south, west, north, east; yaw), `facing_direction` (down, up, south, north, west, east; "overall direction"), `sixteen_way_rotation` 0 (south) to 15 (yaw). The pitch mapping for up/down is not stated.
- `minecraft:placement_position` reads where it was placed: `block_face` (clicked face) and `vertical_half` (bottom/top, "where the player is looking"; exact threshold not stated).

| Block | State | Input as sourced |
|---|---|---|
| stairs | `weirdo_direction`, `upside_down_bit` | Faces the player; click on top or lower half of a side gives upright, click on bottom or upper half of a side gives upside-down [32] |
| slab | `minecraft:vertical_half` | Top face or lower side half gives bottom; underside or upper side half gives top [21] |
| trapdoor | `direction` 0-3, `upside_down_bit`, `open_bit` | Upper part of the clicked block gives top half; lower part gives bottom. Direction logic not described [38]. Preview 26.60 adds holding the place control to lock direction [38] |
| log / stem | `pillar_axis` x, y, z | Axis rule not stated for Bedrock (unverified); wood is "rotatable with the inner side facing the player" since v0.8.0 [39] |
| piston | `facing_direction` 0 down, 1 up, 2 south, 3 north, 4 east, 5 west | "Always placed facing toward the player" [22] |
| observer | `facing_direction`, `powered_bit` | Player-driven (not detailed) [14] |
| dispenser, dropper, furnace, barrel | Not read (unverified) | |
| chest | `minecraft:cardinal_direction` | Latch faces opposite the player [33] |
| glazed terracotta | `facing_direction` 2-5 | Opposite to player's facing [40] |
| anvil | `minecraft:cardinal_direction` | 90 degrees clockwise from the player's facing [41] |
| bed | `direction` | Head away from the player [10] |
| door | `minecraft:cardinal_direction`, `door_hinge_bit` | Faces nearest side; hinge: adjacent matching door's handle side gives double door; else side with more adjacent solid faces. Java's aim tiebreaker has no documented Bedrock equivalent; since 1.21.20 different-material doors default to double-door orientation [9] |
| repeater | `minecraft:cardinal_direction`, `repeater_delay` 0-3 | Value is opposite the player's facing; delay is ticks minus 1 [6] |
| lever | `lever_direction` 0-7 (8 values, not 12) | Top/bottom variants align with the player; chosen value not named [7] |
| button | `facing_direction` 0 down, 1 up, 2-5 N/S/W/E; `button_pressed_bit` | Clicked face [8] |
| torch / ladder / wall sign | see section 2 | Clicked face |
| standing sign | `ground_sign_direction` 0-15 (0 S, 4 W, 8 N, 12 E) | Faces the placing player; yaw mapping not detailed [4] |
| lectern, comparator, hanging sign | Not sourced (unverified) | |

Effect on a placer: the placer must control its own yaw/pitch (or the clicked face and hit position) at click time; for stairs and slabs the hit height on a side face decides top vs bottom, so click above or below the half line explicitly.

## 8. Placement edge cases

- Replaceable: tall grass is replaced by any placed block with no drop [19]; a single snow layer is replaced, multi-layer is not [13]; vines can be replaced by other blocks [17]; seagrass is removed by placing a block into it [16]. Water, lava, fire: not sourced for Bedrock (unverified).
- Sneaking: the Bedrock sources read do not document which blocks open a UI vs accept placement. The torch page says "some blocks require sneaking" [2]. Tutorial/sneaking pages were not retrievable (unverified).
- Reach: Bedrock block interaction range is 5 blocks in Survival and Creative with keyboard, mouse or controller; touch is 12 in Creative and 6 otherwise. Java is 4.5 Survival and 5 Creative. Server-side validation is not documented [42].
- `minecraft:placement_filter` as in section 2 [18].
- Design note from the 2026-10-27 colosseum research: its prior claims (repeater `cardinal_direction` names input side, piston values 2=+z, 3=-z, 4=+x, 5=-x) were not retested [14]; this note's sources agree on piston values by number (2 south, 3 north, 4 east, 5 west) [22] but repeater wording conflicts in direction naming, see open questions.

## Open questions to confirm by live calibration

1. Repeater `cardinal_direction`: wiki says "opposite the direction the player faces"; the colosseum note says it names the input side. Which sign convention, in placement-time terms?
2. Does repeated clicking on the same candle, sea pickle, turtle egg or snow layer increment, and does sneaking change that?
3. Do fence, pane, wall, stair-corner states read back through the server API on the target server version (26.50+ vs older)?
4. Stairs/slabs: exact hit-height threshold between top and bottom half on a side face; trapdoor direction rule; log `pillar_axis` rule; `vertical_half` threshold.
5. Which interactive blocks open a UI instead of accepting a placement click, and does sneak-click always place against them?
6. Do double-plant types (tall grass, large fern, rose bush, peony, sunflower, lilac, pitcher plant) create the upper half automatically, and is headroom required?
7. Bucket result (source vs flowing), liquids replacing which blocks, fire replacement, and what a second layer reads as via the state API.
8. Are redstone dust, rail, torch, carpet, pressure plate, sapling, sugar cane, cactus, kelp, banner, bell, chain, lantern, amethyst cluster, glow lichen, chorus, tripwire, iron bars support rules what the placer assumes? None were sourced.
9. Server-side reach and line-of-sight checks for a bot client.
10. Falling-block timing: how many ticks before a gravity block settles and a state can be trusted.

## References

All pages read 2026-10-28.
[1] https://minecraft.wiki/w/Falling_block
[2] https://minecraft.wiki/w/Torch
[3] https://minecraft.wiki/w/Ladder
[4] https://minecraft.wiki/w/Sign
[5] https://minecraft.wiki/w/Rail
[6] https://minecraft.wiki/w/Redstone_Repeater
[7] https://minecraft.wiki/w/Lever
[8] https://minecraft.wiki/w/Button
[9] https://minecraft.wiki/w/Door
[10] https://minecraft.wiki/w/Bed
[11] https://minecraft.wiki/w/Sea_Pickle
[12] https://minecraft.wiki/w/Turtle_Egg
[13] https://minecraft.wiki/w/Snow
[14] docs/research/2026-10-27-bedrock-redstone-colosseum.md (this repo); also https://minecraft.wiki/w/Observer
[15] https://minecraft.wiki/w/Pointed_Dripstone
[16] https://minecraft.wiki/w/Seagrass
[17] https://minecraft.wiki/w/Vines
[18] https://wiki.bedrock.dev/blocks/block-components (minecraft:placement_filter)
[19] https://minecraft.wiki/w/Tall_Grass
[20] https://minecraft.wiki/w/Pitcher_Plant
[21] https://minecraft.wiki/w/Slab
[22] https://minecraft.wiki/w/Piston
[23] https://learn.microsoft.com/en-us/minecraft/creator/documents/intro-block-traits
[24] https://minecraft.wiki/w/Bucket
[25] https://minecraft.wiki/w/Waterlogging
[26] https://minecraft.wiki/w/Bubble_Column
[27] https://minecraft.wiki/w/Candle
[28] https://minecraft.wiki/w/Fence
[29] https://minecraft.wiki/w/Glass_Pane
[30] https://minecraft.wiki/w/Stairs
[31] https://minecraft.wiki/w/Wall
[32] https://minecraft.wiki/w/Stairs
[33] https://minecraft.wiki/w/Chest
[34] https://minecraft.wiki/w/Redstone_Dust
[35] https://minecraft.wiki/w/Leaves
[36] https://wiki.bedrock.dev/blocks/block-traits (placement_direction, placement_position)
[37] https://learn.microsoft.com/en-us/minecraft/creator/documents/intro-block-traits
[38] https://minecraft.wiki/w/Trapdoor
[39] https://minecraft.wiki/w/Log
[40] https://minecraft.wiki/w/Glazed_Terracotta
[41] https://minecraft.wiki/w/Anvil
[42] https://minecraft.wiki/w/Interaction_range
