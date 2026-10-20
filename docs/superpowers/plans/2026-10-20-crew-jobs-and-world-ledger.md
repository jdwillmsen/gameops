# Crew jobs and world ledger — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Operators start one job from the console. crew-control plans it across bots from a shared world ledger, runs it with retries, and reports a single result. Two built-ins: the parallel demo (JDW-117) and a stock goal.

**Architecture:**
- Units report what they see in chests, and gain an `inspect` task and a `cancel_task` command.
- crew-control gains:
  - a ledger (`internal/ledger`) for places, stock and derived unit status;
  - a job store and a stateless scheduler (`internal/jobs`), which turns ready steps into work items on the existing queue;
  - a pure planner for stock goals;
  - signed-in routes, an SSE `jobs` event, and console views.
- Everything persists in Postgres (V10), so a restart resumes.

**Tech Stack:** Go 1.27, pgx v5, plain JS console, Flyway SQL (jdwlabs/platform), Helm (jdw-deployments).

**Spec:** `docs/specs/2026-10-20-crew-jobs-and-world-ledger-design.md`

## Global Constraints

- Lab only.
- No coordinates or template bodies in logs, Jira or PR text.
- No LLM in the runtime.
- **Sign-in.** Every job, place and stock route is signed-in only, using `Auth.Require` for POSTs and `Auth.Operator` for GETs. The SSE `jobs` event goes only to signed-in streams, and the session is re-checked per event, exactly like `work`.
- **Bounds.**
  - Positions: x,z in −500..499 and y in −64..320.
  - Area boxes: at most 64 on each axis, min ≤ max.
  - Item names: `^[a-z_]{1,32}$`.
  - Quantities: 1..256 per item per job.
- **Retries.** A step is retried while `attempts < 3`. A step with no fixed unit moves to another free unit if one exists.
- **Job limits.**
  - Timeout: 30 min by default.
  - At most one top-up round for a stock goal.
  - Failure reason when the goal falls short: `goal not met: have <n> of <want> <item>`.
- **Freshness.** Stock under 10 min old counts as current.
- **Free unit.** Online, not dead, no `running` item, and at most one `queued` item.
- **Splitting stock goals.** k = min(free units, ceil(need/16)), with chunks of at most 64.
- **Job-scoped cancel** never cancels a unit's non-job work. It uses the new `cancel_task {id}` command.
- **Scheduler.**
  - Ticks every 2 s and also on any work event.
  - Keeps no in-memory job state between ticks.
  - Locks jobs with `FOR UPDATE SKIP LOCKED`.
- **Old units** (no `seen`, no `inspect`) must not break new crew-control, and new units must not break old crew-control. All new wire fields are optional.
- **Commit style.**
  - Comments say why, never what.
  - Conventional Commits.
  - Trailers: `Co-Authored-By: Claude <Model> <noreply@anthropic.com>` plus `Assisted-by: Claude Code:<model-id>`.

## Review Focus

1. **A unit goes offline mid-job.** Its step's work goes quiet. Redelivery hands it back to the same unit. After 3 failures (or the 30 min timeout) the job ends `failed` with a reason, never `running` forever. Tested in Task 5.
2. **An operator hand-queues work on a unit, then cancels a job that uses it.** The hand-queued item survives, and only the job's items end `cancelled`. Tested in Tasks 3 and 5.
3. **A stock goal for an item with no source area or chest.** It fails at once with `no source for <item>` and enqueues nothing. Tested in Task 6.
4. **Two operators start goals for the same chest at once.** Both run. Each check reads stock seen after its own start, so neither falsely passes on the other's deposits. Tested in Task 5, as a check-uses-start-time test.
5. **A crew-control restart mid-job.** The scheduler picks the job up from the DB and finishes it. Tested in Task 5, with a new Scheduler over the same store.

---

### Task 1: Flyway V10 (repo `jdwlabs/platform`)

**Files:** `tenants/jdwillmsen/services/jdwillmsen-schemas/postInstall/migrations-configmap.yaml` (append `V10__minecraft_crew_jobs.sql`)

