# Crew rendered camera spike (JDW-66)

Date: 2026-10-15. Lab only: an offline-mode lab server, a local override, no
Microsoft account. Nothing here touched FWB.

## Answer

**Yes.** A browser can draw a recognisable first-person view of a crew unit
from only what crew's own Bedrock connection receives: chunks, entities and
the unit's own position and look. The prototype shows terrain, trees, water,
a placed platform and mob boxes, and the view follows the unit when it is
moved and turned with `/tp ... facing`. No extra client, account or server
command was used.

What the spike does not settle: textures (flat colours with per-block
jitter only), non-cube blocks (skipped), and cost at production scale (one
unit, one viewer, a lab world).

## Screenshots

Taken with headless Chrome driven by playwright-core, software GL
(SwiftShader), 1024x576. The Playwright MCP browser was busy, so a
scratch script was used.

- `docs/specs/assets/2026-10-15-crew-camera-first-view.png`: unit at spawn,
  inside a tree canopy (so the view is mostly leaves).
- `docs/specs/assets/2026-10-15-crew-camera-facing-south.png`: unit on a
  placed stone platform, facing south, a mob box left and trees ahead.
- `docs/specs/assets/2026-10-15-crew-camera-facing-west.png`: same spot after
  a `/tp @a ... facing` cheat; HUD reads yaw 90 and the scene has rotated.

## Measurements

One unit, view distance 8, lab world, Linux host. Crew CPU is from
`/proc/<pid>/stat`, a percentage of one core. The viewer's own CPU (Chrome
with software GL) is not counted.

| Phase | Crew CPU | Crew RSS | Bytes/s to viewer (raw / deflate est.) |
|---|---|---|---|
| Join + chunk load, 0 viewers (25 s window) | 2.5% | 143 MB | 0 |
| Idle, 0 viewers (30 s) | 0.7% | 143 MB | 0 |
| Viewer connects, replay of held chunks (12 s) | 15% | 142 MB | 2.8 MB/s raw, 0.33 MB/s deflated |
| Idle, 1 viewer (30 s) | 3.7% | 134 MB | 18.6 KB/s raw, 19.8 KB/s est. |
| Idle, 1 viewer (next 15 s) | 2.9% | 138 MB | 13.7 KB/s raw, 14.6 KB/s est. |

- Whole-world replay to a new viewer: 8,100 sub-chunks, 33.5 MB raw, about
  3.9 MB by my per-message deflate estimate. The real websocket uses
  permessage-deflate with context takeover, so the wire figure is likely
  lower; it was not captured on the socket.
- Idle traffic is entity movement as JSON (about 24 entities wandering).
  Small messages deflate badly, so the estimate is a little above raw.
- Time to first rendered frame: 320 to 470 ms after page load (first
  non-empty mesh). Time until all sub-chunks were meshed and the view
  settled: 3 to 8 s. That is bound by software GL meshing in the browser,
  not by crew.
- Server-side decode: 8,100 sub-chunks decoded in 242 ms of crew CPU total
  (about 30 us each), including conversion to the wire format.
- The idle-viewer CPU includes spike overhead: per-message flate used only
  to count bytes, and JSON for every entity move. Production would drop both.
- Binary size: 16.6 MB to 23.2 MB (+6.6 MB) from the dragonfly dependency.
- No baseline run of an identical crew with the hub switched off was taken,
  so the 0-viewer RSS includes the hub's 33 MB of held sub-chunks
  (8,100 x 4 KiB).

## Chunk-format findings

- The lab server (Bedrock 1.26.52, protocol 2193, gophertunnel 1.62) uses the
  **sub-chunk request system** for the overworld. In this protocol revision
  `LevelChunk.SubChunkCount` is **0 with `SubChunkLimit` set** (9 or 10 in the
  lab). The old sentinel counts do not appear and gophertunnel rejects counts
  above 64. The `LevelChunk` payload then carries only biomes.
- Crew must send `SubChunkRequest` per chunk. Base position is
  (chunk x, sub-chunk Y 0, chunk z) with offsets from -4 (the world floor) up to
  the limit. Requesting `limit` sub-chunks from -4 worked: 8,140 requested,
  8,139 `Success`, 1 `SuccessAllAir`. The limit is per chunk (the highest
  populated sub-chunk), so blocks built above it arrive only as block updates.
- Block IDs on the wire are **network block hashes**, not runtime IDs.
  `chunk.NetworkDecode` returns the hashes untouched. They need
  `world.DefaultBlockRegistry.HashToRuntimeID`, then `RuntimeIDToState`, to
  get a block name. Zero unmapped blocks in the lab world, so dragonfly's
  v0.11.5 table matches this server version; an older table would show
  unmapped blocks as a grey placeholder. Do not assume the match holds after
  a server upgrade.
