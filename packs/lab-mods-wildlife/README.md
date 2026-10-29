# lab-mods-wildlife

Tamable animals for the crew lab. The checker applies the `wildlife` policy
(`policy.txt`): animals never target or retaliate against players.

Entity, client and art files are generated and committed. Regenerate with
`python3 -I assets-src/generate.py`; `python3 -I assets-src/test_generators.py`
fails if the committed files differ from a fresh run.

| Path | What |
|---|---|
| `assets-src/species.py` | The species table: stats, taming food, prey, rivals, variants, ability |
| `assets-src/build_behavior.py` | Entity JSON from a species |
| `assets-src/cat_rig.py` | Shared cat skeleton and per-species proportions (tiger, cheetah) |
| `assets-src/build_models.py`, `build_textures.py` | Geometry, animations and PNG textures  |
| `assets-src/generate.py` | Writes everything into the pack |

Component groups `gameops:wild`, `gameops:tamed`, `gameops:sitting` and events
`gameops:on_tamed`, `gameops:on_sit`, `gameops:on_stand` are stable: tamed pets
saved in a world depend on them.

## Test hooks

`/scriptevent` ids for lab testing, not gameplay:

- `gameops:tame` tames the nearest `gameops:` animal within 64 blocks to the player who runs it.
- `gameops:perf` logs one `gameops:perf mean_tick_ms=... ticks=200` line after 200 ticks.
