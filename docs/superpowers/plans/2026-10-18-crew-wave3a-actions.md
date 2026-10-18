# Crew Wave 3a — Actions, Skills and a Local Task Runner Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** On the lab, an operator submits the task "mine 16 cobblestone, deposit in the chest" to crew-1, and crew-1 completes it with no LLM and no operator input after submission.

**Architecture:** Crew learns to act. A new world store keeps the blocks crew receives, independent of the camera. Belief gains the unit's inventory. An actuator owns every outgoing action packet and the unit's predicted pose. A pathfinder plans walks over the world store. Three typed skills (`Navigate`, `MineBlock`, `DepositChest`) drive the actuator one tick at a time. A task runner expands JSON task templates into skill sequences from a recipe table and runs them in order. Operators submit tasks through crew's own status API, behind a bearer token. `crew-control` (the queue, SSE and authentication in front of crew) is Wave 3b and gets its own plan.

**Tech Stack:** Go 1.27 (single module `github.com/jdwillmsen/gameops`), `github.com/sandertv/gophertunnel` v1.62.0, `github.com/go-gl/mathgl/mgl32`, `github.com/df-mc/dragonfly` v0.11.5 (block registry only, already used by the camera), `net/http`, Docker Compose for the local lab, the `crew-lab` chart in `jdw-deployments`.

**Spec:** `docs/specs/2026-10-14-crew-design.md` (Waves table and "Wave 3 outline"). Facts about current Bedrock come from Task 1's spike record, `docs/specs/2026-10-18-crew-actions-spike.md`. Every later task reads it.

## Why 3a and 3b

The outline's Wave 3 is two independent pieces of work. Crew acting in the world carries all the protocol risk and ends in something an operator can watch. `crew-control` needs a Flyway migration in `jdwlabs/platform`, authentication and a new chart, and depends on none of the protocol work. Splitting them puts the watchable result first. This is the planner's call; the Task 7 docs step records it in the design record's Waves table as rows 3a and 3b.

## Global Constraints

- Lab only. Crew never connects to FWB. Nothing in this wave touches `charts/minecraft-fwb`.
- No live LLM, model client or chat handling anywhere in `minecraft/crew` (design D2).
- Do not change any file under `minecraft/afkbot/`, `minecraft/agent/`, `minecraft/mcmap/`, `minecraft/bridge/` or `internal/presenceapi/`. Crew imports `minecraft/agent/pkg/*` read-only.
- Never log a position, coordinate, waypoint or seed. Positions appear only in status API response bodies and the camera websocket. Task JSON carries lab coordinates, so task logs record the task ID, template and state, never the body.
- Lab coordinates may appear in the lab scene script and test fixtures. FWB coordinates, seeds and player names appear nowhere.
- The task endpoints are off unless `TASKS_TOKEN` is set. Every task request needs `Authorization: Bearer <TASKS_TOKEN>`; reads of `/v1/state` stay as they are.
- `STATUS_ADDR` still defaults to loopback.
- Overworld only. A skill whose unit is in another dimension fails with `only the overworld is supported`.
- The tick is 50 ms. One `PlayerAuthInput` per tick while any skill is active, with a strictly increasing `Tick`.
- Cheats are used only by the lab scene script (Task 7), never by crew itself. Crew never sends a command.
- Comments explain *why*, never *what*; match crew's density. No URLs, ticket IDs or names in comments.
- Every commit carries `Co-Authored-By:` and `Assisted-by: <agent>:<model-id>` trailers. Branches are `feat/JDW-<n>-crew-<topic>`, using the ticket key per task from the JDW-59 epic. Merges are rebase-only.

## Review Focus

