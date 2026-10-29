#!/usr/bin/env python3
"""Renders a Bedrock entity geometry plus its texture to a PNG contact sheet.

Run as: python3 -I tools/render-geo.py <geo.json> <texture.png> <out.png> [--geometry ID] [--scale N]
Static pose only: bone rotations in the geometry apply, animations do not. It
is a software orthographic renderer for judging proportions, colours and UV
mistakes without a Bedrock client; it is not pixel-exact to the game.
"""
import json
import math
import sys

import numpy as np
from PIL import Image

VIEWS = (("front", 180), ("three-quarter", 145), ("side", 90), ("back", 0))
BG = (58, 62, 70, 255)


def rot_x(deg):
    # Positive x tilts the top toward the front (-z), as in the game's models.
    c, s = math.cos(math.radians(deg)), math.sin(math.radians(deg))
    return np.array([[1, 0, 0], [0, c, s], [0, -s, c]])


def rot_y(deg):
    c, s = math.cos(math.radians(deg)), math.sin(math.radians(deg))
    return np.array([[c, 0, -s], [0, 1, 0], [s, 0, c]])


def rot_z(deg):
    c, s = math.cos(math.radians(deg)), math.sin(math.radians(deg))
    return np.array([[c, s, 0], [-s, c, 0], [0, 0, 1]])


def bone_matrix(bone):
    rx, ry, rz = bone.get("rotation", [0, 0, 0])
    return rot_z(rz) @ rot_y(ry) @ rot_x(rx)


def world_transforms(bones):
    """Per bone: (rotation matrix, pivot) composed down the parent chain."""
    by_name = {b["name"]: b for b in bones}
    cache = {}

    def build(name):
        if name in cache:
            return cache[name]
        b = by_name[name]
        pivot = np.array(b.get("pivot", [0, 0, 0]), dtype=float)
        local = bone_matrix(b)
        parent = b.get("parent")
        if parent and parent in by_name:
            pr, pp = build(parent)
            # Rotate about the pivot first, then follow the parent's motion.
            def apply(p, pr=pr, pp=pp, local=local, pivot=pivot):
                return pp(local @ (p - pivot) + pivot)
            cache[name] = (None, apply)
        else:
            cache[name] = (None, lambda p, local=local, pivot=pivot: local @ (p - pivot) + pivot)
        return cache[name]

    return {n: build(n)[1] for n in by_name}


def faces(origin, size, uv):
    """Yield (corner, edge_u, edge_v, uv_rect) in model space, outside-facing."""
    ox, oy, oz = origin
    w, h, d = size
    u, v = uv
    x0, y0, z0 = ox, oy, oz
    x1, y1, z1 = ox + w, oy + h, oz + d
    # Front of a Bedrock entity is -z (north).
    yield ((x1, y1, z0), (-w, 0, 0), (0, -h, 0), (u + d, v + d, w, h))      # north
    yield ((x0, y1, z1), (w, 0, 0), (0, -h, 0), (u + 2 * d + w, v + d, w, h))  # south
    yield ((x1, y1, z1), (0, 0, -d), (0, -h, 0), (u, v + d, d, h))          # east (+x)
    yield ((x0, y1, z0), (0, 0, d), (0, -h, 0), (u + d + w, v + d, d, h))   # west (-x)
    yield ((x1, y1, z0), (-w, 0, 0), (0, 0, d), (u + d, v, w, d))           # up
    yield ((x1, y0, z1), (-w, 0, 0), (0, 0, -d), (u + d + w, v, w, d))      # down


