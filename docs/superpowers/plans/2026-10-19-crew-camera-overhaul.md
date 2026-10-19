# Crew camera overhaul: look like Minecraft

Jira: JDW-113 (under the JDW-59 epic). Asked for by the owner on 2026-10-19:
"drastically overhaul and improve the look and design and feel of these live
camera views … make sure all the mobs look accurate … true to Minecraft as it
can be". The owner skipped the design questions, so the calls below are mine.

## Where it stands

The rendered camera (`minecraft/crew/internal/camera`, page in `web/`) draws
textured cubes from the bedrock-samples atlas. Its gaps, in order of how much
they hurt:

1. **Mobs are invented boxes** (`ENTITY_TYPES` in `web/index.html`): guessed
   colours, no textures, no animation, and they snap from tick to tick.
2. **Rough terrain**: nearest-filtered atlas with no mipmaps, so distant
   ground shimmers into moiré; no antialiasing; flat per-face shades with no
   ambient occlusion, so terrain reads as hard-edged and flat.
3. **No block state on the wire**: the decoder keeps only names, so stairs all
   rise towards +z, slabs are always bottom, logs always stand up, doors,
   trapdoors, torches and furnaces face one way, fences and panes never
   connect, water is a full cube.
4. **No light or sky**: caves are as bright as noon, flat sky colour, no sun,
   moon, clouds or day/night.
5. Players are blue boxes, not their skins.

## Decisions (mine)

- Keep the architecture: Go hub → binary websocket → three.js page, with
  Mojang's bedrock-samples fetched at runtime and never committed. The
  true-pixel mirror camera (JDW-65) stays a separate spike.
- Mob models come from the pack's own data: client entity definitions
  (`resource_pack/entity/*.entity.json`), geometry
  (`resource_pack/models/entity/*.geo.json`) and entity textures. Crew's Go
  side resolves, bounds-checks and compiles them into one JSON table plus one
  texture sheet; the page only builds meshes. Model parsing follows the rules
  `minecraft/mcmap/internal/icons/geometry.go` already proved (inheritance,
  per-face UV, mirror, inflate, pivots, bind pose, hard size limits). It is an
  `internal` package of mcmap, so crew gets its own copy of the parts it
  needs rather than a cross-component refactor now.
- Which geometry and texture a mob uses: the definition's `default` entries.
  Render-controller variants (cat breeds, villager professions) are out of
  scope for this pass; baby scale and sheep wool colour come from entity
  metadata because they are cheap and very visible.
- Animation is procedural by bone name (legs swing with ground speed, arms
  counter-swing, head follows head yaw and pitch, idle bob) rather than
  evaluating the pack's Molang animations. Positions are interpolated between
  ticks.
- Block textures move to a `DataArrayTexture` (one layer per tile) with
  generated mipmaps, drawn by one custom shader: no tile bleeding, no moiré,
  and one place for AO, light, fog and day/night. MSAA on, device pixel ratio
  capped at 2.
- Block state goes on the wire (wire version 4): materials become name plus
  the state properties the mesher uses, with 16-bit material ids.
- Light is computed in the mesher worker (Bedrock never sends light): sky
  light from the column above, block light from known emitters, flood-filled
  within loaded sub-chunks, plus vertex ambient occlusion.
- Biome tint stays fixed to plains (biome data is not decoded); out of scope.
- Performance budget: radius-4 tile must still settle in about a second and
  hold 60 fps on a laptop GPU; a four-tile camera wall must stay usable.
  Software-GL numbers in the README are the regression check.

## Phases, each its own PR shipped to production

### Phase 1 — Renderer core (page only)

- Slice the atlas PNG client-side into a `DataArrayTexture` using the table's
  tile rects; generate mipmaps; `NearestMipmapLinear`/`Nearest` mag; anisotropy
  where supported.
