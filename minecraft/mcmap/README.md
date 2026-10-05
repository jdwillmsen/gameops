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

## Live layer

Players and mobs are drawn where they are now, and move within a few
seconds of moving in game.

```
server + script pack   prints one record line a second per list
console bridge         keeps the latest lines     GET /script (long poll)
this service           latest whole list per dimension, in memory
browser                GET /api/live (server-sent events), canvas markers
```

**Where the records come from.** A script pack on the server samples every
player and mob in loaded chunks once a second and prints them to the
console as lines of JSON after a `MCMAP1` sentinel. The console bridge
keeps the newest of those lines in a ring of their own, and this service
holds one request open against the bridge's `GET /script` at a time,
which returns the moment a record arrives.

**One record** is one list, or one part of it:

```
{"gen":417,"dim":"overworld","kind":"players","part":0,"parts":1,"more":0,
 "items":[{"i":"-42949672","n":"Dotablaze","x":120,"y":64,"z":-310,"r":-37}]}
```

`gen` counts samples. `kind` is `players`, `mobs`, or `tick`, the
heartbeat sent with every sample even on an empty server, so that silence
means the pipeline broke and never that nobody is on. A list too long for
one console line is split into `parts`; `more` is how many entities the
pack left out at its cap. A record is refused if it has no generation, an
unknown kind or dimension, a position that is not a finite number (a
script writes `null` for one, which would otherwise be a marker at the
origin), or is over 8 KB. A NUL byte, which the server puts at the start
of the line after an over-long one, is removed and counted.

**What is drawn.** A list replaces the one before it only when every part
of it has arrived. If a part is lost, the last whole list stays until
something whole replaces it or it is `LIVE_TTL` old, measured from when
the bridge received it; then its markers go. A generation lower than the
one before means the server restarted, and lists half built from the old
process are dropped. Each of players and mobs is cut to
`LIVE_MAX_ENTITIES` per dimension, and the page says how many are not
shown.

**The stream.** `GET /api/live?dimension=<id>` sends the current picture
at once and a whole new one after each sample, so a frame a browser
missed is never owed. A browser that stops reading has its unread frame
replaced and holds nobody else up. A stream ends after five minutes or at
its session's expiry, whichever is first, and the browser reconnects by
itself: a session is only checked when a request arrives, so this is what
makes a logout or an expiry stop a stream that is already open. At most
256 streams are open at once; past that the answer is 503.

A stream that is silent for 30 seconds is cut by the load balancer on the
way to the browser, so one that has nothing to say writes a comment line
every `LIVE_KEEPALIVE`. That setting cannot be raised past 20 seconds.

**Turning it off.** `LIVE_ENABLED=false` starts nothing: no request to
the bridge, no route, and the page does not show the filters. The live
layer is also inert until the bridge serves `GET /script` and the pack is
installed; until then the page says `live · no data`.

**When the markers are stale or missing**, read `mcmap_live_frames_total`
by result and `mcmap_live_polls_total` here, then the bridge's
`mc_console_bridge_script_records_total` and
`mc_console_bridge_script_last_record_timestamp_seconds`, then the server
log for `[Scripting]` lines. `mcmap_live_pack_interval_seconds` above one
second is the pack slowing itself down to protect the server's tick rate.

### Icons and heads

A mob is drawn as its icon and a player as their skin's head, each inside a
ring or border in its category's colour so the filters still read at a
glance. A marker with no picture is the dot or arrow it was before.

**Mob icons** are Mojang's own spawn-egg item textures. None of them is in
this repository or in the image: the service fetches them at runtime from
Mojang's public [bedrock-samples](https://github.com/Mojang/bedrock-samples)
repository, at one pinned revision, and keeps them under `DATA_DIR/icons`
(about 50 KB of files).

- **The pin** is `ICONS_REF`, by default the commit tagged `v1.26.50.4`,
  the stable release nearest the game server's 1.26.5x. It is a commit so
  that what is fetched cannot change unless the setting does. Move it when
  a game update adds a mob: a mob the pin does not know is a dot.
