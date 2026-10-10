"""Credential-free tests for the native memcode probe, not provider conformance."""
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import memcode_conformance as probe


class ProbeTests(unittest.TestCase):
    def screen(self):
        return ("○ idle\n\n→  Ask memcode…   ·   $ = shell\n\n"
                "memcode · 0.38.1 · main · clean · " + probe.MODEL + " · allow-all\n")

    def test_ready_requires_initialized_composer_model_and_idle(self):
        self.assertTrue(probe.ready(self.screen()))
        for absent in ("○ idle", "Ask memcode", probe.MODEL):
            with self.subTest(absent=absent):
                self.assertFalse(probe.ready(self.screen().replace(absent, "")))

    def test_banner_draft_and_panic_do_not_prove_readiness(self):
        self.assertFalse(probe.ready("↺ ready main · " + probe.MODEL))
        self.assertFalse(probe.ready(self.screen().replace("Ask memcode…", "human draft")))
        self.assertFalse(probe.ready(self.screen() + probe.PANIC))

    def test_fixture_has_exact_identity_and_nonempty_native_wire_history(self):
        with tempfile.TemporaryDirectory() as temp:
            path = probe.fixture(Path(temp))
            data = json.loads(path.read_text())
            self.assertEqual(data["session_id"], probe.NATIVE_ID)
            self.assertEqual(path.parent.name, probe.NATIVE_ID)
            self.assertEqual([m["role"] for m in data["messages"]], ["user", "assistant"])
            self.assertTrue(all(m["content"][0]["type"] == "text" for m in data["messages"]))

    def test_environment_does_not_forward_credentials_or_user_profile(self):
        with patch.dict(os.environ, {
            "MEMCODE_API_TOKEN": "test-only", "MEMCODE_ENDPOINT_KEY": "test-only",
            "OPENAI_API_KEY": "test-only", "ZAI_API_KEY": "test-only",
            "HOME": "/real-profile", "HTTPS_PROXY": "http://test-only",
        }):
            env = probe.isolated_env(Path("/isolated"))
        for name in ("MEMCODE_API_TOKEN", "MEMCODE_ENDPOINT_KEY", "OPENAI_API_KEY",
                     "ZAI_API_KEY", "HTTPS_PROXY"):
            self.assertNotIn(name, env)
        self.assertEqual(env["HOME"], "/isolated")
        self.assertEqual(env["MEMCODE_ENDPOINT_URL"], "http://127.0.0.1:9/v1")

    def test_report_survives_closed_stdout(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            binary = root / "memcode"
            binary.write_text("fixture")
            p = probe.Probe(binary, root, 1)
            p.result["gates"]["exact_native_restore"] = {"status": "FAIL", "reason": probe.PANIC}
            with patch("builtins.print", side_effect=BrokenPipeError):
                p.write_report()
            self.assertEqual(json.loads((root / "report.json").read_text())["gates"]
                             ["exact_native_restore"]["status"], "FAIL")


if __name__ == "__main__":
    unittest.main()
