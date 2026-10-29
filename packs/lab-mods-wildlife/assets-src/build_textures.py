import struct
import zlib
from dataclasses import dataclass
from functools import lru_cache

import cat_rig
from species import Species

SIZE = cat_rig.TEXTURE

# Per species and variant: base coat, markings, underside, nose, eye.
COLOURS = {
    ("tiger", "orange"): {"base": (222, 128, 42), "mark": (24, 20, 18), "belly": (244, 236, 220),
                          "nose": (70, 44, 44), "eye": (214, 190, 60)},
    ("tiger", "white"): {"base": (238, 234, 226), "mark": (122, 98, 82), "belly": (250, 248, 242),
                         "nose": (120, 78, 80), "eye": (150, 178, 196)},
    ("cheetah", "default"): {"base": (214, 174, 98), "mark": (30, 24, 20), "belly": (240, 224, 188),
                             "nose": (40, 30, 30), "eye": (200, 150, 40)},
}


def texture_names(s: Species) -> list[tuple[str, str]]:
    """(variant, texture path relative to the resource pack, without extension)."""
    if not s.variants:
        return [("default", f"textures/entity/{s.id}")]
    return [(v, f"textures/entity/{s.id}_{v}") for v in s.variants]


def chunk(tag: bytes, body: bytes) -> bytes:
    return struct.pack(">I", len(body)) + tag + body + struct.pack(">I", zlib.crc32(tag + body) & 0xFFFFFFFF)


def png(pixels: bytearray) -> bytes:
    rows = b"".join(b"\x00" + bytes(pixels[y * SIZE * 4:(y + 1) * SIZE * 4]) for y in range(SIZE))
    ihdr = struct.pack(">IIBBBBB", SIZE, SIZE, 8, 6, 0, 0, 0)
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", ihdr)
            + chunk(b"IDAT", zlib.compress(rows, 9)) + chunk(b"IEND", b""))


def noise(*v: int) -> int:
    """Platform-independent hash, so regenerated textures are byte-identical."""
    return zlib.crc32(struct.pack(f">{len(v)}i", *v))


@dataclass(frozen=True)
class Px:
    part: str
    face: str
    x: float
    y: float
    z: float
    col: int
    row: int
    cols: int
    rows: int
    origin: tuple
    size: tuple

    @property
    def up(self) -> float:
        """0 at the cube's underside, 1 at its top."""
        return (self.y - self.origin[1]) / self.size[1]

    @property
    def under(self) -> bool:
        return self.face == "bottom"


def faces(part: cat_rig.Part):
    """Yield (face, px, py, cols, rows, point(col,row)) following the box-UV unfolding."""
    u, v = part.uv
    ox, oy, oz = part.origin
    w, h, d = part.size

    def side(col, row):
        return oy + h - (row + 0.5)

    yield "right", u, v + d, d, h, lambda c, r: (ox, side(c, r), oz + d - (c + 0.5))
    yield "front", u + d, v + d, w, h, lambda c, r: (ox + c + 0.5, side(c, r), oz)
    yield "left", u + d + w, v + d, d, h, lambda c, r: (ox + w, side(c, r), oz + c + 0.5)
    yield "back", u + 2 * d + w, v + d, w, h, lambda c, r: (ox + w - (c + 0.5), side(c, r), oz + d)
    yield "top", u + d, v, w, d, lambda c, r: (ox + c + 0.5, oy + h, oz + d - (r + 0.5))
    yield "bottom", u + d + w, v, w, d, lambda c, r: (ox + c + 0.5, oy, oz + r + 0.5)


def face_features(p: Px, pal: dict):
    """Eyes and nose shared by both cats; None when the pixel is not one of them."""
    if p.part == "head" and p.face == "front":
        eye_col = round(p.cols * 0.2)
        eye_row = round(p.rows * 0.35)
        if p.col in (eye_col, p.cols - 1 - eye_col):
            if p.row == eye_row:
                return pal["eye"]
            if p.row == eye_row - 1 and p.cols >= 8:
                return pal["mark"]
    if p.part == "muzzle" and p.face == "front" and p.row == 0:
        return pal["nose"]
    return None


@lru_cache(maxsize=None)
def stripes(side: int, length: int, seed: int) -> tuple:
    """Stripe plan for one flank: (centre, top width, lean, fork direction, reach) along z from -length/2."""
    out = []
    z = -length / 2 + 1.0
    i = 0
    while z < length / 2 - 0.5:
        n = noise(side, seed, i)
        width = 2.5 + (n % 3) * 0.5
        lean = ((n >> 4) % 7 - 3) * 0.5
        fork = 0 if (n >> 8) % 3 else (1 if (n >> 10) % 2 else -1)
        reach = 0.7 + ((n >> 12) % 4) * 0.1
        out.append((z, width, lean, fork, reach))
        z += width + 2.0 + (n >> 16) % 4
        i += 1
    return tuple(out)


def on_stripe(z: float, depth: float, plan: tuple) -> bool:
    """depth: 0 on the back line to 1 at the belly. Bands taper, lean and some fork towards the belly."""
    for centre, width, lean, fork, reach in plan:
        if depth > reach:
            continue
        c = centre + lean * depth
        if abs(z - c) <= width * (1 - 0.6 * depth) / 2:
            return True
        if fork and depth > 0.4 and abs(z - (c + fork * (depth - 0.4) * 7)) <= 0.55:
            return True
    return False


