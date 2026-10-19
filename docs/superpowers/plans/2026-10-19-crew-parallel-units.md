# Crew Parallel Units Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Three named crew units, Agate, Basalt and Cobble, mine, chop and haul in parallel on the lab, ending with 16 cobblestone and 16 logs in one store chest.

**Architecture:**
- Each unit runs the existing crew binary with its own identity (`CREW_ID` plus the display name `MC_USERNAME`).
- Units cooperate only through chests:
  - Agate and Basalt use the existing gather template, which gains a log recipe.
  - Cobble runs a new `haul` template, built from a new withdraw skill and the existing deposit skill.
- The `crew-lab` chart renders a list of units in place of one, and lab scripts build the shared scene and start all three tasks.

**Tech Stack:** Go 1.x, gophertunnel v1.62.0, bash, Helm (jdw-deployments `charts/crew-lab`), Argo CD.

**Spec:** `docs/specs/2026-10-19-crew-parallel-units-design.md` (approved 2026-10-19). Wave 3a context: `docs/specs/2026-10-18-crew-actions-spike.md`.

## Global Constraints

- Unit identity is a fixed number plus a name:
  - crew-01 is Agate, crew-02 Basalt and crew-03 Cobble. The full list is in the spec.
  - Nobody is renamed when a unit is added, and a number is never reused.
- Every lab command targets units by name, never `@a`.
- Each cluster scene run needs `--confirm-overwrite`. The owner approved this plan's runs on 2026-10-19.
- No coordinates in any log line, task record or error string. Chest positions travel only in task bodies.
- limits.cpu for all crew units together rises by at most 150m over today's 150m. Each unit is limited to 100m CPU, with a 512Mi memory limit.
- Bedrock usernames are 16 characters at most.
- Lab only; nothing touches FWB.
- Comments say why, never what. No ticket IDs, URLs or names in code.
- Commits follow Conventional Commits with scope `crew` (gameops) or `crew-lab` (jdw-deployments), and carry the session's attribution trailers.
- Use `/usr/bin/git` for git commands. Never work on main.
- Checks: `go test -race ./minecraft/crew/...`, `go vet ./minecraft/crew/...`, and `test -z "$(gofmt -l minecraft/crew)"`. `shellcheck` covers lab scripts.

## Review Focus

1. **Empty outboxes at start.** Cobble starts while the outboxes are empty. It must wait by polling, not fail. Task 4 test: `TestHaulWaitsForItemsToAppear`.
2. **Cobble's inventory fills before it has moved everything.** It must deposit and come back, not fail. Task 4 test: `TestHaulMakesSeveralTripsWhenItemsExceedOneLoad`.
3. **A unit restarts mid-task.** The task fails with a reason, the unit rejoins under its own name, and resubmitting works. Covered by Task 1's live proof and Wave 3a's runner tests.
4. **The two gatherers walk through each other's work areas.** A gatherer must not take the other's blocks or open the store chest. The scene layout puts stone and logs at least 8 blocks apart, and each recipe names its own block. Task 5's live run checks the final chest counts exactly.
5. **A withdraw is rejected** (status 49 or 1, or the chest is closed under the unit). The unit closes the window and retries the trip, failing only after the shared retry bound. Task 4 test: `TestWithdrawRetriesAfterARejection`.

---

### Task 1: Rejoin under the same name after a restart (JDW-110)

**Files:**
- Modify: `minecraft/crew/cmd/crew/main.go`, and whatever the diagnosis shows
- Test: `minecraft/crew/cmd/crew/main_test.go` or a new `shutdown_test.go`

**Interfaces:**
- Produces: on shutdown, crew leaves the server so that `Player disconnected: <name>` is logged before the process exits.

- [ ] **Step 1: Reproduce on the local lab.**
  1. `docker compose -f minecraft/crew/lab/compose.yaml up -d --build`
  2. Wait for `Player Spawned: crew-1`.
  3. `docker compose … restart crew-1`, then `docker compose … exec -T lab send-command list`.

  Expected: `crew-1(2)`. Record the server log timestamps for the old session's disconnect and the new connect. If it does not reproduce locally, reproduce it on the cluster lab with `kubectl rollout restart deploy/crew-1`.
