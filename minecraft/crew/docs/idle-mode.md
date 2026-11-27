# Crew idle mode: replacing afkbot

Status: design, 2026-11-27. Epic JDW-375, this design JDW-376.

## Why

FWB's two AFK bots run `minecraft/afkbot`, a second Bedrock client written
next to crew. Bot 2 (Dotablaze7321) needed five releases (afkbot 1.14.1 to
1.14.5, #387 to #391) to sit on its cushion facing the door. Four of those
fixes were things crew's actuator already did: end the loading screen, send
a `PlayerAuthInput` every tick with a rising tick, adopt
`CorrectPlayerMovePrediction`, and use `InteractionModelClassic`. Keeping two
clients means every protocol fix lands twice.

Not everything flows one way. afkbot also learned things crew has not:
acknowledging server teleports (`InputFlagHandledTeleport`), taking the
ground flag from the server, filtering corrections to the player's own, and
riding. Those move into crew as part of this work.

The goal is one bot codebase. An FWB AFK bot becomes a crew unit started in
**idle mode**: it joins, holds its chunks, and otherwise does nothing a
player could notice beyond sitting and facing where it is told.

## Shape: `CREW_MODE=idle` on the existing binary

Idle mode is an environment switch on the `crew` command, not a new
subcommand or binary.

- The chart change at cutover stays a swap of image and env. The afkbot
  Deployment has no `args`, and the crew image's entrypoint is `/crew`.
- One image, one release line, one Renovate rule.
- `CREW_MODE` defaults to `work`, which is crew as it runs today. No existing
  unit changes.

What idle mode turns off is enforced in `config.Load`, so a chart typo cannot
give an FWB bot a worker's behaviour:

- **Forced off**, whatever their defaults: `IDLE_WANDER`, `DEFEND`, `EAT`,
  `RECOVER_DROPS`, `NIGHT_SHELTER`, `INV_MANAGE`, `KIT_GEAR`, the combat
  baseline. Several default to true in work mode, so rejecting only an
  explicit value would not be enough.
- **Startup error if set**: `TASKS_TOKEN`, `WORK_PLAN`, `CONTROL_URL`,
  `CREW_ROLE`, `CREW_SQUAD`, `CREW_HARNESS`, `CREW_LAB`, `KIT_CHEST`,
  `INV_CHEST`, `HOME_POS`, and any of them set to turn a behaviour on.
  Failing loudly beats ignoring: an ignored `DEFEND=true` reads as "the bot
  defends itself" to whoever set it.
- **Unchanged**: `CAMERA_ENABLED` (default off; the FWB chart never sets it)
  and `STATUS_ADDR` (default loopback).

### How idle mode drives the unit

Crew's task runner ticks every 50 ms and, with no task, calls its
`Reflex.Idle`, which sends the tick's one input. Idle mode keeps the runner
and the actuator and swaps the reflex:

- `life.Life` is still constructed, because the read loop feeds it, but it is
  **not** installed as the runner's reflex. The cushion lookup uses belief's
  entity table, which records every `AddActor` with type, position, runtime
  ID and unique ID.
- A new `internal/afk` package provides `afk.Reflex`, which implements
  `task.Reflex`. `Engaged`, `Swimming` and `Tending` always return false, so
  the unit never fights, swims or tends. A reflex that cannot engage is a
  guarantee; a set of `life` flags is a configuration someone can get wrong.
- `afk.Reflex` also has an `Apply(pk)` that `unit.apply` calls from the read
  loop, because the `Reflex` interface sees no packets and stow, seat and the
  logs below need them. State written by `Apply` and read by `Idle` (inventory
  occupancy, selected slot, seat candidates) sits behind one mutex. Logging
  state is read-loop only.
- `Reset`, which the runner calls when a session ends, clears seat tries, the
  aim and the stow state.

The actuator already ends the loading screen before the first tick
(`unit.startActing`), sends one input per tick with a rising tick, adopts
corrections, and uses the classic interaction model, so those lessons are
inherited rather than re-implemented.

## Feature map

Every afkbot setting, its crew equivalent, and the work needed. "Same name"
means the chart keeps the env var as it is.

| afkbot | Crew today | Idle mode |
|---|---|---|
| `MC_HOST`, `MC_PORT`, RakNet and NetherNet | Same names; `agent/pkg/mcdial` dials both | NetherNet connect timeout is 45 s in crew and 60 s in afkbot, which counts the Xbox and PlayFab sign-in. Use 60 s, since FWB is on NetherNet. |
| `MC_USERNAME`, device-code auth | Same name; `agent/pkg/mcauth` | See Auth cache. |
| `AUTH_CACHE_DIR` (default `/data/auth`) | Same name and default | Nothing to do. |
| `RECONNECT_MIN_MS` / `_MAX_MS`, jitter, 60 s stable-session reset | Same names and the same `runConnectLoop` shape | Add the presence gate (see Presence). |
| `MC_VIEW_DISTANCE` (default 12, max 64) → `RequestChunkRadius`, again after respawn | Same name, default 8, max 32; sent at spawn and after respawn | Default 12 in idle mode. Log `chunk_radius_granted` from `ChunkRadiusUpdated`: it is the only evidence the farm radius was granted. |
| `PRESENCE_URL`, `_ACTOR_ID`, `_TOKEN`, `_DEFAULT`, `_POLL_MS` | None (decision of 2026-10-14) | **The main gap.** See Presence. |
| `SKIN_THEME` (dotablaze, neon, sheriff) | None; crew picks a persona from `CREW_UNIT_ROLE` | Add `SKIN_THEME`: the override file first, then the theme, then the persona. |
| `SKIN_DIR` and the `<username>.png` override | Same name, same `skin.Override` | Nothing to do. |
| Skin arm size and resource patch on `ClientData` | Crew sends neither | Send `ArmSize` and `SkinResourcePatch` as afkbot does. The sheriff theme is always slim; without the patch its slim-arm texture is drawn on the wide model. |
| `FACING` (yaw held every tick) | The actuator takes yaw per tick from the caller | `afk.Reflex.Idle` sends `Input{Yaw: FACING, Pitch: held}`. Unset keeps the current rotation. |
| Seated facing via `VehicleRotation` | **None: the actuator cannot ride** | See Seat and riding. |
| `STOW=true` (select an empty hotbar slot, never move items) | `Actuator.SelectHotbar`; belief tracks the inventory | Port the stower: first empty hotbar slot, `stow_sent` / `stow_no_empty_slot`, at most every 10 s, retry when a slot frees or the server puts an item in hand. |
| `SEAT=x,y,z` (nearest `minecraft:cushion` within 1.5 blocks) | `Actuator.InteractEntity` (verified on BDS 1.26.60.6, empty hand included) | See Seat and riding. `SEAT` implies stow, as in afkbot. |
| Teleport acknowledgement (afkbot 1.13.x, `ceb84ce1`, `82725c65`) | **None: crew adopts the teleport but never sets `InputFlagHandledTeleport`** | Port it to the actuator. Bot 2 is verified with it, so it is not optional. |
| Respawn after death | `agent/pkg/liveness`, the same handshake | Nothing to do. Stow and seat get fresh tries after a respawn. |
| `ride_link`, `seat_link`, `server_moved_bot`, `movement_corrected` logs | Correction diagnostics exist; ride logs do not | Port `ride_link` and `seat_link` into `afk.Reflex.Apply`: the cutover check reads `ride_link`. |
| Probes | None on the afkbot pods | None on the FWB pods either (see Presence). |
| `LOG_LEVEL`, JSON logs | Same, via `agent/pkg/logging` | Nothing to do. |
| Pod: 50m/128Mi request, 100m/256Mi limit | Lab units run with a 384Mi limit at radius 8 | See Memory and CPU. |

## Auth cache

Both clients name the cache file `token-<slug>-<sha256[:4]>.json` with the
same slug rules, so crew finds the token afkbot saved on the bot's PVC, and
both images run as a nonroot user under the pod's `fsGroup` 1000.

The name match is not enough. Crew's decoder rejects a cached token with no
expiry, and afkbot's loader only requires a refresh token. A token crew
rejects means a device-code prompt and a bot offline until someone signs in,
and lab units run offline, so the lab never exercises this. Therefore:

- A test loads a token written by afkbot's `saveToken` through crew's
  `FileStore` and `DecodeToken`, and a test pins that the two name functions
  agree, until afkbot is deleted.
- Before cutover, JDW-379 checks that the real PVC token has an `expiry`
  field, without printing the token.
- Rollback stays safe: afkbot needs only a refresh token, which a token crew
  rewrites still carries. The cross-decoder test covers this direction too.

## Presence

The FWB server agent decides whether each bot is in the world. Players use
`!park` and `!unpark`, and the bot polls `GET /v1/actors/{id}/presence` and
reports to `POST .../status` (`internal/presenceapi`). Crew left this out in
2026-10 because there was no agent to talk to. Idle mode reverses that
decision for idle units only.

**Move, do not rewrite.** `afkbot/internal/presence` (client, reconciler,
gate and their tests) moves to `minecraft/agent/pkg/presence`, which crew
already imports from. Its logger interface becomes a `map[string]any` shape
with `Info`, `Warn` and `Error`, and crew's `logAdapter` gains `Warn`. afkbot
imports the moved package until it is deleted (JDW-380).

What carries over unchanged, because each was learned on FWB:

- **Fail-static.** An agent outage, a bad token or a malformed answer leaves
  the bot doing what it was last told, so an agent restart never drops the
  farms.
- `PRESENCE_DEFAULT` is acted on until the agent first answers.
- `If-None-Match` polling every `PRESENCE_POLL_MS` (1 s to 10 min, default
  10 s), with a status report on each poll.
- Streak logging: two lines per outage, not one per poll.

Wiring in crew:

- `runConnectLoop` gains afkbot's `Gate`. `Admit` blocks while parked and
  returns a session context the gate cancels on park. A park is not a
  failure, so it neither grows the backoff nor delays the return.
- The connect function gains afkbot's `spawned` callback, so the gate reports
  `Connected(true)` to the agent once the unit is in the world.
- The gate is `AlwaysPresent` unless `PRESENCE_URL` is set, so work-mode units
  behave exactly as today. Config rejects `PRESENCE_URL` in work mode:
  crew-control owns when a worker joins, and two owners of one decision is
  the overlap the 2026-10-14 decision avoided.
- Leaving on park goes through crew's existing `AfterFunc(ctx, leave)`, so the
  server drops the player at once and the next join is not `name(2)`.
- Between sessions the runner sees no spawn, so it sends nothing. Belief is
  reset by the loop's `onEnd`, and `afk.Reflex.Reset` clears its state.

**Probes.** The FWB bot pods have none, and the status API binds loopback,
which a kubelet cannot reach. Binding it wider would expose the unit's
position and the players near it. So the FWB pods stay probe-free, as they
are today, and `/readyz` keeps its meaning. A probe can come later through a
probe-only listener that serves no state, if one is wanted.

## Seat and riding

afkbot's seat flow, which works on FWB today, is ported with crew's parts in
place of afkbot's:

1. Wait until the hands are known empty (the stow ran) and the unit is alive.
   The interact transaction must describe the held slot, and a full hand
   would use its item on the cushion.
2. Every 10 s, up to 6 tries, find the nearest `minecraft:cushion` within 1.5
   blocks of `SEAT` in belief's entity table. Log `seat_missing` or
   `seat_found`.
3. Look at the cushion's centre (0.125 above its feet) for 500 ms through the
   tick's yaw and pitch, so the server has the look before the click. If the
   unit moves while aiming, re-aim. **Aim from the eye**: afkbot's position is
   the server's eye position, crew's pose is the feet, so the look is computed
   from `Feet + 1.62`. A direct port would aim 1.6 blocks low. A test pins it.
4. `Actuator.InteractEntity(cushion, emptySlot, emptyItem)` sends the
   interact transaction and the arm swing.
5. Once seated, tries reset, so a later dismount or respawn gets six more.

**Riding is new to crew.** `Actuator.Tick` runs the walking physics every
tick, so a seated unit would claim positions under gravity, be corrected
every tick, and have its rotation ignored. Changes:

- **Ride state lives in belief**, which exists from `Spawn`, not in the
  actuator, which starts only once the position is known and would miss an
  early link. Belief already knows the unit's unique ID. It tracks
  `SetActorLink` for links whose rider is the unit: rider sets the vehicle,
  remove clears it only if it names the current vehicle, and any other link
  type clears it. It also reads links carried in `AddActor.EntityLinks`, and
  a `Respawn` clears it.
- **While riding, `Tick` skips physics.** It reports the server's last
  position for the unit, sets `InputFlagClientPredictedVehicle`, and sends
  `ClientPredictedVehicle` and `VehicleRotation{pitch, yaw}`. A rider's yaw is
  overridden by its vehicle's, so the vehicle rotation is how a seated bot
  faces anything. On mount and dismount the carried velocity is zeroed.
- **Corrections are filtered by type.** afkbot adopts only
  `PredictionTypePlayer`. Crew's actuator and belief adopt every correction,
  so a vehicle correction for the cushion would overwrite the unit's feet,
  yaw and believed position. Both gain the filter, with a test.
- **Ground flag.** afkbot takes the server's `OnGround` from `MovePlayer` and
  corrections, and sets `InputFlagVerticalCollision` only when on the ground
  and not riding. Crew's actuator already adopts `OnGround` from both; the
  seated tick must not set the flag.
- **Golden test.** Bot 2's afkbot 1.14.5 input is the verified reference. A
  test feeds crew's actuator and afkbot's `holdFacing` the same state
  (standing, seated, just teleported) and compares the `PlayerAuthInput`
  fields the server reads: position, rotation, flags, vehicle and its
  rotation. It runs until afkbot is deleted.

## Memory and CPU

Crew keeps every received sub-chunk in its block store, because the
actuator's collision model reads it. `storedSubChunks(12)` is 531 columns of
24 sections, 12,744 sub-chunks, about 200 MiB at roughly 16 KiB for a solid
section. That is the store's worst case alone; lab units already need a 384Mi
limit at radius 8, and FWB's built terrain is denser than the lab's flat hub.
afkbot keeps nothing and fits in 256 MiB with a 100m CPU limit.

So the FWB limit is sized from the worst case plus a measured baseline, not
from a lab reading of flat ground: JDW-378 records the idle unit's RSS and
CPU at radius 12, and JDW-379 sets the limit to the store's worst case plus
that baseline with headroom (expected around 512Mi), and checks CPU against
the 100m limit. Dropping the store in idle mode was considered and rejected:
the collision model treats unknown blocks as solid, which keeps a standing
unit up, but the unit could then never step anywhere safely if it is ever
knocked off its seat.

## What stays as it is on FWB

- No operator commands, ever: no `tp`, no `ride`, no `give`. The bot sits by
  clicking the cushion, as a player does.
- The status API stays on loopback and the camera stays off. A unit's
  position is as good as a base location on FWB.
- Crew on FWB needs the FWB group's agreement (owner's ruling, 2026-10-14).
  That is JDW-379's interview, not something this design settles.

