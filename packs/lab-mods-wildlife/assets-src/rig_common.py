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


COLLAR = "collar"


def collar_shape(chest_origin, chest_size, z: float) -> tuple:
    """A two-pixel band around the front of the chest, just proud of it, starting `z` pixels behind its front."""
    (ox, oy, oz), (w, h, _) = chest_origin, chest_size
    return (COLLAR, COLLAR, (ox - 0.5, oy - 0.5, oz + z), (w + 1, h + 1, 2))


def _free_slot(taken, rw, rh):
    for y in range(TEXTURE - rh + 1):
        for x in range(TEXTURE - rw + 1):
            if not any(x < tx1 and tx0 < x + rw and y < ty1 and ty0 < y + rh for tx0, ty0, tx1, ty1 in taken):
                return x, y
    raise ValueError("collar does not fit the texture")


def pack(shapes, label="parts") -> list:
    """Cubes with box-UV origins packed into rows of the texture, tallest first.

    shapes: (bone, name, origin, size) tuples. The collar is placed last in free space, so adding it
    leaves every other cube's UV where it was.
    """
    collar = [s for s in shapes if s[1] == COLLAR]
    parts = _pack([s for s in shapes if s[1] != COLLAR], label)
    for bone, name, origin, (w, h, d) in collar:
        rw, rh = 2 * d + 2 * w, d + h
        taken = [(p.uv[0], p.uv[1], p.uv[0] + 2 * p.size[2] + 2 * p.size[0], p.uv[1] + p.size[2] + p.size[1])
                 for p in parts]
        x, y = _free_slot(taken, rw, rh)
        parts.append(Part(bone, name, origin, (w, h, d), (x, y)))
    return parts


def _pack(shapes, label) -> list:
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
