# crew

A programmable worker unit for Bedrock. Today it joins a server, tracks what
it believes about itself and serves that over a local status API. Design and
standing rules: `docs/specs/2026-10-14-crew-design.md`.

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `MC_HOST`, `MC_USERNAME` | required | Server, and the Microsoft account name (the display name when `MC_OFFLINE` is true) |
| `MC_OFFLINE` | `false` | Lab only: skip Microsoft sign-in and join with `MC_USERNAME` as a bare display name |
| `MC_PORT` | `19132` | RakNet port |
| `MC_VIEW_DISTANCE` | `8` | Chunk radius requested (1 to 32) |
| `STATUS_ADDR` | `127.0.0.1:8080` | Status API and camera listen address |
| `TASKS_TOKEN` | unset | Bearer token for the task endpoints; unset leaves them out entirely (404) |
| `CONTROL_URL`, `CONTROL_TOKEN` | unset | crew-control's base URL and its `WORK_TOKEN`; set both or neither. When set, the unit long-polls crew-control for tasks, stop and per-task `cancel_task` commands and reports their progress, with the contents of the last chest the task closed (dropped and resent once without them if crew-control answers 400 or 413). Unset, it takes tasks only from the Task API |
| `TASK_TIMEOUT_S` | `900` | Longest a task may run before it fails with `timed out` (30 to 3600) |
| `IDLE_WANDER` | `true` | A unit with no task looks about and takes short walks. `false` leaves it standing, though it still sends an input every tick |
| `IDLE_RADIUS` | `8` | How far from where it went idle a wander may stray, in blocks (1 to 32) |
| `DEFEND` | `true` | Fight hostile mobs, retreat at low health and from creepers, and step back from a player's blow |
| `EAT` | `true` | Eat food from the hotbar when hungry and not fighting (see Eating and rearming) |
| `CREW_ROLE` | unset | The unit's role: a warrior (`vanguard`, `striker`, `ranger`; see Squads and warriors) or a builder (`mender`, `builder`; see Mender and builder roles); anything else is refused, unset leaves the reactive defence in charge |
| `CREW_LAB` | `false` | Must be `true` for a `mender` or `builder`, which refuse to start without it; the lab chart or compose sets it, the live server's never does |
| `CREW_SQUAD` | `alpha` with a warrior role | The id of the squad a warrior joins through crew-control, lowercase like `alpha` or `bravo_2`; read but unused without one |
| `KIT_CHEST` | unset | `x,y,z` of a chest to rearm from; unset leaves rearming off |
| `KIT_FOOD` | `8` | Most food items one visit to the kit chest takes (1 to 64) |
| `WORK_PLAN` | unset | Standing work: a JSON array of 1 to 8 task bodies, as the Task API takes them, run in rotation whenever the unit has nothing queued or running. A failed step is skipped and the next one waits 30 s. Work from the Task API or crew-control cancels a running plan task and goes first. Stopping a plan task (a crew-control stop or a Task API cancel) pauses the plan for 10 minutes |
| `JOIN_DELAY_S` | `0` | Seconds to wait before the first connect (0 to 600), so a rollout of many units staggers its logins |
| `CAMERA_ENABLED` | `false` | Serve the rendered camera |
| `CAMERA_MAX_VIEWERS` | `4` | Concurrent camera viewers (1 to 64) |
| `CAMERA_ATLAS_DIR` | `/data/atlas` | Where the built texture atlas is cached; put it on the data volume. Units can share one directory: the first builds, the rest wait for it. The resource pack it is built from is fetched into `/tmp` and removed after the build |

## Idle life and defence

A unit never stands silent. With no task it sends one input per 50 ms tick
like a real client, so the server's knockback and corrections reach its
predicted pose. With `IDLE_WANDER` it also glances around every second or two
and, after a pause of 2 to 8 s, walks 2 to 6 blocks to a random spot within
`IDLE_RADIUS` of where it went idle. Walks use the pathfinder over known,
loaded blocks, keep the whole route inside the radius on safe floor (never
water, lava, ice or a ledge) within 2 blocks of the starting height, and never
break or place anything. A walk knocked outside those bounds stops, and a unit
pushed out of them takes its new spot as the centre. A task
submitted while idle cancels the walk at once.

With `DEFEND` the unit watches the entities the server announces:

- A hostile mob within 6 blocks (zombie, husk, drowned, skeleton, spider and
  so on), or one that just hurt the unit, is faced, approached and hit about
  every 10 ticks with the best hotbar weapon (sword, then axe, then a tool,
  then a fist). Neutral mobs such as an enderman are fought only after they
  hurt the unit.
- At 6 health (3 hearts) or less, and from a creeper within 5 blocks, the unit
  walks away instead. A retreat that cannot shake the threat for 10 s, or has
  nowhere to go, turns into a fight.
- A player is never attacked, and neither are passive mobs. A player's blow
  makes the unit face the player and step back.
- It does this during a task too: the running skill is stopped (closing a
  chest, aborting a break), the unit fights, and the skill then resumes from
  its start with its progress kept. Fight time does not count toward
  `TASK_TIMEOUT_S`.

Logs carry only events and types (`defend_started`, `hurt_by`,
`defend_ended`), never positions. `defend_ended` carries `ticks` and a
`reason`: `gone` (the mob left range or died), `no_stand_cell` (no standable
cell near it), `no_path` (the chase failed) or `fight_limit`.

A warrior also logs `hit_landed` (the swung-at target's type and the fight's
running hit count), `damage_taken` (the attacker's type as the unit could
tell it, the amount from the health attribute when one arrived, and the
health left) and `warrior_died` (the last attacker and the fight's length in
ticks). Its `defend_ended` adds `swings`, `hits`, `damage_taken` and
`deaths` for the fight. The totals are `crew_swings_total`,
`crew_hits_landed_total`, `crew_damage_taken_total` and
`crew_deaths_total{killer}`, and `/v1/state` carries them as `tasks.fight`.
A hit is the target's hurt animation, or its death, within a few ticks of a
swing, or while an arrow flies. The packets name no attacker: the attacker is
the nearest mob the unit would blame, and a teammate's blow in that window
counts as the unit's own hit, so with several warriors on one target the hit
count and rate are an upper bound.

## Squads and warriors

With `CREW_ROLE` set the unit fights as a warrior of that role — `vanguard`
(holds the front behind its shield, leads the squad), `striker` (flanks and
lands critical hits mid-fall) or `ranger` (bows from distance and kites) —
and never as anything else: players are never targeted. The engine shares
one focus target across the squad by rule, not by message: every unit
commits to the lowest-health threat within 12 blocks of the leader, ties
broken by entity id, and only a creeper about to blast or a player it must
give way to overrides it. A hurt warrior eats a golden apple behind the
line, regroups on its leader when pressed, calls out (`low_health`,
`need_heal`, `target_down`) for the squad's benefit, and an operator's
standing order (`focus|hold|advance|regroup|retreat`) steers all of them.
Design: [`docs/crew-squads/docs/specs/2026-10-23-crew-squads-and-arena-design.md`](../../docs/crew-squads/docs/specs/2026-10-23-crew-squads-and-arena-design.md).

`CREW_SQUAD` names the squad the warrior joins, `alpha` by default. Joining
goes through crew-control over the same `CONTROL_URL` and `CONTROL_TOKEN`
as the work client (no separate URL exists): the unit hellos its claim,
reports its `hp`, `food`, held item and feet position from belief every
couple of seconds, and long-polls the standing order and the leader. The
chain of command leads through the fittest member — Vanguard, then Striker,
then Ranger — and a leader that dies or goes silent for 8 s hands over. A
warrior without `CONTROL_URL` still fights, but leads itself and hears no
orders, and says so in one `squad_off` log line.

## Mender and builder roles

`mender` and `builder` are not warriors: they take no warrior engine, say no
squad hello and ignore `CREW_SQUAD`, and for now live the ordinary idle,
defence and eating life. They work in creative mode, which is applied from
the lab console, not by the unit, so they are lab-only: with either role the
unit refuses to start unless `CREW_LAB=true`. The lab's chart or compose sets
it; the live server's never does. Nothing is probed on the network, the
variable is the whole guard.