1. **The unit dies or the session ends mid-task.** The running task fails with reason `session ended` or `unit died`. It does not resume blindly in a new session, and queued tasks wait for the next spawn. (Task 6: `TestRunnerFailsTheRunningTaskWhenTheSessionEnds`, `TestRunnerWaitsForSpawnBeforeStartingQueuedTasks`)
2. **The server corrects the unit's position** (`CorrectPlayerMovePrediction`). The actuator adopts the server's pose, and `Navigate` replans from it instead of walking the old path into a wall. (Task 3: `TestCorrectionResetsThePose`; Task 5: `TestNavigateReplansAfterACorrection`)
3. **Blocks break but the count doesn't rise** (inventory full, a drop out of reach, or a wrong tool). `MineBlock` counts progress from the inventory, not from blocks broken. It fails with `no pickup after 3 breaks` instead of mining forever. (Task 5: `TestMineBlockCountsFromInventoryAndGivesUp`)
4. **The chest is missing, won't open, or is full.** `DepositChest` fails within 5 s of each unanswered step, with a reason that names the step. It never hangs. (Task 5: `TestDepositFailsWhenTheChestDoesNotOpen`, `TestDepositFailsWhenTheChestIsFull`)
5. **No reachable stone, or an unknown block between crew and its goal.** `MineBlock` fails with `no reachable stone within 24 blocks`. The pathfinder treats unknown blocks as solid, so crew never walks into unloaded terrain. (Task 4: `TestUnknownBlocksAreNotWalkable`; Task 5: `TestMineBlockFailsWithNoReachableSource`)

## Dispatch map

```
Task 1 (spike: actions on the lab) ──► Task 2 (world store + inventory) ──► Task 3 (actuator) ──┐
                                                    └──► Task 4 (pathfinder) ─────────────────┴─► Task 5 (skills) ──► Task 6 (task runner + API) ──► Task 7 (wiring, scene, live run, docs, chart)
```

Task 4 may run beside Task 3 once Task 2 has merged. Implementers use Sonnet; per-task reviewers use Sonnet; the final whole-branch review uses Opus.

**Owner prerequisite before Task 7's cluster step:** none. The lab already runs offline (`MC_OFFLINE`), so crew-1 needs no account.

---

### Task 1: Spike — prove crew can walk, break a block and fill a chest on the lab

Timebox: 2 working sessions. The probe is throwaway and is not merged; the record and the fixtures are.

**Files:**
- Create (throwaway, never committed): `minecraft/crew/cmd/crew-probe/main.go`
- Create: `docs/specs/2026-10-18-crew-actions-spike.md`
- Create: `minecraft/crew/internal/act/testdata/*.bin` (recorded server-to-client packets, lab only)

**Interfaces:**
- Produces: the spike record. Tasks 3 and 5 take their exact packet sequences, speeds and timings from it. Where the record and this plan disagree, the record wins, and the implementer notes the difference in their task's PR.