def render(geo, tex, scale, yaw):
    desc = geo["description"]
    tw, th = desc.get("texture_width", 64), desc.get("texture_height", 64)
    tex = tex.convert("RGBA")
    sx, sy = tex.width / tw, tex.height / th
    texa = np.array(tex)
    tr = world_transforms(geo["bones"])
    view = rot_y(yaw)
    width, height = int(80 * scale), int(80 * scale)
    img = np.zeros((height, width, 4), dtype=np.uint8)
    img[:] = BG
    zbuf = np.full((height, width), 1e9)

    def to_screen(p):
        c = view @ p
        return np.array([(c[0] + 40) * scale, height - 1 - (c[1] + 6) * scale]), c[2]

    for bone in geo["bones"]:
        xf = tr[bone["name"]]
        for cube in bone.get("cubes", []):
            if "uv" not in cube or not isinstance(cube["uv"], list):
                continue
            for corner, eu, ev, (ru, rv, rw, rh) in faces(cube["origin"], cube["size"], cube["uv"]):
                c0 = np.array(corner, dtype=float)
                p0 = xf(c0)
                pu = xf(c0 + np.array(eu, dtype=float)) - p0
                pv = xf(c0 + np.array(ev, dtype=float)) - p0
                normal = view @ np.cross(pu, pv)
                # The viewer sits at -z looking toward +z after the yaw turn.
                if normal[2] >= 0:
                    continue
                s0, z0 = to_screen(p0)
                su, zu = to_screen(p0 + pu)
                sv, zv = to_screen(p0 + pv)
                eu2, ev2 = su - s0, sv - s0
                det = eu2[0] * ev2[1] - eu2[1] * ev2[0]
                if abs(det) < 1e-6:
                    continue
                xs = [s0[0], su[0], sv[0], su[0] + ev2[0]]
                ys = [s0[1], su[1], sv[1], su[1] + ev2[1]]
                x_lo, x_hi = max(int(min(xs)), 0), min(int(max(xs)) + 1, width - 1)
                y_lo, y_hi = max(int(min(ys)), 0), min(int(max(ys)) + 1, height - 1)
                if x_lo > x_hi or y_lo > y_hi:
                    continue
                gx, gy = np.meshgrid(np.arange(x_lo, x_hi + 1), np.arange(y_lo, y_hi + 1))
                dx, dy = gx - s0[0] + 0.5, gy - s0[1] + 0.5
                a = (dx * ev2[1] - dy * ev2[0]) / det
                b = (eu2[0] * dy - eu2[1] * dx) / det
                inside = (a >= 0) & (a < 1) & (b >= 0) & (b < 1)
                z = z0 + a * (zu - z0) + b * (zv - z0)
                tx = np.clip(((ru + a * rw) * sx).astype(int), 0, tex.width - 1)
                ty = np.clip(((rv + b * rh) * sy).astype(int), 0, tex.height - 1)
                col = texa[ty, tx]
                draw = inside & (col[..., 3] > 0) & (z < zbuf[gy, gx])
                zbuf[gy[draw], gx[draw]] = z[draw]
                img[gy[draw], gx[draw]] = col[draw]
    return Image.fromarray(img, "RGBA")


def main(argv):
    scale = 6
    ident = None
    it = iter(argv)
    rest = []
    for a in it:
        if a == "--scale":
            scale = int(next(it))
        elif a == "--geometry":
            ident = next(it)
        else:
            rest.append(a)
    if len(rest) != 3:
        sys.exit("usage: render-geo.py <geo.json> <texture.png> <out.png> [--geometry ID] [--scale N]")
    geo_path, tex_path, out_path = rest
    doc = json.load(open(geo_path))
    geos = doc["minecraft:geometry"]
    geo = next((g for g in geos if ident in (None, g["description"]["identifier"])), None)
    if geo is None:
        sys.exit(f"no geometry {ident!r} in {geo_path}")
    tex = Image.open(tex_path)
    tiles = [render(geo, tex, scale, yaw) for _, yaw in VIEWS]
    w = sum(t.width for t in tiles)
    sheet = Image.new("RGBA", (w, tiles[0].height), BG)
    x = 0
    for t in tiles:
        sheet.paste(t, (x, 0))
        x += t.width
    sheet.save(out_path)
    print(f"{out_path}: {sheet.width}x{sheet.height}, views {', '.join(n for n, _ in VIEWS)}")


if __name__ == "__main__":
    main(sys.argv[1:])
