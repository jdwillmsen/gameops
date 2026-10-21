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

Step 4 of the roadmap introduces declarative builds: an operator chooses a blueprint, a target place and an orientation. `crew-control` checks a named supplies chest in the ledger, fetches stocked materials with existing task templates, partitions construction steps across free units, and verifies the finished structure with a new block-reading task.

## Owner decisions

- **First builds:** a hut, a wall, and a farm plot.
- **Lab only:** operates exclusively on the lab Bedrock server.
- **No LLM at runtime:** builds are deterministic, recipe-driven declarative plans.

## 1. Build template format

A versioned JSON blueprint defines a 3D volume in horizontal layers and a block palette.

### Schema

```json
{
  "version": 1,
  "name": "hut",
  "size": {"x": 5, "y": 4, "z": 5},
  "palette": {"0": "air", "1": "cobblestone", "2": "oak_log"},
  "layers": [
    ["11111", "11111", "11111", "11111", "11111"],
    ["11011", "10001", "10001", "10001", "11111"],
    ["11011", "10001", "10001", "10001", "11111"],
    ["22222", "22222", "22222", "22222", "22222"]
  ]
}
```

- `version`: format version (`1`).
- `name`: slug identifier.
- `size`: dimensions (`x, y, z`).
- `palette`: maps single characters to Bedrock block names. Key `"0"` is always `"air"`.
- `layers`: cross-sections from base to top, rows along `z`, characters along `x`.
- Blueprints are embedded in `crew-control` via `//go:embed blueprints/*.json`.

### Materials and placement

Crew can gather `cobblestone` and `oak_log`, and dig `dirt`; it cannot craft or smelt. Every build therefore names an operator-stocked `supplies` chest place. The operator puts the listed blocks, plus any required tools/items, in that chest before launch. The planner uses ledger stock and existing chest `inspect`/`haul` templates; it does not infer recipes. Stale stock is inspected and a shortfall rejects the build.

- Hut: `cobblestone` and `oak_log` (the doorway is open; no door item).
- Wall: `cobblestone` only.
- Farm plot: `dirt`, `oak_log`, a water bucket, and a hoe. No seeds are required; planting is out of scope. A hoe tills dirt by use, it is not a placed block. Water and till actions are new skills and remain dependencies of the farm build.

### Placement and rotation

The job specifies world origin `(X0, Y0, Z0)` and rotation `R ∈ {0°, 90°, 180°, 270°}` (`north`, `east`, `south`, `west`). Local coordinates `(u, v, w)` map to world `(x, y, z)` with uniform height `y = Y0 + v`:
- `0°`: `x = X0 + u`, `z = Z0 + w`
- `90°`: `x = X0 + size.z - 1 - w`, `z = Z0 + u`
- `180°`: `x = X0 + size.x - 1 - u`, `z = Z0 + size.z - 1 - w`
- `270°`: `x = X0 + w`, `z = Z0 + size.x - 1 - u`

The first blueprints avoid directional block states.

### Blueprints

1. **Hut (5×5×4):**
   - `y = 0` (Floor): 5×5 solid `cobblestone`.
   - `y = 1` and `y = 2`: `cobblestone` perimeter walls with a 1×2 doorway; 3×3 hollow interior.
   - `y = 3` (Roof): 5×5 solid `oak_log`.
2. **Wall (N×3×1):**
   - Repeatable 8×3×1 segment:
     - `y = 0` & `y = 1`: 8×1 solid `cobblestone`.
     - `y = 2`: alternating `cobblestone` and `air` (`10101011`) for battlements.
   - Tiled along the target axis for runs of length `N`.
3. **Farm plot (9×9×1):** `y = 0` is dirt with an `oak_log` border and a central water cell from the supplied bucket. Till the dirt cells with the supplied hoe. No lily pad, slab, crafting, or planting is assumed.

## 2. New unit skills

Building requires three new skills in `minecraft/crew/internal/skill/`.

### `place_block`

Places an inventory item against an adjacent block face.

#### Bedrock protocol mechanics (gophertunnel)
In vendored gophertunnel v1.62.0, `PlaceOnTop` sends an `InventoryTransaction` with `UseItemTransactionData`. Its existing top-face path sets:
- `ActionType`: `protocol.UseItemActionClickBlock` (0).
- `TriggerType`: `protocol.TriggerTypePlayerInput`.
- `BlockPosition`: coordinate of the adjacent existing block (`against`), **not** the empty target cell.
- `BlockFace`: `1` for the upper face, as `act.PlaceOnTop` does. The six face values in a general placer still need a lab packet check before implementation.
- `ClickedPosition`: hit vector on the clicked face (e.g. `{0.5, 1.0, 0.5}` for top).
- `HotBarSlot`, `Hand` and `HeldItem`: selected slot, main hand and the full `ItemInstance` for that slot (including stack network ID), as `act.instance` supplies.
- `Position`: player eye position (`feet.Add(Vec3{0, 1.62, 0})`).
- `BlockRuntimeID`: the clicked block runtime ID from the world store, populated from `UpdateBlock`; the store calls this the network hash. It is not an item ID or block-name hash.
- `ClientPrediction`: `protocol.ClientPredictionSuccess`.

Select the slot and aim before sending. `ClickedPosition` is block-relative; reach and confirmation need skill tests and lab proof.

