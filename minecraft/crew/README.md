# crew

A programmable worker unit for Bedrock. Today it joins a server, tracks what
it believes about itself and serves that over a local status API. Design and
standing rules: `docs/specs/2026-10-14-crew-design.md`.

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `MC_HOST`, `MC_USERNAME` | required | Server and the Microsoft account name |
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

`lab/compose.yaml` is a throwaway Bedrock server with cheats on. For a local
run without a Microsoft account, set `ONLINE_MODE: "false"` and
`ALLOW_LIST: "false"` in an uncommitted `compose.override.yaml`; crew itself
always authenticates, so an offline server needs a local, uncommitted build.

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
