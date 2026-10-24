#!/usr/bin/env python3
"""Paints the stage-0 brute texture from scratch (own art, no vanilla pixels).

Run as: python3 -I make_texture.py <output.png>
The face rectangles follow the box-UV unfolding of the cubes in
training_dummy_brute.geo.json; change a cube's uv there and the matching
entry in CUBES here together.
"""
import struct
import sys
import zlib

W = H = 64

SKIN = (92, 128, 70)
SKIN_SHADE = (74, 108, 56)
LEATHER = (112, 74, 40)
LEATHER_SHADE = (88, 58, 32)
WOOD = (138, 98, 58)
WOOD_SHADE = (104, 72, 42)
EYE = (240, 220, 80)
MOUTH = (40, 24, 20)
TUSK = (230, 226, 210)

# name: (u, v, width, height, depth, base colour, shade colour)
CUBES = {
    "head": (0, 0, 8, 8, 8, SKIN, SKIN_SHADE),
    "body": (16, 16, 8, 12, 4, LEATHER, LEATHER_SHADE),
    "right_arm": (40, 16, 4, 12, 4, SKIN, SKIN_SHADE),
    "left_arm": (32, 48, 4, 12, 4, SKIN, SKIN_SHADE),
    "right_leg": (0, 16, 4, 12, 4, LEATHER_SHADE, LEATHER),
    "left_leg": (16, 48, 4, 12, 4, LEATHER_SHADE, LEATHER),
    "club": (48, 0, 2, 10, 2, WOOD, WOOD_SHADE),
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

    fx, fy, _, _ = faces(*CUBES["head"][:5])["front"]
    for ex in (1, 5):
        px[fy + 3][fx + ex] = EYE + (255,)
        px[fy + 3][fx + ex + 1] = EYE + (255,)
    for mx in range(2, 6):
        px[fy + 6][fx + mx] = MOUTH + (255,)
    px[fy + 5][fx + 2] = TUSK + (255,)
    px[fy + 5][fx + 5] = TUSK + (255,)

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
        sys.exit("usage: make_texture.py <output.png>")
    main(sys.argv[1])
