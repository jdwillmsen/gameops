"""Shared four-legged cat skeleton, scaled per species by CatParams.

Model space is pixels, +y up, the animal faces -z, negative x is its right. Every size is an
integer so each cube's box-UV rectangle sits on the pixel grid.
"""
from dataclasses import dataclass, replace

from rig_common import TEXTURE, Part, pack  # noqa: F401
BONES = ("body", "head", "tail", "front_left_leg", "front_right_leg", "back_left_leg", "back_right_leg")
MANE = "mane"   # frames the head and turns with it
MANE_BODY = "mane_chest"   # the bib and shoulder cape, which stay with the body


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
    head_rise: int = 0    # how far the top of the head stands above the back line
    muzzle: tuple = (0.5, 0.4, 0.4)   # width, height, depth as fractions of the head
    scale: float = 1.0
    tuft: int = 0   # length of a tail-end tuft past the tail; 0 for none
    mane: bool = False   # extra bone of boxes around the neck and shoulders, shown per variant


# Tiger: big, heavy and broad. Cheetah: slender, long-legged, small head, long tail, deep chest.
PARAMS = {
    "tiger": CatParams(body_w=12, body_h=11, body_len=22, chest_len=11, tuck=0, leg_len=9, leg_w=5,
                       head_w=10, head_h=10, head_d=8, tail_len=18, tail_w=3, tail_droop=25,
                       head_rise=2, muzzle=(0.5, 0.4, 0.5)),
    # Lion: deep heavy chest on a lean rear, a head wider than tall, a tufted tail and a male mane.
    "lion": CatParams(body_w=11, body_h=11, body_len=22, chest_len=13, tuck=2, leg_len=9, leg_w=4,
                      head_w=10, head_h=8, head_d=8, tail_len=17, tail_w=2, tail_droop=15, head_rise=1,
                      muzzle=(0.55, 0.45, 0.4), tuft=3, mane=True),
    # Black leopard: low, muscular and long, small head, very long tail.
    "black_leopard": CatParams(body_w=8, body_h=7, body_len=22, chest_len=10, tuck=0, leg_len=6, leg_w=3,
                               head_w=6, head_h=6, head_d=5, tail_len=24, tail_w=2, tail_droop=10),
    "cheetah": CatParams(body_w=6, body_h=7, body_len=20, chest_len=10, tuck=2, leg_len=8, leg_w=3,
                         head_w=5, head_h=5, head_d=5, tail_len=20, tail_w=2, tail_droop=12),
}


def _sc(p: CatParams) -> CatParams:
    if p.scale == 1.0:
        return p
    def i(v):
        return max(1, round(v * p.scale))
    return replace(p, body_w=i(p.body_w), body_h=i(p.body_h), body_len=i(p.body_len), chest_len=i(p.chest_len),
                   tuck=round(p.tuck * p.scale), leg_len=i(p.leg_len), leg_w=i(p.leg_w), head_w=i(p.head_w),
                   head_h=i(p.head_h), head_d=i(p.head_d), tail_len=i(p.tail_len), tail_w=i(p.tail_w),
                   tuft=round(p.tuft * p.scale), scale=1.0)


def bone_names(p: CatParams) -> tuple:
    return BONES + ((MANE, MANE_BODY) if p.mane else ())


def pivots(p: CatParams) -> dict:
    p = _sc(p)
    top = p.leg_len + p.body_h
    half = p.body_len / 2
    return {
        # Front of the body, so a sitting pitch drops the hindquarters and keeps the shoulders.
        "body": [0, p.leg_len + p.body_h / 2, -half + 3],
        "head": [0, top + p.head_rise - p.head_h / 2, -half + 1],
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
    mw, mh, md = (max(2, round(hw * p.muzzle[0])), max(2, round(hh * p.muzzle[1])),
                  max(2, round(hd * p.muzzle[2])))
    # The muzzle starts inside the front of the head and sticks out of it.
    out.append(("head", "muzzle", (-mw / 2, pv[1] - hh / 2 + 1, pv[2] - hd + 2 - md), (mw, mh, md)))
    ew = max(2, round(hw * 0.3))
    ear_y = pv[1] + hh / 2
    ear_z = pv[2] - 2
    out.append(("head", "ear_right", (-hw / 2, ear_y, ear_z), (ew, ew, 1)))
    out.append(("head", "ear_left", (hw / 2 - ew, ear_y, ear_z), (ew, ew, 1)))

    tv = pivots(p)["tail"]
    out.append(("tail", "tail", (-p.tail_w / 2, tv[1] - p.tail_w / 2, tv[2]), (p.tail_w, p.tail_w, p.tail_len)))

    if p.tuft:
        out.append(("tail", "tuft", (-p.tail_w / 2 - 0.5, tv[1] - p.tail_w / 2 - 0.5, tv[2] + p.tail_len),
                    (p.tail_w + 1, p.tail_w + 1, p.tuft)))

    if p.mane:
        # The face and muzzle stay clear of the front: the side ruffs start behind the cheeks.
        hx, hy, hz = hw / 2, pv[1] - hh / 2, pv[2]
        out.append((MANE, "mane_side_right", (-hx - 3, hy - 2, hz - 4), (3, hh + 3, 6)))
        out.append((MANE, "mane_side_left", (hx, hy - 2, hz - 4), (3, hh + 3, 6)))
        out.append((MANE, "mane_top", (-hx - 1, hy + hh, hz - 1), (hw + 2, 3, 4)))
        out.append((MANE, "mane_back", (-hx - 4, hy - 3, hz + 1), (hw + 8, hh + 5, 3)))
        out.append((MANE_BODY, "mane_cape", (-(bw + 2) / 2, top - 1, -half + 4), (bw + 2, 3, 6)))
        out.append((MANE_BODY, "mane_bib", (-(bw - 2) / 2, p.leg_len + 1, -half - 2), (bw - 2, p.body_h - 5, 4)))

    for bone in BONES[3:]:
        cx, _, cz = pivots(p)[bone]
        out.append((bone, bone, (cx - p.leg_w / 2, 0, cz - p.leg_w / 2), (p.leg_w, p.leg_len, p.leg_w)))
    return out


def layout(params: CatParams) -> list:
    return pack(_shapes(params), "cat parts")


def build(params: CatParams, identifier: str = "geometry.gameops.cat") -> dict:
    p = _sc(params)
    pv = pivots(p)
    bones = []
    parts = layout(params)
    pv[MANE] = pv["head"]
    pv[MANE_BODY] = pv["body"]
    for bone in bone_names(p):
        entry = {"name": bone, "pivot": pv[bone]}
        # Head, tail, mane and rear legs ride on the body so a sitting pitch carries them down with it.
        if bone in ("head", "tail", "back_left_leg", "back_right_leg", MANE_BODY):
            entry["parent"] = "body"
        if bone == MANE:
            entry["parent"] = "head"
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
