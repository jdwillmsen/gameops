#!/usr/bin/env python3
"""Builds the crew lab's Colosseum as console commands.

    colosseum.py OUT_DIR [--against=OLD.npy] [--save=NEW.npy] [--feed=NS/POD]

Writes clear.cmds (air above the ground), build.cmds (fill commands, bottom
layer first), decor.cmds (entities and chest contents, run last), plan.txt
(all three behind the ticking-area header arena.sh reads) and layout.json, the
document the arena director reads. --against emits only what differs from an
earlier --save, as a patch. --feed runs build.cmds then decor.cmds on the lab
server's console, about 20 commands a second.

The model is voxelised in numpy, then compressed into the fewest boxes. Stages
are separate functions so each can be previewed (preview.py).
"""
import json
import math
import os
import sys

import numpy as np

ARGS = [a for a in sys.argv[1:] if not a.startswith("--")]
OUT = ARGS[0] if ARGS else "."
OPT = {a.split("=")[0]: a.split("=", 1)[1] for a in sys.argv[1:] if a.startswith("--") and "=" in a}

# ---- site -----------------------------------------------------------------
CX, CZ = (int(v) for v in OPT.get("--centre", "-300,-758").split(","))   # centre of the arena
X0, X1 = CX - 120, CX + 119       # build box
Z0, Z1 = CZ - 106, CZ + 105
Y0, Y1 = 36, 122                  # y 63 is the grass; the first air layer is 64
GROUND = 63
A0, B0 = 60, 44                   # semi-axes of the sand floor
SEAT_D, GAL_D, WALL_D, PIL_D = 36, 39, 41, 42   # offsets from the floor edge
PLAZA_D = 58
NB = 80                           # bays round the outer wall

W, H, D = X1 - X0 + 1, Y1 - Y0 + 1, Z1 - Z0 + 1

Xw = np.arange(X0, X1 + 1)[None, None, :]
Zw = np.arange(Z0, Z1 + 1)[None, :, None]
Yw = np.arange(Y0, Y1 + 1)[:, None, None]
REL = Yw - (GROUND + 1)           # 0 is the first air layer above the ground

xs = np.broadcast_to(np.arange(X0, X1 + 1)[None, :] - CX, (D, W)).astype(float)
zs = np.broadcast_to(np.arange(Z0, Z1 + 1)[:, None] - CZ, (D, W)).astype(float)

# d: offset from the sand floor's edge (negative inside), found by bisection on
# the offset ellipse (a0+d, b0+d)
_lo = np.full(xs.shape, -B0 + 0.01)
_hi = np.full(xs.shape, 400.0)
for _ in range(50):
    _mid = (_lo + _hi) / 2
    _big = (xs / (A0 + _mid)) ** 2 + (zs / (B0 + _mid)) ** 2 > 1
    _lo = np.where(_big, _mid, _lo)
    _hi = np.where(_big, _hi, _mid)
d2 = (_lo + _hi) / 2
theta2 = np.arctan2(zs / (B0 + d2), xs / (A0 + d2))
speed2 = np.sqrt(((A0 + d2) * np.sin(theta2)) ** 2 + ((B0 + d2) * np.cos(theta2)) ** 2)
dd = d2[None]


def bays(n):
    """Bay index and lateral offset in blocks from the bay centre, for n bays."""
    frac = (theta2 % (2 * math.pi)) / (2 * math.pi) * n
    idx = np.floor(frac).astype(int)
    q = (frac - idx - 0.5) * (2 * math.pi / n) * speed2
    return frac[None], idx[None], q[None]


frac, bb, qq = bays(NB)

# ---- materials ---------------------------------------------------------------
MATS = {}
LATE = set()      # attachables and fluids: placed after every solid block


def mat(name, states="", late=False):
    key = (name, states)
    if key not in MATS:
        MATS[key] = len(MATS) + 1
        if late:
            LATE.add(MATS[key])
    return MATS[key]


AIR = mat("air")
vox = np.zeros((H, D, W), dtype=np.int16)    # 0 leaves the world as it is


def put(mask, m):
    global vox
    vox = np.where(mask, m, vox)


def box(x0, y0, z0, x1, y1, z1):
    x0, x1, y0, y1, z0, z1 = min(x0, x1), max(x0, x1), min(y0, y1), max(y0, y1), min(z0, z1), max(z0, z1)
    return (Xw >= x0) & (Xw <= x1) & (Yw >= y0) & (Yw <= y1) & (Zw >= z0) & (Zw <= z1)


def cell(x, y, z, m):
    put(box(x, y, z, x, y, z), m)


SAND = mat("sand"); SMOOTH = mat("smooth_sandstone"); CUT = mat("cut_sandstone")
CHISEL = mat("chiseled_sandstone"); SANDST = mat("sandstone"); REDSAND = mat("red_sandstone")
RSAND = mat("red_sand"); SBRICK = mat("stone_bricks"); CBRICK = mat("chiseled_stone_bricks")
PAND = mat("polished_andesite"); ANDES = mat("andesite"); QPIL = mat("quartz_pillar")
GOLD = mat("gold_block"); BARS = mat("iron_bars"); QUARTZ = mat("quartz_block")
DIORITE = mat("polished_diorite"); SEALAMP = mat("sea_lantern"); SMSTONE = mat("smooth_stone")
LANT = mat("lantern", '["hanging"=false]', True); LANT_H = mat("lantern", '["hanging"=true]', True)
ENDROD = mat("end_rod", '["facing_direction"=1]', True)
DEEP = mat("polished_deepslate"); DBRICK = mat("deepslate_bricks"); DTILE = mat("deepslate_tiles")
CDEEP = mat("chiseled_deepslate"); PBLACK = mat("polished_blackstone"); PBBRICK = mat("polished_blackstone_bricks")
GILDED = mat("gilded_blackstone"); REDWOOL = mat("red_wool"); WHITEWOOL = mat("white_wool")
BLACKWOOL = mat("black_wool"); OBSID = mat("obsidian"); CRYING = mat("crying_obsidian")
DIAMOND = mat("diamond_block"); EMERALD = mat("emerald_block"); LAPIS = mat("lapis_block")
IRONB = mat("iron_block"); COPPER = mat("cut_copper"); WEATH = mat("weathered_cut_copper")
OXID = mat("oxidized_cut_copper"); GLASS = mat("glass"); GLASSP = mat("glass_pane")
SOULSAND = mat("soul_sand"); WATER = mat("water", "", True); DIRT = mat("dirt")
REDLAMP = mat("redstone_lamp", "", True); DUST = mat("redstone_wire", "", True)
TUFFB = mat("tuff_bricks"); MUDB = mat("mud_bricks"); NBRICK = mat("nether_brick"); REDNB = mat("red_nether_brick")
SHROOM = mat("shroomlight"); GLOWST = mat("glowstone"); BOOKS = mat("bookshelf"); HAY = mat("hay_block")
DARKPL = mat("dark_oak_planks"); SPRPL = mat("spruce_planks"); DARKLOG = mat("dark_oak_log")
BLACKCARP = mat("black_carpet", "", True); REDCARP = mat("red_carpet", "", True); WHITECARP = mat("white_carpet", "", True)
COBWEB = mat("web", "", True); MAGMA = mat("magma"); SMBASALT = mat("smooth_basalt"); CALCITE = mat("calcite")
AMETHYST = mat("amethyst_block"); BONE = mat("bone_block"); TERRA = mat("hardened_clay"); NETHERRACK = mat("netherrack")

PHASE = None   # filled by finish()


def stairs(name, facing, upside=False):
    return mat(name, f'["upside_down_bit"={"true" if upside else "false"},"weirdo_direction"={facing}]')


def slab(name, top=False):
    return mat(name, f'["minecraft:vertical_half"="{"top" if top else "bottom"}"]')


def prop(name, states="", late=True):
    return mat(name, states, late)


# ---- 1. surface shell ---------------------------------------------------------
def top_rel(dv):
    return 6 + np.floor((dv - 1) * 0.95)


