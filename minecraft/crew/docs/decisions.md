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
