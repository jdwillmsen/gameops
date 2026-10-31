# wildlife-evidence

Video evidence of the wildlife animals (and anything else a crew unit's camera
can see) as MP4s with the run's timeline burned in as captions.

**What the pictures are.** The crew camera's software re-render of game
packets: Chrome with SwiftShader (no GPU) draws a three.js scene on a private
Xvfb display and ffmpeg grabs it. It is **not** a real Bedrock client's pixels.
Compared with a real client it lacks shaders, the game UI, particles, water and
lighting detail, and the animals are posed by the pack's animation expressions
evaluated by the page (summed, not blended), not by the game's engine. Every
video carries a banner saying so. Videos are evidence bundle files under
`~/evidence/wildlife/<run-id>/videos/`; never commit them.

## One command

```
tools/wildlife-evidence/wildlife-clips.sh            # all clips, boots and removes its own preview
tools/wildlife-evidence/wildlife-clips.sh --only tiger,tiger-sit --attach   # reuse a running preview
```

It boots the wildlife pack on a throwaway RakNet, offline Bedrock preview
(never the live lab or FWB), joins a unit with `CAMERA_ENABLED` and
`CAMERA_PACK_DIR`, and records: one clip per animal (`tiger`, `cheetah`,
`lion`, `black_leopard`, `brown_bear`, `hyena`) and a tamed tiger
(`tiger-walk`, `tiger-sit`, `tiger-attack`). Output: `videos/*.mp4|srt|json`,
`videos/index.json`, `timeline.jsonl`, `server.log`, `README.md`.

Needs `node`, `ffmpeg` (libx264), `Xvfb`, Chrome, docker, Go, and
`playwright-core` (point `PW_DIR` or `--pw-dir` at a directory that contains
`node_modules/playwright-core`). The first run downloads the vanilla texture
atlas into `~/.cache/wildlife-evidence/atlas`.

## Recorder alone

```
tools/wildlife-evidence/record.sh --unit http://127.0.0.1:18151 --out clip.mp4 \
  --follow gameops:tiger --seconds 15 --spin 20 --timeline timeline.jsonl --scenario tame \
  --run-epoch-ms 1761870000000
```

`--help` lists everything. `--timeline` takes `{t_ms, source, scenario, event, ...}`
lines (t_ms from the run's start); `--run-epoch-ms` is the wall-clock time of
`t_ms` 0 so captions line up with the video. `--drive CMD` runs a command a
few seconds into the clip (to trigger what is being filmed). The page's clock
is pinned to noon (`--time live` to follow the server): entities are unlit at
night. The unit must stand within a few chunks of the subject, or the terrain
around it is not streamed. `attach-videos.mjs <bundle>` adds `videos/<id>.mp4`
to the scenario with that id in `report.json` (for the scenario runner:
record each scenario to `videos/<scenario id>.mp4`).

## Measured (8-core devbox, no GPU, 1280x720)

Rendered page frame rate with SwiftShader: **about 23-27 fps mean, 18-23 fps
worst second** while following one animal with about 20 entities in view. The
H.264 file is captured at 30 fps, so slower seconds repeat frames; the real
rate per second is in each clip's `.json` (`render.per_second`). Size is
roughly 0.5 MB/s at CRF 20, yuv420p, faststart.

Known limits: sitting is only shown when the server sends the sitting flag (a
console `event ... gameops:on_sit` does not), so `tiger-sit` forces the pose
and says so on screen; attacks are inferred from the victim's hurt event.
