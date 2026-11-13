"""AO's private, version-pinned Phonon bridge. Audio is never written to disk."""
import base64
import json
import sys

wire = sys.stdout
sys.stdout = sys.stderr


def emit(kind, **fields):
    wire.write(json.dumps(dict(type=kind, **fields)) + "\n")
    wire.flush()


try:
    import numpy as np
    import fermion
    from fermion._speech.live import LiveSession, warm_up

    speech = fermion.load_speech(sys.argv[1], quiet=True)
    warm_up(speech)
    session = None
    emit("ready")
    for line in sys.stdin:
        command = json.loads(line)
        kind = command["type"]
        if kind == "start":
            session = LiveSession(
                speech,
                on_partial=lambda text: emit("text", text=" ".join(filter(None, [session.transcript, text]))),
                on_final=lambda text: emit("text", text=session.transcript),
            )
            emit("listening")
        elif kind == "audio" and session is not None:
            block = np.frombuffer(base64.b64decode(command["audio"], validate=True), dtype="<f4")
            if not np.isfinite(block).all():
                raise ValueError("Invalid microphone samples")
            session.feed(block)
        elif kind == "stop" and session is not None:
            emit("finishing")
            text = session.finish()
            session = None
            emit("end", text=text)
        elif kind == "cancel":
            session = None
            emit("cancelled")
        else:
            raise ValueError("Invalid dictation command")
except Exception as error:
    emit("error", message=str(error))
    sys.exit(1)