def surface():
    ground = REL == -1
    # sand floor with rings and a medallion
    arena = ground & (dd < 0)
    put(arena, SAND)
    put(arena & (dd >= -1.5), SMOOTH)
    for a, b, m in ((-10.5, -9.5, RSAND), (-18.5, -17.5, RSAND), (-26.5, -25.5, SMOOTH), (-34.5, -33.5, RSAND)):
        put(arena & (dd >= a) & (dd < b), m)
    cx2 = (xs / A0) ** 2 + (zs / B0) ** 2
    put(ground & (cx2[None] < 0.05), RSAND)
    put(ground & (cx2[None] < 0.013), GOLD)
    put(ground & (dd < -2) & ((np.abs(zs[None]) < 0.6) | (np.abs(xs[None]) < 0.6)) & (cx2[None] > 0.05), SANDST)
    # plaza
    plaza = ground & (dd >= PIL_D + 2) & (dd < PLAZA_D)
    put(ground & (dd >= 0) & (dd < PIL_D + 2), SBRICK)
    put(plaza, PAND)
    put(plaza & (dd < PIL_D + 4), SBRICK)
    put(plaza & (dd >= PLAZA_D - 1), SBRICK)
    put(plaza & (((dd >= 49) & (dd < 50)) | ((dd >= 55) & (dd < 56))), ANDES)
    chk = ((np.floor(xs[None] / 2) + np.floor(zs[None] / 2)) % 2) == 0
    put(plaza & chk & (dd >= 51) & (dd < 54), DIORITE)
    # avenues from the four gates
    ax_x = np.abs(xs[None]) > A0 - 6
    ax_z = np.abs(zs[None]) > B0 - 6
    ave = ground & (dd >= PIL_D) & (dd < PLAZA_D) & (((np.abs(zs[None]) <= 6) & ax_x & (np.abs(zs[None]) < 40)) |
                                                      ((np.abs(xs[None]) <= 6) & ax_z & (np.abs(xs[None]) < 40)))
    put(ave, SBRICK)
    put(ave & (((np.abs(zs[None]) <= 0.6) & ax_x) | ((np.abs(xs[None]) <= 0.6) & ax_z)), CBRICK)

    # seating mass
    mass = (dd >= 0) & (dd < SEAT_D) & (REL >= 0)
    trow = top_rel(dd)
    solid = mass & (REL <= np.where(dd < 1, 5, trow))
    put(solid, SANDST)
    seat = mass & (dd >= 1) & (REL == trow)
    put(seat & (dd < 12), REDSAND)
    put(seat & (dd >= 12) & (dd < 24), SMOOTH)
    put(seat & (dd >= 24), CUT)
    for d0 in (12, 24):
        put(mass & (dd >= d0) & (dd < d0 + 1) & (REL == trow + 1), CHISEL)
    put(solid & (dd < 1) & (REL <= 4), CUT)
    put(solid & (dd < 1) & (REL == 5), SMOOTH)
    put((dd >= 0) & (dd < 1) & (REL >= 6) & (REL <= 8), BARS)
    put((dd >= 0) & (dd < 1) & (REL == 9) & (np.abs(qq) < 0.6) & (bb % 2 == 0), LANT)
    lane = ((frac % 4) < 0.09) | ((frac % 4) > 3.91)
    put(seat & (dd >= 1) & lane, PAND)
    put(mass & (dd >= 3) & (REL == trow + 1) & (np.floor(dd) % 5 == 0) & lane, LANT)

    # gallery: four hollow storeys behind the outer wall
    gal = (dd >= SEAT_D) & (dd < GAL_D) & (REL >= 0) & (REL <= 39)
    put(gal & ((REL == 9) | (REL == 19) | (REL == 29) | (REL == 39)), SMOOTH)
    put((dd >= SEAT_D) & (dd < GAL_D) & (REL == -1), SBRICK)
    put((dd >= SEAT_D) & (dd < SEAT_D + 1) & (REL >= 0) & (REL <= 38) & ~np.isin(REL, (9, 19, 29)), CUT)
    for r_ in (8, 18, 28, 38):
        put((dd >= 37.2) & (dd < 38.4) & (REL == r_) & (np.abs(qq) < 0.55) & (bb % 2 == 0), LANT_H)

    # outer wall, pilasters, cornice, attic
    wall = (dd >= GAL_D) & (dd < WALL_D) & (REL >= 0) & (REL <= 49)
    pil = (dd >= WALL_D) & (dd < PIL_D) & (REL >= 0) & (REL <= 49) & (np.abs(qq) >= 2.4)
    put(wall, CUT)
    put(pil, SMOOTH)
    put((wall | pil) & ((REL % 10) == 9), CHISEL)
    put(wall & (REL <= 0), SBRICK)
    put((dd >= GAL_D) & (dd < PIL_D) & (REL == -1), SBRICK)
    u = REL % 10
    opening = (REL >= 0) & (REL <= 39) & (dd >= GAL_D - 0.1) & (dd < PIL_D + 0.1) & (
        ((u <= 5) & (np.abs(qq) <= 1.5)) | ((u > 5) & (qq ** 2 + (u - 5.0) ** 2 <= 2.55)))
    put(opening, AIR)
    put((dd >= GAL_D - 0.1) & (dd < PIL_D + 0.1) & (REL >= 42) & (REL <= 47) & (np.abs(qq) <= 1.0), AIR)
    put((wall | pil) & (REL >= 48) & (REL <= 49), CHISEL)
    put((dd >= GAL_D) & (dd < 45.5) & (REL == 50), SMOOTH)
    put((dd >= PIL_D) & (dd < 44.6) & (REL == 49), CUT)
    put((dd >= PIL_D) & (dd < 43.4) & (REL == 48), CUT)
    put((dd >= 44.4) & (dd < 45.5) & (REL == 51) & (np.floor(frac * 2) % 2 == 0) & (np.abs(qq) < 3.4), CUT)
    half = (2 * math.pi / NB) * speed2[None] / 2
    mast = (dd >= 39.4) & (dd < 40.4) & (np.abs(qq) < 0.6) & (bb % 4 == 2)
    put(mast & (REL >= 51) & (REL <= 58), ENDROD)
    put(mast & (REL == 59), GOLD)
    put((dd >= 36.0) & (dd < 37.0) & (REL == 40) & (np.abs(qq) < 3.2), CUT)
    # hanging banners down the pilasters
    ban = (dd >= PIL_D - 0.9) & (dd < PIL_D) & (np.abs(np.abs(qq) - half + 0.6) < 0.5) & (bb % 4 == 0)
    put(ban & (REL >= 22) & (REL <= 31), REDWOOL)
    put(ban & (REL == 32), GOLD)
    put(ban & (REL >= 21) & (REL <= 21), WHITEWOOL)


surface()


# ---- 2. gates, pens, plaza ------------------------------------------------------
ASC = {"+x": 0, "-x": 1, "+z": 2, "-z": 3}      # weirdo_direction by ascent direction
if os.environ.get("COLO_FLIP_STAIRS"):
    ASC = {"+x": 1, "-x": 0, "+z": 3, "-z": 2}

GATE_W = 5.5                                     # half-width of a gate's opening
START_GATE = None
PORTS = []
LEVERS_AT = []                                       # sally ports: (axis, sign)


def door(hinge, upper, facing, opened=False):
    return mat("iron_door", f'["door_hinge_bit"={"true" if hinge else "false"},"open_bit"={"true" if opened else "false"},'
                            f'"upper_block_bit"={"true" if upper else "false"},"minecraft:cardinal_direction"="{facing}"]', True)


def gates():
    global START_GATE
    for axis in ("x", "z"):
        lat = np.abs(zs[None]) if axis == "x" else np.abs(xs[None])
        along_pos = xs[None] if axis == "x" else zs[None]
        near = (np.abs(along_pos) > (A0 if axis == "x" else B0) - 8)
        tun = (((lat <= GATE_W) & (REL >= 0) & (REL <= 8)) |
               ((lat ** 2 + (REL - 8.0) ** 2 <= (GATE_W + 0.1) ** 2) & (REL > 8))) & near & (dd < PIL_D + 4) & (dd > -14)
        lintel = (lat >= 0) & (lat < 8.5) & (REL >= 15) & (REL <= 21) & near & (dd >= GAL_D) & (dd < PIL_D + 2)
        pillar = (lat >= 6.2) & (lat < 8.5) & (REL >= 0) & (REL <= 21) & near & (dd >= GAL_D) & (dd < PIL_D + 2)
        put(pillar, QPIL)
        put(pillar & (REL >= 20), GOLD)
        put(lintel & ~tun, QUARTZ)
        put(lintel & ~tun & (lat < 1.2) & (REL >= 17) & (REL <= 19), GOLD)
        put(tun, AIR)
        floor = (lat <= GATE_W) & (REL == -1) & near & (dd < PIL_D + 4) & (dd > -14)
        put(floor, PAND)
        put(floor & (lat <= 1.0), CBRICK)
        put(tun & (REL == 12) & (lat < 0.6) & (np.floor(np.abs(along_pos)) % 6 == 0), LANT_H)
        # the portcullis at the arena end; the director opens the north one
        grille = (lat <= GATE_W) & near & (dd >= 0) & (dd < 1) & (REL >= 0)
        put(grille & (REL <= 8), BARS)
        put(grille & (REL > 8) & (REL <= 14), CUT)
        for sign in (1, -1):
            sel = (along_pos * sign > 0)
            if axis == "z" and sign == -1:
                gm = grille & (REL >= 0) & (REL <= 8) & sel
                idx = np.argwhere(gm)
                START_GATE = (int(X0 + idx[:, 2].min()), int(Y0 + idx[:, 0].min()), int(Z0 + idx[:, 1].min()),
                              int(X0 + idx[:, 2].max()), int(Y0 + idx[:, 0].max()), int(Z0 + idx[:, 1].max()))
                put(box(*START_GATE), BARS)
                continue
            PORTS.append((axis, sign))
            facing = "north" if axis == "z" else "east"
            cen = (np.abs(zs[None] + 0.5) < 1.0) if axis == "x" else (np.abs(xs[None] + 0.5) < 1.0)
            for hinge, off in ((False, -1), (True, 0)):
                lane_ = ((zs[None] == off) if axis == "x" else (xs[None] == off)) & grille & sel
                put(lane_ & (REL == 0), door(hinge, False, facing))
                put(lane_ & (REL == 1), door(hinge, True, facing))
            # a pier either side of the port carries a lever on its arena face
            pier = grille & sel & (REL <= 8) & (((zs[None] == 3) | (zs[None] == -4)) if axis == "x" else ((xs[None] == 3) | (xs[None] == -4)))
            put(pier, CUT)
            LEVERS_AT.append((axis, sign, pier))
    # corner stiffeners so gate pillars read as engaged columns
    put((dd >= GAL_D) & (dd < PIL_D + 2) & (REL == -1), SBRICK)


