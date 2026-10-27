# Why the stage-0 add-on stopped the crew units spawning (JDW-215)

Date 2026-10-27. Status: **root cause not reproduced.** Everything below was run
on a throwaway container; the live lab was not touched.

## Question

After jdw-deployments PR #350 mounted `packs/lab-mods-stage0` (behaviour plus
resource pack) into the crew lab and the server restarted, all 20 crew units
connected and never spawned; each session ended after 120 s with
`connected, awaiting spawn` (connect 30 s + spawn 90 s, `mcdial.DialWithLeave`).
Withdrawing the add-on (jdw-deployments 8c40396) fixed it.

## What the code does

- crew uses gophertunnel v1.62.0 and leaves `Dialer.DownloadResourcePack` nil
  (`minecraft/crew/cmd/crew/main.go`), so the library downloads every offered
  texture pack. Behaviour packs are never sent to a client.
- Everything a pack can change happens inside the dial, before `StartGame`:
  `ResourcePacksInfo` -> client `ResourcePackClientResponse(SendPacks)` ->
  `ResourcePackDataInfo` -> one `ChunkRequest`/`ChunkData` round trip per chunk
  -> `ResourcePackClientResponse(AllPacksDownloaded)` -> `ResourcePackStack` ->
  `ResourcePackClientResponse(Completed)` -> `StartGame`.
- Fragility in that path (`minecraft/conn.go`, `handleResourcePackDataInfo`):
  the chunk loop runs in a goroutine whose failure paths (`incorrect resource
  pack size`, `invalid full resource pack data`) only log and return. Nothing
  then sends `AllPacksDownloaded`, so the dial waits silently until the deadline,
  which is exactly the observed "awaiting spawn" at 120 s. A stalled chunk
  (`<-pack.newFrag`) has no timeout of its own either.
- The server advertises a pack size in `ResourcePacksInfo` that differs from the
  one in `ResourcePackDataInfo` (gophertunnel logs a WARN and adopts the second).
  Harmless in every run below.

## Test setup

`itzg/minecraft-bedrock-server:2026.10.2` (the chart's tag), BDS pinned to
1.26.52.3, `TRANSPORT=raknet`, cheats on, offline, survival, same env as the
crew-lab chart. Client: a minimal gophertunnel dialer using crew's exact path
(`mcproto.Negotiate` then `mcdial.DialWithLeave`, nil callback) with packet
capture, and the real crew binary built from `main`. Packs: the repo's v0.0.2
packs for the matrix; the shipped v0.0.1 tarball plus the real `crew-unit-kit`
script pack, installed with the chart's own init script, for the lab-faithful
runs.

## Matrix (one bot, texturepack-required as shown)

| Packs | required | Spawned | Dial+spawn |
|---|---|---|---|
| none | false | yes | 4.0 s |
| behaviour only | false | yes | 4.0 s |
| resource only | false | yes | 3.9 s |
| both | false | yes | 4.3 s |
| both | true | yes | 4.2 s |

Packets with a pack (ids): 6 ResourcePacksInfo, 8 response(SendPacks), 82
DataInfo, 84 ChunkRequest, 83 ChunkData (3.2 KB, about 135 ms after the
request), 8 response(AllPacksDownloaded), 7 Stack, 8 response(Completed), 11
StartGame. Without a pack the sequence is 6, 8, 7, 8, 11.

## Lab-faithful runs (real crew binary, 20 units at once)

Server: unit-kit script pack plus stage-0 v0.0.1 as shipped.

| Condition | Spawned |
|---|---|
| stage-0 installed, 2 CPU / 2 GiB limits | 20/20 |
| same, 3% loss and 40 ms +-10 ms delay on the server | 20/20 |
| no stage-0 (control), 2 CPU / 2 GiB | 20/20 |
| stage-0, required=true, units declining packs (fix below) | 20/20 |

Twenty simultaneous joins on a fresh world take about 45-55 s per unit with or
without the pack, so concurrency alone does not explain a 120 s failure here.

## Conclusion

With BDS 1.26.52.3 neither pack, nor both, nor `texturepack-required`, nor 20
concurrent units, nor packet loss stops crew spawning. I could not reproduce the
production failure, so the cause is not proven. Candidates that remain, none
checkable without the live lab:

1. A BDS version difference. The chart runs `VERSION: LATEST`; today that is
   1.26.60.6 (protocol 2225), on which even the no-pack baseline fails here
   immediately with `decode packet StartGame` under crew's protocol spoofing.
   That fails fast, not at 120 s, and would have survived the withdrawal, so it
   fits the symptom poorly, but the lab's actual BDS version at 05:27 and at the
   fix is the first thing to read from the pod log.
2. Something particular to the live world or cluster network that stalls the
   chunk exchange. A stalled or failed download in gophertunnel never errors,
   which matches the 120 s timeout.

## Recommended configuration

- Bot side (this PR): crew declines every resource pack
  (`DownloadResourcePack` returns false). It never renders, so the download is
  pure risk and egress. Verified here: with the pack installed and
  `texturepack-required=true`, 20/20 units still spawn, and BDS 1.26.52.3 does
  not eject them. This removes the pack download from the unit's login. It is a
  mitigation, not a proven fix: the failure did not reproduce, and decline
  behaviour was only tested on BDS 1.26.52.3, not on the 1.26.60.x release
  that `VERSION: LATEST` can now resolve to. Retail players still get the
  pack.
- Server side: keep `texturepack-required=false` for the lab so players are not
  forced; ship the add-on only after a crew build with this change is live.
- Rollout order: (1) release crew with the change, units verified spawning on
  the current lab; (2) re-add the add-on; (3) if units fail again, capture the
  pod log first (`Version:` line, `ContentLog`, unit `session_error` text)
  before reverting.
- Fallback if a retail client must not be forced to download: a behaviour pack
  alone spawned in every run; the custom mob then needs a vanilla client entity
  (not tested) and loses its model and texture.

## Caveat on the fix

Declining a pack is accepted by BDS 1.26.52.3 here and in the JDW-214 probe. A
different BDS release could enforce the requirement; the symptom would be an
immediate disconnect with a pack message, not a hang.

