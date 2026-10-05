# mcmap live pack

A Bedrock behaviour pack that samples where players and mobs are and prints
it to the server's console, one JSON record per line. The console bridge
reads those lines and mcmap draws them on the map.

It ships inside the mcmap image (`go:embed`, see `embed.go`). There is no
separate artefact to download.

## What it must never do

The world it runs in is no-cheats survival with no experiments, and its
achievements depend on that. So the pack is read-only by construction:

- It depends on `@minecraft/server` `2.10.0` and nothing else. No `-beta`
  version, no second module, no `capabilities`, no resource pack.
- It never runs a command, changes an entity, writes a dynamic property or
  subscribes to an event. With no event subscription there is nothing it
  could cancel. Its only registration is one `system.runInterval` timer.
- It never prints a line the server would break: see the size cap below.

## Pack identity

| | UUID |
| --- | --- |
| Pack (header) | `dc4810d2-bfe2-4f23-b05b-ca86b34a158f` |
| Script module | `28778864-3ebe-413f-9c50-f29630e8be3e` |

These are fixed for good. The world registers the pack by the header UUID, so
a changed one orphans the registration in every world that has it.

## Records

```
[2026-10-05 11:47:50:385 INFO] [Scripting] MCMAP1 {"gen":99,"tick":562338523,"ts":1791200870385,"dim":"overworld","kind":"mobs","part":1,"parts":3,"more":0,"items":[{"i":"-335007449016","t":"creeper","x":-22,"y":20,"z":-91.5}]}
```

`MCMAP1 ` is a sentinel and a format version: anything after `[Scripting] `
that does not start with it is some other line and is to be ignored. The pack
itself prints one such line at load, naming its version and mob cap.

The shape is the one `internal/live` parses, and that parser is strict: a
record of a kind it does not know, or with one entity it cannot place, is
refused whole. So there are exactly three kinds, and anything else the pack
has to say rides on the heartbeat as fields the parser ignores.

Every record has `gen`, a counter that goes up by one per sample and restarts
at 1 when the server or its scripts are reloaded; `tick`, the server tick the
sample was taken on; and `ts`, the server's clock in Unix milliseconds when
the record was written.

| `kind` | When | Fields |
| --- | --- | --- |
| `players` | every sample, every dimension, even when empty | `dim`, `part`, `parts`, `more`, `items` |
| `mobs` | every sample, every dimension, even when empty | `dim`, `part`, `parts`, `more`, `items` |
| `tick` | every sample, last | `players` and `mobs` (how many were sent), `more`, `scan` and `interval` in ms, `cap`, `level`, `errors`, and `error` on a sample that failed |

`dim` is `overworld`, `nether` or `end`. A list too long for one record is
split; `part` counts from 0 and `parts` is the total, so a reader has a whole
list only when it holds every part of one `gen`. An empty list is still sent,
as one part with no items: it is what clears a dimension the last mob has
left. `more` is how many entities the cap left out of that list, and is the
same on every part. A list never runs to more than 256 parts; what would not
fit is counted in `more`.

The `tick` record is the heartbeat. It is sent with nobody online, so a
stream that goes quiet means something broke, never that the server is empty.
`scan` is what the sample cost. `interval`, `cap` and `level` are the throttle
state the *next* sample will use, so a change shows on the sample that caused
it; `level` 0 is unthrottled. `errors` counts samples or dimensions that
threw since the script loaded, and `error` carries the message on the sample
it happened in.

Items:

| Key | Meaning |
| --- | --- |
| `i` | Entity id, as a string. For a player it is the id of this session, not an XUID; the stable API has none. An entity with no id, or one over 64 bytes, is left out |
| `n` | Player name, or a mob's name tag when it has one. Cut to 64 characters |
| `t` | Mob type without the `minecraft:` prefix |
| `x` `y` `z` | Position, rounded to one decimal. An entity whose position is not a finite number is left out |
| `r` | Player yaw in whole degrees |

### What counts as a mob

