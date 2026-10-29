# lab-mods-wildlife

Tamable animals for the crew lab. The checker applies the `wildlife` policy
(`policy.txt`): animals never target or retaliate against players.

Entity, client and art files are generated and committed. Regenerate with
`python3 -I assets-src/generate.py`; `python3 -I assets-src/test_generators.py`
fails if the committed files differ from a fresh run.

| Path | What |
|---|---|
| `assets-src/species.py` | The species table: stats, taming food, prey, rivals, variants, ability |
| `assets-src/bear_rig.py`, `hyena_rig.py` | Own skeletons for the brown bear (shoulder hump) and hyena (sloped back); same bone names so the shared animations apply |
| `assets-src/rig_common.py` | UV packing and geometry document shared by all rigs |
| `assets-src/build_behavior.py` | Entity JSON from a species |
| `assets-src/cat_rig.py` | Shared cat skeleton and per-species proportions (tiger, cheetah, lion, black leopard) |
| `assets-src/build_models.py`, `build_textures.py` | Geometry, animations and PNG textures  |
| `assets-src/generate.py` | Writes everything into the pack |

Component groups `gameops:wild`, `gameops:tamed`, `gameops:sitting` and events
`gameops:on_tamed`, `gameops:on_sit`, `gameops:on_stand` are stable: tamed pets
saved in a world depend on them.

## Test hooks

`/scriptevent` ids for lab testing, not gameplay:

- `gameops:tame` tames the nearest `gameops:` animal within 64 blocks to the player who runs it.
- `gameops:perf` logs one `gameops:perf mean_tick_ms=... ticks=200` line after 200 ticks.
- `gameops:census` logs one `gameops:census tiger=<n> cheetah=<n> lion=<n> black_leopard=<n> brown_bear=<n> hyena=<n>` line counting loaded animals of each type in all three dimensions; it works from the server console.
- At startup the script logs `gameops:wildlife script loaded`; `tools/lab-preview-smoke.sh` looks for it.