- [ ] **Step 2: Find the cause** with `mattpocock-skills:diagnosing-bugs`. Find out whether the SIGTERM path reaches `conn.Close()`, and whether a RakNet disconnect notification or a `packet.Disconnect` is sent and flushed before exit. Write the finding into the commit message.
- [ ] **Step 3: Write a failing test** that pins the fix at the seam the diagnosis found. For example, `TestShutdownSendsDisconnectBeforeClosing`, with a fake conn that records `WritePacket(&packet.Disconnect{})` and then `Flush()` and `Close()`, in that order, when the context is cancelled.
- [ ] **Step 4: Implement the fix.** If the server still needs time after a clean disconnect, add the smallest delay that the proof in Step 6 shows is enough. Put it either before the first dial or as the chart's `preStop` (Task 6), and say which in the commit message.
- [ ] **Step 5: Run the checks.** Expected: PASS.
- [ ] **Step 6: Live proof on the local lab.** Restart twice in a row; after each restart, `list` shows `crew-1` with no suffix.
- [ ] **Step 7: Commit** as `fix(crew): leave the server cleanly so a restart keeps the unit's name`.

### Task 2: Give each unit a fixed id

**Files:**
- Modify: `minecraft/crew/internal/config/config.go`, `minecraft/crew/cmd/crew/main.go` (logger fields), and `minecraft/crew/internal/statusapi` (state body)
- Test: `minecraft/crew/internal/config/config_test.go`, `minecraft/crew/internal/statusapi/*_test.go`

**Interfaces:**
- Produces:
  - `config.Config.UnitID string`, read from `CREW_ID`. If it is unset, it defaults to `Username`.
  - It must match `^crew-[0-9]{2,}$` when set.
  - Every log line carries `"unit": <UnitID>`.
  - `GET /v1/state` gains `"unit": {"id": <UnitID>, "name": <Username>}`.

- [ ] **Step 1: Write the failing tests:**
  - `TestUnitIDDefaultsToTheUsername`
  - `TestUnitIDMustBeCrewNN`: `CREW_ID=crew-1` and `CREW_ID=agate` are rejected; `crew-01` and `crew-123` are accepted.
  - `TestStateNamesTheUnit`: the body has `unit.id == "crew-02"` and `unit.name == "Basalt"`.
  - `TestUsernameIsAtMost16Characters`: a 17-character `MC_USERNAME` is rejected.
- [ ] **Step 2: Run them.** Expected: FAIL.
- [ ] **Step 3: Implement it.** Attach the unit id to the root logger once, so every event inherits it.
- [ ] **Step 4: Run the checks.** Expected: PASS.
- [ ] **Step 5: Commit** as `feat(crew): give each unit a fixed id beside its name`.

### Task 3: Take items out of a chest (actuator), proven live

**Files:**
- Modify: `minecraft/crew/internal/act/container.go`
- Create: `minecraft/crew/internal/act/testdata/chest_withdraw.bin`, recorded live
- Test: `minecraft/crew/internal/act/container_test.go`
- Modify: `docs/specs/2026-10-18-crew-actions-spike.md`, adding a "Withdraw" section with the accepted sequence and statuses

**Interfaces:**
- Consumes: `Actuator.Container()`, `ContainerContent()`, `Response(id) (status uint8, done bool)`, and `Died()` (Wave 3a).
- Produces: `func (a *Actuator) Withdraw(fromSlot byte, count int, to belief.Item, toSlot int) (requestID int32, err error)`.
  - It sends one `ItemStackRequest` with a `TakeStackRequestAction`. The source is `ContainerLevelEntity` at `fromSlot`, quoting that slot's `StackNetworkID` from `ContainerContent()`. The destination is `ContainerCombinedHotBarAndInventory` at `toSlot`, with `StackNetworkID` 0 for an empty slot or `to.StackNetworkID` when merging.
  - The result is polled with `Response`.
  - Errors: no container open; `fromSlot` out of range or empty; count < 1 or more than the source holds; `toSlot` outside 0–35.

- [ ] **Step 1: Record a live withdraw on the local lab.**
  - Use a throwaway probe built like the Wave 3a spike's, and delete it afterwards.
  - Put 16 cobblestone in a chest, open it, take 16 into an empty slot, and record the server's replies in the fixture framing that `docs/specs/2026-10-18-crew-actions-spike.md` "Fixtures" describes.
  - Record also: taking from an empty slot, taking with a stale `StackNetworkID`, and taking into an occupied slot of another item.
  - Note each status number.
