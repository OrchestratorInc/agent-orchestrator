#!/usr/bin/env python3
"""Read recent public merged PRs. Delete ownership-cache/ to refresh evidence."""
import argparse
from datetime import datetime, timedelta, timezone
import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import subprocess
import time

ROOT = Path(__file__).resolve().parents[1]
HISTORY_DAYS = 540  # Six 90-day half-lives; older churn has less than 1/64 weight.
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
                raise RuntimeError("PR pagination did not advance")
            cursor = following
        if len(nodes) != total:
            raise RuntimeError("PR pagination is incomplete; refresh the GraphQL cache")
        print(f"PRs: {len(nodes)} in {first.date()}..{last.date()}", flush=True)
        return nodes

    nodes = window(start, end)
    if len({n["number"] for n in nodes}) != len(nodes):
        raise RuntimeError("PR pagination is inconsistent; refresh the GraphQL cache")
    prs = []
    for node in nodes:
        for field, selection in [("files", FILES), ("reviews", REVIEWS)]:
            connection = node[field]
            while connection["pageInfo"]["hasNextPage"]:
                query = 'query($number:Int!,$cursor:String) { repository(owner:"OrchestratorInc",name:"agent-orchestrator") { pullRequest(number:$number) { ' + selection + ' } } }'
                connection = api(cache, query, {"number": node["number"], "cursor": connection["pageInfo"]["endCursor"]})["data"]["repository"]["pullRequest"][field]
                node[field]["nodes"].extend(connection["nodes"])
        if len(node["files"]["nodes"]) != node["changedFiles"]:
            raise RuntimeError(f"Incomplete file list for PR #{node['number']}")
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
    parser.parse_args()
    started = time.monotonic()
    cache = ROOT / "research/ownership-cache"
    as_of = datetime.now(timezone.utc).replace(microsecond=0)
    start = as_of - timedelta(days=HISTORY_DAYS)
    prs, fetched, bot_prs, bots = pull_requests(cache, start, as_of)
    meta = {"generated_at": as_of.isoformat(), "history_start": start.isoformat(), "history_days": HISTORY_DAYS,
            "pr_count": len(prs), "pr_fetched_count": fetched, "skipped": {"bot_prs": bot_prs},
            "excluded_bot_logins": sorted(bots),
            "review_states": ["APPROVED", "COMMENTED"], "runtime_seconds": round(time.monotonic() - started, 2)}
    save(ROOT / "research/ownership-raw.json", {"meta": meta, "prs": prs})
    print(json.dumps(meta, indent=2), flush=True)


if __name__ == "__main__":
    main()