#### Failure modes
- **Occupied:** destination has an entity or solid block; pause and retry.
- **Out of reach:** navigate to a valid adjacent stand cell.
- **No support:** order supporting blocks first.
- **Wrong item:** fail with `item missing`.

### `fetch`

Optional multi-item wrapper around existing `withdraw`/`haul`: open chest, withdraw requested stacks, close, and report remaining stock via `seen`.

### `pillar_up` (scaffolding)

For blocks beyond ground reach, reuse the jump/place/land sequence already in `skill.Fell`. Its pose loop checks upward clearance and server block confirmation. A build-specific skill still needs safe teardown and material accounting; prove it in the lab before promising arbitrary heights.

## 3. Planner

Builds use the existing `plan` job kind and `crew_job_step` templates with dependencies; do not add a parallel `build` kind or step store. Example spec: `{"blueprint": "hut", "place": "build-zone-1", "rotation": 0, "clear": false, "tolerance": 0.0}`.

### 1. Material logistics
Before construction starts:
1. Tally the blueprint and tool requirements.
2. Use ledger `Stock` for the named supplies chest; if stale, enqueue existing `inspect` (which reads chests only).
3. Reject before work if the chest does not contain the full bill. No gather step can make planks, glass, doors, tools, or farmland.
4. Use existing `haul`/`withdraw` behavior to stage supplies. A multi-item `fetch` is a convenience only if it reuses those operations.

### 2. Step decomposition and scheduling
The planner divides the blueprint into `crew_job_step` records:
- Order layers bottom-up; each depends on its supporting layer.
- Split same-layer regions without overlapping unit work.
- Encode attachment support in existing `depends_on` step keys.

### 3. Retries
Use the existing scheduler's three attempts and job-scoped cancellation. Permanent placement or material failures fail the job.

## 4. Validation

Silent partial success is an anti-goal. Existing `inspect` only opens chests and reports their contents; it cannot inspect arbitrary blocks. Add a named `observe_blocks` unit task/skill backed by the existing block store. It must request/load the blueprint bounding box, return known block names/states and mark unknown cells explicitly; `crew-control` compares that result with the rotated template. Do not claim the chest `inspect` template validates a structure.

### Inspection pass
1. After placement, enqueue `observe_blocks` for the blueprint box.
2. The task obtains block updates for the box through the crew connection and reads the existing block store.
3. It returns observed names/states and unknown cells in its completion event. The current `inspect` chest task is not used for this check.

### Tolerance check
`crew-control` compares observed world blocks with the rotated blueprint:
- Non-air blocks must match expected types; interior air must contain no debris.
- Default tolerance is zero; any unknown cell fails validation.
- A nonzero tolerance may allow cosmetic differences, never boundary mismatches.
- Match marks the plan done; mismatch may add one repair round, then fails.

## 5. Safety

Automated builds must never damage existing structures or wander off-bounds.

1. **Confined to declared `area`:** every build job targets an existing `area` place from the ledger. The rotated blueprint's bounding box must fit entirely within `[place.box.min, place.box.max]`. If any block falls outside, the job is rejected on submission.
2. Units may break only cells explicitly approved by the build plan, including temporary scaffold.
3. `clear: false` rejects occupied target or interior-air cells. `clear: true` adds a bounded clearing phase and deposits drops in `store`.

## 6. Console

The Jobs panel (`Shift-J`) gains a "Build" option alongside "Parallel demo" and "Stock goal".

- Controls: blueprint, `area` place, supplies chest, rotation, and clear-site toggle.
- Selecting an area shows its bounds and orientation on the camera.
- Existing `jobs` SSE reports layer, workers, placed count, and validation.

## 7. Delivery

Delivery is ordered so each task leaves a working capability and later rows depend only on shipped earlier rows.

| Task | Scope | Deliverables | Done when |
|---|---|---|---|
| **1. Unit placement** | `crew` | Generalize the existing top-face actuator path; `place_block` skill and task template. | A unit places supplied cobblestone on a supported target and observes server confirmation. Validate other faces separately before enabling them. |
| **2. Block observation** | `crew` & `crew-control` | `observe_blocks` task over the existing block store; exact-box validation. | A bounded box returns block names and explicit unknown cells. |
| **3. Single-unit wall** | `crew` & `crew-control` | Wall blueprint, plan steps, supplies chest withdrawal, block observation check. | Agate builds an 8×3×1 cobblestone wall inside an `area`; `observe_blocks` confirms it. |
| **4. Scaffolding & clearing** | `crew` | Reuse `Fell` pillar movement in a build skill; bounded clearing with observed drops. | A unit clears only declared cells, builds above reach, and returns/accounts for scaffold material. |
| **5. Multi-unit planner** | `crew-control` | Region splitting and layer dependencies using existing plan steps and ledger stock. | Two units build a 16-block wall from one declared supplies chest without overlap. |
| **6. Hut, farm & console** | `crew`, `crew-control` & Console | Supplied-material hut; water-bucket and hoe use tasks for farm; build controls and telemetry. | Operator launches each supported template; block observation verifies blocks and farm task reports tilling separately. |

## 8. Open questions for the owner

1. Should the target area be flat, or should builds extend foundations to solid ground?
2. Should scaffold use dirt (then mined back) or Bedrock scaffolding?
3. Should farm plots stop after tilling, or include planting and seed logistics?

