#!/usr/bin/env python3
"""Paints the ground dragon texture from scratch (own art, no vanilla pixels).

Run as: python3 -I make_ground_dragon_texture.py <output.png>
The face rectangles follow the box-UV unfolding of the cubes in
ground_dragon.geo.json; change a cube's uv or size there and the matching
CUBES entry here together. The UV bounds check in tools/check-packs.sh
catches a texture-space overlap or overflow in the model.
"""
import struct
import sys
import zlib

W = H = 256

SCALE = (52, 92, 62)
SCALE_SHADE = (36, 66, 46)
DARK = (26, 34, 30)
DARK_SHADE = (18, 24, 21)
BELLY = (178, 160, 108)
BELLY_SHADE = (146, 128, 84)
BONE = (228, 220, 196)
BONE_SHADE = (186, 176, 150)
WING = (112, 42, 46)
WING_SHADE = (84, 30, 36)
EYE = (255, 176, 32)
NOSTRIL = (12, 10, 10)

# kind: (base colour, shade colour)
KINDS = {
    "hide": (SCALE, SCALE_SHADE),
    "dark": (DARK, DARK_SHADE),
    "bone": (BONE, BONE_SHADE),
    "wing": (WING, WING_SHADE),
}

# name: (u, v, width, height, depth, kind)
CUBES = {
    "body": (0, 0, 24, 20, 36, "hide"),
    "spike_front": (18, 129, 3, 5, 3, "bone"),
    "spike_mid": (30, 129, 3, 5, 3, "bone"),
    "spike_rear": (42, 129, 3, 5, 3, "bone"),
    "neck": (80, 56, 12, 12, 16, "hide"),
    "head": (0, 86, 14, 13, 12, "hide"),
    "snout": (144, 86, 10, 6, 14, "hide"),
    "horn_right_base": (82, 129, 3, 3, 3, "bone"),
    "horn_right_tip": (0, 129, 2, 2, 7, "bone"),
    "horn_left_base": (70, 129, 3, 3, 3, "bone"),
    "horn_left_tip": (224, 111, 2, 2, 7, "bone"),
    "jaw": (36, 111, 8, 4, 12, "dark"),
    "wing_right": (172, 0, 4, 18, 22, "wing"),
    "wing_right_tip": (62, 129, 2, 6, 2, "bone"),
    "wing_left": (120, 0, 4, 18, 22, "wing"),
    "wing_left_tip": (54, 129, 2, 6, 2, "bone"),
    "front_right_leg": (168, 56, 8, 20, 8, "hide"),
    "front_right_leg_toe": (194, 111, 10, 4, 5, "bone"),
    "front_left_leg": (136, 56, 8, 20, 8, "hide"),
    "front_left_leg_toe": (164, 111, 10, 4, 5, "bone"),
    "hind_right_leg": (40, 56, 10, 20, 10, "hide"),
    "hind_right_leg_toe": (130, 111, 12, 4, 5, "bone"),
    "hind_left_leg": (0, 56, 10, 20, 10, "hide"),
    "hind_left_leg_toe": (96, 111, 12, 4, 5, "bone"),
    "tail1": (200, 56, 12, 12, 14, "hide"),
    "tail2": (52, 86, 10, 10, 14, "hide"),
    "tail3": (100, 86, 8, 8, 14, "hide"),
    "tail4": (192, 86, 6, 6, 14, "hide"),
    "tail5": (0, 111, 4, 4, 14, "dark"),
    "tail_tip": (76, 111, 6, 6, 4, "bone"),
}

BELLY_CUBES = ("body", "neck", "tail1", "tail2", "tail3", "tail4")


def faces(u, v, w, h, d):
    """Box-UV face rectangles as (x, y, width, height)."""
    return {
        "top": (u + d, v, w, d),
        "bottom": (u + d + w, v, w, d),
        "right": (u, v + d, d, h),
        "front": (u + d, v + d, w, h),
        "left": (u + d + w, v + d, d, h),
        "back": (u + 2 * d + w, v + d, w, h),
    }


def speckle(x, y):
    """Deterministic per-pixel 0..2 so the output is reproducible byte for byte."""
    n = (x * 73856093) ^ (y * 19349663)
    return (n >> 3) % 3


def main(out):
    px = [[(0, 0, 0, 0)] * W for _ in range(H)]
    for name, (u, v, w, h, d, kind) in CUBES.items():
        base, shade = KINDS[kind]
        for face, (fx, fy, fw, fh) in faces(u, v, w, h, d).items():
            for y in range(fy, fy + fh):
                for x in range(fx, fx + fw):
                    # Offset rows of scales: every other 4-pixel row shifts by two.
                    scale_edge = kind == "hide" and ((y // 4) % 2 == 0) == ((x // 4) % 2 == 0) and speckle(x, y) != 1
                    colour = shade if speckle(x, y) == 0 or scale_edge or face == "back" else base
                    if face == "bottom" and name in BELLY_CUBES:
                        colour = BELLY if speckle(x, y) else BELLY_SHADE
                    px[y][x] = colour + (255,)

    # Pale belly plates run up the underside of the neck and chest sides.
    for name in ("body", "neck"):
        fx, fy, fw, fh = faces(*CUBES[name][:5])["front"]
        for y in range(fy + fh - 4, fy + fh):
            for x in range(fx, fx + fw):
                px[y][x] = (BELLY if speckle(x, y) else BELLY_SHADE) + (255,)

    # Amber eyes on the front and both sides of the head, dark nostrils and
    # bone teeth on the snout and jaw.
    hu, hv, hw, hh, hd, _ = CUBES["head"]
    hf = faces(hu, hv, hw, hh, hd)
    for face, cols in (("front", (0, 1, hw - 2, hw - 1)), ("right", (2, 3)), ("left", (hd - 4, hd - 3))):
        fx, fy, _, _ = hf[face]
        for dx in cols:
            for dy in (3, 4, 5):
                px[fy + dy][fx + dx] = EYE + (255,)
    su, sv, sw, sh, sd, _ = CUBES["snout"]
    sf = faces(su, sv, sw, sh, sd)
    nx, ny, nw, nh = sf["front"]
    for x in (nx + 2, nx + nw - 3):
        px[ny + 1][x] = NOSTRIL + (255,)
    for x in range(nx, nx + nw, 2):
        px[ny + nh - 1][x] = BONE + (255,)
    for face in ("right", "left"):
        fx, fy, fw, fh = sf[face]
        for x in range(fx, fx + fw, 3):
            px[fy + fh - 1][x] = BONE + (255,)
    jx, jy, jw, jh = faces(*CUBES["jaw"][:5])["front"]
    for x in range(jx, jx + jw, 2):
        px[jy][x] = BONE + (255,)

    raw = b"".join(b"\x00" + bytes(c for p in row for c in p) for row in px)

    def chunk(tag, data):
        body = tag + data
        return struct.pack(">I", len(data)) + body + struct.pack(">I", zlib.crc32(body) & 0xFFFFFFFF)

    png = (
        b"\x89PNG\r\n\x1a\n"
        + chunk(b"IHDR", struct.pack(">IIBBBBB", W, H, 8, 6, 0, 0, 0))
        + chunk(b"IDAT", zlib.compress(raw, 9))
        + chunk(b"IEND", b"")
    )
    with open(out, "wb") as f:
        f.write(png)


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit("usage: make_ground_dragon_texture.py <output.png>")
    main(sys.argv[1])
