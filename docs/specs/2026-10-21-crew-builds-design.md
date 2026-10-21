# Crew: builds (hut, wall, farm plot) and automated crafting

**Status:** proposed
**Date:** 2026-10-21
**Epic:** JDW-59. **Ticket:** JDW-118 (Crew builds).
**Builds on:**
- `docs/specs/2026-10-14-crew-design.md` (deterministic runtime, lab only)
- `docs/specs/2026-10-19-crew-console-and-control-design.md` (step 4, "Builds")
- `docs/specs/2026-10-20-crew-jobs-and-world-ledger-design.md` (jobs, ledger places, stock and scheduler)

## Why

Today crew units mine, chop, haul and inspect chests. An operator can ask bots to stock logs or dig a shaft, but cannot instruct them to construct structures or manufacture components.

Step 4 introduces declarative, end-to-end builds: an operator selects a blueprint, a target place and an orientation. Rather than requiring an operator to pre-stock supplies chests, `crew-control` resolves the dependency tree for all materials and tools. Units autonomously gather raw materials from ledger areas, craft intermediate items (planks, sticks, crafting tables, doors, hoes, scaffolding), prepare uneven terrain by extending foundations down to solid ground, erect the structure using adaptive scaffolding, and verify the completed work with block observation.

## Owner decisions

- **First builds:** a hut with a fitted door, a battlement wall, and a tilled and planted farm plot.
- **No operator supplies chest:** builds execute end-to-end from natural ledger sources or existing store stock.
- **Lab only:** operates exclusively on the lab Bedrock server.
- **No LLM at runtime:** planning and crafting are deterministic, recipe-driven systems.
- **No furnace smelting in early builds:** to avoid furnace container ticking, fuel burn curves, and smelting state machines, glass is dropped from early blueprints. Windows use open embrasures.

---

## 1. Blueprints & coordinates

Blueprints are versioned JSON files embedded in `crew-control` via `//go:embed blueprints/*.json`.

### Schema

```json
{
  "version": 1,
  "name": "hut",
  "size": {"x": 5, "y": 4, "z": 5},
  "palette": {"0": "air", "1": "cobblestone", "2": "oak_planks", "3": "oak_door"},
  "layers": [
    ["11111", "11111", "11111", "11111", "11111"],
    ["11311", "10001", "10001", "10001", "11111"],
    ["11011", "10001", "00000", "10001", "11011"],
    ["22222", "22222", "22222", "22222", "22222"]
  ]
}
```

- `palette`: single characters mapped to Bedrock block names (`"0"` is always `"air"`).
- `layers`: horizontal cross-sections from base (`y = 0`) to roof, rows along `z`, characters along `x`.

### Coordinate mapping & rotation

The job specifies world origin `(X0, Y0, Z0)` and rotation `R ∈ {0°, 90°, 180°, 270°}` (`north`, `east`, `south`, `west`). Local coordinates `(u, v, w)` map to world `(x, y, z)` with `y = Y0 + v`:
- `0°`: `x = X0 + u`, `z = Z0 + w`
- `90°`: `x = X0 + size.z - 1 - w`, `z = Z0 + u`
- `180°`: `x = X0 + size.x - 1 - u`, `z = Z0 + size.z - 1 - w`
- `270°`: `x = X0 + w`, `z = Z0 + size.x - 1 - u`

### Blueprints

1. **Hut (5×5×4):**
   - `y = 0` (Floor): 5×5 solid `cobblestone`.
   - `y = 1`: `cobblestone` walls, 3×3 interior air, `oak_door` at front.
   - `y = 2`: `cobblestone` walls with open window embrasures in both side walls and the back wall; upper door space.
   - `y = 3` (Roof): 5×5 solid `oak_planks`.
