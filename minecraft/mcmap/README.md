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
| `DATA_DIR` | no | `/data` | Mirror, tiles and the installed renderer. Rebuildable, but the first render is slow, so keep it on a volume |
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