- One `ShaderMaterial` (or `onBeforeCompile`) for blocks: layer index + tile
  UV attributes, vertex colour × tint, Bedrock face shades (top 1.0, north and
  south 0.8, east and west 0.6, bottom 0.5), AO, alpha test for cutouts,
  translucency pass for water/glass/ice, fog.
- Vertex AO in `mesher.js` (classic 3-neighbour corner rule) with quad flip to
  avoid anisotropic AO artefacts; leaves and cross-plants get none.
- Sky: gradient sky dome with horizon fog matching it; fog distance tied to
  radius.
- `antialias: true`, `setPixelRatio(min(devicePixelRatio, 2))`.
- Water surface sits at 14/16, translucent, faces against water culled.
- Keep `window.__stats`, the HUD overlay and the culling intact.

### Phase 2 — Real mob models

- Go: fetch the entity definitions, geometry and the entity textures they name
  (only for definitions that resolve), invalidating old caches. Compile to
  `/v1/camera/entities.json` (type → bones with pivot, rotation, cubes with
  per-face UV in sheet coordinates, texture size, hit-box height) and
  `/v1/camera/entities.png`. Bound everything like mcmap does.
- Page: `web/entities.js` builds a `THREE.Group` per type from the table, with
  `MeshLambertMaterial`-style lighting and alpha test; procedural animation and
  interpolation; fallback to the current stand-ins while the table is loading
  or for a type it lacks.
- Wire: entity flags (baby, sheared) and sheep colour from `AddActor` /
  `SetActorData` metadata; dropped items carry their item name so they draw as
  a small spinning item icon from the item sheet.
- Name labels keep working; labels look like the game's (dark translucent
  plate, white text).

### Phase 3 — Block states and shapes

- Decoder resolves name + properties (dragonfly `RuntimeIDToState`); the
  material key is the name plus the subset of properties the mesher reads.
  Wire v4 with u16 material ids everywhere (frames, hub storage, worker).
- Mesher shapes: stairs (facing, upside down, inner/outer corners from
  neighbours), slabs (top/bottom/double), pillars (`pillar_axis`), doors and
  trapdoors (facing, open, hinge, upper/lower, top/bottom), fences, walls,
  panes and bars connecting to neighbours, wall torches and lanterns,
  directional fronts (furnace, pumpkin, dispenser, chests as boxes), snow
  layers by height, liquid levels by depth, carpets, crops by age, double
  plants' upper half.

### Phase 4 — Player skins

- Hub keeps each player's skin from `PlayerList` (and `PlayerSkin` updates):
  64×64 or 128×128 classic or slim. Serve it as a PNG at
  `/v1/camera/skins/<entity id>.png`; the page applies it to the pack's
  humanoid geometry (slim arms when the skin says so). Fall back to the pack's
  Steve/Alex. Skins over the size limit or with custom geometry use the
  fallback.

### Phase 5 — Light, time and sky

- Wire: world time and daylight cycle from `StartGame`/`SetTime`, weather from
  `LevelEvent` rain start/stop.
- Mesher: sky light and block light per vertex (smooth lighting), shader mixes
  them with a day factor so night is dark and torches glow.
- Sky: day/night colours, sun and moon (pack `textures/environment`), stars,
  flat moving clouds; rain darkens the sky.
- Animated water and lava from the pack's flipbook textures.

## Verification for every phase

- `go test ./minecraft/crew/...` and `go vet`.
- Local lab (`minecraft/crew/lab/compose.yaml`, service `lab`) with a locally
  built crew (`MC_OFFLINE=true CAMERA_ENABLED=true`), a test scene placed
  with console commands (mobs summoned beside the unit, stairs, slabs, doors,
  fences, water, a cave), and before/after screenshots from headless Chromium
  (playwright-core). Screenshots go in the PR and on JDW-113.
- Page frame time and settle time from `window.__stats` at radius 4, compared
  with the current release.
- After merge: release, jdw-deployments image bump, confirm the new version
  serves at crew.prd.jdwlabs.com/camera.
