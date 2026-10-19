# Crew: three named units working in parallel

**Status:** approved by the owner 2026-10-19
**Date:** 2026-10-19
**Epic:** JDW-59 (Build Crew)
**Builds on:** `docs/specs/2026-10-14-crew-design.md` (design record) and
Wave 3a (`docs/superpowers/plans/2026-10-18-crew-wave3a-actions.md`), which
is live: one unit, crew-1, mines 16 cobblestone into a chest on the cluster
lab in about 30 s.

## Why

The owner wants to see several crew units working at the same time, each
doing a different job, and wants them to have names with personality and
numbering that holds up as more units are added. Today there is exactly one
unit, called `crew-1`. After a pod restart it often rejoins as `crew-1(2)`
(JDW-110), because the server keeps the old session for a few seconds.

This brings part of Wave 5 of the design record ("crew-2, more skills, two
units side by side") ahead of Wave 3b (crew-control) and Wave 4 (Crew
Console). That is the owner's choice. Each unit keeps its own token-guarded
task API until crew-control exists.

## The first thing to watch

Three units on the cluster lab, at the same time:

- **Agate** mines stone and drops 16 cobblestone into an outbox chest.
- **Basalt** chops logs and drops 16 logs into a second outbox chest.
- **Cobble** hauls from both outboxes into one store chest.

It is done when the store chest holds 16 cobblestone and 16 logs, the two
outboxes are empty, and all three tasks finished `done`. An operator starts
it with one command, and a player standing on the lab can watch it.

## Goals

1. **Names and numbering.** Each unit has a stable number and a display name.
   - The number (`crew-01`, `crew-02`, …) is fixed for life. Kubernetes
     objects, logs, metrics, Secrets and the task API's identity use it.
   - The display name is what players see in-game: the unit's username on
     the server.
   - Names come from one ordered list, assigned by number: crew-01 is Agate,
     crew-02 is Basalt, crew-03 is Cobble, then Dusty, Ember, Flint, Gravel,
     Hazel and on to Zinc (the full list is below). Adding a unit takes the
     next number and its name, so nobody is renamed.
2. **No more "(2)" names** (JDW-110). After a restart or rollout, a unit
   rejoins under its own name. The fix is in crew's shutdown and reconnect,
   not in a workaround script.
3. **Several units from one chart.** `charts/crew-lab` renders a list of units
   rather than one. Each gets its own Deployment, Service, data volume and
   username, plus a shared task token.
4. **Two new jobs:**
   - **Chop:** the existing gather template with a second recipe (logs with
     an axe).
   - **Haul:** a new template that takes a named item out of one or more
     source chests and deposits it into a destination chest, until a
     quantity has been moved or the sources are empty. It needs a new
     withdraw skill.
5. **A parallel scene and a demo command.** The lab scene builds a quarry, a
   small grove of logs, two outbox chests and a store chest in non-overlapping
   areas. One command starts all three tasks and reports how each finished.

## Non-goals

- crew-control, a shared queue, or any coordination service. Units don't
  talk to each other; the chests are the only hand-off.
- Real trees. The grove is short trunks of logs (two or three blocks high)
  that the unit can reach from the ground. No pillaring, no leaves handling.
- Units avoiding each other's bodies. If two units bump, the server corrects
  them and they carry on; that is acceptable.
- Anything on FWB.
- A UI. The camera page and the public camera route stay as they are, below.

## Anti-goals (would make this a failure even if it works)

- Renaming a unit that already exists when another is added, or reusing a
  number.
- Any lab command that targets players. Every command names a unit, as
  `scene.sh` does today.
- Starving FWB's CronJobs of the namespace's CPU quota. The 2026-10-17 backup
  failures came from exactly that.
- Logging coordinates.

## Constraints

- **Namespace quota.** `jdwillmsen-prd` has a limits.cpu cap of 8 cores.
  Measured on 2026-10-18 it was at 6,450m, and FWB's backup (1,000m),
  volume-recovery and version-check (500m each) need headroom. crew-1 has a
  150m limit and uses about 7m when idle. Three units must add no more than
  about 150m to the total, for example 100m each (300m in place of 150m).
  Memory has room: 13,952Mi of 16Gi in use, and a unit's limit is 512Mi.
- **The lab is shared.**
  - Real players join it (NodePort 31136, offline mode, no operators).
  - The scene clears areas near the origin, so each run on the cluster needs
    the owner's OK.
  - The scene and the demo target units by name only.