gates()


FACE = {(0, -1): "north", (0, 1): "south", (-1, 0): "west", (1, 0): "east"}


def gate_levers():
    """A lever on each face of the two piers of a gate's sally port."""
    for axis, sign, pier in LEVERS_AT:
        step = (sign, 0) if axis == "x" else (0, sign)
        for yi, zi, xi in np.argwhere(pier & (REL == 1)):
            x, z = X0 + int(xi), Z0 + int(zi)
            for k in (-1, 1):                       # k=-1: arena side, k=1: outside
                cx_, cz_ = x + k * step[0], z + k * step[1]
                face = FACE[(k * step[0], k * step[1])]
                cell(cx_, Y0 + int(yi), cz_, mat("lever", f'["lever_direction"="{face}","open_bit"=false]', True))


gate_levers()


PEN_ANGLES = [15 + 30 * k for k in range(12)]
PENS = []


def pens():
    for k, deg in enumerate(PEN_ANGLES):
        th = math.radians(deg)
        px, pz = A0 * math.cos(th), B0 * math.sin(th)
        ax = "x" if abs(math.cos(th) / A0) >= abs(math.sin(th) / B0) else "z"
        sx = 1 if (math.cos(th) if ax == "x" else math.sin(th)) > 0 else -1
        if ax == "x":
            zc = CZ + int(round(pz))
            edge = next(x for x in (range(CX, X1) if sx > 0 else range(CX, X0, -1)) if d2[zc - Z0, x - X0] >= 0)
            gate = (edge, 64, zc - 1, edge, 66, zc + 1)
            room = (edge + sx, 64, zc - 1, edge + 4 * sx, 66, zc + 1)
            spawn = (edge + 2 * sx, 64, zc)
        else:
            xc = CX + int(round(px))
            edge = next(z for z in (range(CZ, Z1) if sx > 0 else range(CZ, Z0, -1)) if d2[z - Z0, xc - X0] >= 0)
            gate = (xc - 1, 64, edge, xc + 1, 66, edge)
            room = (xc - 1, 64, edge + sx, xc + 1, 66, edge + 4 * sx)
            spawn = (xc, 64, edge + 2 * sx)
        put(box(*room), AIR)
        r = (min(room[0], room[3]), min(room[2], room[5]), max(room[0], room[3]), max(room[2], room[5]))
        put(box(r[0] - (0 if ax == "x" else 1), GROUND, r[1] - (1 if ax == "x" else 0),
                r[2] + (0 if ax == "x" else 1), GROUND, r[3] + (1 if ax == "x" else 0)), SBRICK)
        put(box(*gate), BARS)
        put(box(spawn[0], 66, spawn[2], spawn[0], 66, spawn[2]), LANT_H)
        PENS.append({"id": k + 1, "angle_deg": round(deg), "spawn": {"x": spawn[0], "y": 64, "z": spawn[2]},
                     "gate": dict(zip(("x0", "y0", "z0", "x1", "y1", "z1"), gate)),
                     "open": "fill %d %d %d %d %d %d air" % gate})


pens()


def plaza_furniture():
    lamp_d = (dd >= PIL_D + 5.5) & (dd < PIL_D + 6.5) & (np.abs(qq) < 0.5) & (bb % 2 == 0)
    ax_gate = ((np.abs(zs[None]) < 9) & (np.abs(xs[None]) > A0 - 6)) | ((np.abs(xs[None]) < 9) & (np.abs(zs[None]) > B0 - 6))
    lamp = lamp_d & ~ax_gate
    put(lamp & (REL >= 0) & (REL <= 3), QPIL)
    put(lamp & (REL == 4), GOLD)
    put(lamp & (REL == 5), LANT)
    for axis in ("x", "z"):
        lat = np.abs(zs[None]) if axis == "x" else np.abs(xs[None])
        along = np.abs(xs[None]) if axis == "x" else np.abs(zs[None])
        near = along > (A0 if axis == "x" else B0) - 4
        col = (np.abs(lat - 7.5) < 0.6) & (dd >= PIL_D + 3) & (dd < PLAZA_D - 1) & (np.floor(dd) % 4 == 1) & near
        put(col & (REL >= 0) & (REL <= 6), QPIL)
        put(col & (REL == 7), CHISEL)
        put(col & (REL == 8), LANT)
    # four fountains on the diagonals
    for deg in (45, 135, 225, 315):
        th = math.radians(deg)
        fx = CX + int(round((A0 + 50) * math.cos(th)))
        fz = CZ + int(round((B0 + 50) * math.sin(th)))
        rr = np.sqrt((Xw - fx) ** 2 + (Zw - fz) ** 2)
        put((rr < 7.2) & (REL == -1), SBRICK)
        put((rr >= 5.2) & (rr < 6.4) & (REL >= 0) & (REL <= 1), QUARTZ)
        put((rr >= 5.2) & (rr < 6.4) & (REL == 2) & (np.floor(np.arctan2(Zw - fz, Xw - fx) * 6) % 2 == 0), LANT)
        put((rr < 5.2) & (REL == 0), WATER)
        put((rr < 1.3) & (REL >= 0) & (REL <= 4), QPIL)
        put((rr < 1.3) & (REL == 5), SEALAMP)
        put((rr >= 2.4) & (rr < 3.4) & (REL == -1), PAND)


plaza_furniture()


# ---- 3. promenade light ring and the royal box ---------------------------------------
RING_D = 43.0
RING = []          # (x, z) cells in order, one wide, four-connected, with a straight stretch every other cell
LADDERS = []       # (x, z) of the foot of each ladder up to the wall top


def light_ring():
    """A runway of lamps along the top of the outer wall, lit from one lever.

    The path hops between points on a two-block lattice so it is always made of
    straight runs of three or more cells: a repeater needs a straight stretch,
    and dust only carries fifteen blocks.
    """
    a, b = A0 + RING_D, B0 + RING_D
    nodes = []
    for i in range(3000):
        t = 2 * math.pi * (i / 3000) + math.radians(98)
        n = (CX + 2 * round(a * math.cos(t) / 2), CZ + 2 * round(b * math.sin(t) / 2))
        if not nodes or nodes[-1] != n:
            nodes.append(n)
    cells = [nodes[0]]
    for n in nodes[1:]:
        cur = cells[-1]
        while cur[0] != n[0]:
            cur = (cur[0] + (1 if n[0] > cur[0] else -1), cur[1])
            cells.append(cur)
        while cur[1] != n[1]:
            cur = (cur[0], cur[1] + (1 if n[1] > cur[1] else -1))
            cells.append(cur)
    out, seen = [], set()
    for c in cells:
        if c not in seen:
            seen.add(c)
            out.append(c)
    RING.extend(out[:-10])          # open at the start so the signal cannot circulate for ever
    yl, yd = GROUND + 1 + 50, GROUND + 1 + 51
    since = 0
    for i, (x, z) in enumerate(RING):
        cell(x, yl, z, REDLAMP)
        prev = RING[i - 1] if i else None
        nxt = RING[i + 1] if i + 1 < len(RING) else None
        straight = bool(prev and nxt and (prev[0] == nxt[0] or prev[1] == nxt[1]))
        since += 1
        if since >= 10 and straight:
            since = 0
            face = {(1, 0): "east", (-1, 0): "west", (0, 1): "south", (0, -1): "north"}[(prev[0] - x, prev[1] - z)]
            cell(x, yd, z, mat("unpowered_repeater", f'["minecraft:cardinal_direction"="{face}","repeater_delay"=0]', True))
        else:
            cell(x, yd, z, DUST)
    # ladders up the inside of the wall, one beside each gate; the lever sits by the first
    on_ring = set(RING)
    for sx, sz in ((12, 1), (-12, -1), (1, -1), (-1, 1)):
        if sx in (12, -12):
            x = CX + sx
            z = next(zz for zz in (range(CZ, Z1) if sz > 0 else range(CZ, Z0, -1)) if d2[zz - Z0, x - X0] >= 38.0)
            face = 2 if sz > 0 else 3          # faces the arena, away from the wall it climbs
        else:
            z = CZ + sz * 14
            x = next(xx for xx in (range(CX, X1) if sx > 0 else range(CX, X0, -1)) if d2[z - Z0, xx - X0] >= 38.0)
            face = 4 if sx > 0 else 5
        LADDERS.append((x, z))
        # clear the crenellation where the ladder tops out, then the ladder itself
        put(box(x - 2, GROUND + 52, z - 2, x + 2, GROUND + 52, z + 2) & (dd >= 39) & (dd < 46), AIR)
        put(box(x, GROUND + 1 + 40, z, x, GROUND + 1 + 50, z), mat("ladder", f'["facing_direction"={face}]', True))
    x, z = RING[0]
    for dx, dz in ((1, 0), (-1, 0), (0, 1), (0, -1)):
        lx, lz = x + dx, z + dz
        if (lx, lz) not in on_ring and d2[lz - Z0, lx - X0] > 40.5:
            cell(lx, yd, lz, mat("lever", '["lever_direction"="up_east_west","open_bit"=false]', True))
            break


