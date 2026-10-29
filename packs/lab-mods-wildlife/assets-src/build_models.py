from species import Species

TEXTURE = 64


def geometry(s: Species) -> dict:
    # Stub model: one box per animal until the cat rig replaces it. 22 x 10 x 10 unfolds to 64 x 32.
    return {
        "format_version": "1.12.0",
        "minecraft:geometry": [{
            "description": {
                "identifier": f"geometry.gameops.{s.id}",
                "texture_width": TEXTURE,
                "texture_height": TEXTURE,
                "visible_bounds_width": 3,
                "visible_bounds_height": 2,
                "visible_bounds_offset": [0, 0.75, 0],
            },
            "bones": [{
                "name": "body",
                "pivot": [0, 8, 0],
                "cubes": [{"origin": [-5, 4, -11], "size": [10, 10, 22], "uv": [0, 0]}],
            }],
        }],
    }


def animations() -> dict:
    # Stub: poses are filled in with the rig; the names are what entities already reference.
    return {
        "format_version": "1.8.0",
        "animations": {
            f"animation.gameops.cat.{name}": {"loop": True, "bones": {}}
            for name in ("walk", "sit", "idle", "attack")
        },
    }
