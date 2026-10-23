import contextlib
import importlib.util
import io
from datetime import datetime, timedelta, timezone
import json
import sys
import tempfile
from unittest.mock import patch
from pathlib import Path
import unittest

SPEC = importlib.util.spec_from_file_location("ownership_extract", Path(__file__).resolve().parents[1] / "scripts/ownership_extract.py")
ownership = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(ownership)


class BotTests(unittest.TestCase):
    def test_bots(self):
        for value in ["app[bot]", "dependabot", "github-actions", "renovate", "123+app[bot]@users.noreply.github.com", "bot@example.com", "release-bot@example.com", "noreply@github.com"]:
            with self.subTest(value=value):
                self.assertTrue(ownership.is_bot(value))

    def test_humans_and_missing(self):
        for value in [None, "Rishet11", "robotics", "alice@example.com", "12+alice@users.noreply.github.com"]:
            with self.subTest(value=value):
                self.assertFalse(ownership.is_bot(value))

    def test_github_actor_type_finds_bots_without_suffixes(self):
        self.assertTrue(ownership.is_bot("Cursor", {"cursor"}))
        self.assertFalse(ownership.is_bot("AgentWrapper", {"cursor"}))


class SkipTests(unittest.TestCase):
    def test_paths(self):
        for path in ["frontend/package-lock.json", "go.sum", "yarn.lock", "node_modules/a.ts", "a/dist/x.ts", "build/main.go", "vendor/a.go", "a/gen/code.go", "a/generated/code.ts", "a/__snapshots__/test.ts", "a/test.snap", "app.min.js", "app.js.map", "a.pb.go", "protocol.gen.go", "file.generated.ts", "backend/internal/httpd/apispec/openapi.yaml"]:
            with self.subTest(path=path):
                self.assertIsNotNone(ownership.skip_reason(path))

    def test_normal_source(self):
        for path in ["backend/internal/service/build.go", "frontend/src/generator.ts", "docs/build.md", "test/fixtures/small.txt"]:
            with self.subTest(path=path):
                self.assertIsNone(ownership.skip_reason(path))

    def test_generated_schema_paths_are_excluded_without_file_contents(self):
        for path in ["frontend/src/api/schema.ts", "packages/cloud-client/src/schema.ts"]:
            with self.subTest(path=path):
                self.assertEqual(ownership.skip_reason(path), "generated_minified")


