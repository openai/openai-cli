"""Deterministic synthetic PNGs; Python standard library only, no API or fonts."""
import hashlib
import json
from pathlib import Path
import random
import struct
import zlib

ROOT = Path(__file__).resolve().parent / "fixtures"
ROOT.mkdir(exist_ok=True)


def png(width, height, pixels):
    def chunk(kind, data):
        return (struct.pack("!I", len(data)) + kind + data
                + struct.pack("!I", zlib.crc32(kind + data)))
    scan = b"".join(b"\0" + pixels[y * width * 4:(y + 1) * width * 4] for y in range(height))
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack("!IIBBBBB", width, height, 8, 6, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(scan)) + chunk(b"IEND", b""))


def detail():
    width, height = 720, 280
    pixels = bytearray()
    colors = ((240, 40, 55), (35, 215, 95), (35, 95, 245), (245, 245, 245))
    for y in range(height):
        for x in range(width):
            rgb = (15 + x * 65 // width, 25 + y * 70 // height, 85 + x * 105 // width)
            if x % 40 == 0 or y % 40 == 0:
                rgb = tuple(min(255, c + 28) for c in rgb)
            if 30 <= x < 350 and 25 <= y < 65:
                rgb = colors[(x - 30) // 80]
            if 430 <= x < 690 and 25 <= y < 115:
                rgb = (250, 80 + (x - 430) * 100 // 260, 45 + (y - 25) * 120 // 90)
            if 40 <= x < 680 and abs(y - (155 + (x - 360) ** 2 // 1800)) <= 2:
                rgb = (45, 245, 145)
            if 40 <= x < 360 and 225 <= y < 255:
                rgb = (230, 230, 230) if ((x - 40) // 4) % 2 == 0 else (20, 20, 20)
            pixels.extend((*rgb, 255))
    return png(width, height, pixels)


if __name__ == "__main__":
    fixtures = {"synthetic-large.png": png(1024, 1024, random.Random(7925).randbytes(1024 * 1024 * 4)),
                "synthetic-detail.png": detail()}
    result = {}
    for name, data in fixtures.items():
        path = ROOT / name
        if path.exists() and path.read_bytes() != data:
            raise RuntimeError("Refusing to overwrite a different fixture: " + name)
        path.write_bytes(data)
        result[name] = {"sha256": hashlib.sha256(data).hexdigest(), "bytes": len(data)}
    (ROOT / "fixtures.json").write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result, indent=2))
