#!/usr/bin/env python3
"""Make Persian Markdown readable: right-to-left text, left-to-right code.

Wraps the document in <div dir="rtl"> and every fenced code block in
<div dir="ltr">, with the blank lines CommonMark needs around HTML blocks.
Works on GitHub and in the VS Code preview. Idempotent.

Usage: tools/dev/rtl.py docs/fa/*.md
"""
import re
import sys

OPEN, CLOSE = '<div dir="rtl">', "</div>"
LTR = '<div dir="ltr">'
FENCE = re.compile(r"^(```|~~~)")


def convert(text: str) -> str:
    lines = text.split("\n")
    if lines and lines[0].strip() == OPEN:  # already converted
        return text
    out = [OPEN, ""]
    in_code = False
    for line in lines:
        if FENCE.match(line):  # only unindented fences
            if not in_code:
                out += ["", LTR, "", line]
            else:
                out += [line, "", CLOSE, ""]
            in_code = not in_code
            continue
        out.append(line)
    while out and out[-1] == "":
        out.pop()
    out += ["", CLOSE, ""]
    # collapse runs of blank lines created around the wrappers
    return re.sub(r"\n{3,}", "\n\n", "\n".join(out))


for path in sys.argv[1:]:
    with open(path, encoding="utf-8") as f:
        src = f.read()
    dst = convert(src)
    if dst != src:
        with open(path, "w", encoding="utf-8") as f:
            f.write(dst)
        print(f"rtl: {path}")
    else:
        print(f"rtl: {path} (already converted)")