class ExtractionTests(unittest.TestCase):
    def test_pr_file_and_review_pagination_excludes_typed_bots(self):
        def connection(nodes, cursor=None):
            return {"nodes": nodes, "pageInfo": {"hasNextPage": cursor is not None, "endCursor": cursor}}

        def actor(login, kind="User"):
            return {"login": login, "__typename": kind}

        first = {"number": 1, "mergedAt": "2026-10-21T00:00:00Z", "changedFiles": 2,
                 "author": actor("AgentWrapper"),
                 "files": connection([{"path": "src/a.py", "additions": 3, "deletions": 2}], "files-next"),
                 "reviews": connection([{"author": actor("Cursor", "Bot")}], "reviews-next")}
        second = {"number": 2, "mergedAt": "2026-10-21T00:00:00Z", "changedFiles": 0,
                  "author": actor("Cursor", "Bot"), "files": connection([]), "reviews": connection([])}
        pages = [
            {"data": {"search": {**connection([first], "prs-next"), "issueCount": 2}}},
            {"data": {"search": {**connection([second]), "issueCount": 2}}},
            {"data": {"repository": {"pullRequest": {"files": connection([
                {"path": "src/b.py", "additions": 1, "deletions": 0}])}}}},
            {"data": {"repository": {"pullRequest": {"reviews": connection([
                {"author": actor("bob")}, {"author": actor("bob")}, {"author": None}])}}}},
        ]
        start = datetime(2026, 10, 20, tzinfo=timezone.utc)
        end = start + timedelta(days=1)
        with patch.object(ownership, "api", side_effect=pages) as api, contextlib.redirect_stdout(io.StringIO()):
            prs, fetched, skipped, bots = ownership.pull_requests(Path("unused"), start, end)
        self.assertEqual((fetched, skipped, bots), (2, 1, {"cursor"}))
        self.assertEqual(prs, [{"number": 1, "merged_at": first["mergedAt"], "author": "AgentWrapper",
            "reviewers": ["bob"], "files": [{"path": "src/a.py", "churn": 5}, {"path": "src/b.py", "churn": 1}]}])
        query = "repo:OrchestratorInc/agent-orchestrator is:pr is:merged merged:2026-10-20T00:00:00+00:00..2026-10-21T00:00:00+00:00 sort:created-asc"
        self.assertEqual([call.args[2] for call in api.call_args_list], [
            {"search": query, "cursor": None}, {"search": query, "cursor": "prs-next"}, {"number": 1, "cursor": "files-next"},
            {"number": 1, "cursor": "reviews-next"}])

    def test_search_splits_dense_windows_and_fetches_more_than_3000_prs(self):
        start = datetime(2026, 10, 20, tzinfo=timezone.utc)
        end = start + timedelta(seconds=3)
        empty = {"nodes": [], "pageInfo": {"hasNextPage": False, "endCursor": None}}
        nodes = [{"number": n, "mergedAt": (start + timedelta(seconds=n // 800)).isoformat(),
                  "changedFiles": 0, "author": {"login": "alice", "__typename": "User"},
                  "files": empty, "reviews": empty} for n in range(3200)]
        queries = []

        def api(cache, query, variables):
            bounds = variables["search"].split("merged:")[1].split()[0].split("..")
            first, last = map(datetime.fromisoformat, bounds)
            selected = [n for n in nodes if first <= datetime.fromisoformat(n["mergedAt"]) <= last]
            offset = int(variables["cursor"] or 0)
            following = offset + 50
            queries.append((first, last))
            return {"data": {"search": {"issueCount": len(selected), "nodes": selected[offset:following],
                "pageInfo": {"hasNextPage": following < len(selected), "endCursor": str(following)}}}}

        with patch.object(ownership, "api", side_effect=api), contextlib.redirect_stdout(io.StringIO()):
            prs, fetched, skipped, bots = ownership.pull_requests(Path("unused"), start, end)
        self.assertEqual((len(prs), fetched, skipped, bots), (3200, 3200, 0, set()))
        self.assertEqual({pr["number"] for pr in prs}, set(range(3200)))
        self.assertIn((start, start), queries)
        self.assertIn((end, end), queries)

    def test_incomplete_and_duplicate_search_results_fail(self):
        start = datetime(2026, 10, 20, tzinfo=timezone.utc)
        for nodes, count in [([], 1), ([{"number": 1}, {"number": 1}], 2)]:
            data = {"data": {"search": {"issueCount": count, "nodes": nodes,
                    "pageInfo": {"hasNextPage": False, "endCursor": None}}}}
            with self.subTest(nodes=nodes), patch.object(ownership, "api", return_value=data), \
                 contextlib.redirect_stdout(io.StringIO()), self.assertRaisesRegex(RuntimeError, "pagination"):
                ownership.pull_requests(Path("unused"), start, start)

    def test_cli_uses_bounded_pr_evidence_without_git(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with patch.object(ownership, "ROOT", root), patch.object(sys, "argv", ["ownership_extract.py"]), \
                 patch.object(ownership.subprocess, "check_output", side_effect=AssertionError("unexpected Git call")), \
                 patch.object(ownership, "pull_requests", return_value=([], 0, 0, set())) as prs, \
                 contextlib.redirect_stdout(io.StringIO()):
                ownership.main()
            raw = json.loads((root / "research/ownership-raw.json").read_text())
            self.assertEqual(set(raw), {"meta", "prs"})
            self.assertNotIn("origin_main_sha", raw["meta"])
            self.assertNotIn("pr_cap", raw["meta"])
            self.assertEqual(raw["meta"]["history_days"], 540)
            self.assertEqual(prs.call_args.args[2] - prs.call_args.args[1], timedelta(days=540))


if __name__ == "__main__":
    unittest.main()