- `GameData.PlayerPosition` is a placeholder: its Y is offset by 32768. The
  real position arrives in the `Respawn` (ReadyToSpawn) packet. This also
  affects `internal/belief`: its snapshot is only right because it handles
  `Respawn`.
- Sub-chunk payloads are version 9 with an absolute Y index. dragonfly only
  decodes sub-chunks inside a whole chunk, so the spike appends a minimal
  biome tail and decodes with count 1. About 0.4% (33 of 8,139) failed with
  `cannot read paletted storage (size=5) ... biomePaletteEncoding`, so some
  payloads carry extra data that this wrapper misreads. Production should
  write a small decoder of its own for sub-chunk payloads.
- Block changes arrive as both `UpdateBlock` and `UpdateSubChunkBlocks`
  (a `/fill` uses the latter). Both are handled.
- Own look: the server sends `MovePlayer` for a teleport. A `/tp` to the same
  position with a new facing sent no packet, so yaw did not change. A unit
  that turns by itself will need to track its own outgoing
  `PlayerAuthInput` rotation.
- During the spike the lab's `compose.yaml` set no `TRANSPORT`. The image's
  server.properties defaulted to `transport=nethernet`, which crew's RakNet
  dial cannot reach (it timed out in MTU discovery). The spike set
  `TRANSPORT: raknet` and `ALLOW_LIST: "false"` in the local override. The
  same change that adds these findings now pins `TRANSPORT: raknet` in
  `compose.yaml`, and FWB already runs RakNet deliberately.
- Offline-mode join works: with no `TokenSource`, `IdentityData.DisplayName`
  set, and `ONLINE_MODE: "false"`, gophertunnel joins. The server sanitised
  the name `crew-1` to a different display name, so `/tp crew-1` matched
  nothing; `@a` or `@p` worked.

## What was reused, and licences

- **dragonfly** `github.com/df-mc/dragonfly` v0.11.5 (MIT): `server/world/chunk`
  decoding and the block state registry (`server/block`, imported for its
  side effect of registering states). Pulls in `worldupgrader`
  (MIT), `fasthash`, `intintmap`, `go-toml` and bumps `golang/snappy` to
  v0.0.4. `go build ./minecraft/...` still passes.
- **gophertunnel** (already a dependency): dial, packets.
- **coder/websocket** (already a dependency): the socket, with
  permessage-deflate.
- **three.js** 0.170 (MIT): loaded from a CDN by the page. Not vendored.
- **prismarine-viewer** (MIT) and the `bedrock-viewer` fork: read for the
  idea (per-sub-chunk mesh, draw only faces next to a non-opaque
  neighbour). No code was copied, so there is no attribution owed beyond the
  note in the page's comment.
- mcmap's chunk code reads LevelDB on disk, not the network format, so it was
  not reusable here.

## Design as built

- `minecraft/crew/internal/camera/hub.go`: `Hub.Apply(pk, conn)` is called for
  each packet in crew's read loop. It decodes chunks on the packet goroutine,
  converts to one 4096-byte array of material IDs per sub-chunk, and keeps
  these in memory so a new viewer gets a replay.
- Wire format: binary frame 1 = sub-chunk (x, y, z as int32, 4096 bytes),
  frame 2 = single block; text frames are JSON for materials, self, entity add or
  move, entity remove, reset.
- `server.go`: `/camera` (the page), `/v1/camera/ws`, `/v1/camera/stats`.
- `web/index.html`: three.js, vertex-coloured per-sub-chunk meshes, nearest-first
  meshing with a time budget, fog, entities as boxes.
- `cmd/crew/main.go`: with `CREW_SPIKE_OFFLINE=1`, skips auth, dials offline
  and mounts the camera. Without it, crew behaves as before.
- The viewer drop policy is minimal: a viewer 16,384 messages behind is cut.

## What production would need

- **Auth.** The websocket is open and unauthenticated. It must go behind
  crew-control (Wave 3), as the design's standing rules already say, and
  carry a short-lived token. Check `Origin`.
- **Microsoft accounts and rate limits.** Crew needs its own account on FWB;
  this spike never exercised it. The render path itself costs no extra
  account or server slot.
- **Back-pressure.** Per-viewer queues with coalescing (latest position wins
  for entities), a cap on viewers per unit, and a send budget while a
  replay is in flight. Replay should be lazy: nearest sub-chunks first, and
  only those inside the viewer's frustum or radius.
