# Map history: the world as it was on each day

Status: proposed 2026-10-12. Storage, capture and the API are built behind
`HISTORY_ENABLED`; the page's timeline is the next piece.

## What is wanted

A timeline. Pick a day and see the map as it was then, for the whole world
or one area, and step through days to watch a base go up.

## What was measured

On the FWB world, 2026-10-12, from the running map:

| | |
|---|---|
| World on disk | 819 MB, about 410 LevelDB files |
| Chunks | 130,796 overworld, 19,470 nether, 5,572 end |
| World bytes fetched by one 15-minute snapshot | 300 to 400 MB (156 to 213 of 411 files) |
| The map's whole volume | 1,332 MB, 4,383 inodes |

The volume holds the mirror, two retained generations, the renderer and the
tiles, so every tile of all three dimensions at every zoom is under about
200 MB and a few thousand files. The exact tile figures could not be read:
the image has no shell, and the first captures will report them.

## Two things that could be kept

**The world itself, once a day.** Rejected. Compaction rewrites a third to
a half of the world's bytes every quarter of an hour, so hard links share
almost nothing across a day and each day costs close to a whole world:
about 0.8 GB a day, 300 GB a year. A kept world is also not a picture. It
has to be rendered again to be looked at, by a renderer that may no longer
read it the same way.

**The rendered tiles, once a day.** Chosen. A tile is a WebP file: nothing
about the game's version, the save format or the renderer is needed to show
it again in ten years. Only tiles that changed cost anything, because each
is stored once under the hash of its bytes and a day is a list of hashes.

## Design

```
HISTORY_DIR/
  blobs/ab/abcdef…        one tile's bytes, named by their SHA-256
  days/2026-10-12.json.gz which blob is at each tile address that day
```

**Capture.** At the end of the first cycle of each UTC day in which all
three dimensions rendered, every tile is hashed, the ones not already held
are copied in, and the day's manifest is written last. A day exists only
once its manifest does, and a manifest names only blobs already flushed, so
a capture killed part-way leaves no day at all and its blobs are reused by
the next attempt. It runs in the cycle's own goroutine, after the renders,
so no tile is being rewritten under it.

**Serving.** `GET /api/history` lists the days, with each dimension's
extent. `GET /api/history/tiles/{day}/{dimension}/{zoom}/{x}/{y}.webp` is
one tile as it was. Both need the session, like the map. A day's tile never
changes, so the browser keeps it for good.

**Nothing is deleted.** There is no retention in this version. History
cannot be rebuilt, the numbers that would size a thinning rule do not exist
yet, and on a claim of its own a full volume costs only the capture that
did not fit.

## Versions

| Change | Effect on history |
|---|---|
| The game server is upgraded | None on days already kept. They are images |
| Bedrock changes its save format | None on days already kept. The renderer has to read the new format before new days look right, which is already true of the live map |
| The renderer pin moves | Colours and shading may shift across the whole map, which reads as if everything changed that day and stores every tile again once. Each manifest records the renderer's digest and `NETHER_TOP_Y`, and the list marks the days where they differ from the day before, so the page can say so |
| The tile addressing changes (another renderer) | Old days keep their own extent and format in their manifest and are served as they were |
| The manifest format changes | It carries a `version`; a reader refuses one it does not know rather than guess |

Not settled by reading: whether the renderer rewrites a tile it has not
changed, and whether it re-encodes one to different bytes. Neither breaks
this. Hashing bytes makes a rewrite free, and a re-encode costs space, not
correctness. `mcmap_history_capture_bytes` is what will say.

## Cost

The worst a day can cost is every tile again, under 200 MB. The expected
cost is the tiles somebody built in plus the six coarser tiles above each,
which is a guess of 5 to 15 MB a day and 2 to 5 GB a year until measured.

## What this leaves out

- **The page.** A day picker, stepping, and a comparison of two days.
- **Markers, structures and biomes as they were.** A day keeps terrain only.
- **Days before this was switched on.** The fourteen nightly archives could
  each be rendered once to seed two weeks of history.
- **Thinning**, once there is a year of numbers to decide it by.

## Decisions for the owner

1. **Where it lives.** The map's claim is 5 Gi of block storage that the
   chart describes as rebuildable and does not back up. History is neither
   small for long nor rebuildable. Recommended: a claim of its own on the
   file tier, 20 Gi, mounted at `HISTORY_DIR`.
2. **Once a day, at the first cycle after 00:00 UTC.** That is early evening
   in the Americas. A different hour, or a local day, is a small change.
3. **A day with lost chunks is still captured.** It is what the world looked
   like. The alternative is to capture only snapshots the count found whole.