- [ ] **Step 1: Read the movement settings.** Log `conn.GameData().PlayerMovementSettings` on the local lab (`docker compose -f minecraft/crew/lab/compose.yaml up -d lab`). Record `ServerAuthoritativeBlockBreaking` and `RewindHistorySize`.
- [ ] **Step 2: Walk.** Send one `PlayerAuthInput` per 50 ms tick with `InputFlagUp`, a `MoveVector` of `{0,1}`, a `Position` advanced by the walk step along the yaw, `Delta` set to that step, and an increasing `Tick`. Find the largest step per tick the server accepts on flat ground without a `CorrectPlayerMovePrediction`; expect about 0.2159 blocks per tick. Then jump onto a 1-block step: `InputFlagStartJumping` and `InputFlagJumping`, with vertical motion matching gravity 0.08 and drag 0.98 per tick, initial jump velocity 0.42. Record whether the server needs `InputFlagVerticalCollision` and the `OnGround` handling.
- [ ] **Step 3: Break stone with a wooden pickaxe.** Run `send-command give @a wooden_pickaxe` and `send-command setblock` with a stone block next to the unit. Select the pickaxe with `MobEquipment`, then send `BlockActions` through `PlayerAuthInput` with `InputFlagPerformBlockActions`. The expected order is `PlayerActionStartBreak`, `PlayerActionContinueDestroyBlock` on each tick, then `PlayerActionStopBreak` and `PlayerActionPredictDestroyBlock`. Expect about 23 ticks for stone with a wooden pickaxe. Record the exact actions and tick count the server accepts, and whether `ItemStackRequest` with `MineBlockStackRequestAction` is required for tool durability.
- [ ] **Step 4: Pick up.** Stand within 1 block of the drop. Record the `TakeItemActor` and `InventorySlot` (or `InventoryContent`) packets that arrive.
- [ ] **Step 5: Deposit.** Use an `InventoryTransaction` with `UseItemOnBlock` (`UseItemActionClickBlock`) on a chest within 4 blocks. Record the `ContainerOpen` (`WindowID`, `ContainerType`) and the chest's `InventoryContent`. Then send an `ItemStackRequest` with a `PlaceStackRequestAction`, from the inventory slot (`ContainerCombinedHotBarAndInventory` and the slot's `StackNetworkID`) to the chest (`ContainerLevelEntity`, slot 0, `StackNetworkID` 0). Record the `ItemStackResponse` and the follow-up `InventorySlot` packets, then close with `ContainerClose`.
- [ ] **Step 6: Record fixtures.** Save the server-to-client packets of steps 3-5 as length-prefixed packet bytes under `minecraft/crew/internal/act/testdata/`: `break_stone.bin`, `pickup.bin`, `chest_open.bin`, `chest_deposit.bin`. Use the lab only, with coordinates rewritten to a fixed lab origin.
- [ ] **Step 7: Write the record** at `docs/specs/2026-10-18-crew-actions-spike.md`. Include:
  - the movement mode
  - the walk step, the jump physics, the break action order and tick count for stone with a wooden pickaxe
  - pickup range
  - the deposit sequence
  - anything that drew a correction or a kick
  - a go/no-go for Tasks 2-7

  A no-go stops this plan, and the record says what blocks it.
- [ ] **Step 8: Commit the record and fixtures** (`docs(crew): record the actions spike`). Delete the probe.

---

### Task 2: World store and inventory belief

**Files:**
- Create: `minecraft/crew/internal/world/blocks/store.go`, `store_test.go`
- Create: `minecraft/crew/internal/belief/inventory.go`, `inventory_test.go`
- Modify: `minecraft/crew/internal/belief/belief.go` (Snapshot gains `Inventory`), `minecraft/crew/internal/camera/hub.go` (stop writing `SubChunkRequest`)
- Test: `minecraft/crew/internal/camera/hub_test.go` (adjust the request assertions)

**Interfaces:**
- Consumes: `subchunk.Decode(payload []byte) (subchunk.SubChunk, error)` and `(*subchunk.Layer).Block(x, y, z int) uint32`. Names come from an injected `Names interface{ BlockName(hash uint32) (string, bool) }`; cmd/crew passes the camera's `*camera.NetworkDecoder`.
- Produces:
  - `blocks.New(names Names, maxSubChunks int) *Store`
  - `(*Store) Apply(pk packet.Packet, w PacketWriter)`: handles `LevelChunk` by writing the `SubChunkRequest`, plus `SubChunk`, `UpdateBlock` (layer 0), `UpdateSubChunkBlocks` and `ChangeDimension` (which clears the store)
  - `(*Store) Block(pos protocol.BlockPos) (name string, known bool)`, where names carry no `minecraft:` prefix and air is `air`
  - `(*Store) Reset()`
  - `blocks.SubChunkRequest(*packet.LevelChunk) *packet.SubChunkRequest`, moved here from the hub unchanged
  - `belief.Item{Slot int; Name string; Count int; StackNetworkID int32}`
  - `Snapshot.Inventory []Item`, which covers inventory window 0, slots 0-35, non-empty slots only, in slot order
  - `(*Tracker) ItemNames(entries []protocol.ItemEntry)`, called once per session from `GameData().Items`

