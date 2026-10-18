#!/usr/bin/env python3
"""Print the rows of every HTML table on stdin, one row per line.

The Compute Engine disk compatibility tables are the source of truth for
internal/catalog/machine-families.yaml, and they are easy to misread from a
prose summary - a blank cell and an em dash mean opposite things. This reads
the markup directly.

    curl -sL https://cloud.google.com/compute/docs/disks/hyperdisks \\
      | python3 hack/extract-tables.py

A cell rendered as BLANK is an empty cell (supported, in these tables); a cell
rendered as an em dash is an explicit "not supported".
"""

import html
import re
import sys

CELL = re.compile(r"<t[hd][^>]*>(.*?)</t[hd]>", re.S | re.I)
ROW = re.compile(r"<tr[^>]*>(.*?)</tr>", re.S | re.I)
TABLE = re.compile(r"<table[^>]*>(.*?)</table>", re.S | re.I)
TAG = re.compile(r"<[^>]+>")


def text(cell: str) -> str:
    s = TAG.sub(" ", cell)
    s = html.unescape(s)
    s = " ".join(s.split())
    return s if s else "BLANK"


def main() -> int:
    doc = sys.stdin.read()
    tables = TABLE.findall(doc)
    if not tables:
        print("no tables found", file=sys.stderr)
        return 1
    for i, table in enumerate(tables):
        print(f"--- table {i} ---")
        for row in ROW.findall(table):
            cells = [text(c) for c in CELL.findall(row)]
            if cells:
                print(cells)
        print()
    return 0


if __name__ == "__main__":
    sys.exit(main())
