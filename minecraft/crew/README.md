# crew

A programmable worker unit for Bedrock. Today it joins a server, tracks what
it believes about itself and serves that over a local status API. Design and
standing rules: `docs/specs/2026-10-14-crew-design.md`.

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `MC_HOST`, `MC_USERNAME` | required | Server, and the Microsoft account name (the display name when `MC_OFFLINE` is true) |
| `MC_OFFLINE` | `false` | Lab only: skip Microsoft sign-in and join with `MC_USERNAME` as a bare display name |
| `MC_PORT` | `19132` | RakNet port |
| `MC_VIEW_DISTANCE` | `8` | Chunk radius requested (1 to 32) |
| `STATUS_ADDR` | `127.0.0.1:8080` | Status API and camera listen address |
| `CAMERA_ENABLED` | `false` | Serve the rendered camera |
| `CAMERA_MAX_VIEWERS` | `4` | Concurrent camera viewers (1 to 64) |
| `CAMERA_ATLAS_DIR` | `/data/atlas` | Where the built texture atlas is cached; put it on the data volume. The resource pack it is built from is fetched into `/tmp` and removed after the build |

## Status API

`/healthz`, `/readyz` and `/v1/state`. The state carries `position`,
`position_known`, `yaw` and `pitch`. Until `position_known` is true the
position is the server's placeholder, not a real one.

## Rendered camera

With `CAMERA_ENABLED=true`, crew streams the chunks, entities and its own look
direction it already receives to a browser page that draws a first-person
view with three.js:

- `/camera`: the page
- `/v1/camera/ws`: the websocket the page reads
- `/v1/camera/stats`: counts only
- `/v1/camera/atlas.png` and `/v1/camera/atlas.json`: the texture atlas and its block table, 404 until built

The stream is a position channel and has **no authentication**, so it must not
get an ingress until crew-control puts auth in front of it. Reach it from your
machine with a port-forward and open `http://127.0.0.1:8080/camera`:

```
kubectl -n <namespace> port-forward deploy/<crew deployment> 8080:8080
```

This works with the default `STATUS_ADDR` because a port-forward connects
inside the pod's network namespace. A viewer beyond `CAMERA_MAX_VIEWERS` is
refused with 503, and one that cannot keep up is disconnected and reconnects.
The page loads three.js from a CDN, so the browser needs internet access.

A new viewer is replayed the sub-chunks within 4 chunks of the unit, nearest
first, pulled as it reads; `?r=N` on the page picks another radius and `]`
widens it (up to 32). Sub-chunks travel as a material palette plus bit-packed
indices and moving entities as one batch per 50 ms tick; the byte layout is
documented in `internal/camera/wire.go`. `/v1/camera/stats` also reports bytes
sent, sub-chunks sent, viewers dropped for lagging and viewers refused.

## Lab

`lab/compose.yaml` is a throwaway Bedrock server with cheats on and online
mode off (`ONLINE_MODE: "false"`, `ALLOW_LIST: "false"`), so crew can join it
with `MC_OFFLINE=true` and no Microsoft account.

Offline mode is lab-only. FWB runs online mode, which verifies every client
against Xbox Live and rejects an offline one outright, so an offline unit can
never join it; a FWB unit needs a real account and `MC_OFFLINE` unset. Offline
mode skips the auth cache entirely and logs one `auth_offline` line at start so
such a unit is never mistaken for a signed-in one.

The compose file also runs `crew-1` from the real image, with the camera on
and the status port published on `127.0.0.1:8080` only:

```
docker compose -f minecraft/crew/lab/compose.yaml up -d --build crew-1
curl -s localhost:8080/readyz
```

### Textures

Once the camera knows the server version it builds a texture atlas in the
background from Mojang's `bedrock-samples` pack, fetched at runtime and cached
under `CAMERA_ATLAS_DIR` (about 300 MB). Nothing from the pack is committed
or shipped in the image. When the atlas is ready the stream sends an atlas
frame and the page textures cube faces with nearest filtering, tints grass,
foliage and water, and draws slabs, stairs, cross-plants and torches as simple
shapes. Crew does not receive biome data, so tint is sampled for one climate
everywhere (the plains biome); stairs and slabs have no orientation or half
for the same reason and are drawn as bottom slabs and stairs that rise towards +z.
If the fetch fails, the camera is offline at first start or the page cannot
load the atlas, the view stays flat-coloured and the build is retried on the
next session.

