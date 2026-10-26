#!/usr/bin/env python3
"""Aggregate saved per-file blame evidence into the committed area snapshot."""
import argparse
from collections import Counter
from datetime import datetime
import fnmatch
import json
from pathlib import Path
import re

from format_maps import compact_json
from ownership_extract import is_bot, skip_reason

ROOT = Path(__file__).resolve().parents[1]
LOGIN = re.compile(r"[A-Za-z0-9][A-Za-z0-9-]{0,38}")


def validate(snapshot, taxonomy):
    if not isinstance(snapshot, dict) or snapshot.get("repo") != taxonomy["repo"]:
        raise ValueError("blame snapshot must name the taxonomy repository")
    try:
        captured = datetime.fromisoformat(snapshot["generated_at"].replace("Z", "+00:00"))
        if captured.utcoffset() is None:
            raise ValueError("missing timezone")
    except (KeyError, AttributeError, TypeError, ValueError) as exc:
        raise ValueError("blame snapshot needs a valid generated_at with timezone") from exc
    sha = snapshot.get("origin_main_sha")
    if sha is not None and (not isinstance(sha, str) or not re.fullmatch(r"[0-9a-f]{40}|[0-9a-f]{64}", sha)):
        raise ValueError("blame snapshot has an invalid source commit")
    areas = snapshot.get("areas")
    if not isinstance(areas, dict) or set(areas) != {a["id"] for a in taxonomy["areas"]}:
        raise ValueError("blame snapshot areas must match the taxonomy")
    for area, people in areas.items():
        if not isinstance(people, dict):
            raise ValueError(f"blame snapshot area {area} must contain login counts")
        seen = set()
        for login, lines in people.items():
            if not LOGIN.fullmatch(login) or is_bot(login) or login.lower() in seen or type(lines) is not int or lines < 0:
                raise ValueError(f"blame snapshot has an invalid login/count in {area}")
            seen.add(login.lower())
    return snapshot


def aggregate(raw, rules, taxonomy):
    # The saved extraction already excluded binary, generated and large fixture contents.
    # Reuse the former builder's per-file blame aggregation and path/bot exclusions.
    if not isinstance(raw.get("blame"), dict):
        raise ValueError("raw evidence needs per-file blame counts")
    areas = {area["id"]: Counter() for area in taxonomy["areas"]}
    bots = {login.lower() for login in raw["meta"].get("excluded_bot_logins", [])}
    canonical = {}
    for path, people in raw["blame"].items():
        if skip_reason(path):
            continue
        area = next((rule["area"] for rule in rules if fnmatch.fnmatchcase(path, rule["glob"])), None)
        if area not in areas:
            raise ValueError(f"blame path has no taxonomy area: {path}")
        for login, lines in people.items():
            if not LOGIN.fullmatch(login) or is_bot(login, bots):
                continue
            if type(lines) is not int or lines < 0:
                raise ValueError(f"invalid blame count for {path}: {login}")
            areas[area][canonical.setdefault(login.lower(), login)] += lines
    return validate({"repo": taxonomy["repo"], "generated_at": raw["meta"]["generated_at"],
                     "origin_main_sha": raw["meta"].get("origin_main_sha"),
                     "areas": {area: dict(sorted(people.items())) for area, people in areas.items()}}, taxonomy)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("raw_file", type=Path, help="saved ownership-raw.json containing per-file blame")
    parser.add_argument("--output", type=Path, default=ROOT / "data/blame-lines.json")
    args = parser.parse_args()
    snapshot = aggregate(json.loads(args.raw_file.read_text()), json.loads((ROOT / "data/path-areas.json").read_text()),
                         json.loads((ROOT / "data/issue-areas.json").read_text()))
    args.output.write_text(compact_json(snapshot))


if __name__ == "__main__":
    main()
