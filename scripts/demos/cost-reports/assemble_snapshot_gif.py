#!/usr/bin/env python3
"""Combine two terminal screenshots without losing pixels between GIF frames."""

import sys
from pathlib import Path

from PIL import Image, ImageChops


def main():
    if len(sys.argv) != 4:
        raise SystemExit("usage: assemble_snapshot_gif.py BEFORE.png AFTER.png OUTPUT.gif")
    sources = []
    for name in sys.argv[1:3]:
        with Image.open(name) as image:
            sources.append(image.convert("RGB"))
    colors = sorted({color for image in sources for color in image.get_flattened_data()})
    if len(colors) > 256 or sources[0].size != sources[1].size:
        raise SystemExit("lossless GIF requires equal dimensions and at most 256 combined colors")
    indices = {color: index for index, color in enumerate(colors)}
    palette = [channel for color in colors for channel in color]
    palette.extend([0] * (768 - len(palette)))
    frames = []
    for image in sources:
        frame = Image.new("P", image.size)
        frame.putpalette(palette)
        frame.putdata([indices[color] for color in image.get_flattened_data()])
        frames.append(frame)
    target = Path(sys.argv[3])
    with target.open("xb") as output:
        frames[0].save(output, format="GIF", save_all=True, append_images=frames[1:],
                       duration=3000, loop=0, disposal=2, optimize=False)
    with Image.open(target) as actual:
        if actual.n_frames != 2:
            raise SystemExit("expected two complete snapshot frames")
        for index, expected in enumerate(sources):
            actual.seek(index)
            if ImageChops.difference(actual.convert("RGB"), expected).getbbox() is not None:
                raise SystemExit(f"composited frame {index} differs from its screenshot")
    print("Verified two complete, pixel-identical snapshot frames.")


if __name__ == "__main__":
    main()
