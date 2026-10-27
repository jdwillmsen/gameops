# Bedrock redstone for the Colosseum

Date: 2026-10-27. Ticket JDW-245. Research only; no code, and nothing here was run on a server.

## 0. Status of every claim

- **Nothing in this document is VERIFIED-ON-BDS.** The brief forbids touching the live lab world and no scratch BDS was started, so every design below is **UNVERIFIED**.
- Facts the Colosseum README (PR #246) says were "checked on the lab server" (piston `facing_direction` 2=+z, 3=-z, 4=+x, 5=-x; repeater `cardinal_direction` names the input side; `redstone_wire` needs a block below) are carried over as **PRIOR-CLAIM**, not re-tested here.
- Section 6 is the test plan that turns each design into VERIFIED-ON-BDS. Build on a scratch world, never the lab.
- Wiki pages are community-edited. Where a page was only partly read, the gap is stated.

## 1. Bedrock vs Java: what the sources say

| Topic | Bedrock | Source |
|---|---|---|
| Quasi-connectivity | Not present. The piston page says it is Java-exclusive; the piston-door tutorial says it was removed in Better Together and that Java designs relying on it (the first two flush-door designs, the spacewalker quadruple extender with pistons underneath) do not work. A piston is powered only by a source it touches (or dust pointing at it), never by power aimed at the block above. | [Piston], [Piston doors] |
| Piston timing | Start delay 2 game ticks; pistons activate only on the consume tick; extend and retract each take 2 ticks. Push limit 12 blocks. If anything in the line cannot move, the piston does not extend. | [Piston] |
| Piston and power | Dust beside a piston is redirected toward it. A redstone torch on a piston soft-inverts (off while powered). | [Piston] |
| Slime and honey | Sticky blocks pull their non-diagonal neighbours. Slime and honey do not stick to each other. Glazed terracotta and heavy core can be pushed but do not stick. | [Piston] |
| Immovable | Obsidian, bedrock, barriers, end portal frames and many others. Some block-entity blocks (chests) break when pushed, others cannot be pushed. One search snippet claims Bedrock pistons can push tile entities; the wiki text read says chests break. Treat containers as not pushable. | [Piston], search |
| Observer | Detects anything that causes a block update (wider than Java). Outputs 15 for 2 game ticks. Aligned to redstone ticks, so it sees updates only on odd game ticks (P-ticks). A piston-moved observer always pulses at once (Java differs). States: `facing_direction` (down/up/north/south/east/west), `powered_bit`. | [Observer] |
| Sculk sensor | 8-block spherical radius; vibration travels 1 block/tick; active 30 ticks, 10-tick cooldown after. Output 1-15, stronger the closer the source; wool and carpet block vibration; an amethyst block beside it re-emits the frequency. Output is strong to the block below. `sculk_sensor_phase` is unsupported as a state in Bedrock. Mobs and players both trigger it. | [Sculk sensor] |
| Redstone ladders | Bedrock can build two-way vertical glass-and-piston ladders; Java glass ladders only go up. | [Redstone circuits] |
| BUD | The wiki describes BUDs for Java only. The Bedrock observer is the broader detector, so BUDs are rarely needed. | [Redstone circuits], [Observer] |
| Update and tick order | **Not obtained.** The pages read give no edition comparison. Do not rely on Java ordering or on "location-dependent" folklore; test any design that depends on two signals racing. | gap |
| Flush doors | Wiki lists BE-compatible: 1x2 seamless (8 blocks, with dust in the empty block), 2x3 seamless (30), a 2x2 flush (240, modified Jeb door). No BE-marked 3x3. Community feedback says compact flush doors are much harder on Bedrock and need space below the floor to power the lower pistons. | [Piston doors], [Feedback] |

Consequence for this build: because the console can place a `redstone_block` directly against a piston, the Bedrock lack of quasi-connectivity costs nothing. Every design below powers pistons by direct contact with a block the console places or removes, so it needs no buried dust, repeaters or levers (a lever can be added later as a manual override).

## 2. Console reference

### Placing

```
setblock X Y Z sticky_piston ["facing_direction"=3]     # 3 = faces -z
fill   X0 Y0 Z0 X1 Y1 Z1 redstone_block                 # power on
fill   X0 Y0 Z0 X1 Y1 Z1 air                            # power off (or the wall block)
structure save NAME X0 Y0 Z0 X1 Y1 Z1 disk              # <= 64 x 384 x 64
structure load NAME X Y Z 0_degrees none block_by_block 4
```

`structure load` takes the lower north-west corner; rotation `0/90/180/270_degrees`, mirror `none/x/z/xz`, optional animation `block_by_block` or `layer_by_layer` with seconds, and `integrity` 0-100 [Structure]. Requires cheats. The page has no BDS-specific notes.

Block states from Microsoft's block state list [BlockStates]: `facing_direction` is string-valued for buttons there, but the README confirms piston numbers; `observer` has `facing_direction` and `powered_bit`; `lever` has `lever_direction`; repeaters/comparators use `direction` 0-3 (0 south, 1 west, 2 north, 3 east) plus `repeater_delay` 0-3. The README says the generator uses `cardinal_direction` naming the input side for repeaters, which is how the current build works; keep using that.

### Rules for generated builds (design guidance, UNVERIFIED)

1. **Place pistons first, power last.** A piston set next to an existing power source may or may not extend on placement. Build everything with power absent, then place the redstone block as the last command.
2. **Never `setblock` an extended piston or an arm.** Let power extend it; the arm and the block entity are created by the piston.
3. **Do not `structure save` mid-motion.** Wait more than 10 game ticks after the last power change, with power off.
4. Keep every moving line at most 12 blocks including the pushed row and its travel. Use stone-family blocks for moving parts; avoid iron bars, glass panes, chests, banners and torches until pushability is checked.
5. Powered by contact: a piston base touches a `redstone_block` on any face except the front. Check that no other `redstone_block` or lit lamp neighbours a second piston.
6. Add a delay between power commands (a `sleep` in the feeder, or 5+ game ticks) so retract/extend completes (2+2 ticks) before the next change.
7. Anything that must work for crew units or the director should be a console command, because levers and buttons need a player (README).

## 3. Mechanism recommendations

Notation: `X` is the wall plane running along z (so walkers pass in the +/-x direction through the door cell); `y` is the floor-level row (64 in the Colosseum); `S` = sticky piston, `P` = normal piston, `R` = redstone block, `W` = wall block, `B` = movable plug. Coordinates are relative.

### M1. Hidden sideways-plug passage (1 wide, 2 high) - UNVERIFIED

A wall panel opens a secret doorway flush with the wall. Wall at least 3 thick in x (x = X-1, X, X+1), in the plane X. Door cell `D = (X, y..y+1, Z)`.

```
top view (y level), wall plane X runs along z; walkers go along x

 z=Z+2   W   S(3)  W       S at (X, y, Z+2), facing -z (3)
 z=Z+1   W  head   W       arm (retracted: W-coloured plug B pulled in here)
 z=Z     W   B     W       B in the door cell, flush with both faces
 z=Z-1   W   W     W
         X-1  X   X+1
```

- Closed: piston extended, `B` at `(X,y,Z)`, arm at `(X,y,Z+1)`. Open: piston retracts, sticky pulls `B` into `(X,y,Z+1)`, door cell empty.
- Build order: `B` and wall; `setblock X y Z+2 sticky_piston ["facing_direction"=3]` and the same at `y+1`; then power on the pistons' sides inside the wall, e.g. `fill X-1 y Z+2 X-1 y+1 Z+2 redstone_block`. A powered piston extends, so the default (powered) state is closed with this wiring; the console opens it by replacing the redstone block with wall (`fill ... stone_bricks`) and closes by `fill ... redstone_block`.
- The `redstone_block`s here sit in the wall face's thickness, so the outer surface stays the wall material. Use a 3-thick wall; the power block at `X-1` is behind the plane of the face only if the face is `X+1`.
- Alternative layout from the 1x2 seamless entry in [Piston doors]; compare after the first test.
- Risks: sticky piston leaving the plug behind (Bedrock "spit") when power is removed in the same tick the piston starts; mitigate with rule 6.

### M2. Sliding side-wall panel with shuttle pistons (3 wide, 3 high) - UNVERIFIED

A large panel in a gallery wall slides sideways into a niche, revealing a chamber. A normal piston at each end of each row pushes the row back and forth (no sticky blocks, no slime).

```
front elevation, 3 rows (y, y+1, y+2); panel cells p1 p2 p3, niche n1 n2 n3 to the left

   x:    A    n1 n2 n3 | p1 p2 p3 | C
 row y   P>   .  .  .  | B  B  B  | <P
 row y+1 P>   .  .  .  | B  B  B  | <P
 row y+2 P>   .  .  .  | B  B  B  | <P
```

- `P>` at the left end faces +x (4) and pushes the row right to close? No: left pistons push **right** and close; right pistons (facing -x, 5) push **left** and open into the niche. Only one side may be powered at a time.
- Open = power the right pistons; closed = power the left. Console: `fill A_power_col redstone_block` / `fill C_power_col air`, then swap. One `fill` powers the whole column of three if the power column touches each piston base.
- Row length plus travel is 3 + 3 = 6 blocks, within the 12 limit. A panel 5 wide with a 5-deep niche is 10 blocks, still within it.
- Both columns being powered at once is a deadlock (neither can extend). The generator should refuse to emit commands that power both.
- `B` is stone bricks or the wall block. Trim with a second `fill` of identical wall behind the niche, so the panel looks solid.

### M3. Split double gate, 5-wide sally port - UNVERIFIED

Replaces `fill ... air` on the four gates with a visible mechanism: two leaves meet at the axis and each slides into its pier. Same shuttle as M2, rotated. Per leaf: 3 rows of 3 blocks, a pusher behind and a retractor at the pier end. A 5-wide opening needs a 3-block leaf each side plus 1 overlap of 1 block; keep each row at most 12.

```
plan view of a gate on the z=const axis, doorway centre x = 0

  pier niche   leaf   |   leaf   pier niche
 n n n  [P>] [B B B] | [B B B] [<P] n n n
```

- The existing iron-bar leaf is not used; use bars only after verifying that iron bars move.
- Director opens with two `fill` commands on the power blocks; the old `fill air` stays as an emergency override.

### M4. Floor hatch and pit trap, shuttled under a lip - UNVERIFIED

A 3x3 piece of the sand floor slides under the surrounding floor, opening a pit. Same shuttle as M2 flattened onto the horizontal plane, one layer under the floor so the pit above is 1 deep, then fill it:

```
side elevation, floor y = 63, hatch cells h1 h2 h3, hidden niche under lip

 y 64  floor floor | .  .  .  | floor floor        (air above)
 y 63  floor floor | H  H  H  | floor floor        hatch row
 y 62  P>   n  n  n  |          |   <P  ...
```

- Open: pistons slide the 3 hatch blocks into `n n n` under the lip. Then `fill` water or lava into the pit, or just let mobs fall.
- Combined with the existing water lifts: a 1-block-deep water pit with a soul-sand base is a lift that can be exposed on demand.
- Caveat: entities standing on a moving block are carried with it. Open it as the encounter starts so units are not mid-hatch.

### M5. Lamp-ring chase from the console - UNVERIFIED

The wall-top ring already ripples on a repeater chain. For a faster fanfare, drive the ring in segments from the console: place a `redstone_block` under one lamp segment, wait, remove it, move to the next. Alternatives with no redstone: `lit_redstone_lamp` placed directly, or swapping lamps with `glowstone`/`sea_lantern`. Whether Bedrock lamps switch off instantly or after a delay, and whether a directly placed `lit_redstone_lamp` stays lit without power, was not found in the sources read; test both. The sculk-sensor option: a sensor in the floor lights a nearby lamp when a unit steps near it (output scales with proximity), but it fires for every mob, so use it for ambience only.

### M6. Boss intro (mostly non-redstone) - UNVERIFIED

Suggested sequence, all console-driven:

1. Close the gates (M2/M3 power swap), `playsound`, a `noteblock` fanfare on the royal-box button (exists).
2. `camerashake add @a 0.5 3` and `title @a title ...`, `time set`/`weather` if wanted.
3. Open the boss pen with the matching hatch (M4) while `particle` and `summon lightning_bolt` mark the spot. Lightning on a lightning-rod-less structure sets nothing on fire only if the area is non-flammable; keep sand and stone around it.
4. Lamp ring (M5) flashes red/white; sculk-style `particle` cloud for atmosphere.

Command names are standard Bedrock; syntax not re-checked for 1.26 here.

### M7. Timed self-closing gate - UNVERIFIED, lowest priority

Console should own timing (its own `sleep`). A purely redstone timer: button -> repeaters (up to 4 redstone ticks, i.e. 8 game ticks, each) -> pulse to the shuttle. Durations beyond a second or two need long repeater chains, which cost space and are fragile on Bedrock, so use only for a manual lever or button fail-safe so that a player in the arena can still exit: lever -> M2 open for a fixed time.

## 4. Mapping to the Colosseum

| Need | Mechanism |
|---|---|
| Four axis gates | M3 (replace portcullis fills) |
| Spawn pens (twelve 3x3) | M2 at 3x3, one per pen; keep `fill air` as override |
| Side wall panels | M2 |
| Secret passage from the hall to the plaza | M1 |
| Pit / flooded arena | M4 + water lifts |
| Fanfare, wall ring | M5, M6 |

Do not change the pens' "gate cannot leave the arena sealed open" check: `colosseum.py` asserts pens are sealed with gates shut. Piston gates must keep that invariant in their closed state.

## 5. Open questions

1. Do Bedrock sticky pistons pull a block when power is cut by replacing a `redstone_block` with `air` in one tick?
2. Which of these move on 1.26: iron bars, glass panes, trapdoors, lanterns, banners?
3. Does a piston extend on placement next to an existing `redstone_block` via `setblock`/`fill`, and does `structure load` re-trigger pistons?
4. Do lit lamps turn off instantly, and does a placed `lit_redstone_lamp` stay lit?
5. Update-order and BUD differences: not documented in the sources read.

## 6. Test plan (scratch BDS, never the lab)

Run a throwaway BDS 1.26.52.3 with a flat world and the same permissions. Per mechanism: build with `colosseum.py`-style commands, power on, power off, repeat ten times, record extend/retract count, whether blocks stay, and entity behaviour. Tag the result VERIFIED-ON-BDS with the date and BDS version, and file failures here. Questions 1 to 4 above should be answered first, since every design depends on them.

## 7. Sources

- [Piston]: https://minecraft.wiki/w/Piston (Bedrock sections; first 100,000 of 238,753 characters read)
- [Piston doors]: https://minecraft.wiki/w/Tutorials/Piston_doors (first 100,000 of 140,586 read)
- [Observer]: https://minecraft.wiki/w/Observer (first 100,000 of 250,707 read)
- [Sculk sensor]: https://minecraft.wiki/w/Sculk_Sensor (first 100,000 of 227,019 read)
- [Redstone circuits]: https://minecraft.wiki/w/Redstone_circuits
- [Structure]: https://minecraft.wiki/w/Commands/structure
- [BlockStates]: https://learn.microsoft.com/en-us/minecraft/creator/reference/content/blockreference/examples/blockstateslist (the page returned was the intrinsic block states list, dated 2024-09; may lag 1.26)
- [Feedback]: https://feedback.minecraft.net (search results; user opinion, not authoritative)
- Not obtained: a named Bedrock redstone creator's tested flush-door tutorial. The search found none that could be cited, so no creator is cited. The "Redstone circuits/Bedrock differences" wiki page returned 404.