- [ ] Create a worktree from `origin/main`, branch `feat/JDW-117-crew-jobs-tables`. Read the repo's `AGENTS.md`/`CLAUDE.md`, and V8 and V9 for style.
- [ ] Append V10. All objects go in schema `minecraft`, under V2's default grants (say so in the comment).
  - `crew_place`: `id TEXT PK` (`^[a-z][a-z0-9-]{1,31}$`), `name TEXT NOT NULL`, `kind TEXT CHECK (kind IN ('chest','area'))`, `min_x INT`, `min_y INT`, `min_z INT`, `max_x INT`, `max_y INT`, `max_z INT` (all NOT NULL; a chest has min=max), `items TEXT[] NOT NULL DEFAULT '{}'`, `seeded BOOLEAN NOT NULL DEFAULT false`, `created_at`, `updated_at`.
  - `crew_stock`: `place_id TEXT REFERENCES crew_place(id) ON DELETE CASCADE`, `item TEXT`, `count INT NOT NULL CHECK (count >= 0)`, `seen_at TIMESTAMPTZ NOT NULL`, `unit TEXT NOT NULL`, `PRIMARY KEY (place_id, item)`.
  - `crew_job`: `id UUID PK`, `kind TEXT CHECK (kind IN ('plan','stock'))`, `spec JSONB NOT NULL`, `state TEXT CHECK (state IN ('running','done','failed','cancelled'))`, `reason TEXT NOT NULL DEFAULT ''`, `operator TEXT NOT NULL`, `round INT NOT NULL DEFAULT 0`, `deadline TIMESTAMPTZ NOT NULL`, `created_at`, `updated_at`. Index on `(state, updated_at)`.
  - `crew_job_step`: `id UUID PK`, `job_id UUID REFERENCES crew_job(id) ON DELETE CASCADE`, `key TEXT NOT NULL`, `unit TEXT`, `template JSONB NOT NULL`, `depends_on TEXT[] NOT NULL DEFAULT '{}'`, `state TEXT CHECK (state IN ('waiting','queued','running','done','failed','cancelled'))`, `attempts INT NOT NULL DEFAULT 0`, `work_id UUID`, `reason TEXT NOT NULL DEFAULT ''`, `UNIQUE (job_id, key)`.
  - `ALTER TABLE minecraft.crew_work ADD COLUMN IF NOT EXISTS job_id UUID, ADD COLUMN IF NOT EXISTS step_id UUID`, plus an index on `job_id`.
- [ ] Run the repo checks. Replay V1–V10 on a throwaway postgres:16 and insert into every table as role `app`. Remove the container afterwards.
- [ ] Commit `feat(jdwillmsen-schemas): add the crew job and ledger tables`. Push and open the PR, checking the approval gate first. It is bot-authored and needs owner approval, so **do not merge**. Report the PR URL.

### Task 2: crew — `seen`, `inspect`, `cancel_task`

**Files:**
- `minecraft/crew/internal/belief/inventory.go`: expose the last closed chest's contents.
- `minecraft/crew/internal/task/template.go`: the `inspect` template.
- a new skill `minecraft/crew/internal/skill/inspect.go`.
- `minecraft/crew/internal/control/client.go`: `seen` on events, and the `cancel_task` command.

**Interfaces (produces):**
- **`inspect` template.** Body `{"template":"inspect","chest":{"x","y","z"}}`, with bounds as in Global Constraints and strict decode. The skill walks within reach with `NewNavigate(…, 4.5)`, opens the chest, waits for the window contents, and closes it. It is done when the contents have been observed. It fails with the reason `not a chest` if the window never opens within 5 s.
- **Event wire.** `event` gains an optional `Seen *Seen` field (`json:"seen,omitempty"`). `type Seen struct { Chest struct{X,Y,Z int32} \`json:"chest"\`; Items map[string]int \`json:"items"\`; At time.Time \`json:"at"\` }`. Items carry no `minecraft:` prefix, and counts are summed over slots.
  - A task's events carry the latest chest the task's skill closed.
  - `gather_and_deposit`, `haul` and `inspect` all report on their terminal event, and on progress events after a chest closes.
- **Command wire.** `{"kind":"command","command":{"name":"cancel_task","task":"<uuid>"}}` calls `Runner.Cancel(id)`, then posts `done` for the command id. `ErrNotFound` and `ErrFinished` still post `done`, because the goal state already holds. Redelivered `cancel_task` ids are remembered like stop ids.

