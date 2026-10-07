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
the bridge, no route, and the page shows neither the layer's rows nor its
pause and interval controls. The live
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
(about 80 KB of files, with the marker pictures and names
[fetched with them](#names-and-marker-pictures)).

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
  the atlas, about 180 definitions, about 90 spawn-egg textures, 42 marker
  textures and the language file from `raw.githubusercontent.com`: 317
  requests and 1.6 MB in all, in under ten seconds. No
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

**In the browser** one script, `icons.js`, asks `/api/icons` and keeps the
pictures for every layer: each is decoded once into a bitmap, composed
with its ring into a sprite per picture and colour, and stamped onto the
live layer's one canvas with `drawImage`. Measured with 1,000 mobs and 5
players in view at 1400 by 900 in headless Chromium, repainting the whole
canvas every frame while panning: 16.7 ms frames with none over, before
and after; one whole repaint, flushed, took a median 1.9 ms as dots and
1.4 ms as icons.

### Names and marker pictures

The same fetch, at the same pin, reads two more things, so that the page
can show everything by its proper name and its own picture.

**On the page.** `names.js` fetches `/api/names` once, and again when the
`names.version` in `/api/icons` is no longer the one it holds, and every
name the page shows is looked up there: a live mob's tooltip and card, a
marker's tooltip, the named mobs' list, a structure's row and tooltip, a
search hit. Until the table arrives, and for an id it does not list, the
page tidies the id by the same rule the server does, so `villager_v2` is
Villager from the first frame and `evocation_illager` reads Evocation
Illager for the moment before it becomes Evoker. When the table arrives
or changes, what is on screen is retitled where it stands, an open
tooltip and a listed search included. Nothing is shown as an identifier
or left empty: a mob with no type is Mob, a container of no kind
Container, a waypoint with no name Waypoint.

`icons.js` likewise holds every picture. It asks for one only if its key
is listed, draws it unsmoothed at 16 pixels, and tells the layers when one
has decoded, so a picture the server gets later appears within the 30
seconds between askings and with no reload. A key not listed, a picture
that fails to load (asked for again after five minutes) and a browser
that cannot decode one all leave the marker as it was drawn before there
were pictures: a ring, a dot or a letter.

**Names** are from the game's own English language file,
`resource_pack/texts/en_US.lang`. Its keys follow no single rule, and each
form here was read from the file, not assumed:

| What | Key | Example |
|---|---|---|
| A mob or other entity type | `entity.<type>.name`, the type exactly as the world reports it | `entity.villager_v2.name` is Villager, `entity.evocation_illager.name` is Evoker |
| A container | `tile.chest.name`, `tile.trapped_chest.name`, `tile.barrel.name`, `tile.shulkerBox.name` | |
| A bed of a colour | `item.bed.<colour>.name`, in camel case, light grey spelt `silver` | `item.bed.lightBlue.name`, `item.bed.silver.name` |
| A shulker box of a colour | `tile.shulkerBox<Colour>.name`, likewise | `tile.shulkerBoxSilver.name` is Light Gray Shulker Box |
| A structure | `feature.<kind>`, with no suffix; an outpost is `feature.pillager_outpost` | `feature.monument` is Ocean Monument |

The file is text from outside that ends up in a browser. It may be at most
4 MB and 100,000 lines (it is 0.8 MB and 13,340); only lines with one of
those keys are kept, 193 of them, and at most 4,000; and a name is kept
only if it is at most 64 characters of printable text, with no control
character, no invisible or text-reordering character, no private-use glyph
and no colour code. A name that fails is dropped, not repaired.

**For the page:** every name is plain text and must be written as text
(`textContent`, never markup). The filter above lets `<`, `>`, `&` and
quotes through by design, since a name may hold them.

Anything the file does not list is named by its id tidied into words:
namespace and version suffix dropped, underscores to spaces, each word
capitalised, so `zombie_villager_v2` would be Zombie Villager. No raw id is
served as a name. At the default pin the gaps are:

- **Witch hut**: the file has no `feature.` entry for it, so it is the
  tidied Witch Hut.
- **Biomes**: the file names no biome at all. `icons.BiomeName(id)` is
  therefore always the tidied id, and an id the game has kept from an older
  version reads as that older word (`hell`, not Nether Wastes).
- Four entity types that are block renderers and never appear as mobs
  (`bed`, `decorated_pot`, `skull`, `trial_spawner`).

The table also names every mob type on the live layer and among the
markers at the moment it is asked for, so that with the samples out of
reach, when nothing else says which types exist, each one in view still
has a tidied name.

**Marker pictures** are 42 small PNGs, by key:

| Key | Source under `resource_pack/textures/` | Why that one |
|---|---|---|
| `bed/<colour>` | `items/bed_<colour>`, taken from the atlas's `bed` list, whose order is the colour number the world stores | The bed item, one per colour |
| `container/chest`, `container/trapped_chest` | `blocks/chest_front`, `blocks/trapped_chest_front` | A chest has no item texture: the game draws the block. Its front is the face with the latch |
| `container/barrel` | `blocks/barrel_side` | Likewise; the side is the face with the hoops |
| `shulker/<colour>`, `shulker/undyed` | `entity/shulker/shulker_<colour>`, composed | See below |
| `marker/waypoint` | `items/compass_item` | A waypoint is a place to find your way back to |
| `structure/fortress` | `items/netherbrick` | A fortress is built of nothing else |
| `structure/monument` | `items/prismarine_shard` | Dropped only by the guardians of a monument, and unmistakably of the sea |
| `structure/outpost` | `items/crossbow_standby` | The pillagers' weapon; their banner has no flat texture |
| `structure/witch_hut` | `items/cauldron` | Every hut has one |
| `structure/village` | `items/villagebell` | Every village's meeting point has one |

A structure has no item of its own, so each is a vanilla item that could
stand for nothing else on this map. Items are used rather than blocks
because an item has a shape against a clear background, where a block face
is a filled square like the chest's.

A shulker box has neither an item texture nor a usable block face (its
only one is the plain top). Its picture is made from the texture its model
is wrapped in, which is the model's faces laid flat and not a picture of
anything: the front of the base (16 by 8 of the 64-unit sheet, at 16, 44)
is drawn at the bottom of a 16 by 16 icon and the front of the lid (16 by
12, at 16, 16) over it from the top, the lid coming down over the base as
it does in the game. A sheet must be square, a whole multiple of 64 and at
most 256 a side.

Light grey is `light_gray` in every key here and `silver` in the samples'
file names and language keys.

Each picture is asked for by a path known ahead, so the fetch still makes
one listing request. Each is bounded, decoded and encoded again exactly as
a mob icon is.

None of this can cost the mob icons. Where a picture or the language file
cannot be had, that one thing is left out, everything else is served, and
it is asked for again by itself, with no listing request:

- **The source answered that it does not hold it** (no such file at the
  pin, or one that is too large or not a small PNG): asked again at each
  start and once a day. It is logged under `missing` when found and not
  again for every asking after.
- **The source could not be asked** (no route, a 429 or a 5xx): asked again
  after a minute, then at doubling intervals up to an hour, and logged
  once.

A volume filled by a version that fetched only mob icons keeps serving
them: they are taken as they are, never fetched again, and the pictures
and names are added to them as above. Until those arrive there are no
pictures and every name is a tidied id.

## Markers

Four more kinds of mark, each with a row in the layer panel. Each is drawn
as its own picture on a square plate edged in the row's colour, where the
live layer's markers are round, and as the ring it used to be while there
is no picture for it:

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
  same colour side by side are drawn as a single bed. The record's `color`
  is the dye number, 0 white to 15 black, and is served as the colour's
  name.
- *Containers*: `Chest`, `Barrel` and `ShulkerBox` block entities that hold
  at least one item and are not still waiting on their loot table. On the
  FWB world that is 767 of 16,330: the rest are chests the world generator
  placed and nobody has opened, or opened and emptied. A large chest is one
  marker, which makes those 767 into 575. A container renamed on an anvil shows its name; what is inside is
  not sent. A shulker box's record holds no colour, and a trapped chest's
  is a chest's, so for each container kept the block's own name is looked
  up in the chunk slice holding it (`<chunk key>` + `0x2f` + the slice's
  height index): `red_shulker_box`, `undyed_shulker_box`, `trapped_chest`.
- *Named mobs*: actors with a name tag, placed by the chunk whose actor list
  names them. An actor no chunk lists is a leftover the game never loads
  and is not drawn. The record's `IsBaby` is served, so the page can tell
  a named lamb from a named sheep, and so is its `UniqueID`, written out as
  the game's scripts report an entity's id, which is the id the live layer
  gives the same mob while it is loaded.

Where these are in the database: a chunk's block entities are NBT compounds
one after another under `<x><z>[<dimension>]` + `0x31`, each with its own
`id`, `x`, `y` and `z`; an actor is one compound under `actorprefix` + its
eight-byte storage key, with `identifier`, `CustomName` and `Pos`; and a
chunk's actor list is those storage keys end to end under `digp` + the chunk
key, which is the only place an actor's dimension is written.

The read is one pass over every key, about nine seconds at a whole CPU and
66 MB on the FWB world (2.47 million keys, 153,000 chunks), found 1,493
beds, 575 containers and 5 named mobs there (beds in 12 colours, 21
shulker boxes of which 10 undyed, no trapped chest with anything in it,
and two of the named mobs babies), and leaves the mirror as it was. It is given up after two minutes, and a read that fails or is given up
is logged and counted and stops nothing else: the page keeps the markers of
the snapshot before. Until the first cycle after a start there are none.

**On the page.** A bed is its colour's picture and says its colour (Red
Bed); a container is a chest, a trapped chest, a barrel or a shulker box
of its colour, and says which, after its name if it was given one; a
waypoint is the compass. A bed whose colour the world does not say is
drawn red and called Bed, and a shulker box likewise undyed. From further
out than four blocks to the pixel beds and containers go back to their
rings, since that many 20-pixel pictures at that scale are a heap in which
none can be made out.

A named mob is its type's icon, the one the live layer draws, in the row's
colour, with its name on a label above it at every zoom; a baby is drawn
smaller and called one. They are also listed under the Named mobs row,
by name, with type and `baby`. The label shows the first 24 characters of a
long name and the tooltip, the list and the card all of it.

A named mob is one marker, whichever layer has it. While the mob is loaded
the live layer draws it where it is, under the same label, and the mark the
snapshot left steps aside; the two are known to be one animal by the id
each carries (`i`), never by name or position. With the live layer's row
for it switched off the snapshot's mark comes back, and with Named mobs off
neither has a label. A click or a tap on the marker, on its label, or on
its entry in the list opens the card described under Inspecting and
following: on a loaded mob it tracks it live and Follow works; on one that
is not loaded it shows where the snapshot left it, says that this is its
last saved position and how old, and offers Go to. The list entry also
takes the map there, and the entry the card is about is outlined.

The markers are stamped onto the live layer's canvas from sprites made
once per picture, so that they and the live markers can both be hovered
and none is an element or a request of its own. Measured in headless
Chromium at 1300 by 800 with 1,500 beds, 600 containers and 905 live mobs
all in view, dragging the map: 244 frames, median 16.7 ms, longest
16.8 ms, no long task; one whole repaint of the canvas, flushed, took a
median 4.0 ms.

**Limits.** Per dimension, the 5,000 beds, 5,000 containers and 1,000 named
mobs nearest the origin are kept and the rest counted; the layer's row says how
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
While the agent cannot be reached the row stays and draws nothing.

`MARKERS_ENABLED=false` skips the read and the route; without `AGENT_URL`
there is no waypoint route and no waypoint row.

## Structures

Three layers, drawn so that none can be taken for another: what the world
has recorded, what the seed predicts, and where the seed says a structure
is only possible.

Each kind is drawn as [its picture](#names-and-marker-pictures) and called
by the game's name for it, in its row (Ocean Monuments) and its tooltip. A
known structure's picture sits on a solid square framed in the kind's
colour, over the box the world recorded; a predicted one's in a hollow,
dashed circle; a possible one's in a dotted circle, faint until the pointer
is on it. Until a picture is there its place is taken by the kind's
letter, as F, M, O, H or V. A kind this page has no row for is still
named, by its id made into words.

The panel has a row for each layer, Known, Predicted and Possible, and one
for each kind that filters all three. A kind's row counts everything of the
kind and says under it how that is made up (11 known, 2 predicted, 498
possible), or why the kind is not predicted. Possible starts as the
viewer's Predicted row is set. Every tooltip says which of the three a mark
is, and why.

**Known** structures are the ones this world has generated, read from its
own save. For each chunk the server keeps the boxes in which a structure's
own mobs spawn, as that chunk's record 57: a 32-bit count, then per box six
32-bit block coordinates (minimum x, y, z, then maximum, inclusive) and one
byte for the kind, all little-endian. The kinds are 1 nether fortress,
2 witch hut, 3 ocean monument and 5 pillager outpost; nothing else leaves
such a record. Villages are kept another way and are read too, as below;
temples and the rest are recorded nowhere and are not on this layer.
A box is cut at the chunk's edge, so the boxes of a kind that touch are
joined back into one structure (fortress boxes within 32 blocks, since a
fortress is recorded room by room). A fortress only part generated shows as
the parts there are.

**Villages** are a fifth known kind, `village`, read from the records the
game keeps for each village it runs. They are outside any chunk, under
`VILLAGE_<dimension>_<id>_` and one of five endings, each an unnamed NBT
compound:

| Record | Holds | Read |
|---|---|---|
| `INFO` | The village's box, `X0` `Y0` `Z0` to `X1` `Y1` `Z1`, and `Initialized` | Yes |
| `DWELLERS` | `Dwellers`: four lists of `actors`; the first is villagers, the second iron golems, the fourth cats | Counts only |
| `POI` | `POI`: per villager, the `instances` it has claimed, each a `Type` (0 bed, 1 bell, 2 job site) at `X` `Y` `Z` | Counts only |
| `PLAYERS` | Each player's standing with the village | Never |
| `RAID` | A raid in progress | Never |

`<dimension>` is `Overworld`, `Nether` or `TheEnd`. Only `Overworld` has
been seen in a real world; the other two are the names the game gives its
own per-dimension records. A village under any other name, or under none
as older versions of the game wrote it, is passed over, and counted by its
`INFO` record. A village
is drawn with the box the game recorded and carries how many villagers,
golems and cats it lists and how many beds, bells and job sites its
villagers have claimed, each block counted once. Nothing a player typed and
nothing that names one is read.

What counts as a village worth drawing:

- One the game has counted (`Initialized` 1) with at least one villager.
- One the game has made a record for and not yet run (`Initialized` 0).
  Its box is a first guess, 64 blocks square, and its counts are sent as zero,
  so it goes out as `counted: false`: not known, rather than none. Every
  such record in the FWB world has villagers and beds inside its box.
- Not one the game has counted and found no villager in. That is a record
  still and no longer somewhere to go looking for a villager; it is
  counted under `empty` and not drawn.

A village's records that do not parse, hold no box, or hold one inside out,
past the edge of the world or more than 1,024 blocks across, leave that
village out and are counted.

The layout above is what the game's level format documentation gives for
the keys and tag names; what the lists and types mean was worked out from
the FWB world on 2026-10-05 (game 1.26.52.3) by setting the records beside
the actors and block entities in the same save. It has 70 villages, 55
counted and 15 not, none empty or malformed. Of the 549 actors in the first
list of the 55, 542 are villagers (the other seven have no actor record);
all 42 found from the second are iron golems and all 159 from the fourth
are cats. The third is empty in every one and is not read. Of 481 claimed
blocks of type 0, 479 are bed block entities; all 35 of type 1 are bells;
type 2 carries a profession's name. 512 of the 542 villagers stand inside
their own village's box, and none is more than 15 blocks from where the
village last saw it. Each of the 15 not yet counted has villagers and beds
inside its box, 57 villagers between them.

This layer is not every village. The same world has 97 villagers that
stand in no village's box, in generated villages the game has made no
record for, and those are not known here.

**Predicted** structures are worked out from the seed. The generator cuts
the world into regions, a grid per kind, and gives each region one site at
an offset drawn from a Mersenne Twister seeded with the region, a 32-bit
structure seed and a number per kind. The structure seed is the low 32 bits
of the world seed unless `STRUCTURE_SEED` supplies another, which is what
this world needs; see below. Each kind is a `Predictor` in
`internal/structures`, so one can be corrected alone.

| Kind | Region, chunks | Offset below | Salt | Draws per axis | Built in |
|---|---|---|---|---|---|
| Nether fortress | 30 | 26 | 30084232 | 1, and a third draw picks fortress (2 in 6) or bastion | Any biome |
| Ocean monument | 32 | 27 | 10387313 | 2, averaged | Deep oceans |
| Pillager outpost | 80 | 56 | 165745296 | 2, averaged | Plains, sunflower plains, desert, savanna, taiga, snowy plains, meadow, grove, snowy slopes, cherry grove and the three peaks |
| Village | 34 | 26 | 10387312 | 2, averaged | Plains, sunflower plains, desert, savanna, taiga, snowy plains, meadow |
| Witch hut | 32 | 24 | 14357617 | 1 | Swamp |

A fortress is built at its site whatever the biome. Every other kind is
only built where the biome suits, so a site is one of three things:

- **Possible** (`candidate`): the site's chunk is not generated. Nothing
  knows what biome it will be, so this is where the generator will try and
  no more. Of the monument sites in generated chunks of the FWB world, one
  in twelve holds a monument. Only sites within about 64 chunks of a
  generated one are kept: the country a player could walk into next.
- **Predicted, and the world disagrees** (`generated`): the chunk is
  finished, its biome suits the kind, and nothing is recorded there. It is
  drawn struck through, logged once and counted in
  `mcmap_structures_prediction_disagreements{kind}`. A village is the
  exception, drawn plainly and not counted: the game has no record of a
  village until a player has been near it, so such a site may well hold one.
- **Dropped**: the chunk's biome is known and the kind is not built in it.

The biome is the one [read from the world](#biomes) at the middle of the
site's chunk; an outpost's own corner of the chunk has been seen in the
biome next door. With `BIOMES_ENABLED=false`, or for a chunk whose biomes
were not read, there is no biome to ask: a site in an unfinished chunk is
still possible, and one in a finished chunk with nothing recorded is not
shown, since eleven in twelve of those are simply in the wrong biome.

**Nothing is predicted on trust.** Placement rules differ between game
versions and are easy to get subtly wrong, so every survey sets each kind's
sites beside what the world recorded, twice over:

- *The seed.* Monuments, outposts and witch huts sit exactly on their
  sites. Once three recorded ones do, and more agree than not, the seed is
  `verified`. With fewer it is `unverified`; if they are somewhere else it
  is `refuted`. Either way no kind is predicted, and the page says why.
- *Each kind.* Under a verified seed a kind is predicted only while its own
  rule holds against the world's own structures of that kind: at least
  three on a site, and more on one than not. A kind with fewer recorded is
  `unverified` and one the world contradicts is `refuted`; neither is
  predicted, the others are unaffected, and the kind's row on the page says
  which. `/api/structures` carries each as `kinds`, and
  `mcmap_structures_kind_verified{kind}` is 1 or 0. A world that has
  recorded two fortresses is therefore shown no predicted fortress until it
  records a third.
- Villages are held to a different share, one recorded village in five on
  a site, because the game's village records are not a record of what was
  generated: a bed and a villager anywhere make a village, and its box
  moves with the beds its villagers claim. A village counts as on a site
  when the middle of the site's chunk is within 16 blocks of its box. A
  village with no site, and a site with no village, are neither logged nor
  counted as disagreements.
- Each disagreement of the other kinds is logged once, when it appears: a
  recorded structure no site explains, or a site in a finished chunk that
  suits the kind with nothing recorded.

**How each rule fared** against the FWB world on 2026-10-06 (game
1.26.52.3, the snapshot of 2026-10-05), with the biomes read. "On a site"
is the share of the world's recorded structures the rule explains;
"sites built on" is the share of sites in finished chunks of a suitable
biome that have a recorded structure.

| Kind | Recorded on a site | Sites built on | Outcome |
|---|---|---|---|
| Nether fortress | 11 of 11 | 5 of 6 | Predicted |
| Ocean monument | 11 of 11, exactly | 11 of 13 | Predicted |
| Pillager outpost | 7 of 7, exactly | 7 of 10 | Predicted |
| Village | 31 of 70 | 27 of 37 | Predicted |
| Witch hut | 1 of 1, exactly | 1 of 1 | Not predicted: one is not three |

What the numbers leave open, so that nobody has to find it out again:

- *Monuments.* Both empty sites are at the edge of the generated world,
  with half or more of the country round them not generated. The game also
  wants water all round a monument, which cannot be asked there, so the
  rule here stops at a deep ocean under the site.
- *Outposts and villages do not share a grid* in this version. None of the
  7 outposts is within 16 blocks of a village site, and all 7 are exactly
  on the outposts' own. Whether a village near by keeps an outpost from
  being built could not be settled: of the 3 empty outpost sites, 2 have a
  village site within 4 chunks, and no built outpost has one nearer than 7,
  which is two cases and not a rule. No such exclusion is applied.
- *Villages.* A wrong seed puts a site by 6.5 of the 70 on average, and
  the other grids tried do no better than that, at 2 to 10: regions of 27,
  32 and 40 chunks as well as 34, 10 chunks kept clear between regions as
  well as 8, and one draw per axis instead of two, in every combination.
  Of the 39 villages on no site, 26 have been counted
  by the game and 12 have a bell; they were not told apart from villages
  players founded. The 10 suitable sites with no village recorded have, on
  average, half the chunks round them not finished. Snowy taiga is on the
  game's list of village biomes and left off this one: its three finished
  sites hold no village, where three in four do elsewhere.
- *Witch huts.* The rule puts the world's one hut in its site's chunk, at
  odds of about one in six hundred for a wrong rule, and the one swamp site
  in finished chunks is that hut. That is the same single piece of evidence
  the seed check refuses to act on, so huts stay unpredicted on this world
  and start being predicted, with no change here, once it has recorded
  three. The same sites are the game's for desert and jungle temples and
  igloos, which leave no record to check against and are not predicted.

**Bounds.** Sites are looked for in the box round a dimension's chunks and
64 chunks more, within 49,000 blocks of the middle of them. At most 20,000
sites of a kind are set beside the world, and 500 predictions of a kind
are kept for a dimension, those the world can already be asked about first
and then the nearest the middle; 2,000 for the dimension between them.
Whatever is left out is counted in `predictedMore`. On the FWB world the
overworld is sent 1,121 sites, 15 of them predicted and the rest possible:
500 each of monuments and villages, which is the bound, with 421 left out,
and 121 outposts. The nether is sent 44 fortresses.

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
1,274 boxes) it takes 9 seconds and peaks at 45 MB. Predicting four more
kinds added nothing that could be measured to that: which chunks are
finished is taken from the pass the survey already makes, at eight bytes a
chunk, and a site costs a few hundred multiplications. It keeps at most 200,000
boxes and 2,000 structures of each layer per dimension, and says how many
it left out.

The villages cost that survey nothing to speak of. Their records share a
prefix, so they are read by seeking to it in the view the survey already
has open, not by another pass: 281 records, 1.2 milliseconds on the FWB
world. The read has ten seconds of its own. If it runs out of them or
fails, the villages of the survey before stand, it is logged and counted in
`mcmap_structures_village_read_failures_total`, and the rest of the survey
is as fresh as it would have been; while that lasts,
`mcmap_structures_villages_last_success_timestamp_seconds` falls behind the
survey's. It reads at most 4,096 villages and counts the rest, no record
over 1 MB or nested more than twelve deep, and holds every count to 10,000.
A world with more than 65,536 keys under the prefix, sixteen for each
village it would read where a village has five, is not read at all and is
a failure like any other: stopping part way would show whichever villages
sorted first as all there are. Villages share the 2,000 known structures a
dimension is sent, after the other kinds, the most lived-in first.

To check the rules again after a game update, against a copy of a world:

```sh
MCMAP_REAL_WORLD=/path/to/FWB go test -run RealWorld -v ./minecraft/mcmap/internal/structures/
```

It reads the biomes as the service does and prints, for each kind, the two
shares in the table above and how many sites it would offer. With
`MCMAP_STRUCTURE_SEED` set it uses that seed. It prints neither the seed
nor where anything is predicted, since a few sites and the rule that made
them are the seed; the recorded structures it does print are the world's
own.

## Biomes

The overlay shows the biomes the world has actually stored, chunk by
chunk. Nothing is worked out from the seed, so there is nothing to draw
where no chunk has been generated, and nothing that can be wrong where one
has.

**Where they are in the database.** A chunk's record 43 (`Data3D`, key
`<x><z>[<dimension>]` + `0x2b`) is its heightmap followed by its biomes.

- The heightmap is 256 little-endian 16-bit values, one per column at index
  `z*16+x`: the height of the first air above the column's highest block,
  counted from the bottom of the dimension. Over an ocean it is 127: the
  water's surface is y 62 and the overworld starts at y -64.
- The biomes are one palettised storage per 16 blocks of height, bottom
  first: 24 in the overworld, 8 in the nether, 16 in the end. Each starts
  with a byte whose top seven bits are the bits per entry. `0` is one biome
  for all 4,096 blocks, and a 32-bit id follows. `127` is "the same as the
  storage below", and nothing follows. Anything else is 4,096 indices
  packed low bits first into 32-bit words, as many to a word as fit whole,
  then a 32-bit palette length and that many 32-bit ids. An entry's index
  is `x<<8 | z<<4 | y`.

This was worked out from the Dragonfly server's reader of the same format
and then checked against the FWB world: all 144,163 records parse to their
last byte with exactly 24, 8 or 16 storages; neighbouring chunks' edges
agree in this index order and not the transposed one (16,862 mismatched
edge columns in 1.37 million against 251,230; heights differ by 0.9 a
column against 5.0); and the deep dark is found only in the bottom nine
storages.

**What a chunk is reduced to.** One biome per block column: the biome of
the column's highest block, which is what a player standing there sees and
what a map looking down should show. A fixed height would not do: 53% of
the overworld's columns change biome on the way down, to a cave biome or
the deep dark, and the biome at y 64 differs from the surface's in 9% of
them. It is kept per column and not per 4 by 4 cell because Bedrock's
biome edges run block by block, through 8% of such cells; a recorded witch
hut on the FWB world has the swamp's edge running under it. So the cave
biomes appear only where they come to the surface, and searching for one
finds those places and not the caves below.

A chunk that is one biome throughout, as 72% are, is one byte; any other
is 256. The whole FWB world is 11 MB on the volume and about 30 MB in
memory with its search index.

**Names.** The ids are named from the biome list in Mojang's
`bedrock-samples` (`metadata/vanilladata_modules/mojang-biomes.json`) at the
revision the mob icons are pinned to: 89 biomes. Bedrock's identifiers are
older than the names the game shows (`mushroom_island` is Mushroom Fields,
`hell` is Nether Wastes), so each has both, and either finds it. An id the
list does not have is kept, drawn in a colour of its own and listed as
`unknown_<id>`; it is counted in `mcmap_biomes_kinds{listed="unknown"}`,
which is the sign that the list needs moving on after a game update.

**The reading.** After the tiles and before the structure survey, each
cycle reads every key once more, through hard links opened read-only as
the other readers' are. On the FWB world that takes about ten seconds at a
whole CPU, as long as each of the other three passes; jumping from one
chunk's biome record to the next was measured and is no faster, because
those records are themselves most of what there is to read: 412 MB once
unpacked. So it is not
done every cycle. Biomes only appear when chunks are generated, and the
chunk count already says whether any were: while the count is what it was
at the last reading, the world is not read, for up to an hour. (The hour is
for the chunks, about 9,000 here, that are counted while they hold no
biome record, and may be given one later without the count moving.) A reading is given up after two minutes; one that fails or is
given up is logged and counted and stops nothing else, and the last good
one goes on being served.

The reading is saved to `DATA_DIR/biomes/biomes.bin`, written whole and
then renamed, so a restart serves the overlay from its first request. A
file that is damaged, or of another version, is logged and ignored.

**Tiles.** `GET /api/biomes/tiles/<dimension>/<zoom>/<x>/<y>.png` is
addressed exactly as the terrain tiles are, at every zoom from -12 to 4,
so the page lays one over the other with the same options. A pixel is the
biome of the column under its middle; ungenerated ground is transparent,
and a tile with nothing in it is a 404. Tiles are drawn when asked for and
not kept: one takes 0.3 to 2 ms from memory and is 2 to 7 KB. `biome=<name>`
draws that biome in its colour and everything else as a translucent dark
grey, which is how one biome is picked out. `GET /api/biomes` gives the
colours for the legend and a `version`; a tile asked for with `v=<version>`
is kept by the browser for good.

**On the page.** The Biomes group has one row, off until the viewer turns
it on. On, it lays the tiles over the terrain and lists under the row the
biomes the dimension holds, largest first, each with its colour and its
share of the area. Choosing one asks for `biome=` tiles, so only it keeps
its colour; choosing it again, or All biomes, goes back. Resting the
pointer on the map, or clicking it, names the biome at that block in the
footer. The listing is asked for again each minute while the overlay is on,
and a new `version` redraws it. A service without biomes answers 404 to the
listing, and the page then has no Biomes group at all.

**Finding one.** Chunks holding a biome that touch, or have one chunk
between them, are one *stretch* of it: joined only edge to edge, a single
island comes out as a dozen. `GET /api/biomes/nearest` gives the nearest
stretches of a biome, each as the nearest column that really is that
biome, with the stretch's area and box; `GET /api/biomes/region` gives the
stretch a block belongs to as rows of chunks, for an outline.

**Limits.** A million chunks; 250,000 of them kept column by column, past
which a chunk is kept as its commonest biome; 254 different biomes a
dimension, past which the rest share one colour; two million chunk-and-biome
pairs indexed for search. Whatever a limit leaves out is counted in
`mcmap_biomes_skipped`. A record that does not parse to its last byte is
skipped and counted.

**From Go.** `(*biomes.Store).At(dimension, x, z)` is the biome at a block
as of the last completed reading, and false where no chunk has been
generated. It is what anything else in the service asks.

To check the reading against a copy of a world, and to measure it:

```sh
MCMAP_REAL_WORLD=/path/to/FWB go test -run RealWorld -v ./minecraft/mcmap/internal/biomes/
```

On the FWB world of 2026-10-05 all 11 ocean monuments stand in a deep
ocean, the witch hut in a swamp, and the fortresses in nether biomes.

## Search

`GET /api/search?q=<text>&dimension=<id>&x=<x>&z=<z>` looks the text up,
without regard to case, in everything the map holds that has a name: the
players online now (by gamertag, in every dimension, from the live layer's
own picture), biomes (by either name), recorded and predicted structures, the world spawn, beds,
containers (by their name or kind), named mobs (by name or type), and the
waypoints of the player asking. A marker and a structure are found by
what the page calls them, the game's own names included: `red bed`,
`trapped`, `light blue shulker`, `evoker`. A hit that is a marker carries
the marker's `colour`, `trapped` and `baby`. Hits in the dimension asked from come
first, nearest first, each with its distance; hits in the other dimensions
follow without one. An answer is at most 50 hits and says how many more
there were. Of each matching biome only the three nearest stretches are
offered, so that a common one does not fill the answer. Of each kind of
structure only the five nearest sites the seed gives are offered, each with
`certainty`: `predicted`, or `candidate` for a site in terrain not
generated yet. The page lists them as Ocean Monument (predicted) and Ocean
Monument (possible site), never as a structure the world has.

A player is listed ahead of everything else, with `id`, the id the live
stream tracks them by, and `live: true`: the position is where they are
now, to the block. A named mob carries its `id` where the snapshot gave it
one; while the same mob is loaded it is listed once, at its live position
with `live: true`, and otherwise where the snapshot left it. A mob named
since the last snapshot is found from the live picture alone. `kind=<kind>`
keeps an answer to one kind of hit, `kind=player` for the players. Nothing
here is more than a session already sees on `/api/live`, and no more of it
than `LIVE_MAX_ENTITIES` a dimension.

Waypoints are searched for the player the session names and nobody else,
by the same call to the agent that `/api/waypoints` makes. `waypoints` in
the answer says whether they were: `searched`, `unavailable` while the
agent cannot be read, or `off`.

**On the page.** One box in the bar. It asks 300 ms after the last
keystroke, from the middle of the view, and gives up the request it has out
when another replaces it. Each hit is listed as the server ordered them,
with what it is, where, and how far, or which other dimension it is in. A
hit is titled as its marker or structure is, by the game's names: the name
a player gave it with what it is after, `Lamb Chop (Sheep, baby)`, or what
it is alone, `Light Blue Shulker Box`, beside the picture the map draws it
with. Where the title already says its kind, as Red Bed does, the kind is
not said twice.
The arrow keys move through the list, Enter chooses and Escape closes it.
Choosing a hit takes the map there, changing dimension if it has to, and
rings the spot for twenty seconds; a biome hit also turns the overlay on
with that biome picked out. A player or a named mob moves, so choosing one
opens the card about it instead of ringing where it was: a player and a
loaded mob are tracked live and can be followed, and a mob that is not
loaded is shown at its last saved position, said as that. A hit says
`live` or `last saved` before its position. The list says so in words when nothing matched,
when there were more hits than shown, and when the waypoints could not be
read. Under 900 pixels wide it opens under the bar and pushes the map down,
so it never covers the layer panel or the card about a mob.

## Slime chunks

On Bedrock a slime chunk follows from its chunk coordinates and nothing
else: the same chunks in every world, whatever the seed. So nothing is
served, and the page works them out. For chunk `cx`, `cz` (a block's
coordinates divided by 16, rounded down):

1. `seed = (cx * 0x1f1f1f1f) XOR cz`, in unsigned 32-bit arithmetic.
2. Seed a Mersenne Twister (MT19937) with it and draw one 32-bit number.
3. It is a slime chunk when that number is a multiple of 10.

Only the first number is drawn, which needs the seeded state's words 0, 1
and 397 and no more:

```js
function isSlimeChunk(cx, cz) {
  const state = new Uint32Array(398);
  state[0] = Math.imul(cx, 0x1f1f1f1f) ^ cz;
  for (let i = 1; i < 398; i++) {
    state[i] = Math.imul(1812433253, state[i - 1] ^ (state[i - 1] >>> 30)) + i;
  }
  const y = (state[0] & 0x80000000) | (state[1] & 0x7fffffff);
  let v = state[397] ^ (y >>> 1) ^ (y & 1 ? 0x9908b0df : 0);
  v ^= v >>> 11;
  v ^= (v << 7) & 0x9d2c5680;
  v ^= (v << 15) & 0xefc60000;
  v ^= v >>> 18;
  return (v >>> 0) % 10 === 0;
}
```

`internal/slime` is the same in Go, with the vectors to check another
implementation against. Chunks -1, 0 and 109, 3 are slime chunks; 0, 0 and
110, 3 are not; 16,304 of the 160,000 chunks from -200 to 199 on both axes
are; and the 16 by 16 chunks from -8 to 7, north at the top and west on the
left, are:

```
...............#
..#.............
............#...
........#.......
#.............#.
...............#
.....#..##......
#...##..........
..#....#...#....
...........#....
....#...........
.#..............
#.......#.......
..............#.
.#......#.......
............#...
```

The function is the one the game's own was found to be when it was taken
apart, and the one Bedrock slime finders use. It was also set beside the
FWB world: of the nine chunks holding a slime below y 40 and outside a
trial chamber, eight are slime chunks, where one in ten would be by
chance. Slimes spawn in a slime chunk below y 40.

**On the page.** Slime chunks is a row in the Overlays group, off until
turned on, that shades and outlines them over the Overworld. It is drawn a
tile at a time on a canvas, so only the chunks in view are ever worked out,
and not at all from zoom -3 out, where a chunk is two pixels. In the other
dimensions the row is greyed out. `web/slime.js` holds the function above
letter for letter, which a test checks, and offers it as
`window.mcmap.isSlimeChunk`.

The world spawn is served with the overworld's structures, and the page
marks it with a diamond under a World spawn row in the Structures group.

## Trails

A trail is where a player has been: a point each time they have moved four
blocks, taken from the live layer once a second. A new line starts where
the player was not seen for 30 seconds, changed dimension, or moved more
than 256 blocks between two samples, so a logout, a portal or a teleport
is a gap and not a straight line across the map. Mobs are not recorded.

Older points are kept at lower detail, so that a long retention fits in the
point limit. A point is thinned out if it is closer to the point kept before
it than its age allows: under an hour old, 4 blocks (full detail); an hour
to a day, 16; past a day, 64. A point that starts a line, or is the last
before a gap, a portal or a teleport, is never thinned, and none is moved,
so the lines keep their starts and ends and never join across a gap. It is
done for each player about once a minute of recorded time, in one pass over
that player's points under the lock (about 30 microseconds for a trail of
20,000 points, measured with `BenchmarkThin`). `GET /api/trails` states the
steps in `thinning`, for the page to say what is drawn at lower detail.

