# Creative menders and a builder class

**Status:** draft, spike done (findings below)
**Date:** 2026-10-28
**Builds on:** [Colosseum repair bot](2026-10-27-crew-colosseum-repair-bot-design.md) (`internal/repair`, `cmd/arenarepair`), [crew squads and arena](2026-10-23-crew-squads-and-arena-design.md), `internal/blueprint`

## Why

Repair works today only as an offline tool: `arenarepair` plans from a world copy and applies console `fill` commands. Nobody sees a bot do it, and it covers only the larger Colosseum. The owner wants crew units that walk up to damaged builds, triage them and fix them in the lab, and a second class that builds new things. Items are not the point yet, so these units run in creative.

## Decisions

Everything below is a call made without the owner's review; change any of it.

- **Two new roles, not one.** `CREW_ROLE` today means a warrior role. It grows two builder roles, parsed by the same setting and refused in the same way when unknown:
  - `mender`: triages a build against its plan and repairs the difference.
  - `builder`: raises a new structure from a blueprint (`internal/blueprint`), the in-world twin of `labcourses blueprint`.
  A unit has one role; a warrior role and a builder role never combine. Builder roles do not join a squad.
- **Creative, lab only.** A builder unit is put in creative by the lab's console (`gamemode creative <name>`), never by the unit. The unit refuses to start unless `CREW_LAB=true`, a flag the operator sets and the unit cannot verify against the server it joins. The real barrier is that creative only ever arrives from the console: `labcreative` applies the same lab allowlist as `arenarepair`, refuses any target naming `fwb`, and sets the mode per unit, so warriors and the FWB-bound units stay in survival.
- **Looks like it needs items.** Creative means unlimited stock, but a mender takes each block from the creative inventory just before placing it and visibly holds it, instead of placing from nowhere. No kit-chest trips in v1.
- **Reuse the planner, change the executor.** `internal/repair` already produces ordered rectangles of absolute block states with zones and damage kinds. A mender takes that plan, walks to each rectangle and places or breaks through the actuator. Console `fill` stays the bulk fallback when a rectangle is larger than a unit can cover in a task timeout.
- **Triage first.** A mender's first output is a report (zones, damage kinds, counts, worst first) posted to crew-control as task progress, the same shape `arenarepair plan` prints. Repair starts only on a repair task, so an operator can read the triage and decline.
- **Reads blocks as a client.** The scan reader is the unit's own loaded chunks (the phase 2 note in the repair spec), so a mender needs no world-db copy and no save hold. Cells outside loaded chunks stay `Unknown` and are never repaired.
- **Tasks, not new endpoints.** Two templates, `repair_build` (plan source, area box, `dry_run`) and `build_blueprint` (name, origin, rotation), through the existing Task API and crew-control work queue. Both are bounded to the lab area box like other terrain-changing templates.
- **Fight guard unchanged.** A mender stops while tagged mobs or non-crew players are present, as `repair.Apply` does.

## Goals

- A mender repairs a deliberately damaged part of the larger Colosseum on the lab with a player watching the camera.
- A builder stamps `small_house` at a given spot.
- Both are visible in the console with their role.

## Non-goals

- Survival materials, chests, hauling, or any use on FWB.
- Entities, container contents, sign text, levers (same limits as the repair spec).
- Replacing `arenarepair`; it stays the whole-arena batch path.

## Risks (answered by the spike)

1. **Creative placement on the wire.** Answered: yes, see (b) and (c) below. Taking a stack needs an item-stack request with a creative-craft action, and the unit's own inventory screen must be open for it.
2. **Gamemode visibility.** Answered: `StartGame` does not say; `UpdatePlayerGameType` does. See (a).
3. **Breaking.** Answered: the `StartBreak` block action the survival path already sends breaks at once in creative. The creative-specific destroy code is rejected and drops the session. See (d).
4. **Scale.** Round trips measured, throughput not. See Timings.

## Phases

| # | Output |
|---|--------|
| 0 | Spike: one creative unit takes and places one block on the lab. Findings recorded here |
| 1 | `mender` and `builder` roles in config, belief and console label; creative set by the lab tooling |
| 2 | Creative actuator: take from creative inventory, place, instant break |
| 3 | `repair_build` template with triage report; live trial on a damaged Colosseum section |
| 4 | `build_blueprint` template and builder trial |

## Left to the builder

Names of the skills, the exact progress report fields, the console badge colour, and the walk-or-fill threshold.

## Spike findings (2026-10-28)

Run against a throwaway offline lab (itzg image tag `2026.10.2`, BDS 1.26.60.6, protocol 2225, gophertunnel v1.63.0), one unit joined through the real `mcdial` dialer, belief tracker, block store and `act.Actuator`, put in creative by `docker exec <lab> send-command gamemode creative <name>`. The throwaway program is `minecraft/crew/cmd/creativespike/` and is deleted with the rest of phase 0; this section is what is kept. Everything below was observed, not read from docs. All four questions came back "possible".