- **Memory.** Holding 33 MB of raw arrays per unit is wasteful. Store
  palette plus bit-packed indices, or skip fully enclosed underground
  sub-chunks (most of the 8,100). Send palettised sub-chunks (about 1 to 2 KB
  mostly) instead of 4 KB arrays.
- **Texture atlas.** Needs a Bedrock block-to-texture map (resource pack
  names, per-face, tint for grass and foliage, biome colours, animated water),
  an atlas built at image build time, and models for non-cubes (stairs, slabs,
  plants, fences). None of it is in dragonfly.
- **Block state drift.** The hash table must track the server version. Pin
  the dragonfly version against the server version crew negotiates, or load
  the server's own block palette from `StartGame` (`GameData.CustomBlocks`
  covers only custom blocks), and alert on unmapped blocks.
- **Decoder.** Replace the biome-tail trick with a purpose-written decoder,
  and cover the 0.4% that failed.
- **Look and eye height.** Track own rotation from outgoing input; render
  other players at the right eye offset; add a head turn from `MoveActor*`.
- **Entity models and names.** Boxes are fine for a status tile, not for
  identifying a mob.
- **FWB considerations.** Chunk and entity data from FWB is world data of a
  live server. It stays in-cluster behind crew-control and must not be served
  before the FWB rollout gate. No cheats are available there, so scene
  setup for demos stays lab-only. The unit's view sees what a player would, but
  it is also a map of a base: treat console access as sensitive.
- **Tests.** Replay recorded lab packet fixtures (the design already asks
  for this) through the hub and assert the sub-chunk frames.
- **Frame cost.** Meshing 8,100 sub-chunks took 3 to 8 s on software GL; real
  GPUs and web workers will be faster, but a camera wall of several tiles needs
  a smaller radius per tile (for example 4 chunks) and meshing in a worker.

## Recommendation for the console camera

Build the rendered camera as the default console tile. It is cheap on the
server side (about 2 to 4% of a core and 15 to 20 KB/s per viewer idle in
the lab, ~30 us per sub-chunk decode), needs no extra account, and the answer
to "does it read as the unit's view" is yes. Keep the mirror-camera spike
(Wave 2b) only as a fidelity fallback for full-quality shots. Do the
production work above in crew-control's console in this order: auth and
back-pressure, palettised wire format with lazy replay, then textures.
Start tiles at a 4-chunk radius.

## Follow-up tasks to file

1. Rendered camera v1: move the hub out of spike quality (tests, config,
   a flag, no global registry init side effects), behind crew-control auth.
2. Own sub-chunk payload decoder; investigate the 0.4% decode failures.
3. Palettised wire format, lazy nearest-first replay, per-viewer
   back-pressure and viewer cap.
4. Texture atlas and block model pipeline (Bedrock resource pack to atlas,
   non-cube blocks, biome tint).
5. Block-hash table drift check against the running server version, with a
   metric for unmapped blocks.
6. Fix `internal/belief` and the camera to treat `GameData.PlayerPosition` as
   unreliable (Y offset by 32768) and to track own rotation from input.
7. Done with these findings: the lab compose now pins `TRANSPORT: raknet`.
   FWB also runs RakNet deliberately (jdw-deployments README, "NetherNet,
   and why this server is not on it"), so this matches production. Was: pin `TRANSPORT` in
   the lab compose (and check FWB), since the image default
   moved to nethernet.
8. Camera wall perf test: N units by M viewers, with frustum-limited radius
   and worker meshing.
9. Entity rendering: per-type boxes or simple models, name labels, eye
   offset, head yaw.

## Reproducing

The prototype code is not merged; it lives on the branch
`spike/JDW-66-rendered-camera-prototype`. Check that branch out first.

```
cd minecraft/crew/lab
# local, uncommitted override
cat > compose.override.yaml <<'EOF'
services:
  lab:
    environment: {ONLINE_MODE: "false", ALLOW_LIST: "false", TRANSPORT: "raknet"}
EOF
docker compose -f compose.yaml -f compose.override.yaml up -d
cd ../../..   # back to the repository root
CREW_SPIKE_OFFLINE=1 MC_HOST=<lab container IP> MC_USERNAME=crew-1 \
  STATUS_ADDR=127.0.0.1:8080 go run ./minecraft/crew/cmd/crew
# open http://127.0.0.1:8080/camera ; stats at /v1/camera/stats
```

The lab was reached on the container IP. The loopback-published port timed out
before the transport was fixed, so it was never retried; it probably works with
`TRANSPORT: raknet`.