- **Which texture is which mob's** is read from the samples and never
  guessed. `resource_pack/entity/*.json` holds each mob's client definition,
  whose `spawn_egg.texture` (and sometimes `texture_index`) names an entry
  in `resource_pack/textures/item_texture.json`, which names the file under
  `resource_pack/textures/items/`. The names do not follow from the type:
  `evocation_illager` uses `spawn_egg_evoker`, `zombie_pigman` uses
  `spawn_egg_zombified_piglin`, and `villager` is index 14 of a shared list.
  A mob with several definitions uses the one with the highest
  `min_engine_version`, as the game does. At the default pin 92 types have
  an icon. The 39 that do not are not mobs (boats, minecarts, armour
  stands, projectiles) or have no egg.
- **What is asked for**: one directory listing from `api.github.com`, then
  the atlas, about 180 definitions and about 90 textures from
  `raw.githubusercontent.com`, some 700 KB in all and a few seconds. No
  redirect is followed, each file has a size limit and the whole fetch a
  count, byte and time limit, and every texture must decode as a PNG of at
  most 64 pixels a side, which is then encoded again; the downloaded bytes
  are never served.
- **It is fetched once.** A start that finds every file for the pin intact
  on the volume asks the source for nothing. A changed pin, or a missing
  or altered file, fetches the whole set again and removes the old one.

**When the source cannot be reached**, is slow, is rate limited (the
listing is rationed to 60 an hour per address without a token) or answers
with anything unexpected, nothing else is affected. The fetch runs on its
own, the listeners and the snapshot cycle never wait for it, and the page
draws dots. It is tried again after a minute, then at doubling intervals up
to an hour, and logged each time as `mob icons not fetched`.
`mcmap_icons_mob_types` at zero is this state. `ICONS_ENABLED=false` asks
for nothing at all.

**Player heads** come from the game server, by way of the agent. The server
sends every client each online player's skin; the agent crops the 8 by 8
face, lays the hat layer over it, and reports the result with the player's
XUID and gamertag to `PUT /internal/v1/heads`, under the same
`INTERNAL_TOKEN` it logs players in with. A skin is something a player
made, so the agent reads only the classic sizes (64x32, 64x64, 128x128,
256x256) drawn on the standard player model, and this service decodes what
the agent sends within limits (a square PNG of 8 to 32 pixels, at most
8 KB) and encodes it again before serving it. A head that fails is dropped
and counted in `mcmap_icons_player_heads_refused_total`; the player keeps
their arrow. Skins made in the character creator (persona skins) are laid
out for a model of their own and are skipped, so those players keep their
arrow too.

Heads are kept by XUID, in memory, for the players online now: each report
replaces the last, and one not renewed for five minutes is dropped (the
agent repeats it every minute, which is also what restores heads after
this service restarts).

**Matching a marker to a head.** The live record names a player by
gamertag and a per-session id, never by XUID, so the page asks for a head
by gamertag and this service answers from the agent's report, which pairs
each gamertag with an XUID. A gamertag is answered only while exactly one
online player holds it, compared without regard to case. If two do, or two
markers in one frame carry the same gamertag, neither gets a head: a
marker with no head is better than one with somebody else's. A changed
gamertag arrives in the agent's next report, which replaces the old
pairing, and the page makes the marker again under the new name. The
session's own marker is found by XUID (`me` in `/api/icons`), so it stays
highlighted through a change of gamertag.

**Serving.** `/api/icons` lists what there is and is asked again every 30
seconds, answering 304 when nothing changed. Each picture's address
carries its version (`?v=`), and is served `private, max-age=31536000,
immutable` at that version, so a browser fetches each once however many
frames draw it, and a changed head or pin is a new address. The page's
content security policy is unchanged: pictures are same-origin images.

**In the browser** the pictures are decoded once into bitmaps, composed
with their ring into a sprite per picture and colour, and stamped onto the
live layer's one canvas with `drawImage`. Measured with 1,000 mobs and 5
players in view at 1400 by 900 in headless Chromium, repainting the whole
canvas every frame while panning: 16.7 ms frames with none over, before
and after; one whole repaint, flushed, took a median 1.9 ms as dots and
1.4 ms as icons.

## Markers

Four more kinds of mark are drawn as rings, each with a filter the browser
remembers:

| Marker | From | Who sees it |
|---|---|---|
| Waypoints | The server agent, where players save them with `!waypoint` | Only the player they belong to |
| Beds | The world | Every logged-in player |
| Containers | The world | Every logged-in player |
| Named mobs | The world | Every logged-in player |

