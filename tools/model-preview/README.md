# model-preview

Clips of the wildlife pack's models, rendered in the web Blockbench, for quick
art review. Models only, no gameplay.

```
tools/model-preview/render.sh [--animal tiger,lion] [--fast] [--out DIR] [--pack DIR]
tools/model-preview/render.test.sh     # smoke test: one animal, fast mode
```

Output goes to `~/evidence/wildlife/<run-id>/models/` (the evidence bundle
layout) unless `--out` is given. Per animal and skin variant: a 360 degree
`<animal>_<skin>.turntable.mp4`, one loop per pack animation
(`walk`, `idle`, `sit`, `attack`), and one `<animal>.contact-sheet.png` (a row
per variant: four turntable angles, then one frame per animation). Videos are
never committed. `--fast` renders eight turntable frames and a short walk clip.

## How it works

- Models, skins and animations come from the pack itself: each
  `resource_pack/entity/*.entity.json` names the geometry, textures and
  animations, and the render controller's `part_visibility` hides the bones
  a variant does not show (the lioness has no mane).
- Blockbench poses the model. The pack's Molang animation expressions are
  evaluated here (`bake.mjs`) into plain keyframes, which Blockbench loads
  through its own animation loader and samples per frame. The game layers
  `idle` under every clip, so a clip is the sum of its layers. Runtime queries
  (distance moved, speed, attack time, look target) are pinned to fixed values
  per clip, so a clip shows the animation, not a locomotion simulation.
- Frames are grabbed one by one from the WebGL canvas (deterministic, no screen
  recording) and encoded with ffmpeg (libx264, 24 fps).

## Pins and failure modes

Blockbench is an external web app. The tool is pinned to Blockbench 5.2.2: the
first run records `web.blockbench.net` to
`~/.cache/model-preview/blockbench-5.2.2.har` (needs network once, ~20 MB) and
every later run replays that file offline. The run aborts if the loaded
version differs, if the scene has no grid group to hide, if a model or clip
fails to load, or if a moving clip renders identical frames or a static clip
equals the rest pose.

Needs node, npm, ffmpeg with libx264 and Chrome (`CHROME=/path` to override
`/opt/google/chrome/chrome`). `playwright-core` is installed on the first run.