2. **Wall (N×3×1):** Repeatable 8×3×1 segments: `y = 0` & `1` solid `cobblestone`; `y = 2` alternating `cobblestone` and `air` (`10101010`) for battlements.
3. **Farm plot (9×9×2):** `y = 0` perimeter `oak_log` border, central water cell `(4, 0, 4)`, and 48 tilled `farmland` blocks (49 interior cells minus the water cell); `y = 1` holds `wheat` above every farmland cell and air elsewhere, so observation covers the planting.

---

## 2. Crafting

Crafting transforms raw harvested materials into tools, crafting stations, intermediate parts, and construction blocks.

### Protocol mechanics (gophertunnel v1.62.0)

Packet and action types, field names, `ContainerCreatedOutput` (`60`), `ContainerCombinedHotBarAndInventory` (`12`) and the recipe fields below are checked against vendored `github.com/sandertv/gophertunnel@v1.62.0`. Server behaviour is not; see **Verify on the lab** below.

1. **Recipe network IDs (`packet.CraftingData`):**
   - At join, the server sends `packet.CraftingData` containing `ShapedRecipes` and `ShapelessRecipes`.
   - Each recipe contains `RecipeNetworkID uint32`, `Input []ItemDescriptorCount`, `Output []ItemStack`, and `Block string`.
   - `RecipeNetworkID` is dynamic per server boot; it is never hardcoded. Belief indexes these recipes by output item name and station type.
2. **Crafting requests (`packet.ItemStackRequest`):**
   - Modern Bedrock crafting uses `ItemStackRequest`.
   - The client writes a request containing:
     - `protocol.CraftRecipeStackRequestAction` (or `protocol.AutoCraftRecipeStackRequestAction`) quoting `RecipeNetworkID` and `NumberOfCrafts`.
     - One `protocol.ConsumeStackRequestAction` per ingredient stack, naming the inventory slot and count used.
     - `protocol.TakeStackRequestAction` taking crafted items from `ContainerCreatedOutput` (Container ID `60` in `protocol/container.go`) at the created-output slot (named constant `createdOutputSlot`, `50` assumed) into inventory (`ContainerCombinedHotBarAndInventory`, Container ID `12`).
   - The actuator awaits `packet.ItemStackResponse` matching the request ID for `StatusOK` (`0`).
3. **2×2 inventory grid vs 3×3 crafting table:**
   - **2×2 grid:** Executed directly without opening a container window (`Block == ""`). Produces:
     - `oak_log` (1) → `oak_planks` (4)
     - `oak_planks` (2) → `stick` (4)
     - `oak_planks` (4) → `crafting_table` (1)
     - `torch`, `wooden_slab`
   - **3×3 crafting table:** Used when `Block == "crafting_table"` (e.g. doors, hoes, scaffolding).
     - Unit places `crafting_table` in world.
     - Unit clicks the table with `UseItemActionClickBlock`, receiving `packet.ContainerOpen` (`ContainerTypeWorkbench` = `1`).
     - Unit sends `ItemStackRequest` with the 3×3 recipe network ID, one consume action per ingredient and a take from the created-output slot, then sends `packet.ContainerClose` (`0xff`).
4. **Existing actuator code:** Builds on `act.Deposit` and `act.Withdraw` in `minecraft/crew/internal/act/container.go`, which already handle `ItemStackRequest` and countdown IDs (`nextRequest -= 2`).

### Verify on the lab

These are assumptions until a lab packet capture or server response confirms them; task 1 and task 2 prove them before anything depends on them:

1. **Created-output slot index:** the slot `TakeStackRequestAction` reads from in `ContainerCreatedOutput`. Kept in one named constant, `50` assumed.
2. **Auto-craft consume actions:** whether `AutoCraftRecipeStackRequestAction` needs a `ConsumeStackRequestAction` per ingredient, as `CraftRecipeStackRequestAction` does, or the server consumes ingredients itself.
3. **Face values:** the six `BlockFace` values for a general placer; only the top face (`1`) is exercised by `act.PlaceOnTop` today.
4. **`ClickedPosition` and reach:** the hit vector per face and the reach the server accepts.
5. **Dirt-pillar teardown:** the jump-place loop reuses `skill.Fell`, but safe step-down teardown and scaffold material accounting are unproven.

