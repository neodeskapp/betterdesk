#!/usr/bin/env python3
"""Builds the console's icon font from the full Material Symbols font.

The console draws Material Symbols Outlined at optical size 20, weight 400,
grade 0, fill 0 (src/lib/styles/base.css, .material-icons), the same
instance the desktop client ships (neodesk-client flutter/tool/
build_symbols.py, same source commit). It types glyph names, so the
subset keeps the ligatures from the names' letters to their icons.

Run by scripts/update-material-symbols.ts, which collects the names the
console draws; by hand, from web-ui/ (needs fonttools and brotli, tested
with fonttools 4.66.0):

  python3 scripts/build-material-symbols.py --names <file with one name per line>

It downloads the variable font and its codepoints from the pinned commit of
github.com/google/material-design-icons and checks their SHA-256 (--src DIR
reads them from DIR instead, names as in the repository's variablefont/),
then writes static/fonts/MaterialSymbolsOutlined.woff2,
static/fonts/MATERIAL_SYMBOLS_VERSION.txt and the ?v= cache busters in
src/lib/styles/base.css and src/app.html. The output is the same on every
run for the same names.
"""

import argparse
import hashlib
import os
import re
import sys
import tempfile
import urllib.request

from fontTools import subset
from fontTools.ttLib import TTFont
from fontTools.varLib import instancer

COMMIT = "10ecc14790612ca8fd164e3f4ffe9c16fc50d5b7"
FONT_SHA256 = "f7837ba8573d9a7a74931618ced8307bc81f96d49af296bf6c8052c772ed5282"
CODEPOINTS_SHA256 = "a7404d9e5a1b63f496cf05ace45d386075886b5a7f123fba9f29527cb6006fde"
BASE = "https://raw.githubusercontent.com/google/material-design-icons/" + COMMIT + "/variablefont/"
FONT = "MaterialSymbolsOutlined[FILL,GRAD,opsz,wght].ttf"
CODEPOINTS = "MaterialSymbolsOutlined[FILL,GRAD,opsz,wght].codepoints"
SHA256 = {FONT: FONT_SHA256, CODEPOINTS: CODEPOINTS_SHA256}
AXES = {"FILL": 0, "GRAD": 0, "opsz": 20, "wght": 400}

UI = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
OUT_FONT = os.path.join(UI, "static", "fonts", "MaterialSymbolsOutlined.woff2")
OUT_VERSION = os.path.join(UI, "static", "fonts", "MATERIAL_SYMBOLS_VERSION.txt")
CACHE_BUSTED = [
    os.path.join(UI, "src", "lib", "styles", "base.css"),
    os.path.join(UI, "src", "app.html"),
]


def fetch(src, tmp):
    """Local paths of the font and the codepoints, checked against the pins."""
    paths = {}
    for name in (FONT, CODEPOINTS):
        if src:
            path = os.path.join(src, name)
        else:
            path = os.path.join(tmp, name)
            url = BASE + urllib.request.quote(name)
            print("download", url)
            urllib.request.urlretrieve(url, path)
        with open(path, "rb") as f:
            got = hashlib.sha256(f.read()).hexdigest()
        if got != SHA256[name]:
            sys.exit(f"{path}: SHA-256 {got}, expected {SHA256[name]}")
        paths[name] = path
    return paths[FONT], paths[CODEPOINTS]


def read_names(path, codepoints):
    names = []
    with open(path, encoding="utf-8") as f:
        for line in f:
            name = line.strip()
            if not name:
                continue
            if name not in codepoints:
                sys.exit(f"{name}: no Material Symbol of that name")
            names.append(name)
    return sorted(set(names))


def build(font_path, names, codepoints):
    font = TTFont(font_path, recalcTimestamp=False)
    font = instancer.instantiateVariableFont(font, AXES)
    # The icons' own code points, and the letters their names are typed with
    unicodes = {codepoints[n] for n in names} | {ord(c) for n in names for c in n}
    options = subset.Options()
    options.layout_features = ["liga", "rlig"]
    # Only the listed names: without this, every icon whose name is made of
    # the kept letters would come along through the ligatures
    options.layout_closure = False
    options.name_IDs = ["*"]
    options.flavor = "woff2"
    sub = subset.Subsetter(options)
    sub.populate(unicodes=sorted(unicodes))
    sub.subset(font)
    font.recalcTimestamp = False
    font.flavor = "woff2"
    font.save(OUT_FONT)
    print(f"wrote {os.path.relpath(OUT_FONT, UI)} ({os.path.getsize(OUT_FONT)} bytes)")


def write_version(font_path, names):
    version = TTFont(font_path, lazy=True)["name"].getDebugName(5)
    with open(OUT_FONT, "rb") as f:
        data = f.read()
    digest = hashlib.sha256(data).hexdigest()
    lines = [
        "NeoDesk self-hosted Material Symbols (Apache-2.0)",
        "Upstream: https://github.com/google/material-design-icons/tree/" + COMMIT + "/variablefont",
        f"Upstream commit: {COMMIT}",
        f"Source: {FONT}",
        f"Source SHA-256: {FONT_SHA256}",
        f"Font version: {version}",
        "Axes: " + ", ".join(f"{k}={v}" for k, v in sorted(AXES.items())),
        "License: Apache License 2.0",
        "Built by: web-ui/scripts/build-material-symbols.py (same source and axes as the desktop client)",
        f"Icon count: {len(names)}",
        "",
        "File: MaterialSymbolsOutlined.woff2",
        f"  size: {len(data)}",
        f"  sha256: {digest}",
        "",
        "Icons (alphabetical):",
        ",".join(names),
        "",
    ]
    with open(OUT_VERSION, "w", encoding="utf-8", newline="\n") as f:
        f.write("\n".join(lines))
    print(f"wrote {os.path.relpath(OUT_VERSION, UI)}")
    return digest


def bust_caches(digest):
    for path in CACHE_BUSTED:
        with open(path, encoding="utf-8") as f:
            text = f.read()
        text = re.sub(
            r"MaterialSymbolsOutlined\.woff2(?:\?v=[^'\")]+)?",
            f"MaterialSymbolsOutlined.woff2?v={digest[:12]}",
            text,
        )
        with open(path, "w", encoding="utf-8", newline="\n") as f:
            f.write(text)


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    ap.add_argument("--names", required=True, help="file with one icon name per line")
    ap.add_argument("--src", help="folder with the variable font and codepoints")
    args = ap.parse_args()
    with tempfile.TemporaryDirectory(prefix="nd-console-symbols-") as tmp:
        font_path, cp_path = fetch(args.src, tmp)
        codepoints = {}
        with open(cp_path, encoding="utf-8") as f:
            for line in f:
                name, cp = line.split()
                codepoints[name] = int(cp, 16)
        names = read_names(args.names, codepoints)
        build(font_path, names, codepoints)
        bust_caches(write_version(font_path, names))


if __name__ == "__main__":
    main()