- [ ] **Step 2: Write the failing tests:**
  - `TestWithdrawQuotesTheSourceStackAndAnEmptyDestination`: checks the request fields against the recorded exchange.
  - `TestWithdrawRejectsEmptySourceAndBadSlots`.
  - `TestWithdrawResponseUpdatesBelief`: replaying `chest_withdraw.bin` into `belief.Tracker` shows 16 cobblestone in the destination slot. Extend the tracker's `ItemStackResponse` handling only if this fails.
- [ ] **Step 3: Run them.** Expected: FAIL.
- [ ] **Step 4: Implement `Withdraw`**, plus any belief change the fixture forces.
- [ ] **Step 5: Run the checks.** Expected: PASS.
- [ ] **Step 6: Commit** as `feat(crew): take items out of a chest`, and the record update as `docs(crew): record the withdraw exchange`.

### Task 4: Haul skill, log recipe and haul template

**Files:**
- Create: `minecraft/crew/internal/skill/withdraw.go`, `minecraft/crew/internal/skill/haul.go`
- Test: `minecraft/crew/internal/skill/haul_test.go`, extending the simulator in `sim_test.go` with chests that are filled over time and with withdraw responses
- Modify: `minecraft/crew/internal/task/recipes.go`, `minecraft/crew/internal/task/template.go`
- Test: `minecraft/crew/internal/task/template_test.go`
- Modify: `minecraft/crew/README.md` (Tasks section)

**Interfaces:**
- Consumes:
  - Task 3's `Actuator.Withdraw`, which is added to the `skill.Actuator` interface.
  - `skill.NewDepositChest(world, self, chest, item, qty, opts...)`.
  - The navigator, and the `ChestWorld` interface.
- Produces:
  - `func NewWithdrawChest(world ChestWorld, self func() belief.Snapshot, chest protocol.BlockPos, want map[string]int, opts ...Option) Skill`
    - Name `withdraw_chest`.
    - It walks to the chest, opens it, and takes up to `want[item]` of each listed item while the inventory has room.
    - It logs `chest_contents` (item names and counts only) for the chest it leaves, and closes the chest on every exit path.
    - An empty chest is `Done`, not a failure.
    - It implements `Stopper`.
  - `func NewHaul(world ChestWorld, self func() belief.Snapshot, sources []protocol.BlockPos, dest protocol.BlockPos, want map[string]int, opts ...Option) Skill`
    - Name `haul`.
    - It loops: withdraw what is still wanted from each source in turn; if it carries anything, deposit all of it at `dest` and subtract what was deposited; otherwise idle for `haulPollTicks = 100`, sending idle inputs.
    - It is `Done` when every item's moved count reaches its target.
    - It fails after `maxRetries = 3` consecutive rejected or failed trips, with a coordinate-free reason.
    - It implements `Stopper` by delegating to the running sub-skill.
  - `recipes["oak_log"] = Source{Block: "oak_log", Tool: "axe"}`.
  - The `haul` template, v1. All fields are required and unknown fields are rejected:

    ```json
    {"template":"haul","items":{"cobblestone":16,"oak_log":16},
     "from":[{"x":0,"y":0,"z":0}],"to":{"x":0,"y":0,"z":0}}
    ```

    - `items` holds 1 to 8 entries. Each quantity is 1–256, and each key must be in `recipes`.
    - `from` holds 1 to 4 chests.
    - The gather template is unchanged.

- [ ] **Step 1: Write the failing tests:**
  - `TestHaulWaitsForItemsToAppear`: the sources start empty, and the sim fills them after 300 ticks. The haul ends `Done`, with 16 of each item in `dest`.
  - `TestHaulMakesSeveralTripsWhenItemsExceedOneLoad`: the inventory has room for 9 stacks, and the sources hold 10 stacks. At least two deposits; `Done`.
  - `TestWithdrawRetriesAfterARejection`: the first withdraw response is 49; the haul retries and ends `Done`. Three consecutive rejections give `Failed`, and the reason contains no digits from the positions.
  - `TestWithdrawFromAnEmptyChestIsDone`.
  - `TestHaulStopClosesAnOpenChest`.
  - `TestParseHaulTemplate`: accepts the example above; rejects unknown items, a quantity of 0 or 257, 0 or 5 sources, a missing `to`, and an extra field.
  - `TestGatherAcceptsOakLog`.