Three limits are enforced, and published in `mcmap_trails_limit`:

- **Age.** No point is older than `TRAILS_MAX_AGE` (24 hours). Old points
  go every second, and again whenever trails are asked for.
- **Count.** A player keeps at most `TRAILS_MAX_POINTS` points (5,000:
  twenty kilometres at one point every four blocks). Points are thinned
  first, and only a trail still over the limit loses its oldest.
- **Players.** At most 64 players have a trail. The live list is text the
  game server's console wrote, so a 65th name pushes out the trail of
  whoever was seen longest ago.

A held point is 32 bytes, and 34 measured over trails grown to the limit
(`TestAHeldPointCostsAtMostThisManyBytes`), with the slack of the arrays
they grew in. At the defaults, 64 players are at most about 11 MB.

**What a week costs.** Set `TRAILS_MAX_AGE=168h` with `TRAILS_MAX_POINTS=20000`:
64 players at the limit are 64 x 20,000 x 34 bytes, about 44 MB. A player
sprinting in a straight line (5.6 blocks a second) leaves about 3,600 points
in the last hour, 1,260 an hour in the 16-block tier and 315 an hour past a
day, so 20,000 holds four hours of that every day for the week; walking
about, or circling a base, thins much harder. Only a trail over the limit
after thinning loses its oldest points, and `reason="count"` rising is the
sign to raise it.
`mcmap_trails_points_dropped_total{reason}` counts what each limit let go,
and `mcmap_trails_oldest_point_age_seconds` staying under the age limit is
the evidence that it holds.