light_ring()


def royal_box():
    ax = (np.abs(xs[None]) <= 8) & (zs[None] > 0)
    area = ax & (dd >= 0.5) & (dd < 10)
    put(area & (REL >= 15) & (REL <= 26), AIR)
    tun_z = ((np.abs(xs[None]) <= GATE_W + 0.1) & (REL >= 0) & (REL <= 8)) | ((xs[None] ** 2 + (REL - 8.0) ** 2 <= (GATE_W + 0.2) ** 2) & (REL > 8))
    put(area & ~tun_z & (dd >= 1) & (REL >= 0) & (REL <= 13), SANDST)
    put(area & ~tun_z & (REL == 14), SMOOTH)
    put(ax & (dd >= 0.5) & (dd < 10) & (REL == 15), SMOOTH)
    put(ax & (dd >= 0.5) & (dd < 1.5) & (REL == 15), GOLD)
    put(ax & (dd >= 1.5) & (dd < 9.5) & (np.abs(xs[None]) < 7.5) & (REL == 15), CUT)
    carpet_cells = ax & (dd >= 2) & (dd < 9) & (np.abs(xs[None]) < 7) & (REL == 16)
    put(carpet_cells, REDCARP)
    put(ax & (dd >= 0.5) & (dd < 1.5) & (REL >= 16) & (REL <= 17), BARS)
    for px_ in (-8, 8):
        for dpos in (1.0, 9.0):
            pc = ax & (np.abs(xs[None] - px_) < 0.6) & (np.abs(dd - dpos) < 0.7)
            put(pc & (REL >= 16) & (REL <= 23), QPIL)
            put(pc & (REL == 24), GOLD)
    put(ax & (dd >= 0.5) & (dd < 10) & (REL == 24) & (np.abs(xs[None]) < 8.5), REDWOOL)
    put(ax & (dd >= 0.5) & (dd < 10) & ((np.abs(xs[None]) > 8) | (dd < 1)) & (REL == 24), GOLD)
    put(ax & (dd >= 1.5) & (dd < 9.5) & (np.abs(xs[None]) < 7.5) & (REL == 23) & (np.abs(xs[None]) % 3 < 0.5) & (np.floor(dd) % 3 == 0), LANT_H)
    # throne: gold seat, tall back, quartz arms
    th = (np.abs(xs[None]) < 1.5) & (np.abs(dd - 8) < 0.7)
    put(th & (REL == 16), GOLD)
    put(th & (REL >= 17) & (REL <= 20) & (np.abs(xs[None]) < 0.6), GOLD)
    put((np.abs(np.abs(xs[None]) - 1.5) < 0.6) & (np.abs(dd - 8) < 0.7) & (REL == 16), QUARTZ)
    put(th & (REL == 21) & (np.abs(xs[None]) < 0.6), DIAMOND)
    # fanfare: noteblocks in the floor under a dust line, a button at the end
    fan = ax & (np.abs(dd - 5) < 0.7)
    notes = fan & (np.abs(xs[None]) <= 5) & (np.floor(xs[None]) % 2 == 0) & (REL == 15)
    put(notes, mat("noteblock"))
    put(fan & (np.abs(xs[None]) <= 5) & (REL == 16) & (np.floor(xs[None]) % 2 == 0), DUST)
    put(fan & (np.abs(xs[None]) <= 5) & (REL == 16) & (np.floor(xs[None]) % 2 != 0), DUST)
    put(fan & (xs[None] == 6) & (REL == 16), DUST)
    put(fan & (xs[None] == 7) & (REL == 16), mat("stone_button", '["facing_direction"=1,"button_pressed_bit"=false]', True))


royal_box()


# ---- 4. the hypogeum --------------------------------------------------------------
FLOOR_Y = 49                      # hall floor block; interiors start at y 50
SPACES = []                       # (interior mask, floor, wall, ceiling)
ROOM_ID = np.full(vox.shape, -1, dtype=np.int8)


def shift(m, dy, dz, dx):
    out = np.zeros_like(m)
    ys = slice(max(dy, 0), H + min(dy, 0)); yd = slice(max(-dy, 0), H + min(-dy, 0))
    zs_ = slice(max(dz, 0), D + min(dz, 0)); zd = slice(max(-dz, 0), D + min(-dz, 0))
    xs_ = slice(max(dx, 0), W + min(dx, 0)); xd = slice(max(-dx, 0), W + min(-dx, 0))
    out[ys, zs_, xs_] = m[yd, zd, xd]
    return out


def dilate(m):
    r = m.copy()
    for a in ((1, 0, 0), (-1, 0, 0), (0, 1, 0), (0, -1, 0), (0, 0, 1), (0, 0, -1)):
        r |= shift(m, *a)
    return r


UNDER = np.zeros(vox.shape, dtype=bool)         # every carved interior


def space(mask, floor, wall, ceil, room=None):
    global UNDER
    SPACES.append((mask, floor, wall, ceil))
    UNDER = UNDER | mask
    if room is not None:
        ROOM_ID[mask] = room


def ybetween(a, b):
    return (Yw >= a) & (Yw <= b)


f16, b16, q16 = bays(16)
hw16 = (2 * math.pi / 16) / 2 * speed2[None]
f8, b8, q8 = bays(8)
uu = dd + 14.0                                  # 0 at a room's inner wall, about 8.5 at its back


def carve():
    """Interiors of every underground space, as masks."""
    hall = (dd < -21) & ybetween(50, 59)
    ring = (dd >= -19.5) & (dd < -15.5) & ybetween(50, 56)
    axis_x = (np.abs(zs[None]) <= 4.5) & (np.abs(xs[None]) > A0 - 30)
    axis_z = (np.abs(xs[None]) <= 4.5) & (np.abs(zs[None]) > B0 - 30)
    rooms = (dd >= -14) & (dd < -5.5) & (np.abs(q16) <= hw16 - 1.0) & ybetween(50, 57) & ~axis_x & ~axis_z
    # doors from the ring into each chamber, arches from the hall into the ring
    door = (np.abs(q16) <= 1.5) & (dd >= -15.8) & (dd < -13.7) & (ybetween(50, 53) | ((Yw == 54) & (np.abs(q16) <= 0.7)))
    arch = (np.abs(q8) <= 2.5) & (dd >= -21.4) & (dd < -19.2) & (ybetween(50, 55) | ((Yw == 56) & (np.abs(q8) <= 1.5)) | ((Yw == 57) & (np.abs(q8) < 0.6)))
    # four axis tunnels from the ring out to the plaza, climbing the last stretch
    flat = np.zeros(vox.shape, dtype=bool)
    ramp = np.zeros(vox.shape, dtype=bool)
    steps = []
    for axis in ("x", "z"):
        lat = np.abs(zs[None]) if axis == "x" else np.abs(xs[None])
        along = np.abs(xs[None]) if axis == "x" else np.abs(zs[None])
        near = along > (A0 if axis == "x" else B0) - 30
        flat |= (lat <= 3.5) & near & (dd >= -19.6) & (dd < 33) & ybetween(50, 56)
        floor_y = 50 + np.floor(dd - 33)
        r = (lat <= 3.5) & near & (dd >= 33) & (dd < 48.5)
        fy = np.minimum(floor_y, GROUND)
        ramp |= r & (Yw > fy) & (Yw <= fy + 6)
        steps.append((axis, r & (Yw == fy) & (dd < 46.5)))
    return hall, ring, rooms, door, arch, flat, ramp, steps


HALL, RINGC, ROOMS, DOORS, ARCHES, TUNN, RAMP, STEPS = carve()
space(HALL, "mosaic", DBRICK, DEEP)
space(RINGC, DTILE, DBRICK, DEEP)
space(ROOMS, DTILE, DBRICK, DEEP)
space(DOORS, DTILE, DBRICK, DEEP)
space(ARCHES, DTILE, DBRICK, DEEP)
space(TUNN, DTILE, DBRICK, DEEP)
space(RAMP, SBRICK, SBRICK, SBRICK)
ROOM_ID[ROOMS] = np.broadcast_to(b16, ROOMS.shape)[ROOMS]


