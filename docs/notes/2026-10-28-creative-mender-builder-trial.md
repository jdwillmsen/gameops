# Creative mender and builder live trial (JDW-291)

Status: done. Both goals worked on a real offline Bedrock server (itzg 2026.10.2, BDS 1.26.60, VIEW_DISTANCE 6). Two production fixes came out of it (see Problems).

## Setup

- Own lab: copy of `minecraft/crew/lab/compose.yaml` in `/tmp/jdw291trial`, edited to `name: jdw291trial`, only the `lab` service, host port `127.0.0.1:19392`, `VIEW_DISTANCE: "6"`, offline mode. Project: `docker compose -f /tmp/jdw291trial/compose.yaml up -d`.
- The labcreative/arenarepair/arena.sh guard accepts the service name `lab` unchanged; it resolves the project from the `-f` file, so the copy's `name:` keeps every console command on `jdw291trial-lab-1`. No guard was weakened.
- Units run as local processes from a binary built into `/tmp` (`MC_OFFLINE=true CREW_LAB=true CREW_ROLE=mender|builder`, `STATUS_ADDR=127.0.0.1:1839x`, `TASKS_TOKEN` from a random file, never printed).
- Colosseum slice: `colosseum.py` output filtered to the fills that intersect x -332..-288, y 40..90, z -782..-738 (891 of 39353 fills), fed through a copy of `arena.sh --plan` (about 1 min). The repair area is x -320..-300, y 60..72, z -770..-750.

## Procedure

```
# lab (own project, own port; the copy sets name: jdw291trial)
docker compose -f /tmp/jdw291trial/compose.yaml up -d
# world: dirt to y 62, grass at 63, air above, then the Colosseum slice
bash /tmp/jdw291trial/arena.sh --plan flat-plan.txt build lab      # copy of lab/arena.sh next to the compose copy
# units (local binaries, tokens from a 0600 file)
MC_HOST=127.0.0.1 MC_PORT=19392 MC_OFFLINE=true CREW_LAB=true MC_VIEW_DISTANCE=6 CREW_ROLE=mender MC_USERNAME=Dusty CREW_ID=crew-91 STATUS_ADDR=127.0.0.1:18391 timeout 45m crew
... CREW_ROLE=builder MC_USERNAME=Smith CREW_ID=crew-92 STATUS_ADDR=127.0.0.1:18392 ...
# creative after every join, then move the unit next to the work (lab console tp)
labcreative --compose compose.yaml --lab lab --units Dusty --once --max-duration 1m --max-commands 3
docker compose -f compose.yaml exec -T lab send-command tp Dusty -310 64.5 -758
# reference copy of the repair area for an exact check
send-command clone -320 60 -770 -300 72 -750 -320 100 -770 ; send-command testforblocks <same area> -320 100 -770
```

## Goal 1: mender repairs the Colosseum (worked)

Area x -320..-300, y 60..72, z -770..-750 (4851 cells scanned). Before damage `repair_build` dry run: `triage: 0 of 4851 scanned cells damaged`. Damage by console: 35-cell hole in the floor (`fill ... air`), 27 stray stone blocks at y 64-66, 3 diamond blocks in the sand.

- Dry run (28 ms): `triage: 65 of 4851 scanned cells damaged in 7 rectangles, 0 not scanned; missing 35, intruder 27, replaced 3; zones floor 65; top 30 air -> red_sand, 27 stone -> air, 5 air -> gold_block, 3 diamond_block -> sand`. Exact match to the damage.
- First real run: failed at once, `a hostile mob is nearby` (the lab was in night, a zombie within 32 blocks; the fight guard working). After `difficulty peaceful`, `gamerule domobspawning false`, `time set day` and killing the mobs:
- Real run: `repaired 65 cells; nothing left to repair`, 19.6 s wall clock (06:05:14.9 to 06:05:34.5), 38 blocks placed, 27 broken, 3 creative takes (red_sand, gold_block, sand), 0 failed, 0 retries.
- Verification: `testforblocks` against the pre-damage clone logged `5733 blocks compared` with no error (it printed `Source and destination are not identical` after the damage); a second dry run reported `0 of 4851 scanned cells damaged`.

A mender therefore cannot work while any hostile mob is within 32 blocks, which at night on a normal-difficulty lab is most of the time: the lab needs peaceful or a mob-free area for mender runs.

## Goal 2: builder stamps small_house (worked, after one fix)

Site x -304..-298, y 64..68, z -474..-468 on prepared grass (the Task API bounds a builder to x/z -500..499, so the Colosseum site at z -758 cannot take a stamp).

- Dry run (33 ms): `total 173, to_place 172, skipped_state {wooden_door: 1}`.
- First real run failed: `a cell for oak_planks is in an unloaded chunk` (bug 2 below).
- After the fix: `placed 172, skipped_state {wooden_door: 1}`, 28.7 s wall clock (about 6 blocks per second), 0 retries. A second dry run: `already_correct 172, placed 0`.
- Console check with `clone ... filtered normal <block>` over the footprint: oak_planks 160, glass 8, torch 4, exactly the blueprint's materials; the door is the expected skip.

## Production bugs fixed

1. 8b0f8c22 `fix(crew): drain the tar padding...` (arenarepair snapshot hang).
2. 7ed72867 `fix(crew): treat sections above a column's announced limit as air`: the block store left sections above a column's `SubChunkLimit` unknown, so a builder on open ground (column top at y 63) failed on its first cell and a block placed there was dropped. Test: `TestSectionsAboveTheAnnouncedLimitAreAir`.

## Other observations

- Join with `VIEW_DISTANCE: 6` was clean every time (4 joins); no batch-limit disconnects.
- The mender process hit its 45 minute cap while the session was paused and had to be restarted (and re-set to creative); a restarted unit is a fresh survival player, as documented.
- Placing sand-like blocks needs support below; the production lab's flat grass supplies it, a default-terrain world does not (problem 2 above).

1. `arenarepair` snapshot hung forever after `save hold` on a real server: GNU tar pads to a whole record after the end-of-archive marker, the reader stopped at the marker and the writer blocked on the unread padding, so `save resume` never ran until killed. Fixed in 8b0f8c22 with a failing-first test.
2. Default-terrain lab world: the plan's `sand` at y 63 sits on air at y 61-62 and falls. The production lab is flat with grass at y 63. The trial first filled the slice with dirt to y 62 and grass at 63, then built.
3. First build had 151 `Cannot place blocks outside of the world` errors, which `arena.sh` filters from its error count: the ticking area was not fully loaded when the fills ran. Adding the ticking area 45 s earlier fixed it (arena.sh only probes one corner column).
4. `arenarepair --lab` snapshot is stale for chunks that are still loaded (ticking area or player nearby): changes made minutes earlier were absent from the copied db, even with the ticking area removed and the player 260 blocks away. So re-diffing with arenarepair after a repair is not reliable on a live lab; the trial verifies with console `clone` + `testforblocks` and the task's own re-diff instead.
5. `setblock` into an unloaded chunk (after removing a ticking area) silently does nothing; this was how diamond/emerald markers ended up in the world and were correctly reported by the mender's dry run.