The role reaches crew-control and the console through the status API, not a
squad hello (the squad API refuses builder roles): `/v1/state` carries
`unit.role`, and crew-control's poller keeps it only if it is one of
`vanguard`, `striker`, `ranger`, `mender` or `builder`.

## Water

Water is not solid to the unit's body, but planned paths still never enter or
cross it. The body follows the server's swimming rules, measured on the lab:
weak steering (0.02 per tick), a fifth of the speed lost each tick, a sink of
0.005 per tick, and swim-up of 0.04 per tick while jump is held, which only
works with more than 0.4 blocks of water over the feet. The surface stands
8/9 of a block up, so a unit at the top bobs in and out of the water. A
unit pushing on a block edge not more than 0.6 above its feet is kicked up
0.3 per tick onto it.

In water the unit swims out before it does anything else, even in a fight or a
task, which resumes afterwards. It finds the nearest shore it can stand on
level with the surface (a bank a block higher is out of reach: the body is
kicked up only 0.6 over its feet, and the lab showed the server does not lift a
swimming unit onto one either), rises until it would coast to rest under the
surface and swims along the water to it. A unit that gets no closer to its
shore for 2 s gives that shore up and takes another. Logs carry
`swim_started`, `swim_ended` (with `ticks`) and `swim_stuck`, which carries
`reason` (`no_shore`, or `stalled` for a shore given up), `ticks` and the
unit's block position `x`, `y`, `z`. With no shore at all the unit keeps
treading water and `swim_stuck` is logged once.

Known limits:

- Breath is not modelled. A unit that cannot get out drowns on the server;
  `swim_stuck` is the only sign of it.
- `flowing_water` is taken as a full block of still water. Flow depth and the
  push of a current are ignored, so rivers and waterfalls rely on the server's
  corrections.
- Waterlogged blocks are not seen as water: they live on the block store's
  second layer, which it does not read.
- Entering water from a slow fall can be corrected once; the surface, where
  the server's rules differ most, is avoided by riding just under it.

## Eating and rearming

A unit that died fights barehanded and never heals: on normal difficulty
health only regenerates while food is at 18 of 20 or more. With `EAT` a unit
that is not in a fight and holds food on its hotbar selects it, uses it for
the 32 ticks a meal takes and lets go. It eats from 17 food when hurt and from
14 when not, preferring the richest food (steak, then bread, then lesser
foods); rotten flesh, raw meat and anything that poisons or teleports are
never eaten, and neither is food outside the hotbar. The running skill is
stopped for the meal and resumed after it, and meal time does not count toward
`TASK_TIMEOUT_S`. A fight cuts a meal short. Logs carry `eat` and `eat_failed`
with the food and hunger only; a meal that does not land is not retried for a
minute.

With `KIT_CHEST` set, a unit that has had no sword or axe, or no food, on its
hotbar for five seconds, and is not running a task or fighting, walks to that
chest and takes the best sword it holds and up to `KIT_FOOD` food. This is how
a unit that died gets its sword and bread back. A chest that cannot make the
unit whole is logged as `kit_empty` (with what was `missing`, never a
position) and left alone for three minutes; a failed visit (`kit_failed`) backs
off the same way, except one that found the chest's chunk not yet loaded, or
one whose open click was answered with no window, as on a cold join, which are
retried after five seconds, up to six times in a row before backing off. Each
further failure in a row doubles the back-off, up to 15 minutes, and a visit
that completes starts it over. A unit more than 96 blocks from the chest does
not set out at all: the walk leaves the loaded chunks and outlasts the visit
limit. A timed-out open logs `chest_open_clicks` with `dist` (blocks from the
unit to the chest) and `above` (the block over it). A visit only fills empty hotbar slots, so a tool is never
displaced, and a unit whose hotbar is full does not go; a visit that fills
the hotbar before taking everything it needed logs `kit_no_room` instead of
`kit_empty`. The task queue waits while a visit is under way.

To use it, set the same two variables on every unit in the deployment's
environment, for example in the Helm values that carry `DEFEND`:

```
KIT_CHEST=-3,100,-3
KIT_FOOD=8
```

`parallel-scene.sh` builds and stocks a chest at the position in
`lab/parallel-layout.sh` (`KIT_CHEST`, six stone swords and 64 bread), which is
what to set `KIT_CHEST` to on the lab's units. The scene empties the units'
inventories, so with the kit on they walk to the chest once idle for five
seconds; a task submitted sooner runs first, barehanded.

## Movement and block shapes

The unit predicts its own movement and sends the result with every input; the
server accepts it or corrects it, and `crew_movement_corrections_total` counts
the corrections. Each correction is also logged as `movement_corrected`, at
most one line per unit per 10 s with a `suppressed` count, carrying the skill,
movement state and block names around the feet but no positions; the query
that groups them is in
[`docs/notes/2026-10-21-crew-corrections.md`](../../docs/notes/2026-10-21-crew-corrections.md).
**Clipping.** A unit whose predicted body sits more than 2 cm inside a block
shape, or whose predicted tick the server corrects, is counted in
`crew_movement_clips_total{reason}` and logged as `movement_clip` (names of the
blocks at the feet, never positions; one line per reason per 10 s with a
`suppressed` count). The reason is a closed code:

| Reason | Meaning |
| --- | --- |
| `predicted_inside_block` | a tick's predicted body ended inside a block; a stay inside counts once |
| `corrected_inside_block` | the server corrected a tick whose predicted body was inside a block: the prediction ran through something solid |
| `corrected_in_free_space` | the server corrected a tick whose body was clear of every block: a shape, speed or entity mismatch, not a clip |

`internal/act/conformance_test.go` holds the collision table, a swept-box
reference written independently of the sweep, and seeded random worlds; a
change to the collision code runs against all three.

The prediction collides with each block's real shape, which
comes from the block's name and states (`internal/world/shape`), not from a
full cube per solid block:

| Block | Collision |
| --- | --- |
| Slab | half a block, bottom or top; a double slab is a cube |
| Stairs | the half slab plus the step, by facing, half and corner state |
| Fence, wall, fence gate | 1.5 high, so nothing jumps them; fences and walls follow their `connection` states, a gate is 0.25 thick across its facing and has no collision when open |
| Carpet | 1/16 high |
| Snow layer | `height / 8` high, so a single layer has no collision |
| Door, trapdoor | a panel 0.1825 thick on the side its facing and open state (and a door's hinge) give |
| Ladder | a panel 3/16 thick; a unit that presses into it climbs 0.2 a tick, and one that does not slides down at most 0.15 |

Door and trapdoor thickness, the snow rule and the ladder rates were measured
on the lab; the other boxes match the block table (a test checks them against
every state it knows). A block the unit has not been told about, or one that is
not in `shape`, is a full cube unless the binary's block table knows it: any
other block takes its boxes from dragonfly's block models (chests are 0.95
high, a path 0.9375, an enchanting table 0.75, plants and signs have none),
except liquids and blocks that hurt, and a block whose model needs its
neighbours stays a cube. Where the table is wrong, `shape` has the height the
lab measured (soul sand and mud 0.875, a daylight detector 0.375, a trapped
chest 0.95, the floors of a cauldron, composter and hopper).

A grounded unit that is blocked retries from 0.6 higher and keeps that if it
carries it further, which is how it steps onto a slab, a stair or a carpet
without a jump. The step tick reports the standing vertical delta: the server
reads the delta as velocity and corrects one that carries the rise.

The pathfinder walks the same shapes. It slides the unit's footprint across
each move in eighths of a block, so a half block or a stair is a walk, a block
and a half is a wall and a full block takes a jump, and `NeedsJump` tells the
navigator which waypoints to jump for. A replan starts from where the unit
really stands, not its cell's centre, so a unit that brushed a fence post or a
gate can plan its way out; a diagonal move needs both cells beside it open.
Ladders are collided with and climbed
when the unit walks into one, but the planner does not route up them.

