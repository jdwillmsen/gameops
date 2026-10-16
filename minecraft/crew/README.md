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
| `CAMERA_ATLAS_DIR` | `/data/atlas` | Where the texture atlas and the resource pack it is built from are cached; put it on the data volume |

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
