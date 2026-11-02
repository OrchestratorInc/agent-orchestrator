import contextlib
import importlib.util
import io
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch


SPEC = importlib.util.spec_from_file_location("check_areas", Path(__file__).resolve().parents[1] / "scripts/check_areas.py")
checker = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(checker)


class AreaCheckTests(unittest.TestCase):
    def check(self, rules):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "data").mkdir()
            (root / "data/issue-areas.json").write_text(json.dumps({
                "areas": [{"id": key, "label": key, "description": key}
                          for key in ["source", "shared-infra"]],
                "issues": {},
            }))
            (root / "data/path-areas.json").write_text(json.dumps(rules))
            outputs = [b"commit-sha\n", b"100644 blob object-sha\tsrc/live.py\0", b"print('live')\n"]
            with patch.object(checker, "ROOT", root), patch.object(sys, "argv", ["check_areas.py", "--repo", directory, "--ref", "test-ref"]), patch.object(checker.subprocess, "check_output", side_effect=outputs), contextlib.redirect_stdout(io.StringIO()) as out:
                checker.main()
            return out.getvalue()

    def test_dead_nonfallback_glob_fails_with_ref_and_glob(self):
        with self.assertRaisesRegex(AssertionError, "test-ref: removed/\\*\\*"):
            self.check([
                {"glob": "removed/**", "area": "source"},
                {"glob": "src/*", "area": "source"},
                {"glob": "*", "area": "shared-infra"},
            ])

    def test_overlapping_live_globs_do_not_need_to_win_first_match(self):
        output = self.check([
            {"glob": "src/live.py", "area": "source"},
            {"glob": "src/*", "area": "source"},
            {"glob": "*", "area": "shared-infra"},
        ])
        self.assertIn("PASS: 2 areas; 3 rules", output)

    def test_dead_glob_before_fallback_fails_even_for_shared_infra(self):
        with self.assertRaisesRegex(AssertionError, "removed/\\*"):
            self.check([
                {"glob": "src/*", "area": "source"},
                {"glob": "removed/*", "area": "shared-infra"},
                {"glob": "*", "area": "shared-infra"},
            ])


if __name__ == "__main__":
    unittest.main()
