#!/usr/bin/env python3
"""Build proposed core contributors and owners from PRs, reviews and static blame."""
import argparse
from collections import Counter, defaultdict
from datetime import datetime, timedelta, timezone
import fnmatch
import json
from math import log1p
from pathlib import Path
import re

from ownership_extract import save, skip_reason
from blame_snapshot import validate as validate_blame

ROOT = Path(__file__).resolve().parents[1]
WINDOW_DAYS = HALF_LIFE_DAYS = 90
# Per-area maxima among core contributors normalize each metric to 0..1.
SCORE_WEIGHTS = {"merged_prs_90d": 60, "weighted_churn": 25, "reviews_90d": 10, "blame_lines": 5}
ACTIVITY_RULE = "merged_prs_90d >= 1 or reviews_90d >= 2 (churn-proportional PR equivalents)"


def timestamp(value):
    return datetime.fromisoformat(value.replace("Z", "+00:00")).astimezone(timezone.utc)


def build(raw, rules, taxonomy, blame, generated_at=None, threshold=10, core_override=None):
    validate_blame(blame, taxonomy)
    meta = raw["meta"]
    as_of = timestamp(meta["generated_at"])
    cutoff = as_of - timedelta(days=WINDOW_DAYS)
    area_ids = {area["id"] for area in taxonomy["areas"]}
    if not rules or any(rule["area"] not in area_ids for rule in rules):
        raise ValueError("path rules must reference the taxonomy")
    bots = {login.lower() for login in meta.get("excluded_bot_logins", [])}
    canonical = {}

    def human(login):
        if not isinstance(login, str) or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9-]{0,38}", login) or login.lower() in bots:
            return None
        return canonical.setdefault(login.lower(), login)

    def area_for(path):
        for rule in rules:
            if fnmatch.fnmatchcase(path, rule["glob"]):
                return rule["area"]
        raise ValueError(f"path has no area: {path}")

    def empty():
        return {"merged_prs_90d": 0.0, "weighted_churn": 0.0,
                "reviews_90d": 0.0, "blame_lines": 0, "last_merged_at": None}

    evidence = {area_id: defaultdict(empty) for area_id in area_ids}
    recent_counts = Counter()
    seen = set()
    for pr in raw["prs"]:
        if pr["number"] in seen:
            raise ValueError(f"duplicate PR #{pr['number']}")
        seen.add(pr["number"])
        merged = timestamp(pr["merged_at"])
        if merged > as_of:
            raise ValueError(f"PR #{pr['number']} merged after the evidence capture")
        author = human(pr["author"])
        recent = merged >= cutoff
        if author and recent:
            recent_counts[author] += 1
        churn = Counter()
        for file in pr["files"]:
            if file["churn"] < 0:
                raise ValueError("file churn cannot be negative")
            if skip_reason(file["path"]):
                continue
            churn[area_for(file["path"])] += file["churn"]
        total = sum(churn.values())
        decay = 2 ** (-(as_of - merged).total_seconds() / (86400 * HALF_LIFE_DAYS))
        reviewers = {human(login) for login in pr["reviewers"]} - {None, author}
        for area_id, lines in churn.items():
            # Binary-only / zero-churn PRs split credit equally across touched areas.
            share = lines / total if total else 1 / len(churn)
            if author:
                item = evidence[area_id][author]
                item["merged_prs_90d"] += share * recent
                item["weighted_churn"] += log1p(total) * share * decay
                if item["last_merged_at"] is None or timestamp(item["last_merged_at"]) < merged:
                    item["last_merged_at"] = pr["merged_at"]
            if recent:
                for login in reviewers:
                    evidence[area_id][login]["reviews_90d"] += share
    for area_id, people in blame["areas"].items():
        for login, lines in people.items():
            login = human(login)
            if login:
                evidence[area_id][login]["blame_lines"] += lines
    core_logins = {login for login, count in recent_counts.items() if count >= threshold}
    overrides = core_override or {"include": [], "exclude": []}
    selected = {}
    for key in ("include", "exclude"):
        logins = overrides.get(key)
        if not isinstance(logins, list):
            raise ValueError("core overrides need include and exclude arrays")
        selected[key] = {human(login) for login in logins}
        if None in selected[key] or len(selected[key]) != len(logins):
            raise ValueError("core overrides must contain unique human logins")
    core_logins = (core_logins | selected["include"]) - selected["exclude"]
    shared = {"generated_at": generated_at or datetime.now(timezone.utc).isoformat(),
              "evidence_as_of": meta["generated_at"], "window_start": cutoff.isoformat(),
              "repo": taxonomy["repo"]}
    core = {**shared, "status": "proposed", "note": "Proposed from public history with core overrides.",
            "selection": "merged PR threshold with overrides",
            "threshold_merged_prs_90d": threshold, "contributors": []}
    for login in sorted(core_logins, key=lambda name: (-recent_counts[name], name.lower())):
        top = sorted((area_id for area_id in area_ids if area_id not in {"shared-infra", "needs-area"} and evidence[area_id][login]["merged_prs_90d"] > 0),
                     key=lambda area_id: (-evidence[area_id][login]["merged_prs_90d"], area_id))[:3]
        core["contributors"].append({"login": login, "merged_prs_90d": recent_counts[login], "top_areas": top})

    owners = {**shared, "status": "proposed", "half_life_days": HALF_LIFE_DAYS,
              "blame_generated_at": blame["generated_at"], "blame_origin_main_sha": blame.get("origin_main_sha"),
              "score_weights": SCORE_WEIGHTS, "normalization": "per-area metric maximum among proposed core contributors",
              "activity_rule": ACTIVITY_RULE,
              "notes": ["PR and review credit is proportional to area churn; zero-churn PRs split equally across touched areas.",
                        "PR files exclude lockfiles, generated, vendored, build and snapshot paths.",
                        "Weighted churn is log1p(total eligible PR churn), split by area share, with merge-age decay.",
                        "Fallback shared-infra and needs-area buckets never suggest owners.",
                        "Reviews count unique non-author reviewers on PRs merged in the window; review timestamps are unavailable.",
                        "Top core areas use merged PR equivalents in the 90-day window.",
                        "Blame uses the committed area line-count snapshot, not live Git history."], "areas": {}}
    for area in taxonomy["areas"]:
        if area["id"] in {"shared-infra", "needs-area"}:
            owners["areas"][area["id"]] = {"label": area["label"], "owners": [], "unowned": True}
            continue
        people = evidence[area["id"]]
        maxima = {metric: max((people[login][metric] for login in core_logins), default=0) for metric in SCORE_WEIGHTS}
        ranked = []
        for login in sorted(core_logins):
            item = people[login]
            if item["merged_prs_90d"] < 1 and item["reviews_90d"] < 2:
                continue
            score = sum(weight * item[metric] / maxima[metric] for metric, weight in SCORE_WEIGHTS.items() if maxima[metric])
            ranked.append({"login": login, "score": round(score, 6),
                           **{key: round(value, 6) if isinstance(value, float) else value for key, value in item.items()}})
        ranked.sort(key=lambda item: (-item["score"], -item["merged_prs_90d"], item["login"].lower()))
        owners["areas"][area["id"]] = {"label": area["label"], "owners": ranked[:3], "unowned": not ranked}
    return core, owners


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--raw-file", type=Path, default=ROOT / "research/ownership-raw.json")
    parser.add_argument("--threshold", type=int, default=10)
    parser.add_argument("--overrides-file", type=Path, default=ROOT / "data/core-overrides.json")
    parser.add_argument("--blame-file", type=Path, default=ROOT / "data/blame-lines.json")
    parser.add_argument("--output-dir", type=Path, default=ROOT / "research/owners")
    args = parser.parse_args()
    if args.threshold < 1:
        parser.error("--threshold must be positive")
    core, owners = build(json.loads(args.raw_file.read_text()), json.loads((ROOT / "data/path-areas.json").read_text()),
                         json.loads((ROOT / "data/issue-areas.json").read_text()),
                         json.loads(args.blame_file.read_text()), threshold=args.threshold,
                         core_override=json.loads(args.overrides_file.read_text()))
    args.output_dir.mkdir(parents=True, exist_ok=True)
    for name, data in [("core-contributors", core), ("area-owners", owners)]:
        save(args.output_dir / f"{name}.json", data)
    print(f"Core: {len(core['contributors'])} humans; {core['selection']}; threshold: {core['threshold_merged_prs_90d']}")
    print(f"Areas: {len(owners['areas'])}; unowned: {sum(area['unowned'] for area in owners['areas'].values())}")


if __name__ == "__main__":
    main()