**Trails do not survive a restart.** They are in memory only, on purpose:
they are the one thing the map holds about a person rather than the world,
and on the volume they would be a record of who was where and when that
outlives the process, beside the retained copies of the world. What a
restart costs is lines the players redraw by playing.

Every logged-in player already sees where every other player is, live, so
`GET /api/trails` serves every player's trail on the same terms. An answer
carries at most 20,000 points, each player's newest.

**On the page.** Trails is a row in the Overlays group, off until turned
on, with a choice of the last 1, 6 or 24 hours, sent as `since`. The
viewer's own trail is the green the live layer draws them in, which
`live.js` offers as `window.mcmap.playerColour`; every other player's is
one of eight colours picked by their gamertag, or the next one free where
two on the map pick the same. Under the row is each player with a trail
and their colour, and choosing a name fits the map to that trail. The
lines are drawn on the live layer's canvas behind its markers, so a marker
is never crossed by its own trail and is still what a click on it reaches;
hovering a line anywhere else names the player and the time it covers. The trails are asked for about once a
minute and never more often; between answers the lines are carried forward
from the live frames by the same rules the server records by, and the next
answer replaces them. The row's switch is kept with the rest under
`mcmap.layers`, and the window under `mcmap.trails`. A service without
trails answers 404 once, and the page then has no Trails row.