Descending a flight of stairs is the known weak spot: the unit falls half a
block at a time, and the server's landing and take-off ticks differ slightly
from the prediction, which costs about one small correction per flight.

## Status API

`/healthz`, `/readyz` and `/v1/state`. The state carries `position`,
`position_known`, `yaw` and `pitch`, which follow the unit's own walking as
well as the server's corrections and teleports. Until `position_known` is
true the position is the server's placeholder, not a real one. It also carries
`health`, `hunger`, the 36-slot `inventory`, `held_slot` (only meaningful when
`held_known`), and `effects`: the status effects the server has put on the
unit and not taken back, each with `id`, `name`, `amplifier` and `duration`
in ticks (negative means it does not expire; a logout forgets them all), and
`unique_id`, the signed decimal id the server gave the unit's player, which
is the one its record in the world save is filed under. It
also carries a read-only `tasks` object: `current` (the running task or
null) and `recent` (the last five others, newest first), each with id,
template, state, skill, item, qty, progress, reason and timestamps. It never
carries the task token, and the task routes below stay token-guarded.

## Task API

Off unless `TASKS_TOKEN` is set. Every request needs
`Authorization: Bearer <TASKS_TOKEN>`; a missing or wrong token gets 401.

- `POST /v1/tasks` queues a task and returns 202 with its record.
- `GET /v1/tasks` lists queued, running and the last 50 finished tasks.
- `POST /v1/tasks/{id}/cancel` cancels a queued or running task (404 unknown, 409 already finished).

These templates exist; any other template, item or field is a 400. Each field
is required unless marked optional.

`gather_and_deposit` mines an item and puts it in one chest:

```json
{"template": "gather_and_deposit", "item": "cobblestone", "qty": 16,
 "chest": {"x": 0, "y": 0, "z": 0}}
```

`qty` is 1 to 256 and `item` is `cobblestone` (mined with a pickaxe) or
`oak_log` (chopped with an axe the unit must carry). Oak logs are felled whole
trees at a time: for logs above its reach the unit pillars up beside the trunk
with dirt (or cobblestone, with a pickaxe) from its hotbar, cuts, climbs down
and takes the pillar apart. The pillar needs one hotbar stack holding at least
as many blocks as the pillar is tall; without one, the unit only takes logs it
can reach from the ground.

`area` is optional: an inclusive box `{"min": {x,y,z}, "max": {x,y,z}}` with
min at most max on each axis and at most 64 blocks on an axis. When set, the
unit only ever breaks blocks inside it and fails with `no reachable <block> in
the area` if none is left. Without it, the unit may break any matching block
within 24 blocks, so set it wherever other blocks of that kind matter.

`haul` empties source chests into one destination chest, waiting for items to
arrive and making as many trips as the inventory needs. It ends when every
item has been moved the requested number of times:

```json
{"template": "haul", "items": {"cobblestone": 16, "oak_log": 16},
 "from": [{"x": 0, "y": 0, "z": 0}], "to": {"x": 0, "y": 0, "z": 0}}
```

`items` lists 1 to 8 items from the same set, each 1 to 256. `from` lists 1 to
4 chests. A haul only takes into empty inventory slots or onto stacks of the
same item, so tools stay put, and it fails after three attempts in a row that
move nothing. Its `progress` is the total moved and its `qty` the total asked
for. The destination must not also be a source and a source may be listed once.

`withdraw` moves items out of one chest into the unit's inventory and `deposit`
moves them back in, each for an exact count of named items. Unlike haul they
take any server item name, not just the craftable set, so a unit can be sent to
fetch a tool:

```json
{"template": "withdraw", "chest": {"x": 0, "y": 0, "z": 0},
 "items": {"cobblestone": 16, "oak_log": 16}}
```

`items` lists 1 to 8 items, each 1 to 256, and `chest` has the same bounds as
`goto`'s `to`. A withdraw only fills empty inventory slots or onto stacks of the
same item, so tools stay put, and takes up to each count: a chest short of an
item ends the visit with what it had. A deposit fails naming the item when the
inventory holds less than asked.

`mine_shaft` digs a staircase down from an entrance, tunnels branches off its
bottom, climbs out and deposits everything it gained in one chest. It needs a
pickaxe on the hotbar and is lab-only by construction:

```json
{"template": "mine_shaft", "entrance": {"x": 0, "y": 0, "z": 0}, "facing": "north",
 "depth": 12, "branches": 2, "branch_length": 8,
 "area": {"min": {"x": 0, "y": 0, "z": 0}, "max": {"x": 0, "y": 0, "z": 0}},
 "to": {"x": 0, "y": 0, "z": 0}}
```

A unit knocked somewhere with no walking path out jumps and places cobblestone
under itself, up to 8 blocks. Under a roof two blocks above its feet it cannot
rise far enough: the climb then fails with `no headroom to jump and place a
block`, and the `climb_place_failed` event carries the roof's block name as
`above`. Digging up through the roof is not done yet.

`entrance` is the open ground cell the unit stands in; `facing` is north
(negative z), south, east or west; `depth` is 1 to 32 steps down, one block per
block forward. `branches` is 0 to 3 tunnels off the bottom (ahead, left, right)
of `branch_length` 1 to 32, and is absent when there are none. `area` and `to`
are required, and a plan with any cell outside `area` is rejected when
submitted. A cell is left closed when lava, water, magma, an unloaded block or
no floor is next to it: the heading is dropped, logged as `mine_hazard`, and the
unit carries on with the next. A shaft blocked at its first step fails the
task. Tunnels are not lit.

`goto` walks the unit to within a block and a half of a cell.
`to` is bounded to the lab's flat square: x and z -500 to 499, y -64 to 320:

```json
{"template": "goto", "to": {"x": 0, "y": 0, "z": 0}}
```

`inspect` walks within reach of one chest, opens it, records what it holds and
closes it, taking nothing. `chest` has the same bounds as `goto`'s `to`:

```json
{"template": "inspect", "chest": {"x": 0, "y": 0, "z": 0}}
```

`observe_blocks` walks the unit to a spot it can stand on with the whole box
within four chunks, and reports every block in it from its own world view, so a
finished build can be checked against its plan. Any view distance of 4 or more
streams the box; one longer than about 128 blocks fails as too big to read
whole. `box` corners have the same bounds as `goto`'s `to`, min is
at most max on each axis, and the box holds at most 4096 cells:

```json
{"template": "observe_blocks",
 "box": {"min": {"x": 0, "y": 64, "z": 0}, "max": {"x": 4, "y": 67, "z": 4}}}
```

Unreadable cells — a sub-chunk the server never sent, or a block the unit's
table does not name — are reported as `unknown`. The report travels as one
control event on the task's terminal state: cells are run-length coded against
a palette of block names, and a box whose report cannot fit fails with
`observation too large`, so observe one layer or a smaller box instead. The
observation goes only to the controller, never to the public task record.

`build_blueprint` stamps an embedded blueprint (see Crew blueprints) the way a
builder would: it walks within reach of each cell in build order, breaks a wrong
block, and places the right one from the creative inventory. Only a unit whose
`CREW_ROLE` is `builder` takes it; any other gets 409. The unit must be in
creative mode, or the task fails (a `dry_run` needs no mode):

```json
{"template": "build_blueprint", "blueprint": "small_house",
 "at": {"x": 0, "y": 64, "z": 0}, "rotation": 90, "dry_run": false}
```

`blueprint` is a file name in `blueprints/` without `.json`. `at` is the
minimum corner of the footprint after rotation. `rotation` is 0, 90, 180 or 270
clockwise and optional, as is `dry_run`. Both corners of the footprint must lie
inside `goto`'s bounds and no axis may exceed 64 blocks. A cell that already
holds its block is left alone, so running a stamp again places nothing. Air
cells are not cleared. `qty` is the blocks to stamp and `progress` those the
world shows right.

