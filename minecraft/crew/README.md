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
| `CONTROL_URL`, `CONTROL_TOKEN` | unset | crew-control's base URL and its `WORK_TOKEN`; set both or neither. When set, the unit long-polls crew-control for tasks and stop commands and reports their progress. Unset, it takes tasks only from the Task API |
| `TASK_TIMEOUT_S` | `900` | Longest a task may run before it fails with `timed out` (30 to 3600) |
| `IDLE_WANDER` | `true` | A unit with no task looks about and takes short walks. `false` leaves it standing, though it still sends an input every tick |
| `IDLE_RADIUS` | `8` | How far from where it went idle a wander may stray, in blocks (1 to 32) |
| `DEFEND` | `true` | Fight hostile mobs, retreat at low health and from creepers, and step back from a player's blow |
| `EAT` | `true` | Eat food from the hotbar when hungry and not fighting (see Eating and rearming) |
| `KIT_CHEST` | unset | `x,y,z` of a chest to rearm from; unset leaves rearming off |
| `KIT_FOOD` | `8` | Most food items one visit to the kit chest takes (1 to 64) |
| `WORK_PLAN` | unset | Standing work: a JSON array of 1 to 8 task bodies, as the Task API takes them, run in rotation whenever the unit has nothing queued or running. A failed step is skipped and the next one waits 30 s. Work from the Task API or crew-control cancels a running plan task and goes first |
| `JOIN_DELAY_S` | `0` | Seconds to wait before the first connect (0 to 600), so a rollout of many units staggers its logins
| `JOIN_DELAY_S` | `0` | Seconds to wait before the first connect (0 to 600), so a rollout of many units staggers its logins
| `CAMERA_ENABLED` | `false` | Serve the rendered camera |
| `CAMERA_MAX_VIEWERS` | `4` | Concurrent camera viewers (1 to 64) |
| `CAMERA_ATLAS_DIR` | `/data/atlas` | Where the built texture atlas is cached; put it on the data volume. The resource pack it is built from is fetched into `/tmp` and removed after the build |

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
off the same way. A visit only fills empty hotbar slots, so a tool is never
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
the corrections. The prediction collides with each block's real shape, which
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
`held_known`) and a read-only `tasks` object: `current` (the running task or
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

`mine_shaft` digs a staircase down from an entrance, tunnels branches off its
bottom, climbs out and deposits everything it gained in one chest. It needs a
pickaxe on the hotbar and is lab-only by construction:

```json
{"template": "mine_shaft", "entrance": {"x": 0, "y": 0, "z": 0}, "facing": "north",
 "depth": 12, "branches": 2, "branch_length": 8,
 "area": {"min": {"x": 0, "y": 0, "z": 0}, "max": {"x": 0, "y": 0, "z": 0}},
 "to": {"x": 0, "y": 0, "z": 0}}
```

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

Crew runs one task at a time. A session end or death fails the running task
(`session ended`, `unit died`) and queued tasks wait for the next spawn.
Bodies are capped at 4096 bytes (413) and at most 16 tasks may wait (429).
Task logs carry the id, template, state and reason, never the request.

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

`lab/courses.sh [--confirm-overwrite] <lab> grounds|courses|signs` builds one of two
fixed test fields on the lab's flat ground (grass at y 63), beside the spawn
hub at x -61..51, z -519..-323. Each is a 3x3 grid of 40x40 cells with roads
between, joined to the hub's gates by paths:

| Field | Box | Cells (north row first, west to east) |
|---|---|---|
| `grounds` | x 150..293, z -500..-357 | tree grove (9 woods), ore quarry, liquids and hazards / chests and workstations, beacon plaza with a block gallery, redstone / farm, animal pens, pathfinding course |
| `courses` | x -294..-151, z -500..-357 | drops 1-4, trenches 1-4, jump steps, gaps / pit, low ceilings, water pool, lava behind glass / slabs, stairs, fences, ladders, doors, thin blocks; pads 1-12 (16x16) and 13-15 (38x38) |

A unit's gather `area` can be one cell: the grove, the quarry and the chest
rows are the work yards. Pads are blank on purpose; a test may clear one
(air from y 64 up) and build on it each run. Every number is drawn on its pad.

`cmd/labcourses` generates the commands; the script loads the chunks with
ticking areas, streams the commands to the server's stdin, waits for a logged
end marker and counts real errors. A rebuild replaces the field, including
its stocked chests and its animals, and reports zero errors when everything
placed. A `k8s:` target needs `--confirm-overwrite`:

```
minecraft/crew/lab/courses.sh --confirm-overwrite k8s:<namespace>/<lab-server-pod> courses
```

`signs` places the hub's labels: a credit for the hub's author at spawn, a
direction label inside each east and west gate, and a name at each field's
end of the gate paths. They are name-tagged armor stands, because the console
cannot write sign text and the server ignores block placement from a bot. A
rebuild replaces each stand rather than adding one.

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
sky is a gradient whose horizon colour is the fog colour, and fog closes at the
requested radius so the edge of the loaded terrain fades out. The canvas is
antialiased at up to 2x pixel ratio. Crew does not receive biome data, so tint is sampled for one climate
everywhere (the plains biome); stairs and slabs have no orientation or half
for the same reason and are drawn as bottom slabs and stairs that rise towards +z.
If the fetch fails, the camera is offline at first start or the page cannot
load the atlas, the view stays flat-coloured and the build is retried on the
next session.

### Entities

The atlas build also compiles the pack's mob models. It reads each client
entity definition (the newest one where the pack keeps a legacy copy), the
geometry it names (both file layouts, inheritance, mirrored boxes, per-face
or box-unfolded UVs, bind poses) and its default texture, under the same size
and count limits mcmap applies to the models it reads. The textures are
packed into one sheet. The camera serves `/v1/camera/entities.json` and
`/v1/camera/entities.png` and announces them with a models frame; the table
holds a few hundred KB. A cached atlas without them is rebuilt on the next
start, which downloads the pack again: the entity step adds about 520 files
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
armour, saddles, villager professions, horse markings or glowing eyes. Players
are the pack's Steve until skins are carried.

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
  -a <atlas cache dir> -m <units> -n <viewers> [-r radius] [-k viewer cap] [-d window seconds] -o results.md
```

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
