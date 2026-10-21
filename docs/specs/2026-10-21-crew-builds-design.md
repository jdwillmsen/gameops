# Crew: builds (hut, wall, farm plot)

**Status:** proposed
**Date:** 2026-10-21
**Epic:** JDW-59. **Ticket:** JDW-118 (Crew builds).
**Builds on:**
- `docs/specs/2026-10-14-crew-design.md` (deterministic runtime, lab only)
- `docs/specs/2026-10-19-crew-console-and-control-design.md` (step 4, "Builds")
- `docs/specs/2026-10-20-crew-jobs-and-world-ledger-design.md` (jobs, ledger places, stock and scheduler)

## Why

Today crew units mine, chop, haul and inspect chests. An operator can ask bots to stock 32 oak logs or dig a shaft, but cannot tell them to construct anything.

Step 4 of the roadmap introduces declarative builds: an operator chooses a blueprint, a target place and an orientation. `crew-control` checks ledger stock, gathers missing materials with existing jobs, partitions construction steps across free units, and verifies the finished structure against the blueprint.

## Owner decisions

- **First builds:** a hut, a wall, and a farm plot.
- **Lab only:** operates exclusively on the lab Bedrock server.
- **No LLM at runtime:** builds are deterministic, recipe-driven declarative plans.

## 1. Build template format

A blueprint is a small, versioned JSON document defining a 3D block volume via horizontal 2D layers and a block palette.

### Schema

```json
{
  "version": 1,
  "name": "hut",
  "size": {"x": 5, "y": 4, "z": 5},
  "palette": {"0": "air", "1": "cobblestone", "2": "oak_planks", "3": "oak_door", "4": "glass"},
  "layers": [
    ["11111", "11111", "11111", "11111", "11111"],
    ["12221", "20002", "20002", "20002", "12321"],
    ["12421", "40004", "20002", "40004", "12321"],
    ["22222", "22222", "22222", "22222", "22222"]
  ]
}
```

- `version`: format version (`1`).
- `name`: slug identifier.
- `size`: dimensions (`x, y, z`).
- `palette`: maps single characters to Bedrock block names. Key `"0"` is always `"air"`.
- `layers`: horizontal cross-sections from `y = 0` (base) to `y = size.y - 1` (top), array of strings along `z`, indexed along `x`.
- **Location:** blueprints are embedded in `crew-control` via `//go:embed blueprints/*.json`. They are immutable and version-controlled with the service.

### Placement and rotation

The job specifies world origin `(X0, Y0, Z0)` and rotation `R ∈ {0°, 90°, 180°, 270°}` (`north`, `east`, `south`, `west`). Local coordinates `(u, v, w)` map to world `(x, y, z)` with uniform height `y = Y0 + v`:
- `0°`: `x = X0 + u`, `z = Z0 + w`
- `90°`: `x = X0 + size.z - 1 - w`, `z = Z0 + u`
- `180°`: `x = X0 + size.x - 1 - u`, `z = Z0 + size.z - 1 - w`
- `270°`: `x = X0 + w`, `z = Z0 + size.x - 1 - u`

Directional block states rotate by `R`.

### The three blueprints

1. **Hut (5×5×4):**
   - `y = 0` (Floor): 5×5 solid `cobblestone`.
   - `y = 1` (Lower walls): `cobblestone` corners, `oak_planks` walls, door opening at `(2, 0)` with lower `oak_door`, 3×3 hollow interior.
   - `y = 2` (Upper walls): `cobblestone` corners, `oak_planks` walls, `glass` windows at `(0, 2)` and `(4, 2)`, upper `oak_door`.
   - `y = 3` (Roof): 5×5 solid ceiling of `oak_planks`.
2. **Wall (N×1×3):**
   - Repeatable 8×1×3 segment:
     - `y = 0` & `y = 1`: 8×1 solid `cobblestone`.
     - `y = 2`: alternating `cobblestone` and `air` (`10101011`) for battlements.
   - Tiled along the target axis for runs of length `N`.
