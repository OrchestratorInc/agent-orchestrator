import importlib.util
import json
from pathlib import Path
import shutil
import tempfile
import unittest
from unittest.mock import Mock, patch

path = Path(__file__).resolve().parents[3] / "skillassets/testing/scripts/export_clip.py"
spec = importlib.util.spec_from_file_location("export_clip", path)
clip = importlib.util.module_from_spec(spec)
spec.loader.exec_module(clip)


class ClipSelectionTest(unittest.TestCase):
    def test_bounds_and_foreign_recording_are_refused_before_export(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            attempt = root / "attempt"
            attempt.mkdir()
            movie = attempt / "original.mp4"
            movie.write_bytes(b"original")
            foreign = root / "foreign.mov"
            foreign.write_bytes(b"foreign")
            with patch.object(clip.subprocess, "run") as command:
                for start, seconds in [(-1, 1), (0, 16), (0, 0), (float("nan"), 1)]:
                    with self.assertRaises(ValueError):
                        clip.export(attempt, movie, start, seconds, "before")
                with self.assertRaises(ValueError):
                    clip.export(attempt, foreign, 0, 1, "before")
                command.assert_not_called()

    def test_selected_clip_retains_original_and_records_hashes(self):
        for metadata, expected in [
            ({"format": {"duration": "30"}, "streams": [{"codec_type": "video", "duration": "N/A"}]}, 30),
            ({"format": {"duration": "N/A"}, "streams": [{"codec_type": "video", "duration": "9"}]}, 9),
            ({"format": {}, "streams": []}, None),
            ({"format": {"duration": "N/A"}, "streams": [{"codec_type": "video", "duration": "nan"}]}, None),
        ]:
            with self.subTest(metadata=metadata):
                with patch.object(clip.subprocess, "run", return_value=Mock(stdout=json.dumps(metadata))):
                    if expected is None:
                        with self.assertRaisesRegex(ValueError, "finite positive duration"):
                            clip.duration(Path("recording.mp4"))
                    else:
                        self.assertEqual(clip.duration(Path("recording.mp4")), expected)
        with tempfile.TemporaryDirectory() as tmp:
            attempt = Path(tmp)
            movie = attempt / "original.mp4"
            movie.write_bytes(b"retained original")

            def command(args, **kwargs):
                if "-movflags" in args:
                    Path(args[-1]).write_bytes(b"selected clip")
                    self.assertIn("-n", args)
                    self.assertLess(args.index("-i"), args.index("-ss"))
                    self.assertEqual(args[args.index("-vf") + 1], "fps=30,tpad=stop_mode=clone:stop_duration=30")

            with patch.object(clip, "duration", side_effect=[30, 9]):
                with patch.object(clip.subprocess, "run", side_effect=command):
                    result = clip.export(attempt, movie, 3, 9, "before")
            self.assertEqual(movie.read_bytes(), b"retained original")
            selection = json.loads(result.with_suffix(".selection.json").read_text())
            self.assertEqual(selection["startSeconds"], 3)
            self.assertEqual(selection["durationSeconds"], 9)
            self.assertEqual(selection["exportSHA256"], clip.digest(result))
            self.assertIn(clip.digest(result), result.with_suffix(".sha256").read_text())

        with self.subTest(sparse_recording=True):
            if not shutil.which("ffmpeg") or not shutil.which("ffprobe"):
                self.skipTest("ffmpeg and ffprobe are required for sparse recording export")
            with tempfile.TemporaryDirectory() as tmp:
                attempt = Path(tmp)
                movie = attempt / "sparse.mp4"
                clip.subprocess.run([
                    "ffmpeg", "-v", "error", "-nostdin", "-n", "-f", "lavfi",
                    "-i", "color=c=red:s=64x64:r=30:d=10.1",
                    "-vf", r"select=eq(n\,0)+eq(n\,300)", "-fps_mode", "vfr",
                    "-c:v", "libx264", "-pix_fmt", "yuv420p", str(movie),
                ], check=True)
                original = clip.digest(movie)
                result = clip.export(attempt, movie, 3, 2, "before")
                self.assertEqual(clip.duration(result), 2)
                self.assertEqual(clip.digest(movie), original)
