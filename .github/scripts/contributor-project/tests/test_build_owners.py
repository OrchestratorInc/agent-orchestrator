import importlib.util
import contextlib
import io
import json
import tempfile
from unittest.mock import patch
from datetime import datetime, timedelta, timezone
from math import log1p
import sys
from pathlib import Path
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "scripts"))

SPEC = importlib.util.spec_from_file_location("build_owners", Path(__file__).resolve().parents[1] / "scripts/build_owners.py")
builder = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(builder)
AS_OF = datetime(2026, 10, 21, tzinfo=timezone.utc)
TAXONOMY = {"repo": "OrchestratorInc/agent-orchestrator", "areas": [
    {"id": key, "label": key} for key in ["a", "b", "shared-infra", "needs-area"]]}
RULES = [{"glob": "src/a/*", "area": "a"}, {"glob": "src/*", "area": "b"}, {"glob": "*", "area": "shared-infra"}]


def pr(number, author="alice", age=0, files=None, reviewers=()):
    return {"number": number, "author": author, "merged_at": (AS_OF - timedelta(days=age)).isoformat(),
            "files": files if files is not None else [{"path": "src/a/file.py", "churn": 100}], "reviewers": list(reviewers)}


def snapshot(areas=None):
    return {"repo": TAXONOMY["repo"], "generated_at": AS_OF.isoformat(), "origin_main_sha": "a" * 40,
            "areas": {a["id"]: (areas or {}).get(a["id"], {}) for a in TAXONOMY["areas"]}}


def build(prs, blame=None, **kwargs):
    seeds = [pr(1000 + i, f"seed{i}", files=[{"path": "README.md", "churn": 1}]) for i in range(8)]
    raw = {"meta": {"generated_at": AS_OF.isoformat(), "excluded_bot_logins": ["cursor"]},
           "prs": seeds + prs}
    return builder.build(raw, RULES, TAXONOMY, snapshot(blame), generated_at=AS_OF.isoformat(), threshold=kwargs.pop("threshold", 1), **kwargs)


def owner(data, area, login="alice"):
    return next(person for person in data["areas"][area]["owners"] if person["login"] == login)


