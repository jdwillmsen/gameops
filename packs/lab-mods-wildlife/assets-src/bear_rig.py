"""Brown bear skeleton: stocky, with a muscular hump over the shoulders and a long snout.

Same bone names as the cats so one animation set drives it. Pixels, +y up, faces -z.
"""
from dataclasses import dataclass

import rig_common
from rig_common import Part, TEXTURE  # noqa: F401

PARENTS = {"head": "body", "tail": "body", "back_left_leg": "body", "back_right_leg": "body"}


@dataclass(frozen=True)
class BearParams:
    leg_len: int = 9
    leg_w: int = 6
    body_w: int = 14
    chest_h: int = 12
    rear_h: int = 11
    chest_len: int = 11
    rear_len: int = 11
    hump: tuple = (10, 3, 8)   # width, height, depth, standing on the chest top
    head: tuple = (8, 8, 7)
    snout: tuple = (4, 4, 5)
    ear: int = 2
    tail: tuple = (2, 2, 3)


PARAMS = {"brown_bear": BearParams()}


def pivots(p: BearParams) -> dict:
    half = (p.chest_len + p.rear_len) / 2
    top = p.leg_len + p.chest_h
    x = p.body_w / 2 - p.leg_w / 2
    return {
        "body": [0, p.leg_len + p.chest_h / 2, -half + 3],
        "head": [0, top - 4, -half + 1],
        "tail": [0, top - 3, half - 1],
        "front_left_leg": [x, p.leg_len, -half + p.leg_w / 2],
        "front_right_leg": [-x, p.leg_len, -half + p.leg_w / 2],
        "back_left_leg": [x, p.leg_len, half - p.leg_w / 2],
        "back_right_leg": [-x, p.leg_len, half - p.leg_w / 2],
    }


def _shapes(p: BearParams) -> list:
    pv = pivots(p)
    half = (p.chest_len + p.rear_len) / 2
    top = p.leg_len + p.chest_h
    bw = p.body_w
    out = [
        ("body", "chest", (-bw / 2, p.leg_len, -half), (bw, p.chest_h, p.chest_len)),
        ("body", "rear", (-bw / 2, p.leg_len, -half + p.chest_len), (bw, p.rear_h, p.rear_len)),
        ("body", "hump", (-p.hump[0] / 2, top, -half + 1), p.hump),
    ]
    hw, hh, hd = p.head
    hy = pv["head"][1]
    out.append(("head", "head", (-hw / 2, hy - hh / 2, pv["head"][2] - hd + 1), (hw, hh, hd)))
    sw, sh, sd = p.snout
    # The snout starts inside the front of the head.
    out.append(("head", "muzzle", (-sw / 2, hy - hh / 2, pv["head"][2] - hd + 2 - sd), (sw, sh, sd)))
    e = p.ear
    ear_z = pv["head"][2] - 3
    out.append(("head", "ear_right", (-hw / 2, hy + hh / 2, ear_z), (e, e, 1)))
    out.append(("head", "ear_left", (hw / 2 - e, hy + hh / 2, ear_z), (e, e, 1)))
    tw, th, tl = p.tail
    out.append(("tail", "tail", (-tw / 2, pv["tail"][1] - th / 2, pv["tail"][2]), p.tail))
    for bone in ("front_left_leg", "front_right_leg", "back_left_leg", "back_right_leg"):
        cx, _, cz = pv[bone]
        out.append((bone, bone, (cx - p.leg_w / 2, 0, cz - p.leg_w / 2), (p.leg_w, p.leg_len, p.leg_w)))
    return out


def layout(p: BearParams) -> list:
    return rig_common.pack(_shapes(p), "bear parts")


def build(p: BearParams, identifier: str = "geometry.gameops.brown_bear") -> dict:
    bones = rig_common.bone_entries(pivots(p), layout(p), PARENTS)
    return rig_common.document(identifier, bones, p.leg_len + p.chest_h + p.hump[1])