Placement takes its facing from where the unit clicks, so a block whose states
cannot be asked for by item name (stairs, doors, repeaters, wall torches) is
never placed: it is left out and counted under `skipped_state` in the task
record's `build`, by block name. The exceptions are blocks a click on the top
face decides: a standing torch is placed only where the
world can read the state back, and is broken again if it came out otherwise.
A block with no item of its own name is counted under `skipped_item`. `build`
also holds `total`, `placed`, `already_correct`, `cleared` and, for a `dry_run`,
`to_place`; it names blocks and counts, never cells. The report is on the task
record only, not in crew-control's events. A cell with no solid neighbour yet is
tried again after the others, then fails the task naming only the block.


`repair_build` is for a `mender` unit in creative mode (any other role gets
409). It diffs an area of the larger Colosseum against the generator's plan
from the blocks the unit has loaded, then restores it: it breaks what should be
air and places the planned block, in the console planner's order. `area` is an
inclusive box, at most 64 blocks on each axis, wholly inside the Colosseum's
build box (the lab square `goto` uses stops short of it, so that box is the
bound); `plan` is `colosseum`; `dry_run` is optional and only reports:

```json
{"template": "repair_build", "plan": "colosseum", "dry_run": true,
 "area": {"min": {"x": -320, "y": 60, "z": -770}, "max": {"x": -300, "y": 70, "z": -750}}}
```

The result is one line on the finished task's `reason`: damaged cells by kind
(`missing`, `intruder`, `replaced`) and by zone, worst first, then the top
changes; a real run leads with what it repaired and ends with what is still
wrong. Cells the unit has no chunk for are never touched. Blocks with no item,
and blocks that are right except for a state a click cannot set (a stair's
facing), are left and counted. A task fails, naming `arenarepair`, when the
damage is over 1000 cells,
and while a hostile mob or a player outside the crew names is within 32 blocks.
The mender must start within walking distance, on loaded chunks.

Crew runs one task at a time. A session end or death fails the running task
(`session ended`, `unit died`) and queued tasks wait for the next spawn.
Bodies are capped at 4096 bytes (413) and at most 16 tasks may wait (429).
Task logs carry the id, template, state and reason, never the request.

**Failure reason codes.** A failed task also carries `reason_code`, a closed
code for the free-text `reason`, which stays as it was. Numbers inside a
reason (`status 6`, `after 3 breaks`) do not change the code, so failures group
and chain without parsing strings. It is on the `task_finished` log line, in the
task record, and on the report to the controller; a controller that rejects
unknown fields gets the report again without it. `kit_failed` carries the same
field. `session_id` (random per connection) is on `task_finished`,
`kit_failed`, `died`, `respawned`, `session_error` and `session_ended`.

| Code | Meaning | Example reason | Raised by |
| --- | --- | --- | --- |
| `missing_tool` | the tool a skill needs is not held | `no axe in inventory` | mining skills |
| `kit_chunk_not_loaded` | kit chest's chunk not loaded yet | `the chest's chunk is not loaded` | kit visit |
| `kit_no_path` | no way to the kit chest | `getting to the chest: walking to the goal: no path` | kit visit |
| `kit_chest_timeout` | chest never opened | `chest did not open within 300 ticks` | kit visit, chest skills |
| `kit_visit_timeout` | the whole visit overran | `visit took too long` | kit visit |
| `session_lost` | connection ended mid-task | `session ended`, `wire down` | runner |
| `deposit_rejected` | server refused a chest transfer | `server rejected the deposit with status 6` | deposit, withdraw |
| `no_pickup` | breaks yielded no item | `no pickup after 3 breaks` | mining skills |
| `no_reachable_target` | nowhere to stand within reach | `no standing spot within reach of the chest` | chest, place, observe |
| `trap_climb_failed` | could not climb out of a trap | `climbing out of a trap: ...` | climb |
| `inventory_desync` | inventory never showed the transfer | `inventory did not show the deposit within 40 ticks` | deposit, withdraw |
| `chunk_not_loaded` | target chunk not loaded | `the chest's chunk is not loaded` | chest skills |
| `no_path` | the pathfinder found no route | `haul gave up after repeated failures: ...: no path` | any walking skill |
| `other` | anything not listed | `block did not break` | the fallback |

A reason that matches no rule is `other`; the rules are one table in
`internal/task/reason.go`, checked by a test.

```
curl -H "Authorization: Bearer $TASKS_TOKEN" \
  -d '{"template":"gather_and_deposit","item":"oak_log","qty":16,"chest":{"x":-4,"y":100,"z":0}}' \
  localhost:8081/v1/tasks
curl -H "Authorization: Bearer $TASKS_TOKEN" localhost:8081/v1/tasks
```

## Rendered camera

With `CAMERA_ENABLED=true`, crew streams the chunks, entities and its own look
direction it already receives to a browser page that draws the view with
three.js:

- `/camera`: the page
- `/v1/camera/ws`: the websocket the page reads
- `/v1/camera/stats`: counts only
- `/v1/camera/atlas.png` and `/v1/camera/atlas.json`: the texture atlas and its block table, 404 until built
- `/v1/camera/mesher.js`: the page's meshing worker
- `/v1/camera/items.png` and `/v1/camera/items.json`: one item icon sprite sheet and its name lookup, 404 until built

The stream is a position channel and has **no authentication**, so the unit
gets no ingress of its own; crew-control proxies an allow-list of these routes
publicly. Reach a unit directly from your machine with a port-forward and open
`http://127.0.0.1:8080/camera`:

```
kubectl -n <namespace> port-forward deploy/<crew deployment> 8080:8080
```

This works with the default `STATUS_ADDR` because a port-forward connects
inside the pod's network namespace. A viewer beyond `CAMERA_MAX_VIEWERS` is
refused with 503, and one that cannot keep up is disconnected and reconnects.
A plain GET of `/v1/camera/ws` attaches nothing and answers 503 when the cap
is reached and 426 otherwise; the page uses it to tell a refusal from an
outage. The page loads three.js from a CDN, so the browser needs internet
access.

The page uses relative URLs, so it also works under a path prefix such as
crew-control's `/units/{id}/camera`. `?mode=first|third|orbit|top` picks the
camera (first person by default; in orbit, drag turns and the wheel zooms),
and outside first person the unit's own body is drawn. `?hud=0` hides the
overlay and debug text and skips the state poll, for wall tiles. A
same-origin `postMessage({type: 'crew-camera', mode})` switches mode without
reconnecting.

A new viewer is replayed the sub-chunks within 4 chunks of the unit, nearest
first, pulled as it reads; orbit and top-down ask for 6 instead. `?r=N` on
the page picks another radius for every mode and `]`
widens it (up to 32). Sub-chunks travel as a material palette plus bit-packed
indices and moving entities as one batch per 50 ms tick; the byte layout is
documented in `internal/camera/wire.go`. `/v1/camera/stats` also reports bytes
sent, sub-chunks sent, viewers dropped for lagging and viewers refused.

### Block ids and wire versions

Every distinct block name a unit streams, a stair or slab shape variant
included, gets a two-byte id for the life of the process, so no block shares a
texture with another. Wire version 4 carries those ids; a sub-chunk palette may
list up to 4096 of them. A materials frame defines only the ids it lists, so
the table grows by what is new rather than being resent whole (1500 materials
cost about 45 KB on the wire instead of about 30 MB). The page keeps each
sub-chunk's cells as 16-bit values, 8 KiB instead of 4 KiB.

The page asks for version 4 with `?wire=4` on the websocket. A page loaded
before an upgrade asks for nothing and is sent version 3: the server rewrites
each frame on that connection, ids above 255 all draw as id 255 (the old
behaviour), and the whole table is rebuilt for each materials frame. A new
page that reads a hello of 3 (a server that predates 4) decodes one-byte ids.
Roll the server first: tabs open across the rollout reconnect at version 3
until reloaded, and nothing breaks in either direction.

The mesher worker is told only the ids that are new and re-meshes a sub-chunk
only if a cell already named an id that was not yet defined. Frustum culling
and the worker's message shapes for meshes are unchanged.

## Lab

`lab/compose.yaml` is a throwaway Bedrock server with cheats on and online
mode off (`ONLINE_MODE: "false"`, `ALLOW_LIST: "false"`), so crew can join it
with `MC_OFFLINE=true` and no Microsoft account.

