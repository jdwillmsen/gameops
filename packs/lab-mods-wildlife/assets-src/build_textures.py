import struct
import zlib

from species import Species

SIZE = 64

# Stub art: a flat colour per variant until the painted textures replace it.
COLOURS = {
    ("tiger", "orange"): (214, 120, 40),
    ("tiger", "white"): (234, 232, 224),
}


def texture_names(s: Species) -> list[tuple[str, str]]:
    """(variant, texture path relative to the resource pack, without extension)."""
    if not s.variants:
        return [("default", f"textures/entity/{s.id}")]
    return [(v, f"textures/entity/{s.id}_{v}") for v in s.variants]


def chunk(tag: bytes, body: bytes) -> bytes:
    return struct.pack(">I", len(body)) + tag + body + struct.pack(">I", zlib.crc32(tag + body) & 0xFFFFFFFF)


def paint(s: Species, variant: str) -> bytes:
    r, g, b = COLOURS[(s.id, variant)]
    row = b"\x00" + bytes((r, g, b, 255)) * SIZE
    ihdr = struct.pack(">IIBBBBB", SIZE, SIZE, 8, 6, 0, 0, 0)
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", ihdr)
            + chunk(b"IDAT", zlib.compress(row * SIZE, 9)) + chunk(b"IEND", b""))
