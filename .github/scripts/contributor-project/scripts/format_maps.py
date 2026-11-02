#!/usr/bin/env python3
"""Keep each area, path rule and issue route on one JSON line."""
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def compact_json(value):
    def entries(container, indent):
        if isinstance(container, list):
            rows = [json.dumps(item, ensure_ascii=False) for item in container]
            left, right = "[", "]"
        else:
            rows = [json.dumps(key) + ": " + json.dumps(item, ensure_ascii=False) for key, item in container.items()]
            left, right = "{", "}"
        return left + ("\n" + ",\n".join(indent + row for row in rows) + "\n" + indent[:-2] if rows else "") + right

    if isinstance(value, list):
        return entries(value, "  ") + "\n"
    rows = []
    for key, item in value.items():
        encoded = entries(item, "    ") if isinstance(item, (list, dict)) else json.dumps(item, ensure_ascii=False)
        rows.append("  " + json.dumps(key) + ": " + encoded)
    return "{\n" + ",\n".join(rows) + "\n}\n"


if __name__ == "__main__":
    for name in ("path-areas", "issue-areas"):
        path = ROOT / f"data/{name}.json"
        with path.open() as source:
            before = json.load(source)
        formatted = compact_json(before)
        assert json.loads(formatted) == before
        path.write_text(formatted)