3. **Farm plot (9×9×2):**
   - `y = 0` (Base): central `water` block at `(4, 0, 4)` in a 9×9 dirt basin with an `oak_log` border.
   - `y = 1` (Farmland): 9×9 `farmland` grid hydrated by the central water block (hydrates up to 4 blocks horizontally in all directions). The water block is capped with a `waterlily` or `slab` to prevent bots falling in.

## 2. New unit skills

Building requires three new skills in `minecraft/crew/internal/skill/`.

### `place_block`

Places an inventory item against an adjacent existing block face.

#### Bedrock protocol mechanics (gophertunnel)
Placing a block sends an `InventoryTransaction` packet with `UseItemTransactionData`:
- `ActionType`: `protocol.UseItemActionClickBlock` (0).
- `TriggerType`: `protocol.TriggerTypePlayerInput`.
- `BlockPosition`: coordinate of the adjacent existing block (`against`), **not** the empty target cell.
- `BlockFace`: clicked face on `against` (`0` -Y, `1` +Y, `2` -Z, `3` +Z, `4` -X, `5` +X).
- `ClickedPosition`: hit vector on the clicked face (e.g. `{0.5, 1.0, 0.5}` for top).
- `HotBarSlot` & `HeldItem`: active slot and item instance matching the item's runtime ID.
- `Position`: player eye position (`feet.Add(Vec3{0, 1.62, 0})`).
- `BlockRuntimeID`: runtime hash of the `against` block from `UpdateBlock`.
- `ClientPrediction`: `protocol.ClientPredictionSuccess`.

The unit selects the hotbar slot (`SelectHotbar`), aims yaw and pitch (`lookAt`), and checks reach distance (≤ 4.5 blocks).

#### Failure modes
- **Occupied:** destination contains an entity or solid block; pauses and retries.
- **Out of reach:** eye distance > 4.5 blocks; navigates to an adjacent valid stand cell.
- **No support:** target cell lacks an adjacent solid face; planner orders dependencies so support is placed first.
- **Wrong item:** hotbar slot empty or item mismatch; fails with `item missing`.

### `fetch`

Multi-item withdrawal extending `withdraw` and `haul`. Given `map[string]int`:
1. Navigates to the chest place (`navigate`).
2. Opens container (`OpenChest`).
3. Executes slot withdrawals (`Withdraw`) into hotbar and inventory.
4. Closes container (`CloseContainer`).
5. Attaches a `seen` observation reporting remaining chest stock to the ledger.

### `pillar_up` (scaffolding)

For placing blocks beyond ground reach (Y ≥ 3 above floor):
1. Unit walks to the pillar cell and jumps (`Input{Jump: true}`).
2. At jump apex (clearance ≥ 1.0), calls `PlaceOnTop(feet, slot, "dirt", hash)`.
3. Unit lands on top at Y+1; repeats to working height.
4. **Teardown:** once the upper course is placed, the unit mines the dirt blocks under its feet downward with `act.BreakAction`, descending cleanly to ground level.

## 3. Planner

Builds run as a new job kind in `crew-control`: `kind: "build"`, with spec `{"blueprint": "hut", "place": "build-zone-1", "rotation": 0, "clear": false, "tolerance": 0.0}`.

### 1. Material logistics
Before construction starts:
1. **Bill of materials (BOM):** tally required items from blueprint palette counts.
2. **Ledger check:** query `crew_stock` across known chests.
3. **Gather shortfall:** if stock is insufficient, the planner schedules prerequisite `gather_and_deposit` or `fell` jobs to harvest materials into `store`.
4. **Fetch staging:** free units receive `fetch` steps to load required items before walking to the site.

### 2. Step decomposition and scheduling
The planner divides the blueprint into `crew_job_step` records:
- **Layer ordering:** strictly bottom-up. Layer `y` depends on Layer `y - 1`. A layer's steps become ready only when the supporting layer finishes.
- **Region partitioning:** within a layer, blocks are divided into spatial sectors assigned to distinct units with a 3-block separation.
- **Support dependencies:** blocks with attachment requirements (doors, torches) explicitly depend on base blocks in `depends_on`.

### 3. Retries
- Transient errors (e.g. temporary path obstruction) retry up to 3 times with exponential backoff.
- Permanent failures (unsupported placement, broken prerequisite, or exhausted items) fail the step and the build job, cancelling open work via job-scoped stops.