### Dependency planner

Resolves items recursively into a recipe DAG:
1. **Ledger stock:** For each required item, query ledger `Stock` (`docs/specs/2026-10-20-crew-jobs-and-world-ledger-design.md`). If present in `store` or nearby chests, emit a `haul` or `withdraw` step and skip crafting.
2. **Recursive resolution:** If stock is missing:
   - For 3×3 recipes, ensure a `crafting_table` is available in inventory or plan its 2×2 crafting and placement.
   - Recursively expand each input ingredient into ledger stock, sub-recipes, or natural gather steps (e.g. logs from a grove).
3. **Batching:** Aggregate common prerequisites (e.g. chop 12 logs in one step to supply door, hoe, and table).

---

## 3. Site preparation: foundations & clearing

Lab terrain is rarely flat. Builds handle slopes, hollows, and debris deterministically.

### Bounded clearing (`clear: true`)
When enabled, units scan the blueprint volume `[min, max]`, mine non-conforming blocks, collect drops, and deposit them in the shared staging chest on multi-unit builds or the ledger `store` chest on single-unit builds.

### Deep foundations
Builds do not require flat ground:
1. **Footprint probe:** Before any work, the planner probes every column `(x, z)` in the base layer (`y = 0`) from `y = -1` down to 16 blocks below `Y0`, using observed chunks under the footprint. If those chunks are unobserved, an inspect/observe step runs first and the planner probes once it returns.
2. **Solid ground definition:** `dirt`, `grass_block`, `stone`, `cobblestone`, `sand`, `gravel`, or `bedrock`. If the block at `y = -1` is `air`, `water`, or non-solid foliage, the column requires a foundation.
3. **Downward extension:**
   - Units step down each open column, placing foundation blocks (defaulting to the base layer material, e.g. `cobblestone`) from `y = -1` downwards until solid ground is reached.
   - **Depth limit:** Capped at 16 blocks below `Y0`. If the probe finds no solid ground within 16 blocks in any column (e.g. over a chasm), the planner fails the job before any gathering, crafting or placement with `site too deep` (§10.2).
4. Foundations are placed in Phase 2 before blueprint layers are erected.

---

## 4. Scaffolding: dirt pillars vs Bedrock scaffolding

Units have a reach of 4 blocks. Higher work requires scaffolding. Units use **both** dirt pillars and Bedrock scaffolding (`minecraft:scaffolding`).

### Selection rules

| Scaffolding | Selection rule | Characteristics |
|---|---|---|
| **Bedrock scaffolding** (`minecraft:scaffolding`) | **Selected when:**<br>1. Height exceeds 3 blocks above reach, **AND**<br>2. Work spans horizontal runs (>2 blocks), **AND**<br>3. Bamboo and string are in ledger stock or harvest areas. | • Climbed internally without jumping.<br>• Projects horizontally up to 6 blocks unsupported.<br>• Instant teardown: breaking the single base block collapses the whole tower into drops at the unit's feet. |
| **Dirt pillars** (`minecraft:dirt`) | **Selected when:**<br>1. Vertical lift is short (1–3 blocks), **OR**<br>2. Enclosed 1×1 spaces/corners, **OR**<br>3. Bamboo or string is unavailable. | • Jump-place loop via `skill.Fell`.<br>• Teardown requires step-by-step downward digging.<br>• Zero crafting prerequisites; dirt is universally available. |

### Material acquisition & fallback
- Recipe: 6 `bamboo` + 1 `string` → 6 `scaffolding` on a 3×3 table.
  - `bamboo`: broken by hand in a ledger grove.
  - `string`: gathered from spider drops in mob areas or cobwebs mined with sword/shears.
- If bamboo or string sources are absent in the ledger, the planner falls back to dirt pillars for all scaffolding.

---

## 5. Farm plot: tilling, planting & seed logistics

The farm plot is a 9×9 agricultural unit requiring water containment, tool crafting, and seed gathering.