## Page controls

**The layer panel.** Every layer's switch is a row in one panel over the
map, grouped as Live, Markers, Structures, Biomes and Overlays. A group
appears once a layer registers a row in it, folds away, and has All and
None. A row is a checkbox, a count, and where there is something to say a
note under it, such as why nothing is predicted. The viewer's choices are
kept in the browser under `mcmap.layers`, and which groups are folded under
`mcmap.panel`. The filters the page had before the panel were kept under
`mcmap.live`, `mcmap.markers` and `mcmap.structures`; a row with no choice
saved yet takes the one saved there, so nobody's filters reset. The old
single Structures switch, if it was off, carries over as Known, Predicted
and Possible all off, and the old Live switch as paused.

**Adding a layer.** A layer is a script of its own, loaded after
`layers.js`, which registers its rows and never edits the panel:

```js
const row = window.mcmap.layers.register({
  group: 'biomes',      // live, markers, structures, biomes, overlays, or a new id
  id: 'plains',         // unique within the group; the choice is saved under it
  label: 'Plains',
  enabled: true,        // the choice until the viewer makes one; true if left out
  order: 10,            // lower first; rows given none go last, as registered
  groupLabel: 'Biomes', // the title of a group that is not one of the five
  swatch: 'dot plains', // class of a colour key beside the label, styled in style.css
  picture: node,        // an element shown in the key's place while it is not hidden
});
row.enabled;            // the saved choice, kept current
row.setCount(1234);     // or null for none
row.setNote('Not surveyed yet'); // or '' for none
row.setLabel('Ocean Monuments'); // for a name that arrives late
row.onToggle((on) => { /* draw or clear */ });
row.setAvailable(false);         // greyed out, and skipped by All and None
row.setEnabled(true);            // switch it as the viewer would; kept, and onToggle is told
row.setBody(node);               // the layer's own controls under the row; null for none
row.remove();
```

