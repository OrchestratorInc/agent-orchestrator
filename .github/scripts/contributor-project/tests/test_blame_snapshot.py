import copy
import json
from pathlib import Path
import sys
import unittest

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "scripts"))
import blame_snapshot
from format_maps import compact_json

TAXONOMY = {"repo": "owner/repo", "areas": [{"id": a} for a in ("a", "b", "shared-infra")]}
RULES = [{"glob": "src/a/*", "area": "a"}, {"glob": "src/*", "area": "b"}, {"glob": "*", "area": "shared-infra"}]


class BlameSnapshotTests(unittest.TestCase):
    def test_aggregation_uses_first_path_match_and_excludes_mechanical_files_and_bots(self):
        raw = {"meta": {"generated_at": "2026-10-21T00:18:11Z", "origin_main_sha": "a" * 40,
                        "excluded_bot_logins": ["cursor"]},
               "blame": {"src/a/one.py": {"Alice": 2, "cursor": 99, "app[bot]": 99, "release-bot": 99,
                                          "unknown@example.com": 99},
                         "src/a/two.py": {"ALICE": 3, "bob": 1}, "src/b.py": {"alice": 7},
                         "README.md": {"bob": 4}, "package-lock.json": {"alice": 99},
                         "src/generated/code.go": {"alice": 99}, "test/__snapshots__/test.snap": {"alice": 99}}}
        actual = blame_snapshot.aggregate(raw, RULES, TAXONOMY)
        self.assertEqual(actual["areas"], {"a": {"Alice": 5, "bob": 1}, "b": {"Alice": 7}, "shared-infra": {"bob": 4}})
        self.assertEqual(actual["generated_at"], raw["meta"]["generated_at"])
        self.assertEqual(actual["origin_main_sha"], "a" * 40)

    def test_invalid_snapshot_metadata_and_counts_fail(self):
        valid = {"repo": TAXONOMY["repo"], "generated_at": "2026-10-21T00:00:00Z", "origin_main_sha": None,
                 "areas": {"a": {}, "b": {}, "shared-infra": {}}}
        invalid = [{}, {**valid, "repo": "other/repo"}, {**valid, "generated_at": "bad"},
                   {**valid, "generated_at": "2026-10-21"}, {**valid, "origin_main_sha": "bad"},
                   {**valid, "areas": {"a": {}}}]
        for people in ({"alice": -1}, {"alice": True}, {"alice": 1.5}, {"app[bot]": 1},
                       {"unknown@example.com": 1}, {"Alice": 1, "ALICE": 2}, []):
            item = copy.deepcopy(valid)
            item["areas"]["a"] = people
            invalid.append(item)
        for item in invalid:
            with self.subTest(item=item), self.assertRaises(ValueError):
                blame_snapshot.validate(item, TAXONOMY)
        self.assertEqual(blame_snapshot.validate(valid, TAXONOMY), valid)

    def test_missing_raw_blame_or_unmapped_path_fails(self):
        for raw in ({}, {"blame": {"unmatched.py": {"alice": 1}}, "meta": {}}):
            with self.subTest(raw=raw), self.assertRaises(ValueError):
                blame_snapshot.aggregate(raw, RULES[:-1], TAXONOMY)

    def test_committed_snapshot_matches_taxonomy(self):
        with (ROOT / "data/blame-lines.json").open() as source:
            snapshot = json.load(source)
        with (ROOT / "data/issue-areas.json").open() as source:
            taxonomy = json.load(source)
        blame_snapshot.validate(snapshot, taxonomy)


class CompactFormatTests(unittest.TestCase):
    def test_compact_format_preserves_loaded_content_and_is_idempotent(self):
        for name in ("path-areas", "issue-areas", "blame-lines"):
            path = ROOT / f"data/{name}.json"
            with path.open() as source:
                before = json.load(source)
            formatted = compact_json(before)
            self.assertEqual(json.loads(formatted), before)
            self.assertEqual(compact_json(json.loads(formatted)), formatted)
            self.assertEqual(path.read_text(), formatted)

    def test_compact_format_keeps_each_route_rule_and_area_on_one_line(self):
        for value in ([{"glob": "src/*", "area": "a"}],
                      {"areas": [{"id": "a", "label": "Unicode ☃ and a newline\n"}],
                       "issues": {"1": {"area": "a", "routing_reason": "quoted \"reason\""}}, "empty": []}):
            formatted = compact_json(value)
            self.assertEqual(json.loads(formatted), value)
            for line in formatted.splitlines():
                if any(key in line for key in ('"glob"', '"id"', '"routing_reason"')):
                    self.assertTrue(line.rstrip(",").endswith("}"))


if __name__ == "__main__":
    unittest.main()
