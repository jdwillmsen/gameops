# Creative mender and builder live trial (JDW-291)

Status: in progress (notes written as the trial runs).

## Setup

- Own lab: copy of `minecraft/crew/lab/compose.yaml` in `/tmp/jdw291trial`, edited to `name: jdw291trial`, only the `lab` service, host port `127.0.0.1:19392`, `VIEW_DISTANCE: "6"`, offline mode. Project: `docker compose -f /tmp/jdw291trial/compose.yaml up -d`.
- The labcreative/arenarepair/arena.sh guard accepts the service name `lab` unchanged; it resolves the project from the `-f` file, so the copy's `name:` keeps every console command on `jdw291trial-lab-1`. No guard was weakened.
- Units run as local processes from a binary built into `/tmp` (`MC_OFFLINE=true CREW_LAB=true CREW_ROLE=mender|builder`, `STATUS_ADDR=127.0.0.1:1839x`, `TASKS_TOKEN` from a random file, never printed).
- Colosseum slice: `colosseum.py` output filtered to the fills that intersect x -332..-288, y 40..90, z -782..-738 (891 of 39353 fills), fed through a copy of `arena.sh --plan` (about 1 min). The repair area is x -320..-300, y 60..72, z -770..-750.

## Problems found

1. `arenarepair` snapshot hung forever after `save hold` on a real server: GNU tar pads to a whole record after the end-of-archive marker, the reader stopped at the marker and the writer blocked on the unread padding, so `save resume` never ran until killed. Fixed in 8b0f8c22 with a failing-first test.
2. Default-terrain lab world: the plan's `sand` at y 63 sits on air at y 61-62 and falls. The production lab is flat with grass at y 63. The trial first filled the slice with dirt to y 62 and grass at 63, then built.
3. First build had 151 `Cannot place blocks outside of the world` errors, which `arena.sh` filters from its error count: the ticking area was not fully loaded when the fills ran. Adding the ticking area 45 s earlier fixed it (arena.sh only probes one corner column).
4. `arenarepair --lab` snapshot is stale for chunks that are still loaded (ticking area or player nearby): changes made minutes earlier were absent from the copied db, even with the ticking area removed and the player 260 blocks away. So re-diffing with arenarepair after a repair is not reliable on a live lab; the trial verifies with console `clone` + `testforblocks` and the task's own re-diff instead.
5. `setblock` into an unloaded chunk (after removing a ticking area) silently does nothing; this was how diamond/emerald markers ended up in the world and were correctly reported by the mender's dry run.