**From the world.** After the chunk count and the retained copy, and before
the renders, each cycle reads the mirror once more, through hard links
opened read-only as the count's are, and keeps:

- *Beds*: `Bed` block entities. Both blocks of a bed are one, so two of the
  same colour side by side are drawn as a single bed.
- *Containers*: `Chest`, `Barrel` and `ShulkerBox` block entities that hold
  at least one item and are not still waiting on their loot table. On the
  FWB world that is 767 of 16,330: the rest are chests the world generator
  placed and nobody has opened, or opened and emptied. A large chest is one
  marker, which makes those 767 into 575. A container renamed on an anvil shows its name; what is inside is
  not sent.
- *Named mobs*: actors with a name tag, placed by the chunk whose actor list
  names them. An actor no chunk lists is a leftover the game never loads
  and is not drawn. A named mob that is also loaded is drawn twice, once
  here where the snapshot had it and once by the live layer where it is.

Where these are in the database: a chunk's block entities are NBT compounds
one after another under `<x><z>[<dimension>]` + `0x31`, each with its own
`id`, `x`, `y` and `z`; an actor is one compound under `actorprefix` + its
eight-byte storage key, with `identifier`, `CustomName` and `Pos`; and a
chunk's actor list is those storage keys end to end under `digp` + the chunk
key, which is the only place an actor's dimension is written.

The read is one pass over every key, about nine seconds at a whole CPU and
66 MB on the FWB world (2.47 million keys, 153,000 chunks), found 1,493
beds, 575 containers and 5 named mobs there, and leaves the mirror as it
was. It is given up after two minutes, and a read that fails or is given up
is logged and counted and stops nothing else: the page keeps the markers of
the snapshot before. Until the first cycle after a start there are none.

**Limits.** Per dimension, the 5,000 beds, 5,000 containers and 1,000 named
mobs nearest the origin are kept and the rest counted; the filter says how
many are not shown. A name is cut to 64 characters, loses the game's
formatting codes, and is sent as text; the page builds every label from
text and never from markup. One dimension's answer is at most 2 MB, and is
cut further, and counted, if names alone would push it past that. A record
that does not parse, or a block entity that claims a position outside the
chunk holding it, is skipped and counted in the log.

**Waypoints.** `GET /api/waypoints` asks the agent, at `AGENT_URL`, for the
waypoints of the XUID in the session, and for nobody else's: the request
carries nothing that could name another player. This service authenticates
to the agent with `INTERNAL_TOKEN`, the secret the agent already presents
here, so the two share one credential and no new one. The agent is asked
when a browser asks, so a waypoint saved in chat is on the map at the next
refresh; nothing is kept here. At most four requests to the agent are open
at once, each for three seconds at most, and 500 waypoints are passed on.
While the agent cannot be reached the filter stays and draws nothing.

`MARKERS_ENABLED=false` skips the read and the route; without `AGENT_URL`
there is no waypoint route and no waypoint filter.

## Structures

Two layers, drawn so that one cannot be taken for the other.

**Known** structures are the ones this world has generated, read from its
own save. For each chunk the server keeps the boxes in which a structure's
own mobs spawn, as that chunk's record 57: a 32-bit count, then per box six
32-bit block coordinates (minimum x, y, z, then maximum, inclusive) and one
byte for the kind, all little-endian. The kinds are 1 nether fortress,
2 witch hut, 3 ocean monument and 5 pillager outpost; nothing else leaves
such a record, so villages, temples and the rest are not on this layer.
A box is cut at the chunk's edge, so the boxes of a kind that touch are
joined back into one structure (fortress boxes within 32 blocks, since a
fortress is recorded room by room). A fortress only part generated shows as
the parts there are.

**Predicted** structures are worked out from the seed, and are mostly of
use for chunks nobody has generated yet. A site in a generated chunk that
the world recorded nothing at is still drawn, struck through, because that
disagreement is the evidence about whether the seed can be trusted. The
generator cuts the world into regions and gives each one
site, at an offset drawn from a Mersenne Twister seeded with the region,
a 32-bit structure seed and a number per kind. The structure seed is the low
32 bits of the world seed unless `STRUCTURE_SEED` supplies another, which is
what this world needs; see below. Each kind is a
`Predictor` in `internal/structures`, so one can be corrected alone.

