"""Credential-free safety tests for the memcode interactive restore gate."""
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import memcode_slash_resume as probe


class SlashResumeTests(unittest.TestCase):
    def screen(self):
        return ("○ idle\n\n→  Ask memcode…   ·   $ = shell\n\n"
                "memcode · 0.38.1 · main · clean · ao-native-restore-fixture · allow-all\n")

    def valid(self):
        return {"session_id": "sess_a0a0a0a0", "messages": [
            {"role": "user", "content": [{"type": "text", "text": "record fixture"}]},
            {"role": "assistant", "content": [{"type": "text", "text": "recorded"}]},
        ]}

    def test_transcript_validation_rejects_false_identity_and_unloadable_history(self):
        with tempfile.TemporaryDirectory() as temp:
            workspace = Path(temp)
            native = workspace / ".memcode/sessions/sess_a0a0a0a0"
            native.mkdir(parents=True)
            path = native / "messages.json"
            path.write_text(json.dumps(self.valid()))
            self.assertEqual(probe.validate_transcript(workspace, "sess_a0a0a0a0"), 2)
            for payload in [
                "{", json.dumps({"session_id": "sess_a0a0a0a0", "messages": []}),
                json.dumps(dict(self.valid(), session_id="sess_b1b1b1b1")),
            ]:
                with self.subTest(payload=payload):
                    path.write_text(payload)
                    with self.assertRaises(ValueError):
                        probe.validate_transcript(workspace, "sess_a0a0a0a0")
            path.unlink()
            with self.assertRaises(ValueError):
                probe.validate_transcript(workspace, "sess_a0a0a0a0")

    def test_id_validation_rejects_prefix_latest_and_path_escape(self):
        with tempfile.TemporaryDirectory() as temp:
            for native in ["latest", "a0a0", "sess_a0", "../sess_a0a0a0a0", "sess_a0a0a0a0\n"]:
                with self.subTest(native=native), self.assertRaises(ValueError):
                    probe.validate_transcript(Path(temp), native)

    def test_banner_does_not_override_stale_or_wrong_identity_witness(self):
        screen = self.screen()
        history = "↩ resumed sess_a0a0a0a0 (2 messages)\n"
        witness = {"generation": "launch-current", "native_id": "sess_a0a0a0a0", "sequence": 2}
        self.assertTrue(probe.restored(screen, history, witness, "launch-current", 1, 2))
        for bad in [
            dict(witness, generation="launch-old"),
            dict(witness, native_id="sess_b1b1b1b1"),
            dict(witness, sequence=1), {},
        ]:
            with self.subTest(witness=bad):
                self.assertFalse(probe.restored(screen, history, bad, "launch-current", 1, 2))
        self.assertFalse(probe.restored(screen, "↩ resumed sess_a0a0a0a0 — the conversation continues",
                                        witness, "launch-current", 1, 2))

    def test_restore_requires_current_empty_composer_and_matching_loaded_message_count(self):
        witness = {"generation": "launch-current", "native_id": "sess_a0a0a0a0", "sequence": 2}
        history = "↩ resumed sess_a0a0a0a0 (2 messages)\n"
        self.assertFalse(probe.restored(self.screen().replace("Ask memcode…", "human draft"),
                                       history, witness, "launch-current", 1, 2))
        self.assertFalse(probe.restored(self.screen(), history, witness, "launch-current", 1, 3))
        self.assertFalse(probe.restored("↺ ready main", history, witness, "launch-current", 1, 2))

    def test_report_persistence_does_not_depend_on_stdout(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "report.json"
            with patch("builtins.print", side_effect=BrokenPipeError):
                probe.write_report(path, {"verdict": "FAIL", "reason": "wrong identity"})
            self.assertTrue(path.exists())
            self.assertEqual(json.loads(path.read_text())["reason"], "wrong identity")


if __name__ == "__main__":
    unittest.main()