`onToggle` is called with the new value when the viewer changes the row,
by its checkbox or by the group's All or None, and not when it is
registered: read `enabled` once to begin with. Registering an id again
replaces its row. A label, a note and a group's title are set as text,
never parsed, so a name from the world is safe in any of them.
`window.mcmap.layers.ready` is a promise that resolves, with the same
object, once `register` exists; a script loaded after `layers.js` can call
`register` at once, and one that might run before it waits on `ready`.
Each of `live.js`, `markers.js` and `structures.js` is an example.

**The refresh countdown.** The footer counts down to the next refresh of
the terrain and the markers, from `snapshotAt` and `refreshSeconds` and the
server's own clock as its `Date` header gives it. The service counts its
interval from the end of a cycle, so a healthy refresh lands a little after
zero; for the first 90 seconds past the time the page says `Refresh due
now`, and asks every five seconds whether it has landed. Past that, which
is what a quiet window or a failing cycle looks like, it says `Refresh
overdue by` and counts up, asking every 20 seconds after the first two
minutes. It starts again only when a new snapshot has been seen.

**Live updates.** Beside the grid switch are Pause and an interval of 1,
2, 5, 10 or 30 seconds, both kept in the browser under `mcmap.liveControl`.
A slower interval keeps the stream open and draws the newest frame when one
is due; the rest are dropped unread. Pausing closes the stream, so a paused
tab is not among `mcmap_live_subscribers`, and leaves the last picture on
the map under a notice saying it is paused and how old the positions are.
Resuming opens the stream again. As before, the stream is also closed
while the tab is hidden.