## 4. Validation

Silent partial success is an anti-goal. A build is complete only when verified by world observation.

### Inspection pass
1. When placement steps complete, the scheduler enqueues an `inspect_build` step.
2. An assigned unit walks 2–4 inspection waypoints around the structure.
3. The unit's world store absorbs chunk and block updates for the bounding box.
4. The unit sends its observed block volume in the task completion event.

### Tolerance check
`crew-control` compares observed world blocks with the rotated blueprint:
- Non-air blocks must match expected types; interior air must contain no debris.
- `tolerance = 0.0` (default): 100% exact match of all non-air blocks.
- `tolerance > 0.0`: permits minor cosmetic discrepancies; structural boundaries must be 100%.
- On match, job marks `done`. On mismatch, the scheduler triggers one repair round; if that fails, job ends `failed`.

## 5. Safety

Automated builds must never damage existing structures or wander off-bounds.

1. **Confined to declared `area`:** every build job targets an existing `area` place from the ledger. The rotated blueprint's bounding box must fit entirely within `[place.box.min, place.box.max]`. If any block falls outside, the job is rejected on submission.
2. **Protected terrain:** units are strictly prohibited from breaking any block outside the blueprint envelope and temporary scaffolding.
3. **Site clearance (`clear` flag):**
   - `clear: false` (default): if any non-air block exists where the blueprint expects placement or interior air, the job aborts before acting with `refused: area not clear`.
   - `clear: true`: the planner generates a pre-build clearing phase. Units mine obstacles strictly within the blueprint box, deposit drops in `store`, and then begin foundations.

## 6. Console

The Jobs panel (`Shift-J`) gains a "Build" option alongside "Parallel demo" and "Stock goal".

- **Controls:** Blueprint dropdown (`Hut`, `Wall`, `Farm plot`), Area picker (ledger places with `kind == "area"`), Rotation selector (`North 0°`, `East 90°`, `South 180°`, `West 270°`), and `Clear site` toggle.
- **Camera overlay:** selecting an area renders a 3D translucent wireframe bounding box and orientation indicator on the unit camera feed, confirming placement visually before launch.
- **Telemetry:** SSE `jobs` events report current layer, active workers, placed block counts, and validation status.

## 7. Delivery

Delivery is organized into five ordered, independently shippable tasks.

| Task | Scope | Deliverables | Done when |
|---|---|---|---|
| **1. Unit `place_block`** | `crew` | Actuator `PlaceBlock` supporting 6 faces; `place_block` skill and task template. | A unit on the lab places a cobblestone block against an existing stone block on command and verifies it in belief. |
| **2. Single-unit wall** | `crew` & `crew-control` | `fetch` skill; wall blueprint; single-unit build planner. | Agate fetches cobblestone from `store`, builds an 8×1×3 wall in `build-site-1`, and inspect pass confirms 100% match. |
| **3. Scaffolding & clearing** | `crew` | `pillar_up` skill with teardown; pre-build `clear: true` excavation pass. | A unit clears two obstructing blocks, pillars up to place blocks at Y=3, and tears down the pillar cleanly. |
| **4. Multi-unit planner** | `crew-control` | Ledger material check and auto-gather; spatial region splitting; layer dependency engine. | Agate and Basalt build a 16-block wall concurrently, fetching materials from different chests without collision. |
| **5. Hut, Farm & Console** | `crew-control` & Console | Embedded hut and farm plot blueprints; water bucket & hoe placement; Jobs console Build panel. | An operator launches a Hut and Farm Plot from the console; units complete both unattended; both report `done`. |

## 8. Open questions for the owner

1. **Uneven terrain foundations:** should blueprints assume the target area is pre-flattened at `y = 0`, or should the planner automatically dig down slopes and extend cobblestone foundations to solid ground?
2. **Scaffolding block material:** should units use ordinary dirt pillars (requiring subsequent teardown mining) or Bedrock scaffolding blocks (which collapse on breaking the base block)?
3. **Farm plot planting:** should the farm plot build complete once water is placed and soil is tilled, or should units also fetch and plant seeds (requiring hoe durability and seed stock logistics)?

