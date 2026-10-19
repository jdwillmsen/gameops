# Crew actions spike (Wave 3a, Task 1)

Date: 2026-10-18. Lab only: the compose lab server (image tag 2026.10.1,
`VERSION=LATEST`, Bedrock 1.26.52.3, RakNet, offline mode, cheats on), gophertunnel
v1.62.0, one throwaway probe that dialled the way `cmd/crew` does
(`mcproto.Negotiate`, `mcdial.Dial`, offline identity, `DoSpawnContext`). No
protocol spoofing was needed. Nothing here touched FWB or the cluster. The probe
is deleted; this record and four fixtures are what remains.

## Answer

**Go for Tasks 2-7.** On the lab, crew walked, jumped a 1-block step, broke
stone with a wooden pickaxe, picked up the drop, opened a chest and moved items
into it, all with plain `PlayerAuthInput`, `MobEquipment`, `InventoryTransaction`
and `ItemStackRequest` packets, with zero corrections once the numbers below were
followed. Six things differ from the plan; Tasks 2, 3, 5 and 7 must absorb them
(see "Differences from the plan").

## Movement mode

`GameData().PlayerMovementSettings` is `RewindHistorySize: 40`,
`ServerAuthoritativeBlockBreaking: true`. `ServerAuthoritativeInventory` is
true: every stack has a non-zero `StackNetworkID`. The server is
server-authoritative with rewind: it simulates the unit from each input and
accepts the client's `Position` while it stays near its own result, otherwise
it sends `CorrectPlayerMovePrediction`.

- `PlayerAuthInput.Position` is the **eye** position, feet + 1.62.
- `GameData().PlayerPosition` is a placeholder (Y offset by 32768); the real
  position arrives in `Respawn` (`RespawnStateReadyToSpawn`), as the camera
  spike found.
- Fields that worked: `InputMode` 1 (mouse), `PlayMode` 0, `InteractionModel` 2
  (classic), `HeadYaw` = `Yaw`, `InteractPitch/InteractYaw` = pitch/yaw,
  `MoveVector` `{0, forward}`, `RawMoveVector` and `AnalogueMoveVector` equal to
  it. Yaw is degrees, 0 faces +Z, forward is `(-sin yaw, cos yaw)`.
- `Tick` can start at any value (100 worked) and only has to rise. The server
  never needed it to match its clock.
- `InputFlagBlockBreakingDelayEnabled` was set on every input.

### Loading-screen gate (not in the plan)

After `DoSpawnContext` the server holds the unit under an ability layer of type
`AbilityLayerTypeLoadingScreen` (5): walk speed 0, mine/build/open-containers off.
**While it is in force every input is dropped silently**: no
`CorrectPlayerMovePrediction`, no error, no block break, no `ContainerOpen`. It
lifts when the client sends `packet.ServerBoundLoadingScreen{Type:
packet.LoadingScreenTypeEnd}` (no `LoadingScreenID`). The probe sent it once,
right after `RequestChunkRadius`, and everything below worked. No
`UpdateAbilities` follows. Without it, walking, breaking and the chest all failed
the same silent way. The camera-only crew of Waves 1-2 never needed it.

## Walking

The server's ground model, reproduced exactly (zero corrections over 36
ticks, and the server's final position within 0.004 of the client's):

```
carry   = 0 at rest
v       = carry + 0.098 * forward        // forward in [0,1]
delta   = v along the yaw direction      // horizontal
carry   = v * 0.546
```

Steady state is 0.2159 blocks/tick (4.32 blocks/s), reached from rest as
0.098, 0.1515, 0.1807, 0.1966, ... A constant 0.2159 step from standstill drew
corrections within 12 ticks, so the actuator must keep this ramp. When input
stops the carry keeps the unit moving and decays by 0.546 per tick (about six
ticks), so idle ticks must send the decaying delta, not zero.

Per tick on flat ground the probe sent: `Position += delta`, `Delta = {vx,
-0.0784, vz}` (the vertical term is gravity's first step and is sent while
standing, with `Position.Y` unchanged), `MoveVector {0, 1}`, flags `Up` (and
`VerticalCollision` while grounded). Yaw -90, 90, 45 and 180 all walked with no
corrections.

Tolerance, measured by scaling the acceleration: 1.1x (0.238 blocks/tick) for 36
ticks drew no correction and the server adopted the client position (0.64
blocks ahead of its own simulation); 1.3x drew the first correction at about
0.9 blocks of drift after roughly 25 ticks; 1.6x drew three. The actuator
should not rely on the slack.

