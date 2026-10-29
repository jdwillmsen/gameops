"""Box-UV packing and the geometry document shared by every rig."""
from dataclasses import dataclass

TEXTURE = 128


@dataclass(frozen=True)
class Part:
    bone: str
    name: str
    origin: tuple
    size: tuple
    uv: tuple


def pack(shapes, label="parts") -> list:
    """Cubes with box-UV origins packed into rows of the texture, tallest first.

    shapes: (bone, name, origin, size) tuples.
    """
    rects = [(2 * d + 2 * w, d + h) for _, _, _, (w, h, d) in shapes]
    order = sorted(range(len(shapes)), key=lambda i: (-rects[i][1], i))
    uvs = {}
    x = y = row_h = 0
    for i in order:
        rw, rh = rects[i]
        if x + rw > TEXTURE:
            x, y, row_h = 0, y + row_h, 0
        uvs[i] = (x, y)
        x += rw
        row_h = max(row_h, rh)
    if y + row_h > TEXTURE:
        raise ValueError(f"{label} do not fit the texture")
    return [Part(b, n, o, sz, uvs[i]) for i, (b, n, o, sz) in enumerate(shapes)]


def document(identifier, bones, top) -> dict:
    return {
        "format_version": "1.12.0",
        "minecraft:geometry": [{
            "description": {
                "identifier": identifier,
                "texture_width": TEXTURE,
                "texture_height": TEXTURE,
                "visible_bounds_width": 3,
                "visible_bounds_height": 2,
                "visible_bounds_offset": [0, round(top / 32, 2), 0],
            },
            "bones": bones,
        }],
    }


def bone_entries(pivots, parts, parents, rotations=None) -> list:
    out = []
    for name, pivot in pivots.items():
        entry = {"name": name, "pivot": pivot}
        if name in parents:
            entry["parent"] = parents[name]
        if rotations and name in rotations:
            entry["rotation"] = rotations[name]
        entry["cubes"] = [{"origin": list(c.origin), "size": list(c.size), "uv": list(c.uv)}
                          for c in parts if c.bone == name]
        out.append(entry)
    return out
