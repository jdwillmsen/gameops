# Crew: agent onboarding, offboarding and a multi-world control plane

**Status:** draft for owner review (architecture decision; the open questions are listed at the end)
**Date:** 2026-10-27
**Epic:** filed alongside JDW-142 (Crew Control Plane); tickets in the last section
**Builds on:** `2026-10-19-crew-console-and-control-design.md` (crew-control, pull-based work),
`2026-10-19-crew-parallel-units-design.md` (numbers and names),
`2026-10-23-crew-squads-and-arena-design.md` (roles and squads),
`minecraft/crew/docs/decisions.md`, the crew-control README and `docs/ui-design-principles.md`.

## Why

Every crew unit today belongs to one world, the cluster lab, and crew-control
only knows a unit because the chart wrote it into an environment variable.
There is no record of a unit being *added*, *ready* or *removed*: a unit that
never joins looks the same as one that crashed, a removed unit vanishes from
the console mid-history, and nothing returns its items or releases its work.
The owner wants a standard way to bring a unit into service and take it out,
and a control plane that can see and manage units on more than one world.

This spec maps what exists, then defines a unit's identity, a registration
handshake, lifecycle states, onboarding and offboarding procedures, a world
registry, the heartbeat contract, security rules, a migration from the single
lab without downtime, API sketches and a ticket breakdown.

## 1. What exists today

### 1.1 Declaration