- [ ] **Step 1: Write the failing store tests.**
  - `TestStoreAnswersBlocksFromSubChunks` feeds `testdata` sub-chunk fixtures from `internal/world/subchunk/testdata` through `SubChunk`. It asserts `Block` returns the decoded names at three positions and `known == false` outside any received sub-chunk.
  - `TestUpdateBlockOverwritesLayerZero`
  - `TestChangeDimensionForgetsEverything`
  - `TestStoreEvictsTheFarthestSubChunkPastTheCap`, with a cap of 4
  - `TestLevelChunkWritesOneSubChunkRequest`, using the hub tests' recording `sink`
- [ ] **Step 2: Run `go test ./minecraft/crew/internal/world/blocks/...`.** Expect FAIL (package missing).
- [ ] **Step 3: Implement `Store`.** Use a map from sub-chunk position to the decoded layer-0 hashes, read under an RWMutex. Eviction drops the sub-chunk farthest from the most recent `LevelChunk` position. Move `subChunkRequest` from the hub and export it.
- [ ] **Step 4: Stop the hub writing its own requests.** It still decodes every `SubChunk` that arrives, so the camera keeps working with the store as the only requester. Update `hub_test.go`'s request assertions to expect none from the hub.
- [ ] **Step 5: Write the failing inventory tests.**
  - `TestInventoryContentFillsTheSnapshot`: window 0, three stacks, one empty slot, which is omitted.
  - `TestInventorySlotReplacesOneSlot`
  - `TestOtherWindowsDoNotTouchTheInventory`
  - `TestResetForgetsTheInventory`
  - `TestUnknownRuntimeIDsAreNamedByID`: a missing item entry yields `item#<id>`, never a panic.
- [ ] **Step 6: Implement inventory tracking** in `belief/inventory.go`, applied from `Tracker.Apply`.
- [ ] **Step 7: Run `go test ./minecraft/crew/...`.** Expect PASS.
- [ ] **Step 8: Commit** (`feat(crew): keep the blocks and inventory crew receives`).

---

### Task 3: Actuator

**Files:**
- Create: `minecraft/crew/internal/act/act.go`, `pose.go`, `break.go`, `container.go`, and `act_test.go`, `container_test.go`

**Interfaces:**
- Consumes: Task 2's `belief.Snapshot` (including `Inventory`), the spike record's sequences, and `PacketWriter interface{ WritePacket(packet.Packet) error }`.
- Produces:
  - `act.Pose{Feet mgl32.Vec3; Yaw, Pitch float32; OnGround bool}`. Feet height is eye height minus 1.62.
  - `act.Input{Forward float32; Yaw, Pitch float32; Jump bool; Break *BreakAction; Use *protocol.UseItemTransactionData}`
  - `act.BreakAction{Pos protocol.BlockPos; Face int32; Phase BreakPhase}`, with `BreakStart`, `BreakContinue` and `BreakFinish`
  - `act.Solid interface{ Solid(protocol.BlockPos) bool }`, defined here by its consumer
  - `act.New(w PacketWriter, runtimeID uint64, start Pose, solid Solid) *Actuator`
  - `(*Actuator) Tick(in Input) error`: sends exactly one `PlayerAuthInput` and advances the pose by the spike's walk and jump physics, colliding against `solid`.
  - `(*Actuator) Pose() Pose`
  - `(*Actuator) Apply(pk packet.Packet)`: adopts `CorrectPlayerMovePrediction` and own-ID `MovePlayer`; tracks `ContainerOpen`, `ContainerClose` and `ItemStackResponse`.
  - `(*Actuator) SelectHotbar(slot int, item belief.Item) error`, which sends `MobEquipment`
  - `(*Actuator) Container() (open bool, windowID byte, pos protocol.BlockPos)`
  - `(*Actuator) Deposit(from belief.Item, count int, toSlot byte) (requestID int32, err error)`
  - `(*Actuator) Response(requestID int32) (ok bool, done bool)`
  - `(*Actuator) CloseContainer() error`
  - The actuator is the only writer of action packets. It is safe to call from the skill goroutine while `Apply` runs on the read loop.