- [ ] **Tests** (TDD):
  - `inspect` template parse: valid, out of bounds, and extra field.
  - `inspect` skill in the sim harness (`skill/sim_test.go` patterns): it observes contents and reports them, and fails with `not a chest` on air.
  - Client tests:
    - a deposit task's terminal event carries `seen` with the right counts;
    - `cancel_task` cancels only the named task;
    - `cancel_task` for an unknown id posts `done`;
    - a redelivered `cancel_task` is a no-op that posts `done`.
- [ ] Run `go test -race -count=1 ./minecraft/crew/...`, gofmt and vet. Commit `feat(crew): report chest contents, inspect chests, cancel one task`.

### Task 3: crew-control work queue — job context, per-task cancel, `seen`

**Files:** `minecraft/crew-control/internal/work/{store.go,memstore.go,postgres.go,api.go,operator.go}` and their tests.

**Interfaces (produces):**
- **`Item`** gains `JobID, StepID *uuid.UUID`.
- **`Store.EnqueueStep(ctx, unit string, body json.RawMessage, operator string, job, step uuid.UUID) (Item, error)`.** Kind is task. It validates like `Enqueue`.
- **`Store.CancelTask(ctx, unit string, id uuid.UUID, operator string) (item Item, cmd *Item, err error)`.**
  - Queued: cancel it in place; `cmd` is nil.
  - Delivered or running: enqueue a `cancel_task` command naming it, in one transaction; `cmd` is set.
  - Terminal: return it unchanged.
  - **This replaces the coarse "stopped the unit" path in `cancelItem`.** The `note` field is removed. Update the console tests that assert it (Task 8 owns the console code).
- **`Store.CancelJob(ctx, job uuid.UUID, operator string) ([]Item, error)`.** In one transaction it applies `CancelTask` semantics to every open item with that `job_id`. It never touches other items.
- **`validateBody`** accepts the command `{"name":"cancel_task","task":"<uuid>"}`.
- **`api.go` events.** Decode the optional `seen`, then call a new hook `OnSeen func(unit string, s Seen)` set by main. A malformed `seen` is ignored, not 400, so a bad observation never blocks a state report. Keep the 1 KiB body cap and raise it to 4 KiB, because `seen` carries up to 27 item kinds. Document why.
- **`OnTerminal func(it Item)`** is called after `Apply` moves an item with a `job_id` to a terminal state. The scheduler wakes on it.

- [ ] **Tests:**
  - Store suite (memstore, and Postgres on a throwaway postgres:16 with V9+V10, removed afterwards):
    - `EnqueueStep` stores the job and step ids;
    - `CancelTask` on queued, delivered and terminal items;
    - `CancelJob` leaves a hand-queued item on the same unit queued (Review Focus 2);
    - the `cancel_task` command is delivered before tasks.
  - API: `seen` reaches `OnSeen`; malformed `seen` still applies the state; a body over 4 KiB gets 413.
- [ ] Commit `feat(crew-control): job context on work, cancel one task, record what units see`.

### Task 4: crew-control ledger — places, stock, unit status

**Files:** create `minecraft/crew-control/internal/ledger/{ledger.go,memory.go,postgres.go,seed.go,status.go}` and tests.

**Interfaces (produces):**

```go
type Box struct{ Min, Max Pos }      // Pos{X,Y,Z int}
type Place struct{ ID, Name, Kind string; Box Box; Items []string; Seeded bool }
type Stock struct{ Item string; Count int; SeenAt time.Time; Unit string }
type Ledger interface {
    Places(ctx) ([]Place, error)
    Place(ctx, id string) (Place, bool, error)
    PutPlace(ctx, p Place) error          // ErrSeeded when id is seeded
    DeletePlace(ctx, id string) error     // ErrSeeded
    Seed(ctx, ps []Place) error           // upsert seeded=true; drop seeded rows not in ps
    Observe(ctx, unit string, at time.Time, chest Pos, items map[string]int) error
    Stock(ctx, placeID string) ([]Stock, error)
    ChestAt(ctx, p Pos) (Place, bool, error)
}
func ParseSeed(raw string) ([]Place, error) // env CREW_PLACES JSON
func (u UnitStatus) Free() bool          // per Global Constraints
func Statuses(units []telemetry.Status, open map[string][]work.Item) []UnitStatus
```

