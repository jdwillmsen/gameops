# Decisions

Why crew is shaped the way it is. Dated, because the reasoning was true
against a particular state of the world and can stop being true.

## 2026-10-14: reuses the agent's `pkg/` rather than copying it

Crew needs device-code auth, dialing, protocol negotiation, respawn-on-death,
structured logging and a generated skin. All of them already live in
`minecraft/agent/pkg`, and afkbot's answer to the same need was to copy them
under its own `internal/`.

Crew imports them in place instead. Both modules are one Go module in this
repo, so an import costs nothing, while a copy is a second place to fix the
next protocol bump. `component.yaml` lists `minecraft/agent/pkg/` under
`depends`, so a change there rebuilds and retests crew.

## 2026-10-14: no presence client yet

Afkbot polls the agent for whether it should be in the world. Crew does not.
Crew-control will own when a unit parks, and the lab has no agent to ask, so a
client written now would have nothing to talk to and a contract nobody has
exercised.

A unit joins on start and stays until it is stopped. That is wrong for FWB,
which is why nothing here is deployed there.

## 2026-10-14: the status API defaults to loopback

`/v1/state` reports the unit's position, and the camera streams its
surroundings. On FWB a position is as good as the seed for locating a base, and
neither endpoint has authentication.

`STATUS_ADDR` therefore defaults to `127.0.0.1:8080`. Binding wider is a line
someone writes, as the lab compose does for the container network while
publishing the port to the host's loopback only. A `kubectl port-forward`
still reaches the default, since it connects inside the pod.

## 2026-10-16: offline mode exists, for the lab only

Crew could only join with a Microsoft account that owns Bedrock, so the live
check, the cluster deployment and multi-unit load tests all waited on
accounts. `MC_OFFLINE=true` skips `mcauth` and dials with a bare display name.

It is safe only because of where it works. A server in online mode verifies
every client against Xbox Live and rejects an offline one, and FWB runs online
mode. So an offline unit cannot join FWB by mistake, and a FWB rollout still
needs real accounts. Start logs one `auth_offline` line so an offline unit is
never taken for a signed-in one.

## 2026-10-16: the lab is pinned to RakNet

Bedrock 1.26.51 defaults a fresh world to NetherNet, which opens no RakNet
listener, and crew dials RakNet only. The lab sets `TRANSPORT: "raknet"`.

FWB runs RakNet deliberately for the same reason, and retail clients still
join it despite the server's startup error claiming NetherNet is the only
supported transport. Pinning the lab keeps it the same shape as FWB, so a
break here is the break that would happen there. Revisit when crew gains a
NetherNet dialer.

## 2026-10-18: Wave 3 splits into 3a and 3b

Acting in the world carries all the protocol risk and ends in something an
operator can watch. `crew-control` needs a database migration, authentication
and a chart, and depends on none of that. 3a (crew acts, with its own task
API) ships first; 3b (`crew-control`) follows.

## 2026-10-18: the task API is local and token-guarded

Task requests tell the unit where to go and what to break, so unlike the
read-only status API they need authentication. The endpoints exist only when
`TASKS_TOKEN` is set, and every request needs it as a bearer token. Until
`crew-control` puts real authentication in front, the token and the default
loopback `STATUS_ADDR` are the whole defence. Task JSON carries lab
coordinates, so task logs record the id, template and state, never the body.

## 2026-10-18: unknown blocks are solid

The pathfinder and the collision check treat a block crew has not received as
solid. The cost is that crew will not cross unloaded terrain; the alternative
is walking into a void the server would correct or kill it in.

## 2026-10-18: progress is counted from the inventory

`MineBlock` is done when the inventory holds `qty` more of the item, not when
`qty` blocks have broken. A break can succeed with nothing collected (a full
inventory, a drop out of reach, the wrong tool), and counting breaks would
report a gather that never happened. Three breaks without a pickup fail the
skill with `no pickup after 3 breaks`.

## 2026-10-19: lab commands target the unit by name

The cluster lab is a world real players join, so the scene script never
selects `@a`: it would teleport and strip them. It takes the unit's username
and defaults to `crew-1`. The scene's platform is not stone, because mining
takes the nearest stone and, found live, that was the platform under the unit
rather than the quarry.

## 2026-10-19: parallel units have a fixed number and a name

Units are crew-01 Agate, crew-02 Basalt and crew-03 Cobble: the number is the
identity (`CREW_ID`, logs, deployments) and the name is the Bedrock username
players see. Neither is ever reused for another unit.