Task 4's `path.NewSolid` satisfies `act.Solid`. Until it lands, Task 3's tests pass a fake, so the two tasks stay parallel.

- [ ] **Step 1: Write the failing tests.**
  - `TestTickSendsOneInputWithIncreasingTick`
  - `TestWalkingAdvancesByTheSpikeStep`, using the walk step from the record
  - `TestJumpRisesOneBlockAndLands`, onto a fake 1-block step
  - `TestCorrectionResetsThePose`
  - `TestBreakSendsTheRecordedActionOrder`, comparing against `testdata/break_stone.bin`'s expected client actions in the record
  - `TestDepositBuildsAPlaceRequestFromTheInventorySlot`, asserting the container IDs, slot, count and `StackNetworkID`
  - `TestResponseReportsAFailedRequest`
  - `TestContainerOpenAndCloseAreTracked`
- [ ] **Step 2: Run `go test ./minecraft/crew/internal/act/...`.** Expect FAIL.
- [ ] **Step 3: Implement the actuator.** Use the spike record's values. Keep the physics in `pose.go`, the break phases in `break.go` and the container calls in `container.go`.
- [ ] **Step 4: Run the tests.** Expect PASS.
- [ ] **Step 5: Commit** (`feat(crew): add the actuator for walking, breaking and deposits`).

---

### Task 4: Pathfinder

**Files:**
- Create: `minecraft/crew/internal/path/path.go`, `passable.go`, `path_test.go`

**Interfaces:**
- Consumes: `Blocks interface{ Block(protocol.BlockPos) (string, bool) }`, satisfied by Task 2's `*blocks.Store`.
- Produces:
  - `path.Find(b Blocks, from, to protocol.BlockPos, maxNodes int) ([]protocol.BlockPos, error)`: feet positions from `from` (excluded) to `to` (included).
  - `path.ErrNoPath`
  - `path.Standable(b Blocks, feet protocol.BlockPos) bool`
  - `path.NewSolid(b Blocks) interface{ Solid(protocol.BlockPos) bool }` — satisfies Task 3's `act.Solid`; solid means known and not passable
  - `path.Passable(name string) bool`

Rules:
- A feet cell is standable when its feet and head blocks are passable and the block below is known and solid.
- Moves go in 8 directions. A diagonal never cuts a corner: both orthogonal neighbours must be standable at the same height.
- Crew steps up at most 1 block and drops at most 3.
- Unknown blocks count as solid.
- The search is A\* with an octile-distance heuristic. `maxNodes` defaults to 4096.

The passable set is a list in `passable.go`: `air`, `short_grass`, `tall_grass`, `fern`, `large_fern`, `dandelion`, `poppy`, `torch`, `snow_layer`, `deadbush`, `sweet_berry_bush`, `vine`, `glow_lichen` and the `*_sapling` names. Water and lava are not passable.

- [ ] **Step 1: Write the failing tests.** Use a map-backed fake `Blocks`.
  - `TestFindsAStraightPathOnFlatGround`
  - `TestStepsUpOneBlockButNotTwo`
  - `TestDropsUpToThreeBlocks`
  - `TestDiagonalsDoNotCutCorners`
  - `TestUnknownBlocksAreNotWalkable`
  - `TestReturnsErrNoPathPastMaxNodes`
  - `TestWaterIsNotPassable`
- [ ] **Step 2: Run `go test ./minecraft/crew/internal/path/...`.** Expect FAIL.
- [ ] **Step 3: Implement `Find`, `Standable`, `Passable` and `NewSolid`.**
- [ ] **Step 4: Run the tests.** Expect PASS.
- [ ] **Step 5: Commit** (`feat(crew): plan walks over the blocks crew knows`).

---

### Task 5: Skills

**Files:**
- Create: `minecraft/crew/internal/skill/skill.go`, `navigate.go`, `mine.go`, `deposit.go`, `sim_test.go` (a small simulated world shared by the tests), `navigate_test.go`, `mine_test.go`, `deposit_test.go`

