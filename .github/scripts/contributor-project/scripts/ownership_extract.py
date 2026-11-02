#!/usr/bin/env python3
"""Refresh merged-PR evidence, reusing history outside a seven-day overlap."""
import argparse
from datetime import datetime, timedelta, timezone
import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import time

ROOT = Path(__file__).resolve().parents[1]
HISTORY_DAYS = 540  # Six 90-day half-lives; older churn has less than 1/64 weight.
REFRESH_DAYS = 7
FULL_REFRESH_DAYS = 30
REPO = "OrchestratorInc/agent-orchestrator"
PAGE = "pageInfo { hasNextPage endCursor }"
FILES = "files(first:100,after:$cursor) { nodes { path additions deletions } " + PAGE + " }"
REVIEWS = "reviews(first:100,after:$cursor,states:[APPROVED,COMMENTED]) { nodes { author { login __typename } } " + PAGE + " }"
QUERY = '''query($search:String!,$cursor:String) {
  search(query:$search,type:ISSUE,first:50,after:$cursor) {
    issueCount nodes { ... on PullRequest { number mergedAt changedFiles author { login __typename }
      FILES REVIEWS
    } } PAGE
  }
}'''.replace("FILES", FILES.replace(",after:$cursor", "")).replace("REVIEWS", REVIEWS.replace(",after:$cursor", "")).replace("PAGE", PAGE)


class IncompleteEvidence(RuntimeError):
    """Discard inconsistent cached pagination instead of retrying it forever."""


def is_bot(value, bot_logins=()):
    value = (value or "").lower()
    return bool(value in bot_logins or "[bot]" in value or value in {"noreply@github.com", "actions@github.com"} or
                re.search(r"(^|[^a-z0-9])(dependabot|github-actions|renovate)([^a-z0-9]|$)", value) or
                re.search(r"(^|[+._-])bot([+._@-]|$)", value))