**Inspecting and following.** A click or a tap on a live mob or player
opens a card in the bottom left corner of the map: its picture as the map
draws it, its name tag or gamertag, its type by the game's name for it,
the dimension, and `x y z` to the tenth of a block, which is what the pack
sends. A mob with no name tag is titled by its type, which is then not
said again under the title. The live record does not say whether a mob is
a baby, so the card cannot. The card is brought up to date with every frame
drawn. Copy puts `x y z` on the clipboard, and where there is no clipboard
to write to selects the numbers instead. Close or Escape shuts the card. A
drag that starts on a marker pans the map and opens nothing.

Follow keeps the map centred on the entity at the zoom the viewer has, and
draws a solid ring round it; an entity that is only inspected has a dashed
one. Dragging the map or panning it with the arrow keys turns Follow off
and leaves the card open. Zooming does not.

The card finds its entity again by the id each record carries (`i`), which
the game gives a player and a mob alike and keeps for as long as they
exist. It never matches by position. When a whole frame of the entity's
dimension comes without that id (it died or despawned, its chunk unloaded,
or it fell outside `LIVE_MAX_ENTITIES`), the card says it is no longer
tracked, keeps the last coordinates with how long ago they were seen, and
turns Follow off; if the same id comes back, the card picks it up again
with Follow still off. A stream covers one dimension, so for a player the
page then opens the other dimensions' streams, reads the first frame of
each and shuts them, up to three times three seconds apart, and says which
dimension the player is in if it finds them. Switching the map to another
dimension turns Follow off and the card says it is not tracking. While
live updates are paused the card says so and how old its position is, and
resuming carries on with the same id.

