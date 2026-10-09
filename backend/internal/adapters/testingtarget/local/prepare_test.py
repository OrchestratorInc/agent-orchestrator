import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("prepare", Path(__file__).resolve().parents[3] / "skillassets/testing/scripts/prepare.py")
prepare = importlib.util.module_from_spec(spec)
spec.loader.exec_module(prepare)


class WarmPreparationTest(unittest.TestCase):
    def test_reuses_checkout_and_installs_only_changed_locks(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            calls = []

            def run(args, cwd):
                calls.append((args, cwd))
                if args[:2] == ["git", "clone"]:
                    checkout = Path(args[-1])
                    for name in ["frontend", "packages/product-ui"]:
                        folder = checkout / name
                        folder.mkdir(parents=True)
                        (folder / "package-lock.json").write_text(name)
                        (folder / "node_modules").mkdir()
                        (folder / "node_modules/kept").write_text("cached")
                return "https://example.test/repo.git" if "get-url" in args else ""

            with patch.object(prepare, "run", side_effect=run):
                first = prepare.prepare(root, "a" * 40, root / "cache")
                second = prepare.prepare(first, "b" * 40, root / "cache")
                self.assertEqual(first, second)
                self.assertIn((["git", "fetch", "--no-tags", "origin", "b" * 40], first), calls)
                self.assertEqual(sum(args[:2] == ["git", "clone"] for args, _ in calls), 1)
                self.assertEqual(sum(args[:2] == ["npm", "install"] for args, _ in calls), 2)
                (first / "frontend/package-lock.json").write_text("changed")
                prepare.prepare(root, "c" * 40, root / "cache")
                self.assertEqual(sum(args[:2] == ["npm", "install"] for args, _ in calls), 3)
                (first / ".ao-testing-active").write_text("live launch")
                with self.assertRaises(FileExistsError):
                    prepare.prepare(root, "d" * 40, root / "cache")
                self.assertEqual((first / ".ao-testing-active").read_text(), "live launch")
                self.assertEqual((first / "frontend/node_modules/kept").read_text(), "cached")
                self.assertFalse(any("clean" in args or "ci" in args for args, _ in calls))

    def test_failed_install_does_not_stamp_or_override_caches(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "package-lock.json").write_text("lock")
            with patch.object(prepare, "run", side_effect=subprocess.CalledProcessError(1, "npm")):
                with self.assertRaises(subprocess.CalledProcessError):
                    prepare.install(root, root / "stamp")
            self.assertFalse((root / "stamp").exists())
        with patch.dict(os.environ, {"GOCACHE": "/normal/go", "npm_config_cache": "/normal/npm",
                                     "AO_DATA_DIR": "/supervisor"}):
            with patch.object(subprocess, "run") as command:
                command.return_value.stdout = ""
                prepare.run(["git", "status"], Path("."))
                env = command.call_args.kwargs["env"]
                self.assertEqual(env["GOCACHE"], "/normal/go")
                self.assertEqual(env["npm_config_cache"], "/normal/npm")
                self.assertNotIn("AO_DATA_DIR", env)
