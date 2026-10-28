#!/usr/bin/env python3
"""Paints the colossus texture from scratch (own art, no vanilla pixels).

Run as: python3 -I make_colossus_texture.py <output.png>
The face rectangles follow the box-UV unfolding of the cubes in
colossus.geo.json; change a cube's uv there and the matching CUBES entry here
together.
"""
import struct
import sys
import zlib

W = H = 128

STONE = (74, 76, 88)
STONE_SHADE = (54, 56, 66)
OBSIDIAN = (34, 30, 44)
OBSIDIAN_SHADE = (24, 20, 32)
LAVA = (232, 101, 26)
LAVA_HOT = (255, 190, 70)
HORN = (214, 206, 184)

# name: (u, v, width, height, depth, base colour, shade colour)
CUBES = {
    "head": (0, 0, 16, 12, 14, STONE, STONE_SHADE),
    "horn_right": (64, 0, 3, 8, 3, HORN, STONE_SHADE),
    "horn_left": (76, 0, 3, 8, 3, HORN, STONE_SHADE),
    "body": (0, 32, 32, 18, 18, OBSIDIAN, OBSIDIAN_SHADE),
    "right_arm": (0, 72, 10, 28, 10, STONE, STONE_SHADE),
    "left_arm": (40, 72, 10, 28, 10, STONE, STONE_SHADE),
    "right_leg": (80, 72, 12, 16, 12, OBSIDIAN_SHADE, OBSIDIAN),
    "left_leg": (0, 100, 12, 16, 12, OBSIDIAN_SHADE, OBSIDIAN),
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

    # Lava veins down the chest, the knuckles and between the plates.
    bx, by, bw, bh = faces(*CUBES["body"][:5])["front"]
    for y in range(by, by + bh):
        for x in range(bx, bx + bw):
            local_x, local_y = x - bx, y - by
            if local_x in (15, 16) or (local_y in (5, 12) and 4 < local_x < 28 and speckle(x, y) != 0):
                px[y][x] = (LAVA_HOT if speckle(x, y) == 2 else LAVA) + (255,)
    for arm in ("right_arm", "left_arm"):
        ax, ay, aw, ah = faces(*CUBES[arm][:5])["front"]
        for y in range(ay + ah - 5, ay + ah):
            for x in range(ax, ax + aw):
                px[y][x] = (LAVA if speckle(x, y) else LAVA_HOT) + (255,)

    hx, hy, _, _ = faces(*CUBES["head"][:5])["front"]
    for ex in (3, 10):
        for dx in range(3):
            for dy in range(2):
                px[hy + 4 + dy][hx + ex + dx] = LAVA_HOT + (255,)
    for mx in range(4, 12):
        px[hy + 9][hx + mx] = (14, 10, 16, 255)
    for mx in (5, 7, 9, 11):
        px[hy + 10][hx + mx] = HORN + (255,)

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
        sys.exit("usage: make_colossus_texture.py <output.png>")
    main(sys.argv[1])
