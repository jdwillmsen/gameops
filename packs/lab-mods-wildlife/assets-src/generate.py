"""Writes every generated file of the pack. Run: python3 -I generate.py [pack-dir]

Every species in species.SPECIES is written.
"""
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import build_behavior  # noqa: E402
import build_models  # noqa: E402
import build_textures  # noqa: E402
from species import SPECIES, Species  # noqa: E402

PACK = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def dump(doc) -> bytes:
    return (json.dumps(doc, indent=2) + "\n").encode()


def client_entity(s: Species) -> dict:
    textures = {v: path for v, path in build_textures.texture_names(s)}
    return {
        "format_version": "1.10.0",
        "minecraft:client_entity": {
            "description": {
                "identifier": f"gameops:{s.id}",
                "materials": {"default": "entity_alphatest"},
                "textures": textures,
                "geometry": {"default": f"geometry.gameops.{s.id}"},
                "animations": {n: build_models.ANIMATION_PREFIX + n for n in build_models.ANIMATION_NAMES},
                "scripts": {"animate": [
                    "walk", "idle",
                    {"sit": "query.is_sitting"},
                    {"attack": "variable.attack_time > 0"},
                ]},
                "render_controllers": [f"controller.render.gameops.{s.id}"],
                "spawn_egg": {"base_color": s.egg[0], "overlay_color": s.egg[1]},
            },
        },
    }


def render_controller(s: Species) -> dict:
    ctl = {"geometry": "Geometry.default", "materials": [{"*": "Material.default"}]}
    if s.id == "lion":
        # The female shares the male's geometry, so her mane bone is hidden rather than absent.
        male = f"query.variant == {s.variants.index('male')}"
        ctl["part_visibility"] = [{"*": True}, {"mane": male}, {"mane_chest": male}]
    if s.variants:
        ctl["arrays"] = {"textures": {"Array.skins": [f"Texture.{v}" for v in s.variants]}}
        ctl["textures"] = ["Array.skins[query.variant]"]
    else:
        ctl["textures"] = ["Texture.default"]
    return {"format_version": "1.8.0", "render_controllers": {f"controller.render.gameops.{s.id}": ctl}}


def lang(species) -> bytes:
    lines = []
    for s in species:
        name = s.id.replace("_", " ").title()
        lines += [f"entity.gameops:{s.id}.name={name}", f"item.spawn_egg.entity.gameops:{s.id}.name={name} Spawn Egg"]
    return ("\n".join(lines) + "\n").encode()


def files() -> dict:
    species = list(SPECIES.values())
    out = {
        "resource_pack/animations/cat.animation.json": dump(build_models.animations()),
        "resource_pack/texts/en_US.lang": lang(species),
    }
    for s in species:
        out[f"behavior_pack/entities/{s.id}.json"] = dump(build_behavior.entity_json(s))
        out[f"resource_pack/entity/{s.id}.entity.json"] = dump(client_entity(s))
        out[f"resource_pack/models/entity/{s.id}.geo.json"] = dump(build_models.geometry(s))
        out[f"resource_pack/render_controllers/{s.id}.render_controllers.json"] = dump(render_controller(s))
        for variant, path in build_textures.texture_names(s):
            out[f"resource_pack/{path}.png"] = build_textures.paint(s, variant)
    return out


def generate(pack_dir=PACK) -> list:
    written = []
    for rel, data in files().items():
        path = os.path.join(pack_dir, rel)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "wb") as f:
            f.write(data)
        written.append(rel)
    return written


if __name__ == "__main__":
    for rel in generate(sys.argv[1] if len(sys.argv) > 1 else PACK):
        print(rel)