- [ ] **Step 2: Run them.** Expected: FAIL.
- [ ] **Step 3: Implement** `withdraw.go`, `haul.go`, the recipe, and `ParseTemplate`/`Expand` for `haul`.
  - `Expand` returns a single `NewHaul` skill.
  - `Template` gains `Items map[string]int`, `From []Chest` and `To *Chest`, with the existing fields left in place.
  - The task's `progress` for a haul is the total moved so far.
- [ ] **Step 4: Run the checks.** Expected: PASS.
- [ ] **Step 5: Update the README Tasks section** with the haul template, the log recipe, and an example.
- [ ] **Step 6: Commit** as `feat(crew): haul items between chests`, and `feat(crew): gather oak logs` for the recipe.

### Task 5: Parallel scene, demo script and local three-unit run

**Files:**
- Create: `minecraft/crew/lab/parallel-layout.sh`, sourced by the two scripts below, holding the layout constants
- Create: `minecraft/crew/lab/parallel-scene.sh`, `minecraft/crew/lab/parallel-demo.sh`
- Modify: `minecraft/crew/lab/compose.yaml`: services `crew-01`, `crew-02` and `crew-03`, with `CREW_ID` and `MC_USERNAME` Agate, Basalt and Cobble, status ports `127.0.0.1:8081`–`8083`, and the shared `TASKS_TOKEN` passthrough
- Modify: `minecraft/crew/README.md`, `minecraft/crew/docs/decisions.md` (names and numbering, chests as the only hand-off), and `docs/specs/2026-10-14-crew-design.md` (Waves table: a row noting that parallel units came ahead of 3b)

**Interfaces:**
- Consumes: the `scene.sh` conventions (lab argument forms `<compose-service>` and `k8s:<ns>/<pod>`, `--confirm-overwrite`, the unit-name check), and Task 4's templates.
- Produces:
  - `parallel-scene.sh [--confirm-overwrite] <lab> [agate] [basalt] [cobble]`, with names defaulting to Agate, Basalt and Cobble.
  - `parallel-demo.sh <compose|k8s:<ns>> [timeout-s]`. It submits the three tasks, polls each unit's `GET /v1/tasks` until all are finished, prints one line per unit (`<id> <name> <state> <reason> <seconds>`), and exits non-zero unless all three are `done`.

The layout, written as constants in `parallel-layout.sh`, all at y = 100 on a smooth_stone floor at y = 99, clears x −12..12, y 100..106, z −12..12:

| Thing | Where |
| --- | --- |
| Quarry (stone) | x 6..9, y 100..101, z −10..−8 (24 blocks) |
| Grove (oak_log trunks, 2 high) | trunks at x ∈ {−9, −7, −5, −3}, z ∈ {8, 10}, y 100..101 (16 logs) |
| Outbox A (Agate) | chest at (4, 100, −5) |
| Outbox B (Basalt) | chest at (−5, 100, 5) |
| Store (Cobble) | chest at (0, 100, 0) |
| Starts | Agate (5, 100, −7), Basalt (−6, 100, 7), Cobble (1, 100, 2) |
| Tools | Agate `wooden_pickaxe`; Basalt `stone_axe`, because a wooden axe takes about 30 ticks per log, which is the whole break budget, while a stone axe takes about 15; Cobble nothing. All three are cleared first |

- [ ] **Step 1: Write the scripts.**
  - `parallel-scene.sh` adds the tickingarea, clears the area, builds the floor, quarry, grove and chests, then `clear`s, `tp`s and `give`s each unit by quoted name, and removes the tickingarea.
  - `parallel-demo.sh` reads the token from `$TASKS_TOKEN` for compose, or for `k8s:` from Secret `crew-tasks` (key `token`) through `kubectl get secret … | base64 -d` inside the curl header, never printed.
  - For `k8s:` it uses three `kubectl port-forward` processes on 18081–18083, tracked by PID and killed on exit, with a `trap`.
  - It submits: Agate `gather_and_deposit` cobblestone 16 → outbox A; Basalt `gather_and_deposit` oak_log 16 → outbox B; Cobble `haul` {cobblestone: 16, oak_log: 16} from [A, B] to the store.
