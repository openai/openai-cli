#!/usr/bin/env python3
"""Check resident image output after executable replacement, offline.

    go test -c -o /tmp/openai-image.test ./cmd/openai
    python3 -I scripts/check-image-output-compatibility.py /tmp/openai-image.test OUTPUT_DIR

Uses the existing PTY runner; neither a graphical terminal nor image libraries
are required. This verifies protocol pixels, not native visual presentation.
"""
import base64
import binascii
import json
import os
import pathlib
import re
import struct
import sys
import zlib

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
from terminal_test import run_terminal_test

TEST = 'TestMainImageOutputSurvivesExecutableReplacementTerminal'
PIXELS = bytes([230, 0, 0, 255, 0, 0, 200, 127])  # imageGenerationPNG fixture


def check(condition, message):
    if not condition:
        raise RuntimeError(message)


def check_fixture_png(png):
    """Decode only this test's 2x1 RGBA8 PNG, including any PNG row filter."""
    check(png.startswith(b'\x89PNG\r\n\x1a\n'), 'image payload is not PNG')
    offset, compressed, header, ended = 8, bytearray(), False, False
    while offset < len(png):
        check(offset + 12 <= len(png), 'truncated PNG chunk')
        length = struct.unpack('>I', png[offset:offset + 4])[0]
        kind = png[offset + 4:offset + 8]
        end = offset + 8 + length
        check(end + 4 <= len(png), 'truncated PNG payload')
        data = png[offset + 8:end]
        crc = struct.unpack('>I', png[end:end + 4])[0]
        check(binascii.crc32(kind + data) & 0xffffffff == crc, 'PNG checksum mismatch')
        if kind == b'IHDR':
            check(not header and offset == 8, 'unexpected PNG header')
            check(data == struct.pack('>IIBBBBB', 2, 1, 8, 6, 0, 0, 0), 'fixture PNG shape changed')
            header = True
        elif kind == b'IDAT':
            compressed.extend(data)
        elif kind == b'IEND':
            check(length == 0 and end + 4 == len(png), 'invalid PNG end')
            ended = True
        offset = end + 4
    check(header and ended, 'incomplete PNG')
    decoder = zlib.decompressobj()
    row = decoder.decompress(compressed, 10)
    check(len(row) == 9 and decoder.eof and not decoder.unused_data, 'invalid PNG row data')
    filter_type, pixels = row[0], bytearray(row[1:])
    check(filter_type <= 4, 'unknown PNG filter')
    for index in range(len(pixels)):
        left = pixels[index - 4] if index >= 4 else 0
        # On the first and only row, Up is zero and Paeth selects Left.
        prediction = left if filter_type in (1, 4) else left // 2 if filter_type == 3 else 0
        pixels[index] = (pixels[index] + prediction) & 255
    check(pixels == PIXELS, 'rendered fixture pixels changed')


def validate(code, captured):
    check(code == 0 and b'--- SKIP' not in captured, 'replacement tests failed or skipped')
    expected = {TEST + '/' + phase + '/replace-' + replace: 3 if phase == 'progress' else 1
                for phase in ('api', 'stdin', 'progress') for replace in ('false', 'true')}
    sections = re.findall(rb'COMPAT-BEGIN ([^\r\n]+)\r?\n(.*?)COMPAT-END \1\r?\n', captured, re.S)
    check(len(sections) == 6, 'expected six completed replacement cases')
    results = {}
    total = 0
    for raw_name, section in sections:
        name = raw_name.decode('ascii')
        check(name in expected and name not in results, 'unexpected or duplicate replacement case')
        frames = re.findall(rb'\x1b_G([^;]*);(.*?)\x1b\\', section, re.S)
        check(len(frames) == expected[name], 'wrong native image count: ' + name)
        for header, payload in frames:
            fields = dict(field.split(b'=', 1) for field in header.split(b','))
            check(all(fields.get(k) == v for k, v in {b'a': b'T', b'f': b'100', b'm': b'0', b'q': b'2'}.items()),
                  'unexpected graphics frame: ' + name)
            check_fixture_png(base64.b64decode(payload, validate=True))
        total += len(frames)
        results[name] = {'images': len(frames), 'exact_rgba': True, 'passed': True}
    check(captured.count(b'\x1b_G') == total, 'incomplete or unexpected graphics output')
    return results


def main():
    if len(sys.argv) != 3:
        raise SystemExit('usage: check-image-output-compatibility.py MAIN_TEST OUTPUT_DIR')
    binary = str(pathlib.Path(sys.argv[1]).resolve())
    output = pathlib.Path(sys.argv[2]).resolve()
    output.mkdir(parents=True, exist_ok=True)
    env = {k: v for k, v in os.environ.items() if not k.startswith('OPENAI_')}
    code, captured = run_terminal_test(binary, TEST, env, timeout=100)
    (output / 'image-output-compatibility.raw').write_bytes(captured)
    results = validate(code, captured)
    (output / 'image-output-compatibility.json').write_text(json.dumps(results, indent=2) + '\n')
    print(json.dumps(results, indent=2))


if __name__ == '__main__':
    main()
