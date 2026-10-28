#!/usr/bin/env python3
"""Paints the werewolf texture from scratch (own art, no vanilla pixels).

Run as: python3 -I make_werewolf_texture.py <output.png>
The face rectangles follow the box-UV unfolding of the cubes in
werewolf.geo.json; change a cube's uv there and the matching CUBES entry here
together.
"""
import struct
import sys
import zlib

W = H = 128

FUR = (92, 78, 66)
FUR_SHADE = (66, 56, 48)
DARK = (44, 38, 34)
DARK_SHADE = (32, 28, 26)
BELLY = (132, 116, 98)
CLAW = (226, 222, 208)
EYE = (230, 40, 30)
NOSE = (16, 12, 12)

# name: (u, v, width, height, depth, base colour, shade colour)
CUBES = {
    "head": (0, 0, 14, 12, 14, FUR, FUR_SHADE),
    "snout": (60, 0, 8, 6, 10, FUR, FUR_SHADE),
    "ear_right": (100, 0, 4, 6, 2, DARK, DARK_SHADE),
    "ear_left": (100, 10, 4, 6, 2, DARK, DARK_SHADE),
    "body": (0, 32, 28, 22, 16, FUR, FUR_SHADE),
    "right_arm": (0, 72, 10, 34, 10, FUR_SHADE, FUR),
    "left_arm": (40, 72, 10, 34, 10, FUR_SHADE, FUR),
    "claw_right": (0, 116, 10, 4, 2, CLAW, FUR_SHADE),
    "claw_left": (24, 116, 10, 4, 2, CLAW, FUR_SHADE),
    "right_leg": (88, 32, 10, 18, 10, DARK, DARK_SHADE),
    "left_leg": (88, 60, 10, 18, 10, DARK, DARK_SHADE),
    "tail": (96, 104, 6, 18, 6, FUR, DARK),
}


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
    for u, v, w, h, d, base, shade in CUBES.values():
        for name, (fx, fy, fw, fh) in faces(u, v, w, h, d).items():
            for y in range(fy, fy + fh):
                for x in range(fx, fx + fw):
                    colour = shade if speckle(x, y) == 0 or name in ("bottom", "back") else base
                    px[y][x] = colour + (255,)

    # Pale belly patch and darker mane across the chest front.
    bx, by, bw, bh = faces(*CUBES["body"][:5])["front"]
    for y in range(by + 4, by + bh):
        for x in range(bx + 8, bx + bw - 8):
            px[y][x] = (BELLY if speckle(x, y) else FUR_SHADE) + (255,)
    for y in range(by, by + 5):
        for x in range(bx, bx + bw):
            px[y][x] = (DARK if speckle(x, y) else DARK_SHADE) + (255,)

    # Red eyes and a dark nose; teeth on the snout front.
    hx, hy, _, _ = faces(*CUBES["head"][:5])["front"]
    for ex in (2, 9):
        for dx in range(3):
            px[hy + 3][hx + ex + dx] = EYE + (255,)
            px[hy + 4][hx + ex + dx] = EYE + (255,)
    sx, sy, sw, sh = faces(*CUBES["snout"][:5])["front"]
    for x in range(sx + 2, sx + sw - 2):
        px[sy][x] = NOSE + (255,)
    for x in range(sx, sx + sw, 2):
        px[sy + sh - 1][x] = CLAW + (255,)

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
        sys.exit("usage: make_werewolf_texture.py <output.png>")
    main(sys.argv[1])
