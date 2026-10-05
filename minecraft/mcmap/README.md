# mcmap

A web map of a Minecraft Bedrock world, drawn from the running server's own
save rather than regenerated from its seed. It shows the terrain players
actually explored and built, for the overworld, the nether and the end, and
catches up with the server every few minutes.

## How it works

Every `REFRESH_INTERVAL` the service runs one cycle:

1. **Mirror.** It asks the console bridge for a snapshot
   (`POST /snapshot`, see `minecraft/bridge/README.md`), sending the list of
   LevelDB table files it already holds. The bridge pauses saving, streams
   only what is new, and resumes. The files land in `DATA_DIR/mirror`. A
   stream that does not end with the bridge's completion marker changes
   nothing.
2. **Render.** It runs the renderer over the mirror, once per dimension, into
   `DATA_DIR/maps/<dimension>`. Rendering is incremental: unchanged areas are
   skipped, so a cycle after the first takes seconds.
3. **Serve.** The page asks `GET /api/map` what exists, and loads tiles from
   `GET /tiles/<dimension>/<zoom>/<x>/<y>.webp`.

Measured on the FWB world (128,032 overworld chunks, 736 MB on disk), with
four chunk processors: the first cycle mirrored 762 MB in 5 s and rendered in
78 s, 9 s and 11 s; the next mirrored 4.9 MB and rendered in 8 s, 4 s and 1 s.

### The renderer