Offline mode is lab-only. FWB runs online mode, which verifies every client
against Xbox Live and rejects an offline one outright, so an offline unit can
never join it; a FWB unit needs a real account and `MC_OFFLINE` unset. Offline
mode skips the auth cache entirely and logs one `auth_offline` line at start so
such a unit is never mistaken for a signed-in one.

The compose file also runs three units from the real image, `crew-01` Agate,
`crew-02` Basalt and `crew-03` Cobble, with the camera on and their status
ports published on `127.0.0.1:8081` to `8083` only:

```
export TASKS_TOKEN=$(openssl rand -hex 16)   # compose passes it to every unit
docker compose -f minecraft/crew/lab/compose.yaml up -d --build --remove-orphans
curl -s localhost:8081/readyz
```

A unit's number (`CREW_ID`, `crew-NN`) is fixed and its name (`MC_USERNAME`,
16 characters at most) is what players see. They are never renamed or reused.

### Creative for menders and builders

`mender` and `builder` units work in creative, and only the server console can
set that: `lab/creative.sh <lab> Dusty[,Name...]` runs `gamemode creative` for
each named unit that is online, and again whenever one rejoins, because an
offline lab unit is a fresh survival player on every login. It polls the
console's `list` every 3 s and always stops: `--max-duration` (default 30m,
at most 12h), `--max-commands` (default 100) or Ctrl-C; `--once` makes one
pass and exits 3 if a named unit was not online. A rejoin faster than the poll
is missed, so poll no slower than a unit's reconnect. Targets other than the
crew lab need `--confirm-lab`, and FWB is refused. The unit itself must have
`CREW_LAB=true` and `CREW_ROLE=mender` or `builder`. `lab/compose.yaml` has
a commented `crew-04` example (not run by default) and a commented
`VIEW_DISTANCE: "6"`: at the image's default of 32 about half of one spike's
joins were closed by a first-join burst over the client's 16 MiB batch limit.

A creative take goes into an empty hotbar slot. With none empty it replaces
the least recently used slot that an earlier take filled, destroying that stack
in the same request, so a task may use more block types than the hotbar has
slots; the unit's own tools and food are never replaced. A hotbar full of
anything else fails the take with `no empty hotbar slot for the item`. Block
types are taken as the work reaches them, so a task that alternates among more
than nine types pays a take each time one comes round again.

### Gather-and-deposit scene

`lab/scene.sh <lab> [unit]` builds a stone-free platform, a quarry
and a chest, then puts the unit on the platform with a wooden pickaxe:

```
minecraft/crew/lab/scene.sh lab Agate
```

On the cluster lab, name the server pod instead of a compose service. The
scene erases a 17x8x17 area, so a `k8s:` target needs `--confirm-overwrite`:

```
minecraft/crew/lab/scene.sh --confirm-overwrite k8s:<namespace>/<lab-server-pod>
```

The cluster lab is joinable by real players (details in the deployment repo),
so lab commands target the unit by name and never `@a`: the script's second
argument is the unit's username. `scene.sh` defaults it to `crew-1`, the name
from the single-unit lab; the compose lab now runs Agate, Basalt and Cobble, so
pass a name there or use `parallel-scene.sh`. `send-command` exits
0 even when the server rejects a command, so check the server log. The scene
needs cheats, so it only ever runs against a lab; crew itself sends no command.

### Three units in parallel

`lab/parallel-scene.sh [--confirm-overwrite] <lab> [agate] [basalt] [cobble]`
clears 25x8x25 around the origin and builds a quarry, a grove of ten oak
trunks, two outbox chests and a store chest (the layout lives in
`lab/parallel-layout.sh`), then places and equips the three units by name.
`lab/parallel-demo.sh <compose|k8s:<ns>> [timeout-s]` submits one task each:
Agate mines 16 cobblestone into outbox A, Basalt fells 16 oak logs into outbox
B, and Cobble hauls both into the store. It prints
`<id> <name> <state> <reason> <seconds>` per unit and exits non-zero unless
all three are `done`. The units share nothing but the chests.

```
minecraft/crew/lab/parallel-scene.sh lab
minecraft/crew/lab/parallel-demo.sh compose 600
```

The `k8s:<ns>` form port-forwards `deploy/crew-01` to `crew-03` on 18081 to
18083 and reads the token from Secret `crew-tasks`; it needs
`--confirm-overwrite` on the scene like the single-unit one. It waits for each
forward to answer `/healthz` before sending the token and refuses a local port
that is already in use. A timeout, error or interrupt cancels every task still
running. The gatherers get the scene's cleared box as their `area`.

### Twenty units with standing work

The cluster lab runs `crew-01` to `crew-20` at once, each with a `WORK_PLAN`
(see Configuration) in a 32x32-block plot of its own, so none shares a block or
a chest with another. The deployment chart derives plot and plan from the
unit's number and `role`, and a behaviour pack on the server keeps each plot
supplied once a minute:

| Role | Plan, in rotation |
|---|---|
| `miner` | gather 16 cobblestone from the plot's quarry into its chest |
| `chopper` | fell 8 oak logs from the plot's grove into its chest |
| `hauler` | move 64 cobblestone from one chest to the other, then back |
| `shaft` | `mine_shaft` from two entrances in the plot's south strip, into the chest |

Because the pack runs `tp`, a unit that joins or respawns at the hub is sent to
its plot; it also gives the unit its pickaxe or axe and bread. `JOIN_DELAY_S`
spaces the first logins. A plan task yields to anything submitted through the
Task API or crew-control, so an operator's job starts at once.

### Test fields

`lab/courses.sh [--confirm-overwrite] [--plan <file>] <lab> grounds|courses|trees|works|signs`
builds one of four fixed test fields on the lab's flat ground (grass at y 63)
around the spawn hub at x -61..51, z -519..-323. Each is a 3x3 grid of 40x40
cells with roads between, joined to the hub's gates or to the next field by
paths:

| Field | Box | Cells (north row first, west to east) |
|---|---|---|
| `grounds` | x 150..293, z -500..-357 | tree grove (9 woods), ore quarry, liquids and hazards / chests and workstations, beacon plaza with a block gallery, redstone / farm, animal pens, pathfinding course |
| `courses` | x -294..-151, z -500..-357 | drops 1-4, trenches 1-4, jump steps, gaps / pit, low ceilings, water pool, lava behind glass / slabs, stairs, fences, ladders, doors, thin blocks; pads 1-12 (16x16) and 13-15 (38x38) |
| `trees` | x -444..-301, z -500..-357 (west of `courses`) | nine fixed oaks, numbered 1-9 by difficulty, natural trees of 16 species, two-by-two giants / dense oak wood, terraces with fixed oaks, mangrove swamp / sapling nursery, lumber yard with deposit chests, huge nether fungi |
| `works` | x 150..293, z -700..-557 (north of `grounds`) | powered rail loop with a minecart, boat ring with a blue ice lane, fenced arena with armoured dummies / iron doors worked by buttons and levers and a trapdoor hatch, five climbs to a landing (bubble column, ladder, scaffolding, vines, spiral stair) and a magma column down, trench bridges 3/2/1 wide and stepping stones / 30-block surface lanes, clutter, a roofed tunnel maze |

A unit's gather `area` can be one cell: the grove, the quarry and the chest
rows are the work yards. The fixed oaks in `trees` are the same blocks on
every rebuild, so a felling test can count logs; oak 1 has a 4-log trunk,
oak 3 a 12-log trunk that needs a pillar, oak 5 a 2x2 trunk. The other trees
there are the game's own tree features and grow differently every rebuild,
and the field clears 48 blocks up because the giants outgrow 32. Pads are blank on purpose; a test may clear one
(air from y 64 up) and build on it each run. Every number is drawn on its pad.

`cmd/labcourses` generates the commands; the script loads the chunks with
ticking areas, streams the commands to the server's stdin, waits for a logged
end marker and counts real errors. A rebuild replaces the field, including
its stocked chests and its animals, and reports zero errors when everything
placed. `--plan` feeds a saved `labcourses <field>` output instead of running
the generator, for hosts without Go. A `k8s:` target needs `--confirm-overwrite`:

