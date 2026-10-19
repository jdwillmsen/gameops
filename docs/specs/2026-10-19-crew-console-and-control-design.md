# Crew: a multi-unit console and a control plane

**Status:** approved by the owner 2026-10-19
**Date:** 2026-10-19
**Epic:** JDW-59 (Build Crew). Tickets: JDW-115 (v0 console), JDW-116 (commands), JDW-117 (jobs), JDW-118 (builds), JDW-119 (trees), JDW-120 (mine).
**Builds on:** `docs/specs/2026-10-14-crew-design.md` (D5, D8, Waves 3b and 4)
and `docs/specs/2026-10-19-crew-parallel-units-design.md` (three named units).

## Why

Three units (Agate, Basalt, Cobble) work on the cluster lab, but an operator
can watch only Agate. `crew.prd.jdwlabs.com` routes to crew-01 alone, and the
other cameras need a port-forward. The camera page shows one view: first
person, from the unit's eyes. There is no way to switch units or views.

Every command still goes through a script on the operator's machine that
calls each unit's token-guarded `POST /v1/tasks`. The owner wants to send
commands, jobs, builds and tasks from one place and watch them run.

This design covers both requests. They share one new service, `crew-control`
(design record D5), so the camera work is its first slice rather than chart
routing that would be thrown away later.

## What the owner gets, in order