def finish_shells():
    put(UNDER, AIR)
    for m, floor, wall, ceil in SPACES:
        fl = shift(m, -1, 0, 0) & ~UNDER & (Yw <= GROUND - 1)
        ce = shift(m, 1, 0, 0) & ~UNDER & (Yw <= GROUND - 1)
        sh = dilate(m) & ~UNDER & (Yw <= GROUND - 1)
        put(sh & ~fl & ~ce, wall)
        put(ce, ceil)
        if floor != "mosaic":
            put(fl, floor)
    # the hall floor: concentric bands round a gold medallion
    fh = shift(HALL, -1, 0, 0) & ~UNDER
    e = np.sqrt((xs[None] / 40.0) ** 2 + (zs[None] / 24.0) ** 2)
    band = np.floor(e * 14).astype(int) % 4
    put(fh, DEEP)
    put(fh & (band == 1), PBBRICK)
    put(fh & (band == 2), DTILE)
    put(fh & (band == 3), PBLACK)
    put(fh & (e < 0.16), GOLD)
    put(fh & (e >= 0.16) & (e < 0.2), CDEEP)
    put(fh & ((np.abs(xs[None]) < 0.6) | (np.abs(zs[None]) < 0.6)) & (e >= 0.2), GILDED)
    # stair blocks of the four ramps
    for axis, mask in STEPS:
        along = xs[None] if axis == "x" else zs[None]
        for sign, key in ((1, "+" + axis), (-1, "-" + axis)):
            put(mask & (along * sign > 0), stairs("stone_brick_stairs", ASC[key]))


finish_shells()


DECOR = []          # entity and chest-content commands, run after the blocks are down


# ---- 5. furnishing ---------------------------------------------------------------------
CHEST = mat("chest", '["minecraft:cardinal_direction"="south"]', True)
ANVIL = mat("anvil", "", True); GRIND = mat("grindstone", "", True); SMITH = mat("smithing_table")
FLETCH = mat("fletching_table"); CARTO = mat("cartography_table"); LOOM = mat("loom")
ENCH = mat("enchanting_table", "", True); LECT = mat("lectern", "", True); BREW = mat("brewing_stand", "", True)
CAULD = mat("cauldron", "", True); BARREL = mat("barrel"); BLAST = mat("blast_furnace"); SMOKER = mat("smoker")
FURN = mat("furnace"); COMP = mat("composter"); JUKE = mat("jukebox"); BELL = mat("bell", "", True)
CAMP = mat("campfire", "", True); CANDLE = mat("candle", "", True); CHAIN = mat("chain", "", True)
STAND = {}          # armour stand markers by gear set: removed from the build, summoned as entities
for _g in ("leather", "copper", "iron", "diamond", "netherite", "gold"):
    STAND[_g] = mat("__stand_" + _g)

GEAR = {
    "leather": ("leather_helmet", "leather_chestplate", "leather_leggings", "leather_boots", "wooden_sword"),
    "copper": ("copper_helmet", "copper_chestplate", "copper_leggings", "copper_boots", "copper_sword"),
    "iron": ("iron_helmet", "iron_chestplate", "iron_leggings", "iron_boots", "iron_sword"),
    "diamond": ("diamond_helmet", "diamond_chestplate", "diamond_leggings", "diamond_boots", "diamond_sword"),
    "netherite": ("netherite_helmet", "netherite_chestplate", "netherite_leggings", "netherite_boots", "netherite_sword"),
    "gold": ("golden_helmet", "golden_chestplate", "golden_leggings", "golden_boots", "golden_sword"),
}

ROOM_TYPES = ["forge", "armoury", "gear", "library", "barracks", "infirmary", "trophy", "treasury",
              "ops", "baths", "kitchen", "shrine", "cages", "cells", "storage", "lounge"]


def room(i):
    return ROOMS & (b16 == i)


def where(i, w):
    base = room(i)
    if w == "back":
        return base & (uu >= 7.4)
    if w == "front":
        return base & (uu < 1.6) & (np.abs(q16) > 2.0)
    if w == "sides":
        return base & (np.abs(q16) >= hw16 - 2.2)
    if w == "mid":
        return base & (np.abs(uu - 4.2) < 0.55) & (np.abs(q16) < hw16 - 3.0)
    if w == "centre":
        return base & (np.abs(uu - 4.2) < 1.6) & (np.abs(q16) < 2.2)
    if w == "all":
        return base
    raise ValueError(w)


def feat(i, w, y0, y1, items, pitch=2):
    mk = where(i, w) & ybetween(y0, y1)
    if isinstance(items, (int, np.integer)):
        put(mk, int(items))
        return
    lat = q16 + 60.0
    k = np.floor(lat / pitch).astype(int) % len(items)
    on = (lat % pitch) < 1.0
    for j, m in enumerate(items):
        put(mk & on & (k == j), m)


def finish_room(i, floor=None, wall=None, ceil=None):
    m = room(i)
    fl = shift(m, -1, 0, 0) & ~UNDER
    ce = shift(m, 1, 0, 0) & ~UNDER
    sh = dilate(m) & ~UNDER & (Yw <= GROUND - 1)
    if wall:
        put(sh & ~fl & ~ce & ~(Yw >= 58), wall)
    if floor:
        put(fl, floor)
    if ceil:
        put(ce & (Yw <= GROUND - 1), ceil)


def lights(i, y=57, cycle=(0, 1, 2)):
    mk = room(i) & (Yw == y) & (np.abs(uu - 2.5) < 0.6) | room(i) & (Yw == y) & (np.abs(uu - 6.0) < 0.6)
    mk &= (np.abs((q16 + 60.0) % 5.0 - 2.0) < 0.6)
    put(mk, LANT_H)