class OwnerBuilderTests(unittest.TestCase):
    def test_mechanical_files_do_not_change_pr_credit_or_churn(self):
        source = {"path": "src/a/file.py", "churn": 100}
        mechanical = [{"path": path, "churn": 1000000} for path in [
            "package-lock.json", "frontend/pnpm-lock.yaml", "go.sum", "yarn.lock",
            "vendor/import.go", "a/generated/code.ts", "a/gen/code.go", "a/dist/bundle.js",
            "test/__snapshots__/ui.snap", "api.pb.go", "backend/internal/httpd/apispec/openapi.yaml",
            "frontend/src/api/schema.ts", "packages/cloud-client/src/schema.ts",
        ]]
        _, baseline = build([pr(1, files=[source], reviewers=["seed0"])])
        _, actual = build([pr(1, files=[source] + mechanical, reviewers=["seed0"])])
        self.assertEqual(actual, baseline)

    def test_mechanical_only_and_fallback_prs_never_suggest_owners(self):
        core, data = build([pr(1, files=[{"path": "package-lock.json", "churn": 1000000}]),
                            pr(2, files=[{"path": "README.md", "churn": 100}])])
        self.assertTrue(any(person["login"] == "alice" for person in core["contributors"]))
        self.assertFalse(any(area["owners"] for area in data["areas"].values()))

    def test_pr_churn_grows_logarithmically(self):
        _, data = build([pr(1), pr(2, "bob", files=[{"path": "src/a/file.py", "churn": 1000000}])])
        alice = owner(data, "a")["weighted_churn"]
        bob = owner(data, "a", "bob")["weighted_churn"]
        self.assertAlmostEqual(bob, log1p(1000000), places=6)
        self.assertLess(bob / alice, 3)

    def test_churn_proportions_half_life_and_evidence(self):
        core, data = build([
            pr(1, age=90, files=[{"path": "src/a/one.py", "churn": 75}, {"path": "src/b.py", "churn": 25}]),
            pr(2), pr(3, files=[{"path": "src/b.py", "churn": 100}]),
        ])
        a, b = owner(data, "a"), owner(data, "b")
        self.assertAlmostEqual(a["merged_prs_90d"], 1.75)
        self.assertAlmostEqual(b["merged_prs_90d"], 1.25)
        self.assertAlmostEqual(a["weighted_churn"], log1p(100) * 1.375, places=6)
        self.assertAlmostEqual(b["weighted_churn"], log1p(100) * 1.125, places=6)
        self.assertEqual(a["last_merged_at"], AS_OF.isoformat())
        alice = next(person for person in core["contributors"] if person["login"] == "alice")
        self.assertEqual(alice["merged_prs_90d"], 3)
        self.assertEqual(alice["top_areas"], ["a", "b"])
        self.assertNotIn("origin_main_sha", data)
        self.assertEqual(core["status"], "proposed")

    def test_window_boundary_and_old_prs(self):
        _, data = build([pr(1, age=90), pr(2, age=91), pr(3, age=180)])
        item = owner(data, "a")
        self.assertEqual(item["merged_prs_90d"], 1)
        self.assertNotIn("merged_prs_all_time", item)
        self.assertAlmostEqual(item["weighted_churn"], log1p(100) * (0.5 + 2 ** (-91 / 90) + 0.25), places=6)

    def test_reviews_are_unique_proportional_recent_and_not_self_reviews(self):
        _, data = build([
            pr(1, "seed0", files=[{"path": "src/a/x", "churn": 50}, {"path": "src/b", "churn": 50}], reviewers=["alice", "alice", "seed0"]),
            pr(2, "seed0", reviewers=["alice"]), pr(3, "seed0", reviewers=["alice"]),
            pr(4, "seed0", age=91, reviewers=["alice"]),
            pr(5, files=[{"path": "README.md", "churn": 1}]),
        ])
        self.assertEqual(owner(data, "a")["reviews_90d"], 2.5)
        self.assertEqual(owner(data, "a", "seed0")["reviews_90d"], 0)

    def test_old_prs_and_tiny_recent_changes_do_not_establish_ownership(self):
        _, data = build([
            pr(1, files=[{"path": "src/a/x", "churn": 1}, {"path": "src/b", "churn": 999}]),
            pr(2, age=180), pr(3, files=[{"path": "src/b", "churn": 100}]),
        ])
        self.assertEqual(data["areas"]["a"]["owners"], [])
        self.assertTrue(data["areas"]["a"]["unowned"])
        self.assertTrue(data["areas"]["needs-area"]["unowned"])

    def test_recent_prs_outrank_less_active_contributors_and_noncore_is_excluded(self):
        records = [pr(1), pr(2), pr(3, "bob"), pr(4, "outsider", age=91)]
        _, data = build(records)
        self.assertEqual([person["login"] for person in data["areas"]["a"]["owners"]], ["alice", "bob"])
        self.assertEqual(owner(data, "a")["score"], 85)
        self.assertLess(owner(data, "a", "bob")["score"], 85)

    def test_blame_restores_five_percent_and_missing_login_means_zero(self):
        records = [pr(1), pr(2), pr(3, "seed0", reviewers=["alice"])]
        _, missing = build(records)
        _, data = build(records, {"a": {"alice": 10}})
        self.assertEqual(data["score_weights"], {"merged_prs_90d": 60, "weighted_churn": 25, "reviews_90d": 10, "blame_lines": 5})
        self.assertEqual(owner(missing, "a")["score"], 95)
        self.assertEqual(owner(missing, "a")["blame_lines"], 0)
        self.assertEqual(owner(data, "a")["score"], 100)
        self.assertEqual(owner(data, "a")["blame_lines"], 10)
        self.assertEqual(data["blame_origin_main_sha"], "a" * 40)
        self.assertEqual(data["blame_generated_at"], AS_OF.isoformat())

    def test_blame_normalizes_by_area_maximum_among_core_only(self):
        records = [pr(1), pr(2, "bob")]
        _, baseline = build(records)
        _, data = build(records, {"a": {"ALICE": 50, "bob": 100, "outsider": 1000000}, "b": {"alice": 10000}})
        self.assertEqual(owner(data, "a")["score"] - owner(baseline, "a")["score"], 2.5)
        self.assertEqual(owner(data, "a", "bob")["score"] - owner(baseline, "a", "bob")["score"], 5)
        self.assertEqual(owner(data, "a")["blame_lines"], 50)

    def test_blame_alone_does_not_establish_activity_or_core_membership(self):
        records = [pr(1, files=[{"path": "src/b.py", "churn": 100}])]
        core, data = build(records, {"a": {"alice": 1000000, "outsider": 1000000}})
        self.assertEqual(data["areas"]["a"]["owners"], [])
        self.assertNotIn("outsider", {person["login"] for person in core["contributors"]})

    def test_zero_churn_prs_split_equally_and_first_path_match_wins(self):
        _, data = build([pr(i, files=[{"path": "src/a/b", "churn": 0}, {"path": "src/c", "churn": 0}]) for i in [1, 2]])
        self.assertEqual(owner(data, "a")["merged_prs_90d"], 1)
        self.assertEqual(owner(data, "b")["merged_prs_90d"], 1)
        self.assertEqual(owner(data, "a")["weighted_churn"], 0)

    def test_bots_unresolved_emails_and_case_duplicates(self):
        core, data = build([pr(1, "Alice"), pr(2, "alice"), pr(3, "cursor"), pr(4, "app[bot]"), pr(5, "name@example.com")])
        self.assertEqual(next(person for person in core["contributors"] if person["login"] == "Alice")["merged_prs_90d"], 2)
        self.assertEqual([item["login"] for item in data["areas"]["a"]["owners"]], ["Alice"])

    def test_bad_input_and_core_threshold_stop_generation(self):
        for records, kwargs, message in [
            ([pr(1), pr(1)], {}, "duplicate PR"), ([pr(1, age=-1)], {}, "after the evidence"),
        ]:
            with self.subTest(message=message), self.assertRaisesRegex(ValueError, message):
                build(records, **kwargs)

    def test_overrides_adjust_threshold_list_and_exclude_wins(self):
        overrides = {"include": ["bob", "alice"], "exclude": ["ALICE"]}
        core, data = build([pr(1), pr(2), pr(3, "bob")], core_override=overrides)
        self.assertEqual([person["login"] for person in core["contributors"] if person["login"] in {"alice", "bob"}], ["bob"])
        self.assertEqual([person["login"] for person in data["areas"]["a"]["owners"]], ["bob"])
        self.assertEqual(core["threshold_merged_prs_90d"], 1)

    def test_ten_recent_pr_threshold_has_no_membership_count_limit(self):
        core, _ = build([pr(i) for i in range(10)], threshold=10)
        self.assertEqual([p["login"] for p in core["contributors"]], ["alice"])
        core, _ = build([pr(i) for i in range(9)], threshold=10)
        self.assertEqual(core["contributors"], [])

    def test_invalid_core_overrides_fail(self):
        for overrides in ({"include": ["bad@login"], "exclude": []}, {"include": ["alice", "ALICE"], "exclude": []}, {"include": [], "exclude": "alice"}):
            with self.subTest(overrides=overrides), self.assertRaisesRegex(ValueError, "core overrides"):
                build([], core_override=overrides)

    def test_failed_build_keeps_restored_owners_or_leaves_them_absent(self):
        for restored in (True, False):
            with self.subTest(restored=restored), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                (root / "data").mkdir()
                output = root / "research/owners/area-owners.json"
                output.parent.mkdir(parents=True)
                if restored:
                    output.write_text("restored owners")
                for name, data in [("issue-areas", TAXONOMY), ("path-areas", RULES),
                                   ("core-overrides", {"include": ["invalid@login"], "exclude": []}), ("blame-lines", snapshot())]:
                    (root / f"data/{name}.json").write_text(json.dumps(data))
                raw_file = root / "raw.json"
                raw_file.write_text(json.dumps({"meta": {"generated_at": AS_OF.isoformat()}, "prs": []}))
                with patch.object(builder, "ROOT", root), patch.object(sys, "argv", ["build_owners.py", "--raw-file", str(raw_file)]), \
                     self.assertRaisesRegex(ValueError, "core overrides"):
                    builder.main()
                self.assertEqual(output.read_text() if output.exists() else None, "restored owners" if restored else None)

    def test_cli_writes_generated_maps_only_to_ignored_output(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "data").mkdir()
            for name, data in [("issue-areas", TAXONOMY), ("path-areas", RULES),
                               ("core-overrides", {"include": [], "exclude": []}), ("blame-lines", snapshot())]:
                (root / f"data/{name}.json").write_text(json.dumps(data))
            raw = {"meta": {"generated_at": AS_OF.isoformat()},
                   "prs": [pr(i) for i in range(10)]}
            raw_file = root / "raw.json"
            raw_file.write_text(json.dumps(raw))
            with patch.object(builder, "ROOT", root), patch.object(sys, "argv", ["build_owners.py", "--raw-file", str(raw_file)]), contextlib.redirect_stdout(io.StringIO()):
                builder.main()
            self.assertEqual(json.loads((root / "research/owners/core-contributors.json").read_text())["contributors"][0]["login"], "alice")
            self.assertTrue((root / "research/owners/area-owners.json").exists())
            self.assertFalse((root / "data/area-owners.json").exists())
            self.assertFalse((root / "data/core-contributors.json").exists())

    def test_missing_or_invalid_committed_blame_fails_without_replacing_owners(self):
        for contents in (None, "{broken", "{}"):
            with self.subTest(contents=contents), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                (root / "data").mkdir()
                for name, data in [("issue-areas", TAXONOMY), ("path-areas", RULES),
                                   ("core-overrides", {"include": [], "exclude": []})]:
                    (root / f"data/{name}.json").write_text(json.dumps(data))
                if contents is not None:
                    (root / "data/blame-lines.json").write_text(contents)
                raw_file = root / "raw.json"
                raw_file.write_text(json.dumps({"meta": {"generated_at": AS_OF.isoformat()}, "prs": []}))
                output = root / "research/owners/area-owners.json"
                output.parent.mkdir(parents=True)
                output.write_text("restored owners")
                with patch.object(builder, "ROOT", root), patch.object(sys, "argv", ["build_owners.py", "--raw-file", str(raw_file)]), \
                     self.assertRaises((FileNotFoundError, ValueError)):
                    builder.main()
                self.assertEqual(output.read_text(), "restored owners")


if __name__ == "__main__":
    unittest.main()