```
minecraft/crew/lab/courses.sh --confirm-overwrite k8s:<namespace>/<lab-server-pod> courses
```

`signs` places the hub's labels: a credit for the hub's author at spawn, a
direction label inside each east and west gate, and a name at each field's
end of the gate paths. They are name-tagged armor stands, because the console
cannot write sign text and the server ignores block placement from a bot. A
rebuild replaces each stand rather than adding one.

#### Nightly rebuild

The lab chart can rebuild fields every night so a test starts clean
(`labRebuild` in `charts/crew-lab`). The crew image carries `/labcourses`; an
init container runs `labcourses -o <dir> grounds courses trees works`, which writes
`<field>.plan` files and the two lab scripts into a shared volume, and a
kubectl container runs `nightly.sh` from there. `nightly.sh` is the only
extra logic: for each field it checks `list` and `tickingarea list`, then
calls `courses.sh --confirm-overwrite --plan <field>.plan`, the same feed as
a manual run. It skips (exit 0, one log line) while a human is online, and
waits up to `STALE_AFTER` seconds for another rebuild's `crew-course-*`
ticking areas to go away before removing them as stale and carrying on. It
exits 1 if a field reports errors or the server's state cannot be read.

A player is human unless `NON_HUMAN` names them or their latest `Player
connected:` line in the server log has an empty `xuid`: people sign in with
an Xbox ID, offline-auth bots such as test bots never do. A player without a
connect line counts as human, since a rebuild destroys the field under them.

```
LAB=k8s:<ns>/<pod> FIELDS="grounds courses trees works" PLANS=<dir> \
  NON_HUMAN="Agate Basalt load-*" STALE_AFTER=900 \
  nightly.sh
```

#### Field tests

`lab/fieldtest.sh k8s:<namespace> [case-glob] [timeout-s]` drives idle units
through the fields and prints one line per case as it finishes, then a summary:

```
drops-ledge-1 crew-07 pass done,server_pos=-283.5,64.0,-487.5 41
summary pass=1 fail=0 units=1 seconds=44
```

It exits 0 only when every selected case passed, 1 otherwise and 2 for bad
arguments. The cases cover goto onto each ledge, across each trench and up
each jump step, into the shapes cell and onto pads 1, 7 and 13, plus a
cobblestone gather in the quarry, an oak gather in the grove and a haul between
two storage chests. In the tree field they fell fixed oaks 1, 2 and 3 (each
confined to one trunk's box) and the dense wood, depositing into a lumber-yard
chest; in the works field they cross the 3-, 2- and 1-wide bridges, climb to
the landing at y 77 and pass through a gates-cell iron door. `--list` prints
the names; `--units crew-04,crew-05` limits the units; `--reset
grounds|courses|trees|works --confirm-overwrite` rebuilds a field first (the
gather and haul cases deplete stocked chests, fell oaks and fell the grove).

- A unit is used only when nothing is queued or running on it and nobody but
  this run has touched it for 120 seconds (`FIELDTEST_QUIET_S`), so a load test
  or a crew-control job keeps its units. Each unit runs one case at a time;
  tasks the run did not start are never cancelled, and tasks it did start are
  cancelled on a timeout, an error or Ctrl-C.
- A unit knows only the terrain near it, so a goto hundreds of blocks away
  has no path. The script first walks the unit along the roads between cells,
  in hops of at most 64 blocks, until the target is in reach.
- A goto is also checked on the server: `querytarget` must place the unit
  within 2.5 blocks across and 2 up or down of the cell. The task result is
  all there is for a chest, since the console cannot read one.
- A new case is one line in `cases_table`: `name|pre|task|expect`, where `pre`
  is an optional `x y z` to goto first, `task` is `goto x y z` or a template
  JSON object, and `expect` is `done` or `failed`. `lab/fieldtest.test.sh`
  checks the table (unique names, valid JSON, coordinates inside a field, area
  spans) and the argument handling without a cluster.

### Crew blueprints

Blueprint JSON lives in `blueprints/<name>.json`. Each file has a `name`, a
`kind` (`house`, `redstone`, `farm` or `infrastructure`), an `x/y/z` size,
palette entries containing Bedrock block names and optional `states`, and one
compact `layers` string per Y level. A layer stores X fastest, then Z; each
character indexes the palette through
`0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz-_`. Layers
are bottom-up. `build_order` is optional: absent, the placement order is
derived layer by layer bottom-up, solid blocks before blocks that need
support (torches, lanterns, doors, ladders, redstone_wire, repeaters,
comparators, levers, buttons, pressure plates, rails, carpets, signs, vines,
trapdoors), and the validator's support check runs against that derived
order. An explicit `build_order` overrides it — every `{x,y,z}` cell exactly
once, bottom-up — and is validated the same way. `materials` is checked
against the blocks (air is omitted and a two-part door counts once). Sizes
are limited to 48 blocks per axis and the compact palette to 64 entries.

For example, the sample redstone check uses this shape:

```json
{
  "name": "redstone_sample",
  "kind": "redstone",
  "size": {"x": 5, "y": 2, "z": 3},
  "palette": [
    {"name": "air"},
    {"name": "stone"},
    {"name": "lever", "states": {"lever_direction": "up_east_west", "open_bit": false}},
    {"name": "unpowered_repeater", "states": {"direction": 3, "repeater_delay": 0}},
    {"name": "redstone_lamp"},
    {"name": "redstone_wire"}
  ],
  "layers": ["..."],
  "materials": [{"item": "stone", "count": 5}],
  "checks": [{
    "set": {"x": 1, "y": 1, "z": 1},
    "block": {"name": "lever", "states": {"lever_direction": "up_east_west", "open_bit": true}},
    "expect": {"x": 3, "y": 1, "z": 1},
    "expected": {"name": "lit_redstone_lamp"}
  }]
}
```

The abbreviated arrays above are illustrative; the complete checked-in
blueprints include every layer cell. Import a Bedrock little-endian
`.mcstructure` from the repository root with:

```
go run ./minecraft/crew/cmd/labcourses blueprint-import house.mcstructure house infrastructure
```

The importer reads `size`, the first `structure.block_indices` layer and
`structure.palette.default.block_palette`, then writes the normalized JSON
(no `build_order` — it is derived on load) under `minecraft/crew/blueprints/`
by default; `-o <dir>` writes elsewhere. The default directory must already
exist, so running the importer from somewhere else fails clearly instead of
sprouting a stray `minecraft/` tree. The checked-in blueprints are also
embedded in the binary through `blueprints/embed.go`, so stamping needs no
checkout — the nightly job's image ships the binary alone. Stamp a named
blueprint at X/Z, optionally rotated in quarter turns, using the same guarded
lab runner as the fields:

```
minecraft/crew/lab/courses.sh --confirm-overwrite k8s:<namespace>/<lab-server-pod> blueprint small_house 0 -800 90
```

Stamp plans include the same `# area` headers as other fields. Rotation
updates the common Bedrock facing, direction, lever, rail, cardinal and pillar
axis states, in both their string and numeric encodings — Bedrock's
`rail_direction`, for one, is an int 0-9.

On the BDS 1.26 lab there is no console-only way to place a working repeater;
every candidate was tried live and left the lamp unlit while an identical
lever-wire-lamp control lit:

- `unpowered_repeater` next to a lever switched on, in any orientation;
- `powered_repeater` placed directly: it reads back as powered but emits
  nothing (even a freshly re-placed lamp beside it stays dark) and it decays
  back to `unpowered_repeater` on its own;
- a block update forced under it (dirt swapped for stone) and a neighbour
  block placed and removed;
- a powered wire (signal 15) feeding its input;
- `structure save` + `structure load` of a lever-on circuit, a load of a
  lever-off circuit followed by switching the loaded lever on, and `/clone`
  of the live circuit.

A lever plus adjacent `redstone_wire` does light a lamp, so the redstone
sample's check follows that proven path while keeping two repeaters as placed
examples for the day a server update fixes console placement. The validator
warns whenever a palette contains a repeater.

### Operator overlay

The page draws a game-style overlay from `/v1/state`, polled once a second:
hearts, a nine-slot hotbar (the selected slot is outlined once crew has chosen
one or the server has moved it), a crosshair, the full inventory, the current
task with a progress bar and the last five, and connection, alive or dead,
position and dimension. It needs no task token. The debug text is in its own
box, visible by default; F3 hides it. Item icons come from the item sheet; an
item the pack has no picture for is drawn as a grey placeholder.

### Textures

Once the camera knows the server version it builds a texture atlas in the
background from Mojang's `bedrock-samples` pack, fetched at runtime; only the
built atlas is cached, under `CAMERA_ATLAS_DIR`. The item sheet is built
from the same pack's item textures, with block items drawn as a block face.
Nothing from the pack is
committed or shipped in the image. When the atlas is ready the stream sends an
atlas frame and the page loads every tile as a layer of one array texture with
mipmaps (crisp up close, smooth at distance; leaves keep their holes), tints
grass, foliage and water, and draws slabs, stairs, cross-plants and torches as
simple shapes. Faces carry Bedrock-style shading (top brightest, bottom
darkest) and soft ambient occlusion at corners; water is translucent with its
surface two sixteenths below the block's edge, and glass and ice are blended. The
sky follows the world time (below) and its horizon colour is the fog colour, and fog closes at the
requested radius so the edge of the loaded terrain fades out. The canvas is
antialiased at up to 2x pixel ratio. The page cannot see block states, so
stairs, top slabs, doors, trapdoors and snow layers carry their boxes in the
block name, from the same shape table the unit's pathing uses; past a budget of
shape variants a block falls back to its default shape.
A grass block's side is an overlay mask in the pack (opaque fringe, transparent
dirt), so the build bakes it into a tile of its own with the dirt filled in and
the fringe coloured for that one climate; drawn raw it was a hole. Chests, beds,
soul sand, the enchanting table, cacti and a few other blocks the table calls
cubes are drawn at their real, smaller size, cave vines and eyeblossoms as
crossed planes, and chains, lanterns, trip wire and the piston helper blocks
are not drawn as cubes. `docs/research/2026-10-27-crew-camera-fidelity-audit.md`
lists what still differs from the game. `loadtest/shots.mjs` screenshots a
unit's camera, optionally with a locally built atlas and mesher served over the
live scene, for before/after comparisons.
If the fetch fails, the camera is offline at first start or the page cannot
load the atlas, the view stays flat-coloured. A build that fails, or can only
serve an older cached atlas, is retried on its own after 5 to 10 minutes; the
delay doubles with each failure in a row up to an hour, and is jittered so
units started together do not all ask GitHub's rate-limited API at once.