Sprint: acceleration x1.3 with flags `Sprinting` and `SprintDown` (no
`StartSprinting` needed) walked at 0.28 blocks/tick with no corrections.
Crew does not need it.

### Collisions and corrections

The server stops the unit 0.3 blocks from a solid face (the hitbox half width).
Pushing into a wall drew a `CorrectPlayerMovePrediction` roughly every 6 ticks
for as long as the input continued. Its fields: `PredictionType` 0, `Position`
(eye), `Delta` (for example `{0, -0.0784, v}` when standing), `Rotation`,
`OnGround`, and `Tick`, the client tick that was corrected. Adopting `Position`,
`OnGround` and the horizontal `Delta` as the new carry resumed cleanly. A
`/tp` arrives as `MovePlayer` with `Mode` 2 (teleport) for the unit's own
runtime ID; adopting its position was enough, the `InputFlagHandledTeleport`
acknowledgement was not needed (walking right after a teleport, with and without
it, drew no corrections).

### Idle

A unit that sent nothing for 45 s after walking was not kicked, not moved and not
corrected; sending resumed with the next consecutive tick and the first walk
matched the server's result. Sending idle inputs every tick is fine; so is
silence between skills.

## Jumping

Vertical motion, exactly gravity 0.08 and drag 0.98, applied after the move:

```
tick 0:  vy = 0.42; dy = vy;  vy = (vy - 0.08) * 0.98
dy per tick: 0.42, 0.3332, 0.2481, 0.1648, 0.0831, 0.0030, -0.0754, -0.1523, ...
```

The apex is 1.2522 above the takeoff, so a 1-block step is cleared on the third
tick. In the air the horizontal model is `v = v * 0.91 + 0.02 * forward`. On the
landing tick `dy` is clamped to reach the ground exactly (it was -0.0244 onto a
step). Flags: tick 0 `Jumping`, `StartJumping`, `JumpDown`; airborne ticks
`Jumping`; the landing tick **`VerticalCollision`**.

- Jumping onto a 1-block step from standing and from a walk drew zero
  corrections. Takeoff worked with the unit's centre anywhere from 2.25 blocks
  to 0.52 blocks short of the step face (the unit lands on the step on the
  ninth packet). Takeoff after the wall had already stopped the unit (centre
  0.3 from the face) drew corrections.
- Without `VerticalCollision` on the landing tick, the same jump drew a
  correction every time (3 of 3 runs): required.
- Without the `Jumping`/`StartJumping` flags the server saw an unexplained rise
  and corrected. An initial velocity of 0.5 instead of 0.42 was corrected too.
- Not tested: step-down, `HorizontalCollision`, jumping in water.

## Breaking stone with a wooden pickaxe

Select the pickaxe with `MobEquipment{EntityRuntimeID, NewItem (the
inventory's ItemInstance), InventorySlot, HotBarSlot, WindowID 0}`. It is
needed: with an empty hand the same stone took 150 ticks (7.5 s) and dropped
nothing; with the pickaxe held it took 23.

The break is **server-timed**, not input-counted:

- `PlayerAuthInput` with `InputFlagPerformBlockActions` and a single
  `PlayerBlockAction{Action: PlayerActionStartBreak, BlockPos, Face}` starts it.
- The server removed the block 1.15 s (23 ticks) later whether the client sent
  `PlayerActionContinueDestroyBlock` each tick, only a few of them, or none at
  all. Continue, `PlayerActionStopBreak` and `PlayerActionPredictDestroyBlock`
  sent early (tick 10) did not cancel or speed it, and sent late they were
  accepted with no visible effect. The plan's order is harmless but not needed.
- `PlayerActionAbortBreak` cancels it.
- The session stays open after the block is gone: a block placed back at the same
  position was mined again with no new Start. A skill that gives up must send
  Abort, and one that moves to the next block just sends a new Start there.
- Consecutive breaks, each started the same tick as the previous one finished,
  all worked.
- No `ItemStackRequest` with `MineBlockStackRequestAction` is needed. The server
  updates tool wear itself: an `InventorySlot` for the pickaxe arrives with the
  NBT `Damage` raised by 1 and the same `StackNetworkID`.
- Face, facing and distance were not enforced: breaks worked looking away, with
  the wrong face, and at 8 blocks centre to centre; they failed at 10. Use the
  plan's 4.5.