**Interfaces:**
- Consumes: Task 3's `*act.Actuator` behind the interface below, Task 4's `path.Find`, `path.Standable` and `path.Passable`, Task 2's `belief.Snapshot` and `*blocks.Store`.
- Produces the design record's skill interface, with the actuator narrowed to what skills use:

```go
type Status int // Running, Done, Failed

type Actuator interface {
	Pose() act.Pose
	Tick(act.Input) error
	SelectHotbar(slot int, item belief.Item) error
	Container() (open bool, windowID byte, pos protocol.BlockPos)
	Deposit(from belief.Item, count int, toSlot byte) (int32, error)
	Response(requestID int32) (ok, done bool)
	CloseContainer() error
}

type Skill interface {
	Name() string
	Ready(belief.Snapshot) error
	Step(ctx context.Context, a Actuator) (Status, error)
}
```

- `skill.NewNavigate(world path.Blocks, self func() belief.Snapshot, goal protocol.BlockPos, within float64) Skill`: done when the feet are within `within` blocks of `goal`. It replans when the pose moves more than 0.5 blocks off the planned segment, and it fails after 3 consecutive replans that make no progress.
- `skill.NewMineBlock(world path.Blocks, self func() belief.Snapshot, block, item, tool string, qty, radius int) Skill`
  - `tool` is a name suffix: `pickaxe` matches any `*_pickaxe`.
  - It finds the nearest known `block` within `radius` (default 24) that has a standable cell within reach (4.5 blocks from the eye).
  - It walks there with an inner Navigate, selects a hotbar item matching `tool`, and breaks the block over the recorded tick count.
  - It waits up to 40 ticks for pickup and counts `item` in the inventory against the count at start.
  - It is done at `qty`. It fails with `no pickup after 3 breaks` or `no reachable <block> within <radius> blocks`.
  - `Ready` fails with `no <tool> in inventory` when none is held.
- `skill.NewDepositChest(world path.Blocks, self func() belief.Snapshot, chest protocol.BlockPos, item string, qty int) Skill`
  - It walks within 3 blocks, opens the chest, and moves stacks of `item` until `qty` have been placed, filling chest slots 0-26 in order.
  - Then it closes the chest.
  - Each wait (open, response) times out after 100 ticks (5 s) with a reason naming the step.
  - It fails with `chest is full` when every chest slot holds another item or a full stack.
  - After closing, it logs `chest_contents` at info with item names and counts only, taken from the chest `InventoryContent` the server sent. That is the live check's independent witness.

- [ ] **Step 1: Write `sim_test.go`.** It is a map-backed world plus a fake `Actuator` that moves the pose by `Input`, breaks a block after the recorded tick count and turns the broken block into an inventory item. It answers deposits from a 27-slot chest model and can be told to drop responses, refuse pickups or inject a correction.
- [ ] **Step 2: Write the failing skill tests.**
  - `TestNavigateArrivesOnFlatGround`
  - `TestNavigateReplansAfterACorrection`
  - `TestNavigateFailsWithNoPath`
  - `TestMineBlockCollectsSixteenCobblestone`, with stone around the unit and a wooden pickaxe in slot 0
  - `TestMineBlockCountsFromInventoryAndGivesUp`
  - `TestMineBlockFailsWithNoReachableSource`
  - `TestMineBlockIsNotReadyWithoutAPickaxe`
  - `TestDepositMovesSixteenIntoTheChest`
  - `TestDepositFailsWhenTheChestDoesNotOpen`
  - `TestDepositFailsWhenTheChestIsFull`
  - `TestSkillsRefuseOtherDimensions`
- [ ] **Step 3: Run `go test ./minecraft/crew/internal/skill/...`.** Expect FAIL.
- [ ] **Step 4: Implement the three skills.**
- [ ] **Step 5: Run the tests.** Expect PASS.
- [ ] **Step 6: Commit** (`feat(crew): add navigate, mine and deposit skills`).

---

### Task 6: Task templates, runner and API