## 2026-10-19: chests are the only hand-off between units

Units never message each other or share state. One unit's output is a chest,
the next unit's input is the same chest, and a hauler waits for items to
arrive. A crashed unit therefore leaves a chest that is simply less full. The
others are not told: a hauler waits until it is cancelled or times out.

## 2026-10-19: a unit with no task still ticks, and defends itself

Found live: crew-01 to crew-03 died to zombies every few minutes. With no
task the runner sent no input, so the unit stood frozen: it took no knockback,
never turned and never fought back, and a server that expects an input every
tick had a player that was alive but silent.

An idle unit now sends one input per tick, takes the server's knockback
(`SetActorMotion` becomes the predicted pose's velocity, so the server stops
correcting it) and, by default, looks around and strolls within a few blocks
of where it went idle. The stroll is bounded by `IDLE_RADIUS`, planned over
known blocks only, and limited to floor that is neither liquid nor hurtful,
because the planner alone would happily stand on water or lava.

Defence preempts a running task. The skill is stopped, the unit fights or
flees, and the skill resumes through a `Resume` that forgets which step it was
in but keeps its progress; `Ready` cannot be reused, since a deposit part way
through no longer holds what it began with. The task timeout does not count
fight time, because a timeout that a mob can trigger would make a gather task
fail for being attacked. Resuming restarts the skill's current step
rather than its place in it: a pending chest request is forgotten, and a
withdraw may be counted short and take a stack again.

Judgment calls: creepers are backed away from, not meleed, since the blast
costs more than the kill gains; a retreat that cannot shake its threat for 10
s becomes a fight, as does one with no escape route; a mob with no route to it
is ignored for 10 s, so a zombie behind a wall cannot hold a miner hostage.
So is a mob the unit has fought for 30 s, unless it hit the unit recently from
within reach; an attacker still hitting at arm's length is not dropped.

**crew never attacks a player.** Only entities the server announced as mobs
of a hostile type are targets. A player's blow makes the unit face them and
step back, which is all it does. The cost is that a griefer can hit a unit
freely; the gain is that a bug in target choice cannot turn a worker into a
danger to the people it shares the world with.

## 2026-10-19: units eat when hungry and rearm from a kit chest

A unit that dies loses its sword and bread (keepInventory is off on the lab)
and, on normal difficulty, health only regenerates at 18 of 20 food. So it
fought barehanded and never healed.

**Eating.** The unit tracks its hunger from its own `minecraft:player.hunger`
attribute. Below the threshold, with food on the hotbar and no fight, the
runner hands it the tick (the same preemption a fight uses: stop the skill,
act, `Resume`). The
sequence is `MobEquipment` to select the stack, an `InventoryTransaction`
with `UseItemTransactionData{ActionType: ClickAir, TriggerType:
PlayerInput, HotBarSlot, HeldItem}`, a `PlayerAuthInput` with
`InputFlagStartUsingItem` set on the next tick only, still inputs while
holding, and an `InventoryTransaction` with `ReleaseItemTransactionData`.
Thresholds: 17 when hurt (regeneration needs 18, so at most a point or two is
wasted) and 14 otherwise (so a 5-point bread never overshoots 20 by much and
a full unit never tries, which the server refuses).

Confirmed from source (gophertunnel v1.62.0, dragonfly v0.11.5):
- `UseItemActionClickAir` with `TriggerTypePlayerInput` is what starts a use;
  dragonfly's handler calls `UseItem` for it, and compares `HeldItem` with the
  stack in the held slot, ignoring the click on a mismatch.
- `InputFlagStartUsingItem` exists in `PlayerAuthInput`. dragonfly ignores it;
  it is the flag a vanilla client sets on the tick it starts using an item.
- `ReleaseItemTransactionData` carries the hotbar slot, held item and head
  position; dragonfly's `ReleaseItem` ends the use, and a releasable item is
  only released when its requirements hold.
- Completion shows as the stack's count dropping or hunger rising, both
  arriving as `InventorySlot`/`InventoryContent` and `UpdateAttributes`, which
  belief already applies. `ActorEventFeed` (57) is only the eating particles
  sent to viewers, so it is not used as a completion signal.

Not confirmed, needs a live check on the lab:
- That Bedrock Dedicated Server starts the meal from the ClickAir transaction
  alone, or also wants `InputFlagStartUsingItem`. Both are sent.