Server replies, in order (`break_stone.bin`): `UpdateBlock` of the block to air
(several, with neighbours), `InventorySlot` for the pickaxe (new `Damage`),
a final `UpdateBlock`, then `AddItemActor` for the drop (a cobblestone item,
spawned within about 0.5 blocks of the broken block with a small random
velocity).

## Picking up

Nothing is sent by crew. Ten ticks after the drop spawns the server collects it
if it is within about **1.5 blocks horizontally** of the unit (collected at 1.35,
1.48 and 1.50; left at 1.71 and beyond; vertical offsets up to 0.6 were fine).
Items sometimes drift toward the unit, so a drop slightly out of range may still
arrive, but crew should stand within 1 block of the drop.

The reply is **not** `InventorySlot` or `InventoryContent`. It is a
server-to-client `InventoryTransaction` with `NormalTransactionData` and two
actions, then `TakeItemActor{ItemEntityRuntimeID, TakerEntityRuntimeID: the
unit}` (`pickup.bin`):

1. `SourceType` 0 (container), `WindowID` 0, `InventorySlot` n, `OldItem` the
   old stack (count 41), `NewItem` the new stack (count 42, new
   `StackNetworkID`).
2. `SourceType` 2 (world), `WindowID` unset, the item entity's stack as `OldItem`,
   empty `NewItem`.

A new stack lands in the first free slot with the same shape (`OldItem` empty).

## Chest

Open with a standalone `packet.InventoryTransaction{TransactionData:
&protocol.UseItemTransactionData{ActionType: UseItemActionClickBlock,
TriggerType: TriggerTypePlayerInput, BlockPosition, BlockFace: 5 (the face
toward the unit), HotBarSlot, Hand: HandSlotMainHand, HeldItem (the held
ItemInstance), Position (eye), ClickedPosition {0.5, 0.5, 0.5}, BlockRuntimeID:
the chest's network hash, ClientPrediction: ClientPredictionSuccess}}`. The
chest hash is in the `UpdateBlock` the server sent for it. It opened at up to 6
blocks and not at 6.5; the plan's 3 is safe.

Server replies (`chest_open.bin`): `ContainerOpen{WindowID, ContainerType: 0,
ContainerPosition, ContainerEntityUniqueID: -1}`; `InventoryContent` with that
`WindowID` and 27 slots; `BlockEvent{EventType 1, EventData 1}` (lid); a full
`InventoryContent` for window 0 (36 slots); `PlayerHotBar`; the chest
`InventoryContent` twice more. **`WindowID` differs per open** (2, 7, ...);
take it from `ContainerOpen`.

Deposit with `packet.ItemStackRequest{Requests: []protocol.ItemStackRequest{{
RequestID, Actions: [&protocol.PlaceStackRequestAction{Count, Source:
{Container: {ContainerID: ContainerCombinedHotBarAndInventory (12)}, Slot, StackNetworkID: the
stack's}, Destination: {Container: {ContainerID: ContainerLevelEntity (7)}, Slot,
StackNetworkID: 0 for an empty slot, else the slot's}}]}}}`. Request IDs were
negative odd numbers counting down (-3, -5, ...); the server accepted them.

Server replies (`chest_deposit.bin`):

- `InventorySlot{WindowID: chest window, Slot: chest slot, NewItem}`, sent twice
  around the response.
- `ItemStackResponse{Responses: [{Status 0, RequestID, ContainerInfo: [{
  Container 12, SlotInfo: [{Slot, Count (what is left, 0 if emptied),
  StackNetworkID (0 if emptied)}]}, {Container 7, SlotInfo: [{Slot, Count,
  StackNetworkID}]}]}]}`.
- **No `InventorySlot` for the unit's own slot**; the new count of the source
  stack is only in the response.