- **Observe at an unknown chest position** creates an unseeded place with id `chest-<x>-<y>-<z>` (a negative coordinate is written as `m12`) and name `Unnamed chest`.
- **Observe replaces** that place's whole stock row set, because the observation is the full chest contents. Items missing from it get count 0.
- **Validation:** the bounds, id and item rules from Global Constraints. A chest must have min == max.

- [ ] **Tests** (memory, and Postgres when `CREW_PG_TEST_DSN` is set):
  - seed upsert and removal;
  - seeded places refuse edit and delete;
  - observe at a known chest;
  - observe at an unknown position creates `chest-…`;
  - observe replaces the old items;
  - `Free()` truth table.
- [ ] Wire it into `main.go`: build the ledger from the same Postgres pool, or memory when `WORK_STORE=memory`. Seed from `CREW_PLACES` at start. Set `work.OnSeen` → `ledger.Observe`.
- [ ] Commit `feat(crew-control): world ledger of places, stock and unit status`.

### Task 5: crew-control jobs — store and scheduler

**Files:** create `minecraft/crew-control/internal/jobs/{store.go,memory.go,postgres.go,scheduler.go,plan.go}` and tests.

**Interfaces (produces):**

```go
type Step struct{ ID uuid.UUID; Key, Unit string; Template json.RawMessage; DependsOn []string; State string; Attempts int; WorkID *uuid.UUID; Reason string }
type Job struct{ ID uuid.UUID; Kind string; Spec json.RawMessage; State, Reason, Operator string; Round int; Deadline, Created, Updated time.Time; Steps []Step }
type Store interface {
    Create(ctx, j Job) (Job, error)
    Running(ctx) ([]uuid.UUID, error)
    // Lock runs fn with the job locked for update (SKIP LOCKED: a locked job is skipped, ok=false).
    Lock(ctx, id uuid.UUID, fn func(*Job) error) (ok bool, err error)
    Get(ctx, id uuid.UUID) (Job, error)
    List(ctx, n int) ([]Job, error)
}
type Scheduler struct{ /* jobs Store, work work.Store, ledger ledger.Ledger, units func() []ledger.UnitStatus, now func() time.Time, notify func(jobID uuid.UUID) */ }
func (s *Scheduler) Run(ctx context.Context)          // tick 2 s + Wake()
func (s *Scheduler) Wake()
func (s *Scheduler) Tick(ctx context.Context) error   // exported for tests
func (s *Scheduler) Cancel(ctx context.Context, id uuid.UUID, operator string) (Job, error)
```

**Each `Tick`, for each running job id, under `Lock`:**

1. **Deadline passed:** fail the job (`timed out`), then `work.CancelJob`.
2. **Steps with work:** read each step's work item and copy its state.
   - **Work `failed`:**
     - with `attempts < 3`, reset the step to `waiting`, clear `WorkID`, and record a different-unit preference if the step has no fixed unit;
     - otherwise mark the step `failed`, fail the job with reason `step <key>: <work reason>`, and run `work.CancelJob`.
   - **Work `cancelled` but the job is not cancelled:** treat it as failed.
3. **Waiting steps whose dependencies are all `done`:** choose a unit — the fixed unit if the step names one, else the free unit with the fewest open items, avoiding the unit that last failed the step. If one is found, call `EnqueueStep` and mark the step `queued`, `attempts++`. If there is no free unit, the step stays waiting.
4. **All steps `done`:** run the job's check (`plan`: true; `stock`: see Task 6). If it passes, the job is `done`. If it fails:
   - with `round == 0`: append the top-up steps from the planner and set `round = 1`;
   - otherwise fail with the goal reason.

**Wiring:** call `notify(job)` on any change. `work.OnTerminal` calls `Wake`.