### Entities

Players, mobs and dropped items are drawn as simple boxes sized to their
Bedrock hit boxes, with a per-type colour (unknown types get a hashed colour).
A player's head turns independently of its body and carries a name label; the
page keeps the type table, so adding a mob is a page-only change. Entity
records on the wire carry the feet position and a head yaw byte. Players
reported by `MovePlayer` are shifted down by the 1.62 eye height; the other
position packets are taken to report the feet.

## Camera wall load test

`loadtest/run.sh` starts M crew units against a lab server with distinct
usernames and status ports, opens N headless Chrome viewers spread across them
(`loadtest/viewers.mjs`, playwright-core), and appends one row per run to a
markdown file. Crew CPU and RSS come from `/proc`, bytes from
`/v1/camera/stats`, and frame cost from the page's `window.__stats`.

```
minecraft/crew/loadtest/run.sh -b <crew binary> -c <chrome> -w <dir holding node_modules/playwright-core> \
  -a <atlas cache dir> -m <units> -n <viewers> [-r radius] [-k viewer cap] [-d window seconds] -o results.md
```

Units are named `load-1`, `load-2`, ... (`-u` changes the prefix) on status
ports from 18100 up (`-p`). The lab must already be running. Crew CPU and
bytes cover the steady window after every page has meshed; "Replay" is the one
off cost of the viewers' first connect.

Results, 8-core Linux host shared with other work, lab world, view distance 8,
software GL (no GPU), 60 s steady window, one viewer per unit where N equals M.
Frame work is time inside the page's frame callback; the software renderer's
cost lands outside it, so the page frame rate is the figure to read. The max
includes the replay burst. The page has no frustum-limited radius or worker
meshing yet, so these rows are without them.

| Units | Viewers | Radius | Crew CPU mean / max | Crew RSS max (MB) | Steady KB/s per unit | Steady KB/s per viewer | Replay MB (all units) | Settle (s) | Frame work avg (ms) | Frame work max (ms) | Page fps |
|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 | 1 | 4 | 1.1% / 1.1% | 116 | 3.9 | 3.9 | 0.6 | 0.4 | 0.6 | 72 | 60.0 |
| 2 | 3 | 4 | 1.1% / 1.2% | 111 | 6.3 | 4.2 | 1.7 | 0.7 | 0.8 | 349 | 34.3 |
| 4 | 4 | 4 | 0.9% / 1.0% | 129 | 3.8 | 3.8 | 2.2 | 0.8 | 0.9 | 597 | 25.5 |
| 4 | 4 | 8 | 1.0% / 1.0% | 112 | 4.3 | 4.3 | 15.5 | 1.4 | 2.7 | 947 | 8.4 |
| 1 | 8 | 4 | 2.3% / 2.3% | 109 | 30.0 | 3.8 | 4.6 | 1.6 | 0.8 | 1024 | 13.1 |

The last two rows are beyond the required (1,1), (2,3), (4,4) runs: radius 8
for comparison, and eight viewers on one unit with `CAMERA_MAX_VIEWERS=8`.

Chosen defaults: **tile radius 4 and viewer cap 4, unchanged.** Crew is not
the limit: a unit stays near 1 to 2% of a core and about 4 KB/s per viewer
however many units run, and even eight viewers on one unit cost 2.3% CPU. The
browser is: doubling the radius to 8 sends 7 times the replay data and drops
four tiles from 25 to 8 fps on software GL, while radius 4 settles in under a
second. The cap stays 4 because the cost it guards is the viewers' rendering
and the single unit's fan-out has no measured cliff below it; raise it per
deployment if a wall needs more than four viewers on one unit. Re-measure on
real GPUs once frustum limiting and worker meshing land.