def furnish_rooms():
    # --- 0 forge
    finish_room(0, PBLACK, NBRICK, DBRICK)
    feat(0, "back", 50, 50, [BLAST, ANVIL, SMITH, BLAST, GRIND, ANVIL], 2)
    feat(0, "back", 51, 51, [CHAIN, 0, CAULD, 0], 2)
    feat(0, "sides", 50, 50, [MAGMA, NETHERRACK, MAGMA], 3)
    feat(0, "centre", 50, 51, [IRONB], 1)
    feat(0, "centre", 52, 52, [CAMP], 1)
    feat(0, "mid", 50, 50, [CHEST, 0, 0, 0], 3)
    lights(0)
    # --- 1 armoury
    finish_room(1, DEEP, DBRICK, CDEEP)
    feat(1, "back", 50, 50, [CHEST, ANVIL, CHEST, GRIND, CHEST, SMITH], 2)
    feat(1, "back", 51, 51, [STAND["netherite"], 0, STAND["diamond"], 0], 2)
    feat(1, "sides", 50, 50, [STAND["iron"], 0, 0, STAND["copper"], 0, 0], 3)
    feat(1, "mid", 50, 50, [DARKPL, DARKPL, DARKPL], 1)
    feat(1, "mid", 51, 51, [CANDLE, 0, 0], 3)
    lights(1)
    # --- 2 gear selection: one stand and a chest for each tier
    finish_room(2, DBRICK, TUFFB, CDEEP)
    feat(2, "back", 50, 50, [STAND["leather"], 0, STAND["copper"], 0, STAND["iron"], 0, STAND["diamond"], 0, STAND["netherite"], 0, STAND["gold"], 0], 1)
    feat(2, "sides", 50, 50, [CHEST, 0, 0], 3)
    feat(2, "mid", 50, 50, [PBBRICK], 2)
    feat(2, "mid", 51, 51, [STAND["netherite"], 0, 0, 0], 4)
    lights(2)
    # --- 3 enchanting library
    finish_room(3, DARKPL, DARKPL, DARKPL)
    feat(3, "sides", 50, 53, BOOKS)
    feat(3, "back", 50, 54, BOOKS)
    feat(3, "centre", 50, 50, [ENCH], 1)
    feat(3, "mid", 50, 50, [LECT, 0, 0, 0], 3)
    feat(3, "mid", 51, 51, [CANDLE, 0, 0, 0], 3)
    lights(3)
    # --- 4 barracks: wool bunks along both sides
    finish_room(4, SPRPL, SPRPL, DARKPL)
    feat(4, "sides", 50, 50, [REDWOOL, REDWOOL, WHITEWOOL], 1)
    feat(4, "sides", 51, 51, [REDCARP, REDCARP, 0], 1)
    feat(4, "sides", 52, 52, [WHITEWOOL, WHITEWOOL, REDWOOL], 1)
    feat(4, "back", 50, 50, [BARREL, CHEST, BARREL, 0], 2)
    feat(4, "mid", 50, 50, [DARKPL, 0, 0], 3)
    lights(4)
    # --- 5 infirmary
    finish_room(5, QUARTZ, QUARTZ, QUARTZ)
    feat(5, "sides", 50, 50, [WHITEWOOL], 1)
    feat(5, "sides", 51, 51, [WHITECARP, WHITECARP, 0], 1)
    feat(5, "back", 50, 50, [BREW, CAULD, BREW, CHEST], 2)
    feat(5, "mid", 50, 50, [REDCARP, REDCARP, REDCARP, REDCARP], 1)
    lights(5)
    # --- 6 trophy hall: gold pedestals under glass
    finish_room(6, GILDED, CHISEL, QUARTZ)
    feat(6, "mid", 50, 51, [GOLD, DIAMOND, EMERALD, LAPIS], 3)
    feat(6, "mid", 52, 52, [GLASS], 3)
    feat(6, "sides", 50, 50, [QPIL, 0, 0], 3)
    feat(6, "sides", 51, 54, [REDWOOL, 0, 0], 3)
    feat(6, "back", 50, 50, [CHEST, 0, 0, 0], 4)
    lights(6)
    # --- 7 treasury, behind an iron door
    finish_room(7, GOLD, DEEP, CHISEL)
    feat(7, "back", 50, 50, [GOLD, DIAMOND, GOLD, EMERALD], 1)
    feat(7, "back", 51, 51, [CHEST, GOLD, CHEST, 0], 2)
    feat(7, "sides", 50, 50, [GOLD, CHEST], 2)
    lights(7)
    # --- 8 operations room
    finish_room(8, DEEP, PBBRICK, DBRICK)
    lever = mat("lever", '["lever_direction"="up_east_west","open_bit"=false]', True)
    for j, side in enumerate((-1, 1)):
        bank = where(8, "back") & (np.sign(q16) == side) & (np.abs(q16) > 1.5) & (np.abs(q16) < hw16 - 2.5)
        put(bank & (Yw == 50), REDLAMP)
        put(bank & (Yw == 51), DUST)
        end = bank & (Yw == 51) & (np.abs(q16) < 2.6)
        put(end, lever)
    feat(8, "mid", 50, 50, [CARTO, LECT, 0, 0], 3)
    feat(8, "sides", 50, 50, [FLETCH, 0, 0], 3)
    lights(8)
    # --- 9 baths
    finish_room(9, QUARTZ, QUARTZ, CHISEL)
    feat(9, "all", 49, 49, 0)
    pool = room(9) & (np.abs(uu - 4.2) < 2.8) & (np.abs(q16) < hw16 - 3.5) & (Yw == 50)
    put(pool, WATER)
    put(room(9) & (np.abs(uu - 4.2) >= 2.8) & (np.abs(uu - 4.2) < 3.8) & (np.abs(q16) < hw16 - 2.5) & (Yw == 50), QUARTZ)
    feat(9, "sides", 50, 50, [QPIL, 0, 0], 3)
    feat(9, "sides", 51, 53, [QPIL, 0, 0], 3)
    lights(9)
    # --- 10 kitchen and mess
    finish_room(10, SBRICK, SBRICK, DARKPL)
    feat(10, "back", 50, 50, [SMOKER, FURN, BLAST, COMP, BARREL], 2)
    feat(10, "mid", 50, 50, [DARKPL, DARKPL, DARKPL, 0], 1)
    feat(10, "mid", 51, 51, [mat("cake", "", True), 0, 0, 0], 4)
    feat(10, "sides", 50, 50, [HAY, 0, BARREL], 3)
    lights(10)
    # --- 11 shrine
    finish_room(11, OBSID, CRYING, CHISEL)
    feat(11, "back", 50, 52, [GOLD, 0, 0], 3)
    feat(11, "centre", 50, 50, [GOLD], 1)
    feat(11, "centre", 51, 51, [CANDLE], 1)
    feat(11, "sides", 50, 50, [CANDLE, 0, 0, 0], 2)
    put(room(11) & (Yw == 57) & (np.abs(uu - 4.2) < 0.6) & (np.abs(q16) < 0.6), mat("soul_lantern", '["hanging"=true]', True))
    # --- 12 beast cages
    finish_room(12, DBRICK, DBRICK, DEEP)
    feat(12, "sides", 50, 52, [BARS, BARS, 0], 1)
    feat(12, "mid", 50, 50, [HAY, HAY, 0], 1)
    feat(12, "back", 50, 50, [BONE, 0, COBWEB], 2)
    lights(12)
    # --- 13 cells
    finish_room(13, SBRICK, SBRICK, SBRICK)
    feat(13, "mid", 50, 52, [BARS, BARS, BARS, 0], 1)
    feat(13, "back", 50, 50, [HAY, 0, HAY], 3)
    feat(13, "sides", 50, 50, [0, HAY, 0], 3)
    lights(13)
    # --- 14 storage
    finish_room(14, DBRICK, DBRICK, DEEP)
    feat(14, "back", 50, 51, [BARREL, CHEST, BARREL], 1)
    feat(14, "sides", 50, 51, [BARREL, BARREL, CHEST, 0], 1)
    feat(14, "mid", 50, 50, [HAY, 0, BARREL, 0], 2)
    lights(14)
    # --- 15 gladiators' lounge
    finish_room(15, DARKPL, SPRPL, DARKPL)
    feat(15, "mid", 50, 50, [BARREL, 0, 0], 3)
    feat(15, "mid", 51, 51, [CANDLE, 0, 0], 3)
    feat(15, "sides", 50, 50, [REDCARP, REDCARP, REDCARP], 1)
    feat(15, "back", 50, 50, [BARREL, CHEST, JUKE, 0], 2)
    lights(15)


furnish_rooms()


def furnish_hall():
    # ring of columns with gold bases and capitals
    for k in range(12):
        th = math.radians(15 + 30 * k)
        px, pz = CX + int(round(30 * math.cos(th))), CZ + int(round(17.5 * math.sin(th)))
        put(box(px, 50, pz, px, 58, pz), CDEEP)
        put(box(px, 50, pz, px, 50, pz), GOLD)
        put(box(px, 59, pz, px, 59, pz), GOLD)
    # centre fountain
    rr = np.sqrt(xs ** 2 + zs ** 2)[None]
    put(HALL & (rr >= 3.4) & (rr < 4.6) & ybetween(50, 51), QUARTZ)
    put(HALL & (rr < 3.4) & (Yw == 50), WATER)
    put(HALL & (rr < 1.2) & ybetween(50, 55), QPIL)
    put(HALL & (rr < 1.2) & (Yw == 56), SEALAMP)
    # chandeliers
    for k in range(8):
        th = math.radians(22.5 + 45 * k)
        px, pz = CX + int(round(14 * math.cos(th))), CZ + int(round(8 * math.sin(th)))
        put(box(px, 58, pz, px, 59, pz), CHAIN)
        put(box(px, 57, pz, px, 57, pz), LANT_H)
    # gear on show: a stand on a blackstone plinth for every tier, twice
    sets = ["leather", "copper", "iron", "diamond", "netherite", "gold"] * 2
    for k, g in enumerate(sets):
        th = math.radians(30 * k)
        px, pz = CX + int(round(10.5 * math.cos(th))), CZ + int(round(6.5 * math.sin(th)))
        put(box(px, 50, pz, px, 50, pz), PBBRICK)
        put(box(px, 51, pz, px, 51, pz), STAND[g])
    # four water lifts to the arena, a glass tank each
    for sx, sz in ((22, 12), (-22, 12), (22, -12), (-22, -12)):
        px, pz = CX + sx, CZ + sz
        put(box(px - 2, 50, pz - 2, px + 2, 59, pz + 2), GLASS)
        put(box(px - 1, 50, pz - 1, px + 1, 62, pz + 1), AIR)
        put(box(px - 1, FLOOR_Y, pz - 1, px + 1, FLOOR_Y, pz + 1), SOULSAND)
        put(box(px - 1, 50, pz - 1, px + 1, 62, pz + 1), WATER)
        cell(px, GROUND, pz, WATER)
        put(box(px - 2, 49, pz - 2, px + 2, 49, pz + 2), GOLD)
        put(box(px - 1, 49, pz - 1, px + 1, 49, pz + 1), SOULSAND)
        # cap at arena level: eight blocks round an iron hatch, a lever on one
        put(box(px - 1, GROUND, pz - 1, px + 1, GROUND, pz + 1), CHISEL)
        cell(px, GROUND, pz, WATER)
        cell(px, GROUND + 1, pz, mat("iron_trapdoor", '["direction"=0,"open_bit"=false,"upside_down_bit"=false]', True))
        cell(px + 1, GROUND + 1, pz, CHISEL)
        cell(px + 1, GROUND + 2, pz, mat("lever", '["lever_direction"="up_east_west","open_bit"=false]', True))
    # lamp arms along the axes, one lever each at the fountain
    for sign in (1, -1):
        for axis in ("x", "z"):
            reach = 36 if axis == "x" else 20
            for t in range(6, reach):
                x, z = (CX + sign * t, CZ) if axis == "x" else (CX, CZ + sign * t)
                cell(x, FLOOR_Y, z, REDLAMP)
                if t % 12 == 11:
                    fx = {(1, "x"): "west", (-1, "x"): "east", (1, "z"): "north", (-1, "z"): "south"}[(sign, axis)]
                    cell(x, 50, z, mat("unpowered_repeater", f'["minecraft:cardinal_direction"="{fx}","repeater_delay"=0]', True))
                else:
                    cell(x, 50, z, DUST)
            x, z = (CX + sign * 5, CZ) if axis == "x" else (CX, CZ + sign * 5)
            cell(x, 50, z, mat("lever", '["lever_direction"="up_east_west","open_bit"=false]', True))


furnish_hall()


