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

A mob is drawn as its face, or its spawn egg, and a player as their skin's
head, each inside a ring or border in its category's colour so the filters
still read at a glance. A marker with no picture is the dot or arrow it was before.

**Mob icons** are Mojang's own spawn-egg item textures. None of them is in
this repository or in the image: the service fetches them at runtime from
Mojang's public [bedrock-samples](https://github.com/Mojang/bedrock-samples)
repository, at one pinned revision, and keeps them under `DATA_DIR/icons`
(252 files and 229 KB, with the marker pictures, the names and the
[pictures made from the game's models](#faces-blocks-and-structure-pictures)
that are fetched with them).

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
- **What is asked for**: one listing from `api.github.com`, then from
  `raw.githubusercontent.com` the two atlases, 180 definitions, 193 model
  files and 173 render controllers, about 90 spawn-egg textures, 37 marker
  textures, about 150 textures the made pictures are made from, and the
  language file: 839 requests and 7.7 MB in all, in 13 to 16 seconds, measured at
  the default pin. Before faces and blocks were made it was 319 requests
  and 1.6 MB in under ten seconds. Most of the growth is the listing:
  three directories are wanted, the API lists one directory to a request,
  and the one request that lists more is the whole of `resource_pack`, 5.5
  MB of it, which is chosen over three requests because it is the listing
  that is rationed, sixty an hour to an address. No redirect is followed,
  each file has a size limit and the whole fetch a count, byte and time
  limit, and every picture served as it came must decode as a PNG of at
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
into a sprite per picture, colour and shape, and stamped onto the live
layer's one canvas with `drawImage`.

#### One registry, three styles, and size apart

`icons.js` holds one registry of everything the map draws a picture for,
by key: `mob/<type>`, `villager/<profession>`, `bed/<colour>`,
`shulker/<colour>`, `container/<kind>`, `structure/<kind>`,
`marker/waypoint`. A key has the renditions the server lists for it (a
mob's face and its egg; anything else's flat picture and its block), and
which is drawn is decided there and nowhere else, so the map, the layer
panel, the inspect card, a structure's sheet and a search result show the
same picture. It is on `window.mcmap.icons.registry` for any script that
draws: `renditions(key)`, `chosen(key)`, `marker(key, colour, { baby })`
for the map's own sprite, `picture(key)` for an element, `tier()`, and
`STATES`.

| Marker style | A mob | A bed or container | A structure |
|---|---|---|---|
| Dots | Its dot, as before there were pictures | Its ring | Its letter |
| Pictures on plates (the default) | Its face, or its egg, on a round plate | Its flat picture on a square plate | Its picture on a square plate |
| Large pictures | Its face, larger, with no plate | The block drawn from three sides | Its picture, twice the size, framed |

A plate is, from the outside in, a dark casing a pixel wide, a ring two
wide in the layer's colour, a dark backing and the picture, which is kept
inside the ring so that nothing in it covers the colour the filters are
read by. Round is what moves and square is what stays put. A picture with
no plate is cased round its own outline, in the layer's colour and then
in the dark, so a chest is a chest's shape and still reads on snow.

**Mob picture** chooses between a mob's face and its spawn egg. A mob
with no face (17 of 92 at the default pin) is its egg under either, and
one with a face and no egg its face.

**Size is chosen apart from style**: the box a picture is drawn in is 12,
16, 24 or 32 pixels on a plate (32 and 48 for the two larger on a screen
that is not dense, as before) and 24, 32, 48 or 64 with no plate. Within
its box a picture is enlarged by the most whole screen pixels to each of
its own that fit, so a face 8 pixels a side fills a 16 pixel box at 2 and
one 6 a side stands 12 in it. Where a whole number would leave a picture
under three quarters of its box it is stretched to the box, unblended, so
that no picture is markedly smaller than the next; its pixels are then
one or two screen pixels wide and never blurred. One larger than its box
is blended down.

**Markers that heap up are grouped from far out**, where the viewer has
it so (Appearance, "Group markers when zoomed out", on unless switched
off, kept with the other appearance settings and carried by a saved view
that has them). From a block to two pixels outwards, whatever falls in
one square of the screen is one mark: mobs of every row, beds and
containers together. A mark a family was tried first, and put several
marks on one spot with their counts across each other. A player, a named
mob, a waypoint, a structure, a saved position and whoever the card is
about are each somebody: they are never one of several, and are drawn
over the groups' marks. Whatever a filter hides is not counted.

A square is 48 CSS pixels a side, and is a square of the world at that
zoom and not of the screen, so panning regroups nothing and a group is
the same from one frame to the next; a mob within 15% of a square's side
past its edge stays in the group it was in, so one pacing over an edge
does not make two marks blink. The mark stands in the middle of its
square, not of its members, and is 26, 32 or 38 pixels across for up to
24, up to 199 and more, so that two squares side by side always have ten
pixels or more between their marks. It is a ring in the colours of the
families it holds, each an arc as long as its share, largest first from
the top, round the count of them all, cased in the dark as a plate is;
the same mark in every style, four pixels narrower with plain marks.
Under the pointer it lists each family with its count and each type
under it. A click or a tap takes the map to its members, no closer than
a block to the pixel, where nothing is grouped; where that would not
part them, because they stand within 16 blocks of each other, it lists
them instead, by family and type with counts, and one chosen from the
list has its card opened (a mob) or is gone to (a bed or a container).
`N` lists the next group from the middle of the map outwards, with the
focus in the list, so a group is reached and worked without a pointer;
Escape shuts it. The panel's counts are of things and not of marks, and
do not change. The far tiers still apply to what is left by itself.

The sorting is `groups.js`, with nothing of the page in it; the live
layer draws every mark, and the markers' layer offers it its beds and
containers and is told which were taken. Until a script that draws has
said so nothing is taken off the map, so a page whose scripts are of two
ages draws every marker as before. Measured in headless Chromium at 1300
by 800 with 900 mobs and 2,100 markers on the map, three steps out, over
five seconds of live frames: 3,017 marks with it off and 21 with it on,
a median frame of 16.7 ms and no long task either way, in each style.

**A face is sized and placed by its head**, not by the whole of its
picture. A face is as wide and as tall as its horns, its hat and its nose
make it: a villager's is its head, 8 by 10, over one row of nose, and
squared to 11 it had its head half a pixel left of the middle and half a
pixel high, which on a plate came out as much as 2 pixels off, at one and
a half screen pixels to the pixel. The server says where the head is in
each face (`boxes` in `/api/icons`), and the page puts the middle of the
head on the middle of the plate, on whole pixels, at a whole number of
screen pixels to the pixel chosen by the head's longer side. On a plate
that number may take the head up to a third over its box where the next
one down would leave it under three quarters, since the ring hides what
spills and a head a little cropped is still the head: a villager is at
2 to the pixel in a 16 pixel box, as wide as the zombie beside it. With
no plate nothing may spill, so there the whole face is sized to its box
as before. A face all of which fits its box is kept wholly inside it, as
near the middle by its head as that leaves, so a fox keeps its ears; and
a head that is half or less of what is drawn (a bat's between its wings,
a shulker's under its lid) is not what the picture is of, and that one
is drawn by the whole of it. Every picture on the page is drawn by the
one routine, so the map, a row of the panel, the card, a search result
and a structure's sheet agree. A server from before it said where heads
are has its faces drawn by the whole picture, as they were. On the page itself, in a panel's
row or a search result, a block is drawn only where it fits its 16 pixel
box whole, which is on a dense screen; elsewhere its flat picture is.

**How far out the map is decides what is drawn at all**, whatever the
style. Zoom 0 is a block to the pixel.

| Zoom | Mobs | Beds and containers | Names over markers |
|---|---|---|---|
| -3 and further out | Dots | Rings | None |
| -2 | Dots | Pictures | None |
| -1 | Pictures | Pictures | None |
| 0 and closer | Pictures | Pictures | Drawn |

Mobs are where players are, so their plates heap up sooner than those of
beds and chests, which are spread over the world. Players and waypoints
are few and are what the map is looked at for, and keep their heads,
pictures and names from however far out. The layers are told when a tier
is crossed and not at every step.

**States are written down once**, in the registry: the marker under the
pointer is ringed two pixels out, the inspected one four, and a mark that
is only a record of where something was (a named mob the live layer is
not drawing) is at 55% in a broken ring. That last is laid over whatever
the style draws, so a saved mark is told from a live one in all three.

A sprite is composed once per picture, colour and shape at the screen's
density and kept until the theme, style, size or mob picture changes, or
the screen's density does (a window moved to another screen, a page
zoomed), when the canvas is told and everything is composed again;
drawing a marker is one `drawImage`. With 900 mobs and 2,100 markers in
view at 1300 by 800 in headless Chromium, panning held 16.7 ms frames in
every style, with one repaint a median 2.6 ms as dots and 3.7 ms as
plates or large pictures.

**What was kept before.** There used to be two switches, pictures or
plain, one for mobs and players and one for everything placed. A record,
a saved view or a link from then is read as dots where the mobs were
plain and as plates otherwise, and the switches are not kept. For the few
minutes a page and its scripts can be of different versions, a newer
script on older settings draws as the two switches say, and an older
script on newer settings is told pictures or plain from the style.

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

**Marker pictures** served as they came are 37 small PNGs, by key, and a
structure's is one of the [made pictures](#faces-blocks-and-structure-pictures)
with the item named here to fall back on:

| Key | Source under `resource_pack/textures/` | Why that one |
|---|---|---|
| `bed/<colour>` | `items/bed_<colour>`, taken from the atlas's `bed` list, whose order is the colour number the world stores | The bed item, one per colour |
| `container/chest`, `container/trapped_chest` | `blocks/chest_front`, `blocks/trapped_chest_front` | A chest has no item texture: the game draws the block. Its front is the face with the latch |
| `container/barrel` | `blocks/barrel_side` | Likewise; the side is the face with the hoops |
| `shulker/<colour>`, `shulker/undyed` | `entity/shulker/shulker_<colour>`, composed | See below |
| `marker/waypoint` | `items/compass_item` | A waypoint is a place to find your way back to |
| `structure/fortress` | a blaze's face, or `items/netherbrick` | The mob met there and nowhere else; a fortress is built of nothing but the brick |
| `structure/monument` | an elder guardian's face, or `items/prismarine_shard` | Each monument has three; the shard is dropped only by its guardians |
| `structure/outpost` | a pillager's face, or `items/crossbow_standby` | Who holds it, or their weapon |
| `structure/witch_hut` | `map/swamp_hut`, or `items/cauldron` | The game's own mark for a hut on an explorer's map. A witch's face, sized by its head, loses its hat and reads as a villager |
| `structure/village` | `map/village_plains`, or `items/villagebell` | The game's own mark for a village on an explorer's map: a house, 8 pixels a side with its own outline, drawn at a whole number of screen pixels to the pixel. A villager's face stood for it before, which read as a villager. Which of its mark, its mob's face and its item a kind is drawn as is one line of `structureArts` in `internal/icons/pictures.go` |
| `structure/village_desert`, `_savanna`, `_snowy`, `_taiga` | `map/village_<biome>`, or the plains one | The game's marks for a village built in each of those biomes. A structure's sheet wears the one for the biome at its middle once that is known; a mark on the map wears the one the survey says, which it sets from the biome at the middle of the village's box (`variant` on a recorded village, absent for plains and where the biome is not known), and a sheet falls back on asking the biome itself |
| `structure/stronghold` | `items/ender_eye` | What finds one, and what lights its portal. No mob is a stronghold's own |
| `structure/trial_chamber` | `map/trial_chambers`, or `items/trial_key` | The game's own mark for a chamber; the key opens its vaults |
| `structure/desert_pyramid` | `map/desert_pyramid`, or `blocks/sandstone_carved` | The game's own mark for a pyramid; carved sandstone alone is a pale square |
| `structure/jungle_temple` | `map/jungle_temple`, or `blocks/cobblestone_mossy` | The game's own mark for a temple; mossy cobble alone is speckle |
| `structure/igloo` | `items/snowball` | What an igloo is made of |
| `structure/trail_ruins` | `items/brush` | What its buried blocks are brushed with |
| `structure/abandoned_camp` | `items/campfire` | Every camp has one, unlit |
| `structure/end_city` | `items/elytra` | What a city's ship is gone to for. A shulker's face is a square of one colour |
| `structure/end_gateway` | `items/ender_pearl` | What goes through one |
| `structure/exit_portal` | `items/end_crystal` | What is set round the portal to bring the dragon back. The egg's side is black on a dark plate |
| `structure/bastion` | a piglin brute's face, or `blocks/gilded_blackstone` | The mob met there and nowhere else; the block only a bastion has |
| `structure/ruined_portal` | `blocks/crying_obsidian` | The block a ruined portal's frame is broken with |
| `structure/mansion` | the mansion cut from `map/map_icons`, or `items/totem` | The game's own mark for a mansion, from the sheet its older marks are kept on. An evoker's face is an outpost's pillager over again |
| `structure/ancient_city` | `map/ancient_city`, or `items/echo_shard` | The game's own mark for a city. A warden's face is dark on a dark plate |
| `structure/shipwreck` | `items/boat_oak` | A boat |
| `structure/ocean_ruins` | a drowned's face, or `items/nautilus` | Who walks them |
| `structure/buried_treasure` | `items/heartofthesea_closed` | What every one of them holds |

A structure has no item of its own, so each is the face of the mob a
player meets there, which is the convention seed maps follow, and failing
that a vanilla item that could stand for nothing else on this map. The
face is our own crop of Mojang's texture at the pin, made as every mob's
is, and never another site's drawing. The samples also hold the game's
own map markers for a village, a swamp hut and a trial chamber
(`textures/map/`); they are 8 pixels a side with a black outline drawn in,
half the detail of a face, and read as three small houses, so none is
used.

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

Each of these is asked for by a path known ahead. Each is bounded, decoded
and encoded again exactly as a mob icon is.

#### Faces, blocks and structure pictures

Beside the textures served as they came, the service makes pictures of its
own from the game's models, at the fetch and from the same pin:

| Key | What it is | Made from |
|---|---|---|
| `face/<type>` | A mob's face, 75 of the 92 mob types at the default pin | Its model and its texture |
| `villager/<profession>` | A villager's face in each of 14 professions, as a structure's sheet lists them | The same, with the profession's texture laid over the skin |
| `structure/<kind>` | The face of the mob that stands for the structure | That mob's face, or the item above |
| `block/chest`, `trapped_chest`, `ender_chest`, `barrel`, `spawner`, `vault` | The block seen from above and to one side | The three textures the terrain atlas names for its top, front and side |
| `block/shulker_<colour>`, `block/shulker_undyed` | Likewise | The lid and base of the texture its model is wrapped in |
| `block/bed_<colour>` | Likewise, 16 by 6 by 32 | The two slabs of the texture its model is wrapped in |
| `block/bell` | The bell as the game shows it in the hand, at two pixels to each of its own so that it is the size a block's picture is | `items/villagebell`, which the atlas names as `bell_carried`. A bell is no box, and its three sides drawn as one are three bells adrift |

**A face** is worked out the way the game would draw the mob, and nothing
about any mob is guessed:

1. The mob's client definition names its models, its textures and its
   render controllers. Each controller is an expression over what the mob
   is at that moment (`query.is_baby ? Geometry.baby : Array.geos[v.index]`),
   and is evaluated for a grown mob of the default variant: every query and
   variable nought. A controller listed with a condition is used only if
   the condition holds of such a mob.
2. The model is read from `models/entity/*.json` and `models/mobs.json`,
   in either of their two layouts, with what it inherits put under it
   (`geometry.zombie.husk:geometry.zombie`).
3. The head is the bone called `head`, or failing that one of a few other
   spellings, or the body; where that bone is empty, the box is looked for
   on the bones hung off it. Its largest box with some thickness is the
   face (a flat sheet is a frill or a branch), and the front of that box
   is found on the texture the way a box unfolds: for a box of
   whole-number size `x, y, z` wrapped from `u, v`, the rectangle at
   `u+z, v+z` of `x` by `y`; or where the box says so face by face, the
   rectangle it gives. A mirrored box is drawn turned left to right. A
   head set at an angle is still that rectangle.
4. Every other box on the head and on the bones that hang off it is drawn
   in its place, nearest last: a snout, horns, a hat, a villager's nose.
   A box turned more than 15 degrees out of square is left out, since its
   face is no rectangle of the texture; so is a bone the model never
   draws, one the controller's `part_visibility` hides on a default mob
   (a horse's saddle and bridle), and anything further from the head than
   half its size. Each controller's textures are laid over the last,
   which is how a stray gets its clothes and a villager its profession.
5. The result comes out square, one pixel to each pixel of the texture and
   never blended, with the head in the middle of whatever room its shape
   leaves. It is not enlarged here: the page enlarges it by a whole number
   of screen pixels, which a size fixed on the server could not suit.

**Every face is made for the same box.** The page fits a face into a
square box (16 pixels at the usual size): the most whole screen pixels to
each of its own that fit, or the box itself where that would leave it
under three quarters of it, centred, the rest left clear; one larger
than the box is blended down to it. So that this gives faces of a like size,
a face whose ears or horns make it wider or taller than 16 is made again
as the head alone; one under 4 pixels along its shorter side, or more
than two and a half times as long as it is tall, is a strip that would
be a speck, and is not made.

A face that comes out blank, nearly all one colour, or such a strip is
rejected, and the mob keeps its spawn egg. So does any mob the table of
overrides says to leave alone. That table (`internal/icons/overrides.go`)
is data, one line a mob with its reason, for the mobs whose face is not
the front of a box called head. At the default pin 51 faces are made with
no override, 24 with one, and 17 mobs keep their egg:

| Mobs | What is done | Why |
|---|---|---|
| ghast, happy ghast | The body alone | No head; the body is the face, and the tentacles hang far below |
| slime, magma cube, sulfur cube | The core, the eight slices, the cube | No head |
| sheep | The sheared model with the woolly one over it | The head is two boxes in two models: the bare face, and the wool round it |
| wither | Its usual skin, its middle head | Its controller picks the pale skin of one just summoned |
| shulker | The shell | Its head is seen only when it opens |
| creeper | Without its second controller | That one draws the charge of a creeper struck by lightning |
| copper golem | Its own model and texture | The first of its controllers draws the flower it sometimes holds |
| guardian, elder guardian | Without the spikes | They would leave the eye a speck among them |
| pufferfish | Its largest form | The smallest is three pixels across |
| llama, trader llama | The top 8 of the head's box | Head and neck are one box 18 tall |
| camel, camel husk | The third box of the head bone | The two larger are its neck |
| ravager | The top 16 of its head, alone | The head is 16 by 20 with the jaw |
| frog | The lip with the two eyes above it | The eyes are on a bone of their own |
| bat | The head with its ears | The head alone is four pixels wide |
| dolphin, turtle | The head from the side | The front is a blank; the eye and the beak are on the side |
| nautilus, zombie nautilus | The shell from the side | It is a spiral only from there |
| horse, donkey, mule, skeleton horse, zombie horse | Spawn egg | A horse is known in profile, and its profile is its neck, a bone set 30 degrees off square that a flat view cannot draw; the head alone is a bar with an eye |
| hoglin, zoglin | Spawn egg | The head is a long box hung at a slant: its front is a brow six pixels tall, and its top is not a face |
| sniffer | Spawn egg | Its face is behind a beak that fills the front of its head |
| phantom, armadillo | Spawn egg | A head seven pixels by three, and three by five |
| parrot, tadpole | Spawn egg | Two and three pixels wide |
| cod, salmon, tropical fish, silverfish, endermite | Spawn egg | A fish is its side, which is a line at a marker's size; the tropical fish's colours are laid on by the game; the two arthropods are rows of segments with no face |

Goat, allay and creaking need no override: a goat's head is set at an
angle, an allay's hangs off an empty bone, and a creaking's largest
"box" is a flat sheet of branches, all of which the steps above allow
for.

Some of the model textures are TGA files, which Go's standard
library does not read, so the service has a reader of its own for the
plain and run-length-encoded true-colour kinds the samples use. The fourth
channel of such a file is whatever its material makes of it (which parts
glow, which take a dye), so a mob's own skin is drawn solid.

**A block** is drawn two pixels across to one down, each side's pixels
moved and none blended, with the top at 98% brightness, the left side at
80% and the right at 60.8%, which is how the Minecraft Wiki's block
renders are lit. A 16 pixel block comes out 32 by 32. The flat pictures
above are kept, and are what the page draws where a block's is not there.

**What the map knows of a mob** decides which variants are made. A live
mob is a type and a name and nothing more, and a named mob's mark adds
only whether it is a baby, so a sheep is white, a cat the first of its
coats and a cow the temperate one: the game's default for each. A
structure's sheet does say each villager's profession, so those 14 are
made. A baby is its grown face drawn smaller.

**Everything read is held to bounds before it is used.** A model file is
at most 256 KB and 24 levels deep, a model 512 bones and 1,024 boxes, and
every number in it within 1,024 of nought. A definition is drawn by its
first 16 render controllers, and a face is at most 64 layers, counted as
they are gathered. A texture is at most 512 KB and 512 pixels a side,
checked from its header before a pixel is decoded, and a whole multiple,
at most 4, of the size its model says; a rectangle that does not lie
inside its texture is refused. There are at most 600 model files, 600
controller files and 600 textures to a fetch, and 40 MB in all.

A render controller's expressions are a small language, and an expression
can name an array whose entries are expressions that name arrays. So the
reading of them is counted: an expression is at most 1,024 characters,
24 levels and 4,000 steps, a file 64 controllers of 64 arrays and
200,000 steps in all, and each entry of an array is worked out once
however often it is named. A file that runs past any of these is given
up on, its mobs fall back to what their definitions call default, and it
is not read again. Without the count a controller file of 4 KB took 15
seconds to read and one of 6 KB would have taken days; with it each takes
under a millisecond.

Textures are decoded one picture at a time and let go once those held
come to a million pixels, and a fetch decodes at most 32 million in all,
so what is held is a few megabytes however many textures the pin has.
One real fetch at the default pin holds 14.5 MB of heap at its most.

One mob's model or texture being wrong costs that mob its face and
nothing else, and that holds for a fault in this service's own reading of
them too: one is caught, counted in `mcmap_icons_faults_total` and logged
in a single line saying which picture it cost. A fault in the making as a
whole leaves every made picture unmade, the icons and the rest as they
were, and is not tried again until the pin changes.

**The listing, and what happens when it cannot be had.** Everything
starts from a listing, and the host rations those by address, sixty an
hour:

- The whole of `resource_pack` is asked for in one request, with two
  minutes to arrive where a file has fifteen seconds. GitHub's
  [documentation](https://docs.github.com/en/rest/git/trees) cuts such a
  tree short at 100,000 entries or 7 MB; at the default pin it is 18,714
  entries and 5.5 MB, so there is room for five times the files but only
  a quarter more bytes.
- **If a later pin's tree is cut short**, the entity definitions alone
  are listed, as they were before any picture was made from a model
  (that listing is one directory, 180 files of the 1,000 it allows). The
  spawn eggs, the names, the pictures served as they come, the blocks and
  the structures' items are fetched as ever. Only the faces go without:
  every mob is its egg, the log says once `mob faces are not made at this
  pin`, with why, and nothing is asked for again on that account. The
  page works as it does with **Mob picture** set to spawn eggs.
- **Each asking is written to the volume** (`DATA_DIR/icons/listing.json`)
  with when the next may be made: ten minutes after an answer; after a
  failure one minute, then doubling to an hour, or when the host's own
  `X-RateLimit-Reset` or `Retry-After` says, up to two hours. A fetch
  that was given its listing and then failed on a file uses that listing
  again when it is retried a minute later, at no request. A start
  that comes before then has none and does not ask, so a service restarting every few
  seconds makes at most 7 listing requests in an hour, where it could
  have made one at every start.

**Each is kept with its recipe**: which rectangles of which textures go
where. The recipes are in the index on the volume, so a picture whose
texture could not be fetched is made later from the texture alone.

None of this can cost the mob icons. Where a picture or the language file
cannot be had, that one thing is left out, everything else is served, and
it is asked for again by itself, with no listing request. The one
exception is a model or controller file that could not be fetched: no
recipe is worked out from half the models, so every made picture waits,
and the next asking reads the listing, the definitions and the models
again, but only once the rest of the source has answered.

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
pictures and every name is a tidied id. Likewise a volume filled before
any picture was made, or under another revision of the making
(`icons.ArtRevision`, raised when the same samples would give different
pictures), keeps serving everything it holds, a structure's old item
included, while the made pictures are made and put beside or over them.

**Rolling back to the release before made pictures.** The index on the
volume has the same format, with fields that release does not read. It
reads an index of up to 200 pictures; at the default pin there are 173,
so it takes the volume as it is, serves every picture in it (a structure
keeps the face it was given; the faces and blocks are simply never asked
for by its page) and fetches nothing. At a pin with more than 200 it
would refuse the index, fetch its own smaller set over it, and a later
roll forward would find no made pictures and make them again, serving
what is there meanwhile. Neither direction loses a picture that the
running release draws. A set over the limits this release reads back
(800 pictures, 600 recipes) is never written, since it would be refused
and fetched again at every start; it is served from memory and said in
the log.

#### Where the pictures come from, and what is done with them

This is the posture the service takes, not legal advice. Mojang's samples
carry no open licence: "(c) Mojang AB. All rights reserved", subject to
the Minecraft EULA, which lets a tool be built round the game and does not
let Mojang's content be handed on. So:

- **Nothing of Mojang's is in this repository or in the image**: no
  texture, no model, no crop of one. That includes the tests, which paint
  their own textures and write their own models in code.
- **Everything is fetched at runtime** from Mojang's public repository by
  the instance that will use it, at a pin its operator chose, and kept
  only on that instance's volume.
- **A crop or a render is treated exactly as the texture it came from.**
  A mob's face cut from its texture and a chest drawn from its three sides
  are still Mojang's, so they are made on the instance, kept on the same
  volume, and never committed or published.
- **They are served only to logged-in players**: `/api/icons` and every
  picture under it need a session, are `Cache-Control: private`, and are
  not served at all with `ICONS_ENABLED=false`, which draws the whole map
  in dots, rings and letters of its own.

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
each carries (`i`), never by position. Where the snapshot has an id nothing
else is asked, so a loaded mob of the same name under another id is another
animal and both are shown. Only a mob the world saved without an id is
looked for by name and type, and only while exactly one is saved and
exactly one is loaded under them; two that share a name are never taken
for each other.

The snapshot's mark is a saved position, not a mob that is there: it is as
old as the snapshot, and the mob may have walked off, been renamed or died
since. So it is drawn faded, in a broken ring, where a live marker is
solid, and its tooltip says `saved 4 min ago`. It is what is on the map
when the mob is not loaded, when it is in another dimension, when the live
layer's row or type filter for it is switched off, when the game's cap on
reported mobs left it out, and while there is no recent live picture. A mob
that died after the snapshot keeps its saved mark until the next one. The
row's count is of the mobs the snapshot holds, each once, loaded or not,
and each is an item under the row that says whether it is loaded or only
saved, under the name it has now, and can be hidden by itself. With Named
mobs off neither marker has a label. A click or a tap on the marker or on
its label, or Go to on its item (Enter, with the focus on it), opens the
card described under Inspecting and
following: on a loaded mob it tracks it live and Follow works; on one that
is not loaded it shows where the snapshot left it, says that this is its
last saved position and how old, and offers Go to. Go to on the item also
takes the map there.

The markers are stamped onto the live layer's canvas from sprites made
once per picture, so that they and the live markers can both be hovered
and none is an element or a request of its own. Measured in headless
Chromium at 1300 by 800 with 1,500 beds, 600 containers and 905 live mobs
all in view, dragging the map: 244 frames, median 16.7 ms, longest
16.8 ms, no long task; one whole repaint of the canvas, flushed, took a
median 4.0 ms.

Leaflet does not draw a canvas again while a zoom is animated, or through
a pinch: it stretches the one it has, so everything on it would swell to
twice or four times its size and drop back when the zoom ended, while a
structure's mark, which is an element, only moved. For as long as the
canvas is stretched it is drawn again each frame instead, every marker
smaller or larger about its own point by as much as the canvas is
stretched the other way, so a picture, a dot, a ring and a name are on
the screen at the size chosen all the way through. With the same 3,017
markers on the canvas, zooming in and out by the wheel: 252 frames,
median 16.7 ms, longest 16.8 ms, no long task; with large pictures at
the largest size, a median 16.7 ms and a longest 33.4 ms. A marker is
stamped unblended while the canvas is stretched, which is what keeps
those frames short: blended, the same zooms had frames of 100 ms.

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
is on it. Until a picture is there its place is taken by a letter of the
kind's own, as F for a fortress or M for a monument. A kind this page has
no letter for is still named, by its id made into words, and listed if
the server lists it. Most kinds are known another way, [by their
blocks](#found-by-their-blocks): the box is dashed, and the tooltip says
`found by its blocks` where another says `recorded by the world`. A site
one of the world's own explorer maps points at says so in place of
`predicted from the seed`.

The panel has a row for each layer, Known, Predicted and Possible, and one
for each kind that filters all three. A kind's row counts everything of the
kind and says under it how that is made up (11 known, 2 predicted, 498
possible), or why the kind is not predicted. Possible starts as the
viewer's Predicted row is set. Every tooltip says which of the three a mark
is, and the details say why.

**The kinds listed are the dimension's own.** A fortress is no row in the
Overworld and a village none in the Nether: the list holds the kinds the
game generates in the dimension on screen, each with how many of it that
dimension has, and is drawn again on the way to another. Which dimensions
a kind can be in comes with every `/api/structures` answer as `catalog`,
with whether it is off until asked for, so the page lists what the server
knows and supposes nothing of its own; a server from before `catalog`
gets the seven kinds there were then, in every dimension, as it always
did. A kind's switch is one choice wherever it is listed. Hiding the
villages, going to the End and coming back leaves them hidden; a kind
that is no row here is not counted among what is hidden here, so the
panel's "N of M shown" is about the lines on screen, while its Reset
puts every kind back as it first was, those of the other dimensions with
the rest; a saved view or a link that names a kind this dimension has none of shows
no line for it and is as it says once the map is somewhere that has one;
and a choice made of a kind that is only ever listed in another dimension
is kept, where an id in no list for a month would otherwise be dropped as
gone. A search still finds structures in every dimension, and says which
each is in.

**Details.** A click or a tap on a structure's mark, Enter on it while it
has the focus, or choosing a structure in the search, opens a sheet over
the page with everything the page knows of it, each as a labelled line of
text. The tooltip is left short: what it is, a village's first line of
counts, and `Click for details`. The sheet says:

- its kind and picture, and whether it is recorded, predicted or only a
  possible site, with a sentence saying what that means; for a site the
  seed gives, also whether the terrain there is generated and how the
  kind's rule fared against this world (`agree` and `disagree` from
  `kinds`);
- its dimension and its centre; for a recorded one the blocks it covers on
  each axis with their length, and its chunks; for a predicted one the
  block and chunk the seed puts it at, since it has no box;
- how far and which way it is from the middle of the map and, while the
  logged-in player is in the live picture, from them;
- for a village, every count the world keeps, or that the game has not
  counted it yet; for another kind, how many recorded spawn areas were
  joined into its box (`areas`);
- for a recorded one, [what the save holds in it](#what-a-structure-holds),
  asked of `/api/structures/detail` when the sheet opens and put in when
  it comes, under a heading for what only its kind has and one for what
  every kind has:
  - a village: its grown villagers by profession and trade level, its
    babies, the villagers it lists that the save holds no record of, how
    many of its golems and cats the save still holds, its claimed job
    sites by the block each is, how long before the snapshot the game last
    ran it, how far its raid got if it has had one, how many players it
    has met, and what it thinks of the player looking;
  - a monument: how many of its three elder guardians, and how many
    guardians, are in the save inside its box;
  - an outpost: its pillagers and how many are captains, and the allays
    and iron golems in its box;
  - a witch hut: its witch, its cat and its cauldron;
  - a fortress: its blazes, wither skeletons and blaze spawners;
  - a stronghold: whether its end portal is lit, and its silverfish
    spawner; a trial chamber: its trial spawners by mob and its vaults,
    ominous ones apart;
  - every kind: the saved mobs in its box by type, each with the game's
    picture and name, the named ones by name, its spawners by mob and
    where each is, its chests, barrels and shulker boxes by whether each
    is not yet opened, has something in it or is empty, and its unbroken
    decorated pots, dispensers and droppers, each as a count of its own;
  - and how old that is: `as of the last snapshot, 4 min ago`.

  A fact the save does not hold is said so (`Not recorded by the game`,
  `None in the save`, `No raid record`), never shown as nought. A site the
  seed gives has none of this, and the sheet says that only a kind and a
  place can be said of it;
- the biome at its middle, asked of `/api/biomes/at` while the service has
  biomes, and how many of its chunks are slime chunks, in the Overworld;
- for a recorded one, what the map already holds inside its box, counted
  in the browser from the layers loaded: beds by colour, containers by
  kind, named mobs by name, and the players and mobs in the last live
  frame. A named mob and a player are buttons that close the sheet and
  open the card about them. The sheet says that this is only what the map
  was sent.

Go to fits a recorded structure's box in the view, or centres on a
predicted site; Copy coordinates copies the centre; Copy link copies an
address that reopens the sheet. One chosen in the search before its
dimension's structures have arrived opens when they do, within eight
seconds; if they fail to arrive, or the map is taken to another dimension
first, it is dropped and the page says so, rather than open later over
something else. The address names an open sheet in a fifth
part after the view, `structure~<kind>~<r or p>~<x>~<z>`, which the page
keeps while the sheet is open and drops when it shuts. The sheet is the
browser's modal dialog: the focus stays in it, Escape, Close and a click
outside shut it, and the focus goes back to the mark.

A village's records still hold more than is served: where each claimed
bed, bell and job site is and which villager claimed which, the box its
raid is fought in, and three more ticks in `INFO` whose meaning was not
worked out. Nothing is made of those.

**Known** structures are the ones this world has generated, read from its
own save. For each chunk the server keeps the boxes in which a structure's
own mobs spawn, as that chunk's record 57: a 32-bit count, then per box six
32-bit block coordinates (minimum x, y, z, then maximum, inclusive) and one
byte for the kind, all little-endian. The kinds are 1 nether fortress,
2 witch hut, 3 ocean monument and 5 pillager outpost; nothing else leaves
such a record, and the current game no longer writes it for a chunk it
generates.

A newer game keeps a second record, 119 of the chunk, which is read too:
the boxes of every structure piece that reaches into the chunk, under the
structure's own name. Little-endian throughout, it is a 32-bit version
(1); a count of names and for each a 32-bit handle, a 16-bit length and
the name; a count of boxes and for each a handle and six 32-bit block
coordinates; then two lists of entries, each a count followed by a box's
handle, a name's handle and a 32-bit flag, 1 on the box round the whole
structure and 0 on a piece of it. Names and boxes are numbered in one
run. The first list is for the kinds the game builds from data files and
the second, whose entries carry one more 32-bit number before the flag,
for the older ones. From it come five kinds no other record has, `igloo`,
`desert_pyramid`, `jungle_temple`, `trail_ruins` and `abandoned_camp`
(which carries `variant`, the biome the camp was built for, as the record
names it), and the fortresses, monuments, outposts and swamp huts of
chunks too new to have record 57. An older kind is kept as its pieces,
which stand at the height it was built at, where the box round it stands
at the height it was first given; a data-file kind as the box round it.
A record that is not laid out so is refused whole and counted; a box of a
kind this version does not know, or one outside its own chunk, is left
out and counted; the empty box the game writes for a piece that builds
nothing is passed over.

Villages are kept another way and are read too, as below, and strongholds
and trial chambers are [found by their blocks](#found-by-their-blocks).
A box is cut at the chunk's edge, so the boxes of a kind that touch are
joined back into one structure (an outpost's within 48 blocks, since its
tents and cages stand apart from its tower). A fortress is recorded room
by room, and one the world has only part generated is rooms with the
country between them missing: its boxes within 96 blocks of each other
are one fortress, which is what the list counts and what its line in the
panel says. On the FWB world that is 13 fortresses, where boxes joined
only at 32 blocks came to 18 parts of them.

**Villages** are a fifth known kind, `village`, read from the records the
game keeps for each village it runs. They are outside any chunk, under
`VILLAGE_<dimension>_<id>_` and one of five endings, each an unnamed NBT
compound:

| Record | Holds | Read |
|---|---|---|
| `INFO` | The village's box, `X0` `Y0` `Z0` to `X1` `Y1` `Z1`, `Initialized`, and `Tick`, the game tick it was last run at | Yes |
| `DWELLERS` | `Dwellers`: four lists of `actors`; the first is villagers, the second iron golems, the fourth cats. Each actor is an `ID`, the `UniqueID` of the mob's own record | The counts, and up to 512 ids of each for the details |
| `POI` | `POI`: per villager, the `instances` it has claimed, each a `Type` (0 bed, 1 bell, 2 job site) at `X` `Y` `Z`; a job site's `Name` is the profession it gives | The counts, and job sites by profession |
| `PLAYERS` | `Players`: an `ID` and `S` for each player the village has met, the player's own `UniqueID` and the village's standing for them, a whole number that starts at nought | For the details; a standing is sent only to the player it is of |
| `RAID` | `Raid`: `GroupNum` of `NumGroups` waves, `NumRaiders`, and `GameTick`. Only a village that has had a raid has one, and the game does not take it away when the raid is over | For the details |

`PLAYERS` and `RAID` decorate a village and never decide whether there is
one: one that does not parse is counted as skipped and the village stands
without it.

`<dimension>` is `Overworld`, `Nether` or `TheEnd`. Only `Overworld` has
been seen in a real world; the other two are the names the game gives its
own per-dimension records. A village under any other name, or under none
as older versions of the game wrote it, is passed over, and counted by its
`INFO` record. A village
is drawn with the box the game recorded and carries how many villagers,
golems and cats it lists and how many beds, bells and job sites its
villagers have claimed, each block counted once. Nothing in the list every
viewer is sent names a player or says anything of one.

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

### Every structure the game generates

An audit of what Bedrock 1.26.50 generates, in each dimension, against
what this map can say of it, made on 2026-10-10 from the game's own
files at the icons' pin, the Minecraft Wiki's pages for each structure,
and the FWB world's save (the snapshot of 2026-10-05, game 1.26.52.3).
Every rule below was treated as a guess and set beside that world; the
figures are counts of what it holds and never where anything is.

**What the save turned out to hold.** Four things beyond the spawn areas
and the village records, none of which the map read before:

- *Record 119 of a chunk*, which the level format names `AABBVolumes` and
  describes nowhere. It is the boxes of every structure piece that
  reaches into the chunk, under the structure's own name: a version, the
  names, the boxes, and two lists saying which box belongs to which
  structure and whether it is the box round the whole of it or one piece.
  The game writes it for the kinds it builds from data files (trial
  chambers, trail ruins, abandoned camps) and for igloos, both pyramids
  and swamp huts, beside the fortresses, monuments and outposts record 57
  has. 7,568 chunks carry one; all parse, with nothing left over. It is
  not written for a village, stronghold, mineshaft, shipwreck, ocean
  ruin, ruined portal, buried treasure, bastion, ancient city or end
  city, and the game did not write it at all when the older half of this
  world was generated: 214 of the 243 groups of trial spawners lie
  inside a recorded chamber, 3 of the 7 desert pyramids known by their
  loot, 4 of 6 jungle temples, and all 33 abandoned camps with a chest
  still shut.
  Chunks the current version generates carry record 119 and no record
  57, so every monument, outpost and fortress generated since the world
  was updated is in this record only.
- *Which seed each chunk was generated from.* Record 63 of a chunk is a
  hash into `LevelChunkMetaDataDictionary`, whose entry holds the
  `GenerationSeed` and the game versions the chunk was first and last
  written by. This world has **two** seeds: 80,961 chunks generated by
  versions 1.20.41 to 1.21.31 from one, and 63,202 generated by 1.26.43
  to 1.26.52 from the seed now in `level.dat`; 4,619 chunks carry no such
  record. That is why the seed in `level.dat` does not place the
  structures the older chunks recorded, and it means a site has to be
  worked out from the seed of the chunk it falls in, and from
  `level.dat`'s for a chunk not generated yet.
- *Where the game's own explorer maps point.* Each `map_` record holds
  its `decorations`, and one for a structure carries the block the game
  itself worked the structure out to be at, generated or not. Of 55,870
  maps, 11,158 carry one, to 56 places: 15 buried treasures, 10
  monuments, 10 trial chambers, 8 of a type that is most likely the
  abandoned camp's, 7 villages, 3 woodland mansions and a jungle temple.
- *The End's own record*, the key `TheEnd`: whether the dragon has been
  killed, where the exit portal is, and which gateways are still to
  come. The key `portals` is the portals players have linked, which are
  not generated structures.

There is no record of structure starts as Java keeps them, and no
`JigsawStructureBlueprint` record (120) in this world. Beyond the above
a structure is known only by the blocks it leaves: 54 loot tables are
still named on 32,585 chests, barrels, pots, dispensers and suspicious
blocks nobody has opened, and a loot table names its structure exactly.

**Abandoned camps** are what the game calls its campsites: a wool tent,
an unlit campfire and chests or barrels, in eighteen surface biomes,
added to Bedrock in 1.26.40 and to this world when it was updated. Each
is recorded in record 119 under its biome's name, 42 of them in 13
variants here. Nothing else was added to Bedrock between 1.21.0 and
1.26.50 but trial chambers; sulfur caves and fallen trees are terrain.

**Each structure.** "On a site" is the share of the world's own of the
kind, recorded or found, that a site of the rule explains; "sites holding
one" is the share of the rule's sites, in finished chunks of a biome that
suits, that hold one. Both are as the survey itself counts them, over
chunks that name their seed, with each site worked out from its own
chunk's seed.

| Structure | In | The save holds | Rule (region, offset below, draws) | On a site | Sites holding one | Spoiler | Shown as |
|---|---|---|---|---|---|---|---|
| Village, zombie village, biome variants | Overworld | Village records, once a player has been near | 34, 26, two averaged | 56 of 67 | 48 of 57 | Low | Recorded (70) and predicted |
| Pillager outpost | Overworld | Records 57 and 119 | 80, 56, two averaged | 19 of 21 | 17 of 17 | Low | Recorded (22) and predicted |
| Ocean monument | Overworld | Records 57 and 119 | 32, 27, two averaged | 15 of 16 | 15 of 16 | Low | Recorded (20) and predicted |
| Swamp hut | Overworld | Records 57 and 119 | 32, 24, one | 2 of 2 | 2 of 2 | Low | Recorded (3); predicted once three are on a site |
| Desert pyramid | Overworld | Record 119; chests and suspicious sand until touched | The hut's sites, in desert | 6 of 6 | 6 of 6 | Medium | Recorded (3), found by its loot (4), predicted in finished chunks |
| Jungle temple | Overworld | Record 119; chests and trap dispensers | The hut's sites, in jungle | 5 of 5 | 5 of 9 | Medium | Recorded (4), found (2), predicted in finished chunks |
| Igloo, with or without basement | Overworld | Record 119; the basement's chest | The hut's sites, in snowy plains, snowy taiga and slopes | 11 of 11 | 9 of 11 | Low | Recorded (9), found (3), predicted in finished chunks |
| Abandoned camp | Overworld | Record 119, by biome | The wiki gives a 37-chunk grid; no number for it is published | Not checked | Not checked | Low | Recorded (42), with the biome each was built for |
| Trail ruins | Overworld | Record 119; suspicious gravel until brushed | 34, 26, one, by Java's generator and the whole seed | 22 of 37; 6 of 24 in the older seed's chunks, where it is set aside | 11 of 16 | Low | Recorded (26), found (17), predicted in finished chunks |
| Trial chambers | Overworld | Record 119; trial spawners and vaults | 34, 22, one, by Java's generator and the whole seed | 133 of 149 | 123 of 171; nine in ten in chunks the current game made | Low | Found by blocks (219); predicted where not generated |
| Woodland mansion | Overworld | Chests until opened; explorer maps | 80, 60, two averaged, in dark forest and pale garden | 3 of 3 map targets | None of the finished sites is in its biome | High | None built; the one site a map points at, off until asked for |
| Ancient city | Overworld | Chests until opened; sculk, which the deep dark has without a city | 24, 16, one: not told from chance | 15 of 19 within 64 blocks, where chance is about half | 12 of 205 | High | Found by its chests (20), off until asked for; not predicted |
| Stronghold | Overworld | Portal blocks, the portal room's spawner | Not tried | - | - | Highest | Found by blocks (4), off until asked for |
| Buried treasure | Overworld | The chest, at block 8, 8 of its chunk, until opened; treasure maps | 4, 2, two averaged, on beaches | 446 of 446 | 444 of 501 | High | Withheld unless the owner says otherwise; then found by its chest (525), with 57 sites with nothing found |
| Shipwreck | Overworld | Chests until opened | 24, 20, one, in oceans and on beaches | 37 of 37 | 29 of 75 | Medium | Found by its chests (45); not predicted |
| Ocean ruins, warm and cold | Overworld | Chests, suspicious sand and gravel | 20, 12, one, in oceans | 140 of 142 | 137 of 144 | Low | Found by its loot (169), predicted in finished chunks |
| Ruined portal | Overworld | The chest until opened | 40, 25, one | 144 of 144 | 83 of 101 | Low | Found by its chest (177), predicted in finished chunks |
| Mineshaft, and the badlands one | Overworld | Cave spider spawners (3,192), chest minecarts (6,319) | Per chunk, not a grid; not tried | - | - | Low | Left out: about 640, and one with no spider corridor leaves nothing |
| Dungeon | Overworld | Its spawner and chests (4,910 rooms) | None: scattered | - | - | Low | Left out: thousands |
| Desert well | Overworld | Suspicious sand until brushed (14) | None: scattered | - | - | Low | Left out as too slight; it could be found by its sand |
| Fossil, amethyst geode | Overworld | Nothing but plain blocks | None: scattered | - | - | Low | Cannot be shown without reading every block |
| Nether fortress | Nether | Records 57 and 119 | 30, 26, one, and a third draw: 2 in 6 | 8 of 10 | 6 of 6 | Low | Recorded (13) and predicted |
| Bastion remnant | Nether | Chests until opened, a magma cube spawner in one type of four | The fortress's sites: the other 4 in 6, not in basalt deltas | 17 of 19 | 14 of 15; none of 4 in basalt deltas | Medium | Found by its loot (21), predicted |
| Ruined portal | Nether | The chest until opened | 25, 15, one | 37 of 37 | 34 of 41 | Low | Found by its chest (42), predicted in finished chunks |
| Nether fossil | Nether | Nothing but plain blocks | None | - | - | Low | Cannot be shown |
| End city, with or without ship | The End | Chests until opened, shulkers, the ship's dragon head and framed elytra | 20, 9, two averaged | 19 of 19 | 20 of 23 | High | Found by what is left in it (21), predicted, off until asked for |
| End gateway | The End | Its block, which nothing breaks | None: made by the dragon's death and by use | - | - | Low | Found by its block (9) |
| Exit portal | The End | Its portal blocks once lit; the End's own record | Fixed at the middle | - | - | None | Found by its blocks |
| Obsidian pillars | The End | Nothing | Fixed: the same ten in every world | - | - | None | Not shown: the render already draws them |

What the figures leave open:

- *The seed of a chunk with no record 63* is not known. What is in one is
  shown, and counted on neither side of any rule.
- *A structure is of the seed of its own chunks, not of the chunk it
  starts in.* A first reading of the ruined portals had 62 chests of 143
  at no site. Each was at a site whose own chunk is not in the save at
  all: the portal stands in the chunk next to it. Set beside the sites of
  its own chunks' seed, wherever those sites fall, every chest is at one.
- *Trial chambers and trail ruins* are placed by Java's generator, seeded
  with all 64 bits of the world seed, where every older kind uses the
  game's own generator and the low 32: under the old rule neither scores
  above a wrong seed. Chunks older than a structure hold none of it,
  which is all the old chunks' lower shares say of trial chambers; the
  older trail ruins were placed some other way, which was not found.
- *Abandoned camps* are placed the same way, the wiki says, on a grid of
  37 chunks, and the number that seeds it is published nowhere. They are
  recorded, every one, and not predicted.
- *Ancient cities.* A city is some 200 blocks across and known by chests
  all over it, so a site of nearly any rule is within reach of one about
  half the time, and the published rule's 15 of 19 does not settle it.
  What does settle the use of it is the other share: 12 finished sites in
  205 hold a city, because a city needs the deep dark, which is a biome
  under the ground that the map's biomes do not show. A site would be
  wrong nineteen times in twenty. Cities are known by their chests only:
  385 not yet opened, in 20 cities.
- *Shipwrecks* are where the rule says, every one, and three sites in
  five in finished water hold no unopened chest. Nothing tells a wreck
  that was looted from one that was never built, so a site is not shown.
- *Bastions and fortresses* share their sites. No bastion's chest is at a
  fortress's site, and none of the four bastion sites in basalt deltas
  holds any.
- *Woodland mansions.* None is generated in this world. The rule is
  borne out by the game itself: each of the three places its explorer
  maps send a player to is a site, and no site of a wrong seed is.

### What a structure holds

A structure's record says where it is. What is in it is in the records of
the things themselves, and the survey reads those in the pass it already
makes over every key, with no second pass:

- **Saved mobs.** Every mob the game has saved is one record under
  `actorprefix`, and a chunk's `digp` record lists which are in it, which
  is the only place a mob's dimension is written. A record is a mob if it
  has `HurtTime`, which no item, arrow or boat has; one without the word
  is passed over unread. Kept of each: its type, the block it stands in,
  whether it is a baby or a raid captain, its name tag, and for a villager
  `PreferredProfession` and `TradeTier`. A mob no chunk lists, a dead one
  and an armour stand are not counted.
- **Block entities.** Each chunk's record `0x31` is its block entities one
  after another. Kept: chests, barrels and shulker boxes, with whether
  each still carries the `LootTable` it was generated with (the game
  rolls it and drops the tag the first time the container is opened), has
  `Items` or has none; spawners and trial spawners with the mob each
  spawns; vaults, and whether their loot table is the ominous one;
  cauldrons, bells and end portal blocks. What a container holds is never
  read, and a loot table's name is never sent: the answer says
  `unopened`, not what is inside.

  A decorated pot, a dispenser and a dropper hold things as well, and are
  not lumped with those. A pot the generator placed carries a loot table
  until it is broken, so it is counted as an unbroken pot and a pot
  without one is not counted; nobody opens a pot. A dispenser's or a
  dropper's load says nothing of who has been by, so each is a plain
  count. In a trial chamber these are most of what carries a loot table:
  the FWB world's 219 chambers hold 7,264 unbroken pots and 4,500
  dispensers beside 747 chests and 1,210 barrels not yet opened.

Once the boxes are known, each mob and block entity is set inside the
recorded structures whose box it is in, height included. The two kinds
found by their blocks have no box of their own, only the box round what
they were found by, so they are counted differently and the answer says
how:

- A trial chamber's rooms run on past its last spawner. Its contents are
  counted 24 blocks past that box each way and 12 up and down, the answer
  carries `reach: 24`, and the sheet's heading and every `None` say
  `within 24 blocks of what it was found by`.
- A stronghold is found by one room of a structure hundreds of blocks
  across. Nothing says where the rest is, so nothing else is counted: the
  answer carries `uncounted: true`, whether the portal is lit and whether
  the silverfish spawner is still there, and no list of mobs or
  containers, which would read as a stronghold with none. Where the
  spawner stands is not sent either. That is what
`/api/structures/detail` answers with. A village's own records add to it:
each villager it lists is looked up by `UniqueID` among the saved mobs,
which is where its profession and level come from, and a villager with no
`PreferredProfession` is counted as having none, the unemployed and the
nitwits together since the record does not tell them apart.

All of it is as old as the snapshot, and the sheet says so. The game saves
a mob with the chunk it stood in when that was last written, so a mob that
has since moved, died or despawned is still counted until the next
snapshot, and one that was not in a saved chunk is not counted at all.
`None in the save` means that and no more.

**A standing is the player's own.** What a village thinks of a player is
in `PLAYERS` under the player's `UniqueID`, which is not their XUID. The
session holds an XUID, as it does for waypoints, and nothing in a request
can name another player. The two are joined where both are seen at once:
the agent reports the gamertag each XUID is online under, and the live
layer the id the game gives the player of that gamertag, which is the
`UniqueID`. A gamertag two online players hold joins nobody.

That id is held in memory, and held to three rules, because an id means
something in one world only:

- It is believed for 30 minutes after the live layer last bore it out,
  and for as long as it goes on doing so.
- It is forgotten, with every other, when a survey is of another world or
  of an earlier copy of the same one: another seed, a game tick lower than
  the survey before, or a `level.dat` that could not be read. In a world
  put back from a backup, an id handed out since may be somebody else's.
- It is used only against a snapshot taken after the player was first
  seen. A snapshot from before may be of the world before this one, which
  nothing can tell until the next is read.

The answer carries `standing` with `state`: `known` and the `value`;
`none` where the village has never met the player; `pending` where the
player has been seen only since the snapshot, which lasts until the next
one; or `unknown` where the map cannot say which record is theirs. With no
login it is always `unknown`. No other player's standing or id is in any
answer: a village says only how many players it has met. What the number
means beyond higher being better is the game's business, and the page says
only that it starts at nought.

What was looked for and is not there:

- **A raid in progress.** The `RAID` record outlives the raid: the one in
  the FWB world was last run hundreds of millions of ticks before its
  village was. So the sheet says how far a raid got and how long ago, and
  not that one is on.
  `State` and `Status` in it were not worked out from one example.
- **Hero of the Village** is an effect on a player, in the player's own
  record, which the map does not read.
- **A village's centre and radius.** `INFO` holds the box and nothing
  else of its shape.
- **Sponge rooms and a monument's gold core** are plain blocks. Finding
  them means decoding the block palettes of every slice of every
  monument, which is not cheap, so it is left out.
- **Pillagers at an outpost.** None of the seven outposts in the FWB
  world has a saved mob in its box: pillagers despawn, and the thirteen
  allays the world holds are all somewhere else. The sheet has the rows
  and says `None in the save`.

Measured on the FWB world on 2026-10-07: 2,892 saved mobs and 45,557 block
entities kept, none skipped. All 11 monuments hold saved mobs, 8 with
three elder guardians, 1 with two and 2 with one. 4 of 11 fortress parts
hold blaze spawners, 7 between them, and 5 hold chests, 9 of them not yet
opened. 68 of 70 villages hold saved mobs and 57 hold containers: 113
chests not yet opened, 198 chests, 2 barrels and 10 shulker boxes with
something in them, 43 chests and 27 barrels empty. Of the 55 counted villages 40 have villagers with a profession:
524 grown villagers found, 291 with one, 18 babies, 7 listed with no
record; 315 job sites; every one has its last tick, one a raid record, and
five hold a standing for somebody, twelve between them.

The bounds: 200,000 mobs and 400,000 block entities a dimension, no record
over 8 MB or nested more than 64 deep, 2,048 type names; and in an answer
40 types of mob, 20 named mobs and 64 spawners a structure, with 5,000
names and 20,000 spawner positions a survey. A box is gone through by its
64-block squares or by everything the dimension holds, whichever is
fewer, so one joined across a whole world costs no more than the world
holds. What is left out is counted
(`mobKindsMore`, `namedMore`, `spawnersMore`, and
`mcmap_structures_contents_skipped`). A record that does not parse is
skipped and counted. Setting the contents inside the boxes has ten seconds
of its own, checked at each structure; if it runs out, the
structures are served without details and
`mcmap_structures_detail_failures_total` counts it.

### Found by their blocks

Kinds the world keeps no record of are found all the same, without the
seed, by what only they are generated with. They come from the same pass
and cost nothing more. There are two sorts of thing to go by:

- *Blocks nobody can move.* Trial spawners, vaults, a spawner, portal and
  gateway blocks: nobody in a survival world can pick one up and put it
  somewhere else, so where one is, its structure is.
- *Loot nobody has touched.* The game gives each chest, barrel and
  suspicious block of a structure a loot table named after that structure,
  and drops the name the first time the chest is opened or the block
  brushed. One that still carries it is where the generator put it and has
  not been touched. Such a kind is found for as long as one such block is
  left in it; when the last is opened the map stops finding it, though it
  stands where it stood. The sheet says so, and the kind is `quiet` in the
  catalog: a finished site of its rule with nothing found is offered as a
  site and is not the world saying there is nothing there. Which loot
  table a block carries is never sent, and nor is anything a chest holds.

| Kind | In | Found by | Joined | The box |
|---|---|---|---|---|
| `trial_chamber` | Overworld | Trial spawners and vaults | By the generator's grid: every block in one square of it is one chamber | Around those blocks. The chamber's corridors run on past them |
| `stronghold` | Overworld | The silverfish spawner of its portal room, or the blocks of its end portal once lit | Chunks holding either, where they touch | Around those blocks: one room of a structure hundreds of blocks across. A stronghold whose spawner is broken and whose portal is not lit is not found |
| `end_city` | The End | Chests nobody has opened, shulkers, and the dragon head and framed elytra of its ship | By the generator's grid of 20 chunks, moved six back | Around those. Its contents are counted 32 blocks past it |
| `end_gateway` | The End | Its gateway block | Each its own | The block |
| `exit_portal` | The End | The portal blocks of the fountain, once the dragon is dead | Where they touch | Around them |
| `bastion` | Nether | Chests nobody has opened, and the magma cube spawner of a treasure room | Chunks holding either, up to three apart | Around those; contents are counted 32 blocks past it |
| `ruined_portal` | Overworld, Nether | Its chest, while nobody has opened it | Each its own | The chest; contents are counted 8 blocks past it |
| `mansion` | Overworld | Chests nobody has opened | Chunks up to four apart | Around those; 16 blocks past |
| `ancient_city` | Overworld | Chests nobody has opened | Chunks up to six apart | Around those; 32 blocks past |
| `shipwreck` | Overworld | Chests nobody has opened | Chunks up to two apart | Around those; 8 blocks past |
| `ocean_ruins` | Overworld | Chests nobody has opened, and suspicious sand and gravel nobody has brushed | Chunks up to two apart | Around those; 8 blocks past |
| `buried_treasure` | Overworld | Its chest, while nobody has opened it | Each its own | The chest |
| `desert_pyramid`, `jungle_temple`, `igloo`, `trail_ruins` | Overworld | Their chests, the temple's trap dispensers, and suspicious sand and gravel, where the world has no record of the structure | Chunks touching, and for the ruins up to three apart | Around those |

A shulker is a mob, and is counted for an end city all the same: a city
whose chests are all opened is otherwise not found at all, and its
shulkers are what is left to go there for. One carried off and kept
elsewhere in the End would read as a city found by one shulker.

**In the save of each.** An end city's sheet says how many shulkers the
save holds in it and whether its ship's dragon head and the elytra in
their frame are there, which is also how a ship is known at all: a city
with no ship and one stripped of both look the same. A bastion's says
which of the four it is where its blocks still say (a treasure room by
its chests or its spawner, the stables and the bridge by chests of their
own; the housing units have none that say), and its piglin brutes,
piglins and hoglins. The exit portal's says whether it is lit.

One the world has also recorded, as a newer game records an igloo an
older one left only a chest of, is left to its record: what is found
within 64 blocks of a recorded structure of the same kind is that
structure. A temple's traps are found beside the box the game recorded
for it, 17 blocks off in one case here, and the next structure of any
of these kinds is hundreds of blocks away.

**Some kinds are for finding.** This is a survival world with no cheats.
A kind that is a goal in itself, or that is gone to for what is in it, is
`asked` in the catalog: off until the viewer turns its row on, which is
one click, and left out of every search until then.

| Kind | Why it is off until asked for |
|---|---|
| Stronghold | The way to the End, which a player sets out to find |
| End city | Where the elytra and the shulkers are |
| Woodland mansion | Rare, far off, and gone to for its totems; the game sells a map to it |
| Ancient city | The deep dark's own prize, found by going down and looking |
| Buried treasure | Nothing but a chest, and a mark on the map is the whole of finding it |

Fortresses, bastions, monuments and the rest are on from the start: they
are large, seen from far off, and what is in them is fought for and not
found. Where the stronghold is, is the one thing on this map a player
sets out to find for themselves; one in chunks somebody else generated is
found here without anybody having walked into it. So it is held back
further than any other kind:

- its item among the kinds of structure is off until the viewer turns it
  on, and says so;
- a search does not list one unless the page says the viewer has that row
  on (`asked=stronghold`, or `strongholds=1` as pages before the list of
  such kinds grew said it), whatever is typed;
- its details say whether the portal is lit and whether the silverfish
  spawner is still there, and not where the spawner stands.

Trial chambers are on from the start, like every recorded kind.

**What the server enforces, and what is courtesy.** Two different things
keep a kind off a viewer's map, and only one of them is a lock.

- *Withheld by the owner: enforced.* `STRUCTURES_WITHHELD` names kinds
  the service keeps off the map for everybody: by default
  `buried_treasure`, since where each of 525 chests lies is a treasure
  map of a world with no cheats. The survey does not record, find or
  predict a withheld kind, so nothing downstream has one to give away,
  and the server leaves it out again where a list becomes an answer: it
  is not in `/api/structures` (its `recorded`, `predicted`, `kinds` or
  `catalog`), not in `/api/structures/detail`, and not in a search,
  whatever a request says. With no entry in the catalog the page has no
  line for it, no count, and nothing a saved view or a link naming it
  can switch on. `STRUCTURES_WITHHELD=none` withholds nothing; a name
  that is no kind's stops the service at start, since the kind meant
  would otherwise be shown.
- *Off until asked for: courtesy.* The other kinds in the table above
  are sent to every logged-in browser, since a row has to be able to draw
  them, and are kept off by the page until the viewer asks. That keeps
  one from being stumbled on and is not a lock: anyone who can log in
  can ask the API. What the page does hold to is that asking is the
  viewer's own act. A kind that is off until asked for is drawn only
  while it is among the ones this viewer turned on, whatever else a
  choice says: "only this" and "just these" do not show one. A link
  carries what its sender had hidden and not what they had asked for, so
  opening one switches no such kind on, and a view kept from a link is
  the same. A search lists one only for a page that says its row is on.

**The answer is kept between surveys.** `/api/structures` is the same
bytes for every session until the next survey, a quarter of a megabyte
for the overworld here. It is written out once a survey for each
dimension and sent with an `ETag` and `Cache-Control: private,
no-cache`, as the markers are: a browser that holds it is answered 304
and sent nothing. The service does not compress what it sends, this
answer or any other; nothing in it does, and that is left to whatever
stands in front of it.

**A chamber's grid.** The generator cuts the world into squares of 34
chunks and gives each at most one trial chamber, which starts in the first
22 chunks of its square and reaches a few chunks either way. Its blocks
therefore lie from five chunks before the square to 26 into it, and the
two chunks after that hold no chamber's. All 6,282 trial spawners and
vaults in the FWB world bear this out: not one is in those two chunks, on
either axis. So the chamber a block belongs to is the square it is in,
with the squares moved seven chunks back, and that needs no seed. Parts
of one chamber cut apart by chunks the world has not generated are one
chamber, which joining by nearness got wrong eighteen times in this
world, and two chambers side by side are never one. `-run
RealWorldDetails` is how to check it again after a game update: a chamber
found by a handful of blocks next to another is the sign the grid has
moved.

**In part.** A chamber found by fewer than 20 blocks carries `partial`,
and the sheet says it is most likely only partly generated. The FWB
world's chambers fall in two groups with nothing between: 164 found by 21
to 73 blocks, and 55 by fewer than 20, every one of those at the edge of
what the world has generated. A stronghold is never marked so: one room
is all that is ever found of it.

A box wider than one square of that grid, 544 blocks, is no one
structure's, whatever joined it: portal blocks laid in a line join without
end. It is left out and counted as skipped. The widest chamber in the FWB
world is found across 214 blocks.

Finding them is a few lookups for each block kept, and shares the ten
seconds the details have. At the bound, 400,000 blocks of a kind's own
each in a chunk of its own, it takes 0.16 seconds; if it ever ran out of
time, the kinds the world records would be served without these, and
`mcmap_structures_detail_failures_total` would count it.

Each carries `evidence`, how many such blocks it was found from, in place
of `areas`. The page draws its box dashed and says in the sheet that the
box is the box around those blocks and that the structure reaches further.
Neither is predicted, and neither is checked against the seed. On the FWB
world: 219 trial chambers and 4 strongholds, two by their spawner and two
by a lit portal whose spawner is gone.

What else was looked at, kind by kind, and why each is or is not shown,
is in [the audit](#every-structure-the-game-generates).

**Predicted** structures are worked out from the seed. The generator cuts
the world into regions, a grid per kind, and gives each region one site at
an offset drawn from a Mersenne Twister seeded with the region, a 32-bit
structure seed and a number per kind. The structure seed is the low 32 bits
of the seed the chunk was generated from, which is [not the same for
every chunk](#the-seed-of-each-chunk). Each kind is a `Predictor` in
`internal/structures`, so one can be corrected alone.

| Kind | Region, chunks | Offset below | Salt | Draws per axis | Built in |
|---|---|---|---|---|---|
| Nether fortress | 30 | 26 | 30084232 | 1, and a third draw picks fortress (2 in 6) or bastion | Any biome |
| Ocean monument | 32 | 27 | 10387313 | 2, averaged | Deep oceans |
| Pillager outpost | 80 | 56 | 165745296 | 2, averaged | Plains, sunflower plains, desert, savanna, taiga, snowy plains, meadow, grove, snowy slopes, cherry grove and the three peaks |
| Village | 34 | 26 | 10387312 | 2, averaged | Plains, sunflower plains, desert, savanna, taiga, snowy plains, meadow |
| Witch hut | 32 | 24 | 14357617 | 1 | Swamp |
| Desert pyramid | The witch hut's sites | | | | Desert, desert hills |
| Jungle temple | The witch hut's sites | | | | Jungle, jungle hills |
| Igloo | The witch hut's sites | | | | Snowy plains, snowy taiga, snowy slopes |
| Bastion remnant | The fortress's sites | | | The fortress's third draw: the other 4 in 6 | Any Nether biome but basalt deltas |
| End city | 20 | 9 | 10387313 | 2, averaged | The End, on the outer islands |
| Ruined portal | 40, and 25 in the Nether | 25, and 15 | 40552231 | 1 | Any biome |
| Woodland mansion | 80 | 60 | 10387319 | 2, averaged | Dark forest, dark forest hills, pale garden |
| Ocean ruins | 20 | 12 | 14357621 | 1 | Any ocean |
| Buried treasure | 4 | 2 | 16842397 | 2, averaged | Beach, snowy beach, stony shore, mushroom field shore |
| Trial chambers | 34 | 22 | 94251327 | 1, by Java's generator | Any biome |
| Trail ruins | 34 | 26 | 83469867 | 1, by Java's generator | Taiga, snowy taiga, the old growth taigas, old growth birch forest, jungle |

**Java's generator.** The two kinds the game builds from data files are
not placed as the older ones are. Their regions are the same squares, and
the offset is drawn from `java.util.Random`, a 48-bit generator, seeded
with the region, the kind's number and all 64 bits of the world seed: the
game places these where Java Edition does. Under the Mersenne Twister and
the low 32 bits neither is at a site more often than a wrong seed puts one
there. They are worked out only from a seed all of which is known: each
chunk's own, and `level.dat`'s; not from `STRUCTURE_SEED` or a seed the
search found, which are 32 bits.

A fortress is built at its site whatever the biome. Every other kind is
only built where the biome suits, so a site is one of three things:

- **Possible** (`candidate`): the site's chunk is not generated. Nothing
  knows what biome it will be, so this is where the generator will try and
  no more. Of the monument sites in generated chunks of the FWB world, one
  in twelve holds a monument. Only sites within about 64 chunks of a
  generated one are kept: the country a player could walk into next.
- **Predicted, and the world disagrees** (`generated`): the chunk is
  finished, its biome suits the kind, and nothing is recorded there, of a
  kind the game records when it builds it. It is drawn struck through,
  logged once and counted in
  `mcmap_structures_prediction_disagreements{dimension,kind}`.
- **Nothing found here now** (`generated` and `vacant`): the same, of a
  kind that can stand where the save shows no sign of it, because what it
  is known by is opened, brushed or taken, or because the game records it
  late. Either one stood here and has been emptied or none was ever
  built, and nothing says which. It is not a prediction and is not drawn
  or named as one: its mark is the faintest on the map, its tooltip and
  its sheet say `a site with nothing found at it now`, its kind's line
  counts it apart (`1 predicted, 3 sites with nothing found`), it is not
  counted as a disagreement, and a search does not list it.
- **Dropped**: the chunk's biome is known and the kind is not built in it;
  or the site is within 48 blocks of a structure of its kind that the
  world already holds, which is that structure and not another (a
  village's box moves off its site with its beds, and a chamber's
  spawners stop short of its site), except for buried treasure, whose
  next site is that near and is known by the very chunk it is in.

What becomes of a site in a finished chunk of a suitable biome where the
save shows nothing, kind by kind, and why:

| Kind | Such a site is | Because |
|---|---|---|
| Fortress, monument, outpost, witch hut | Shown struck through, as a disagreement | The game records one when it builds it, so none recorded is the world saying no |
| Trial chamber | Dropped | Its spawners cannot be taken away: none there is no chamber, as in every chunk older than the structure |
| Village | Nothing found here now | The game keeps a record only once a player has been near |
| Desert pyramid, jungle temple, igloo, trail ruins | Nothing found here now | An older game kept no record, and their loot is taken |
| Bastion, end city, ruined portal, ocean ruins, buried treasure | Nothing found here now | Known only by loot, which is taken |
| Woodland mansion | Nothing found here now | Known only by loot; no such site exists in this world |
| Shipwreck, ancient city | Not predicted at all | A site is empty more often than not |

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
  `mcmap_structures_kind_verified{dimension,kind}` is 1 or 0. A world that has
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

**How each rule fared** against the FWB world on 2026-10-10 (game
1.26.52.3, the snapshot of 2026-10-05), with the biomes read and each
site worked out from its own chunk's seed. "On a site" is the share of
the world's recorded structures the rule explains, of those in chunks
that name a seed; "sites built on" is the share of sites in finished
chunks of a suitable biome that have a recorded structure.

| Kind | Recorded | On a site | Sites built on | Outcome |
|---|---|---|---|---|
| Nether fortress | 13 | 8 of 10 | 6 of 6 | Predicted |
| Ocean monument | 20 | 15 of 16, exactly | 15 of 16 | Predicted |
| Pillager outpost | 22 | 19 of 21, exactly | 17 of 17 | Predicted |
| Village | 70 | 56 of 67 | 48 of 57 | Predicted |
| Witch hut | 3 | 2 of 2, exactly | 2 of 2 | Not predicted: two are not three |
| Desert pyramid | 3, and 4 found | 6 of 6, exactly | 6 of 6 | Predicted, in finished chunks; none is left to predict |
| Jungle temple | 4, and 2 found | 5 of 5, exactly | 5 of 9 | Predicted, in finished chunks |
| Igloo | 9, and 3 found | 11 of 11, exactly | 9 of 11 | Predicted, in finished chunks |
| Bastion remnant | 21 found | 17 of 19 | 14 of 15 | Predicted |
| End city | 21 found | 19 of 19 | 20 of 23 | Predicted |
| Ruined portal, overworld | 177 found | 144 of 144 | 83 of 101 | Predicted, in finished chunks |
| Ruined portal, Nether | 42 found | 37 of 37 | 34 of 41 | Predicted, in finished chunks |
| Woodland mansion | None; 3 map targets | 3 of 3 | No finished site is in its biome | Predicted |
| Trial chambers | 219 found | 133 of 149 | 123 of 171 | Predicted where not generated |
| Trail ruins | 26, and 17 found | 22 of 37 | 11 of 16 | Predicted, in finished chunks of the current seed |
| Ocean ruins | 169 found | 140 of 142 | 137 of 144 | Predicted, in finished chunks |
| Buried treasure | 525 found | 446 of 446 | 444 of 501 | Predicted, in finished chunks |

Under the one seed the map used before, the same world gave 31 villages
of 70 on a site and 27 sites of 37 built on. The structures on no site
now are parts of ones cut off where the chunks of one seed meet the
chunks of the other, which hold a piece and not the block the rule
looks for: three fortress parts, a monument and two outposts.

The pyramids, temples and igloos share the witch hut's sites, and the
biome under a site says which of the four is built there. Until a chunk
is finished nothing says which, so these three are offered only in
finished chunks, where the biome is known: a site in country not
generated would otherwise be a mark for each of them. A finished site
with none is offered plainly and is not counted as a disagreement: the
older half of this world was generated by a game that kept no record of
these kinds, so a site there with nothing recorded may well hold one.

The kinds found by their blocks are set beside their rules the same way,
what was found standing in for what was recorded. A bastion counts as on
a site when the middle of the site's chunk is within 64 blocks of the box
round its chests, an end city within 24 and a ruined portal's chest
within 24. A portal is slight and common, so it is offered only in
finished chunks, as the pyramids are; bastions and end cities are offered
in country not generated as well, an end city as a possible site, since
it is built only where the outer islands give it ground.

What the numbers leave open, so that nobody has to find it out again:

- *Bastions.* The two on no site are each a chest or two at the edge of
  what is generated. The one finished site with none has no unopened
  chest within reach; none of the four sites in basalt deltas has any.
- *End cities.* The three finished sites with nothing found are where the
  rule puts a city and the land, most likely, does not hold one up.
- *Ruined portals.* Every chest in a chunk that names its seed is at a
  site. One site in five in a finished chunk has no unopened chest, which
  is a portal somebody has been to as easily as one that is not there.
- *Woodland mansions.* This world has built none: no finished site is in
  a dark forest. The rule is checked against the game instead. A woodland
  explorer map holds the block the game itself worked a mansion out to be
  at, generated or not, and each of the three places this world's maps
  point to is a site of one of its two seeds, where no site of a wrong
  seed is. Two of the three were worked out from the older seed, in
  country the world will now generate from the newer one, and no mansion
  will be built there: a map is evidence for the rule and is not shown as
  a mansion. The third is a site of the current seed, and is on the map
  as `mapped`, which is more than a possible site and less than a
  mansion: the game has said it will build one there. No other mansion
  site is offered in country not generated: of those the biome will allow
  one in twenty, and three maps do not make a hundred marks. A site in a
  finished chunk is offered only where the biome read from the world is a
  dark forest or a pale garden, of which this world has none. A map that
  is lost is not lost to the save: the game keeps a map's record whatever
  becomes of the item, so the rule stays checked. Were the records to go,
  the rule would have fewer than three to stand on, and mansions would
  stop being offered until the world had three again.
- *Trial chambers.* A chamber's spawners cannot be taken away, so a
  finished site with none has no chamber and is not offered: 48 such
  sites are in chunks generated before the game had chambers. In chunks
  the current game generated, nine sites in ten hold one. A chamber is
  built whatever the biome above it, so a site in country not generated
  is predicted and not merely possible; the deep dark, which has none,
  is the exception nothing here can see.
- *Trail ruins.* The current game's ruins are where the rule says. The 24
  in the chunks of the older seed are not, 6 of them on a site: an older
  game placed them another way. The rule is set aside for that seed,
  nothing is predicted in its chunks, and the kind stands by the rest.
- *Ocean ruins and buried treasure* are where their rules say almost
  without exception, and are slight and many. Each is offered only in a
  finished chunk of a biome that suits, where a site with nothing found
  is most likely one somebody has emptied. A treasure's sites are one
  chunk in sixteen, so they are asked for only in the regions the world
  has chunks in.
- *Shipwrecks* have a rule that puts every wreck found at a site (24
  chunks to a region, an offset below 20, one draw, the number
  165745295), and three finished sites in five in water hold no unopened
  chest. A site says too little to be worth a mark, so shipwrecks are
  found by their chests and not predicted.
- *Ancient cities* are found by their chests and not predicted: the rule
  published for them is not told from chance by this world, and a city
  needs a biome the map cannot see.
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
- *Witch huts.* The world holds three, one of them in a chunk that names
  no seed. The rule puts the other two in their sites' chunks, at odds of
  about one in six hundred each for a wrong rule, and the two swamp sites
  in finished chunks are those huts. Two is fewer than the check acts on,
  so huts stay unpredicted on this world and start being predicted, with
  no change here, once it has a third.

**Bounds.** Sites are looked for in the box round a dimension's chunks and
64 chunks more, within 49,000 blocks of the middle of them. At most 20,000
sites of a kind are set beside the world, and 500 predictions of a kind
are kept for a dimension, those the world can already be asked about first
and then the nearest the middle; 2,000 for the dimension between them.
Whatever is left out is counted in `predictedMore`. On the FWB world the
overworld is sent 1,384 known structures and 1,830 sites: 500 each of
monuments, villages and trial chambers, which is the bound, 121 outposts,
120 mansions and 89 of the kinds offered only in finished chunks, with
1,739 left out. The Nether is sent 81 known and 119 sites, and the End 31
known and 441.

**The seed.** `RandomSeed` and the world spawn are read from `level.dat` in
the mirror, which is little-endian NBT behind an eight-byte header and is
only ever opened for reading. The seed is never served or logged: with it,
a seed map shows everything the world has yet to generate. The spawn is
served with the overworld's structures.

<a id="the-seed-of-each-chunk"></a>**The seed of each chunk.** A world is
not always generated from one seed, and the FWB world was not: the low 32
bits of its `RandomSeed` do not place the structures its older chunks
recorded, because those chunks were generated from another seed, before
the world was given the one it has now. The game writes which with every
chunk. Record 63 of a chunk is an eight-byte hash, and the entry of that
hash in `LevelChunkMetaDataDictionary` (a 32-bit count, then for each
entry its hash and an unnamed NBT compound) holds the chunk's
`GenerationSeed` beside the game versions that first and last wrote it.
So:

- a site is worked out from the seed of the chunk it falls in, and is
  offered only if that is the seed that puts a site there;
- a site in a chunk nobody has generated is worked out from the seed in
  `level.dat`, which is the one the game will generate that chunk from;
- a recorded structure is set beside the sites of its own chunks' seed,
  wherever it starts: one that starts in a chunk an earlier seed made is
  still built, in part, in the chunks beside it that the later seed made;
- nothing is said of a site in a chunk that names no seed, and a
  structure recorded in one counts neither for a rule nor against it.

Before this the map worked every site out from the one seed its recorded
structures answered to, which was the older one: right for the older
chunks, and wrong for every site it offered in country not generated,
all of which the game will generate from the seed in `level.dat`.

A kind whose rule a game version changed leaves the chunks of that
version's seed disagreeing with the rule. Each seed's chunks are judged
apart: where three or more structures of a kind are in the chunks of one
seed and the rule is not borne out by them, the rule is set aside for
that seed, nothing is predicted in its chunks, and the kind stands or
falls by the rest. Where the rest bears the rule out, the chunks of the
seed set aside are not counted against the seeds either: what an older
game did otherwise says nothing of whether they are right. A kind
contradicted wherever it can be judged still puts the seed in doubt.
`mcmap_structures_generation_seeds` is how many seeds
the world's chunks name, and `mcmap_structures_chunks_without_seed` how
many chunks name none: 2 and 4,619 here. A world that names more than
eight seeds is logged as doing so, and the chunks of the ninth and later
are among those that name none. The dictionary is at most 32 MB
and 65,536 entries, and at most eight seeds are told apart; one that
cannot be read is refused whole and logged, and the world is then taken
to be all of one seed, as a world from before the game kept the record
is.

**Working the seed out.** For a world that does not say which seed made
its chunks, when the seed in hand is refuted, the service
finds the one the world's records answer to by trying all 2^32 of them. A
recorded monument, outpost or witch hut says which offsets its region's
site can have had; one of them, the record a chance seed is least likely
to explain, is tested against every seeding of the generator, and the few
million seedings that pass are set against the rest. The seed that
explains that record and at least two more, and more than any other does,
is the answer. It is used from the next survey and checked there like any
other, so a wrong answer is a quiet layer and not a wrong one.

The search is one goroutine: about fifty minutes of one CPU, and longer
under a CPU limit below that. Nothing is predicted meanwhile and nothing
else waits for it. Its outcome is kept in `structure-seed.json` in
`DATA_DIR`, readable by the service alone, beside a mirror that holds
`level.dat` already: a restart reads it instead of searching, and a search
that found nothing is not run again until the world records another such
structure. A search cut short by a restart starts over. The seed it finds
is never served or logged. `STRUCTURE_SEED_SEARCH=false` switches it off.

`STRUCTURE_SEED` supplies the value by hand instead, and is checked against
the world in the same way before anything is predicted from it. A whole
world seed is accepted and cut to its low 32 bits, as the game cuts it. A
seed given this way is used for every chunk, whatever the chunks say of
their own, and is never searched past, so one the world refutes stays
refuted until it is taken away. Treat it as the seed, and leave it unset
for a world that names its chunks' seeds.

The survey runs last in each cycle, on hard links like the chunk count, and
its failure costs nothing else. That includes a panic: it parses over a
hundred thousand records the game wrote, so one it cannot get through is
caught, logged with its stack and counted, and the cycle and the service
go on with the structures of the last survey that worked. On the FWB world (2.47 million records,
1,274 boxes) it took 9 seconds and peaked at 45 MB when it read four
kinds. Reading every kind there is now costs half a second more: measured
on one CPU on 2026-10-10, three runs each way, the survey went from a
median of 9.2 seconds to 9.8, and the process, with the biomes it reads
first, from a peak of 126 MB to 135. The new kinds are found in the pass
the survey already makes: record 119 and record 63 are two more records
of chunks it is passing, the loot table of a chest is read from the block
entity already in hand, a map is skipped by its length but for its dozen
tags, and which seed each chunk is of is a map of nine bytes a chunk. A
site costs a few hundred multiplications. It keeps at most 200,000
boxes and 2,000 structures of each layer per dimension, and says how many
it left out.

The villages cost that survey nothing to speak of. Their records share a
prefix, so they are read by seeking to it in the view the survey already
has open, not by another pass: 281 records, 1.2 milliseconds on the FWB
world. The read has ten seconds of its own. If it runs out of them or
fails, the villages of the survey before stand, so long as that survey was
of this world and no later in it (the same seed, and a game tick that has
not gone back); in another world they would be villages that are not
there, and none are served. Either way it is logged and counted in
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

Reading what the structures hold added nothing that could be measured to
the survey's time: sixteen runs each way on one CPU against the FWB world
came to 8.64 seconds with it and 8.79 without, the difference being less
than the spread. It walks 35 MB of actor records and 19 MB of block
entities in place, from the values the pass has in hand already, and
allocates nothing for a record it does not keep; setting the contents
inside 323 boxes takes 6 milliseconds. Peak memory for the survey alone
went from about 60 MB to about 67 MB. In about one run in ten a single
collection ran late and the peak was near 130 MB for a moment.

To check the rules again after a game update, against a copy of a world:

```sh
MCMAP_REAL_WORLD=/path/to/FWB go test -run RealWorld -v ./minecraft/mcmap/internal/structures/
```

It reads the biomes as the service does and prints, for each kind, the two
shares in the table above and how many sites it would offer. With
`MCMAP_STRUCTURE_SEED` set it uses that seed. It prints neither the seed
nor where anything is predicted, since a few sites and the rule that made
them are the seed; the recorded structures it does print are the world's
own. `-run RealWorldDetails` prints what the save holds in the structures
instead, as counts for each kind and never a place or a name.

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
grey, which is how one biome is picked out. `biomes=<name>,<name>` draws
the biomes named and nothing else, and `except=<name>,<name>` everything
but them; what is left out is left clear, as ungenerated ground is. A
tile is asked for in one of the three ways at a time, every name must be
a biome the game has or one this dimension holds, and a list holds at
most 128 biomes, each counted once, in at most 8,320 characters:
anything else is a 400. The tile's `ETag` names the set in one order, so the same biomes are
the same tile however they were written. `GET /api/biomes` gives the
colours for the panel and a `version`; a tile asked for with `v=<version>`
is kept by the browser for good.

**On the page.** Biomes is a section of the layer panel whose own switch
is the overlay, off until the viewer turns it on. On, it lays the tiles
over the terrain, and under it is every biome the dimension holds,
largest first, each an item with its colour and its share of the area.
Hiding some draws the rest: the page asks for whichever is the shorter
of the list to draw (`biomes=`) and the list to leave out (`except=`),
in one order, so several biomes are shown at once and the same choice is
the same address. Only on a biome asks for `biome=` tiles instead, which
keep it its colour and dim the rest; pressing it again goes back to what
was hidden before. Resting the
pointer on the map, or clicking it, names the biome at that block in the
footer. The listing is asked for again each minute while the overlay is on,
and a new `version` redraws it. A service without biomes answers 404 to the
listing, and the page then has no Biomes section at all.

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
with `live: true`, under the name it has now and in the dimension it is in
now, whatever the snapshot had of either, and otherwise where the snapshot
left it. A mob named
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
The arrow keys move through the list and Enter chooses. A clear button in
the box, shown while there is something typed, empties it, shuts the list,
takes the ring off the map and leaves the cursor in the box; Escape does the
same, and a second Escape, with nothing left to clear, leaves the box for
the map. Neither closes the card about a mob.
Choosing a hit takes the map there, changing dimension if it has to, and
rings the spot for twenty seconds; a biome hit also turns the overlay on
with that biome picked out. A player or a named mob moves, so choosing one
opens the card about it instead of ringing where it was: a player and a
loaded mob are tracked live and can be followed, and a mob that is not
loaded is shown at its last saved position, said as that. The search asks
the server, which has every dimension's newest picture; the page has only
the last frame it drew, which is older while it is paused or paced slowly.
So a player the search found who is not in that frame is shown where the
search put them, with `This position is as of the search`, and is called
no longer tracked only once a frame drawn since the search lacks them. A hit says
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

**On the page.** Trails is a section of the layer panel, off until turned
on. Under it is how far back the trails go, as four buttons side by side
(1 h, 6 h, 24 h, 7 d) and Custom, which opens a small box to type any
other length in as the live interval's is typed (a bare number is hours),
from a minute up to what the server keeps; a length the server does not
keep is not offered. It is sent as `since`. Each player with a trail is
an item under that, keyed by the colour of their line, which can be
hidden by itself and has Zoom to. The
viewer's own trail is the green the live layer draws them in, which
`live.js` offers as `window.mcmap.playerColour`; every other player's is
one of eight colours picked by their gamertag, or the next one free where
two on the map pick the same. Zoom to on a player's item fits the map to
that trail. The
lines are drawn on the live layer's canvas behind its markers, so a marker
is never crossed by its own trail and is still what a click on it reaches;
hovering a line anywhere else names the player and the time it covers. The trails are asked for about once a
minute and never more often; between answers the lines are carried forward
from the live frames by the same rules the server records by, and the next
answer replaces them. The row's switch and the window are kept with
everything else the page keeps (see Page controls). A service without
trails answers 404 once, and the page then has no Trails row.

## Page controls

**The layer panel.** Everything the map can show is in one panel beside
the map, as a list three deep and no deeper: a section, the layers in it,
and what each layer is made of.

| Section | Layers | Items of each |
|---|---|---|
| Players | (the section is the layer) | Each player, with their head; Go to opens their card |
| Mobs | Hostile, Passive, Villagers, Other | Each type of mob there now, with its icon and count |
| Markers | Beds, Containers, Named mobs, Waypoints | Beds by colour; containers by what they are, shulker boxes by colour; each named mob, with its type, whether a baby, and whether it is loaded or only saved; each waypoint |
| Structures | Known, Predicted, Possible, under the heading How sure | Every kind once, as a list over those three switches, each with its picture, how many are known, and under it how many more are predicted or possible. A mark is on the map when its kind and its certainty are both on. Strongholds are off until asked for |
| Biomes | (the section is the overlay) | Each biome of the dimension, with its colour and share |
| Trails | (the section is the layer) | Each player's trail, in its colour, under the choice of how far back |
| Overlays | Slime chunks, Grid, Chunk focus, World spawn | none |

Every line at every depth is the same line, drawn by one function: what
opens it, a checkbox, a picture or a colour key, a name, a count, and a
button for the rest of what can be done with it. A checkbox is half on
when some of what is under it is hidden, and a count is then
`shown / all`. Switching a layer off leaves its items' own choices as
they were, so switching it on again brings the same selection back, and
so does ticking one of its items while it is off, with that item shown
as well. A section's own checkbox does the same over its layers: with
any of them on it switches the section off and remembers which were on,
and the next press puts those back. An item or a row that is off as the
page first has it, as strongholds are, is the baseline: it makes nothing
read as half on or as hidden until the viewer has changed something, and
Reset returns to it. The button at the end of a line opens a menu: Only
this (and, once it is the one alone, Back to before, which puts every
row or item exactly as it was, on or off), Show all in group, Hide all
in group, Zoom to where the line has an extent, and Go to for a player,
a named mob or a waypoint. Under a mouse, Only also appears on the line
itself. Only on an item shows that one and nothing else its choice
covers, so Only on a type of mob hides every other type in all four
rows and Only on a kind of structure is that kind however sure the map
is of it; on a layer it hides the section's other layers; on a section,
every other section. What was shown alone is still alone, with its way
back, after a reload, and the way back is let go of only when the viewer
switches one of those rows themselves or a saved view puts them another
way.

Above the list is a box that narrows the panel to the lines whose names
hold what is typed, opens whatever a match is under and marks the match.
It changes nothing on the map and nothing that is kept. Under it one line
says how much of everything is shown, `41 of 72 shown`, with Reset
whenever anything differs from how the page first has it, so a panel that
is scrolled or put away still says that something is hidden. The panel's
own menu has three widths, the three densities, Expand all, Collapse all
and Reset layers to defaults.

The lines are a list of checkboxes that open, not a tree widget: each
control is what a screen reader already knows it to be (a button that
says whether it is expanded, a checkbox that may say mixed, a menu
button), and a layer's items are a named group. The Tab key stops once in
the list; from there the arrow keys move between lines, Right and Left
open and close a line or go to the one it is under, Home and End go to
the first and last, Space switches a line, Enter opens a group or goes to
a player, a named mob or a waypoint, Shift and F10 open the line's menu,
and a letter goes to the next line that begins with it.

**The panel's shell.** With 960 pixels or more across, the panel is docked
on the right and the map is as wide as what is left: it is resized, not
covered. It opens 340 pixels wide (280 on the compact density, 400 on the
spacious) and its inner edge is a separator: dragged, or with the focus
on it moved 16 pixels by the Left and Right arrows, to its narrowest and
widest by Home and End, and put back to the usual width by a
double-click. The width is between 240 and 480 pixels and never more
than 45 hundredths of the window, and is kept. The three widths in the
panel's menu are the way that needs no dragging. Collapsed, by its
button, by Enter on the edge or by `L`, the panel is a rail 40 pixels
wide with a button for each section, which opens the panel at that
section and is marked where the section has something hidden. Between
721 and 959 pixels only the rail takes room, and the open panel lies over
the map's edge. On a small screen the panel is a sheet from the bottom
with a handle: pressing the handle goes to the next of three heights
(the title alone, half, all of the map), the arrow keys on it do the
same, and dragging it follows the finger and settles at the nearest, or
shuts the sheet if let go below the shortest. Below its full height the
map above the sheet is still the map; at its full height what it covers
is inert until it is lower. In a wide short window the sheet is a drawer
down the right instead. A drag of the edge or of the handle is drawn
once a frame however fast the pointer moves, and ends when the pointer
is let go, cancelled or taken, or the window is no longer in front: with
900 mobs and 2,100 markers on the map and five moves to a frame, a frame
of a drag of the edge took 28 ms at the median and the page was laid out
117 times in 60 frames.

**Density.** The page is sized by one scale of named lengths in the
stylesheet, which the panel, the bar, the cards and the sheets all use.

| | Compact | Comfortable | Spacious | Under a finger |
|---|---|---|---|---|
| Row | 24 | 32 | 40 | 44 |
| Section heading | 28 | 36 | 44 | 48 |
| Padding at a row's ends | 8 | 12 | 12 | 16 |
| Gap between a row's parts | 4 | 8 | 8 | 12 |
| Indent a level | 16 | 20 | 24 | 20 |
| Text | 14 on 20 | 14 on 20 | 16 on 24 | 16 on 22 |
| Counts and notes | 12 on 16 | 12 on 16 | 14 on 20 | 14 on 20 |
| Checkbox | 16 | 18 | 20 | 22 |
| Picture or colour key | 16 | 20 | 24 | 24 |
| Least anything pressed | 24 | 28 | 32 | 44 |

The last column is used wherever the pointer is a finger or the screen is
a phone's, whichever density is chosen. A picture from the game is drawn
at 16 pixels in every density, in a box the size the scale gives: pixel
art is not enlarged by a fraction.

**Which items are shown, and how that is kept.** A layer's items are
shown by a choice the panel keeps beside the switches, as what differs
from everything showing, under `<group>#<name>`. It has two forms. As
`{ only, hidden, shown }` everything is drawn but what is in `hidden`,
and `shown` is the few that are off until asked for and have been; an
item that turns up later is drawn. As `{ mode: "just", just }` nothing
is drawn but what is in `just`, and an item that turns up later is not:
this is what leaving Only by ticking a second item comes to, and what a
list of hides is turned into once it passes 200 and the list of what is
shown is the shorter. Which form it is changes only when the viewer
changes the choice, never because a list grew. `only` is the one shown
alone, with the rest of the choice left as it was underneath, which is
what Back to before returns to. A list holds at most 200 ids; where
neither form fits in that, the row says that only the first 200 will be
as they are next time. An id that has been in none of its lists for
thirty days is dropped, and one that comes back before then is not. One
choice may cover several layers, as `live#mobs` covers the four rows of
mobs. A frame of the live layer
pays two set lookups an entity for it: a marker is put on the canvas or
left off when its entity first appears, and again only when a row or a
choice changes. The panel is drawn at most once for everything that
changes in a turn of the page and writes only what differs; a list of
more than 40 items shows 40 and a line that asks for the rest; and while
the pointer or the focus is in a list nothing in it moves. Measured in
headless Chromium at 1440 by 900 with 900 live mobs of 60 types, 1,500
beds, 600 containers, 232 known structures and 210 biomes, every group of
mobs open: ten seconds of live frames wrote to the panel four times and
left no long task; a pan took the two frames it was measured over (33.3
ms median, 36.6 worst of 30); switching between the built-in views took
5 to 11 ms of script and 36 to 51 ms to the frame that showed it; and
hiding one type, one colour of bed or one kind of container took 1 to 2
ms of script and was on the next frame. An entity the card is about that
a hidden item or a switched-off row hides is still tracked, and followed
if it was; the card says it is hidden and why.

**Adding a layer.** A layer is a script of its own, loaded after
`layers.js`, which registers its rows and never edits the panel:

```js
const row = window.mcmap.layers.register({
  group: 'markers',     // with id, what the viewer's choice is saved under
  id: 'beds',
  label: 'Beds',
  enabled: true,        // the choice until the viewer makes one; true if left out
  order: 10,            // lower first; rows given none go last, as registered
  section: 'markers',   // where it is shown; the group, if left out
  whole: false,         // true for a layer that is its section's own heading
  groupLabel: 'Markers', // the title of a section that is not one of the page's
  swatch: 'ring beds',  // class of a colour key beside the label, styled in style.css
  picture: 'bed/red',   // the game's picture, by its key or as an element
  facet: 'beds',        // names the choice its items are shown by; rows may share one
  bare: false,          // true for a list with no switch: its items stand under the section
  heading: '',          // a word or two written over this row and those after it
  actions: { zoom: (item) => {}, go: (item) => {} }, // item is left out for the row itself
});
row.enabled;            // the saved choice, kept current
row.setCount(1234);     // or null for none
row.setNote('Not surveyed yet'); // or '' for none
row.setLabel('Ocean Monuments'); // for a name that arrives late
row.onToggle((on) => { /* draw or clear */ });
row.setAvailable(false);         // greyed out, and not switched by its own checkbox
row.setEnabled(true);            // switch it as the viewer would; kept, and onToggle is told
row.setItems([                   // what the layer is made of, in the order to show it
  { id: 'red', label: 'Red Bed', colour: '#b02e26', count: 93 },
  // also: picture, swatch, shape: 'line', detail, note, off, disabled, go: false, zoom: false
]);
row.shows('red');                // whether that item is on the map: two set lookups
row.setControl({ label, options: [{ value, label }], value, onChange, custom }); // a choice among a few values
row.remove();

const choice = window.mcmap.layers.facet('markers', 'beds'); // the same choice, without a row
choice.shows('red'); choice.only; choice.solo('red'); choice.onChange(() => { /* redraw */ });
```

A layer says what it has as plain data and builds none of the panel: the
list given to `setItems` is diffed against the last one, so giving it
again every second costs what changed in it. `setBody(node)`, by which a
script from before items put controls of its own under its row, still
works and is no longer used by any script here.

A layer that keeps more than its switches, as the live layer keeps which
types it shows, asks the panel to keep it beside them:
`window.mcmap.layers.retain(group, name, value)` stores a small JSON value
beside the switches under `<group>#<name>` (null removes it) and
`recall(group, name)` reads it back, or null. Small is three levels of
plain values, strings of up to 64 characters and lists of up to 200;
anything else is dropped when it is read back, and so is a key that is a
name every object has, such as `constructor`. What comes back is the
viewer's storage and is checked by whoever reads it.

`onToggle` is called with the new value when the viewer changes the row,
by its checkbox, its section's or a menu, and not when it is
registered: read `enabled` once to begin with. Registering an id again
replaces its row. A label, a note and a group's title are set as text,
never parsed, and so are an item's label, detail and note, so a name from
the world is safe in any of them.
`window.mcmap.layers.ready` is a promise that resolves, with the same
object, once `register` exists; a script loaded after `layers.js` can call
`register` at once, and one that might run before it waits on `ready`.
Each of `live.js`, `markers.js` and `structures.js` is an example.

**Going to coordinates.** X, Z and Go take the view to a block and ring
it, labelled with its coordinates, until it is cleared or the map changes
dimension. A clear button beside Go, shown while there is a number or a
ring to clear, empties both boxes, removes the ring and puts the cursor
back in X; the address in the bar is the view's, as ever, and is not
changed by clearing. Pasting `x z`, `x, z` or `x y z` into X, with or
without the axes' letters, fills X and Z from it (`y` is not needed by a
map) and moves to Go.

**Chunk focus.** With Grid on, the chunk under the pointer is outlined and
a box under the zoom buttons says which it is, by its chunk coordinates,
the blocks it covers, and in the Overworld whether it is a slime chunk
(`chunk.js`, by the same function the slime layer uses). The chunk of
block -1 is chunk -1: every coordinate is floored, never truncated. A click
or a tap pins the chunk: its outline turns green and stays through panning
and zooming, the box goes on describing it while the pointer is elsewhere,
and Unpin or a second click on it lets it go. Under Go to a chunk in the
box, Chunk X, Z and Go take the map to a chunk by its own coordinates and
pin it. A pin belongs to
the dimension it was made in. From further out than four pixels to a chunk
the focus is the region instead, 32 chunks square, with its chunk and block
ranges and how many of its 1,024 chunks are slime chunks, so that zooming
out outlines one square and not a thousand. The Nether and the End get the
same coordinates and are said to have no slime chunks.

**Small screens.** Narrower than 720 pixels, or shorter than 480, the page
is laid out to give the map the screen. Those are where the layout breaks,
not the size of any device: under 720 the footer starts to wrap and the
bar's controls are about to take a fourth row, and in a window under 480
high two rows of bar leave less than two thirds of it to the map. There:

- the bar is one row: the dimensions, a button that opens the search over
  the bar with its results hanging under it, and More;
- More holds everything else the bar has, in titled parts: go to
  coordinates, the grid, the live pause and interval, copy link and the
  shortcuts list, and who is logged in. It is a sheet under the bar, or
  down the right-hand side when the window is wider than it is tall;
- the layer panel's button stays in the corner of the map and the panel
  comes up from the bottom over the whole width as a sheet with three
  heights, or down the right-hand side in a wide short window, scrolling
  by itself. It is never found open on arriving: the choice made where
  the panel sits beside the map is kept for there;
- the footer is one line: where the middle of the map is, and one reading
  of how old the live positions are (or when the terrain was updated, on a
  map with no live layer), which opens both timers over the footer;
- the chunk focus sits between the zoom buttons and Layers.

Only one of them is open at a time. Opening the search, More, the layer
panel or the timers shuts the other three; the card about a mob or player
is tucked away while any of them is open and is back, about the same
thing, when it shuts (in a wide short window it stays beside the panel);
choosing something that opens the card shuts whatever was open; and a
structure's details or the shortcuts list, which take the whole page, shut
all four. Each is shut by its own button, by Escape, and, all but the
layer panel, by a touch outside it: the map above the panel's sheet is
there to be used, and a touch on it is a touch on the map. Nothing of
this is kept but the height the sheet was last at: the page opens with
everything shut.

Everything that is pressed is at least 44 pixels each way, the zoom
buttons included. The page is as tall as the visible part of the window
(`100dvh`), so a browser's own bars never cover the footer, and keeps
clear of a notch and a home indicator by `env(safe-area-inset-*)`. Sheets
fade in over a seventh of a second unless the viewer has asked for less
motion. Wider and taller than that, nothing here applies and the layout is
the one described above.

Measured in headless Chromium with touch, everything shut, the map's share
of the viewport went from 41.1% to 82.7% at 320 by 568, from 58.5% to
86.8% at 360 by 740, from 72.7% to 89.1% at 414 by 896, and from 51.4% to
72.8% at 740 by 360.

**Shortcuts.** The `?` button in the bar, and the `?` key, open a list of
the keyboard shortcuts and of what the search box takes besides a name. It
is the browser's own modal dialog: the focus is held inside it, Escape, its
Close button and a click outside all shut it, and the focus goes back to
where it was.

| Key | Does |
|---|---|
| `/` | Puts the cursor in the search box |
| `Esc` | Clears a search, then closes the card; in the search box a second press leaves it |
| `G` | Grid and chunk focus on or off |
| `L` | Layer panel open, or put away to its rail |
| `P` | Pause or resume live positions |
| `F` | Follow, or stop following, what the card is about |
| `1` `2` `3` | Overworld, Nether, The End |
| `V` | Opens the list of views; in it, `1` to `9` switches to the view with that number |
| `S` | Go to the world spawn |
| `M` | Go to the logged-in player and open their card, while they are online |
| `N` | Lists the next group of markers, from the middle of the map outwards, to choose one from |
| `+` `-` | Zoom in and out (`=` zooms in too) |

Each key works the control the page already has, as a click on it would,
so a shortcut cannot do what the page does not offer; one that can do
nothing just now says why in a line over the map. None fires while the
focus is in a text box, a number box or a menu (a checkbox or a button
takes no text, and does not silence them), while the list itself is
open, with Ctrl, Alt or the command key held, or while logged out. They
are single keys, which a screen reader's own single keys and speech input
can collide with, so the list has a switch that turns them all off, kept
with the page's other settings; Escape is not one of them. With the map itself
focused, zooming and the arrow keys are Leaflet's, as before.

**Shorthand in the search box.** `120 -340`, `120, -340` or `120 64 -340`
is listed as those coordinates and goes to them; `chunk 7 -21` is that
chunk, and choosing it goes there, pins it and turns the grid on; `spawn`
lists the world spawn first; `me` lists the logged-in player first, while
online; and `@name` looks among the players online and nothing else. Only
`@name` is the whole question: whatever else was typed is also looked up as
the name it may be, and those hits are listed beneath, so a container
called Spawn farm is still found by `spawn`. Coordinates and a chunk are
answered by the page; the other three ask `/api/search` for one `kind`.
The `S` and `M` keys ask for themselves and leave the box's list as it
was. None of them is a command for the game, and the page writes none.

**The footer.** Where the pointer is, and the biome there, are on the
left. On the right is one group saying how current the map is, in two parts
with a label each: `Live positions`, how old the picture of players and
mobs is (or `paused`, `connecting`, `reconnecting`, `no data`), then the
interval if it is slower than the server's and how many mobs are shown if
some were left out; and `Terrain`, when the tiles were last updated and the
countdown below. The parts of each are separated by a dot and the two by a
rule. Every figure is set in digits of one width in a slot as wide as its
longest ordinary value, so nothing in the footer moves as the seconds
count. On a small screen the group is behind one short reading, as below.

**The refresh countdown.** The footer counts down to the next refresh of
the terrain and the markers, from `snapshotAt` and `refreshSeconds` and the
server's own clock as its `Date` header gives it. The service counts its
interval from the end of a cycle, so a healthy refresh lands a little after
zero; for the first 90 seconds past the time the page says `refresh due
now`, and asks every five seconds whether it has landed. Past that, which
is what a quiet window or a failing cycle looks like, it says `refresh
overdue by` and counts up, asking every 20 seconds after the first two
minutes. It starts again only when a new snapshot has been seen.

**Live updates.** Beside the grid switch are Pause and an interval, both
kept in the browser. The interval is chosen from
a menu (1, 2, 5, 10 or 30 seconds, 1 or 5 minutes) or, under Other, typed:
a number with `s`, `m`, `h` or `d`, whole or not, alone or joined, such as
`5`, `90s`, `1.5m`, `1m30s`, `2h` or `1d`; a bare number is seconds. What
was understood is said in words under the box as it is typed (`every 1 min
30 s`) and Enter applies it. Something that is not a length, a negative
one, or a number with no unit in a joined form is refused with a sentence
saying why, and nothing changes. The shortest is 1 second, the server's own
pace, and the longest a day; a length outside those is brought to the
nearer one and the page says so. A length typed is added to the menu, kept,
and said in the footer in the same words; a pace kept by an earlier version
of the page is still used. The terrain's own refresh is the server's cycle
(`REFRESH_INTERVAL`) and is not something the page can set.
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

Follow keeps the entity in the middle of the map that can be seen, at the
zoom the viewer has, and draws a solid ring round it; an entity that is
only inspected has a dashed one. The map that can be seen is its box less
a band for whatever lies across the middle of it: the panel where it
floats over the map's edge, the sheet at the height it stands at, the
card where it runs across the foot of a phone. A card in a corner, clear
of the middle, takes nothing, and neither does a sheet at its full
height, which leaves no map to find a middle in (`room.js`). The map is
brought back in the frame any of those changes. Dragging the map or
panning it with the arrow keys turns Follow off and leaves the card open.
Zooming does not, and is about the entity while it is followed, so the
wheel, a double click, a pinch, the buttons and the keys all leave it
where it is and not where the pointer was. Only the map's own bounds keep
it from the middle, at an edge of the world. A player's trail ends at
their marker while they are in the live picture, not at the last block
recorded. While it is on, the button reads
Following and is filled, and is still the pressed toggle it was to a
screen reader; pressing it again stops. Whatever turns it off, the button,
a drag, an arrow key, the entity going, another dimension or the card
closing, the button goes back to Follow and a status line that is read out
and not shown says `Following <name>.` or `No longer following <name>.`

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
opening the card on one needs a pointer; a player's and a named mob's
item in the layer panel opens it from the keyboard too, and everything on
the card is a button.

Everything here is a native button, checkbox or select: each is reached
with Tab, worked with Space or Enter (the arrow keys, for the interval),
and outlined while it has the focus.

**What the page keeps.** Everything the viewer chooses is kept in the
browser and nowhere else, as one record under `mcmap.settings`, written and
read by `settings.js` alone, which the other scripts reach as
`window.mcmapSettings`. The record has a version (`v: 2`; it went from 1
when a saved view began to keep which of a layer's items are shown, so
that a tab of the page from before, which reads a later record and
writes nothing over it, cannot take them out again; a record or an
exported file of version 1 is read and brought up) and these parts:

| Part | Holds |
|---|---|
| `layers` | Each row's switch as `"<group>/<id>": true`, and what is kept beside them as `"<group>#<name>"`: which of a layer's items are shown, and under `panel#…` the panel's width and sheet height, which layers are open to their items, and what an Only is to go back to |
| `panel` | Whether the layer panel is open, and which sections are folded |
| `live` | `paused`, and `interval` in seconds |
| `trails` | The window, as `seconds` |
| `shortcuts` | Whether the single-key shortcuts are on |
| `grid` | Whether the grid is on |
| `biome` | The one biome picked out, or null |
| `look` | The appearance settings |
| `views` | The saved views, their order, which are hidden, and which the page opens with |
| `old` | A stamp of each old key as it was last seen, to tell when a script from before has written one |

Every part is read against a closed description: a value of the wrong type
is put back to its default, a number out of range is brought to the nearer
bound, and a key nobody described is dropped. The record may come to
200,000 characters; a change that would take it past that is refused whole
and said, and a record found larger than that, which the page never
writes, is left exactly as it is while the page works from its defaults
and says so. A browser that refuses storage, or has no room left, gets a
page that works the same on what it holds in memory, and the Views and
Appearance sheets say that nothing will outlast the visit. A record
written by a later version of the page is read for what this one knows and
left as it is. A second tab's write is taken in, and its theme is on this
tab at once, so that two tabs do not undo each other.

Before there was one record, each script kept its own key, and they are
carried over the first time the record is made:

| Old key | Held | Becomes |
|---|---|---|
| `mcmap.layers` | The panel's switches and filters | `layers`, as it was |
| `mcmap.panel` | `{ open, folded }` | `panel` |
| `mcmap.liveControl` | `{ paused, interval }` | `live` |
| `mcmap.trails` | `{ seconds }`, or `{ hours }` before that | `trails`, in seconds |
| `mcmap.shortcuts` | `{ on }` | `shortcuts` |
| `mcmap.live` | The filters before the panel, and one Live switch | A row's switch where `layers` has none; the switch off becomes `live.paused` |
| `mcmap.markers` | The filters before the panel | A row's switch where `layers` has none |
| `mcmap.structures` | The filters before the panel, and one Structures switch | A row's switch where `layers` has none; the switch off becomes Known, Predicted and Possible off |

None of the old keys is ever removed, and the first five are still written
in the form they always had whenever their part changes: the page and its
scripts are cached apart for five minutes, and for that long after a
release a page may have scripts of two ages. Three things keep the
viewer's choices through it. The record is under a name of its own and
not on `window.mcmap`, which an `app.js` from before replaces. A script
that still finds no record, on a page from before `settings.js`, reads
and writes the key it always had, exactly as it did. And since a script
from before writes only the old key, the record keeps a stamp of each old
key as it last saw it and, on load, takes the value of any that has
changed since; the old keys are written before the record and read back,
so one that could not be written is not later taken for a newer choice.
Checked in a browser with the old page and the old `app.js` from the
release before, in all four mixes with the new ones: the layers, the
pause (no stream opened), the pace, the shortcuts switch, the folded
groups and the trails' window are as they were kept, a choice made in any
mix outlasts a reload of it, and is in the record once every script is
new. A load that changes nothing writes nothing. The grid and the biome
picked out were not kept before and are now.

**Views.** A view is a named snapshot of what the map shows and how: every
row's switch, the mob and player filters, the biome picked out, the trails'
window, the grid, the live interval, and, each by a checkbox when it is
saved, the place (dimension, centre, zoom and a pinned chunk) and the
appearance settings. Views is a button in the bar, and in More on a small
screen; it opens a sheet that lists them. Choosing one, or pressing its
number, switches to it and shuts the sheet; a line over the map says which,
and what of it could not be done. Under Edit a view can be renamed, updated
to what the map shows now, moved up or down, marked as what the page opens
with, copied as a link, and deleted, with Undo offered until something else
is done. Three views come with the page and are not kept in the record:
Everything (players, mobs, markers, structures and trails), Exploring
(players, waypoints, structures, biomes and the world spawn, with no mobs)
and Base (players, named mobs, beds, containers and waypoints). They cannot be
renamed or deleted, and can be hidden and shown again. The page keeps up to
50 views, each with a name of up to 40 characters, with no control
characters, none that take no room or turn the direction of writing
round, and no more than three accents piled on one letter.

Switching happens in one turn of the page: the record is changed once,
every row is switched before any layer is told, and a layer that listens
with one function for all its rows redraws once. Measured in headless
Chromium with 907 live mobs and 2,100 markers in view, at 1300 by 800 and
at 360 by 740, over 56 switches: one between the built-in views holds the
page for 3 to 7 ms and the next frame is drawn 14 to 32 ms after the
press; one that also changes the theme and the marker size holds it for
13 to 19 ms, with the next frame within 41 ms. No switch made a long
task. A view with a place turns Follow off before it
moves the map, and the line over the map says so; one without leaves
Follow alone. A view the record has no room for is refused before anything
is moved.

The view marked as what the page opens with is put into the record by
`settings.js` in the head, before any other script has read its part, so
nothing is drawn one way and then another; a place named in the address
wins over the view's own. Every load puts back its layers, filters,
overlays and place, and never its appearance: how the page looks is
always as the viewer last set it, and a view's own appearance comes with
it only when the viewer switches to it. The list says so under the marked
view, and the save form beside the checkbox. A view may name a layer the page no
longer has or the server does not offer, a type of mob nobody has heard
of, a biome the dimension does not hold or a dimension that is not
rendered: the rest of it is applied, and the line over the map says what
was left out. A row that is only greyed out for now, as Slime chunks is in
the Nether, is switched all the same.

**A view in a link or a file.** Copy link to this view makes an address
with one more part than the page's own: `#<dimension>/<x>/<z>/<zoom>/<what
is open>/<view>`. An address without the sixth part reads exactly as it
did, and a page from before views reads one with it as far as it
understands. A view saved with its place puts that place in the first
parts; one saved without says `-/0/0/0`, which is no dimension, so whoever
opens it stays where their own page would start. The view is `v1.` and a
small JSON object in the URL-safe base64 alphabet: `n` the name, `l` one
character a known layer (`1` on, `0` off, `-` not said), `x` any other
layer, `m` the mob filter, `b` the biome, `t` the trails' window, `i` the
interval, `g` the grid, `p` and `q` the place and its pin, `a` the
appearance settings the view has, each under its number in a fixed list.
The two fixed lists only ever grow at the end. Which players a view hides is never put in a link. The part may be
1,800 characters; a view of every layer with a filter, its place and its
appearance is 455, and one too long to fit is not cut short but said, with the
file offered instead.

A link is read before any of it is used: its length, its alphabet, that
it is UTF-8 and JSON, that it has no key outside the closed set, and then,
as a view, against the same description the record is, strictly, so that
one wrong value refuses the whole. A refusal is a sentence of the page's
own, with nothing from the link in it. A link that is read is only an
offer: the address moves the map nowhere, and the sheet opens with the
view set apart at the top, under its name as text, with Preview it, Save
as and Dismiss. A preview is held in memory: what is kept is held as it
was before anything is touched, nothing the preview or the viewer changes
meanwhile is written, and a bar between the header and the map says a
shared view is being previewed. Keep it saves it as a new view and shows
that for keeps; Go back puts back what was there, every layer, the theme,
the place, the pinned chunk and whoever was followed; and a reload is the
viewer's own setup. Saving adds a view and replaces none.

Export to a file writes the whole record as `mcmap-settings.json`. Import
reads a file of up to 300,000 bytes by the same strict description, says
what it will do, and waits to be told: the file's views are added to the
viewer's own, never in the place of one, and its other parts take the
place of theirs, on the page at once.

**Appearance.** Appearance is beside Views. Each setting marks its default,
and Reset to defaults puts them all back.

| Setting | Values (default first) |
|---|---|
| Theme | Dark, light, high contrast, or the same as the system |
| Marker style | Pictures on plates, dots, large pictures |
| Mob picture | Faces, spawn eggs |
| Marker size | Normal, small, large, extra large |
| Label size | Normal, small, large |
| Names of named mobs, of players, of waypoints | Always shown, under the pointer, never; each separately |
| Biome tint, trails, slime chunks | 60%, 100% and 100%, each from 10% to 100% |
| Density | Comfortable, compact, spacious |
| Motion | The same as the system, reduced, full |
| Coordinates | Blocks, or blocks and the chunk |

Every colour on the page is a custom property named once for each theme at
the top of `style.css`, and what is drawn on a canvas reads the same names
through `settings.colour(name)`. The terrain is the same in every theme,
so a marker keeps a dark outline and a dark backing behind its picture in
all of them; a theme changes the page, its panels, and the plates that
labels are written on. A change of theme, size or label size composes the
sprites and name tags anew from the pictures already decoded and restyles
the markers in place; nothing is fetched again. A larger marker is the
next whole number of screen pixels to a texture pixel (twice the size, or
one and a half times on a dense screen), so pixel art is never blended on
the way up; a smaller one is the 12 pixels a baby has always been drawn
at, and is blended as a baby is. Reduced motion stills the page's own
animations and the map's zoom, pan and fade. No density changes a
finger's 44 pixels on a small screen or under a coarse pointer. A slider is on the map as it
is dragged and written once, when it is let go.

The light and the high-contrast themes are held to 4.5 to one for text and
3 to one for the edge of a control or a marker, over the lightest and the
darkest terrain where it is drawn on the map, by a test over the
stylesheet's own values. Measured on the page as drawn, over 1,770 pieces
of text in eight states of the page at two widths: the lowest text is 6.81
to one in the light theme and 8.22 in high contrast, the lowest name tag
6.95 and 12.04, and the lowest edge 3.80 and 8.28. The letter a structure
is drawn as while it has no picture is at 5.28 and 5.67 on a recorded one
and 4.79 and 5.67 on a predicted one, over snow; a trail's key, which is
its line on the line's own dark casing, is at 6.46 and 6.94 against the
casing. The dark theme is the page as it was, and was not changed to meet
them: there a predicted structure's letter over snow is still at 1.17.

**Settings that follow a player (a design, not built).** Everything above
stays in one browser. This is how it would follow a player to another.

*What is stored, and where.* One JSON document a player, on the map's
volume at `settings/<xuid>.json`, written to a temporary file and renamed.
It holds the parts of the record that are the player's and not the
device's: `views` (without the hidden players of each view, which are
other people's gamertags) and `look`, with a revision number and the time
of the write. The layer switches as they stand, the panel, the pause and
the grid stay with the device; a view is how they travel. The server reads
the document against the same closed description the page uses, kept as
one set of test vectors that both the Go and the page's checks must pass.

*Limits.* 64 KiB a document, 50 views, names of 40 characters, one write
every five seconds a session and 2,000 documents in all, the least
recently written going first: 128 MB at the very worst and a few kilobytes
a player in practice.

*Endpoints.* All behind the session, with the XUID taken from it and never
from the request, `Cache-Control: no-store`, and the cross-origin
protection the page's other writes have.

| Request | Answers |
|---|---|
| `GET /api/settings` | `200` with `{ rev, updatedAt, views, look }` and `ETag: "<rev>"`, `304`, or `204` for a player with none |
| `PUT /api/settings` with `If-Match: "<rev>"` | `200 { rev }`; `412` with the document as it stands; `413` too large; `422` not valid; `429` too soon |
| `DELETE /api/settings` | `204`, and the document is gone |

*Two devices.* The page pulls on load and pushes two seconds after a
change. A push made against an old revision is refused with what the
server has, and the page merges and pushes once more: views by id, each
with the time it was last changed, the later one kept and the other saved
beside it as "<name> (other device)" if both changed; a deleted view kept
as a tombstone for 30 days so that it is not brought back; `look` whole,
by the later change; the order the server's, with views only this device
has at the end.

*Privacy.* No endpoint takes an XUID or a gamertag, so no player can ask
for another's. The document is not logged, only its size, and is not in
any metric. Delete removes it at once.

*Logged out.* A map with no login, or a page that is logged out, works
from the browser's record exactly as it does now. On logging in: with
nothing on the server, the page asks once whether these settings should
follow the player; with a document there and a browser at its defaults,
the document is taken; with both, they are merged as above. Logging out
leaves the browser's record as it is, marked with whose it was, and a
different player logging in is offered it as an import, not given it.

*Cost.* One conditional GET a page load and one small PUT a change; about
300 lines of Go with its tests, and as much again in the page.

*What to build first.* (1) The store, GET and PUT with the revision, the
limits and the checks. (2) In the page, pull on load and push after a
change, with a refused push answered by a choice of "use the other
device's" or "keep this one's" and no merging. (3) Merging by view, with
tombstones. (4) DELETE, and a switch to keep this device out of it.

## Login

The map shows where every base is, so it sits behind a login that proves the
visitor plays on the server.

1. The page asks `POST /auth/start` and shows the six-character code it gets
   back. The secret that will collect the login is set as an `HttpOnly`,
   `SameSite=Strict` cookie that script cannot read.
   Beside the command is Copy command, which puts the whole of it on the
   clipboard as it has to be typed, `!map` and the code, and says Copied;
   where there is no clipboard to write to the command is selected
   instead and the page says so. It is offered only while there is a code.
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
| `STRUCTURE_SEED` | no | each chunk's own seed where the world names it; otherwise the low 32 bits of the seed in `level.dat`, or what the search found | The 32 bits structure placement is seeded with, or a whole world seed whose low 32 bits are taken, for a world whose `level.dat` does not hold them. As secret as the seed. Switches the search off |
| `STRUCTURES_WITHHELD` | no | `buried_treasure` | The kinds of structure kept off the map for everybody, by the names the API uses, separated by commas: not surveyed, and in no answer. `none` withholds nothing; a name that is no kind's stops the service |
| `STRUCTURE_SEED_SEARCH` | no | `true` | `false` leaves a seed the world's records refute as it is, instead of working the right one out from them |
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
| `GET /api/icons` | Session required. Which live markers have a picture: `mobs` with a `version` and the `types` that have an icon, `pictures` with a `version`, the `keys` that have a picture and `boxes`, the head of each that is a face as `[x, y, width, height]` in the picture's own pixels (the groups `bed`, `container`, `shulker`, `marker` and `structure`, and since faces and blocks were made `face`, `villager` and `block` in the same list under the same version, so a page from before them reads the answer as it always did), `names` with the `version` of `/api/names`, `heads` giving each head's version by gamertag in lower case, and `me`, the gamertag the session's player is online under. Carries an `ETag` and answers 304 to a matching `If-None-Match`. Not served with `ICONS_ENABLED=false` |
| `GET /api/icons/mob/{type}?v=<version>` | Session required. That mob type's icon as a PNG, kept for good by the browser when `v` is the current version. 404 for a type with no icon |
| `GET /api/icons/picture/{group}/{name}?v=<version>` | Session required. The picture with the key `{group}/{name}` as a PNG, whichever group it is of, kept for good by the browser when `v` is the current `pictures.version`. 404 for a key `/api/icons` does not list |
| `GET /api/names` | Session required. Display names by id: `entities` (by mob type), `containers` (`chest`, `trapped_chest`, `barrel`, `shulker`), `beds` and `shulkers` (by colour, plus `default`, and `undyed` for shulkers) and `structures` (by kind), with a `version`. Every value is plain text, to be written as text and never as markup. Carries an `ETag` and answers 304 to a matching `If-None-Match`. Not served with `ICONS_ENABLED=false` |
| `GET /api/icons/head?name=<gamertag>&v=<version>` | Session required. The head of the one online player holding that gamertag, as a PNG. 404 if nobody does, two players do, or their skin gave no head |
| `GET /api/structures?dimension=<id>&kinds=all` | Session required. `recorded` (each a `kind` and its box, `minX` to `maxZ`, with `areas`, how many recorded boxes it was joined from, and for an abandoned camp `variant`, the biome it was built for; or for a `village` with `village`: `counted`, `villagers`, `golems`, `cats`, `beds`, `bells`, `jobSites`; or for a kind found by its blocks with `evidence`, how many blocks and mobs it was found by, and for a trial chamber `partial` where that is fewer than a finished one is found by), `predicted` (each a `kind`, `x`, `z`, with `candidate` where the chunk is not generated and the biome will decide, `generated` where the chunk is finished, suits the kind and the world holds none, with `vacant` beside it for a kind that can stand where the save shows no sign of it, or `mapped` where one of the world's explorer maps points at a site in country not generated), `recordedMore` and `predictedMore` for what the bounds left out, `prediction` (`verified`, `unverified`, `refuted` or `unknown`, of the seed), `kinds` (for each kind the dimension has a rule for, its own `state` and how many recorded ones `agree` and `disagree`), `catalog` (every kind the map can show, in any dimension: its `kind`, the `dimensions` the game generates it in, `asked` where it is off until the viewer turns it on, and `quiet` where a finished site with none may hold one all the same), `surveyed`, `at`, and with the overworld `spawn`. Without `kinds=all` the answer holds only the seven kinds there were before `catalog`, for a page that could not put a later one away. 400 for an unknown dimension. Not served with `STRUCTURES_ENABLED=false` |
| `GET /api/structures/detail?dimension=<id>&kind=<kind>&x=<x>&z=<z>` | Session required. One recorded structure of the list, named by its kind and the middle of its box (`minX + (maxX - minX) / 2` rounded down, and likewise `z`, which is what the page's address carries). `at`, the structure as the list gives it, and `detail`, left out if the last survey could not work it out: `reach`, for a kind found by its blocks, how many blocks past the box round those the rest was counted in; `uncounted`, for a stronghold, where nothing but what it was found by is said and the lists are empty for that reason; `mobsTotal`; `mobs` (each `kind`, `count`, and `babies` and `captains` where there are any) with `mobKindsMore`; `named` (each `kind`, `name`, `baby`, and a villager's `profession` and `level`, 1 to 5) with `namedMore`; `spawnerCounts` (each `mob`, `count`, `trial`); `spawners` (each `mob`, `x`, `y`, `z`, `trial`) with `spawnersMore`; `containers` (each `kind` of `chest`, `barrel` or `shulker`, with `unopened`, `holding`, `empty`); `blocks` (counts of `cauldron`, `bell`, `vault`, `ominous_vault`, `end_portal`, `end_gateway`, `dispenser`, `dropper`, `unbroken_pot`, `unbrushed` for suspicious sand and gravel nobody has brushed, and `dragon_head` and `elytra` for an end ship's, those there are); `elders` for a monument; `bastion` for a bastion whose blocks still say which it is, `treasure`, `stables` or `bridge`; and for a counted village `village`: `professions` (each `profession`, empty for none, `count`, and `levels`, five counts from novice to master), `babies`, `missing`, `notLookedUp`, `golems`, `cats`, `jobSites` (each `profession`, `count`), `idleSeconds`, `raid` (`wave`, `waves`, `raiders`, `idleSeconds`) and `met`. With a counted village, `standing`: `state` (`known`, `none`, `pending`, `unknown`) and, when known, `value`, which is only ever the standing of the player the session belongs to. A name tag is plain text, to be written as text and never as markup. Never cached: `no-store`. 400 without a dimension, kind, `x` and `z`; 404 for a structure the list does not hold, and before the first survey. Not served with `STRUCTURES_ENABLED=false` |
| `GET /api/biomes?dimension=<id>` | Session required. `extracted`, `at`, `version`, `tiles` (`minZoom`, `maxZoom`, `size`), and `biomes`, largest first: each `id`, `name`, `label`, `color` (`#rrggbb`), `known`, `area` in square blocks, `chunks` and `regions`. 400 for an unknown dimension. Served only with `BIOMES_ENABLED=true`, like the four below |
| `GET /api/biomes/tiles/{dimension}/{zoom}/{x}/{y}.png?biome=<name>&v=<version>` | Session required. One 256-pixel tile of the overlay, addressed as the terrain's; zoom -12 to 4. `biome` picks one out and dims the rest; `biomes=<name>,…` draws only those and `except=<name>,…` all but those, one of the three at a time and at most 128 names. Carries an `ETag`, answers 304 to a matching `If-None-Match`, and is kept for good when `v` is the current version. 404 where the world has no chunks, 400 for a bad address or an unknown biome |
| `GET /api/biomes/at?dimension=<id>&x=<x>&z=<z>` | Session required. `generated`, and with it the `biome` at that block |
| `GET /api/biomes/nearest?dimension=<id>&biome=<name>&x=<x>&z=<z>&limit=<n>` | Session required. `biome`, and `hits`, nearest first: each `x`, `z`, `distance` and its `region` (`area`, `chunks`, `minX`, `minZ`, `maxX`, `maxZ`), with `more`. `limit` is 10 unless given and at most 50. 400 for an unknown biome |
| `GET /api/biomes/region?dimension=<id>&x=<x>&z=<z>` | Session required. The stretch of biome that block is in: `found`, `biome`, `region`, and `rects`, at most 4,096 rows of chunks each `[minX, minZ, maxX, maxZ]` in blocks, with `rectsMore` |
| `GET /api/search?q=<text>&dimension=<id>&x=<x>&z=<z>&limit=<n>&kind=<kind>` | Session required. `hits`, each `kind` (`player`, `biome`, `structure`, `spawn`, `bed`, `container`, `mob`, `waypoint`), `name`, `detail`, a marker's `colour`, `trapped` and `baby` where it has them, a player's or mob's `id` and `live` (true where the position is the live layer's), `dimension`, `x`, `z`, `y` where there is one, and `distance` in the dimension asked from; `more`; and `waypoints` (`searched`, `unavailable`, `off`). `limit` is 20 unless given and at most 50. `kind`, if given, keeps the answer to that kind. `asked=<kind>,<kind>` lists the kinds named among the structures where they are kinds that are off until asked for, which are otherwise left out; `strongholds=1` is the older way of saying so of strongholds. 400 without `q` of 1 to 64 characters, a dimension, `x` and `z`, or with a `kind` that is not one |
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
| `mcmap_icons_marker_pictures` | Marker, structure, face and block pictures held, 173 when whole at the default pin. Zero means every marker is a ring and every structure a letter, which is also the case with `ICONS_ENABLED=false` |
| `mcmap_icons_names` | Display names read from the language file. Zero means every name served is a tidied id, or that `ICONS_ENABLED=false` and none is served |
| `mcmap_icons_faults_total` | Faults caught while making a picture from the samples' models. Each cost one picture, or all the made ones, and is one line in the log; none stopped the map |
| `mcmap_icons_player_heads`, `mcmap_icons_player_heads_refused_total` | Online players with a head, and heads the agent sent that were refused |
| `mcmap_structures_recorded{dimension,kind}`, `mcmap_structures_predicted{dimension,kind,certainty}` | Structures on each layer at the last survey; `certainty` is `predicted`, or `candidate` for a site in terrain not generated yet |
| `mcmap_structures_seed_verified` | 1 while recorded structures are where the seed puts them. 0 means nothing is being predicted |
| `mcmap_structures_kind_verified{dimension,kind}` | 1 while the seed is verified and the kind's own recorded structures are where its rule puts them. 0 means that kind is not being predicted |
| `mcmap_structures_prediction_disagreements{dimension,kind}` | Places where the seed and the world's records disagree |
| `mcmap_structures_generation_seeds`, `mcmap_structures_chunks_without_seed` | How many seeds the world's chunks say they were generated from, 0 for a world that does not say; and the chunks of a world that does which name none, for which no site is worked out |
| `mcmap_structures_areas_skipped{reason}` | Recorded boxes left out: `malformed`, `unknown` (a kind this version does not know), `limit` |
| `mcmap_structures_survey_last_success_timestamp_seconds`, `mcmap_structures_survey_duration_seconds`, `mcmap_structures_survey_failures_total`, `mcmap_structures_survey_panics_total` | Whether the survey is running. A panic is a survey abandoned over a record it could not get through: the structures of the last one that worked are still served, and it is logged with its stack |
| `mcmap_structures_villages_skipped{reason}` | Villages left out: `empty` (counted by the game, no villagers), `malformed`, `unknown` (a key this version does not know), `limit` |
| `mcmap_structures_contents{sort}`, `mcmap_structures_contents_skipped{reason}` | Saved mobs and block entities the last survey kept to set inside structures, `sort` being `mob` or `block`; and what it left out: `malformed` (an actor, block entity, or a village's `PLAYERS` or `RAID` record that did not parse), `limit` |
| `mcmap_structures_detail_duration_seconds`, `mcmap_structures_detail_failures_total` | How long setting those inside the structures took; and times a survey ran out of time doing it, or finding the structures known by their blocks, and was served without that |
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

The page under `web/` is plain files with no build step and no packages.
`web/*_test.go` holds what can be checked of the scripts as text. What
the layer panel does is also checked by doing it:
`web/testdata/panel.test.cjs` loads `settings.js` and `layers.js` into a
page made of plain objects (`web/testdata/dom.cjs`), registers rows as
the layers do, presses lines and reloads, with nothing but node's own
modules. `go test ./web/` runs it where there is a `node` and skips it
where there is none; by hand it is `node web/testdata/panel.test.cjs`.
`web/testdata/structures.test.cjs` does the same for the structures'
script, beside the panel's, with a map that remembers what is put on it
and a server that answers as each test says: which kinds a dimension
lists, what is drawn and what is not, and what a link may switch on.
The three records read for the newer kinds have fuzz targets with seeds
built in code, `FuzzVolumes`, `FuzzSeedBook` and `FuzzMapRecord` in
`internal/structures`, run as
`go test -run '^$' -fuzz FuzzVolumes -fuzztime 1m ./minecraft/mcmap/internal/structures/`.
The rest is checked in a browser. Leaflet is vendored
in `web/lib/leaflet` with its licence.

## Releases

Cut by [`semantic-release.yml`](../../.github/workflows/semantic-release.yml)
from commits that touch this component, tagged `mcmap-v<version>`, and
published as `ghcr.io/jdwillmsen/minecraft-map:<version>` and
`docker.io/jdwillmsen/minecraft-map:<version>`. Never `latest`.
