# Crew: jobs and a shared world ledger

**Status:** written 2026-10-20. The owner chose "smarter crew bots" and asked for no further questions, so every decision below is my call unless it is marked otherwise.
**Epic:** JDW-59. **Ticket:** JDW-117 (multi-unit jobs).
**Builds on:**
- `docs/specs/2026-10-19-crew-console-and-control-design.md` (step 3, "Jobs")
- `docs/specs/2026-10-19-crew-control-commands-design.md` (the queue, sign-in and unit pull, live as of 2026-10-20)

## Why

Today an operator gives each bot one task at a time, and has to know the coordinates of every chest, tree and quarry. The three-bot demo only runs from `lab/parallel-demo.sh` on the operator's machine. Nothing records what is in which chest or which bot is busy, so every request needs a human to plan it.

The owner wants the bots to carry more of that thinking:
- say what you want ("stock 32 oak logs and 32 cobblestone in the store"), not who does what;
- crew-control works out which bots, where, in what order, and handles failures;
- it remembers what it has learned about the world, so the next request is smarter.

## Goals

1. **Jobs.** A job is one request that crew-control turns into tasks across bots, with dependencies. It reports one overall result: `running`, `done`, `failed` or `cancelled`. It is started, watched and cancelled from the console.
2. **A world ledger in crew-control.** It holds:
   - **places:** named chests and resource areas;
   - **stock:** what crew-control last knew was in each chest, and when;
   - **unit status:** who is free, busy, or offline.

   Planners read it, and units keep it fresh as they work.
3. **Two job kinds:**
   - **plan:** explicit steps with dependencies. The parallel demo is a built-in plan.
   - **stock goal:** "have N of item X in place P". crew-control plans it from the ledger, splits it across free bots, checks the result, and retries or reassigns failed parts.
4. **JDW-117's done condition:** the parallel demo runs as one job from the console and finishes `done`, with no script on the operator's machine.
5. **This design's own done condition:** a stock goal, 32 oak_log and 32 cobblestone into the store, finishes `done` with work split across at least two bots. Afterwards the store's ledger stock reads at least 32 of each, confirmed by a unit having seen it.

## Non-goals (this step)

- No LLM in the runtime. Planning is plain code with fixed rules.
- No crafting, smelting, farming or building. Builds are JDW-118; their skills come later.
- No reordering of individual queued tasks.
- No job editor UI for arbitrary plans. Plans come from built-in templates. A JSON plan can be submitted through the API, but the console only offers the templates.
- No pathfinding or skill changes on the bots, except reporting what they see in chests.

## Anti-goals

- **Silent partial success.** A job that ends `done` while the goal isn't met.
- **Endless retry.** A broken step loops forever and burns bot time.
- **Clobbered work.** A job tramples work an operator queued by hand, or another job's work.
- **Coordinates in logs,** Jira or PRs. Positions live only in places, request bodies and the lab-only stream, as before.

## Design

### 1. Places (world ledger, part 1)

- **What a place is:** `{id, name, kind, box, items?}`.
  - `kind` is `chest` or `area`.
  - `box` is a single block position for a chest, or an inclusive min/max box for an area, at most 64³ like the existing `area`.
  - `items` is the list of items an area yields, e.g. `["oak_log"]` for a grove or `["cobblestone"]` for a quarry.
  - Ids are slugs like `store`, `outbox-a`, `grove-1` or `quarry-1`.
- **Where places come from:**
  - **Seed from the chart.** `crew.control.places` holds the parallel-scene layout, rendered into env like `CREW_OPERATORS`. Seeded places are read-only in the UI, and git stays the source of truth for them.
  - **Created by signed-in operators** from the console. They are stored in Postgres and editable and deletable there.
- **Validation:** bounds are the same as `goto`, inside the lab's flat square. A box larger than 64³ is rejected.

### 2. Stock (world ledger, part 2)

