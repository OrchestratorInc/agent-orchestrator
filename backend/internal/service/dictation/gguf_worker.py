"""Private transcribe.cpp worker: local PCM in, cumulative transcripts out."""
import array
import base64
import json
import math
import sys

wire = sys.stdout
sys.stdout = sys.stderr


def emit(kind, **fields):
    wire.write(json.dumps(dict(type=kind, **fields)) + "\n")
    wire.flush()


try:
    import transcribe_cpp as speech

    model = speech.Model(sys.argv[1])
    vad_model = speech.Model(sys.argv[2], backend="cpu")
    vad = vad_model.vad_session(n_threads=1)

    class Recording:
        def __init__(self):
            vad.stream_reset()
            self.pending = array.array("f")
            self.preroll = array.array("f")
            self.segment = array.array("f")
            self.finals = []
            self.quiet = 0
            self.fed = 0
            self.session = None
            self.stream = None
            limit = model.capabilities.max_audio_ms
            self.limit = min(20000, limit if limit > 0 else 20000) * 16

        def text(self, partial=""):
            return " ".join(filter(None, [*self.finals, partial.strip()]))

        def batch(self, pcm):
            try:
                return self.session.run(pcm).text.strip()
            except (speech.OutputTruncated, speech.InputTooLong):
                if len(pcm) <= 4000:
                    raise
                middle = len(pcm) // 2
                return " ".join(filter(None, [self.batch(pcm[:middle]), self.batch(pcm[middle:])]))

        def decode(self, final=False):
            if not self.segment:
                return
            if self.session is None:
                self.session = model.session()
                if model.capabilities.supports_streaming:
                    self.stream = self.session.stream()
            if self.stream is not None:
                if len(self.segment) > self.fed:
                    self.stream.feed(self.segment[self.fed:])
                    self.fed = len(self.segment)
                if final:
                    self.stream.finalize()
                text = self.stream.text().full.strip()
            elif final:
                text = self.batch(self.segment)
            else:
                return
            if final:
                self.finals.append(text)
                if self.stream is not None:
                    self.stream.reset()
                self.session.close()
                self.session = self.stream = None
                self.segment = array.array("f")
                self.quiet = self.fed = 0
                emit("text", text=self.text())
            else:
                emit("text", text=self.text(text))

        def frame(self, frame, probability):
            voiced = probability >= 0.5
            if not self.segment:
                if not voiced:
                    self.preroll.extend(frame)
                    self.preroll = self.preroll[-3200:]
                    return
                self.segment = self.preroll
                self.preroll = array.array("f")
            self.segment.extend(frame)
            self.quiet = 0 if voiced else self.quiet + len(frame)
            if self.quiet >= 11200 or len(self.segment) >= self.limit - 512:
                self.decode(final=True)
            elif len(self.segment) - self.fed >= 8000:
                self.decode()

        def feed(self, block):
            self.pending.extend(block)
            while len(self.pending) >= 512:
                frame = self.pending[:512]
                del self.pending[:512]
                result = vad.stream_feed(frame)
                self.frame(frame, result.probs[-1])

        def finish(self):
            if self.pending:
                frame = self.pending + array.array("f", [0]) * (512 - len(self.pending))
                result = vad.stream_feed(frame)
                self.frame(frame, result.probs[-1])
            self.decode(final=True)
            return self.text()

    recording = None
    emit("ready")
    for line in sys.stdin:
        command = json.loads(line)
        kind = command["type"]
        if kind == "start":
            recording = Recording()
            emit("listening")
        elif kind == "audio" and recording is not None:
            block = array.array("f")
            block.frombytes(base64.b64decode(command["audio"], validate=True))
            if sys.byteorder != "little":
                block.byteswap()
            if not all(math.isfinite(value) for value in block):
                raise ValueError("Invalid microphone samples")
            recording.feed(block)
        elif kind == "stop" and recording is not None:
            emit("finishing")
            text = recording.finish()
            recording = None
            emit("end", text=text)
        elif kind == "cancel":
            if recording is not None:
                if recording.stream is not None:
                    recording.stream.reset()
                if recording.session is not None:
                    recording.session.close()
            recording = None
            emit("cancelled")
        else:
            raise ValueError("Invalid dictation command")
except Exception as error:
    emit("error", message=str(error))
    sys.exit(1)