- [ ] **Tests** (memory stores, fake clock, fake units):
  - **Plan with deps:** A and B run in parallel, then C. C is enqueued only after both are done, and the job ends `done`.
  - **Retries:** a step fails twice, then succeeds, ending `done` with attempts=3. Failing 3 times gives `failed`, with the reason naming the step, and the job's other work cancelled.
  - **Reassign:** the step that failed on crew-01 is retried on crew-02 when crew-02 is free.
  - **Timeout:** past the deadline the job is `failed timed out`.
  - **Unit offline mid-job** (Review Focus 1): the work goes quiet and redelivers. With failures injected the job fails within 3 attempts, never `running` forever.
  - **Cancel:** the job's items are cancelled through `CancelJob`, and a hand-queued item survives (Review Focus 2).
  - **Restart** (Review Focus 5): a new Scheduler over the same stores finishes a job half-run by the old one.
  - **No free unit:** the step waits, and no work is enqueued.
  - **Postgres:** run the Store suite when `CREW_PG_TEST_DSN` is set, including two concurrent `Lock` calls where only one gets `ok`.
- [ ] Commit `feat(crew-control): job store and scheduler`.

### Task 6: Planners — parallel demo and stock goal

**Files:** `minecraft/crew-control/internal/jobs/plan.go`, `stock.go` and tests.

**Interfaces (produces):**
- `func ParallelDemo(places ledger.Ledger) ([]Step, error)`. It uses place ids `quarry-1`, `grove-1`, `outbox-a`, `outbox-b` and `store`. A missing id gives the error `place <id> not found`.
  - `mine` (unit crew-01): `gather_and_deposit cobblestone 16`, area quarry-1, chest outbox-a.
  - `chop` (unit crew-02): `gather_and_deposit oak_log 16`, area grove-1, chest outbox-b.
  - `haul` (unit crew-03, after mine and chop): `haul {cobblestone:16, oak_log:16}` from [outbox-a, outbox-b] to store.
  - `check`: `inspect store`, after haul.
- `type StockSpec struct{ Place string \`json:"place"\`; Want map[string]int \`json:"want"\` }`
- `func PlanStock(ctx, l ledger.Ledger, spec StockSpec, free []ledger.UnitStatus, since time.Time) (steps []Step, err error)`. The algorithm is spec §5:
  - need = want − stock at target, counting only stock seen at or after `since`. If the target's stock is older than 10 min, or missing, plan an `inspect` step first and make every gather step depend on it.
  - A chest with stock of the item becomes a haul source first. Gather from areas whose `Items` contain it otherwise.
  - Chunks follow Global Constraints. When there are fewer free units than chunks, chunks are given no fixed unit and the scheduler assigns them as units free up.
  - A final `inspect` depends on everything.
  - With no source, return the error `no source for <item>`, and the job is created already `failed` with that reason, with nothing enqueued (Review Focus 3).
- `func CheckStock(ctx, l ledger.Ledger, spec StockSpec, since time.Time) (ok bool, short string, err error)` reads stock seen after `since`, which is the job's creation time (Review Focus 4). `short` is `have <n> of <want> <item>`.

- [ ] **Tests:**
  - ParallelDemo returns the exact four steps and dependencies, and fails on a missing place.
  - PlanStock covers:
    - need already met: only the final inspect;
    - 32 oak_log with 3 free units: 2 chunks of 16;
    - a chest source becomes a haul;
    - stale stock: an inspect first;
    - no source: the error.
  - CheckStock ignores stock seen before `since` (Review Focus 4).
- [ ] Commit `feat(crew-control): plan the parallel demo and stock goals`.

### Task 7: Operator routes and the SSE `jobs` event

**Files:** `minecraft/crew-control/internal/jobs/routes.go` and its test, `internal/ledger/routes.go` and its test, `internal/telemetry/sse.go`, and `main.go`.

**Routes (all signed-in):**
- `POST /v1/jobs` with `{"kind":"plan","template":"parallel-demo"}` or `{"kind":"stock","place":"store","want":{"oak_log":32}}` → 202 Job. Answers 400 on bad input, 422 `{"error":"no source for x"}`, and 503 when the store is down.
- `GET /v1/jobs` lists the last 50.
- `GET /v1/jobs/{id}` returns one job.
- `POST /v1/jobs/{id}/cancel`.
- `GET /v1/places` returns the places, each with its stock.
- `POST /v1/places` (create or update) and `POST /v1/places/{id}/delete`. A seeded place answers 409.