## Cutover order (input for JDW-379)

The epic names bot 2 as the canary. This design recommends **bot 1 first**,
for the owner to decide in the JDW-379 interview:

- Bot 1 uses no facing or seat, so it exercises auth, presence, chunk radius
  and reconnects without the new riding code.
- It still changes behaviour: afkbot without `FACING` sends no input at all,
  and under crew it sends an input every tick. Bot 1 is where that is proven
  on FWB.
- Bot 2's seat and facing are the hard-won part. They move only once bot 1 has
  soaked and the riding golden test passes.

Rollback for either bot is setting its image back to afkbot 1.14.5, which
reads the same token file.

## Build tasks (JDW-377)

Each task adds only the settings it implements, so no release accepts a
setting that does nothing.

1. **Move presence** to `agent/pkg/presence`; afkbot imports it. No behaviour
   change; afkbot's tests move with it.
2. **Auth cache tests**: the name match and the cross-decoder test.
3. **Idle mode and presence.** `CREW_MODE`, the forced-off and rejected
   settings, idle default view distance 12, `chunk_radius_granted`, `PRESENCE_*`
   with afkbot's parsers and limits, the gate and the `spawned` callback in
   the connect loop, the 60 s NetherNet timeout, and `afk.Reflex` holding the
   current rotation. Port afkbot's `loop_test.go` cases for park, unpark and
   backoff. This is enough for bot 1.
