"""Hyena skeleton: high shoulders falling away to short hind legs, big head, heavy muzzle, neck ridge.

Same bone names as the cats so one animation set drives it. Pixels, +y up, faces -z.
"""
from dataclasses import dataclass

import rig_common
from rig_common import Part, TEXTURE  # noqa: F401

PARENTS = {"head": "body", "tail": "body", "back_left_leg": "body", "back_right_leg": "body"}


@dataclass(frozen=True)
class HyenaParams:
    front_leg: int = 8
    rear_leg: int = 6    # shorter, so the hips sit below the shoulders
    leg_w: int = 3
    body_w: int = 7
    front_h: int = 7
    rear_h: int = 6
    front_len: int = 10
    rear_len: int = 10
    ridge: tuple = (2, 2, 9)
    head: tuple = (7, 6, 6)
    muzzle: tuple = (5, 3, 4)
    ear: int = 3
    tail: tuple = (2, 2, 8)
    tail_droop: int = 40


PARAMS = {"hyena": HyenaParams()}


def pivots(p: HyenaParams) -> dict:
    half = (p.front_len + p.rear_len) / 2
    front_top = p.front_leg + p.front_h
    x = p.body_w / 2 - p.leg_w / 2
    return {
        "body": [0, p.front_leg + p.front_h / 2, -half + 3],
        "head": [0, front_top - 2, -half + 1],
        "tail": [0, p.rear_leg + p.rear_h, half],
        "front_left_leg": [x, p.front_leg, -half + p.leg_w / 2],
        "front_right_leg": [-x, p.front_leg, -half + p.leg_w / 2],
        "back_left_leg": [x, p.rear_leg, half - p.leg_w / 2],
        "back_right_leg": [-x, p.rear_leg, half - p.leg_w / 2],
    }


def _shapes(p: HyenaParams) -> list:
    pv = pivots(p)
    half = (p.front_len + p.rear_len) / 2
    front_top = p.front_leg + p.front_h
    bw = p.body_w
    out = [
        ("body", "chest", (-bw / 2, p.front_leg, -half), (bw, p.front_h, p.front_len)),
        ("body", "rear", (-bw / 2, p.rear_leg, -half + p.front_len), (bw, p.rear_h, p.rear_len)),
        ("body", "ridge", (-p.ridge[0] / 2, front_top, -half + 1), p.ridge),
    ]
    hw, hh, hd = p.head
    hy = pv["head"][1]
    out.append(("head", "head", (-hw / 2, hy - hh / 2, pv["head"][2] - hd + 1), (hw, hh, hd)))
    mw, mh, md = p.muzzle
    out.append(("head", "muzzle", (-mw / 2, hy - hh / 2, pv["head"][2] - hd + 2 - md), (mw, mh, md)))
    e = p.ear
    ear_z = pv["head"][2] - 3
    out.append(("head", "ear_right", (-hw / 2, hy + hh / 2, ear_z), (e, e, 1)))
    out.append(("head", "ear_left", (hw / 2 - e, hy + hh / 2, ear_z), (e, e, 1)))
    tw, th, tl = p.tail
    out.append(("tail", "tail", (-tw / 2, pv["tail"][1] - th / 2, pv["tail"][2]), p.tail))
    for bone in ("front_left_leg", "front_right_leg", "back_left_leg", "back_right_leg"):
        cx, py, cz = pv[bone]
        out.append((bone, bone, (cx - p.leg_w / 2, 0, cz - p.leg_w / 2), (p.leg_w, py, p.leg_w)))
    return out


def layout(p: HyenaParams) -> list:
    return rig_common.pack(_shapes(p), "hyena parts")


def build(p: HyenaParams, identifier: str = "geometry.gameops.hyena") -> dict:
    bones = rig_common.bone_entries(pivots(p), layout(p), PARENTS, {"tail": [-p.tail_droop, 0, 0]})
    return rig_common.document(identifier, bones, p.front_leg + p.front_h + p.ridge[1])