Go to centres the map once on whatever the card is about, at its last known
position, without following it.

A marker is drawn on a canvas and cannot take the keyboard's focus, so
opening the card on one needs a pointer; a named mob's entry in the list
under its row is a button that opens it too, and everything on the card is
a button.

Everything here is a native button, checkbox or select: each is reached
with Tab, worked with Space or Enter (the arrow keys, for the interval),
and outlined while it has the focus.

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
  and the map API, tiles, markers, biomes, search, trails and live stream
  behind the session.
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
| `DATA_DIR` | no | `/data` | Mirror, retained world copies, tiles, the installed renderer and the fetched mob icons, marker pictures and names. The retained copies are the one thing here that cannot be rebuilt, so keep it on a volume |
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
| `ICONS_ENABLED` | no | `true` | `false` draws every live marker as a dot or arrow: no mob icon, marker picture or name is fetched, no head is accepted, and `/api/icons`, `/api/icons/picture/` and `/api/names` are not served |
| `ICONS_REF` | no | the commit tagged `v1.26.50.4` | Tag or commit of Mojang's `bedrock-samples` the mob icons, marker pictures and names are fetched at. A commit cannot move; a tag can |
| `STRUCTURES_ENABLED` | no | `true` | `false` reads no structures and does not serve `/api/structures` |
| `STRUCTURE_SEED` | no | the low 32 bits of the seed in `level.dat` | The 32 bits structure placement is seeded with, 0 to 4294967295, for a world whose `level.dat` does not hold them. As secret as the seed |
| `BIOMES_ENABLED` | no | `false` | `true` reads biomes and serves `/api/biomes` and its tiles; off, search finds no biomes |
| `TRAILS_ENABLED` | no | `false` | `true` keeps player positions and serves `/api/trails`. Trails also need `LIVE_ENABLED` |
| `TRAILS_MAX_AGE` | no | `24h` | How long a trail point is kept; `1m` to `168h`. Checked only with `TRAILS_ENABLED=true` |
| `TRAILS_MAX_POINTS` | no | `5000` | Most trail points kept for one player, after old ones are thinned; 10 to 50000. `20000` suits `TRAILS_MAX_AGE=168h`. Checked only with `TRAILS_ENABLED=true` |

## Endpoints

