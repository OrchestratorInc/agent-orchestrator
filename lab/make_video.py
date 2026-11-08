"""Side-by-side before/after video aligned on the Steer click.

Usage: python3 make_video.py <runsDir> <beforeRun> <afterRun> <out.mp4> <slowdown> <fromMs> <toMs> <title>
Each run's screencast frames carry timestamps relative to the click; every output
tick shows the newest frame captured at or before that moment.
"""
import json
import subprocess
import sys
import tempfile
from pathlib import Path

FFMPEG = "/opt/homebrew/opt/ffmpeg/bin/ffmpeg"
FONT = "/System/Library/Fonts/Helvetica.ttc"
FPS = 30

runs, before, after, out, slowdown, start, end, title = sys.argv[1:9]
slowdown, start, end = float(slowdown), int(start), int(end)
work = Path(tempfile.mkdtemp(prefix="steer-video-"))


def summary(run):
    return json.loads((Path(runs) / run / "summary.json").read_text())


def frame_at(frames, ms):
    shown = [f for f in frames if f["t"] <= ms]
    return shown[-1] if shown else frames[0]


def state_at(samples, ms):
    shown = [s for s in samples if s["t"] <= ms]
    return shown[-1] if shown else None


def render(run, label):
    s = summary(run)
    frames = s["frames"]
    step = 1000 / FPS / slowdown  # capture-time ms per output frame
    listing = []
    ms = start
    while ms <= end:
        f = frame_at(frames, ms)
        st = state_at(s["samples"], ms)
        if ms < 0:
            caption = "before click"
        elif st and st["dock"] and st["rows"] == 0:
            caption = "EMPTY DOCK - message shown nowhere"
        elif st and st["inTimeline"]:
            caption = "message shown as a steer in the timeline"
        else:
            caption = ""
        listing.append((Path(runs) / run / "frames" / f["file"], ms, caption))
        ms += step
    seq = work / run
    seq.mkdir()
    for i, (src, ms, caption) in enumerate(listing):
        text = f"{label}   t = {ms:+.0f} ms   {caption}".replace(":", r"\:")
        subprocess.run([
            FFMPEG, "-loglevel", "error", "-y", "-i", str(src),
            "-vf", f"scale=960:-2,drawbox=x=0:y=ih-44:w=iw:h=44:color=black@0.75:t=fill,"
                   f"drawtext=fontfile={FONT}:text='{text}':x=14:y=h-31:fontsize=20:fontcolor="
                   + ("0xff6b6b" if "EMPTY" in caption else "white"),
            str(seq / f"{i:05d}.png"),
        ], check=True)
    return seq


b = render(before, f"BEFORE ({before})")
a = render(after, f"AFTER ({after})")
subprocess.run([
    FFMPEG, "-loglevel", "error", "-y",
    "-framerate", str(FPS), "-i", str(b / "%05d.png"),
    "-framerate", str(FPS), "-i", str(a / "%05d.png"),
    "-filter_complex", f"[0][1]hstack=inputs=2,pad=iw:ih+56:0:56:color=0x111111,"
    f"drawtext=fontfile={FONT}:text='{title}':x=16:y=18:fontsize=24:fontcolor=white",
    "-c:v", "libx264", "-pix_fmt", "yuv420p", "-crf", "20", out,
], check=True)
print(out)
