#!/usr/bin/env python3
"""Render a real `xserial demo --snapshot` view as the README's SVG preview."""

import argparse
import html
from pathlib import Path
import re
import subprocess
import tempfile


def render(snapshot: str) -> str:
    lines = snapshot.rstrip("\n").splitlines()
    width, line_height = 1040, 23
    height = 64 + line_height * len(lines)
    output = [
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}">',
        '<title>xserial binary workbench demo</title>',
        '<desc>Actual workbench output from a simulated Modbus register query.</desc>',
        f'<rect width="{width}" height="{height}" rx="12" fill="#080808"/>',
        '<text x="20" y="29" font-family="monospace" font-size="14" fill="#aaa">xserial demo --snapshot</text>',
        '<g font-family="DejaVu Sans Mono, monospace" font-size="16" xml:space="preserve">',
    ]
    escape = re.compile(r"\x1b\[([0-9;]*)m")
    for row, line in enumerate(lines):
        spans, color, position = [], "#dddddd", 0
        for match in escape.finditer(line):
            if match.start() > position:
                spans.append(f'<tspan fill="{color}">{html.escape(line[position:match.start()])}</tspan>')
            parts = match.group(1).split(";")
            if len(parts) == 5 and parts[:2] == ["38", "2"]:
                color = "#" + "".join(f"{int(value):02x}" for value in parts[2:])
            elif match.group(1) in ("", "0", "39"):
                color = "#dddddd"
            position = match.end()
        spans.append(f'<tspan fill="{color}">{html.escape(line[position:])}</tspan>')
        output.append(f'<text x="20" y="{60 + row * line_height}">' + "".join(spans) + "</text>")
    output.extend(["</g>", "</svg>"])
    return "\n".join(output) + "\n"


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="bin/xserial")
    parser.add_argument("--output", default="assets/workbench-demo.svg")
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve())
    with tempfile.TemporaryDirectory(prefix="xserial-preview-") as temporary:
        snapshot = subprocess.run(
            [binary, "demo", "--snapshot", "--commands", str(Path(temporary) / "commands.json")],
            check=True, text=True, capture_output=True,
        ).stdout
    Path(args.output).write_text(render(snapshot), encoding="utf-8")


if __name__ == "__main__":
    main()