- Moving a whole stack moved its `StackNetworkID` with it. Placing onto an
  existing stack of the same item (destination `StackNetworkID` = the stack's)
  merged and kept the destination ID.

`ContainerClose{WindowID, ContainerType: 0xff, ServerSide: false}` closed it. The
server replied `ContainerClose{WindowID, ContainerType: 247, ServerSide: false}`
and a `BlockEvent` with `EventData` 0.

### Withdraw

Recorded 2026-10-19 on the lab (`chest_withdraw.bin`, one throwaway probe, deleted).
Take with `packet.ItemStackRequest{Requests: [{RequestID, Actions:
[&protocol.TakeStackRequestAction{Count, Source: {Container:
{ContainerID: ContainerLevelEntity (7)}, Slot, StackNetworkID: the chest slot's},
Destination: {Container: {ContainerID: ContainerCombinedHotBarAndInventory
(12)}, Slot, StackNetworkID: 0 for an empty slot}}]}]}`. Request IDs
counted down from -3 as for a deposit.

Accepted exchange (16 cobblestone from chest slot 0 into empty slot 9): an
`InventorySlot` for the chest slot (now empty), then `ItemStackResponse{Status 0,
ContainerInfo: [{Container 7, [{Slot 0, Count 0, StackNetworkID 0}]},
{Container 12, [{Slot 9, Count 16, StackNetworkID 7}]}]}`, then the same
`InventorySlot` again. **No `InventorySlot` or `InventoryContent` for the
unit's own slot**, and the response does not name the item. The destination
kept the chest stack's `StackNetworkID` (7). Belief therefore remembers item
IDs from the open window's contents and names the new stack from its stack ID,
or, after a partial take (new ID), from the stack left in the chest.

| Case | Status |
|---|---|
| Taking 16 into an empty slot | 0 |
| Source slot empty (quoted ID 0) | 49 `FailedToValidateSrcSlot` |
| Source `StackNetworkID` stale (99999) | 49 |
| Destination holds another item (wooden pickaxe, correct ID quoted) | 1 `Error` |
| Count 20 against a stack of 8 | 0, and all 8 moved: the server clamps, so crew must check the count itself |

Not separately recorded: a destination slot index of 36. The one request sent
for it was aimed at a chest slot an earlier request had just emptied, so its
49 says nothing about the index; crew refuses it before sending.

### Rejections

The server always answered; a request was never dropped silently and nothing
kicked the unit. `ItemStackResponse.Status` with no `ContainerInfo`:

| Case | Status |
|---|---|
| Source `StackNetworkID` wrong, or request sent after the container closed | 49 `FailedToValidateSrcSlot` |
| Destination slot holds another item, or a full stack of the same item | 1 `Error` |
| Destination slot index 27 (past the chest) | 55 `CannotPlaceItem` |

A "full chest" shows up as every slot being another item or a 64 stack; the
server gives no distinct status, so `chest is full` has to be decided from the
chest `InventoryContent`. The plan's 5 s response timeout stays as a safety net,
but in the lab it never fired.

## Fixtures

`minecraft/crew/internal/act/testdata/`, lab only (fixed lab scene, no
FWB data). Each file is a sequence of records with no header: a `uint32`
little-endian length, then that many bytes: the packet header (`varuint32`,
low 10 bits are the packet ID) followed by the packet payload exactly as the
server sent it. Only server-to-client packets of these types were kept:
`UpdateBlock`, `UpdateSubChunkBlocks`, `InventorySlot`, `InventoryContent`
(windows 0 and the chest's, not 119/120/124), `InventoryTransaction`,
`TakeItemActor`, `AddItemActor`, `ContainerOpen`, `ContainerClose`,
`ItemStackResponse`, `BlockEvent`, `PlayerHotBar`.

Decode with gophertunnel:

```go
pool := packet.NewServerPool() // packets the server sends
body := bytes.NewBuffer(record) // bytes.Buffer, not bytes.Reader
var h packet.Header
_ = h.Read(body)
pk := pool[h.PacketID]()
pk.Marshal(protocol.NewReader(body, shieldID, false))
```

Two traps: use `bytes.Buffer`, because `bytes.Reader.Read` returns EOF on a
zero-length read and fails on every empty item; and pass the shield item's
runtime ID as `shieldID` (358 in this lab: the `minecraft:shield` entry of
`GameData().Items`), or a stack with that ID reads wrongly. Item IDs in the
fixtures are this server's: 4 is cobblestone, 312 is the wooden pickaxe, 358
the shield. Block hashes: air 3690217760, stone 2150698529, chest facing north
3162850062. Tests should supply an `ItemNames` table with those.

| File | Contents, in order |
|---|---|
| `break_stone.bin` | 4 x `UpdateBlock`, `InventorySlot` (pickaxe, `Damage` 1), `UpdateBlock`, `AddItemActor` |
| `pickup.bin` | the same break (`Damage` 2), then `InventoryTransaction` (slot 1, 41 to 42 cobblestone, new `StackNetworkID`), `TakeItemActor` |
| `chest_open.bin` | `ContainerOpen` (window 2, type 0), `InventoryContent` (window 2, 27 empty), `BlockEvent`, `InventoryContent` (window 0, 36 slots), `PlayerHotBar`, `InventoryContent` x2 (window 2) |
| `chest_deposit.bin` | `InventorySlot` (window 2, slot 0, 16 cobblestone), `ItemStackResponse` (status 0, request -3, source slot 1 left at 26), `InventorySlot`, `ContainerClose` (window 2, type 247), `BlockEvent` |

For Task 3's `TestBreakSendsTheRecordedActionOrder` the expected client actions
are: one `PlayerAuthInput` with `InputFlagPerformBlockActions` and
`{PlayerActionStartBreak, pos, face}`; optional `{PlayerActionContinueDestroyBlock,
pos, face}` per later tick; nothing else is needed, and the server finishes the
block after 23 ticks from Start.

## Differences from the plan

Later tasks take this list over the plan.

1. **Loading-screen gate (Task 3, Task 7).** Crew must send
   `ServerBoundLoadingScreen{Type: LoadingScreenTypeEnd}` once per session after
   spawn and the chunk-radius request, before the first action input. The plan
   does not mention it, and without it every action fails silently.
2. **Break needs one Start, not an ordered sequence (Task 3, Task 5).** Start
   once, then wait up to 23 ticks (budget about 30) for the block to turn to air
   in `UpdateBlock`; Continue is optional; Stop, Predict and
   `MineBlockStackRequestAction` are not needed; Abort cancels. The session
   outlives the block, so `MineBlock` should send Abort when it gives up. The
   `BreakAction` phases `BreakStart/BreakContinue/BreakFinish` shrink to
   Start, optional Continue, Abort. The tick count is 23, as the plan guessed.
3. **Pickup is a server `InventoryTransaction` (Task 2).** `belief.Inventory`
   must apply server-to-client `InventoryTransaction` (`NormalTransactionData`,
   container actions on window 0) as well as `InventoryContent` and
   `InventorySlot`.
4. **Deposit changes the source stack only in `ItemStackResponse` (Task 2, Task
   3, Task 5).** The unit's inventory count after a deposit is only in
   `ItemStackResponse.ContainerInfo` (container 12). The belief store should
   apply it (a `Count` of 0 empties the slot; keep `StackNetworkID` otherwise).
   Without it, `DepositChest` would misjudge what is left. Also, tool wear
   changes the pickaxe via `InventorySlot` with the same `StackNetworkID`.
5. **Walk speed is a ramp (Task 3).** `act.Pose` needs a carry velocity; the
   step is not a flat 0.2159 from the first tick. The pose also needs the idle
   decay, and `VerticalCollision` on the landing tick. The walk and jump
   formulas above replace the plan's "about 0.2159" and "expect"-style numbers.
6. **Container window ID is per open, and the response status is generic
   (Task 3, Task 5).** `Container()` must return the ID from `ContainerOpen`,
   and `Response()` should surface the status number; 1 and 49 are the
   rejections to expect.

Smaller points, no task change: the pickup range is about 1.5 blocks; reach
for breaking was far more generous than 4.5; `CorrectPlayerMovePrediction`
repeats every 6 ticks while the client keeps pushing; the teleport
acknowledgement flag was not needed; the standalone `ItemStackRequest` and
`InventoryTransaction` packets worked, so the `PlayerAuthInput` carriers
(`ItemStackRequest`, `ItemInteractionData`) were not needed and were not
tested.

## Not tested

Full-inventory pickup; the unit dying; `HorizontalCollision`; step-down and
multi-block falls; water; other dimensions; the chest closing by itself when
the unit walks away; more than one unit; a server upgrade (the block-hash table
and item IDs here are 1.26.52.3's, and the loading-screen gate is new enough
that a later server may change it).

## Go / no-go

| Capability | Result |
|---|---|
| Walk | Go. Exact model above, zero corrections. |
| Jump | Go, onto a 1-block step; needs `VerticalCollision` on landing. |
| Break | Go. One Start, 23 ticks, no tool request. |
| Pickup | Go. Automatic within about 1.5 blocks; arrives as `InventoryTransaction`. |
| Deposit | Go. `PlaceStackRequestAction`, status 0, rejections answered. |

Tasks 2-7 can proceed with the six differences above. Nothing blocks the plan.