### Seed logistics
1. Farm plot requires `wheat_seeds`.
2. If `wheat_seeds` are missing in ledger stock (<48):
   - Enqueue a `gather_seeds` step targeting a grassland ledger area.
   - Units break `short_grass` and `tall_grass` blocks (~12% Bedrock seed drop rate) until 48 seeds are collected.

### Tool & water logistics
- **Hoe:** Planner crafts a `wooden_hoe` (2 sticks + 2 planks on 3×3 table).
- **Water:** Central block `(4, 0, 4)` is filled from a `water_bucket` in ledger stock, or from a bucket crafted from 3 `iron_ingot` already in ledger stock and filled at a ledger water source. Ingots are never smelted; with neither in stock, the planner fails the job with `farm needs a water_bucket or 3 iron_ingot in stock`. A single water block hydrates farmland up to 4 blocks horizontally in all directions.

### Execution
1. Place perimeter `oak_log` border and hollow center.
2. Place water at `(4, 0, 4)`.
3. With `wooden_hoe` selected, click dirt top faces with `UseItemActionClickBlock` to convert dirt to `farmland`.
4. With `wheat_seeds` selected, click farmland top faces to plant `wheat` at stage 0.

---

## 6. End-to-end build workflows

Builds execute across four sequential phases mapped directly onto existing `crew_job` and `crew_job_step` records. No new database tables or schedulers are added.

1. **Phase 1: Materials & Logistics**
   - Resolve dependency DAG.
   - Enqueue gather steps (felling logs, mining stone, harvesting grass for seeds, bamboo/string).
   - Enqueue crafting steps (2×2 for planks/sticks/tables, 3×3 for tools/doors/scaffolding).
   - Haul supplies to build staging.
2. **Phase 2: Site Preparation**
   - If `clear: true`, clear non-conforming blocks in the bounding box.
   - Foundation pass: extend foundation blocks down to solid ground for all footprint columns.
3. **Phase 3: Construction**
   - Bottom-up layer placement (`y = 0` to `y = size.y - 1`). Steps enforce `depends_on`.
   - Deploy scaffolding (scaffolding blocks or dirt pillars per §4) when blocks exceed reach.
   - Install functional components: `oak_door`, till farmland with hoe, plant `wheat_seeds`.
   - Dismantle scaffolding and collect dropped items.