def skip_reason(path):
    parts = PurePosixPath(path.lower()).parts
    name = parts[-1]
    if set(parts[:-1]) & {"node_modules", "dist", "build", "vendor", "vendored", "generated", "gen"}:
        return "generated_vendor_build"
    if name in {"package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "go.sum", "bun.lockb"} or name.endswith(".lock"):
        return "lockfile"
    if name.endswith((".snap", ".snapshot")) or set(parts[:-1]) & {"snapshots", "__snapshots__"}:
        return "snapshot"
    if name.endswith((".min.js", ".min.css", ".map", ".pb.go")) or re.search(r"[._](?:gen|generated)\.", name) or path in {
        "backend/internal/httpd/apispec/openapi.yaml", "frontend/src/api/schema.ts", "packages/cloud-client/src/schema.ts",
    }:
        return "generated_minified"
    return None


def save(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(".tmp")
    temporary.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")
    temporary.replace(path)


def api(cache, endpoint, variables):
    key = hashlib.sha256(json.dumps([endpoint, variables], sort_keys=True).encode()).hexdigest()
    path = cache / ("graphql-" + key + ".json")
    if path.exists():
        return json.loads(path.read_text())
    command = ["gh", "api", "graphql", "-f", "query=" + endpoint]
    for key, value in variables.items():
        if value is not None:
            command += ["-F", f"{key}={value}"]
    for attempt in range(3):
        try:
            response = subprocess.run(command, capture_output=True, text=True, check=True, timeout=60)
            break
        except (subprocess.TimeoutExpired, subprocess.CalledProcessError) as error:
            if attempt == 2:
                if error.stderr:
                    print(error.stderr.strip(), flush=True)
                raise
            print("GitHub request failed; retrying: " + str(error.stderr or type(error).__name__)[:200], flush=True)
    data = json.loads(response.stdout)
    if data.get("errors"):
        raise RuntimeError(json.dumps(data["errors"]))
    save(path, data)
    return data


def pull_requests(cache, start, end):
    def window(first, last):
        query = ("repo:OrchestratorInc/agent-orchestrator is:pr is:merged "
                 f"merged:{first.isoformat()}..{last.isoformat()} sort:created-asc")
        nodes, cursor = [], None
        while True:
            connection = api(cache, QUERY, {"search": query, "cursor": cursor})["data"]["search"]
            total = connection["issueCount"]
            # Search exposes at most 1,000 results. Split before paginating it.
            if total > 1000:
                if first >= last:
                    raise RuntimeError("More than 1,000 PRs merged in one second")
                middle = first + timedelta(seconds=int((last - first).total_seconds()) // 2)
                return window(first, middle) + window(middle + timedelta(seconds=1), last)
            nodes.extend(connection["nodes"])
            if not connection["pageInfo"]["hasNextPage"]:
                break
            following = connection["pageInfo"]["endCursor"]
            if not following or following == cursor:
                raise IncompleteEvidence("PR pagination did not advance")
            cursor = following
        if len(nodes) != total:
            raise IncompleteEvidence("PR pagination is incomplete; refresh the GraphQL cache")
        print(f"PRs: {len(nodes)} in {first.date()}..{last.date()}", flush=True)
        return nodes

    nodes = window(start, end)
    if len({n["number"] for n in nodes}) != len(nodes):
        raise IncompleteEvidence("PR pagination is inconsistent; refresh the GraphQL cache")
    prs = []
    for node in nodes:
        for field, selection in [("files", FILES), ("reviews", REVIEWS)]:
            connection = node[field]
            seen_cursors = set()
            while connection["pageInfo"]["hasNextPage"]:
                cursor = connection["pageInfo"]["endCursor"]
                if not cursor or cursor in seen_cursors:
                    raise IncompleteEvidence(f"PR #{node['number']} {field} pagination did not advance")
                seen_cursors.add(cursor)
                query = 'query($number:Int!,$cursor:String) { repository(owner:"OrchestratorInc",name:"agent-orchestrator") { pullRequest(number:$number) { ' + selection + ' } } }'
                connection = api(cache, query, {"number": node["number"], "cursor": connection["pageInfo"]["endCursor"]})["data"]["repository"]["pullRequest"][field]
                node[field]["nodes"].extend(connection["nodes"])
        if len(node["files"]["nodes"]) != node["changedFiles"] or len({f["path"] for f in node["files"]["nodes"]}) != node["changedFiles"]:
            raise IncompleteEvidence(f"Incomplete or duplicate file list for PR #{node['number']}")
        author = (node["author"] or {}).get("login")
        if not is_bot(author):
            reviewers = {(r["author"] or {}).get("login") for r in node["reviews"]["nodes"]}
            prs.append({"number": node["number"], "merged_at": node["mergedAt"], "author": author,
                        "reviewers": sorted(r for r in reviewers if r and not is_bot(r)),
                        "files": [{"path": f["path"], "churn": f["additions"] + f["deletions"]} for f in node["files"]["nodes"]]})
    bots = {actor["login"].lower() for node in nodes
            for actor in [node["author"]] + [r["author"] for r in node["reviews"]["nodes"]]
            if actor and actor["__typename"] == "Bot"}
    prs = [{**p, "reviewers": [r for r in p["reviewers"] if not is_bot(r, bots)]}
           for p in prs if not is_bot(p["author"], bots)]
    return prs, len(nodes), len(nodes) - len(prs), bots


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--full-refresh", action="store_true", help="rebuild all evidence instead of reusing history")
    args = parser.parse_args()
    started = time.monotonic()
    cache = ROOT / "research/ownership-cache"
    as_of = datetime.now(timezone.utc).replace(microsecond=0)
    progress = ROOT / "research/ownership-progress.json"
    if progress.exists():
        try:
            saved = json.loads(progress.read_text())
            pinned = datetime.fromisoformat(saved["as_of"])
            if saved.get("repo") == REPO and as_of - timedelta(days=30) <= pinned <= as_of:
                as_of = pinned
        except (OSError, ValueError, KeyError, TypeError):
            pass
    start = as_of - timedelta(days=HISTORY_DAYS)
    output = ROOT / "research/ownership-raw.json"
    previous = None
    if output.exists() and not args.full_refresh:
        try:
            candidate = json.loads(output.read_text())
            old_meta = candidate["meta"]
            captured = datetime.fromisoformat(old_meta["generated_at"])
            old_start = datetime.fromisoformat(old_meta["history_start"])
            full_refresh = datetime.fromisoformat(old_meta["full_refresh_at"])
            if (old_meta.get("repo") == REPO and old_meta.get("evidence_version") == 1
                    and old_start <= start and full_refresh <= captured <= as_of
                    and as_of - full_refresh < timedelta(days=FULL_REFRESH_DAYS)):
                if not isinstance(candidate["prs"], list):
                    raise ValueError("cached PRs must be an array")
                numbers = [p["number"] for p in candidate["prs"]]
                if len(set(numbers)) != len(numbers):
                    raise ValueError("duplicate cached PRs")
                previous = candidate
        except (OSError, ValueError, KeyError, TypeError) as exc:
            print(f"::warning::Owner evidence cache is invalid; rebuilding history: {exc}", flush=True)
    refresh_start = max(start, datetime.fromisoformat(previous["meta"]["generated_at"]) - timedelta(days=REFRESH_DAYS)) if previous else start
    # Pin request windows across runner timeouts, so successful API pages can be
    # restored and reused until this extraction is complete.
    save(progress, {"repo": REPO, "as_of": as_of.isoformat()})
    try:
        prs, fetched, bot_prs, bots = pull_requests(cache, refresh_start, as_of)
    except IncompleteEvidence:
        # Outages retain completed pages; inconsistent snapshots need fresh ones.
        progress.unlink(missing_ok=True)
        shutil.rmtree(cache, ignore_errors=True)
        raise
    retained = {}
    if previous:
        bots |= set(previous["meta"].get("excluded_bot_logins", []))
        retained = {p["number"]: p for p in previous["prs"]
                    if datetime.fromisoformat(p["merged_at"].replace("Z", "+00:00")) >= start}
    reused = len(retained)
    retained.update({p["number"]: p for p in prs})
    prs = [{**p, "reviewers": [r for r in p["reviewers"] if not is_bot(r, bots)]}
           for p in retained.values() if not is_bot(p["author"], bots)]
    prs.sort(key=lambda p: p["number"])
    meta = {"generated_at": as_of.isoformat(), "history_start": start.isoformat(), "history_days": HISTORY_DAYS,
            "pr_count": len(prs), "pr_fetched_count": fetched, "skipped": {"bot_prs": bot_prs},
            "repo": REPO, "evidence_version": 1, "refresh_start": refresh_start.isoformat(),
            "reused_pr_count": reused,
            "full_refresh_at": previous["meta"]["full_refresh_at"] if previous else as_of.isoformat(),
            "excluded_bot_logins": sorted(bots),
            "review_states": ["APPROVED", "COMMENTED"], "runtime_seconds": round(time.monotonic() - started, 2)}
    save(output, {"meta": meta, "prs": prs})
    progress.unlink(missing_ok=True)
    shutil.rmtree(cache, ignore_errors=True)
    print(json.dumps(meta, indent=2), flush=True)


if __name__ == "__main__":
    main()
