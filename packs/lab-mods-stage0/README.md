# lab-mods-stage0

Stage-0 spike for custom entities on the crew lab: one small hostile mob,
`gameops:training_dummy_brute`, as a behaviour pack plus a resource pack. All
art and JSON here is our own; nothing is copied from the vanilla packs.

| Path | What |
|---|---|
| `behavior_pack/` | Manifest and the entity definition (health 40, `minecraft:attack` 4, melee goal, target goal) |
| `resource_pack/` | Manifest (depends on the behaviour pack header UUID), client entity, box model, render controller, 64x64 texture, lang file |
| `assets-src/make_texture.py` | Paints the texture; not shipped in the packs |

## Behaviour

The mob only targets entities in the family `arena_target` (and explicitly
never `player`). It has no `hurt_by_target` goal, so a player hitting it does
not make it retaliate, and it does not use `minecraft:boss`. To fight it, give
a target mob the family `arena_target`; with none around it just wanders.

## Build the add-on

`.mcaddon` is a zip holding both pack folders. From this directory:

```sh
python3 -I assets-src/make_texture.py resource_pack/textures/entity/training_dummy_brute.png   # only if the art changed
mkdir -p ../../out
python3 -I -m zipfile -c ../../out/lab-mods-stage0.mcaddon behavior_pack resource_pack
```

`out/` is git-ignored. `python3 -m zipfile` is used so no extra tool is
needed; `zip -r` produces the same layout.

## Lab rollout (not done yet)

**Rolling this out restarts the lab server and kicks everyone on it. It needs
owner approval first (risk tier R3: server restart plus a change to the lab's
deployment config). Do not start it without that approval.**

Steps, in the `jdw-deployments` crew-lab chart (not in this repo):

1. Deliver the two pack folders through the existing crew-lab mechanism. A
   textured pack is far too big for a ConfigMap in general, but this one is a
   few KB, so a ConfigMap is enough for the spike (limit: 1 MiB including
   base64, so about 780 KB of raw bytes; `tools/check-packs.sh` prints the
   current size).
2. Set `FORCE_PACK_COPY=true` on the server. Without it the image copies a
   pack only when it is absent, and the lab keeps serving the old one after an
   update.
3. Register both packs (UUID and version from the manifests) in the world's
   `world_behavior_packs.json` and `world_resource_packs.json`, the way the
   crew unit-kit pack is already merged in.
4. Enable `content-log-file-enabled=true` so the boot can be checked for pack
   errors.
5. Restart the server. A changed entity JSON is not picked up by `reload`;
   only a restart applies it.

### Version-bump rule

Bump `version` in **both** manifests on every change that ships (header and
module, plus the resource pack's dependency on the behaviour pack, plus the
matching entries in the `world_*_packs.json` files). Clients cache resource
packs by UUID and version, so an unbumped change shows stale models and
textures.

### Stage-0 success criteria

- `/summon gameops:training_dummy_brute` works.
- A retail client sees the custom model and texture.
- A Go bot joins and stays connected.
- The content log has no errors and no "pack not found" lines.
