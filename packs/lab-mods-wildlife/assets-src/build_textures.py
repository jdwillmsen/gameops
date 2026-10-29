import math
import struct
import zlib
from dataclasses import dataclass

import cat_rig
from species import Species

SIZE = cat_rig.TEXTURE

# Per species and variant: base coat, markings, underside, nose, eye.
COLOURS = {
    ("tiger", "orange"): {"base": (222, 128, 42), "mark": (24, 20, 18), "belly": (244, 236, 220),
                          "nose": (214, 120, 120), "eye": (214, 190, 60)},
    ("tiger", "white"): {"base": (238, 234, 226), "mark": (112, 90, 72), "belly": (250, 248, 242),
                         "nose": (214, 150, 150), "eye": (120, 170, 210)},
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
        if p.row == round(p.rows * 0.35) and p.col in (eye_col, p.cols - 1 - eye_col):
            return pal["eye"]
    if p.part == "muzzle" and p.face == "front" and p.row == 0:
        return pal["nose"]
    return None


def tiger(p: Px, pal: dict):
    feat = face_features(p, pal)
    if feat:
        return feat
    wob_y, wob_x = noise(int(p.y // 2)) % 3 - 1, noise(int(p.x // 2)) % 3 - 1
    if p.part in ("chest", "rear"):
        if p.under or p.up < 0.28:
            return pal["belly"]
        band = (p.z + (wob_x if p.face == "top" else wob_y)) % 4.0
        return pal["mark"] if band < 1.6 else pal["base"]
    if p.part == "head":
        if p.face == "top":
            return pal["mark"] if abs(p.x) % 3 < 1 else pal["base"]
        if p.face in ("right", "left"):
            if p.up < 0.4:
                return pal["belly"]
            return pal["mark"] if (p.z + wob_y) % 3.5 < 1.1 else pal["base"]
        if p.face == "front":
            return pal["belly"] if p.up < 0.45 else pal["base"]
        return pal["base"]
    if p.part == "muzzle":
        if p.face == "front" and p.row == 1 and p.col == p.cols // 2:
            return pal["mark"]
        return pal["belly"]
    if p.part.startswith("ear"):
        return pal["mark"] if p.face == "back" else pal["base"]
    if p.part == "tail":
        rear = p.z - p.origin[2]
        return pal["mark"] if int(rear // 3) % 2 == 0 or rear > p.size[2] - 2 else pal["base"]
    if p.y < 2.5:
        return pal["belly"]
    return pal["mark"] if (p.y + wob_x) % 3.5 < 1.1 else pal["base"]


def spotted(p: Px, cell: int, chance: int, seed: int) -> bool:
    ix, iy, iz = (math.floor(c / cell) for c in (p.x, p.y, p.z))
    n = noise(ix, iy, iz, seed)
    if n % 100 >= chance:
        return False
    cx = (ix + 0.25 + ((n >> 8) % 50) / 100) * cell
    cy = (iy + 0.25 + ((n >> 14) % 50) / 100) * cell
    cz = (iz + 0.25 + ((n >> 20) % 50) / 100) * cell
    radius = 1.1 + ((n >> 26) % 2) * 0.5
    return math.dist((p.x, p.y, p.z), (cx, cy, cz)) <= radius


def cheetah(p: Px, pal: dict):
    feat = face_features(p, pal)
    if feat:
        return feat
    if p.part == "head" and p.face == "front":
        eye_col = round(p.cols * 0.2)
        # Dark tear marks run from each eye down to the muzzle.
        if p.col in (eye_col, p.cols - 1 - eye_col) and p.row > round(p.rows * 0.35):
            return pal["mark"]
        return pal["belly"] if p.up < 0.3 else pal["base"]
    if p.part == "muzzle":
        return pal["belly"]
    if p.part.startswith("ear"):
        return pal["mark"] if p.face == "back" else pal["base"]
    if p.part == "tail":
        rear = p.z - p.origin[2]
        if rear > p.size[2] - 2:
            return pal["belly"]
        if rear > p.size[2] * 0.55 and int(rear // 3) % 2 == 0:
            return pal["mark"]
        return pal["base"]
    if p.under or (p.part in ("chest", "rear") and p.up < 0.25):
        return pal["belly"]
    if p.part.endswith("leg") and p.y < 1.5:
        return pal["belly"]
    return pal["mark"] if spotted(p, 3, 80, 7) else pal["base"]


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