- **Offline mode.** Units join with a username and no account. Bedrock
  usernames are at most 16 characters; the names here are short single words.
- **One server, small CPU.** The lab server has a 400m limit. Three units
  near each other load mostly the same chunks, so the server cost is lower
  than three spread-out players.

## Decided

| Decision | Whose call |
| --- | --- |
| Personality names, no number shown in-game | owner |
| Mining-flavoured, alphabetical theme; numbers fixed behind the names | mine (owner accepted) |
| First demo: Agate mines, Basalt chops, Cobble hauls both to a store chest | owner |
| Hauling via outbox chests rather than item hand-off between units | mine |
| One shared task token Secret for all units on the lab | mine |
| Rename the existing unit `crew-1` to crew-01 "Agate" | mine |
| The scene is the only source of tools: Agate gets a pickaxe, Basalt an axe, Cobble nothing, and all three have their inventories cleared first | mine |
| Haul is given per-item quantities (16 cobblestone, 16 logs) and waits for items to appear, polling its sources, until it has moved them all or the task times out. So Cobble starts at the same time as the others and still ends `done` | mine |
| Withdrawing logs `chest_contents` for the source chest after each visit, as deposit does for its chest, so the empty outboxes are shown in the logs and no read-only skill is needed | mine |
| The demo runs from the operator's machine: `kubectl exec`/port-forward to each unit's ClusterIP Service, with the token read from the Secret and never printed. It is a bash script beside `scene.sh` | mine |
| The shared Secret is `crew-tasks`, created in-cluster from a random value that is never printed. `crew-1-tasks` is deleted once nothing references it | mine |
| The public camera route moves to Agate's Service. A short camera outage during the rename is fine on the lab | mine |

### Name list

crew-01 Agate, 02 Basalt, 03 Cobble, 04 Dusty, 05 Ember, 06 Flint, 07 Gravel,
08 Hazel, 09 Ingot, 10 Jasper, 11 Kettle, 12 Loam, 13 Marble, 14 Nugget,
15 Onyx, 16 Pebble, 17 Quartz, 18 Rusty, 19 Slate, 20 Tinder, 21 Umber,
22 Vein, 23 Wick, 24 Xeno, 25 Yarrow, 26 Zinc. A 27th unit starts a second
list, to be written then.

Chest positions travel in the task body, as in Wave 3a. Wave 3a already
keeps the body out of logs and task records, and the new skills' errors
must stay coordinate-free in the same way.

## Left to the builder

- Where the number-to-name mapping lives. It must have one source of truth:
  the chart's unit list is the obvious place, and crew takes the name as
  configuration.
- How the haul template names its source chests and its stopping rule (fixed
  quantity, or "until the sources are empty and nothing is pending").
- The withdraw skill's request shape. The spike recorded
  `PlaceStackRequestAction` for deposits; taking from a chest is the same
  exchange in the other direction, so it needs a short live check on the
  local lab before it is trusted.
- The JDW-110 fix. Likely: send `Disconnect` and let it flush on shutdown,
  plus a short delay before rejoining, or a pre-stop pause in the chart.
  Crew sends no commands, so it cannot ask the server with `list`. However it
  is done, it must be proven with two consecutive rollouts on the cluster lab.
- Release order across the two repos: crew first, then the chart.
- What happens to the existing `crew-1` objects:
  - its volume holds the atlas cache, which takes seconds to rebuild, so a
    new volume per unit is fine;
  - the public camera route (`crew.prd.jdwlabs.com`, `crew.route`) should
    follow Agate;
  - the other units' cameras stay reachable by port-forward.
- Whether `scene.sh` grows into one parallel scene or gains a second script.
- Resource numbers within the quota constraint above.

## Done when

- On the cluster lab, the demo command finishes with all three tasks `done`.
  The store chest's `chest_contents` shows 16 cobblestone and 16 logs, and
  the outboxes show empty, all in under 10 minutes.
- `list` on the lab shows `Agate, Basalt, Cobble` with no suffixes, before
  and after two consecutive rollouts.
- No unit's logs contain coordinates.
- limits.cpu in `jdwillmsen-prd` rose by no more than 150m, and the next
  nightly FWB backup ran.
- The design record's Waves table notes that this came ahead of Wave 3b.
