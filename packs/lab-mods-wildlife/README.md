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

These names are stable, because tamed pets saved in a world depend on them; never rename or remove one:

- Component groups `gameops:wild`, `gameops:tamed`, `gameops:tamed_active`, `gameops:sitting`, and `gameops:ability_ready`, `gameops:ability_active`, `gameops:ability_tired` (tiger and cheetah only).
- Events `gameops:on_tamed`, `gameops:on_sit`, `gameops:on_stand`, and `gameops:ability_ready`, `gameops:ability_active`, `gameops:ability_tired` (same two species).
- Entity property `gameops:sitting` (bool), mirrored by the sit and stand events because script cannot read the sittable state.
- Dynamic property `gameops:owner` (the owner's player id), written by script when the tame event fires. `minecraft:tameable` leaves with the wild group, so the owner cannot be read later. Pets tamed before this property existed have none: they keep every self and pet aura, but the lion roar and the bear guard have no owner to buff until the pet is tamed again.

Tamed state for the auras is the `minecraft:is_tamed` marker in `gameops:tamed`.

## Test hooks

`/scriptevent` ids for lab testing, not gameplay:

- `gameops:tame` tames the nearest untamed `gameops_wildlife` animal within 64 blocks to the player who runs it.
- `gameops:effects <player name>` logs `gameops:effects <name> strength=<amplifier|none> resistance=<amplifier|none>`; the smoke uses it to prove a lion tamed by a joined bot gives that bot Strength.
- `gameops:perf` logs one `gameops:perf mean_tick_ms=... ticks=200` line after 200 ticks. An idle server reads 50.0 ms whatever the load, so the probe only detects lag; the smoke also prints `docker stats` CPU and memory before and after summoning for cost.
- `gameops:census` logs one `gameops:census tiger=<n> cheetah=<n> lion=<n> black_leopard=<n> brown_bear=<n> hyena=<n>` line counting loaded animals of each type in all three dimensions; it works from the server console.
- At startup the script logs `gameops:wildlife script loaded`; `tools/lab-preview-smoke.sh` looks for it.

## Manual checklist

For a human with a real client; the headless smoke cannot cover these. Start a
preview with `tools/lab-preview up packs` (it prints the join address) and
accept the resource pack.

The first four items need two clients and are the central safety rule: no
animal or pet may ever target or hurt a player. Running them is a condition of
the owner's approval to roll the pack out.

- Player B hits the owner: the owner's pet ignores player B.
- The owner hits player B: the pet ignores player B.
- The owner hits their own pet: it does not fight back.
- With a pet sitting, a wild rival nearby does not make it attack.

- Tame each animal with its food: tiger raw beef, cheetah raw rabbit, lion raw mutton, black leopard raw chicken, brown bear raw salmon, hyena rotten flesh (`gameops:tame` is the shortcut).
- A tamed pet follows you, sits and stays on interact, and stands again.
- A pet defends you when you are hit.
- Abilities: tiger charge then the tired phase and cheetah sprint, both starting when the animal lands a hit (never on a timer alone); lion roar; black leopard at night only; brown bear guard; a hyena pack of four.
- A lion and a hyena fight each other on sight.
- Hit a wild animal: it never attacks you.
- A second player cannot sit another player's pet.
- Spawn eggs exist and spawn each animal.
- Models and textures look right; the mane is hidden on the female lion.
- Strength and resistance effects actually appear on the recipients (the startup log warns if an effect id is unknown).

## Known gaps

- No command function places a wild group; use spawn eggs or `/summon`.
- No wolf-style heal-by-feeding and no dyeable collar.
- Lion and tiger take livestock only, not rabbit or chicken.
- Wild animals stay because they have no despawn component, not because they carry `minecraft:persistent`.