| Where | What it says |
| --- | --- |
| `jdw-deployments/charts/crew-lab/values.yaml` `crew.units[]` | `id` (`crew-NN`), `name` (Bedrock username), `role` (`miner`, `chopper`, `hauler`, `shaft`, `warrior`), optional `squad`, `squadRole`, `replicas`, `sharedVolume`, `hard` |
| `templates/crew-deployment.yaml` | One Deployment per unit (Recreate, one replica). Env: `MC_HOST` (the release's own server Service), `MC_PORT`, `CREW_ID`, `MC_USERNAME`, `MC_OFFLINE`, `WORK_PLAN` (from role and yard), `CREW_ROLE`/`CREW_SQUAD` (warriors only, from `squadRole`/`squad`), `JOIN_DELAY_S` (number x stagger), `KIT_CHEST`/`KIT_FOOD`, `CONTROL_URL`/`CONTROL_TOKEN` (shared `WORK_TOKEN`), `TASKS_TOKEN` |
| `templates/crew-control-deployment.yaml` | Renders `CREW_UNITS` (`id`, `name`, `url`) from `crew.units`, **skipping units with zero replicas**; scales itself to 0 when no unit runs |
| `templates/unit-kit-gate.yaml` | Builds the non-human player list from `crew.units[].name`; holds a sync that changes `UNIT_KIT_REV` (the server's unit-kit behaviour pack) until no human is online |
| Behaviour pack (unit-kit) | Teleports a unit that joins or respawns at the hub to its yard, gives it the role's tools and bread, tags kit holders `kitted`; restocks yards once a minute |

The number (`crew-NN`) is identity for life and never reused; the name comes
from a fixed 26-name list by number (parallel-units design).

### 1.2 Discovery and observation in crew-control

- `internal/roster`: `CREW_UNITS` parsed once at start; 1 to 26 units; id
  `^crew-[0-9]{2,}$`; optional `player` (world-save UniqueID). Changing it
  means a new env value, so a controller restart (Recreate).
- `internal/telemetry` poller: GET each unit's `/v1/state` once a second;
  two failures make it `offline`. Fleet states are `working`, `idle`,
  `stuck`, `dead`, `offline`, derived per poll (`states.go`).
- Derived events (`online`, `offline`, `died`, `respawned`, `task_*`,
  `stuck`, `low_*`) go to `minecraft.crew_event`, keyed by the unit slug;
  30-day retention.
- Unit-facing routes: `GET /v1/units/{id}/work` (long-poll), `POST
  /v1/units/{id}/events`, `/v1/squads/{id}/hello|status|callout|orders`.
  All take the one shared `WORK_TOKEN` and refuse `X-Forwarded-For`; the
  unit id is taken from the path (work) or the body (squads) and checked
  only for roster membership.
- `events.FromUnit` already validates a unit's own `kit_failed`, `died`,
  `respawned`, `session_error` against a closed list, **but no route accepts
  them**: they only reach the unit's logs.
- Squads live in memory, rebuilt from hellos; a leader silent for 8 s is
  replaced.
- Alerts: `unit_silent` after `silent_for` (2 m in prd).

### 1.3 What identifies a unit

| Identifier | Scope | Source |
| --- | --- | --- |
| `crew-NN` (`CREW_ID`) | fleet, forever | chart |
| Display name (`MC_USERNAME`) | fleet name list; unique per server by necessity | chart |
| `unique_id` (player UniqueID) | **per world save** | server at spawn, reported in `/v1/state` |
| XUID | empty: lab units sign in offline | n/a |
| `session_id` | one connection | unit, random per connect (`internal/sessionid`) |
| `crew_version` | build | unit |

### 1.4 What "world", "lab" and "server" mean today

One Helm release (`crew-lab` in `jdwillmsen-prd`) is one Bedrock Dedicated
Server pod (`<release>-minecraft-bedrock-0`, NodePort 31136) with one level
(`levelName: crew-lab`), one crew-control, and every unit. "Lab", "server" and
"world" are therefore the same thing, and the code reflects it: `MC_HOST` is
derived from the release; `CREW_WORLD_DB` is a single path; yard origins, the
kit chest and `CREW_PLACES` are absolute coordinates in that one world; place
ids (`chest-x-y-z`) are unique only within it; heatmaps and replay assume one
coordinate space.

### 1.5 What happens at each moment today

| Moment | Today |
| --- | --- |
| Start | Status server first (`/readyz` 503 until in world), squad client hello if a role, control long-poll, sign-in (offline), `JOIN_DELAY_S`, protocol negotiation, dial and spawn. The pack teleports and kits it. crew-control sees `online` on the next poll. No record that onboarding started, or which server version or kit it met |
| Crash or reconnect | Liveness restarts the pod or the connect loop redials; new `session_id`. A running work item silent 30 s is handed out again. Poller emits `offline`/`online`; `unit_silent` after 2 m; squad hands over leadership after 8 s |
| `kit_failed` | Unit logs it with a closed reason code and retries in 3 minutes. Not stored as an event (no route) |
| Scale to zero | Unit disappears from `CREW_UNITS` and crew-control restarts. Its history stays in `crew_event` but the console no longer lists it, so the explorer shows rows for a unit it cannot name |
| Removal from the list | Deployment deleted (PVC kept for a non-shared unit). Plain queued work stays addressed to an id the work routes now 404; job steps may be reassigned by the scheduler's offline-unit path, nothing else is. Items in its inventory stay in the world save. Squad state drops it only when it goes silent |

Gaps, in one line each: no explicit lifecycle; addition and removal need a
controller restart; no drain; no item return; no claim release; unit-sent
events dropped; one shared token lets any unit speak as any other; one world.

## 2. Goals and non-goals

**Goals**

1. One identity model and one registration handshake for every unit.
2. A lifecycle state machine with one writer, visible in the console and the
   event history.
3. Standard, idempotent, observable onboarding and offboarding, each step with
   a reason code from the closed vocabulary.
4. Many worlds in one control plane, with no id collisions and no cross-world
   leakage.
5. Get there from today's lab with no unit downtime.

**Non-goals**

- FWB units. A world of kind `fwb` is refused by code until the FWB gate (the
  console design asked for a code rule; this is it).
- Moving a unit between worlds while it works. A move is offboard then onboard.
- Autoscaling. Units are declared in git.
- A Kubernetes operator (section 4, alternative C).

**Anti-goals** (failure even if it works)

- A unit can claim another unit's identity or another world.
- A coordinate from one world drawn on another world's map.
- A retired id or name reused.
- Offboarding that destroys items rather than returning them.

## 3. Identity

A unit is `crew-NN`, **unique across the whole fleet**, not per world. Every
table already keys on the slug; keeping it global means no row needs a
composite key to stay unambiguous, and a unit that moves world keeps one
history. Its world is an attribute, recorded on every row it produces.

| Field | Asserted by | Trusted because |
| --- | --- | --- |
| `id` | declaration | token binds to it (section 9) |
| `world` | declaration | token binds to it; unit's claim must match |
| `name` | declaration | must match declaration |
| `role`, `squad`, `squad_role` | declaration | must match declaration |
| `capabilities` | unit (what the build can do) | intersected with what the role allows; never widens it |
| `crew_version`, `build` | unit | informational, bounded and shape-checked |
| `kit_rev` | unit (from `CREW_KIT_REV`, rendered from `UNIT_KIT_REV`) | compared with the world's declared revision |
| `server.version`, `server.protocol` | unit (from the server's advertisement) | informational; a mismatch with the world record is a warning |
| `player.unique_id` | unit (from spawn) | per world; stored as `(world, unique_id)` |
| `session_id` | unit | shape-checked; one per connection |

The world-save player id is only meaningful with its world, so detail views
look it up as `(world, unique_id)`.

Names: the 26-name list ends at Zinc and the roster caps at 26. More worlds
mean more units, so a second list is needed before unit 27 (open question 3).

## 4. Alternatives for registration

### A. Pull-based self-registration

A unit with a valid token calls `POST /v1/register`; whatever registers exists.

- For: adding a unit needs no controller change; scales to any world.
- Against: identity rests entirely on a bearer token; a typo or a stray pod
  makes a ghost unit; the controller cannot tell "declared but never came" from
  "never existed", which is exactly the onboarding failure we most need to see;
  retirement has no source of truth.

### B. Declarative registry, attested by a handshake (recommended)

Git declares worlds and units (desired state). crew-control loads the
declaration as its registry and re-reads it without a restart. A unit proves
it is the declared unit with a world-bound token and reports runtime facts in a
hello. The controller is the single writer of each unit's lifecycle state,
derived from declaration, reachability and the unit's own reports.

- For: one source of truth (the chart, as today); "declared but not ready" is
  visible; undeclared units are refused; retirement is a git change that
  leaves an audit trail; no new cluster permissions; fits the 50m CPU budget;
  units keep pulling, so crew-control can restart freely.
- Against: adding a unit is a PR (it already is); the controller does not
  start or stop pods, so offboarding ends with a git change (section 7).

### C. Kubernetes operator with `CrewWorld` and `CrewUnit` resources

A controller reconciles custom resources into Deployments and tells
crew-control.

- For: full lifecycle automation, including scaling pods.
- Against: cluster-scoped CRDs and RBAC are platform changes (R3); a second
  long-running pod against a namespace already near its CPU limit; it
  duplicates what Argo CD does from git; worlds on other hosts still need a
  non-Kubernetes path.

### D. crew-control watches pods through the Kubernetes API

- For: sees `provisioning` and crash loops directly.
- Against: needs a Role to read pods (a new credential path); ties the
  control plane to one cluster; the unit's own reports carry the same signals.

**Decision: B.** It keeps one source of truth, adds no permissions, survives
controller restarts, and makes the onboarding failures visible that A hides. C
is the upgrade path if units ever scale automatically; nothing in B blocks it,
because B's registry format is what a `CrewUnit` would render.

## 5. Lifecycle

### 5.1 States

| State | Meaning | Owner (who moves it here) |
| --- | --- | --- |
| `requested` | Declared in the registry; nothing heard yet | declaration |
| `provisioning` | Status endpoint answers `/healthz`; not yet in the world | controller (from the poll) |
| `joining` | Hello accepted; unit is negotiating, dialling, spawning | unit (hello, `phase: joining`) |
| `kitting` | In the world; acquiring its role's required items | unit (`phase: kitting`) |
| `ready` | In the world, kit complete, accepting work | unit (`phase: ready`), confirmed by controller checks |
| `working` | Running a task (queue or standing plan) | controller (from the work stream and `/v1/state`) |
| `draining` | No new work; finishing or cancelling the current task | operator (console) or declaration (`retire: true`) |
| `offboarding` | Returning items, releasing claims, leaving the world | controller |
| `retired` | Out of the world and removed from the declaration; history kept | declaration (unit removed) after `offboarding` finished |
| `failed` | A step could not complete; carries a reason code; retried by policy | controller |

`working` and `ready` are the two in-service states. Today's fleet readings
(`working`, `idle`, `stuck`, `dead`, `offline`) stay as a *condition* of an
in-service unit, not lifecycle states: a `ready` unit can be `dead` for a
respawn without leaving service.

### 5.2 Transitions

```
requested ──healthz ok──► provisioning ──hello ok──► joining ──spawned──► kitting ──kit ok──► ready ◄──► working
    │                         │                        │                    │                    │
    └──── any check fails past its budget ───────────► failed ──retry policy──► (back to the failed step's start)
                                                                                                 │
ready/working ──drain (operator or retire flag)──► draining ──task ended or drain_timeout──► offboarding
offboarding ──returned, released, left──► parked* ──removed from declaration──► retired
```

`parked` is a sub-state of `offboarding`: the unit has left the world but its
process still runs, because the controller cannot delete pods (section 7.4).

| From | To | Trigger | Reason code on the event |
| --- | --- | --- | --- |
| (none) | `requested` | registry load | `declared` |
| `requested` | `provisioning` | first good `/healthz` | — |
| `provisioning` | `joining` | hello accepted | — |
| `provisioning` | `failed` | hello refused | `token_rejected`, `world_mismatch`, `declaration_mismatch` |
| `joining` | `kitting` | unit reports spawned | — |
| `joining` | `failed` | join budget passed | `auth_failed`, `protocol_mismatch`, `spawn_timeout`, `join_failed` |
| `kitting` | `ready` | kit check passes | — |
| `kitting` | `failed` | kit budget passed | existing `missing_tool`, `kit_no_path`, `kit_chest_timeout`, `kit_visit_timeout`, `kit_chunk_not_loaded` |
| `ready` | `working` | task starts | — |
| `working` | `ready` | task ends | (task's own code) |
| any in-service | `joining` | new `session_id` (reconnect) | `session_lost` |
| any | `failed` | no heartbeat past budget | `heartbeat_lost` |
| `ready`/`working` | `draining` | operator drain or `retire: true` | `operator_drain`, `declared_retire` |
| `draining` | `offboarding` | task ended, or cancelled at `drain_timeout` | `drain_timeout` when cancelled |
| `offboarding` | `failed` | return or release incomplete | `return_incomplete`, `release_incomplete` |
| `offboarding` | `retired` | parked and removed from the declaration | `retired` |
| `failed` | step start | retry policy | `retry` |
| `failed` | `retired` | removed from the declaration | `retired` |
| `requested` | `retired` | removed before it ever came | `retired` |

Retry policy for `failed`: exponential from 30 s to 10 m, unlimited for
`heartbeat_lost` and `session_lost` (the unit reconnects on its own),
three attempts for kit and join failures, then it stays `failed` and an
alert fires. A failed unit is never handed work.

### 5.3 Who writes

crew-control is the only writer of the lifecycle record. Units report phases
and facts; the declaration reports desire; the controller decides. Every
transition is one `lifecycle` event in `crew_event` (`payload.from`,
`payload.to`, `reason_code`, `session_id`, `world`). A repeated report that
does not change the state writes nothing, so replaying a hello or a phase is
harmless.

## 6. Onboarding

Each step is idempotent (safe to repeat from the start), has a budget, and
ends in a transition event. The unit runs steps 3 to 6 itself; the controller
checks each from its side.

| # | Step | Done when | Budget | Failure code |
| --- | --- | --- | --- | --- |
| 1 | Declare | unit appears in the registry with world, name, role | — | registry load refuses a bad entry (whole file, like `CREW_UNITS` today) |
| 2 | Provision | `/healthz` answers | `startupProbe` budget (5 m) | `pod_unreachable` |
| 3 | Hello | controller accepts identity (section 9) | 3 attempts | `token_rejected`, `world_mismatch`, `declaration_mismatch` |
| 4 | Join | spawned; `server.version` and `player.unique_id` known | 2 m after `JOIN_DELAY_S` | `auth_failed`, `protocol_mismatch`, `spawn_timeout`, `join_failed` |
| 5 | Kit | hotbar holds the role's required items (from capabilities) and `kit_rev` matches the world's | 3 m | kit codes as today |
| 6 | Ready | unit's `/readyz` is 200 and its first heartbeat after kitting arrives | 30 s | `heartbeat_lost` |
| 7 | Accept work | controller marks `ready`; long-poll may now return items; standing plan may run | — | — |

Checks the controller runs at step 7 and every hello afterwards:
name in-game matches the declared name (a `(2)` suffix is `name_collision`,
which is JDW-110's symptom); the world's declared server version matches what
the unit saw (warn only); the unit's capabilities cover the role.

Until a unit is `ready`, the long-poll answers 204 and the unit's standing
plan does not start. This needs one unit change: the planner waits for the
controller's `ready` (or runs as today when `CONTROL_URL` is unset).

## 7. Offboarding

### 7.1 Drain

Trigger: an operator's **Drain** in the console, or `retire: true` on the
unit in the declaration. The controller:

1. stops handing out queue items and tells the unit `drain: true` on the next
   poll answer; the unit pauses its standing plan;
2. lets the current task finish up to `drain_timeout` (default 10 m, under
   `TASK_TIMEOUT_S`), then cancels it with `drain_timeout`;
3. reassigns job steps queued for the unit (the job scheduler already
   cancels and reassigns a step queued on an offline unit; drain uses the
   same path) and cancels its other queued items with `unit_draining`, so
   nothing is stranded.

Drain is reversible until offboarding starts (**Undrain**).

### 7.2 Return items

The world declares a `return` place (a chest or chest row). The controller
queues one `return_inventory` task: the existing deposit skill run over every
stack in the inventory, armour included, kit tools included. The deposit's
`seen` report updates the world ledger, so the returned stock is visible as
ordinary stock. Done when the unit's reported inventory is empty, or after
two attempts (`return_incomplete`, then the operator decides: retry or
abandon, which is recorded).

### 7.3 Release claims

| Claim | Released by |
| --- | --- |
| Queue items | cancelled at drain (above) |
| Job steps | reassigned at drain |
| Squad membership | controller removes the member; chain of command hands over at once, not after 8 s silence |
| Camera viewers | proxy closes the unit's sockets |
| Yard and plot | stays reserved to the number forever (plots follow the number); recorded as vacant |
| Alerts and mutes | resolved with `retired` |

### 7.4 Leave and park

The controller sends a `leave` command on the priority lane. The unit sends
`Disconnect` (the JDW-110 path that avoids the `(2)` suffix), stops
reconnecting, keeps its status server up with `/readyz` 503 and
`phase: parked`. The process stays alive so the Deployment does not restart
it into the world. The controller has no permission to delete it, so the
console then shows **Ready to remove** with the one-line values change; an
agent PR removing the unit (or setting `replicas: 0`) finishes it.

### 7.5 Archive

When the unit leaves the declaration after `parked`, the registry row becomes
`retired` with `retired_at`, the last world and the reason. The id and name
are tombstoned: the registry load refuses a retired id or name reappearing.
Events stay queryable: the explorer, timeline and replay keep resolving the
unit's name from the registry (retired units hidden behind an **Include
retired** filter, not deleted). `lifecycle` events are exempt from the 30-day
prune (a handful per unit; open question 6).

## 8. Multi-world model

### 8.1 World registry

```yaml
worlds:
  - id: crew-lab            # ^[a-z][a-z0-9-]{1,23}$, never reused
    name: Crew lab
    kind: lab               # lab | fwb (refused until the FWB gate)
    server: { host: crew-lab-minecraft-bedrock.jdwillmsen-prd.svc.cluster.local, port: 31136 }
    level: crew-lab
    world_db: /world/worlds/crew-lab/db   # optional, for unit detail; one mount per world
    kit_rev: "<UNIT_KIT_REV>"
    places: [...]            # today's CREW_PLACES, now per world
    return_place: store      # offboarding target
    visibility: public       # public | operators
    operators: [owner, josh] # who may command units in this world; default all
units:
  - { id: crew-01, name: Agate, world: crew-lab, role: miner }
```

It ships as a ConfigMap file (`CREW_REGISTRY_FILE`) that crew-control re-reads
every 30 s and on change, so adding a unit or world is a sync, not a
controller restart. A bad file is refused whole and the last good one kept,
with `registry_invalid` logged and alerted. `CREW_UNITS` stays accepted for
one release as a single implicit world.

### 8.2 Data

Every unit-produced row gains `world`: `crew_work`, `crew_event`,
`crew_position`, `crew_alert`, `crew_job` (a job is in one world), and the
ledger. Ledger place ids gain a world prefix in their key (`crew-lab/chest-x-y-z`)
because `chest-x-y-z` collides between worlds; the API keeps showing the short
id within a world. New tables: `crew_world` and `crew_unit` (the registry
snapshot plus lifecycle state, `retired_at`, last `session_id`). All additive,
nullable or defaulted to the first world, like V12. The store checks for the
columns and runs ahead of the migration, as V12 did.

### 8.3 Console

| View | Change |
| --- | --- |
| Bar | A **World** selector (`all` or one id) in the address (`world=`), hidden while only one world exists |
| Fleet (`o`) | Groups by world; a lifecycle column (state word plus shape); filter `life=` |
| Wall (`w`) | Filters by world; non-in-service units show their state, not a dead camera |
| Explorer (`x`) | `world` filter and column; `lifecycle` kind; **Include retired** |
| Timeline (`t`) | Lifecycle band per unit |
| Heatmap (`h`), replay (`y`), ledger (`l`) | **Require exactly one world**; with `all` they say `Pick a world to see positions.` (empty state). Coordinates never cross worlds |
| Unit detail (`d`) | World, lifecycle, versions, kit revision, sessions; **Drain**, **Undrain**, **Retry** for operators |

Lifecycle and world names shown to users come from closed lists (principle 7):
the state vocabulary above, world names from the registry, unknown values as
a neutral word.

### 8.4 Auth per world

- Operator commands check the world's `operators` list.
- `visibility: operators` worlds are absent from anonymous streams and routes
  (not an error, just not listed).
- Signed-in position streams carry only worlds the operator may see.

## 9. Security

**What a unit may assert:** its runtime facts (versions, session, phase,
health, inventory, player id). **What it may not:** its id, world, name, role
or squad beyond what its token and the declaration say; any operator action;
any other unit's state.

**Tokens.** Phase 1 binds the shared `WORK_TOKEN` to a world: one token per
world, and the controller maps token to world, so a unit on world A cannot
speak for world B. Phase 2 gives each unit its own token: the controller holds
one key per world and accepts `HMAC(world_key, world/id)` for unit `id`; the
per-unit Secret is created in-cluster by a hook job from the world key and
never printed. Every unit-facing route then derives the unit from the token,
not from the path or body, which closes today's gap where any unit can hello
or post as any roster member.

**No cross-world leakage.**

- Every query takes a world filter and every row has a world; the console's
  position views refuse `all`.
- A hello whose claimed world differs from the token's is refused with
  `world_mismatch` and alerted; it is never "corrected".
- Unit-sent text stays bounded and stays in operator-only payloads, as today.
- No coordinates in logs, metrics, Jira or PRs (standing rule); world ids and
  unit ids are fine.

**Network.** Units in another namespace reach crew-control by its cluster DNS
name; crew-control reaches them by the URL in the registry. Any NetworkPolicy
change for that is a platform (R3) change.

## 10. Heartbeat and health

Two independent signals, because each catches a failure the other misses:

| Signal | Direction | Proves | Interval | Lost after |
| --- | --- | --- | --- | --- |
| Status poll (`/v1/state`) | controller to unit | the unit is reachable and its world view | 1 s (as today) | 2 misses: condition `unreachable` |
| Heartbeat | unit to controller | the unit's control loop runs, its session and phase | 15 s, piggybacked on the work long-poll (headers `X-Crew-Session`, `X-Crew-Phase`) or posted alone when no poll is open | 60 s: `heartbeat_lost` |

The long-poll already returns at least every 25 s, so the heartbeat costs no
new connection. Health is the combination:

| Poll | Heartbeat | Reading |
| --- | --- | --- |
| ok | ok | healthy |
| ok | lost | control lost (unit runs, cannot reach controller) |
| lost | ok | status server down or network path broken |
| lost | lost | offline; after the budget, `failed` with `heartbeat_lost` |

The heartbeat body, when posted alone, is the hello's runtime fields; a new
`session_id` in it is a reconnect (back to `joining`).

## 11. API sketches

Unit-facing (token, cluster-only, `X-Forwarded-For` refused):

```
POST /v1/units/{id}/hello
{ "world":"crew-lab", "session_id":"a1b2c3d4e5f6", "crew_version":"1.51.0",
  "build":"<git sha>", "phase":"joining", "role":"miner", "squad":null,
  "capabilities":["mine","fell","haul","shaft","deposit","camera"],
  "kit_rev":"<rev>", "server":{"version":"1.26.51","protocol":<n>},
  "player":{"unique_id":"-1234"} }
200 { "state":"joining", "world":"crew-lab", "heartbeat_s":15,
      "drain":false, "registry_rev":"<hash>" }
409 { "reason_code":"declaration_mismatch", "field":"role" }
403 { "reason_code":"world_mismatch" }

POST /v1/units/{id}/phase      { "session_id", "phase":"kitting|ready|parked", "reason_code"? }
POST /v1/units/{id}/unit-events  (kit_failed, died, respawned, session_error; FromUnit already validates)
GET  /v1/units/{id}/work       (existing; answer gains "drain" and a "leave" command)
```

Operator (session plus `X-Crew-Request`, world operator list):

```
POST /v1/units/{id}/drain     { "reason":"..." }  202
POST /v1/units/{id}/undrain                       202 | 409 once offboarding
POST /v1/units/{id}/retry                         202 (failed only)
POST /v1/units/{id}/offboard/abandon-return       202 (records who and why)
```

Read (anonymous for `public` worlds, no coordinates):

```
GET /v1/worlds                    [{id,name,kind,units:{ready,working,failed,...},server_version}]
GET /v1/units?world=&state=&include_retired=
GET /v1/units/{id}/lifecycle      transitions, newest first, keyset-paged
```

Existing routes gain an optional `world=` filter; the position routes require
it once a second world exists.

## 12. Migration from today's lab, without downtime

Every step deploys on its own and leaves units working. crew-control
restarts are Recreate (a few seconds of console gap); units pull, so they keep
working through them (console design decision).

1. **Registry file, one world.** crew-control reads `CREW_REGISTRY_FILE` if
   set, else `CREW_UNITS` as the implicit world `crew-lab`. Chart renders both
   for one release. No behaviour change.
2. **Lifecycle in memory, legacy-tolerant.** Units that never hello are
   inferred: healthz ok and in world means `ready`. Lifecycle events start.
3. **Unit hello and phases** (crew release). Old units keep working; new ones
   report phases. Unit-event route turns on.
4. **Schema** (additive migration, `world` columns default `crew-lab`, new
   tables). The store detects columns, as V12. Applied through
   jdwlabs/platform: **R3, owner applies**.
5. **World-bound token**, then per-unit tokens: the controller accepts the old
   shared token and the new ones for one release, logs which each unit used,
   and the old one is removed once every unit shows the new.
6. **Drain and offboard** (controller and unit).
7. **Console world filter and lifecycle views.**
8. **Second world**: a second server (open question 1) declared in the
   registry. The first real use of the multi-world path.

Rollback at every step is the previous image or values; the schema change is
additive, so an older controller ignores the columns.

## 13. Phased tickets

| # | Ticket | Repo | Risk | Depends |
| --- | --- | --- | --- | --- |
| T1 | Load a hot-reloaded world and unit registry in crew-control | gameops | R2 | — |
| T2 | Render the registry ConfigMap and `CREW_KIT_REV` from the crew-lab chart | jdw-deployments | R2 | T1 |
| T3 | Track unit lifecycle state and write `lifecycle` events | gameops | R2 | T1 |
| T4 | Unit hello, phases and heartbeat; accept unit-sent events | gameops (both) | R2 | T3 |
| T5 | Add world columns and registry tables (additive migration) | gameops + platform | R3 (apply) | T1 |
| T6 | Bind unit tokens to a world, then issue per-unit tokens | gameops + deployments | R3 (credentials) | T4 |
| T7 | Drain, return items, release claims, leave and park | gameops | R2 | T3, T4 |
| T8 | World selector and lifecycle in fleet, wall, explorer, timeline, detail | gameops | R2 | T3, T5 |
| T9 | Stand up a second world and onboard a unit to it | deployments | R2 (R3 if quota or network) | T1–T8 |
| T10 | Retain lifecycle events past the 30-day prune; retired-id tombstones | gameops | R2 | T3 |

## 14. Open questions for the owner

1. **What is the second world?** A second crew-lab release (its own server,
   CPU and NodePort) or a second level on another server? `jdwillmsen-prd`'s
   CPU limit has little headroom; a second server likely needs a quota raise
   in jdwlabs/platform (your change).
2. **One control plane for all worlds, or one per world?** This spec assumes
   one crew-control (one console, one sign-in). One per world is simpler but
   gives no fleet view.
3. **Names past 26.** A second name list (theme?) and whether display names
   must be unique fleet-wide or only per world. This spec keeps them
   fleet-wide.
4. **Who may drain and retire?** Every operator, or only the owner? Should
   retire need a second confirmation?
5. **Item return when the return chest is full or missing**: abandon (items
   stay in the save), or block retirement?
6. **History retention**: keep `lifecycle` events forever, a year, or follow
   the 30-day prune?
7. **Per-unit tokens (T6)** touch credentials, which your ruling keeps at R3.
   Is a world-bound shared token enough for labs, with per-unit tokens only
   before any FWB unit?
8. **Should a `failed` unit be parked automatically** (leave the world) or
   stay in the world idling and defending itself, as today?
9. **Kit revision mismatch**: should a unit whose `kit_rev` differs from the
   world's be held in `kitting`, or only warned about? (The pack bump restarts
   the server, so mismatches are brief.)
