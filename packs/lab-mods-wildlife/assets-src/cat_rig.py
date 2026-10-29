"""Shared four-legged cat skeleton, scaled per species by CatParams.

Model space is pixels, +y up, the animal faces -z, negative x is its right. Every size is an
integer so each cube's box-UV rectangle sits on the pixel grid.
"""
from dataclasses import dataclass

TEXTURE = 128
BONES = ("body", "head", "tail", "front_left_leg", "front_right_leg", "back_left_leg", "back_right_leg")


@dataclass(frozen=True)
class CatParams:
    body_w: int
    body_h: int
    body_len: int
    chest_len: int   # the front part of the body, which keeps the full height
    tuck: int        # how much shallower the rear body is than the chest
    leg_len: int
    leg_w: int
    head_w: int
    head_h: int
    head_d: int
    tail_len: int
    tail_w: int
    tail_droop: int = 0   # degrees the tail hangs below level
    scale: float = 1.0


# Tiger: big, heavy and broad. Cheetah: slender, long-legged, small head, long tail, deep chest.
PARAMS = {
    "tiger": CatParams(body_w=12, body_h=11, body_len=22, chest_len=11, tuck=0, leg_len=9, leg_w=5,
                       head_w=10, head_h=9, head_d=8, tail_len=18, tail_w=3, tail_droop=25),
    "cheetah": CatParams(body_w=6, body_h=7, body_len=20, chest_len=10, tuck=2, leg_len=8, leg_w=3,
                         head_w=5, head_h=5, head_d=5, tail_len=20, tail_w=2, tail_droop=12),
}


@dataclass(frozen=True)
class Part:
    bone: str
    name: str
    origin: tuple
    size: tuple
    uv: tuple


def _sc(p: CatParams) -> CatParams:
    if p.scale == 1.0:
        return p
    def i(v):
        return max(1, round(v * p.scale))
    return CatParams(i(p.body_w), i(p.body_h), i(p.body_len), i(p.chest_len), round(p.tuck * p.scale),
                     i(p.leg_len), i(p.leg_w), i(p.head_w), i(p.head_h), i(p.head_d), i(p.tail_len),
                     i(p.tail_w), p.tail_droop, 1.0)


def pivots(p: CatParams) -> dict:
    p = _sc(p)
    top = p.leg_len + p.body_h
    half = p.body_len / 2
    return {
        # Front of the body, so a sitting pitch drops the hindquarters and keeps the shoulders.
        "body": [0, p.leg_len + p.body_h / 2, -half + 3],
        "head": [0, top - p.head_h / 2 + 1, -half + 1],
        "tail": [0, top - p.tail_w, half - 1],
        "front_left_leg": [p.body_w / 2 - p.leg_w / 2, p.leg_len, -half + p.leg_w / 2],
        "front_right_leg": [-(p.body_w / 2 - p.leg_w / 2), p.leg_len, -half + p.leg_w / 2],
        "back_left_leg": [p.body_w / 2 - p.leg_w / 2, p.leg_len, half - p.leg_w / 2],
        "back_right_leg": [-(p.body_w / 2 - p.leg_w / 2), p.leg_len, half - p.leg_w / 2],
    }


def _shapes(p: CatParams):
    """(bone, name, origin, size) for every cube, before UV placement."""
    p = _sc(p)
    half = p.body_len / 2
    top = p.leg_len + p.body_h
    bw = p.body_w
    rear_len = p.body_len - p.chest_len
    out = []
    out.append(("body", "chest", (-bw / 2, p.leg_len, -half), (bw, p.body_h, p.chest_len)))
    out.append(("body", "rear", (-bw / 2, p.leg_len + p.tuck, -half + p.chest_len),
                (bw, p.body_h - p.tuck, rear_len)))

    pv = pivots(p)["head"]
    hw, hh, hd = p.head_w, p.head_h, p.head_d
    out.append(("head", "head", (-hw / 2, pv[1] - hh / 2, pv[2] - hd + 1), (hw, hh, hd)))
    mw, mh, md = max(2, round(hw * 0.5)), max(2, round(hh * 0.4)), max(2, round(hd * 0.4))
    out.append(("head", "muzzle", (-mw / 2, pv[1] - hh / 2, pv[2] - hd + 1 - md), (mw, mh, md)))
    ew = max(2, round(hw * 0.25))
    ear_y = pv[1] + hh / 2
    ear_z = pv[2] - 2
    out.append(("head", "ear_right", (-hw / 2, ear_y, ear_z), (ew, ew, 1)))
    out.append(("head", "ear_left", (hw / 2 - ew, ear_y, ear_z), (ew, ew, 1)))

    tv = pivots(p)["tail"]
    out.append(("tail", "tail", (-p.tail_w / 2, tv[1] - p.tail_w / 2, tv[2]), (p.tail_w, p.tail_w, p.tail_len)))

    for bone in BONES[3:]:
        cx, _, cz = pivots(p)[bone]
        out.append((bone, bone, (cx - p.leg_w / 2, 0, cz - p.leg_w / 2), (p.leg_w, p.leg_len, p.leg_w)))
    return out


def layout(params: CatParams) -> list:
    """Cubes with box-UV origins packed into rows of the texture, tallest first."""
    shapes = _shapes(params)
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
        raise ValueError("cat parts do not fit the texture")
    return [Part(b, n, o, sz, uvs[i]) for i, (b, n, o, sz) in enumerate(_shapes(params))]


def build(params: CatParams, identifier: str = "geometry.gameops.cat") -> dict:
    p = _sc(params)
    pv = pivots(p)
    bones = []
    parts = layout(params)
    for bone in BONES:
        entry = {"name": bone, "pivot": pv[bone]}
        # Head, tail and rear legs ride on the body so a sitting pitch carries them down with it.
        if bone in ("head", "tail", "back_left_leg", "back_right_leg"):
            entry["parent"] = "body"
        if bone == "tail" and p.tail_droop:
            entry["rotation"] = [-p.tail_droop, 0, 0]
        entry["cubes"] = [{"origin": list(c.origin), "size": list(c.size), "uv": list(c.uv)}
                          for c in parts if c.bone == bone]
        bones.append(entry)
    top = p.leg_len + p.body_h
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