Tiles are drawn by [uNmINeD](https://unmined.net/)'s command line tool. Its
licence allows free non-commercial use but not redistribution, so it is not
in the image: the service downloads it on first use, checks it against
`UNMINED_SHA256`, and keeps it under `DATA_DIR/tools`.

The download address always serves the newest build, so the pinned digest
stops matching the day a new one is published. An installed copy keeps
working; only a fresh volume needs the pin moved, after checking the new
build renders correctly. The refusal names the digest it was served.

Two things about this renderer are handled in `internal/render`:

- Its default image format, JPEG, has crashed after writing blank tiles while
  still exiting 0. The service always asks for WebP, and rejects a render
  that left no full-scale tile with anything on it.
- The nether is rendered below `NETHER_TOP_Y`; otherwise the map is the
  bedrock roof.

Everything else goes through the `Renderer` interface, so a different
renderer is a new implementation, not a change to its callers.

### Quiet windows

Other jobs (a nightly backup, a census) pause the server's saving themselves,
through a channel the bridge cannot see. If the map resumed saving while one
of them was still copying, that copy could be inconsistent. The bridge
refuses to start while saving is already paused, but nothing stops one of
those jobs starting during a snapshot, so `QUIET_UTC` lists the times the map
must not touch the save state at all. The map also stays away for the six
minutes before each window, the longest a snapshot started then could still
be running, so a window only needs to cover the job itself.

### When a cycle fails

A renderer that cannot run (a digest mismatch on a fresh volume, say) is
checked before the snapshot, so the server is never paused for a snapshot
nothing could use; a refused download is remembered for six hours instead of
being fetched again every cycle. A refused snapshot is retried after a
minute. A failed one is retried after a minute, then two, then four, up to
the normal interval. The page keeps serving the last good tiles throughout
and says the refresh failed; the reason is in the log, not in the API, since
it names internal addresses.

### Counting chunks

A Bedrock world never deletes a chunk in normal play. One that disappears
went with a LevelDB table file, which is what happens when the volume under
the server is lost mid-write: on the next start the server prints
`LevelDB ... status NOT OK(Corruption: N missing files ...). Trying repair.`
and drops whatever it can no longer find.

So after every snapshot, before rendering, the service counts the chunks in
the mirror and compares them with every chunk it has ever seen. A chunk seen
before and absent now is *missing*. Every chunk found missing is also
*lost*, and stays lost until an operator accepts the world as it is: a lost
chunk does not stay missing, because Bedrock generates it again from the
seed as soon as a player comes near, with everything built on it gone. The
places players visit most are the first to come back, so "missing" alone
would clear itself exactly where the loss matters.

`POST /internal/v1/world/acknowledge` with the `checkedAt` of the count being
accepted clears what is lost and stops expecting the chunks still missing.
It is for after a restore has been checked, or after a deliberate rollback,
whose newer chunks are missing too. A count newer than the one named is
refused, since it may hold losses nobody has looked at.

The ledger (`chunks/seen.bin` on the data volume) holds what was seen, what
is missing and what is lost, and is written before a count's result is
used, so a restart neither forgets a loss nor takes the damage as the new
normal; the lost-chunk gauges are published from it at startup, before the
first count. A ledger that cannot be read stops the service rather than
starting again from empty.

A chunk that keeps some of its records and loses others still counts as
present: the count sees whole chunks only. On 2026-10-02, 107 chunks lost
part of their data that way. The console bridge's corruption signal is what
covers that case.

Even a read-only LevelDB open writes a `LOCK` file, and the mirror must hold
only the server's files, so the count runs on hard links to them under
`chunks/`. It takes about 12 seconds on the full world at a whole CPU and a
few tens of megabytes. A count that fails is logged and counted and stops
nothing else; the missing-chunk gauges keep their last value.

### Keeping the world that was there before

The mirror is overwritten by every snapshot, so until this existed it was
also the fastest way to lose a world. On 2026-10-01 at 23:43:38 UTC the
mirror held a complete, consistent copy of the world, taken 115 seconds
before the node froze. The first snapshot after the server repaired its
database, at 02:04:49, replaced it with the damaged one. A restore from
that copy would have cost about two minutes of play; the nightly archive
used instead cost about 42 hours.

So the mirror is no longer the only copy. Under `DATA_DIR/generations`:

```
a/            one retained copy of the world
b/            the other
current ->    a symlink naming whichever of the two is the restore point
damaged/      the first snapshot that was found to have lost chunks
```

Each cycle builds into the slot `current` does **not** name, and only then
moves the symlink. The copy being overwritten is therefore always the older
of the two, and the newest whole world is never the one at risk. After the
switch, `current` is the snapshot just taken and the other slot is the one
before it: at a fifteen-minute interval, a restore point from minutes ago
and one from a quarter of an hour before that.

A generation is **hard links** to the mirror's files, not a copy of them.
LevelDB never rewrites a table once it is closed, and the mirror only ever
creates a file, replaces it by rename, or unlinks it, so a link made now
holds that file's content for as long as the link lives — pruning the
mirror cannot reach it. Two generations and the mirror together cost one
world plus what has changed between them: measured on a 763 MiB, 404-file
world, three retained snapshots and the mirror came to 792 MiB in total,
and a capture took 0.5 to 1.3 seconds. Copying instead would cost a second
and a third full world on a volume sized for one, every cycle, for no extra
safety, so a filesystem that cannot link is a refusal rather than a
fallback.

**What is promoted.** Only a snapshot the chunk count above proved whole.
One that lost chunks is moved into `generations/damaged` instead and
displaces neither generation — which is the whole point, since the damaged
world is exactly what overwrote the good copy before. A count that could
not run at all retains nothing: nothing has said the snapshot is safe to
make the restore point. Only the *first* damaged snapshot is kept; every
later one holds the same loss plus whatever the world did afterwards, and
keeping them would fill the volume while nothing can be promoted. Promotion
resumes once the loss is acknowledged, which is why the acknowledgement
comes after the restore and not before it.

**Interrupted part-way.** A capture is: link the world into `.building`,
write its marker, flush; rename the slot being replaced to `.retired-<slot>`;
rename `.building` into the slot; move the `current` symlink; remove the
retired copy. The slot and the `current` link only ever change by a rename,
and a `current` link that exists names a whole world before and after every
step, renames and the rest alike, so a process killed anywhere in here leaves
at least one complete generation and never a partial one presented as the
restore point. A store whose `current` link has gone missing builds beside
the generation it still holds rather than over it, and the link returns with
the next promotion. A directory is a generation only if it holds its
`generation.json` marker, which is written last; the next start discards
`.building`, `.retired-*` and a half-made symlink. The files are flushed
before anything names them, which also flushes the mirror's own writes:
a copy still only in the page cache would be lost by exactly the event it
exists for.

**Running out of room** costs the copy being built and nothing else. The
retained generations are never deleted to make space, the build gives back
what it took, and the failure is logged and counted while the tiles and the
count carry on. The restore point is then older than it could be, never
absent.

The restore procedure — copying a generation onto the restore scratch claim
and promoting it onto the live world — is in the `minecraft-fwb` chart's
README in `jdw-deployments`, which is also where the claim and the server
live.

## Login

The map shows where every base is, so it sits behind a login that proves the
visitor plays on the server.

1. The page asks `POST /auth/start` and shows the six-character code it gets
   back. The secret that will collect the login is set as an `HttpOnly`,
   `SameSite=Strict` cookie that script cannot read.
2. The player types `!map <code>` in game chat. The agent, which sees the
   chat packet and so the sender's XUID, reports it to
   `POST /internal/v1/claims`.
3. The page, polling `GET /auth/status`, is handed a session cookie: the
   XUID, the gamertag, when it was issued and an expiry, signed with a key
   kept on the data volume. No session is stored; each one issued is logged
   with the player it was issued to.

Codes last ten minutes, are used once, and are drawn from 32 symbols with no
`0`, `O`, `1` or `I`. An unknown, expired and already-used code all get the
same answer. Only a player can be logged in: an XUID that is not a number,
such as the console's, is refused.

Anyone can ask for a code, so the table of waiting logins is bounded, at
10,000. When it is full the oldest waiting login makes room for the new one;
refusing instead would let one burst of requests lock every player out.
Pushing out a code a player is still typing takes hundreds of requests a
second, kept up. Nothing here limits requests per client: the service sees
only the gateway's address.

A session cannot be withdrawn by itself, but a player's can be withdrawn
together. `POST /internal/v1/revocations` records the moment, on the data
volume, and every session that player was issued up to then stops working.
The agent calls it for whoever types `!map logout`, which is the way out for
a player who typed a code off someone else's screen. Deleting `session.key`
logs everyone out.

Both cookies carry the `__Host-` prefix, so a browser accepts them only from
this exact host over HTTPS and no other site under the same parent domain
can plant one. Browsers treat `localhost` as secure, so a port-forward still
works. A `POST` that another site started in the visitor's browser is
refused with 403.

The login is on unless `AUTH_DISABLED=true` says otherwise by name; with it
on and no `INTERNAL_TOKEN` the service refuses to start. Other ways to log
in can be added beside the code flow: anything that can establish an XUID
ends in the same `Sessions.Issue`.

Two listeners keep the internet away from what is not for it:

- `HTTP_ADDR` is what a route may publish: the page, the login endpoints,
  and the map API and tiles behind the session.
- `INTERNAL_ADDR` is for the cluster only: `/metrics`, and the claims and
  revocations the agent reports.

## Environment variables

| Variable | Required | Default | Purpose |
|---|---|---|---|
| `BRIDGE_URL` | yes | | Console bridge base URL |
| `BRIDGE_TOKEN` | yes | | The bridge's bearer token |
| `LEVEL_NAME` | yes | | The world's directory name on the server |
| `HTTP_ADDR` | no | `:8080` | Public listener: page, login, and the gated map API and tiles |
| `INTERNAL_ADDR` | no | `:9090` | Cluster-only listener: metrics and the agent's login claims and revocations. Never route this publicly |
| `INTERNAL_TOKEN` | unless `AUTH_DISABLED` | | Bearer token the agent presents to the internal API; at least 16 characters. Whoever holds it can log in as any player, so give it a secret of its own |
| `AUTH_DISABLED` | no | `false` | `true` serves the map with no login. Only for a service nothing publishes |
| `SESSION_TTL` | no | `168h` | How long a login lasts |
| `DATA_DIR` | no | `/data` | Mirror, retained world copies, tiles and the installed renderer. The retained copies are the one thing here that cannot be rebuilt, so keep it on a volume |
| `REFRESH_INTERVAL` | no | `15m` | Time between cycles, as a Go duration. At least `1m`: each cycle pauses world saving for a moment |
| `QUIET_UTC` | no | empty | Daily UTC windows with no snapshot, `HH:MM-HH:MM,HH:MM-HH:MM`. A window may cross midnight |
| `RENDER_CHUNK_PROCESSORS` | no | `1` | Chunks rendered at once. More is faster and uses more CPU and memory |
| `NETHER_TOP_Y` | no | `100` | Highest nether layer drawn |
| `UNMINED_URL` | no | the dev Linux x64 build | Where the renderer is downloaded from; must be https |
| `UNMINED_SHA256` | no | the build this release was verified against | Digest the download must match |

## Endpoints

| Route | Purpose |
|---|---|
| `GET /` | The map page; public, and holds nothing about the world |
| `GET /api/config` | Whether there is a login; public |
| `POST /auth/start`, `GET /auth/status`, `POST /auth/logout` | The login flow above |
| `GET /api/me` | The logged-in player's gamertag |
| `GET /api/map` | Session required. World name, refresh interval, each dimension's extent and last render time, and `problem` (`snapshot` or `render`) while the last cycle failed |
| `GET /tiles/{dimension}/{zoom}/{x}/{y}.webp` | Session required. One 256-pixel tile. Zoom 0 is one block per pixel; each step below halves the scale. 404 where the world has no chunks |
| `GET /healthz` | Liveness, on both listeners |

On `INTERNAL_ADDR` only:

| Route | Purpose |
|---|---|
| `GET /metrics` | Prometheus |
| `POST /internal/v1/claims` | Bearer `INTERNAL_TOKEN`. `{"code","xuid","gamertag"}`: this player typed this code. 204, or 404 for a code that is unknown, expired or used |
| `POST /internal/v1/revocations` | Bearer `INTERNAL_TOKEN`. `{"xuid"}`: end every session this player holds. 204 |
| `GET /internal/v1/world` | Bearer `INTERNAL_TOKEN`. The last chunk count: `checked`, `checkedAt`, and by dimension `chunks`, `missing` and `lost`, with `missingTotal`, `lostTotal`, and up to 20 lost chunks as block coordinates in `lostSample`. `{"checked":false}` before the first count. Also `generations`, with `current`, `previous` and `damaged`, each naming its directory and carrying `takenAt`, `files` and `bytes` — what a restore needs to choose between them |
| `POST /internal/v1/world/acknowledge` | Bearer `INTERNAL_TOKEN`. `{"checkedAt"}` from the GET: accept the world as that count found it. 204; 409 before the first count or if a newer count has replaced that one |

The internal API is served whenever `INTERNAL_TOKEN` is set, with or without
the login.

The page's address carries the view, `#<dimension>/<x>/<z>/<zoom>`, so a link
opens at the same place.

## Metrics

| Metric | Meaning |
|---|---|
| `mcmap_snapshots_total{result}` | Cycles by outcome: `ok`, `busy` (the bridge declined), `quiet`, `failed` |
| `mcmap_snapshot_last_success_timestamp_seconds` | When the mirror last matched the server |
| `mcmap_snapshot_bytes_total`, `mcmap_snapshot_duration_seconds` | Cost of mirroring |
| `mcmap_render_last_success_timestamp_seconds{dimension}` | When each dimension's tiles were last current |
| `mcmap_render_duration_seconds{dimension}`, `mcmap_render_failures_total{dimension}` | Cost and failures of rendering |
| `mcmap_world_chunks{dimension}` | Chunks in the world at the last count |
| `mcmap_world_chunks_missing{dimension}` | Chunks seen before and absent from the last count |
| `mcmap_world_chunks_lost{dimension}` | Chunks found missing since the last acknowledgement, including any generated again since. Above zero means the world has lost data; only an acknowledgement clears it |
| `mcmap_world_census_last_success_timestamp_seconds`, `mcmap_world_census_duration_seconds`, `mcmap_world_census_failures_total` | Whether the count is running |
| `mcmap_generations` | Complete world copies held, 0 to 2. Below 1 there is no near-current restore point |
| `mcmap_generation_current_timestamp_seconds` | When the snapshot now serving as the restore point was taken; the distance from now is how far a restore would roll the world back |
| `mcmap_generation_captures_total{outcome}` | Snapshots by what was done with them: `promoted`, `quarantined`, `skipped`, `failed` |
| `mcmap_generation_damaged` | 1 while a snapshot that lost chunks is quarantined, which is also while nothing is being promoted |
| `mcmap_generation_bytes{generation}` | World bytes `current`, `previous` and `damaged` each name. They are hard links, so this is not the space they add |

## Build and test

From the repo root:

```sh
go test -race ./minecraft/mcmap/...
docker build -f minecraft/mcmap/Dockerfile -t minecraft-map:dev .
```

The page under `web/` is plain files with no build step; Leaflet is vendored
in `web/lib/leaflet` with its licence.

## Releases

Cut by [`semantic-release.yml`](../../.github/workflows/semantic-release.yml)
from commits that touch this component, tagged `mcmap-v<version>`, and
published as `ghcr.io/jdwillmsen/minecraft-map:<version>` and
`docker.io/jdwillmsen/minecraft-map:<version>`. Never `latest`.
