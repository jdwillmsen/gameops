# Bedrock redstone notes for the Colosseum generator

Target: Bedrock Dedicated Server 2026.9.x, arena emitted by a Python script using
`/fill`, `/setblock`, `/structure`. Researched 2026-10-27.

**Status of every design here: unverified on BDS.** The lead verifies in-world.

Trust levels: [W] = minecraft.wiki page text; [C] = community site/forum
(lower trust); [D] = my own derivation from [W] rules, not found in any
source. Where a source did not answer, this doc says "not found" instead of guessing.
Layout coordinates are all [D].

## 0. Conventions

- Origin `O=(0,0,0)`; +x east, +z south, +y up. Facing numbers for pistons:
  0=down 1=up 2=+z 3=-z 4=+x 5=-x (given; wiki agrees: 2 south, 3 north, 4 east,
  5 west) [W https://minecraft.wiki/w/Piston].
- Repeater `minecraft:cardinal_direction` names the input side (given).
- Sticky piston rule used throughout: piston base `p`, facing `d`. Retracted: the
  cell `p+d` holds a block `B` (pulled back). Extended: head fills `p+d`, `B` sits at `p+2d`.
  So the cell `p+d` is never empty; the "doorway" cell must be `p+2d` [D].

## 1. Bedrock vs Java differences that break designs

### 1.1 Quasi-connectivity
- Bedrock has none; pistons, dispensers and droppers need power on a face that
  actually touches them. The wiki's quasi-connectivity tutorial is labelled
  "exclusive to Java Edition and Legacy Console Edition"
  [W https://minecraft.wiki/w/Quasi-connectivity].
- Piston-door tutorial page: "since the Better Together update, quasi-connectivity
  has been removed on Bedrock, so many contraptions may no longer work"
  [W https://minecraft.wiki/w/Tutorials/Piston_doors].
- Bedrock-only: redstone dust beside a piston auto-points at it, so a piston
  connects to adjacent dust [W https://minecraft.wiki/w/Redstone_mechanics].
- Edition comparison also says Bedrock needs "Bedrock-compatible power connections"
  [C https://minecraft.linxi.com.au/java-vs-bedrock/redstone].
- Consequence: never power a piston from the block above/diagonal. Put a
  redstone block, torch, repeater output, or dust directly adjacent to the
  piston body (not its front face).

### 1.2 Tick model (P-tick / C-tick)
- A "redstone tick" = 2 game ticks (community convention). Bedrock splits it into
  an output phase (P-tick, odd game ticks) and an input phase (C-tick, even game ticks)
  [W https://minecraft.wiki/w/Redstone_mechanics].
- Pistons, lamps, doors and copper bulbs activate only on the input (C) tick;
  repeaters, comparators, torches, observers read input on the output (P) tick
  [W Redstone_mechanics; W https://minecraft.wiki/w/Copper_Bulb says bulb is C-tick only;
  W https://minecraft.wiki/w/Hopper says hopper locking takes effect on the C-tick].
- Wiki note: "In Bedrock Edition, block updates and redstone are not connected"
  [W Redstone_mechanics]. Redstone dust updates every game tick, so 1-game-tick pulses exist
  [W Redstone_mechanics].
- Piston starts moving 2 game ticks after activation and takes 2 ticks to finish;
  "Pistons can only activate on the input/consumer redstone tick"
  [W https://minecraft.wiki/w/Piston].

### 1.3 Piston update order / randomness
- Not found on the wiki pages read (the middle of the piston page was not
  retrievable). A comparison site says Bedrock "uses its own redstone event
  system and can resolve simultaneous piston actions differently" than Java's
  deterministic location-dependent order [C linxi, above]. A feedback thread
  suggests Mojang changed update order recently and that "locationality and
  directionality" complaints dropped, but this is a player comment and the
  details were not confirmed [C https://feedback.minecraft.net/hc/en-us/community/posts/43298646289677-MOJANG-FIX-BEDROCK].
- Practical rule: do not rely on the order in which two pistons powered by the
  same pulse start. Make mechanisms order-independent, or stagger them with
  repeaters/separate command pulses [D].
- Java-only tricks to avoid on Bedrock: zero-tick, sticky-piston block-spit with
  1-tick pulses, monostable "short pulse" tricks. Piston-door page: "many of the
  faster doors use Java-only zero-tick techniques" [W Tutorials/Piston_doors];
  sticky pistons "don't reproduce that behavior the same way" [C linxi].
- Bedrock-only "soft inversion": a torch placed on a (sticky) piston turns off
  when the piston is powered; do not place torches on pistons by accident
  [W https://minecraft.wiki/w/Redstone_Torch]. The wiki lists it as a Bedrock bug
  that "won't get fixed" (MCPE-17909) [W Piston page, unreadable detail].

### 1.4 Push limit, immovable blocks, slime/honey
- Piston moves up to 12 blocks; the page does not say this differs by edition
  [W Piston]. Keep every moving structure <= 12 incl. slime.
- Slime and honey do not stick to each other; the change landed in Bedrock beta
  1.14.0.2 [W https://minecraft.wiki/w/Slime_Block].
- Glazed terracotta and the heavy core (BE only) do not stick to adjacent slime or
  honey but can be pushed [W Piston]. Avoid glazed terracotta in moving panels.
- Anything adjacent to a moving slime block moves with it unless excluded.
  Leave air gaps around slime or the wall around the panel gets pulled [D from W Slime_Block rules].
- Bedrock item frames are blocks (not entities) and so interact with pistons
  differently than on Java [W Piston footnote]; keep item frames off moving parts.
- Entities on a moving honey block travel with it [W Piston].
- Obsidian, bedrock, barriers, command blocks, etc. are immovable: use stone
  or concrete for moving panels (full immovable list not re-read, check in-world) [W Piston].

### 1.5 Observers
- Bedrock observer = block update detector: "detects anything that causes a
  block update"; Java detects shape updates instead [W https://minecraft.wiki/w/Observer].
- Pulse is 2 game ticks at strength 15, aligned to redstone ticks; detection only
  on P-ticks (odd game ticks). An observer moved by a piston outputs immediately [W Observer].
- States: `minecraft:facing_direction` (down,east,north,south,up,west; default
  down) = direction watched; `powered_bit` (0x8) true while pulsing. Output
  leaves the back face (opposite of watched face) [W Observer; output side is general knowledge].
- Wiki's comparison table lists many cases where only one edition triggers
  (e.g. shulker box open/close). Re-test any observer trigger [W Observer].

### 1.6 Sticky piston retraction timing
- 2-tick start delay plus 2-tick travel on both extend and retract [W Piston].
- No Bedrock "spit block" on 1-tick pulses [C linxi]. Keep pulses >= 4 game ticks
  (>= 2 redstone ticks); a `/setblock` redstone_block held for seconds is far longer.

## 2. Flush / hidden piston mechanisms

Known facts:
- Wiki door table: BE-compatible entries are the 1x2 (8 blocks, "works on BE if
  redstone dust is added in the empty block"), a 2x3 (30 blocks), and a 2x2
  flush (240 blocks, "Modified Jeb door"); the 72-block 2x2 Jeb door and 126-block
  seamless flush have no Bedrock note; 3x3 doors (56/72/112/350 blocks) have no
  Bedrock note and many use Java-only zero-tick tech [W Tutorials/Piston_doors].
- The layouts for those doors are on linked subpages the fetch could not
  expand (`Modified_Jeb_door` 404). **Do not hand-derive a 2x2/3x3 flush door.**
  A community post says compact flush doors on Bedrock are much harder than on
  Java and often need extra space below the floor to power the bottom pistons
  [C https://feedback.minecraft.net/hc/en-us/community/posts/45220111740429-Quasi-connectivity].
- Tutorial pages: instructables/CurseForge describe 12-sticky-piston 2x2 flush
  doors; Bedrock status unconfirmed [C https://instructables.com/Simple-Flush-Piston-Door,
  https://blog.curseforge.com/how-to-make-a-secret-door-in-minecraft/].

Recommendation for 2x2 / 3x3: build one in a Bedrock creative world from a
Bedrock-verified video/schematic, save with a Structure Block, and place it with
`/structure load` from the generator (the file is the source of truth; the
generator only places it and supplies the power cell). Structure load keeps
block states [D, general feature]. Rotation/mirror options exist on load.

### 2.1 Design A: console-powered sliding wall panel (1 wide x 3 high), pistons visible in the wall plane
Wall runs along x at z=0, arena on the +z side, wall 1 thick, cells in the
wall plane. Doorway column at x=2, y=0..2.
```
 p   = (0,y,0), y=0..2   sticky_piston facing_direction=4 (+x)   [3 pistons]
 p+d = (1,y,0)           stone (B retracted) / piston head (extended)
 p+2d= (2,y,0)           doorway: empty when retracted, B when extended
 power: redstone_block at (0,y,-1) behind each piston (adjacent to body, not front)
        or one redstone_block line: place a single redstone_block at (0,1,-1) plus
        dust is NOT needed if all three bases touch a block of redstone each.
```
- Default retracted = open. To close: `setblock 0 y -1 redstone_block` x3 (or `/fill 0 0 -1 0 2 -1 redstone_block`).
  To open: `/fill 0 0 -1 0 2 -1 air` (or stone). Start state in the generator:
  pistons placed unpowered; B must be placed manually at (1,y,0) as stone, not at (2,y,0).
- Pistons' sides show in the wall plane at x=0 (visible from arena). Not flush-hidden.
  Cover with a decorative layer: move pistons to z=-1 and use slime (Design B).
- Bedrock-safe: yes in principle (direct adjacent power, no QC, no short pulse). Unverified.

### 2.2 Design B: slime-backed hidden sliding panel (flush face, hidden pistons)
Wall 2 thick: face layer z=0 (arena side), mechanism layer z=-1. Panel 2 wide
(x=2..3), 3 high (y=0..2) face blocks at z=0; slime behind them at z=-1.
```
 sticky_piston facing 4 at (0,y,-1)  y=0..2   (head slot (1,y,-1))
 slime_block at (1,y,-1) retracted / (2,y,-1) extended ... see note
 face blocks (stone) at (2,y,0),(3,y,0), stuck to slime at z=-1
 pocket: air at (4..5,y,0)  (panel slides +x into pocket)
 power: redstone_block at (0,y,-2)
```
- Problem: sliding in plane means the face blocks move 1 block per piston
  stroke, so a 2-wide panel opens only by 1 and needs a pocket matching the
  stroke; real sliding walls chain pistons or pull from the far side [D]. Also
  slime pulls every non-air neighbour (wall above/below at z=-1). Needs air gaps. 
  **Not recommended without in-world prototyping.** Pushing is capped at 12.
- Lowest-risk alternative: Design A with decorative stone around, accept visible pistons.

### 2.3 Design C: flush 1x2 door, vertical
Doorway cells `(0,0,0)`,`(0,1,0)` in a wall along x, plus the piston above.
```
 sticky_piston facing_direction=0 (down) at (0,4,0)?  
```
Only one-cell travel; instead use two stacked pistons going opposite ways:
```
 top    : sticky_piston facing 0 at (0,3,0)  head slot (0,2,0)  doorway cell (0,1,0)
 bottom : sticky_piston facing 1 at (0,-2,0) head slot (0,-1,0) doorway cell (0,0,0)
 B (stone) retracted at (0,2,0) and (0,-1,0) ; extended: B at (0,1,0) and (0,0,0)
 power: redstone_block at (1,3,0)? i.e. adjacent to top piston body, (1,-2,0) for bottom
```
- Wall thickness 1, the doorway is closed when pistons are extended, open when retracted (doorway shows open
  1x2 hole; the stone blocks parked at y=2 and y=-1 look like normal wall).
  Bottom piston sits below floor level y=0 (floor at y=-1 is B; pit is not needed).
- This is the 8-block-class idea the wiki lists for BE ("1x2, 8 blocks, works on BE
  if redstone dust is added", [W Piston doors]); my coordinates are [D], not the wiki's.
- Bedrock-safe: probable, only two pistons, direct power. Unverified.

### 2.4 Design D: rising / sinking floor section (3x3 or larger)
Floor plane y=0. Pit below. Pistons under each floor block:
```
 for each (x,z) in 3x3 at origin (0..2, 0..2):
   sticky_piston facing_direction=1 (up) at (x,-2,z)
   stone floor tile: retracted at (x,-1,z) (sunken, plane y=-1); extended at (x,0,z) (flush)
   head fills (x,-1,z) when extended
   power: redstone_block at (x,-3,z)  (below base, adjacent to body) - or one /fill
          redstone_block row beneath + keep rows 1 gap so they do not touch neighbours
```
- Rising floor: power -> sections rise to y=0 (flush); unpower -> sink 1 block, leaving a 1-deep recess.
- 9 pistons; they extend in an order not guaranteed [section 1.3]; harmless for a flat plate.
- Bedrock-safe: yes in principle. Unverified. 1-deep only; for a lethal pit use /fill air (section 3.4).
- Variant "pit trap floor": non-sticky piston facing 1 at (x,-2,z), head at (x,-1,z)
  as floor top ... piston head plate is flush with cell top when extended [D]; retract and
  the cell is empty above the base. Check height flushness in-world.

### 2.5 Design E: deep pit trap (non-piston)
- Deep pit (y -1..-8) covered by a floor layer, opened by a slab pushed sideways: one row of
  floor blocks at y=0 pushed by a horizontal piston; as computed in 2.1 the pocket must be
  empty and push moves only 1 cell per stroke, so one row = one cell per piston [D].
  Use one piston per floor cell (in a pocket cell on the flank), or the `/fill air` approach (3.4).
  Safer: build the trap floor from piston heads only (non-sticky pistons facing 1 every cell).

## 3. Triggers and gating

### 3.1 Console-driven power (recommended default)
- A redstone block placed adjacent to a piston is powering it; removing it
  depowers. `/setblock x y z redstone_block` then `/setblock x y z air` is a
  pure-command "lever" that needs no wiring, no QC, and no timing. `redstone_block`
  is block id 152, giveable; weak power 15 [W https://minecraft.wiki/w/Block_of_Redstone].
  Whether `/setblock` placement causes the piston's neighbour update on BDS: not
  stated in sources, unverified; first test in-world.
- Prefer a dedicated "power cell" per mechanism at a known coordinate:
  state ON = `redstone_block`, OFF = a plain block (e.g. stone) so that
  the cell is never a hole.
- Do NOT use a command block; none of these designs need one. If needed,
  command blocks are creative-only and immovable; not researched here.
- Pulse (open then auto close): bots run `setblock ... redstone_block`, wait N s, `setblock ... air`.
  Prefer this over redstone pulse circuits (more robust).

### 3.2 Observers
- Bedrock observer fires on any block update [W Observer]. A `/setblock`
  into the observed cell is expected to trigger it (replacing a block with a
  different block is a block update), but that is [D]; test in-world.
- BUD safety: since observers fire on all updates, an observer chain watching
  the arena can be fired by block changes you generate (floor swaps). Place observers
  so the observed face is a dedicated sensor cell, not the floor you rewrite; and use
  `/fill ... replace` carefully, as a replacement of identical block may or may not update.
- Moving an observer by piston fires immediately [W Observer].

### 3.3 Sculk sensor, target, daylight
- Sculk sensor: radius 8 spheres; 10-tick cooldown after placement/deactivation;
  stays active 30 ticks; output strength `max(1, 15 - floor(15/range*distance))`;
  wool/carpet blocks vibration line; Bedrock wool occlusion since Preview 1.18.30.23;
  sneaking mutes frequency 1-3 [W https://minecraft.wiki/w/Sculk_Sensor]. Bedrock
  state listed is `sculk_sensor_phase` (unsupported metadata; use `/setblock ... sculk_sensor` default).
- Good for "player stepped on arena floor" triggers; also fires from piston movement,
  so keep it away from pistons it controls (the wiki notes the 10-tick cooldown
  exists to avoid that feedback) [W Sculk_Sensor].
- Target block: snowballs etc. hold power 8 ticks; arrows/tridents 20 ticks; output 1-15 by
  hit accuracy [W https://minecraft.wiki/w/Target]. Bot-compatible if bots can shoot.
- Daylight detector: ids `daylight_detector` and `daylight_detector_inverted`; state
  `redstone_signal` 0-15; output refreshes once per second; no signal in Nether/End
  [W https://minecraft.wiki/w/Daylight_Detector]. Slow and global; not good for gates.

### 3.4 Command fallback (not redstone)
- For anything fragile (deep pit, large door): `/fill` the opening to air or stone, or
  `/structure load` a stored open/closed state. Deterministic, and can be
  scripted by the generator. Recommended whenever the mechanism is unverified.

## 4. Timed / sequenced mechanisms

- Repeater: `repeater_delay` 0..3 = 1..4 redstone ticks = 2,4,6,8 game ticks.
  Bedrock has separate ids `unpowered_repeater` (93) / `powered_repeater` (94), so
  `setblock ... repeater` only sets the unpowered one; do not place `powered_repeater`
  by hand [W https://minecraft.wiki/w/Redstone_Repeater]. Page also lists a `locked`
  state as upcoming in 26.70 [W same] (Bedrock lock has existed through the usual side-input method; not verified).
- Delay chains: put repeaters in series between a power cell and piston
  groups; each repeater adds up to 8 game ticks [W Repeater]. Stagger pistons by adding
  repeaters in front of each piston group for a rolling gate/curtain effect. Simple and
  deterministic [D].
- Lamp sequences: `redstone_lamp` (unlit) / `lit_redstone_lamp`; lamp turns on instantly and
  off after 6 ticks on Bedrock (4 on Java) [W https://minecraft.wiki/w/Redstone_Lamp].
  Do not `/setblock lit_redstone_lamp` expecting a stable lit lamp: it needs power
  to remain lit [D]; power it with a redstone block adjacent.
- Copper bulb: toggles state; Bedrock states `lit`, `powered_bit` (no metadata bits);
  comparator reads lit state (15 lit): a comparator + bulb gives a T flip-flop; C-tick only
  [W https://minecraft.wiki/w/Copper_Bulb]. Good for "pulse once = toggle lights"
  command-driven: setblock a redstone block adjacent, wait, remove, repeat (each rising edge toggles; edge detail not
  confirmed by source).
- Hopper clock: hopper cooldown is 8 game ticks on Bedrock; a powered hopper is locked and
  locking only applies on C-tick; states `facing_direction` (hopper values listed 0 down, 1 unused,
  2 north, 3 south, 4 west, 5 east, which differs from pistons) and `toggle_bit` 0x8
  [W https://minecraft.wiki/w/Hopper]. The page has no hopper-clock design; classic
  2-hopper loop with a comparator tap should work [D]; unverified on BDS. Needs items in hoppers.
- Crafter: 4 game tick delay after signal then ejects one item; states `crafting`,
  `orientation` (default north_up), `triggered_bit` [W https://minecraft.wiki/w/Crafter]. Not useful for gates.
- Observer clock: observer facing an observer pair is a 4-tick loop on Java; Bedrock
  edge cases differ [C linxi]; avoid. Clocks must be killable by a command (replace one block), always include a kill switch.
- Command-block-free: all designs here use a redstone block power cell.
- Any pulse < 2 redstone ticks might not register for pistons; use >= 4 game ticks.

## 5. /setblock syntax reference (Bedrock)

Block-state syntax: `setblock x y z minecraft:NAME ["state"=value,...]`
(strings quoted, ints/bools bare). Items marked [W] come from the block's wiki page; the rest are [D].

| Block | Example | Notes |
|---|---|---|
| piston | `piston ["facing_direction"=4]` | 0..5 as in section 0 [W Piston]; no `extended` state on BE |
| sticky_piston | `sticky_piston ["facing_direction"=1]` | placing via command does not extend it; powered later by neighbour |
| piston arm blocks | `piston_arm_collision`, `sticky_piston_arm_collision` | internal head blocks; never setblock them [D] |
| observer | `observer ["minecraft:facing_direction"="north","powered_bit"=false]` | watches the named face [W Observer] |
| repeater | `unpowered_repeater ["minecraft:cardinal_direction"="north","repeater_delay"=1]` | input side = named direction (given); delay 0..3; pre-1.20.30 states used `direction` [W Repeater] |
| redstone_block | `redstone_block` | [W Block_of_Redstone] |
| redstone_lamp | `redstone_lamp` (off) / `lit_redstone_lamp` | [W Redstone_Lamp] |
| copper_bulb | `copper_bulb ["lit"=false,"powered_bit"=false]` | also exposed_/weathered_/oxidized_/waxed_ [W Copper_Bulb] |
| crafter | `crafter ["orientation"="north_up","triggered_bit"=false,"crafting"=false]` | [W Crafter] |
| hopper | `hopper ["facing_direction"=0,"toggle_bit"=false]` | [W Hopper] |
| daylight_detector | `daylight_detector ["redstone_signal"=0]` | inverted id `daylight_detector_inverted` [W] |
| redstone_torch | `redstone_torch ["torch_facing_direction"="top"]` | values west,east,north,south,top; `unlit_redstone_torch` separate id [W Redstone_Torch] |
| sculk_sensor | `sculk_sensor` | state `sculk_sensor_phase` unsupported [W Sculk_Sensor] |
| slime_block | `slime_block` | |
| target | `target` | |

- Namespaces: `minecraft:` prefix is optional on BDS commands but include it in generated files for consistency [D].
- Verify each state name against BDS with `/testforblock` or by reading the block states after placing; versions
  rename states (repeater `direction` -> `minecraft:cardinal_direction` in 1.20.30) [W Repeater].
- `/fill` with `replace`: fills triggering updates is not sourced; test.

## Open questions for in-world testing
1. Does `setblock redstone_block` next to a placed piston extend it, and does removal retract it?
2. Do `/structure load` pistons arrive powered/extended and update correctly?
3. Does a three-piston stack extend simultaneously? Is the order stable?
4. Floor head height flush for non-sticky piston facing up.
5. Does `/setblock` into an observed cell fire an observer?
6. Sticky piston retract after a 4-tick power cell removal pulse.

## Recommended designs for the arena (ranked by robustness)

All entries: **unverified on BDS**.

1. Command-only openings (`/fill` or `/structure load` of open/closed state): no redstone at all, deterministic. (unverified on BDS)
2. Rising/sinking floor sections, Design D, sticky pistons facing up, per-piston redstone-block power cell below: no QC, no flush door complexity, direct power. (unverified on BDS)
3. Design A, sliding wall panel with visible in-plane pistons plus per-piston power cells; straightforward and decorable. (unverified on BDS)
4. Design C, 1x2 stacked-piston gate with two pistons and power cells. (unverified on BDS)
5. Large flush 2x2/3x3 doors: only via an in-world-built, Bedrock-verified structure placed with `/structure load`; hand-derived layouts not advised. (unverified on BDS)
6. Design B slime-backed hidden panel and deep piston pit traps: prototype first; high risk of slime pulling the wall. (unverified on BDS)
7. Sequenced lighting/curtains: repeater chains and copper bulbs driven by the same power cell; treat hopper/observer clocks as last resort. (unverified on BDS)

## Verified on BDS (2026-10-27, lab scratch pad)

| Test | Result |
|---|---|
| `/setblock` a `redstone_block` beside a sticky piston body | extends and pushes the block in front; removing the block retracts and pulls it back (answers open question 1) |
| 3x3 rising floor, 9 sticky pistons facing up, one `/fill redstone_block` row beneath | all 9 corners rise to the flush plane and sink again; no desync seen (Design D) |
| Single sticky piston, power cell behind the body | extends and retracts as expected (Design A building block) |
| Secret closet door (generator `secret_door`): two stacked sticky pistons, one dust cell on a block that touches the lower piston and beside the upper, lever up = shut | built at all four tunnels; lever down opens both plug cells, up closes them again (checked on the +x and +z doors) |
| Redstone torch as an inverter (support block lever-powered) | works; `torch_facing_direction` names the side the support block is on (`east` for a support to the east) |

The closet door is built extended (shut): the generator places the pistons powered with the plug in front of the empty head slot, which is the opposite of the retracted layout in section 2.1.

Still unverified: Design B, the 1x2 vertical door (C), observer triggering from `/setblock`, and `/structure load` of a piston in its extended state.