| Route | Purpose |
|---|---|
| `GET /` | The map page; public, and holds nothing about the world |
| `GET /api/config` | Whether there is a login; public |
| `POST /auth/start`, `GET /auth/status`, `POST /auth/logout` | The login flow above |
| `GET /api/me` | The logged-in player's gamertag |
| `GET /api/map` | Session required. World name, refresh interval (`refreshSeconds`), when the last snapshot was taken (`snapshotAt`, absent before the first), each dimension's extent and last render time, `live` (whether there is a live stream to open), and `problem` (`snapshot` or `render`) while the last cycle failed |
| `GET /tiles/{dimension}/{zoom}/{x}/{y}.webp` | Session required. One 256-pixel tile. Zoom 0 is one block per pixel; each step below halves the scale. 404 where the world has no chunks |
| `GET /api/live?dimension=<id>` | Session required. Server-sent events: one frame at once and one per sample, each the whole of that dimension as `at`, `serverNow`, `players`, `mobs`, `more`, `stale` and `ttlSeconds`. 400 for an unknown dimension, 503 when too many streams are open. Not served with `LIVE_ENABLED=false` |
| `GET /api/markers?dimension=<id>` | Session required. That dimension's `beds`, `containers` and `mobs`, each `x`, `y`, `z` with `k` (a container's kind or a mob's type), `n` (a name, where there is one), `c` (a bed's or shulker box's colour, `undyed` for a shulker box nobody dyed, absent when not known), `t` (true on a trapped chest), `b` (true on a baby mob) and `i` (a named mob's own id, the `i` the live stream gives the same mob while it is loaded; absent where the world does not say); `at`, the snapshot they were read from; and `more`, how many of each were left out at the limit. Carries an `ETag` and answers 304 to a matching `If-None-Match`. 400 for an unknown dimension. Not served with `MARKERS_ENABLED=false` |
| `GET /api/waypoints` | Session required. The logged-in player's own `waypoints`, each `name`, `x`, `y`, `z` and `dimension`, across all dimensions, and `more`. 502 while the agent cannot be read, 503 when too many reads are open. Not served without `AGENT_URL` |
| `GET /api/icons` | Session required. Which live markers have a picture: `mobs` with a `version` and the `types` that have an icon, `pictures` with a `version` and the `keys` that have a picture, `names` with the `version` of `/api/names`, `heads` giving each head's version by gamertag in lower case, and `me`, the gamertag the session's player is online under. Carries an `ETag` and answers 304 to a matching `If-None-Match`. Not served with `ICONS_ENABLED=false` |
| `GET /api/icons/mob/{type}?v=<version>` | Session required. That mob type's icon as a PNG, kept for good by the browser when `v` is the current version. 404 for a type with no icon |
| `GET /api/icons/picture/{group}/{name}?v=<version>` | Session required. The marker or structure picture with the key `{group}/{name}` as a PNG, kept for good by the browser when `v` is the current `pictures.version`. 404 for a key `/api/icons` does not list |
| `GET /api/names` | Session required. Display names by id: `entities` (by mob type), `containers` (`chest`, `trapped_chest`, `barrel`, `shulker`), `beds` and `shulkers` (by colour, plus `default`, and `undyed` for shulkers) and `structures` (by kind), with a `version`. Every value is plain text, to be written as text and never as markup. Carries an `ETag` and answers 304 to a matching `If-None-Match`. Not served with `ICONS_ENABLED=false` |
| `GET /api/icons/head?name=<gamertag>&v=<version>` | Session required. The head of the one online player holding that gamertag, as a PNG. 404 if nobody does, two players do, or their skin gave no head |
| `GET /api/structures?dimension=<id>` | Session required. `recorded` (each a `kind` and its box, `minX` to `maxZ`, with `areas`, or for a `village` with `village`: `counted`, `villagers`, `golems`, `cats`, `beds`, `bells`, `jobSites`), `predicted` (each a `kind`, `x`, `z`, with `candidate` where the chunk is not generated and the biome will decide, or `generated` where the chunk is finished, suits the kind and the world recorded none), `recordedMore` and `predictedMore` for what the bounds left out, `prediction` (`verified`, `unverified`, `refuted` or `unknown`, of the seed), `kinds` (for each kind the dimension has a rule for, its own `state` and how many recorded ones `agree` and `disagree`), `surveyed`, `at`, and with the overworld `spawn`. 400 for an unknown dimension. Not served with `STRUCTURES_ENABLED=false` |
| `GET /api/biomes?dimension=<id>` | Session required. `extracted`, `at`, `version`, `tiles` (`minZoom`, `maxZoom`, `size`), and `biomes`, largest first: each `id`, `name`, `label`, `color` (`#rrggbb`), `known`, `area` in square blocks, `chunks` and `regions`. 400 for an unknown dimension. Served only with `BIOMES_ENABLED=true`, like the four below |
| `GET /api/biomes/tiles/{dimension}/{zoom}/{x}/{y}.png?biome=<name>&v=<version>` | Session required. One 256-pixel tile of the overlay, addressed as the terrain's; zoom -12 to 4. `biome` picks one out and dims the rest. Carries an `ETag`, answers 304 to a matching `If-None-Match`, and is kept for good when `v` is the current version. 404 where the world has no chunks, 400 for a bad address or an unknown biome |
| `GET /api/biomes/at?dimension=<id>&x=<x>&z=<z>` | Session required. `generated`, and with it the `biome` at that block |
| `GET /api/biomes/nearest?dimension=<id>&biome=<name>&x=<x>&z=<z>&limit=<n>` | Session required. `biome`, and `hits`, nearest first: each `x`, `z`, `distance` and its `region` (`area`, `chunks`, `minX`, `minZ`, `maxX`, `maxZ`), with `more`. `limit` is 10 unless given and at most 50. 400 for an unknown biome |
| `GET /api/biomes/region?dimension=<id>&x=<x>&z=<z>` | Session required. The stretch of biome that block is in: `found`, `biome`, `region`, and `rects`, at most 4,096 rows of chunks each `[minX, minZ, maxX, maxZ]` in blocks, with `rectsMore` |
| `GET /api/search?q=<text>&dimension=<id>&x=<x>&z=<z>&limit=<n>&kind=<kind>` | Session required. `hits`, each `kind` (`player`, `biome`, `structure`, `spawn`, `bed`, `container`, `mob`, `waypoint`), `name`, `detail`, a marker's `colour`, `trapped` and `baby` where it has them, a player's or mob's `id` and `live` (true where the position is the live layer's), `dimension`, `x`, `z`, `y` where there is one, and `distance` in the dimension asked from; `more`; and `waypoints` (`searched`, `unavailable`, `off`). `limit` is 20 unless given and at most 50. `kind`, if given, keeps the answer to that kind. 400 without `q` of 1 to 64 characters, a dimension, `x` and `z`, or with a `kind` that is not one |
| `GET /api/trails?dimension=<id>&player=<gamertag>&since=<unix seconds>` | Session required. `players`, each a `name` and `segments`, lines of `[t, x, y, z]` points oldest first; `more`, `maxAgeSeconds` and `maxPoints`; `thinning`, the detail points are kept at, each a point `olderThanSeconds` (0 for full detail) and its `stepBlocks`. 400 for an unknown dimension. Served only with `TRAILS_ENABLED=true` and `LIVE_ENABLED=true` |
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
| `mcmap_icons_fetches_total{result}` | Attempts to fetch the mob icons, marker pictures and names, `ok` or `failed`. None at all means they were read from the volume, or that `ICONS_ENABLED=false` |
| `mcmap_icons_marker_pictures` | Marker and structure pictures held, 42 when whole. Zero means every marker is a ring and every structure a letter, which is also the case with `ICONS_ENABLED=false` |
| `mcmap_icons_names` | Display names read from the language file. Zero means every name served is a tidied id, or that `ICONS_ENABLED=false` and none is served |
| `mcmap_icons_player_heads`, `mcmap_icons_player_heads_refused_total` | Online players with a head, and heads the agent sent that were refused |
| `mcmap_structures_recorded{dimension,kind}`, `mcmap_structures_predicted{dimension,kind,certainty}` | Structures on each layer at the last survey; `certainty` is `predicted`, or `candidate` for a site in terrain not generated yet |
| `mcmap_structures_seed_verified` | 1 while recorded structures are where the seed puts them. 0 means nothing is being predicted |
| `mcmap_structures_kind_verified{kind}` | 1 while the seed is verified and the kind's own recorded structures are where its rule puts them. 0 means that kind is not being predicted |
| `mcmap_structures_prediction_disagreements{kind}` | Places where the seed and the world's records disagree |
| `mcmap_structures_areas_skipped{reason}` | Recorded boxes left out: `malformed`, `unknown` (a kind this version does not know), `limit` |
| `mcmap_structures_survey_last_success_timestamp_seconds`, `mcmap_structures_survey_duration_seconds`, `mcmap_structures_survey_failures_total` | Whether the survey is running |
| `mcmap_structures_villages_skipped{reason}` | Villages left out: `empty` (counted by the game, no villagers), `malformed`, `unknown` (a key this version does not know), `limit` |
| `mcmap_structures_village_read_duration_seconds`, `mcmap_structures_village_read_failures_total`, `mcmap_structures_villages_last_success_timestamp_seconds` | How long the village records took to read; surveys that could not read them and kept the villages of the one before; and the snapshot the villages being served came from |
| `mcmap_biomes_chunks{dimension}` | Chunks whose biomes are held |
| `mcmap_biomes_kinds{listed}` | Different biomes held, `known` to this version's list or `unknown`. Unknown above zero means the list is behind the game |
| `mcmap_biomes_skipped{reason}` | What the last reading left out or cut down: `malformed`, `out_of_range`, `limit`, `coarsened`, `kinds`, `unindexed` |
| `mcmap_biomes_readings_total{result}` | Cycles by what became of the biomes: `read`, `unchanged` (nothing was generated, so the world was not read), `failed` |
| `mcmap_biomes_last_success_timestamp_seconds`, `mcmap_biomes_duration_seconds` | The snapshot the biomes served were read from, and how long that reading took |
| `mcmap_biomes_save_failures_total` | Readings that could not be written to the volume |
| `mcmap_trails_points`, `mcmap_trails_players` | Trail points held in memory, and players with a trail |
| `mcmap_trails_limit{limit}` | The retention in force: `age_seconds`, `points_per_player`, `players` |
| `mcmap_trails_oldest_point_age_seconds` | Age of the oldest trail point held. It stays under the age limit |
| `mcmap_trails_points_dropped_total{reason}` | Trail points let go: `age`, `thinned`, `count`, `players` |

## Build and test

From the repo root:

```sh
go test -race ./minecraft/mcmap/...
docker build -f minecraft/mcmap/Dockerfile -t minecraft-map:dev .
```

The page under `web/` is plain files with no build step, and CI has no
JavaScript runner: `web/*_test.go` holds what can be checked of the scripts
as text, and the rest is checked in a browser. Leaflet is vendored
in `web/lib/leaflet` with its licence.

## Releases

Cut by [`semantic-release.yml`](../../.github/workflows/semantic-release.yml)
from commits that touch this component, tagged `mcmap-v<version>`, and
published as `ghcr.io/jdwillmsen/minecraft-map:<version>` and
`docker.io/jdwillmsen/minecraft-map:<version>`. Never `latest`.