- That it finishes the meal itself after about 32 ticks of held use rather
  than waiting for the release. The unit holds 34 ticks and releases either
  way, and reads the count or hunger afterwards for 20 ticks.
- Whether a release after the server already finished the meal is harmless.
- Whether the server wants `TriggerTypeSimulationTick` transactions while the
  use is held. None are sent.
If a meal does not land the unit logs `eat_failed` and does not retry for a
minute, so a wrong guess costs a minute of hunger, not a loop.

**Kit chest.** `KIT_CHEST` names a chest; a unit with no sword/axe or no food
on its hotbar, idle for five seconds, runs the existing withdraw skill on it,
asking for the best sword and up to `KIT_FOOD` food through a ranked budget
(`skill.OneOf`) rather than by name, because the chest's stock is not known in
advance. Only what is missing is asked for. A kit never interrupts a task or a
fight (a fight stops the visit and closes the chest), and the task queue waits
for a visit in progress. The hotbar is the test because a unit can only hold a
hotbar stack; for the same reason the withdraw only places into the hotbar,
and a unit with no free hotbar slot does not visit. A kit that cannot make the unit whole is not retried for three
minutes. Judgment call: a unit's weapon sitting outside the hotbar counts as
no weapon, since `loadout` never selects it either.

## 2026-10-19: units fell whole oaks (JDW-119)

The `oak_log` gather recipe used the plain mine skill, which only breaks logs
a standable cell puts within reach. A full oak (trunk of 4 to 7 logs under a
canopy) has logs above that, so the unit took the low ones and then failed
with "no reachable oak_log".

**Approach.** `oak_log` now expands to a `fell_tree` skill that wraps the
mine skill. It runs the mine skill from the ground first, which takes every
log within reach and collects the drops. If logs remain that no standable
cell reaches, it pillars up beside the trunk, cuts what is then in reach,
climbs down and collects the drops that fell.
- Pillar: stand on the ground in a column next to the log, clear any leaves
  in the body column and the jump's headroom, then jump and place a block
  from the hotbar (dirt, or cobblestone when a pickaxe is also held) under
  the feet at the top of the jump. At most two blocks: the unit that is left
  on one (a failed task, a fight that knocks it off) steps or falls at most
  two blocks, which does no fall damage, so it is never stranded. Reach is
  the mine skill's 4.5 from the eye, so a 7-log trunk needs one block.
- Cutting from the pillar uses the mine skill in a hold-position mode: it
  breaks logs in reach only, does not walk to drops, and does not count
  progress (the drops are on the ground).
- Descent: break the block under the feet (one-block falls), top first, so
  the pillar is cleaned up and its blocks return to the inventory. If a
  break fails the unit walks off, which is safe at two blocks high.
- Collect: the drops rest where the logs were, so the unit visits the
  columns it cut, standing on the ground there until the pickup lands.
- Leaves are only cleared where the unit's own body or jump needs the cell.
  The canopy is never cleared. Logs count as the tree if they are oak logs in
  the task's area within the mine radius, so branches are cut when in reach;
  the `area` bound applies to every log broken, and every pillar block and
  cleared leaf must lie inside it too, or that log is left.
- A fight preempts as for any skill: `Resume` from the pillar cuts what is in
  reach and descends; from the ground it carries on. At most four pillars
  are built per task and a log that survives an attempt is ruled out.

**Rejected.**
- Standing in the trunk's own column after cutting the base: no more reach
  (the eye stays 1.6 above the feet) and floating logs sit over the head.
- Jump-mining: the unit gains 1.2 blocks for 3 ticks only, and the break
  takes longer than that.
- Climbing leaves or a ladder: not walkable in the movement model, and no
  scaffolding material to rely on.
- A separate template: callers should say "gather oak logs", not how.

**Not done.** Replanting saplings. Fell logs of other species.