### (a) Learning the gamemode

- `StartGame.PlayerGameMode` is **5** ("use the world default") and `WorldGameMode` is 0 on this lab, whatever the unit's real mode. A join never says creative. Offline lab units are fresh players on every login, so a reconnect is survival again: the lab tooling must re-run `gamemode creative <name>` after every join, not once.
- The change arrives as `packet.UpdatePlayerGameType{GameType: 1, PlayerUniqueID, Tick}`, about 20 to 40 ms after the console returns. `GameType` 0 survival, 1 creative. It is broadcast, so belief must match `PlayerUniqueID` against the unit's own `EntityUniqueID` (`gd.EntityUniqueID`, already kept by `tracker.SetUniqueID`). Note it is **`UpdatePlayerGameType`, not `SetPlayerGameType`** (the latter never arrived). No `UpdateAbilities` followed it.
- `gamemode` is the only console route: `defaultgamemode` is an unknown command on BDS.
- Belief needs one new field, e.g. `Snapshot.Creative bool` plus `GameModeKnown`, set from that packet and reset on `Reset`/respawn handling like the other join-time facts.
- `CreativeContent` is sent at login in **survival too** (1 packet, 124 groups, 1980 items) and was not resent on the change. So the table can be captured once per session before the unit is ever in creative.

### (b) Taking a named block into the hotbar

Item lookup:

- `CreativeContent.Items[i].Item.NetworkID` is the same runtime ID as the `StartGame` item table entry (`gd.Items`, name `minecraft:stone` is 1, `oak_planks` 5, `spruce_planks` -739). So name to ID uses the table `Actuator.ItemNames` already holds, and ID to creative entry is a scan of `CreativeContent.Items`.
- The ID the request quotes is **`CreativeItem.CreativeItemNetworkID`**, a different number (stone is 604, `oak_stairs` 77). Do not confuse the two.
- 1980 entries map to 1590 distinct item IDs; 13 IDs have more than one entry (variants), so pick by `Item.NetworkID` and, where it matters, `Item.MetadataValue`/`BlockRuntimeID`. 1020 entries carry `Item.BlockRuntimeID`, and it is the block's default-state network hash: stone's `-2144268767` as `int32` equals the `uint32` 2150698529 that `UpdateBlock` reports for placed stone. So a creative entry can also be found from a plan's block hash for default-state blocks. Non-default states (stair facing, slab half) are not in the item; the server derives them from the click, so a builder cannot ask for them by item (phase 4 problem, not solved here).

The request that worked (one `packet.ItemStackRequest`, one request):

1. Open the unit's own inventory screen first: `packet.Interact{ActionType: InteractActionOpenInventory, TargetEntityRuntimeID: <own runtime ID>}` (what `craft.go`'s `openInventory` already sends). The server answers in 20 to 60 ms with `ContainerOpen{WindowID: n, ContainerType: 255}`; `n` rises with every open (2, 3, ...), so read it, do not assume it.
2. Send `ItemStackRequest{RequestID: r, Actions: [CraftCreative, (optional CraftResultsDeprecated), Take]}` with:
   - `CraftCreativeStackRequestAction{CreativeItemNetworkID: <creative ID>, NumberOfCrafts: 1}`
   - `TakeStackRequestAction` (a `PlaceStackRequestAction` worked equally) with `Count` 64 (1 also worked), `Source = {Container: ContainerCreatedOutput (60), Slot: 50, StackNetworkID: r}` and `Destination = {Container: ContainerCombinedHotBarAndInventory (12), Slot: <empty hotbar slot>, StackNetworkID: 0}`.
   - `CraftResultsDeprecated` is **not needed**; the plain two-action request succeeded.
   - `RequestID` is the usual negative odd counter (`nextRequest`).
3. The source `StackNetworkID` **must equal the request's own `RequestID`**. With 0 the server answers status 49 (`StatusBadSource`); that was the only thing that separated failure from success once the screen was open. Source slot must be 50 (slot 0 gave 49 too).
4. Answer: `ItemStackResponse{Status: 0, ContainerInfo: [{Container: 12, SlotInfo: [{Slot, HotbarSlot, Count: 64, StackNetworkID: <new>}]}]}` in 25 to 45 ms. The new stack ID is small and per session (3, 4, 7, 13 in different runs). Fold it into belief/`inv` as the existing move path does. **The item is not named in the answer**, and `Actuator.reqItem[r]` is only filled by `Deposit`/`Withdraw`/`moveToHotbar`, so a creative take must set `reqItem[r] = <item runtime ID>` before `applyWindow0Locked` can write the stack into `a.inv`. The spike patched this by applying a synthetic `InventorySlot`.
5. Close the screen with `ContainerClose{WindowID: n, ContainerType: 0xff}`; the server echoes a `ContainerClose` (type 247). Placement worked both with the screen still open and after closing it, so closing is housekeeping.