- [ ] **Step 2: Run `shellcheck` on all three files.** Expected: clean.
- [ ] **Step 3: Live run on the local lab.**
  1. `export TASKS_TOKEN=$(openssl rand -hex 16)`
  2. `docker compose … up -d --build`
  3. `parallel-scene.sh lab`
  4. `parallel-demo.sh compose 600`

  Expected: three `done` lines in under 10 minutes. Cobble's last `chest_contents` for the store is `{"cobblestone":16,"oak_log":16}`, and its withdraw logs show both outboxes empty. Then `docker compose … logs | grep -E '"(x|y|z)"'` prints nothing, and `send-command list` shows `Agate, Basalt, Cobble`.
- [ ] **Step 4: Fix anything the live run forces.** Each fix is its own `fix(crew): …` commit with a test that reproduces it. Hard cap: 6 fix-and-retry cycles, then stop and report.
- [ ] **Step 5: Update the docs** listed in Files.
- [ ] **Step 6: Commit** as `feat(crew): run three units in parallel on the lab`, plus a docs commit.

### Task 6: Chart renders a list of units; ship and prove on the cluster lab

**Files (jdw-deployments):**
- Modify: `charts/crew-lab/values.yaml`, replacing the single `crew.name`/`username` with `crew.units`
- Modify: `charts/crew-lab/templates/crew-deployment.yaml`, `crew-pvc.yaml`, `crew-service.yaml` (each ranges over `crew.units`), `crew-route.yaml` (backend `crew.route.unit`, default `crew-01`), and `_helpers.tpl`
- Modify: `charts/crew-lab/README.md` (units and names, the shared Secret, the parallel demo)
- Modify: `tools/tests/test-crew-route.sh`, if the route's backend name is asserted there

**Interfaces:**
- Consumes: crew image env `CREW_ID` and `MC_USERNAME` (Task 2), and the release containing Tasks 1–5.
- Produces the values shape:

  ```yaml
  crew:
    units:
      - id: crew-01
        name: Agate
      - id: crew-02
        name: Basalt
      - id: crew-03
        name: Cobble
    route: {enabled: true, host: crew.prd.jdwlabs.com, unit: crew-01}
    tasks: {enabled: true, secretName: crew-tasks, timeoutSeconds: 900}
    resources: {requests: {cpu: 20m, memory: 128Mi}, limits: {cpu: 100m, memory: 512Mi}}
  ```

  Each unit's Deployment, Service and PVC are named by `id`. `MC_USERNAME` is the unit's `name` and `CREW_ID` its `id`. Add a `preStop` only if Task 1 chose it.

- [ ] **Step 1: Write the failing chart check.** A `helm template` assertion, in `tools/tests` beside the route test, checks:
  - three Deployments named `crew-01` to `crew-03`, with `MC_USERNAME` Agate, Basalt and Cobble;
  - the route backend is `crew-01`;
  - the sum of `limits.cpu` across crew Deployments is ≤ 300m;
  - every `TASKS_TOKEN` references `crew-tasks`.
- [ ] **Step 2: Run it.** Expected: FAIL.
- [ ] **Step 3: Implement the chart change and bump the crew image tag** to the release with Tasks 1–5.
- [ ] **Step 4: Run the check, `helm lint` and the repo's CI scripts.** Expected: PASS.
- [ ] **Step 5: Create Secret `crew-tasks` in-cluster** from `openssl rand -hex 32`, never printed. Do this before the PR merges, so units start with their routes.
- [ ] **Step 6: Open the PR, wait for green, merge,** and wait for Argo CD `jdwillmsen-crew-lab-prd` to be Synced/Healthy. Delete Secret `crew-1-tasks` once nothing references it.
- [ ] **Step 7: Cluster proof.**
  1. `parallel-scene.sh --confirm-overwrite k8s:jdwillmsen-prd/<lab pod>`
  2. `parallel-demo.sh k8s:jdwillmsen-prd 600`

  Expected: three `done` lines, the store at `{"cobblestone":16,"oak_log":16}`, and no coordinate lines in any unit's logs.
- [ ] **Step 8: Rollout proof.** Run `kubectl rollout restart` twice, waiting between them, on all three Deployments. After each, `send-command list` shows `Agate, Basalt, Cobble` with no suffixes.
- [ ] **Step 9: Quota proof.** Record `kubectl -n jdwillmsen-prd get resourcequota tenant-quota` before and after. Expected: limits.cpu rose by ≤ 150m. After 04:00 UTC the next day, confirm the FWB backup Job succeeded.