**Files:**
- Create: `minecraft/crew/internal/task/template.go`, `recipes.go`, `runner.go`, `api.go`, and `template_test.go`, `runner_test.go`, `api_test.go`
- Modify: `minecraft/crew/internal/config/config.go` (+ test): `TASKS_TOKEN` (string, empty disables the endpoints), `TASK_TIMEOUT_S` (`intInRange`, default 900, 30-3600)

**Interfaces:**
- Consumes: Task 5's constructors and `skill.Skill`, `belief.Snapshot`.
- Produces:
  - **Template JSON (v1).** Field names are fixed here:
    ```json
    {"template": "gather_and_deposit", "item": "cobblestone", "qty": 16,
     "chest": {"x": 0, "y": 0, "z": 0}}
    ```
    `qty` runs from 1 to 256. Unknown templates, unknown items, and fields the template doesn't use are rejected with 400.
  - **Recipe table.** `recipes.go` holds `map[string]Source{Block string; Tool string}`, with `"cobblestone": {Block: "stone", Tool: "pickaxe"}` as the only entry.
  - **Expansion.** `gather_and_deposit` expands to `[MineBlock(source.Block, item, source.Tool, qty, 24), DepositChest(chest, item, qty)]`.
  - **Task record.** `task.Task{ID string; Template string; State string; Reason string; Skill string; Progress int; Created, Updated time.Time}`.
    - The ID is 16 random hex characters.
    - `State` is one of `queued`, `running`, `done`, `failed` or `cancelled`.
    - `Progress` is the item count for the current skill.
  - **Runner.**
    - `task.NewRunner(newSkills func(Template) ([]skill.Skill, error), act skill.Actuator, self func() belief.Snapshot, timeout time.Duration, log *logging.Logger) *Runner`
    - `(*Runner) Submit(t Template) (Task, error)`, `Cancel(id string) (Task, error)`, `List() []Task`
    - `Run(ctx)` ticks every 50 ms. It runs one task at a time, `Ready` first, then `Step` each tick.
    - `SessionEnded()` and `Died()` fail the running task, with reasons `session ended` and `unit died`.
    - `SetActuator(skill.Actuator)` is called on each new session. Queued tasks start only once a session is spawned.
    - The runner keeps the last 50 finished tasks in memory, oldest dropped first.
  - **API.** `task.Routes(mux *http.ServeMux, r *Runner, token string)`:
    - `POST /v1/tasks` takes a template and returns 202 with the Task.
    - `GET /v1/tasks` returns the list.
    - `POST /v1/tasks/{id}/cancel`.
    - A missing or wrong token gets 401, compared in constant time. An unknown ID gets 404. With an empty token, the routes are not registered (404).
    - Logs: `task_submitted`, `task_started`, `task_finished` with `{id, template, state, reason}` only.

- [ ] **Step 1: Write the failing tests.**
  - `TestTemplateRejectsUnknownFieldsAndItems`
  - `TestQtyOutOfRangeIsRejected`
  - `TestGatherAndDepositExpandsToMineThenDeposit`
  - `TestRunnerRunsTasksInOrder`
  - `TestRunnerFailsTheRunningTaskWhenTheSessionEnds`
  - `TestRunnerWaitsForSpawnBeforeStartingQueuedTasks`
  - `TestRunnerTimesOutATask`
  - `TestCancelStopsTheRunningSkill`
  - `TestTaskLogsCarryNoBody`: the chest coordinates never appear in a log line.
  - `TestTasksNeedTheBearerToken`
  - `TestTasksAreOffWithoutAToken`
  - `TestTasksTokenAndTimeoutConfig`
- [ ] **Step 2: Run `go test ./minecraft/crew/internal/task/... ./minecraft/crew/internal/config/...`.** Expect FAIL.
- [ ] **Step 3: Implement the template, recipes, runner, routes and config.**
- [ ] **Step 4: Run the tests.** Expect PASS.
- [ ] **Step 5: Commit** (`feat(crew): run gather-and-deposit tasks from a token-guarded API`).