4. **Skin.** `SKIN_THEME`, `ArmSize` and `SkinResourcePatch`.
5. **Actuator fixes.** Teleport acknowledgement, the correction type filter
   in actuator and belief.
6. **Riding.** Ride state in belief, the seated tick, the golden test.
7. **Facing, stow, seat** in `afk.Reflex`, with `FACING`, `STOW`, `SEAT`,
   `ride_link` and `seat_link`. Port afkbot's `facing_test.go` and
   `stowsit_test.go`: the empty-hand gate, capped retries, re-aim after a
   move, tries reset once seated, plus the eye-height aim test.
8. **Docs and release.** README section, a `decisions.md` entry superseding
   "no presence client yet", and a crew image release.

## Lab proof (JDW-378)

One idle unit on the crew lab, set up without operator commands:

- A cushion placed by hand or in a lab structure, `SEAT` pointing at it and
  `FACING` at a marked point.
- The crew-lab chart gains a per-unit `env` map, so one unit can run idle mode
  without forking the Deployment template.
- A presence stub, `crew/cmd/presencestub`, serving the `presenceapi`
  contract for one actor and flipped by a local HTTP call. It is lab-only and
  never ships to FWB.

Evidence for each point goes in a JDW-378 comment: seated, facing, empty
hands, chunk radius granted, surviving a server restart, parking and
unparking, and RSS and CPU at radius 12.

## Rejected

- **Keep afkbot and share a protocol library.** It halves the duplicated code
  but keeps two release lines and two places to wire each feature. The epic's
  point is one runtime.
- **Idle mode as `life` settings** (`IDLE_WANDER=false`, `DEFEND=false` and
  so on). `life` still fights when a flag is forgotten, and seat and stow
  would hang off a package about survival.
- **A separate `crew idle` subcommand.** It gains nothing over the env
  switch, and the chart would need `args` it does not have today.
- **Readiness while parked.** It needs the status API off loopback on FWB.