def tiger(p: Px, pal: dict):
    feat = face_features(p, pal)
    if feat:
        return feat
    base, mark, belly = pal["base"], pal["mark"], pal["belly"]
    if p.part in ("chest", "rear"):
        if p.face == "front":
            return belly if p.up < 0.6 else base
        if p.under or p.up < 0.28 + (noise(int(p.z)) % 3 - 1) * 0.04:
            return belly
        if p.face in ("front", "back"):
            return base
        side = -1 if (p.x < 0 if p.face == "top" else p.face == "right") else 1
        plan = stripes(side, 22, 3)
        if p.face == "top":
            # Stripes cross the back and fade towards the spine, breaking at the midline.
            if abs(p.x) < 1.0:
                return base
            return mark if on_stripe(p.z, abs(p.x) / 12, plan) else base
        return mark if on_stripe(p.z, (1 - p.up) / 0.72, plan) else base
    if p.part == "head":
        if p.face == "top":
            return mark if abs(p.x) < 1.1 or (2.4 < abs(p.x) < 3.4 and p.z % 4 < 2.5) else base
        if p.face in ("right", "left"):
            if p.up < 0.4:
                return belly
            return mark if (p.z + (noise(int(p.y)) % 2) * 0.7) % 3.3 < 1.2 else base
        if p.face == "front":
            if p.up < 0.4:
                return belly
            return mark if p.col in (0, p.cols - 1) and p.row in (4, 5) else base
        return base
    if p.part == "muzzle":
        if p.face == "front" and p.col == p.cols // 2 and p.row == 1:
            return mark
        return base if p.face == "top" else belly
    if p.part.startswith("ear"):
        if p.face == "back":
            return belly if p.col == p.cols // 2 and p.row == p.rows // 2 else mark
        return base
    if p.part == "tail":
        rear = p.z - p.origin[2]
        thick = 1.5 + noise(int(rear // 3)) % 2
        return mark if rear > p.size[2] - 3 or rear % 3 < thick else base
    return leg(p, pal)


def leg(p: Px, pal: dict):
    """Pale paws and inner legs, dark bands of uneven thickness across the rest."""
    centre = p.origin[0] + p.size[0] / 2
    inner = p.face in ("right", "left") and abs(p.x) < abs(centre)
    if p.y < 2.5 or inner or p.under:
        return pal["belly"]
    k = int((p.y - 2.5) // 3)
    n = noise(k, int(centre * 2))
    thick = 1.0 + (n >> 3) % 3 * 0.5
    return pal["mark"] if n % 4 and (p.y - 2.5) % 3 < thick else pal["base"]


def small_spot(p: Px, seed: int, cell=(4, 3), chance=90) -> bool:
    """2x2 spots at irregular offsets inside a grid of cells laid out per face."""
    face_seed = seed + zlib.crc32(f"{p.part}{p.face}".encode()) % 100000
    cw, ch = cell
    gx, gy = p.col // cw, p.row // ch
    n = noise(gx, gy, face_seed)
    if n % 100 >= chance:
        return False
    ox = gx * cw + (n >> 8) % (cw - 1)
    oy = gy * ch + (n >> 12) % (ch - 1)
    return ox <= p.col <= ox + 1 and oy <= p.row <= oy + 1


def cheetah(p: Px, pal: dict):
    feat = face_features(p, pal)
    if feat:
        return feat
    base, mark, belly = pal["base"], pal["mark"], pal["belly"]
    if p.part == "head":
        if p.face == "front":
            eye_col = round(p.cols * 0.2)
            # Tear marks run from each eye down to the mouth.
            if p.col in (eye_col, p.cols - 1 - eye_col) and p.row > round(p.rows * 0.35):
                return mark
            return belly if p.up < 0.3 else base
        if p.under or p.up < 0.3:
            return belly
        return mark if p.face in ("top", "right", "left") and small_spot(p, 5, (4, 4), 45) else base
    if p.part == "muzzle":
        return base if p.face == "top" else belly
    if p.part.startswith("ear"):
        return mark if p.face == "back" else base
    if p.part == "tail":
        rear = p.z - p.origin[2]
        if rear > p.size[2] - 2:
            return belly
        if rear > p.size[2] * 0.55 and int(rear // 2) % 2 == 0:
            return mark
        return base
    if p.under or p.up < 0.28 or p.face == "bottom":
        return belly
    if p.part.endswith("leg") and p.y < 1.5:
        return belly
    return mark if small_spot(p, 11) else base


SKINS = {"tiger": tiger, "cheetah": cheetah}


def paint(s: Species, variant: str) -> bytes:
    pal = COLOURS[(s.id, variant)]
    skin = SKINS[s.id]
    pixels = bytearray(SIZE * SIZE * 4)
    for part in cat_rig.layout(cat_rig.PARAMS[s.id]):
        for face, fx, fy, cols, rows, point in faces(part):
            for row in range(rows):
                for col in range(cols):
                    x, y, z = point(col, row)
                    px = Px(part.name, face, x, y, z, col, row, cols, rows, part.origin, part.size)
                    r, g, b = skin(px, pal)
                    i = ((fy + row) * SIZE + fx + col) * 4
                    pixels[i:i + 4] = bytes((r, g, b, 255))
    return png(pixels)