Units may share one `CAMERA_ATLAS_DIR` (a ReadWriteMany volume). The atlas
depends only on the server version, so the first unit to need it takes a
`.build.lock` directory in the cache, builds, and releases it; the others wait
and then read the result, and a start that finds the cache filled makes no
network request. The holder refreshes the lock while it works; one left by a
crashed unit is taken over after five minutes without a refresh. An unwritable
cache directory fails at once instead of after the download.

### Lighting, sky and biome tint

The server sends blocks, not light, so the page works light out itself. The
mesher worker computes sky light (full where nothing is above, falling one
level through leaves and water, shut out by solid blocks) and block light
(torches, lava, glowstone, lanterns and the other emitters, by name), spreads
both one level per block, and bakes the result into each mesh as smooth
per-vertex light. The vertex shader applies the game's light curve, so the
time of day changes a uniform, never a mesh: night takes up to 11 levels off
the sky, with a dim blue moonlight floor where the sky is open, and block light
warms as it dims. A cave is near black but not pure black. The light reaches
8 blocks past a sub-chunk's edge, so a torch lights its own surroundings
exactly but a far one is missed across a chunk boundary; stairs and slabs
shut light out from half a block of volume, and block state is not on the
wire, so a campfire or lamp counts as lit only when the server names the lit
form. Entities are dimmed by the time of day only, not by the light where they
stand. Weather does not darken the sky.

The unit sends the world time (a time frame, from the start-game data, set
time packets and the day-cycle rule); the page runs it on at 20 ticks a second
unless the cycle is stopped, and `?time=<ticks>` pins it (tick 0 is sunrise,
6000 noon, 18000 midnight). The sky has the game's colour curves, a glow at
sunrise and sunset, square sun and moon, and stars at night. `l` toggles light
(fully lit when off); the debug box shows the hour.

Biomes come from the chunk payload: the surface biome of each column goes to
the page with the first sub-chunk replayed in it, and a table gives each
biome's grass, foliage and water colour (the atlas colour maps sampled at the
climate in the server's biome definitions) and water opacity (from the pack's
biome file). Each block averages the biomes within two blocks, so a border
fades. A server that sends no biome definitions, or a column whose biomes did
not parse, is tinted as plains. Seen from under water the view is the biome's
water colour, closing in within 24 blocks.

Cost: the extra work is in the worker (about 2.7 ms against 0.9 ms to mesh a
typical sub-chunk, so a first view settles later) and one byte pair per vertex.
The page frame is unchanged: one tile at radius 4 on software GL measured 18.8
fps with 0.7 ms of frame work, against 17.7 to 18.9 fps for main in the table
below.

### Entities

The atlas build also compiles the pack's mob models. It reads each client
entity definition (the newest one where the pack keeps a legacy copy), the
geometry it names (both file layouts, inheritance, mirrored boxes, per-face
or box-unfolded UVs, bind poses) and its default texture, under the same size
and count limits mcmap applies to the models it reads. The textures are
packed into one sheet. The camera serves `/v1/camera/entities.json` and
`/v1/camera/entities.png` and announces them with a models frame; the table
holds a few hundred KB. A cached atlas without them, or with an entity table
from before armour, is served as it is and rebuilt, which downloads the pack
again: the entity step adds about 520 files
(definitions, models and only the textures the definitions name, roughly 1.5
MB) to the several thousand the atlas already reads.

The page (`web/entities.js`) builds a bone tree per entity from that table,
nearest-filtered and lit like a Lambert surface, in the pack's own
coordinates (a model faces north and is drawn mirrored in z so a yaw of zero
faces +z). Legs and arms swing with ground speed, the head follows the
entity's head yaw and pitch, zombies hold their arms out, spiders hold their
default leg pose, and positions are interpolated between the 50 ms ticks. Baby
entities use the pack's baby model where it has one and half size otherwise; a
sheep is drawn as its bare skin with the wool model over it, tinted by its dye
colour, and shows the skin alone once sheared. Dropped items are drawn as a
spinning, bobbing icon from the item sheet. Names are drawn above an entity on
a dark translucent plate. Until the table loads, and for any type it lacks,
the old stand-in boxes (sized to the Bedrock hit boxes, coloured per type)
are drawn.

The pack leaves some posing to animations and render controllers that are not
read. The build covers the common cases (quadruped torsos lie down, spider
legs splay) and shows one fixed look per entity: its default texture, no
saddles, villager professions, horse markings or glowing eyes.

Players, the unit's own body included, wear their skins: crew relays each
listed skin as a PNG in skin frames, with slim arms, legacy 64x32 skins
converted, and the pack's Steve for a skin it cannot draw. Worn armour, from
`MobArmourEquipment` and the unit's own armour window, travels in an armour
frame and is drawn from the pack's `_1`/`_2` armour textures, which the atlas
build packs into the entity sheet (leather in its undyed brown).

Appearance that is not pose travels in an entity data frame: baby, sheared,
the sheep dye index and a dropped item's name, taken from `AddActor`,
`AddItemActor` and `SetActorData`. Entity records on the wire carry the feet
position and a head yaw byte. Players reported by `MovePlayer` are shifted
down by the 1.62 eye height; the other position packets are taken to report
the feet. The frame layouts are documented in `internal/camera/wire.go`.

## Camera wall load test