Placement rules differ between game versions and are easy to get subtly
wrong, so nothing is predicted on trust. Every survey sets the seed's sites
beside what the world recorded:

- Monuments, outposts and witch huts sit exactly on their sites. Once three
  recorded ones do, and more agree than not, the seed is `verified` and
  predictions are served. With fewer it is `unverified`; if they are
  somewhere else it is `refuted`. Either way nothing is predicted, and the
  page says why.
- Each disagreement is logged once, when it appears, and counted in
  `mcmap_structures_prediction_disagreements`: a recorded structure no site
  explains, or a predicted fortress whose chunk is generated with nothing
  recorded. The second kind is still drawn, struck through.

Only fortresses are predicted. A fortress is built at its site whatever the
biome. A monument, an outpost or a hut is only built where the biome suits,
which nothing here can know for a chunk that does not exist yet: of the
monument sites in generated chunks of the FWB world, one in twelve holds a
monument. Their sites are used to check the seed and not shown.

Checked against the FWB world on 2026-10-05 (game 1.26.52.3): all 11
monuments, 7 outposts and the 1 witch hut are on their sites, and every
recorded fortress has a fortress site within reach. One fortress site lies
in generated chunks with no fortress recorded.

**The seed.** `RandomSeed` and the world spawn are read from `level.dat` in
the mirror, which is little-endian NBT behind an eight-byte header and is
only ever opened for reading. The seed is never served or logged: with it,
a seed map shows everything the world has yet to generate. The spawn is
served with the overworld's structures.

The low 32 bits of the FWB world's `RandomSeed` do **not** place its
structures; another 32-bit value does, exactly. Why is not known.
`STRUCTURE_SEED` supplies such a value, and is checked against the world in
the same way before anything is predicted from it. Treat it as the seed.

The survey runs last in each cycle, on hard links like the chunk count, and
its failure costs nothing else. On the FWB world (2.47 million records,
1,274 boxes) it takes 9 seconds and peaks at 45 MB. It keeps at most 200,000
boxes and 2,000 structures of each layer per dimension, and says how many
it left out.

To check the rules again after a game update, against a copy of a world:

```sh
MCMAP_REAL_WORLD=/path/to/FWB go test -run RealWorld -v ./minecraft/mcmap/internal/structures/
```

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
  and the map API, tiles, markers and live stream behind the session.
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
| `DATA_DIR` | no | `/data` | Mirror, retained world copies, tiles, the installed renderer and the fetched mob icons. The retained copies are the one thing here that cannot be rebuilt, so keep it on a volume |
| `REFRESH_INTERVAL` | no | `15m` | Time between cycles, as a Go duration. At least `1m`: each cycle pauses world saving for a moment |
| `QUIET_UTC` | no | empty | Daily UTC windows with no snapshot, `HH:MM-HH:MM,HH:MM-HH:MM`. A window may cross midnight |
| `RENDER_CHUNK_PROCESSORS` | no | `1` | Chunks rendered at once. More is faster and uses more CPU and memory |
| `NETHER_TOP_Y` | no | `100` | Highest nether layer drawn |
| `UNMINED_URL` | no | the dev Linux x64 build | Where the renderer is downloaded from; must be https |
| `UNMINED_SHA256` | no | the build this release was verified against | Digest the download must match |
| `LIVE_ENABLED` | no | `true` | `false` turns the live layer off: nothing asks the bridge for records and `/api/live` is not served |
| `LIVE_POLL_WAIT` | no | `2s` | How long the bridge may hold one request for records open; `100ms` to `25s`, the bridge's own limit |
| `LIVE_TTL` | no | `10s` | How old a position may be and still be drawn; `2s` to `10m` |
| `LIVE_MAX_ENTITIES` | no | `1000` | Most players, and most mobs, sent to a browser per dimension; 1 to 10000. Lowering it eases the browser, not the game server: the pack's own cap is set where the pack is installed |
| `LIVE_KEEPALIVE` | no | `15s` | Longest a live stream stays silent; `1s` to `20s`. The load balancer cuts a connection idle for 30 s |
| `MARKERS_ENABLED` | no | `true` | `false` stops beds, containers and named mobs being read from each snapshot, and `/api/markers` is not served |
| `AGENT_URL` | no | empty | The server agent's HTTP address, e.g. `http://<release>-server-agent:8080`, asked for the logged-in player's waypoints with `INTERNAL_TOKEN`. Empty leaves waypoints off the map. Needs the login |
| `ICONS_ENABLED` | no | `true` | `false` draws every live marker as a dot or arrow: no mob icon is fetched, no head is accepted, and `/api/icons` is not served |
| `ICONS_REF` | no | the commit tagged `v1.26.50.4` | Tag or commit of Mojang's `bedrock-samples` the mob icons are fetched at. A commit cannot move; a tag can |
| `STRUCTURES_ENABLED` | no | `true` | `false` reads no structures and does not serve `/api/structures` |
| `STRUCTURE_SEED` | no | the low 32 bits of the seed in `level.dat` | The 32 bits structure placement is seeded with, 0 to 4294967295, for a world whose `level.dat` does not hold them. As secret as the seed |