**Live check** (private Bedrock Dedicated Server, offline, 7-log oak with
leaves hanging over the pillar's column): the task `gather_and_deposit
oak_log qty 7 area ...` ended `done`, the chest held 7 `oak_log`, the unit's
three dirt were back in its inventory and it stood on the ground at full
health. Confirmed:
- The server accepts the `ClickBlock` transaction on a top face with the
  clicked block's hash as `BlockRuntimeID`, sent in the air above the target
  cell, on the first jump.
- A leaf above the pillar is broken by hand in time, and breaking the top log
  from the pillar worked with leaves around it, so no line-of-sight leaf
  clearing was needed.
- Breaking the block under the feet drops the unit one block and returns the
  dirt.
- Pickups: the last log landed after the unit stood in the column, which is
  why collection waits for the inventory to reach the expected count (up to
  2 s per column) instead of a fixed pause.

Not live-checked: a fight on the pillar (the sim covers a resume at every
step), a 2-block pillar, a cobblestone pillar, and the saplings the leaves
drop are left on the ground.

## 2026-10-19: units dig a stair shaft and a branch mine

**Decision.** A `mine_shaft` template digs a staircase down from an entrance
to a depth, tunnels up to three branches off the bottom, climbs out and
deposits everything the unit gained into one chest. It is one skill
(`mine_shaft`) that reuses the navigator for every walk and the deposit skill
for the hand-off; its only new mechanics are the hazard checks and the break
loop.

```json
{"template":"mine_shaft","entrance":{"x":0,"y":0,"z":0},"facing":"north",
 "depth":12,"branches":2,"branch_length":8,
 "area":{"min":{...},"max":{...}},"to":{"x":0,"y":0,"z":0}}
```

`area` and `to` are required: the lab bound is not optional for a skill that
removes terrain. Depth 1 to 32, branches 0 to 3 (forward, left, right of the
facing, all off the shaft's bottom cell), branch length 1 to 32. Submit
rejects a plan with any cell outside `area`, so a bad request fails
there, and the skill checks every cell again before opening it.

**Geometry.** One block down per block forward, three cells tall per step, so
the unit walks both ways with the planner's existing step-up and one-block
drop and needs no items. Each step is dug top cell first from the previous
step's floor, so a cell is only ever opened beside or above the unit and never
under it. A branch is a two-high tunnel on the bottom level.

**Hazards.** Before a cell is opened, and again before every re-break, the
skill reads the cell's six neighbours (and the floor the unit will stand on).
Lava, water, magma, a missing or unknown block, a floor that is not solid, or
a cell outside `area` rules out that heading: the skill logs `mine_hazard`
with the kind, leaves the cell closed and moves on. A shaft that is ruled out
after at least one step still gets its branches, at the depth it reached; one
ruled out at the first step fails the task with the hazard named. A branch
ruled out is skipped. Nothing is breached, so there is nothing to seal; sealing
would need side-face placement the actuator does not have.

Falling blocks: when a cell has gravel, sand or powder above it, the check
also looks up that column and at what caps it, and after the cell is broken
the skill waits for the column to settle, then breaks whatever fell into the
cell, up to 12 times, re-running the hazard check each time. Past that it
fails the heading.

**Delivery.** Anything whose inventory count rose since the start, which is
the cobblestone, ore, gravel and dirt the digging dropped, is deposited with
the deposit skill after the unit is back on the surface. Tools and torches
did not rise, so they stay.

**Rejected.**
- A ladder shaft: needs a stack of ladders and is not walkable.
- Digging straight down: no way back and no way to see what is below.
- Sealing a hazard with a held block: needs a side-face place; not opening it
  is as safe and needs no items.
- Torches: the ticket makes them optional and the actuator only places on top
  faces, so tunnels are left dark for now.

**Needs a live check.** Walking the stairs both ways under the real physics,
drops from a staircase landing on the steps, gravel falling on the server's
timing, and that a lava pocket two blocks from a branch is left alone.

**Live check** (private Bedrock Dedicated Server, offline, a flat stone slab
with a gravel stack in the third step's column, a lava pocket beside the
forward branch and a water pocket in the left branch): the task `mine_shaft`
at depth 6 with 3 branches of 6 ended `done`; the unit stood on the surface at
full health, never died, and the chest held 35 `cobblestone` and 3 `gravel`.
The log showed one `mine_hazard` of kind `lava` (forward branch stopped three
cells in) and one of kind `water` (left branch stopped two cells in); the cells
beside both pockets were still stone and the pockets intact. Confirmed:
- The stairs walk both ways under the server's physics, and drops from a
  staircase are collected on the way.
- Gravel is slow to fall on the server: the first attempt moved on while it
  was still hanging and it dropped into the cells dug below, so the skill now
  waits while the block above the cell is still falling kind.

Not live-checked: a fight mid-shaft (the sim resumes at 12 points), sand,
a hazard that appears after a cell is opened, and ore other than stone.