- **Units report what they see.** Whenever a unit closes a chest window it has opened (deposit, withdraw, haul), it adds an observation to its next task event:

  ```json
  "seen": {"chest": {"x":0,"y":100,"z":0}, "items": {"oak_log": 16, "cobblestone": 16}, "at": "…"}
  ```

  This is a new optional field on the existing events POST. Old crew-control ignores it, and old units never send it.
- **Recording.** crew-control matches the position to a chest place. If none matches, it records an unnamed chest that operators can name later. It then stores the counts with `seen_at` and the reporting unit.
- **Freshness.** Stock is "what was true at `seen_at`". Nothing else (players, hoppers) is known to touch lab chests, so planners treat stock under 10 minutes old as current. Older stock is a hint: a goal job re-checks it before planning, with a cheap `inspect` task that opens and closes the chest.
- **New template: `inspect {chest}`.** A unit walks to the chest, opens it, and reports `seen`. It is the only new skill, built from the existing navigate plus open and close.

### 3. Unit status (world ledger, part 3)

This is derived and needs no new storage:
- online and alive come from telemetry;
- busy comes from open work items in the queue (`delivered` or `running`);
- the job, if any, comes from `crew_work.job_id`.

**"Free"** means online, alive, and holding no running item and no more than one queued item.

### 4. Jobs

- **Job and step tables.**
  - `crew_job(id, kind, spec jsonb, state, reason, operator, created_at, updated_at)`
  - `crew_job_step(id, job_id, key, unit nullable, template jsonb, depends_on text[], state, attempts, work_id nullable, reason)`
- **Linking work to jobs.** Work items gain `job_id` and `step_id`, both nullable, so hand-queued work is untouched.
- **Scheduler.** A loop in crew-control ticks every 2 s and on any work event. For each running job:
  1. A step whose `depends_on` are all `done` and that has no work is **ready**. Ready steps get a unit: the fixed unit if the step names one, otherwise the free unit with the fewest open items, ties broken by roster order. They are then enqueued through the existing queue, which wakes the unit's long poll.
  2. **Failed work:** a step whose work ends `failed` is retried while `attempts < 3`. A step with no fixed unit goes to a different free unit if one exists. On the third failure the step is `failed` and so is the job. The job's other open work is cancelled, using the atomic per-unit stop only for units running this job's work. Hand-queued work on those units is left queued, because the stop cancels only work with this `job_id` (see §6).
  3. **Done:** the job is `done` when every step is `done` and its **check** passes.
- **Checks guard against silent partial success.**
  - A plan job's check is "all steps done".
  - A stock goal's check reads ledger stock seen after the job started. If that is missing or older than the last deposit, the scheduler first enqueues an `inspect` step.
  - If the check fails, the job enters at most one **top-up round**, planned again from the new stock. A second check failure fails the job with reason `goal not met: have X of Y`.
- **Cancel** from the console: queued steps are cancelled, and units running this job's work get a job-scoped stop. The job ends `cancelled`.
- **Restarts.** The scheduler keeps no state in memory between ticks. Everything is in Postgres, so a crew-control restart resumes running jobs.
- **Durations.** A job has a timeout, 30 min by default, and is `failed` when it passes it.

### 5. The stock-goal planner

Input: `{place: "store", want: {"oak_log": 32, "cobblestone": 32}}`.

1. **Need.** need = want − current stock at the target (0 if unknown and not yet inspected).
2. **Sources.**
   - **Areas.** For each item with need > 0, find the areas whose `items` include it. If there are none, the job fails before any work starts, with reason `no source for oak_log`.
   - **Chests.** Chests with stock of the item, other than the target, are also sources: a haul replaces gathering.
3. **Split.**
   - Divide each item's need across k units, where k = min(free units, ceil(need / 16)), in chunks of at most 64.
   - Each chunk becomes a `gather_and_deposit` with `chest = target` and `area = the source area`. If a chest source covers it, it becomes a `haul`.
   - For a split, the planner prefers units already near the source. Nearness is the distance from the unit's last known position to the box centre, and the coordinates are used only in memory.
4. **Final check.** One `inspect` step on the target depends on all the others.