Gates that were observed:

| Situation | Status |
|-----------|--------|
| creative, screen open, `StackNetworkID` = request ID, slot 50 | 0 (OK) |
| same, screen **not** open | 7 (`InvalidCraftRequest`), with or without the deprecated results action |
| `StackNetworkID` 0 or source slot 0 | 49 |
| survival, screen open | 28 (`PlayerNotInCreativeMode`) |

Survival with the screen shut also returns 7, so 28 is only reachable with the screen open; the executor should read 28 as "not in creative" and 7 as "screen not open".

Stock is unlimited: three placements from one 64 stack left the stack at 64 (server `InventoryContent` after each). Not tried: tools or other max-stack-1 items, merging onto an occupied slot, a full hotbar.

### (c) Placing with the existing path

`Actuator.PlaceBlock(target, "stone")` worked unchanged once the hotbar slot held the stack in `a.inv`: it sent `MobEquipment` for the slot then the `UseItem` click-block `InventoryTransaction`, and `UpdateBlock{Layer 0, hash 2150698529}` (stone) arrived 80 to 125 ms later at the target. No creative-specific placement packet exists. Details:

- The server answers each placement with a full window-0 `InventoryContent` and `PlayerHotBar{SelectedHotBarSlot: <slot>, SelectHotBarSlot: false}`; the actuator already folds the former into `a.inv`.
- A held stack with `BlockRuntimeID: 0` placed just as well as one carrying the creative `BlockRuntimeID`; `act.instance()` building a bare stack from the item ID is enough.
- Creative stock does not run down, so a take is needed once per block type, not once per block.
- Gotcha: `PlaceBlock` returns `ErrNoSupport` for a target whose floor is not a known, solid, non-interactive block (the spike's first target search kept finding too few valid cells until it also required a solid floor); that is existing behaviour, not creative-specific.

### (d) Breaking

- The survival `BreakAction{Phase: BreakStart}` (`PlayerBlockAction{Action: PlayerActionStartBreak (0), BlockPos, Face: 1}` in `PlayerAuthInput.BlockActions` with `InputFlagPerformBlockActions`) **breaks the block instantly in creative**, with no Continue/Stop needed: `UpdateBlock` to air 60 to 105 ms after the input. Natural blocks (grass) and placed blocks behave the same.
- The server also resends neighbouring `UpdateBlock`s (the cell above, duplicates), so wait on the target's own position becoming air, not on the first update.
- Two breaks on consecutive 50 ms ticks both landed. There is no per-block delay to wait out between them in this test.
- **Do not send `PlayerActionCreativePlayerDestroyBlock` (13).** BDS answers `PacketViolationWarning{Severity: 2, PacketID: 144, "PlayerAuthInputPacket: unsupported player block action type"}` and the session is closed within about 500 ms. `act/break.go` needs no new phase for creative; only a guard that a creative unit sends `BreakStart` and skips the `BreakContinue` and `BreakAbort` it may use for survival timing.

### Timings

| Step | Observed |
|------|----------|
| console `gamemode creative` to `UpdatePlayerGameType` | 20 to 40 ms |
| `Interact` to `ContainerOpen` | 20 to 60 ms |
| `ItemStackRequest` to response | 25 to 45 ms |
| place click to `UpdateBlock` | 80 to 125 ms |
| break input to `UpdateBlock` | 60 to 105 ms |

A mender's cost per block is therefore about one round trip each way plus its walking, and one take per block type. Sustained throughput of a place-and-break loop was not measured, so the walk-or-fill threshold stays a tuning call for phase 2.

### Other gotchas met

- The lab's BDS sends a first-join burst that sometimes decompresses past gophertunnel's 16 MiB batch limit (`decompress batch: size exceeds limit 16777216`), which closes the connection about 2 to 5 s after spawn with a bare "use of closed network connection". It hit about half the joins at the image's default `VIEW_DISTANCE` (32), and the one full run after setting `VIEW_DISTANCE=6` on the spike's own lab joined cleanly (a single sample). Unrelated to creative; a unit that dies this way simply reconnects, but the phase 3 live trial should use a lab with a modest view distance.
- Join needs the liveness respawn handshake (`liveness.Respawner.Handle` answering the server's `Respawn` packets) before the unit stays connected, as `cmd/crew` already does.
- The negative control matters: the same request in survival was refused (28), so a creative unit that was never actually switched fails loudly rather than silently doing nothing.