`loadtest/run.sh` starts M crew units against a lab server with distinct
usernames and status ports, opens N headless Chrome viewers spread across them
(`loadtest/viewers.mjs`, playwright-core), and appends one row per run to a
markdown file. Crew CPU and RSS come from `/proc`, bytes from
`/v1/camera/stats`, and frame cost from the page's `window.__stats`.

```
minecraft/crew/loadtest/run.sh -b <crew binary> -c <chrome> -w <dir holding node_modules/playwright-core> \
  -a <atlas cache dir> -m <units> -n <viewers> [-r radius] [-k viewer cap] [-d window seconds] \
  [-g swiftshader|gpu] -o results.md
```

`-g gpu` renders on a real GPU through Vulkan instead of software GL; the
renderer Chrome actually used goes to stderr, since headless Chrome falls back
to software when it cannot use the GPU. In a container, an NVIDIA GPU needs
graphics capability as well as compute, e.g. with podman and CDI:
`--device nvidia.com/gpu=all -e NVIDIA_DRIVER_CAPABILITIES=all`.

Units are named `load-1`, `load-2`, ... (`-u` changes the prefix) on status
ports from 18100 up (`-p`). The lab must already be running. Crew CPU and
bytes cover the steady window after every page has meshed; "Replay" is the one
off cost of the viewers' first connect.

Results, 8-core Linux host shared with other work, lab world, view distance 8,
software GL (no GPU), 60 s steady window, one viewer per unit where N equals M.
Frame work is time inside the page's frame callback; the software renderer's
cost lands outside it, so the page frame rate is the figure to read. The max
includes the replay burst. These rows predate worker meshing and culling.

| Units | Viewers | Radius | Crew CPU mean / max | Crew RSS max (MB) | Steady KB/s per unit | Steady KB/s per viewer | Replay MB (all units) | Settle (s) | Frame work avg (ms) | Frame work max (ms) | Page fps |
|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 | 1 | 4 | 1.1% / 1.1% | 116 | 3.9 | 3.9 | 0.6 | 0.4 | 0.6 | 72 | 60.0 |
| 2 | 3 | 4 | 1.1% / 1.2% | 111 | 6.3 | 4.2 | 1.7 | 0.7 | 0.8 | 349 | 34.3 |
| 4 | 4 | 4 | 0.9% / 1.0% | 129 | 3.8 | 3.8 | 2.2 | 0.8 | 0.9 | 597 | 25.5 |
| 4 | 4 | 8 | 1.0% / 1.0% | 112 | 4.3 | 4.3 | 15.5 | 1.4 | 2.7 | 947 | 8.4 |
| 1 | 8 | 4 | 2.3% / 2.3% | 109 | 30.0 | 3.8 | 4.6 | 1.6 | 0.8 | 1024 | 13.1 |

The last two rows are beyond the required (1,1), (2,3), (4,4) runs: radius 8
for comparison, and eight viewers on one unit with `CAMERA_MAX_VIEWERS=8`.

Chosen defaults: **tile radius 4 and viewer cap 4, unchanged.** Crew is not
the limit: a unit stays near 1 to 2% of a core and about 4 KB/s per viewer
however many units run, and even eight viewers on one unit cost 2.3% CPU. The
browser is: doubling the radius to 8 sends 7 times the replay data and drops
four tiles from 25 to 8 fps on software GL, while radius 4 settles in under a
second. The cap stays 4 because the cost it guards is the viewers' rendering
and the single unit's fan-out has no measured cliff below it; raise it per
deployment if a wall needs more than four viewers on one unit. Re-measure on
real GPUs.

### Worker meshing and culling

The page meshes sub-chunks in a Web Worker (`web/mesher.js`, buffers
transferred back) and culls each mesh by hand: a three.js `Frustum` against the
tight box the worker measures, plus a cut at the fog's far plane by view depth,
where everything is already the sky colour. About 36% of meshes are drawn at
radius 4 and 31% at radius 8 (looking along one axis). three.js already tested
a loose bounding sphere per mesh, so the gain is the tighter box and the fog
cut, not culling as such.

These rows are not from `run.sh`, which needs a lab server. They use a
synthetic hills world served by a throwaway hub (no bots, 1 unit), four
headless Chrome viewers on software GL, `loadtest/viewers.mjs`, a 15 s window,
three alternating before/after runs each. The host was busy (load average
5 to 10), so absolute numbers are lower and noisier than the table above.

| Viewers | Radius | Page fps before (runs) | Page fps after (runs) |
|---|---|---|---|
| 4 | 4 | 42.1, 43.1, 43.9 | 38.8, 43.4, 44.9 |
| 4 | 8 | 17.2, 18.1, 18.8 | 20.8, 20.8, 21.1 |

Radius 4 is unchanged within noise: it was already past 30 fps in this world,
so the rows above, not this one, show the render-bound case. Radius 8 gains
about 16%.

The `run.sh` re-run on the lab, three builds (`crew-v1.10.0` before worker
meshing and culling, `crew-v1.10.1` with them, and main, which adds the
mipmapped array, AO, sky and mob models), interleaved, three runs per cell, 60 s
window, software GL, the lab world with its three standing units. Each run
started once the host's 1-minute load average was below 1.5 (1.27 to 1.48,
the floor is the previous run's decay; the host was otherwise idle).

| Units | Viewers | Radius | 1.10.0 page fps (runs) | 1.10.1 page fps (runs) | main page fps (runs) | main vs 1.10.1 |
|---|---|---|---|---|---|---|
| 1 | 1 | 4 | 41.3, 49.1, 47.5 | 48.3, 46.6, 48.0 | 17.7, 18.7, 18.9 | 61% slower |
| 4 | 4 | 4 | 11.5, 11.8, 11.5 | 11.5, 11.6, 11.2 | 4.5, 4.5, 4.5 | 61% slower |
| 4 | 4 | 8 | 7.2, 6.9, 6.7 | 7.8, 7.6, 8.1 | 2.5, 2.1, 2.4 | 71% slower |

Four tiles at radius 4 do not reach 30 fps on any build here: about 11.5 on
1.10.0 and 1.10.1, 4.5 on main. Culling shows nowhere at radius 4 and about
13% at radius 8 (6.9 to 7.8). Main runs at under half the 1.10.1 rate in every
row; the camera overhaul (1.12 onward) is the likely cause, which these runs do
not isolate. Its units also send about twice the bytes (4 tiles at radius 4:
5.7 to 7.7 KB/s per unit against 2.7 to 3.4). Crew CPU stays under 2.2% of a
core. An earlier run on a busy host showed the same ordering. Software GL on
one host is not a GPU measurement; re-measure on real GPUs.

The same runs on a real GPU: an RTX 5090 passed through to an 8-vCPU VM, Chrome
in a container rendering through Vulkan (`-g gpu`, renderer checked on every
run), the same three builds interleaved, three runs per cell, 60 s window. The
lab had changed since the rows above: view distance 16 and 20 standing units.
Main on software GL in the same VM is the control.

| Units | Viewers | Radius | 1.10.0 GPU fps | 1.10.1 GPU fps | main GPU fps | main software GL fps (same VM) |
|---|---|---|---|---|---|---|
| 1 | 1 | 4 | 60.0, 60.0, 60.0 | 60.0, 60.0, 60.0 | 60.0, 60.0, 60.0 | 13.4, 17.5, 18.2 |
| 4 | 4 | 4 | 59.9, 60.0, 60.0 | 60.0, 60.0, 60.0 | 60.0, 60.0, 60.0 | 3.3, 4.2, 4.4 |
| 4 | 4 | 8 | 60.0, 60.0, 60.0 | 60.0, 60.0, 60.0 | 59.9, 59.9, 60.0 | 1.7, 2.4, 2.5 |

On a GPU every build holds Chrome's 60 fps frame cap in every cell, four tiles
at radius 8 included, so the 30 fps target is met with room to spare and the
software-GL slowdown of main does not show. 60 is a ceiling, not a measurement
of headroom; frame work inside the callback stays at 0.5 to 4 ms on all three
builds. The software control matches the earlier host (about 18, 4.5 and
2.4 fps), so the slowdown is a cost of software rendering only.