### 6. Queue changes

Two new nullable columns on `crew_work`, `job_id` and `step_id`, give every work item its job context.

`StopUnit` gains an optional job filter: it cancels only the matching open items and enqueues a stop. That raises a problem. The unit's stop cancels everything it holds, including hand-queued work.

- So job cancel never sends a unit-wide stop while that unit holds non-job work.
- Instead it uses a new **`cancel_task {id}` command**: the unit cancels one held task by id.
- This adds per-item cancel on the unit side, which the commands design deferred. Item Cancel in the console uses it too, replacing the coarse "stopped the unit".

### 7. Console

- **Jobs view.** There is a new "Jobs" view (key `j`), visible only when signed in.
  - Start a job from a template:
    - "Parallel demo" (no fields);
    - "Stock goal" (place picker plus item/qty rows).
  - A list of jobs with state, progress (steps done / total) and reason.
  - Expanding a job shows its steps: unit, template summary, state, attempts.
  - Each running job has a Cancel button.
- **Places view.**
  - A list of places with their last seen stock and how long ago it was seen.
  - Create, rename and delete for non-seeded places.
  - Selecting a place shows it on the focused unit's camera as a highlighted box: a browser-side overlay that adds no unit CPU.
- **Live updates.** A new SSE event `jobs` goes to signed-in streams, coalesced like `work`.

### 8. Storage

Flyway V10 in jdwlabs/platform adds:
- `minecraft.crew_place`
- `minecraft.crew_stock (place_id, item, count, seen_at, unit)`
- `minecraft.crew_job`
- `minecraft.crew_job_step`
- the two `crew_work` columns

All of these sit in the `minecraft` schema for V2's default grants, as V9 did.

### 9. Unit side (crew)

- The `seen` observation on events, from the existing chest-window inventory tracking.
- The `inspect` template and skill.
- The `cancel_task {id}` command, which calls `Runner.Cancel(id)` and posts `done`.

## Constraints

- **Lab only.** Nothing touches FWB.
- **CPU.** crew-control stays at a 50m limit. The scheduler is a cheap DB query every 2 s. Measure it in the live check, and if it goes over, raise the tick to 5 s.
- **Compatibility.** Old units must keep working against new crew-control: no `seen`, no `inspect`. A stock goal with an old unit falls back to "steps done" plus an inspect that can never run, so it waits. Ship both images together; the chart bumps both.
- **Sign-in.** All job and place routes are signed-in only. Anonymous viewers see no jobs, places or stock.

## Delivery

| PR | Repo | Contents |
|---|---|---|
| 1 | jdwlabs/platform | V10 tables and columns |
| 2 | gameops | crew: `seen`, `inspect`, `cancel_task`. crew-control: ledger, scheduler, planner, routes, console views |
| 3 | jdw-deployments | `crew.control.places` seed values (parallel-scene layout), image bumps |

The live proof needs the parallel scene built on the lab, with `lab/parallel-scene.sh --confirm-overwrite`, which needs a fresh owner OK. Alternatively the seed places can point at the existing crew test grounds east of the hub (x 150..293, z −500..−357), which already have trees and a quarry. **My call:**
- Seed places from the test grounds, taking positions from `lab/courses.sh`, which builds them: the tree cell, the ore quarry cell, and the chests in the storage cell.
- The built-in "parallel demo" plan is then written over **place ids**, not fixed coordinates: Agate mines `quarry-1` into `outbox-a`, Basalt chops `grove-1` into `outbox-b`, and Cobble hauls both into `store`. The demo runs wherever those places point.
- Nothing gets overwritten, and the old y=100 scene is not rebuilt. It was removed when the lab square was flattened.
- If the storage cell lacks three free chests, the chart-side task places them with console `setblock`, inside the crew test grounds only.

## Left to the builder

- Exact SQL indexes, and whether the scheduler uses `FOR UPDATE SKIP LOCKED` on jobs. It should, for a future second replica.
- Console layout details within the existing style.
- The exact coalescing interval for `jobs` events.