def furnish_circulation():
    # ring corridor: lanterns and banner panels in the walls
    lantern = RINGC & (Yw == 56) & (np.abs((dd + 17.5)) < 0.6) & (np.floor(frac * 0 + (theta2[None] * 20)) % 2 == 0)
    put(lantern, LANT_H)
    # tunnels: lanterns on the centre line, red panels between them
    for axis in ("x", "z"):
        lat = np.abs(zs[None]) if axis == "x" else np.abs(xs[None])
        along = np.abs(xs[None]) if axis == "x" else np.abs(zs[None])
        tun = TUNN & (lat <= 3.5)
        put(tun & (Yw == 56) & (lat < 0.6) & (np.floor(along) % 6 == 0), LANT_H)
        wallc = dilate(TUNN) & ~UNDER & ybetween(51, 55) & (lat > 3.6) & (lat < 5.2) & (np.floor(along) % 8 < 2) & (dd > -12)
        put(wallc, REDWOOL)
        put(dilate(TUNN) & ~UNDER & (Yw == 56) & (lat > 3.6) & (lat < 5.2) & (np.floor(along) % 8 < 2) & (dd > -12), GOLD)
        floor = shift(TUNN, -1, 0, 0) & ~UNDER & (lat < 1.2)
        put(floor, PBBRICK)
    # ramp exit: posts and lanterns either side
    for axis in ("x", "z"):
        lat = np.abs(zs[None]) if axis == "x" else np.abs(xs[None])
        post = (np.abs(lat - 4.0) < 0.6) & (dd >= 47.5) & (dd < 48.5) & (np.abs(qq) > -1)
        put(post & (REL >= 0) & (REL <= 3), QPIL)
        put(post & (REL == 4), LANT)
        put((np.abs(lat - 4.0) < 0.6) & (dd >= 44) & (dd < 48.5) & (REL == 0) & ~post & (dd >= 44), SBRICK)


furnish_circulation()


