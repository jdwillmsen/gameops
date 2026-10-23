# Crew: a unit detail API for inventory, armour, effects, XP and the ender chest

**Status:** implemented; the rollout wiring below is not yet in the deployments repo
**Date:** 2026-10-23
**Ticket:** JDW-147
**Builds on:** `docs/specs/2026-10-19-crew-console-and-control-design.md` (the console's route families)

## Why

The console shows a unit's vitals and its task, but nothing it holds. What a
unit carries — its inventory, what it wears, its experience, and above all its
ender chest — lives in the lab world's save, which no service but the map
reads. The owner wants one read-only API per unit that answers all of it,
merged with the fresher live state the console already polls.

## What was built

`GET /units/{id}/detail` on crew-control:

- **Inventory, armour, XP and all 27 ender chest slots** decoded from the
  world save: each item with id, count, slot, durability, custom name and
  enchantments, and what a shulker box carries. `enderChest` is every slot
  in slot order, `null` for an empty one.
- **Merged with live state** from the once-a-second poll of each unit's
  `/v1/state`: while the unit is online, live health, hunger and inventory
  win (a live stack keeps what the save knows about it — durability, a
  custom name, enchantments, box contents); effects are live only, because
  the save holds none (the units now track their own `MobEffect` packets to
  report them). Armour, XP and the ender chest come from the save alone.
  Live wins by being online, not by comparing times: the time on the save is
  when it was read, and the record itself carries none, so such a comparison
  would say nothing about which is newer.
- The body carries provenance: `save` and `live`, each
  `known`/`none`/`pending`/`off` with an `asOf` time.
- The answer stays under 50 KB (past the cap it drops box contents) and is
  cached: the save is read at most once every few seconds
  (`CREW_WORLD_EVERY`, default `5s`) and held between reads.

## Decisions

1. **Signed-in only.** The camera and `/v1/state` routes are public by the
   owner's choice, but the ender chest is private to its owner, so the
   detail route answers 401 without an operator session and is marked
   `Cache-Control: private`. It is crew-control's own route, not added to
   the public `/units/` proxy allow-list; the proxy's "anything else under
   `/units/` is 404" promise is unchanged. The public gateway route
   (`crew.route` in the deployments repo) lists `/units` as a prefix, so it
   does pass `/units/{id}/detail` through to crew-control: the 401 there is
   the only guard, and a test holds it (signed out, the route is refused and
   the unit never sees a request).
2. **One shared decoder, not a copy.** The map's player-record decode moved
   to `minecraft/pkg/playerdata` (with the read-only LevelDB open in
   `minecraft/pkg/worldview`); mcmap's `internal/players` is now aliases
   over it and its behaviour and tests are unchanged. crew-control reads
   the same records through the same code.
3. **Read-only, and no small writes to the world's volume.** The save is
   opened on linked files in a scratch directory (`CREW_WORLD_WORK`), so a
   read writes nothing where the world lies — not even the `LOCK` file a
   read-only LevelDB open would create. On the world's own volume each read
   links the save's files; anywhere else, including from a read-only mount,
   it copies them (26 MB for the lab) and removes the copy.
4. **A unit finds its record by the id it reports.** The save files a
   record under the player's UniqueID, which the unit holds from its game
   data and now reports as `unique_id` in `/v1/state`. A `player` in
   `CREW_UNITS` names it by hand instead and wins, for a unit that has not
   been online since crew-control started. Reporting it spares the
   deployments repo a hand-copied number for each of 20 units that would
   change whenever the lab is rebuilt. The assumption — that the id in the
   unit's game data is the id of its record — is checked at rollout (below).

## Where the lab save is, and how crew-control can reach it

- The lab world is `/data/worlds/crew-lab/db` in the Bedrock StatefulSet
  (`datadir-jdwillmsen-crew-lab-prd-minecraft-bedrock-0`): 2 Gi,
  `truenas-iscsi`, ReadWriteOnce, the pod pinned to one node. The db is 26 MB
  in 19 files, owned by uid 1000 and world-readable.
- crew-control's volume is a 1 Gi `truenas-nfs` claim of its own for its keys.
  It has no path to the world.
- The lab has no console-bridge sidecar, which is how the map gets the main
  world's snapshot.
- A unit cannot supply the chest: the server sends an ender chest's contents
  only to a client that opens it.

So the choices were a mount of the world's volume, or a bridge sidecar for
the lab. A read-only mount is the least new machinery and is what is built;
the bridge is the alternative if a snapshot taken under a save hold is ever
wanted.

## Rollout wiring still missing

The code reads the lab world save from a path; the deployment does not yet
give crew-control one. This is new chart wiring in the deployments repo,
deliberately not invented here:

1. A **read-only mount** of the Bedrock datadir claim into the crew-control
   pod, with `CREW_WORLD_DB` set to its `worlds/crew-lab/db`. A
   ReadWriteOnce volume can be mounted by several pods only on one node, and
   crew-control is not pinned (it happened to land on the Bedrock pod's node),
   so it needs a pod affinity to the Bedrock pod or the same node affinity.
2. A **writable scratch volume** for `CREW_WORLD_WORK`: an emptyDir. Not the
   NFS data volume, which would take the read's small writes. A read-only
   mount cannot take hard links, so each read copies the 26 MB db into the
   scratch and removes it; `CREW_WORLD_EVERY` (5 s) bounds how often, and a
   read happens only when a request asks.
3. Nothing in `CREW_UNITS` is required once the units run a release that
   reports `unique_id`.
4. Nothing in `crew.route` needs to change to serve it, and nothing there
   blocks it: its `/units` prefix already passes the path to crew-control,
   whose own 401 is the guard. Tightening that prefix to the listed
   camera and state paths would make the gateway refuse it too, and would
   need `tools/tests/test-crew-route.sh` to say so.

On rollout, check that each unit's detail answers `save: known`. If it says
`none` for every unit the reported id is not the record's id, and the
`player` field is the fallback. Then compare the 27 chest slots of each unit
with the world, as the ticket asks.

Until the mount exists the route still serves: every answer says `save: off`
and carries the live half.

Mid-save reads of the live world can see a record between the server's
writes, or a table the server is compacting away; a read that fails is
logged once and the last good records are kept, and a record that does not
decode is skipped, the same as the map's reader skips it.