4. **Phase 4: Validation**
   - Dispatch `observe_blocks` across the blueprint bounding box (the farm's box is 2 high, `y = 0..1`, so planted wheat is checked).
   - Compare observed block IDs with the rotated template.

---

## 7. Unit skills

Skills reside in `minecraft/crew/internal/skill/` and actuators in `minecraft/crew/internal/act/`.

### `place_block`
Places held block against an adjacent face using `InventoryTransaction` with `UseItemTransactionData`:
- `ActionType`: `UseItemActionClickBlock` (0).
- `BlockPosition`: coordinate of existing supporting block (`against`), not empty target.
- `BlockFace`: target face (1 for top, 0 for bottom, 2–5 for sides).
- `ClickedPosition`: relative hit coordinates (e.g. `{0.5, 1.0, 0.5}`).
- `HotBarSlot`, `Hand`, `HeldItem`: slot and stack instance.
- `BlockRuntimeID`: runtime hash of clicked block from belief store.
- `ClientPrediction`: `ClientPredictionSuccess`.

### `craft`
- `Craft2x2(recipeID uint32, count int)`: writes a craft-recipe action, one consume action per ingredient, and a take from `ContainerCreatedOutput` at `createdOutputSlot`.
- `Craft3x3(tablePos protocol.BlockPos, recipeID uint32, count int)`: opens crafting table window, sends the same craft, consume and take actions, closes window.

### `till_and_plant`
- `Till`: Targets dirt top face (`BlockFace: 1`) with held hoe.
- `Plant`: Targets farmland top face (`BlockFace: 1`) with held seeds.

### `scaffold_pillar`
- **Dirt mode:** Jump-place dirt beneath feet; mine downward on teardown.
- **Scaffolding mode:** Places base scaffolding, climbs internally, projects horizontal outriggers up to 6 blocks; single-block base break collapses structure.

### `observe_blocks`
Queries belief store over bounding box `[min, max]`, returning `(x, y, z) -> blockName` and unstreamed chunks.

---

## 8. Validation and safety

1. **Volume containment:** Bounding box and foundation columns must fit inside declared ledger `area`.
2. **Tolerance:** Default tolerance is `0.0`. Every non-air block must match blueprint; air spaces must contain no debris or temporary scaffolding.
3. **One repair attempt:** Defective cells trigger one targeted repair step before failing the job.

---

## 9. Delivery tasks

Tasks are strictly ordered and independently shippable with lab-provable done conditions. Crafting tasks are placed early.

| # | Task | Scope | Deliverables | Done when |
|---|---|---|---|---|
| **1** | **Unit block placement** | `crew` | Generalize actuator placement to 6 faces; `place_block` skill and task. | Unit places cobblestone against a target face in the lab and confirms `UpdateBlock`. |
| **2** | **Unit crafting actuator (2×2 & 3×3)** | `crew` | Index `packet.CraftingData`; implement `Craft2x2` and `Craft3x3` via `ItemStackRequest`. | Lab confirms the created-output slot and whether auto-craft needs consume actions; unit then crafts planks in 2×2 grid, places crafting table, and crafts `oak_door` via 3×3 grid. |
| **3** | **Crafting dependency planner** | `crew-control` | Recursive recipe DAG solver; ledger stock lookup; gather/craft job steps. | Request for `oak_door` gathers logs, crafts planks and table, and produces door with no pre-stocked chest. |
| **4** | **Block observation** | `crew` & `crew-control` | `observe_blocks` skill over belief store; bounding box comparison in `crew-control`. | Bounded volume query returns block IDs and detects test discrepancies with zero false positives. |
| **5** | **Foundations & clearing** | `crew` & `crew-control` | Uneven ground detection; downward foundation placement to solid ground; bounded clearing. | Unit builds a 5×1 foundation across a 2-block depression, leveling the base to solid ground. |
| **6** | **Dual-mode scaffolding** | `crew` | Dirt pillar jump-place/mine-down and Bedrock scaffolding climb/outrigger/collapse skills. | Unit builds a pillar >5 blocks high using dirt when isolated, and scaffolding when materials exist, fully recovering all scaffold blocks. |
| **7** | **Farm cultivation** | `crew` & `crew-control` | Grass breaking for seed logistics; hoe tilling; seed planting skill. | Unit gathers seeds from grass, crafts wooden hoe, tills 9×9 plot around water, and plants wheat seeds. |
| **8** | **Single-unit wall & hut** | `crew` & `crew-control` | End-to-end execution of Wall and Hut blueprints from raw materials to validation. | Unit gathers stone/wood, crafts door, extends foundations, builds 5×5 hut, and passes `observe_blocks` validation. |
| **9** | **Multi-unit builds & console** | `crew-control` & Console | Region partitioning; multi-unit coordination; Jobs panel (`Shift-J`) build triggers. | Two units build a 16-block wall in parallel with no overlapping collisions, reporting live progress via SSE. |

---

## 10. Decided (orchestrator's calls, 2026-10-21)

The owner approved the design and asked for no further questions on these:

1. **Staging chest.** Multi-unit builds use a shared staging chest at the site. The bots craft and place it like any other block; units do not carry every material themselves. Cost if wrong: one extra chest per build.
2. **Deep foundations.** If the ground drops more than 16 blocks under the footprint, the planner fails the job before any work with `site too deep`. It does not bridge open air. Cost if wrong: such sites need a later bridging task.
3. **Leftovers.** After a build, units return hoes, crafting tables and recovered scaffolding to the `store` chest. Cost if wrong: units hold fewer spare tools.