Everything `getEntities` returns except a list of types and families that are
not creatures: players, items, experience orbs, projectiles, minecarts,
boats, armour stands, NPCs and the like. The list is in `NOT_MOBS`.

Filtering on the `mob` family would have been shorter and is wrong in both
directions. Spawning one of every type on 1.26.52.3 and asking each showed
that goats, pandas, piglin brutes and all four fish lack the family, while
armour stands and NPCs have it. An entity type added by a later release is
reported until it is added to the list, which is the safe way to be wrong.

## Limits

| | Value | Why |
| --- | --- | --- |
| Sample interval | 1 s (20 ticks) | The map's cadence |
| Record size | 3,500 bytes of JSON | See below |
| Mobs per dimension | `PACK_MOB_CAP`, default 1,000, at most 5,000 | Bounds what is read, logged and drawn |
| Players | not capped | The server's own player limit is the bound |

### The size cap

A console line of 4,093 bytes or more makes the server start the *next* line
with a NUL byte. Measured on 1.26.52.3 by printing lines one byte apart: a
4,049-byte message was clean and a 4,050-byte one was not. The count is in
UTF-8 bytes and includes the server's own `[<timestamp> INFO] [Scripting] `
prefix, which is 43 bytes for `console.log`. A record is therefore held to
3,500 bytes, counted as bytes rather than characters, with multi-byte names
counted at their upper bound.

### The mob cap, and what it does not bound

The cap is passed to the query as `closest`, with the first player in the
dimension as the point to measure from, so the server hands the script at
most that many entities and the ones kept are those nearest a player. A
dimension with no player in it measures from the origin.

That bounds almost all of the cost, but not all of it. Measured on the rig
with 5,037 mobs in one dimension:

| Query | Time |
| --- | --- |
| every mob | 2.45 ms |
| `closest: 1000` | 1.20 ms |
| `closest: 1` | 1.10 ms |

So the server still walks every entity to find the nearest (about 0.2 ms per
1,000), and no query option avoids that. What `closest` saves is building the
entities for the script, and what the cap saves beyond that is reading them:
reading a position is the single largest cost of a sample.

Because a capped query cannot say how many it left out, `more` comes from a
full count taken on the first sample that reaches the cap and every tenth
after it, and is repeated unchanged in between.

The cap is read from `scripts/config.js`, which is written when the pack is
installed: a script cannot read the container's environment.

### The self-throttle

Each sample is timed, all three dimensions together.

- Over 20 ms: the next step down. The interval goes 1 s, 2 s, 4 s, 5 s; once
  it is 5 s, each further step halves the mob cap, to a floor of 100.
- Under 10 ms for 30 samples in a row: one step back up.
- Between the two: it stays where it is.

Each change shows as a new `level` on the heartbeat. The tick rate always
wins over the map's freshness. The throttle does not make a sample cheaper than walking
the entities costs; at the floor that walk is what is left.

What that means at the default cap, measured on the rig (1.26.52.3, a
Ryzen 9950X): a sample cost about 2 ms with the 150 to 200 mobs loaded
around one or two players, and about 6 ms for each 1,000 mobs sent. With the
cap reached in all three dimensions at once, 3,000 mobs, it took 17 to 22 ms.
That is under the first threshold most of the time and never under the
second, so the pack backed off on its first slow sample and stayed backed
off, at a 5 s interval and a cap of 500 within ten minutes, until the mobs
were removed. It then stepped back to 1 s, one level per 30 samples. A cap
that holds the 1 s cadence is one whose full sample stays under 10 ms.

Console output follows the same count, at about 60 bytes a mob: 1.2 KB/s
with nobody online, 11 to 14 KB/s at the loads above, and 177 KB/s
(15 GB a day) for 3,000 mobs sampled every second. Every byte of it is also
a container log line.

The server's own script watchdog sits behind this one and is much further
out. With the default `server.properties` it logs a script that is slow
across several ticks (10 ms) or spikes in one (100 ms), and shuts the server
down if a single tick's script time passes 10 s or script memory passes
250 MB.