**SSE:** `event: jobs`, `data: {"jobs":[…List(20)]}`. It goes only to signed-in streams, re-checked per event, and is coalesced like `work`. It is sent when a stream opens and on every `notify`.

**Route allow-list:** the chart must add PathPrefix `/v1/jobs` and `/v1/places`. Record this for Task 10.

- [ ] **Tests:** every route answers 401 anonymous, 403 without the header, and works on the happy path. An anonymous stream never gets `jobs`. Run `go test -race -count=1 ./minecraft/crew-control/...`.
- [ ] Commit `feat(crew-control): job and place routes with live updates`.

### Task 8: Console — Jobs and Places views

**Files:** `minecraft/crew-control/internal/console/web/{index.html,console.js,console.css,state.js,state.test.mjs}`.

- **Signed-in only:** key `j` toggles a Jobs panel, and the panel has a Places tab.
- **Jobs:**
  - Start "Parallel demo".
  - Start "Stock goal": a place select from `/v1/places` (chests only), plus rows of item select (cobblestone, oak_log) and qty input with a "+ item" button.
  - The list shows state, done/total steps, reason, and Cancel.
  - A row expands to show its steps.
- **Places:** a list with stock and "seen 3m ago". Create and rename for unseeded places, delete with a confirm.
- **Item cancel:** remove the "stopped the unit" handling. The cancel response no longer has `note`.
- `textContent` only. Inputs don't trigger shortcuts. No inline script.
- [ ] **Tests:**
  - `node --test` covers the pure helpers: stock-goal form to body, and "seen Xm ago".
  - A Playwright run with a virtual authenticator against a local crew-control (memory stores, `CREW_PLACES` seed) and a fake unit that answers `gather`/`haul`/`inspect` with `seen`:
    - start the parallel demo;
    - watch it reach `done`;
    - start a stock goal and see it `done`;
    - cancel a running job.

  Reuse the approach in `/tmp/t7e2e` if it is still there.
- [ ] Commit `feat(crew-control): jobs and places in the console`.

### Task 9: Ship gameops

- [ ] Run the final Opus whole-branch review, then the fix wave.
- [ ] Run `/no-mistakes` with full intent. Write the PR body by the rule. Merge when green.
- [ ] Confirm the crew and crew-control releases and anonymous pulls.

### Task 10: Chart, platform merge, live proof

- [ ] Ask the owner to approve the V10 PR (or, if the owner says "merge", approve it with their auth). Merge it and check the Flyway job's log shows `version v10`.
- [ ] **jdw-deployments.**
  - Add `crew.control.places`: rendered inline as env `CREW_PLACES` (JSON), rolling the pod on change.
  - Seed it from the crew test grounds. Get the positions of the tree cell, the ore quarry cell and three chests in the storage cell from `minecraft/crew/cmd/labcourses/grounds.go`. If the storage cell has fewer than three chests, add the missing ones through `courses.sh`'s building method, inside x 150..293 and z −500..−357 only, and say so in the PR.
  - Ids are `grove-1` (items oak_log), `quarry-1` (items cobblestone), `outbox-a`, `outbox-b` and `store`.
  - Add PathPrefix `/v1/jobs` and `/v1/places` to the route allow-list and its test.
  - Bump both images. Make sure the units have tools for the quarry; check `crew-lab-unit-gear` (the GitOps behaviour pack).
- [ ] **Merge and wait for Argo.** The units and crew-control must be Ready, and every unit logs `control_connected`.
- [ ] **Live proof without credentials.**
  - Anonymous requests to `/v1/jobs` and `/v1/places` get 401, and no `jobs` SSE.
  - The units report `seen` on hand-run work: check the ledger in the DB through the crew-control logs, which should show `stock_observed` with the place id only.
- [ ] **The owner runs the signed-in proof.**
  - Start "Parallel demo": it should end `done`, which is JDW-117's done condition.
  - Start "Stock goal store: oak_log 32, cobblestone 32": it should end `done`, with steps on at least two bots and the store showing at least 32 of each, seen after the start.

  Give them the checklist, then post the results on JDW-117 (@mention Josh) and move it to Done when the owner confirms.
