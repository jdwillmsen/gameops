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
