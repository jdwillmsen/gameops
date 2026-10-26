#!/usr/bin/env python3
"""Renders plan slices and sections of a --save'd Colosseum voxel file.

    preview.py VOX.npy OUT_DIR [--mats=mats.json]

Plans at the given y levels, and sections through the centre on both axes.
Colours are by material name; unknown names get a stable hash colour.
"""
import hashlib
import json
import os
import sys

import numpy as np
from PIL import Image

vox = np.load(sys.argv[1])
out = sys.argv[2]
mats_path = next((a.split("=", 1)[1] for a in sys.argv[3:] if a.startswith("--mats=")), os.path.join(os.path.dirname(sys.argv[1]), "mats.json"))
meta = json.load(open(mats_path))
mats = meta["mats"]
os.makedirs(out, exist_ok=True)
Y0, X0, Z0, CX, CZ = meta["y0"], meta["x0"], meta["z0"], meta["cx"], meta["cz"]
PAL = {"air": (235, 240, 255), "sand": (232, 217, 160), "smooth_sandstone": (230, 212, 156), "cut_sandstone": (220, 203, 144),
       "chiseled_sandstone": (205, 185, 122), "sandstone": (217, 199, 138), "red_sandstone": (181, 83, 43),
       "red_sand": (192, 101, 46), "stone_bricks": (125, 125, 125), "chiseled_stone_bricks": (106, 106, 106),
       "polished_andesite": (154, 154, 154), "andesite": (133, 133, 133), "quartz_pillar": (240, 236, 228),
       "gold_block": (245, 197, 24), "iron_bars": (60, 60, 60), "quartz_block": (244, 240, 232), "water": (60, 110, 220),
       "lantern": (255, 179, 71), "end_rod": (255, 255, 255), "deepslate_bricks": (60, 60, 66), "deepslate_tiles": (50, 50, 56),
       "polished_deepslate": (70, 70, 76), "polished_blackstone": (40, 36, 44), "polished_blackstone_bricks": (48, 44, 52),
       "redstone_lamp": (200, 150, 90), "redstone_wire": (200, 20, 20), "red_wool": (170, 40, 40), "glass": (190, 230, 240),
       "soul_sand": (84, 64, 51), "sea_lantern": (170, 220, 220), "chiseled_deepslate": (80, 80, 90), "gilded_blackstone": (120, 90, 40)}
cols = np.zeros((len(mats) + 1, 3), dtype=np.uint8)
cols[0] = (200, 200, 200)
for i, m in enumerate(mats, 1):
    name = m.split(" ")[0]
    cols[i] = PAL.get(name) or tuple(int(b) for b in hashlib.md5(name.encode()).digest()[:3])
S = 3


def save(arr, name):
    img = Image.fromarray(cols[arr][::-1])
    img.resize((img.width * S, img.height * S), Image.NEAREST).save(os.path.join(out, name))


for y in (50, 53, 58, 64, 70, 80):
    save(vox[y - Y0], f"plan_y{y}.png")
save(vox[:, CZ - Z0, :], "section_x.png")
save(vox[:, :, CX - X0], "section_z.png")