---

### Task 7: Wire it in, prove it live, document it, ship it to the cluster lab

**Files:**
- Modify: `minecraft/crew/cmd/crew/main.go`, `camera.go`, and the `main_test.go` additions
  - The session builds the store, actuator and runner wiring, and feeds `store.Apply`, `actuator.Apply` and `tracker.Apply` from the read loop.
  - The read loop calls `runner.SessionEnded()` on return and `runner.Died()` on death.
  - `statusHandler` mounts `task.Routes`.
- Create: `minecraft/crew/lab/scene.sh`. It takes the lab compose service name as its only argument. It runs:
  - `fill -8 100 -8 8 106 8 air` (clears any terrain above the platform)
  - `fill -8 99 -8 8 99 8 stone` (the platform)
  - `fill 3 100 -3 6 101 3 stone` (the quarry)
  - `setblock -4 100 0 chest`
  - `tp @a 0 100 0`
  - `give @a wooden_pickaxe`
  - `clear @a cobblestone`

  These are lab coordinates, above the default spawn's usual terrain.
- Modify: `minecraft/crew/README.md` (Tasks section: env vars, the template, a curl example), `minecraft/crew/docs/decisions.md` (record the 3a/3b split, the token-guarded local API, unknown-is-solid, and inventory-counted progress), `docs/specs/2026-10-14-crew-design.md` (Waves table: rows 3a and 3b), root `README.md` Layout table (add `minecraft/crew`)
- Modify in `jdw-deployments` (separate PR): `charts/crew-lab` gains `TASKS_TOKEN` from a Secret the chart references by name, `crew-1-tasks`. The chart does not create the Secret: the owner creates it in a separate terminal (the command goes in the chart README).

**Interfaces:**
- Consumes: every earlier task's Produces block.

- [ ] **Step 1: Write the failing test `TestSessionWiresTasksToTheReadLoop`.** Using the existing session test seams, a `ContainerOpen` from the read loop must reach the actuator, and ending the session must fail the running task.
- [ ] **Step 2: Run `go test ./minecraft/crew/cmd/crew/...`.** Expect FAIL.
- [ ] **Step 3: Wire the session and routes.**
- [ ] **Step 4: Run `go test ./minecraft/crew/...` and `go vet ./minecraft/crew/...`.** Expect PASS.
- [ ] **Step 5: Live run on the local lab.**
  1. `docker compose -f minecraft/crew/lab/compose.yaml up -d --build`
  2. `minecraft/crew/lab/scene.sh lab`
  3. `curl -H "Authorization: Bearer $TASKS_TOKEN" -d '{"template":"gather_and_deposit","item":"cobblestone","qty":16,"chest":{"x":-4,"y":100,"z":0}}' localhost:8080/v1/tasks`

  Expect `GET /v1/tasks` to show `done` within 5 minutes. Bedrock has no command that reads a chest, so the witness is the server's own chest contents: the `chest_contents` log line must show 16 cobblestone. Record the wall time, and run `docker compose logs crew-1 | grep -E '"(x|y|z)"'`, which must print nothing.
- [ ] **Step 6: Live failure checks.**
  - Run `send-command kill @a` mid-task. Expect `failed`, `unit died`.
  - Break the chest and resubmit. Expect `failed` with the chest-open reason within 10 s.
- [ ] **Step 7: Update the docs** as listed in Files.
- [ ] **Step 8: Commit** (`feat(crew): wire tasks into the session and add the lab scene`) and the docs (`docs(crew): document tasks and record the 3a/3b split`).
- [ ] **Step 9: Cluster.** After the crew release, open the `jdw-deployments` PR bumping `charts/crew-lab` to the new crew tag and adding the `TASKS_TOKEN` Secret reference. Hand the owner the `kubectl create secret` command for `crew-1-tasks` to run in their own terminal. Once synced, run `scene.sh` against the cluster lab's console and repeat Step 5's task with `kubectl port-forward`.