1. **Console v0 (view only).** `crew.prd.jdwlabs.com` opens one page with a
   unit picker (Agate / Basalt / Cobble, plus keys 1–9) and four camera
   modes:
   - first person (today's view);
   - third person, behind the unit;
   - free orbit around the unit;
   - top-down.
   Switching changes units without reloading the page. Each tile shows the
   unit's health, current task and status. A wall view shows every unit at
   once in small, low-radius tiles; clicking a tile focuses it. It stays public and view
   only, as the owner chose for the camera page on 2026-10-19.
2. **Commands and tasks (sign-in required).** From the console a signed-in
   operator can:
   - give **immediate commands**: stop, go to a point, come to me, follow a
     player, return home, toggle idle-wander or defend;
   - queue **tasks** from the existing templates (gather, chop, haul) on a
     chosen unit;
   - cancel, reorder and watch tasks, with history.
3. **Jobs.** A job is a small plan of tasks across units with dependencies,
   for example the parallel demo: Agate mines and Basalt chops, then Cobble
   hauls once both outboxes have items. A job starts from the console and
   reports one overall result.
4. **Builds.** A build places a structure from a saved template. It needs new
   skills (place a block, fetch materials from a chest, climb or scaffold).
   This is the largest step and gets its own short design when we reach it.

Each step ends in something the owner can watch work.

## Architecture

```
browser ──HTTPS──► crew-control ──HTTP (cluster)──► crew-01 / 02 / 03
  console page        │ roster, auth, queue, jobs        │ camera WS, /v1/state
  camera WS (proxied) │ SSE telemetry                    │ pulls work, reports events
                      ▼
                 Postgres (platform cluster): tasks, jobs, history
```

- **crew-control** is a new Go service at `minecraft/crew-control` with its
  own image and release. It serves the console's static files, as crew serves
  the camera page today. A separate `crew-console` (D8) is only needed if
  the UI outgrows that.
- **Roster.** The chart's unit list stays the one source of truth. The chart
  renders it into crew-control's config (id, name, Service). crew-control
  probes each unit's `/v1/state` for status.
- **Camera proxy.** crew-control relays `/v1/units/{id}/camera/*` (including
  the websocket) to the unit's Service. Units stop being routed publicly, and
  the HTTPRoute points at crew-control. The renderer stays in the browser.
  Two things change on the unit side to allow this:
  - the camera page stops hard-coding `/v1/camera/...` and `/v1/state`, and
    takes a unit base path;
  - the unit's websocket accepts crew-control's origin (`OriginPatterns`).
    Today it rejects any `Origin` that differs from the request's host.
- **Camera modes.** Third person, orbit and top-down are new camera
  placements in the page, which today pins the camera to the unit's eye.
  They need a wider chunk radius than first person, so they cost the unit
  more replay and bandwidth, and the browser more meshing. The radius per
  mode is measured in v0, not guessed.
- **Viewer limits.** The camera stream is stateful per viewer: each one
  picks its radius and gets its own nearest-first replay. So crew-control
  relays one upstream socket per browser tile and does not share them. Wall
  tiles use a small fixed radius. Each unit's `maxViewers` (4 today) and
  CPU are measured with a full wall open before the cap is raised. A shared
  fan-out would need a multiplexer that understands the wire format; that
  is left until the measured cost demands it.
- **Telemetry.** crew-control polls each unit's `/v1/state` about once a
  second and serves one SSE stream for the console (unit status, task
  progress, job state). It includes positions because every unit is a lab
  unit. No unit serves FWB data before the FWB gate. That gate, not a code
  rule, is what keeps FWB positions off this stream, and the FWB design must
  add a code rule.
- **Work dispatch.** Units pull: each unit long-polls
  `GET /v1/units/{id}/work` on crew-control with its unit token and posts
  task events back. This is the shape D5 chose ("crew polls it for work, the
  same way afkbot polls presence"). A unit that loses crew-control keeps
  finishing its current task and then idles; it never needs a push path in.
- **Immediate commands** go on the same long-poll as a separate priority
  lane. The unit's runner preempts the current task with them the way the
  JDW-114 reflex preempts for a fight. Stop must work even when the queue is
  stuck. Stop and go-to use existing pieces (cancel, Navigate). The others
  need new skills:
  - follow a player and come to me (both track a player's position from the
    unit's own entity view);
  - return home (home is a per-unit point in the chart's unit list).
  So the commands step ships stop and go-to first.
- **Wire format** of the work long-poll, task events and IDs (idempotent
  retries) is settled in the commands step's plan, not here.
- **Unit-local task API.** `POST /v1/tasks` on each unit stays cluster-only,
  for lab scripts and debugging, until the console replaces the demo
  scripts. Removing it is a later cleanup.
- **Persistence.** Tasks, jobs and their history live in Postgres on the
  platform's shared cluster, the way the agent stores its data. v0 needs no
  database.
- **Chart.** `charts/crew-lab` gains crew-control's Deployment, Service,
  Secret references and config rendered from the existing `crew.units` list.
  The HTTPRoute moves from crew-01 to crew-control and keeps its allow-list
  style. Postgres access arrives with the commands step.
- **Auth.** v0 is anonymous and view only. Everything that changes a unit
  needs sign-in (see the open questions). crew-control talks to units with
  the existing `crew-tasks` token and per-unit work tokens. None of them
  reach the browser.

## Standing rules this keeps

- Lab only. Nothing here connects to FWB or serves FWB data before the FWB
  gate. No cheats anywhere but the lab.
- No coordinates in logs, metrics, Jira or PR text. Goals and positions stay
  in request bodies and the lab-only telemetry stream, as Wave 3a does.
- No LLM in the runtime. Jobs and builds are declarative plans.
- CPU: `jdwillmsen-prd` is close to its limits.cpu quota. crew-control must
  fit in about 50m. If it doesn't, a quota raise in jdwlabs/platform comes
  first, and that is the owner's change.

## Decided (my calls, open to change)

| Decision | Why |
| --- | --- |
| One new service, `crew-control`, serving the console too | One deployable, one auth point. D8's separate console waits until it is needed |
| Camera switching ships as crew-control v0, not as per-unit chart routes | The routes would be removed again when auth arrives |
| Units pull work; crew-control never dials into a unit to start work | Matches D5. A unit survives crew-control restarts |
| Extra camera modes are browser-side | Zero unit CPU. The data is already on the page |
| One upstream camera socket per tile, not shared | The stream is per-viewer stateful. Sharing needs a multiplexer, built only if measurement says so |
| Builds get their own design later | New skills and materials logistics. Too big to settle here |

## Owner answers (2026-10-19)

1. **Who can command units:** the owner and Josh, through the map's sign-in
   (passkeys now, Xbox sign-in once JDW-71 lands), with an allow-list in the
   chart.
2. **The view-only console stays public.** Task names and status are shown to
   anonymous viewers. Commands and their history need sign-in.
3. **First builds:** a hut, a wall and a farm plot. Alongside them, felling
   real trees (today's chop only handles short log trunks) and digging a mine
   shaft and a mine. Each gets its own short design when the builds step
   starts.

## Done when (per step)

- **v0:** on `crew.prd.jdwlabs.com`, without a reload, an anonymous viewer
  switches between all three units and between the four camera modes, and
  opens the wall view. With the wall plus one focused view open, no unit is
  throttled at its 50m CPU limit (measured, with the numbers in the PR).
- **Commands and tasks:** a signed-in operator queues a chop on Basalt from
  the console, watches it finish, and stops Agate mid-task with one click.
  An anonymous viewer cannot do either.
- **Jobs:** the parallel demo runs as one job from the console and finishes
  `done`, with no script on the operator's machine.
- **Builds:** defined in the build design.