def vault():
    """An iron door into the treasury, with a button on each side of it."""
    wall = (np.abs(q16) <= 1.0) & (dd >= -15.8) & (dd < -13.7) & ybetween(50, 51) & (b16 == 7)
    put(wall, AIR)
    cells = np.argwhere(wall & (Yw == 50))
    if not len(cells):
        return
    yi, zi, xi = cells[len(cells) // 2]
    x, z = X0 + int(xi), Z0 + int(zi)
    radial = (x - CX) / (A0 + 1.0), (z - CZ) / (B0 + 1.0)
    toward_ring = (-1 if radial[0] > 0 else 1, 0) if abs(radial[0]) >= abs(radial[1]) else (0, -1 if radial[1] > 0 else 1)
    lateral = (0, 1) if toward_ring[0] else (1, 0)
    facing = "east" if toward_ring[0] else "north"
    cell(x, 50, z, door(False, False, facing))
    cell(x, 51, z, door(False, True, facing))
    for lat in (-1, 1):
        for side in (-1, 1):
            cx_ = x + lat * lateral[0] + side * toward_ring[0]
            cz_ = z + lat * lateral[1] + side * toward_ring[1]
            sx_ = x + lat * lateral[0]
            sz_ = z + lat * lateral[1]
            if vox[51 - Y0, cz_ - Z0, cx_ - X0] == AIR and vox[51 - Y0, sz_ - Z0, sx_ - X0] not in (0, AIR):
                fd = {(0, -1): 2, (0, 1): 3, (-1, 0): 4, (1, 0): 5}[(side * toward_ring[0], side * toward_ring[1])]
                cell(cx_, 51, cz_, mat("stone_button", f'["facing_direction"={fd},"button_pressed_bit"=false]', True))


vault()


# ---- 5b. secret doors ---------------------------------------------------------------
# Each axis tunnel hides a closet behind a two-high stone plug. Two sticky
# pistons hold it shut while powered; a lever on the tunnel wall cuts the power
# and they pull the plug into a pocket in the wall. One dust cell sits on a
# block that touches the lower piston and beside the upper one, so both follow
# the same lever. Lever up (on) is shut.
SECRETS = []                                   # (x, y, z) of each lever, for the docs


def secret_door(axis, sa, sl):
    a0 = (A0 if axis == "x" else B0) + 10
    u = (sa, 0) if axis == "x" else (0, sa)
    v = (0, sl) if axis == "x" else (sl, 0)

    def at(a, l):
        return CX + u[0] * a + v[0] * l, CZ + u[1] * a + v[1] * l

    def put_at(a, l, y, m):
        x, z = at(a, l)
        cell(x, y, z, m)

    def fill_at(a0_, a1_, l0_, l1_, y0_, y1_, m):
        for a in range(a0_, a1_ + 1):
            for l in range(l0_, l1_ + 1):
                for y in range(y0_, y1_ + 1):
                    put_at(a, l, y, m)

    facing = {(1, 0): 4, (-1, 0): 5, (0, 1): 2, (0, -1): 3}[(-u[0], -u[1])]
    sticky = mat("sticky_piston", f'["facing_direction"={facing}]', True)
    # the closet: a shell, then the room
    fill_at(a0 - 5, a0 + 9, 4, 10, 49, 53, DBRICK)
    fill_at(a0 - 4, a0 + 1, 5, 9, 50, 52, AIR)
    fill_at(a0 - 4, a0 + 1, 5, 9, 49, 49, DTILE)
    fill_at(a0 - 4, a0 + 1, 5, 9, 53, 53, DEEP)
    put_at(a0 - 3, 9, 50, CHEST)
    x, z = at(a0 - 3, 9)
    ROOM_ID[50 - Y0, z - Z0, x - X0] = ROOM_TYPES.index("trophy")
    for a in (a0 - 3, a0):
        put_at(a, 7, 52, LANT_H)
    fill_at(a0 - 1, a0, 9, 9, 50, 50, GILDED)
    # the plug and its pocket
    fill_at(a0, a0, 4, 4, 50, 51, SBRICK)
    fill_at(a0 + 1, a0 + 1, 4, 4, 50, 51, AIR)
    for y in (50, 51):
        put_at(a0 + 2, 4, y, sticky)
    # block under the dust (powers the lower piston), pocket for the upper dust cell
    put_at(a0 + 2, 5, 50, DBRICK)
    fill_at(a0 + 2, a0 + 8, 6, 6, 50, 50, DBRICK)
    fill_at(a0 + 2, a0 + 2, 5, 5, 51, 51, DUST)
    fill_at(a0 + 2, a0 + 8, 6, 6, 51, 51, DUST)
    fill_at(a0 + 8, a0 + 8, 5, 5, 50, 50, DBRICK)
    put_at(a0 + 8, 5, 51, DUST)
    put_at(a0 + 8, 4, 51, DBRICK)
    lv = (-v[0], -v[1])
    face = FACE[lv]
    put_at(a0 + 8, 3, 51, mat("lever", f'["lever_direction"="{face}","open_bit"=true]', True))
    SECRETS.append(at(a0 + 8, 3) + (51,))


for _axis, _sa, _sl in (("x", 1, 1), ("x", -1, -1), ("z", 1, -1), ("z", -1, 1)):
    secret_door(_axis, _sa, _sl)


# ---- 6. entities and chest contents -------------------------------------------------
LOOT = {
    "forge": [("iron_ingot", 64), ("gold_ingot", 32), ("coal", 64), ("copper_ingot", 64), ("netherite_scrap", 8), ("anvil", 1)],
    "armoury": [("diamond_sword", 1), ("netherite_axe", 1), ("bow", 1), ("crossbow", 1), ("arrow", 64), ("shield", 1), ("trident", 1), ("mace", 1)],
    "gear": [("leather_chestplate", 1), ("copper_chestplate", 1), ("iron_chestplate", 1), ("diamond_chestplate", 1), ("netherite_chestplate", 1), ("golden_chestplate", 1)],
    "library": [("book", 32), ("enchanted_book", 4), ("experience_bottle", 32), ("lapis_lazuli", 64)],
    "barracks": [("bread", 32), ("white_wool", 16), ("shield", 1), ("iron_sword", 1)],
    "infirmary": [("golden_apple", 16), ("enchanted_golden_apple", 4), ("glass_bottle", 16), ("honey_bottle", 8)],
    "trophy": [("diamond", 16), ("emerald", 32), ("gold_ingot", 32), ("totem_of_undying", 2)],
    "treasury": [("diamond", 64), ("emerald", 64), ("gold_ingot", 64), ("netherite_ingot", 8), ("enchanted_golden_apple", 8), ("totem_of_undying", 4)],
    "ops": [("redstone", 64), ("repeater", 16), ("lever", 16), ("redstone_lamp", 16)],
    "baths": [("sponge", 4)],
    "kitchen": [("cooked_beef", 64), ("bread", 64), ("golden_carrot", 32), ("cake", 4)],
    "shrine": [("enchanted_golden_apple", 2), ("candle", 8)],
    "cages": [("bone", 32), ("lead", 8)],
    "cells": [("bread", 8)],
    "storage": [("cobblestone", 64), ("oak_planks", 64), ("torch", 64), ("arrow", 64), ("coal", 64)],
    "lounge": [("cooked_porkchop", 32), ("golden_apple", 4), ("bread", 32)],
}


def decor_commands():
    cmds = []
    for gear, mid in STAND.items():
        for yi, zi, xi in np.argwhere(vox == mid):
            x, y, z = X0 + int(xi), Y0 + int(yi), Z0 + int(zi)
            cmds.append(f"summon armor_stand {x}.5 {y} {z}.5")
            cmds.append(f"tag @e[type=armor_stand,x={x}.5,y={y},z={z}.5,r=0.4] add colo_stand")
            sel = f"@e[type=armor_stand,tag=colo_stand,x={x}.5,y={y},z={z}.5,r=0.4]"
            for slot, item in zip(("slot.armor.head", "slot.armor.chest", "slot.armor.legs", "slot.armor.feet", "slot.weapon.mainhand"), GEAR[gear]):
                cmds.append(f"replaceitem entity {sel} {slot} 0 {item} 1")
            vox[yi, zi, xi] = AIR
    chest_ids = [v for (n, _), v in MATS.items() if n == "chest"]
    for cid in chest_ids:
        for yi, zi, xi in np.argwhere(vox == cid):
            x, y, z = X0 + int(xi), Y0 + int(yi), Z0 + int(zi)
            rid = int(ROOM_ID[yi, zi, xi])
            kind = ROOM_TYPES[rid] if 0 <= rid < len(ROOM_TYPES) else "storage"
            for slot, (item, n) in enumerate(LOOT.get(kind, LOOT["storage"])):
                cmds.append(f"replaceitem block {x} {y} {z} slot.container {slot} {item} {n}")
    return cmds


DECOR.extend(decor_commands())


# ---- 9. output ------------------------------------------------------------------------


def sealed(pen):
    sp = pen["spawn"]
    start = (sp["y"] - Y0, sp["z"] - Z0, sp["x"] - X0)
    seen, todo = {start}, [start]
    while todo:
        y, z, x = todo.pop()
        for dy, dz, dx in ((1, 0, 0), (-1, 0, 0), (0, 1, 0), (0, -1, 0), (0, 0, 1), (0, 0, -1)):
            n = (y + dy, z + dz, x + dx)
            if not (0 <= n[0] < H and 0 <= n[1] < D and 0 <= n[2] < W) or n in seen or vox[n] not in (0, AIR):
                continue
            seen.add(n)
            todo.append(n)
            if len(seen) > 200:
                return False
    return True


def write_clear():
    """Air above the ground over the whole box, so a rebuild starts from nothing."""
    cmds = []
    for x in range(X0, X1 + 1, 32):
        for z in range(Z0, Z1 + 1, 32):
            for ya, yb in ((GROUND + 1, GROUND + 31), (GROUND + 32, Y1)):
                cmds.append(f"fill {x} {ya} {z} {min(x + 31, X1)} {yb} {min(z + 31, Z1)} air")
    open(os.path.join(OUT, "clear.cmds"), "w").write("\n".join(cmds) + "\n")


def write_plan():
    """The single file arena.sh feeds the server: ticking areas, then every command."""
    xa, xb, za, zb = X0 & ~15, X1 | 15, Z0 & ~15, Z1 | 15
    xm, zm = ((xa + xb) // 32) * 16 - 1, ((za + zb) // 32) * 16 - 1
    areas = []
    for xl, xh in ((xa, xm), (xm + 1, xb)):
        for zl, zh in ((za, zm), (zm + 1, zb)):
            areas.append(f"# area {xl} {zl} {xh} {zh}")
    parts = [os.path.join(OUT, f) for f in ("clear.cmds", "build.cmds", "decor.cmds")]
    with open(os.path.join(OUT, "plan.txt"), "w") as out:
        out.write("\n".join(areas) + "\n")
        for f in parts:
            out.write(open(f).read())


def write_layout():
    leaks = [p["id"] for p in PENS if not sealed(p)]
    if leaks:
        sys.exit(f"pens {leaks} are not sealed: a mob could walk out with the gates shut")
    area = {"x0": (CX - 64) & ~15, "z0": (CZ - 48) & ~15, "x1": (CX + 63) | 15, "z1": (CZ + 47) | 15}
    layout = {
        "center": {"x": CX, "y": 64, "z": CZ},
        "ground_y": GROUND,
        "floor_radius": B0 - 2,
        "outer_radius": A0 + PIL_D,
        "areas": [area],
        "pens": PENS,
        "start_gate": {"name": "squad_start", "gate": dict(zip(("x0", "y0", "z0", "x1", "y1", "z1"), START_GATE)),
                       "open": "fill %d %d %d %d %d %d air" % START_GATE},
        "boss": {"x": CX, "y": 65, "z": CZ},
    }
    json.dump(layout, open(os.path.join(OUT, "layout.json"), "w"), indent=1)


def compress():
    src = vox
    if "--against" in OPT:
        old = np.load(OPT["--against"])
        names = json.load(open(OPT["--against"] + ".mats"))
        now = {f"{a} {b}".strip(): v for (a, b), v in MATS.items()}
        table = np.zeros(len(names) + 1, dtype=np.int16)
        for i, n in enumerate(names, 1):
            table[i] = now.get(n, -1)
        old = table[old]
        changed = vox != old
        late = np.isin(vox, list(LATE))
        changed = changed | (dilate(changed) & late)
        src = np.where(changed & (vox == 0), AIR, np.where(changed, vox, 0)).astype(np.int16)
    if "--save" in OPT:
        np.save(OPT["--save"], vox)
        json.dump([f"{a} {b}".strip() for (a, b) in MATS], open(OPT["--save"] + ".mats", "w"))
    by_id = {v: k for k, v in MATS.items()}
    rects = {}
    for yi in range(H):
        layer = src[yi]
        rows = {}
        for zi in range(D):
            row = layer[zi]
            if not row.any():
                continue
            change = np.nonzero(np.diff(row))[0] + 1
            starts = np.concatenate(([0], change))
            ends = np.concatenate((change, [W])) - 1
            for a, b in zip(starts, ends):
                m = int(row[a])
                if m and not by_id[m][0].startswith("__"):
                    rows.setdefault((m, int(a), int(b)), []).append(zi)
        for (m, a, b), zl in rows.items():
            run0 = prev = zl[0]
            for z in zl[1:] + [None]:
                if z is not None and z == prev + 1:
                    prev = z
                    continue
                rects.setdefault((m, a, b, run0, prev), []).append(yi)
                if z is not None:
                    run0 = prev = z
    cmds = []
    for (m, a, b, z0_, z1_), yl in rects.items():
        name, states = by_id[m]
        r0 = p = yl[0]
        for y in yl[1:] + [None]:
            if y is not None and y == p + 1:
                p = y
                continue
            vol = (b - a + 1) * (z1_ - z0_ + 1)
            step = max(1, 32000 // vol)
            yy = r0
            while yy <= p:
                ye = min(p, yy + step - 1)
                cmds.append((1 if m in LATE else 0, Y0 + yy, f"fill {X0 + a} {Y0 + yy} {Z0 + z0_} {X0 + b} {Y0 + ye} {Z0 + z1_} {name} {states}".rstrip()))
                yy = ye + 1
            if y is not None:
                r0 = p = y
    cmds.sort(key=lambda c: (c[0], c[1]))
    open(os.path.join(OUT, "build.cmds"), "w").write("\n".join(c[2] for c in cmds) + "\n")
    patch = "--against" in OPT
    decor = [] if patch else [f"kill @e[type=armor_stand,tag=colo_stand,x={X0},y={Y0},z={Z0},dx={W - 1},dy={H - 1},dz={D - 1}]"] + DECOR
    open(os.path.join(OUT, "decor.cmds"), "w").write("\n".join(decor) + ("\n" if decor else ""))
    if not patch:
        write_plan()
    return len(cmds)


def feed():
    import subprocess
    ns, pod = OPT["--feed"].split("/", 1)
    ctr = os.environ.get("LAB_CONTAINER", "jdwillmsen-crew-lab-prd-minecraft-bedrock")
    script = 'while IFS= read -r c; do send-command "$c" >/dev/null 2>&1; sleep 0.045; done'
    cmds = []
    for f in ("build.cmds", "decor.cmds"):
        cmds += [l.strip() for l in open(os.path.join(OUT, f)) if l.strip()]
    for i in range(0, len(cmds), 1000):
        r = subprocess.run(["kubectl", "-n", ns, "exec", "-i", pod, "-c", ctr, "--", "sh", "-c", script],
                           input="\n".join(cmds[i:i + 1000]) + "\n", text=True, capture_output=True)
        if r.returncode:
            sys.exit(f"feed failed after {i} commands: {r.stderr.strip()[:200]}")
        print(f"fed {min(i + 1000, len(cmds))}/{len(cmds)}", flush=True)


if __name__ == "__main__":
    os.makedirs(OUT, exist_ok=True)
    write_layout()
    if "--against" not in OPT:
        write_clear()
    n = compress()
    json.dump({"mats": [f"{a} {b}".strip() for (a, b) in MATS], "x0": X0, "z0": Z0, "y0": Y0, "cx": CX, "cz": CZ}, open(os.path.join(os.path.dirname(OPT["--save"]) if "--save" in OPT else OUT, "mats.json"), "w"))
    print(f"{n} fill commands, {len(DECOR)} decor commands, {int((vox != 0).sum())} voxels")
    if "--feed" in OPT:
        feed()