## Endpoints

| Route | Purpose |
|---|---|
| `GET /` | The map page; public, and holds nothing about the world |
| `GET /api/config` | Whether there is a login; public |
| `POST /auth/start`, `GET /auth/status`, `POST /auth/logout` | The login flow above |
| `GET /api/me` | The logged-in player's gamertag |
| `GET /api/map` | Session required. World name, refresh interval, each dimension's extent and last render time, `live` (whether there is a live stream to open), and `problem` (`snapshot` or `render`) while the last cycle failed |
| `GET /tiles/{dimension}/{zoom}/{x}/{y}.webp` | Session required. One 256-pixel tile. Zoom 0 is one block per pixel; each step below halves the scale. 404 where the world has no chunks |
| `GET /api/live?dimension=<id>` | Session required. Server-sent events: one frame at once and one per sample, each the whole of that dimension as `at`, `serverNow`, `players`, `mobs`, `more`, `stale` and `ttlSeconds`. 400 for an unknown dimension, 503 when too many streams are open. Not served with `LIVE_ENABLED=false` |
| `GET /api/markers?dimension=<id>` | Session required. That dimension's `beds`, `containers` and `mobs`, each `x`, `y`, `z` with `k` (a container's kind or a mob's type) and `n` (a name, where there is one); `at`, the snapshot they were read from; and `more`, how many of each were left out at the limit. Carries an `ETag` and answers 304 to a matching `If-None-Match`. 400 for an unknown dimension. Not served with `MARKERS_ENABLED=false` |
| `GET /api/waypoints` | Session required. The logged-in player's own `waypoints`, each `name`, `x`, `y`, `z` and `dimension`, across all dimensions, and `more`. 502 while the agent cannot be read, 503 when too many reads are open. Not served without `AGENT_URL` |
| `GET /api/icons` | Session required. Which live markers have a picture: `mobs` with a `version` and the `types` that have an icon, `heads` giving each head's version by gamertag in lower case, and `me`, the gamertag the session's player is online under. Carries an `ETag` and answers 304 to a matching `If-None-Match`. Not served with `ICONS_ENABLED=false` |
| `GET /api/icons/mob/{type}?v=<version>` | Session required. That mob type's icon as a PNG, kept for good by the browser when `v` is the current version. 404 for a type with no icon |
| `GET /api/icons/head?name=<gamertag>&v=<version>` | Session required. The head of the one online player holding that gamertag, as a PNG. 404 if nobody does, two players do, or their skin gave no head |
| `GET /api/structures?dimension=<id>` | Session required. `recorded` (each a `kind` and its box, `minX` to `maxZ`, with `areas`), `predicted` (each a `kind`, `x`, `z`, and `generated` where the chunk exists and the world recorded none), `recordedMore` and `predictedMore` for what the bounds left out, `prediction` (`verified`, `unverified`, `refuted` or `unknown`), `surveyed`, `at`, and with the overworld `spawn`. 400 for an unknown dimension. Not served with `STRUCTURES_ENABLED=false` |
| `GET /healthz` | Liveness, on both listeners |

On `INTERNAL_ADDR` only:

| Route | Purpose |
|---|---|
| `GET /metrics` | Prometheus |
| `POST /internal/v1/claims` | Bearer `INTERNAL_TOKEN`. `{"code","xuid","gamertag"}`: this player typed this code. 204, or 404 for a code that is unknown, expired or used |
| `POST /internal/v1/revocations` | Bearer `INTERNAL_TOKEN`. `{"xuid"}`: end every session this player holds. 204 |
| `GET /internal/v1/world` | Bearer `INTERNAL_TOKEN`. The last chunk count: `checked`, `checkedAt`, and by dimension `chunks`, `missing` and `lost`, with `missingTotal`, `lostTotal`, and up to 20 lost chunks as block coordinates in `lostSample`. `{"checked":false}` before the first count. Also `generations`, with `current`, `previous` and `damaged`, each naming its directory and carrying `takenAt`, `files` and `bytes` — what a restore needs to choose between them |
| `POST /internal/v1/world/acknowledge` | Bearer `INTERNAL_TOKEN`. `{"checkedAt"}` from the GET: accept the world as that count found it. 204; 409 before the first count or if a newer count has replaced that one |
| `PUT /internal/v1/heads` | Bearer `INTERNAL_TOKEN`. `{"players":[{"xuid","gamertag","head"}]}`: everyone online now, `head` a PNG in base64 or absent. Replaces the last report whole. 200 with `players` and how many heads were `refused`; 400 for more than 256 players, a body over 1 MB, or an entry that is not a player. Not served with `ICONS_ENABLED=false` |

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
| `mcmap_live_last_frame_timestamp_seconds` | When the bridge received the newest record accepted, heartbeats included. Old means the pipeline is broken, not that the server is empty |
| `mcmap_live_frames_total{result}` | Records by outcome: `applied` (completed a list), `buffered`, `heartbeat`, `incomplete` (a list abandoned with parts missing), `dropped` (a repeat), `unparseable`; `nul_stripped` is counted in addition |
| `mcmap_live_polls_total{result}` | Requests to the bridge: `ok`, `empty`, `gap` (records were lost, or the bridge restarted), `busy` (429), `failed` |
| `mcmap_live_entities{dimension,kind}` | Entities in the last whole list |
| `mcmap_live_frame_interval_seconds` | Histogram of the time between heartbeats |
| `mcmap_live_log_lag_seconds`, `mcmap_live_ingest_lag_seconds`, `mcmap_live_fanout_seconds` | Histograms of each hop: pack to bridge (only for records carrying the pack's own time), bridge to this service, this service to a browser |
| `mcmap_live_pack_scan_seconds`, `mcmap_live_pack_interval_seconds` | What a sample costs the game server and how often the pack samples, by its own report |
| `mcmap_live_subscribers`, `mcmap_live_streams_total{reason}` | Open streams, and ended ones by why: `client`, `limit`, `write`, `shutdown` |
| `mcmap_live_fanout_dropped_total` | Frames replaced before a slow browser read them |
| `mcmap_markers{dimension,kind}` | Beds, containers and named mobs (`bed`, `container`, `mob`) read at the last scan and served |
| `mcmap_markers_left_out{dimension,kind}` | Markers the last scan found beyond the limit for their kind |
| `mcmap_markers_last_success_timestamp_seconds`, `mcmap_markers_duration_seconds`, `mcmap_markers_failures_total` | Whether the marker scan is running, and what it costs |
| `mcmap_icons_mob_types` | Mob types that have an icon. Zero means every mob is being drawn as a dot |
| `mcmap_icons_fetches_total{result}` | Attempts to fetch the mob icons, `ok` or `failed`. None at all means they were read from the volume |
| `mcmap_icons_player_heads`, `mcmap_icons_player_heads_refused_total` | Online players with a head, and heads the agent sent that were refused |
| `mcmap_structures_recorded{dimension,kind}`, `mcmap_structures_predicted{dimension,kind}` | Structures on each layer at the last survey |
| `mcmap_structures_seed_verified` | 1 while recorded structures are where the seed puts them. 0 means nothing is being predicted |
| `mcmap_structures_prediction_disagreements` | Places where the seed and the world's records disagree |
| `mcmap_structures_areas_skipped{reason}` | Recorded boxes left out: `malformed`, `unknown` (a kind this version does not know), `limit` |
| `mcmap_structures_survey_last_success_timestamp_seconds`, `mcmap_structures_survey_duration_seconds`, `mcmap_structures_survey_failures_total` | Whether the survey is running |

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
