#!/usr/bin/env python3
"""Fixture-only oracle for the chat traffic benchmark.

Derives, from the committed fixtures alone, the canonical state the daemon must
reproduce: every history row (the index of its turn, kind, role or activity
kind, status, sha256 of its text, output size, and sha256 of its tool payload
and whole output) and every remote-cut tool output (sha256 and size). It is an
independent re-implementation of the fixture rules in
backend/internal/chattraffic/fixture.go, so an expected state generated from
the product can be checked against something the product did not produce.

    python3 -I scripts/chat-traffic-oracle.py            # write oracle files
    python3 -I scripts/chat-traffic-oracle.py --check    # exit 1 if they are stale

Standard library only. Reads fixtures and the renderer's REMOTE_OUTPUT_LIMIT.
"""
import hashlib
import json
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
FIXTURES = ROOT / "backend/internal/chattraffic/testdata/fixtures"
EXPECTED = ROOT / "backend/internal/chattraffic/testdata/expected"
HOOK = ROOT / "frontend/src/renderer/hooks/useConversation.ts"


def sha(text):
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def size(text):
    return len(text.encode("utf-8"))


def canonical_number(value):
    """Go decodes every JSON number to a float64 and writes an integral one
    without a fraction, so 0.0 and 0 both become 0. The two encoders agree
    exactly only for integers within 2**53, so fixture payloads keep to those
    and anything else is refused rather than hashed differently."""
    if isinstance(value, float) and not value.is_integer():
        sys.exit("fixture payload number %r is not an integer; the canonical payload encoding covers integers only" % value)
    number = int(value)
    if abs(number) > 2**53:
        sys.exit("fixture payload number %d is outside +-2**53, where a float64 cannot hold it exactly" % number)
    return number


def canonical_value(value):
    if isinstance(value, bool) or value is None or isinstance(value, str):
        return value
    if isinstance(value, (int, float)):
        return canonical_number(value)
    if isinstance(value, list):
        return [canonical_value(v) for v in value]
    if isinstance(value, dict):
        return {k: canonical_value(v) for k, v in value.items()}
    sys.exit("fixture payload holds an unsupported value %r" % (value,))


def canonical_payload(detail):
    """An activity detail without output, outputBytes and text, as compact JSON
    with sorted keys: the bytes chattraffic.CanonicalPayload renders."""
    rest = {k: canonical_value(v) for k, v in (detail or {}).items() if k not in ("output", "outputBytes", "text")}
    if not rest:
        return ""
    return json.dumps(rest, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def self_check():
    """The Go side pins these bytes in TestCanonicalPayloadKeepsToolContentOnly."""
    detail = {"output": "x", "outputBytes": 9.0, "text": "thinking", "exitCode": 0.0,
              "files": [{"path": "a<b>&c.go", "patch": "@@ -1 +1 @@"}]}
    want = '{"exitCode":0,"files":[{"patch":"@@ -1 +1 @@","path":"a<b>&c.go"}]}'
    if canonical_payload(detail) != want:
        sys.exit("canonical_payload no longer renders the bytes Go renders: " + canonical_payload(detail))


def expand(template, number):
    def sub(value):
        return value.replace("{n}", str(number))

    streamed = {}
    steps = []
    for step in template["steps"]:
        item = sub(step.get("item", ""))
        count = max(step.get("repeat", 0), 1)
        deltas = [sub(step.get("delta", "")).replace("{r}", str(r)) for r in range(1, count + 1)]
        if step.get("join"):
            deltas = ["".join(deltas)]
        for delta in deltas:
            event = {
                "kind": step["kind"],
                "item": item,
                "delta": delta,
                "text": sub(step.get("text", "")).replace("{streamed}", streamed.get(item, "")),
                "summary": sub(step.get("summary", "")),
                "activityKind": step.get("activityKind", ""),
                "status": step.get("status", ""),
                "turnState": step.get("turnState", ""),
            }
            if "detail" in step:
                raw = sub(json.dumps(step["detail"], ensure_ascii=False))
                raw = raw.replace('"{streamed}"', json.dumps(streamed.get(item, ""), ensure_ascii=False))
                event["detail"] = json.loads(raw)
            if step["kind"].endswith(".delta"):
                streamed[item] = streamed.get(item, "") + delta
            steps.append(event)
    return {"user": sub(template["user"]), "steps": steps}


def turn_rows(turn):
    """Rows one turn leaves in the timeline, in first-appearance order."""
    rows = [("message", "user", turn["user"], 0)]
    items = {}
    order = []
    state = "missing"
    for step in turn["steps"]:
        if step["kind"] == "turn.completed":
            state = step["turnState"]
        if not step["item"]:
            continue
        if step["item"] not in items:
            items[step["item"]] = {"message": False, "text": "", "settled": "", "streamed": "", "detail": None}
            order.append(step["item"])
        it = items[step["item"]]
        kind = step["kind"]
        if kind == "message.delta":
            it["message"] = True
            it["text"] += step["delta"]
        elif kind == "message.completed":
            it["message"] = True
            if step["text"]:
                it["text"] = step["text"]
        elif kind == "reasoning.delta":
            it["settled"] += step["delta"]
        elif kind == "command.output.delta":
            it["streamed"] += step["delta"]
        elif kind in ("activity.started", "activity.completed"):
            it["kind"], it["status"], it["summary"] = step["activityKind"], step["status"], step["summary"]
            if step["text"]:
                it["settled"] = step["text"]
            if "detail" in step:
                it["detail"] = step["detail"]
    outputs = []
    for key in order:
        it = items[key]
        if it["message"]:
            rows.append(("message", "assistant", it["text"], 0))
            continue
        detail = dict(it["detail"] or {})
        if it["streamed"]:
            # Documented display rule (controllers.activityDetailPayload): streamed
            # command output wins over the provider's aggregate, and says so.
            detail.update(output=it["streamed"], outputSource="stream", outputMayBePartial=True)
            detail.pop("outputTruncated", None)
        text = it["summary"]
        if isinstance(detail.get("text"), str):
            text += "\x00" + detail["text"]
        elif it["settled"] and it["kind"] == "reasoning":
            text += "\x00" + it["settled"]
        output = detail["output"] if isinstance(detail.get("output"), str) else ""
        rows.append(("activity", it["kind"], text, size(output), it["status"], canonical_payload(detail), output))
        outputs.append((it["kind"], it["status"], output))
    return state, rows, outputs


def canonical(turn_states, rows):
    """Rows end with the index of their turn in turn_states."""
    lines = ["turn\t%s\n" % s for s in turn_states]
    for row in rows:
        if row[0] == "message":
            lines.append("message\t%d\t%s\t%s\t%d\n" % (row[-1], row[1], sha(row[2]), size(row[2])))
        else:
            lines.append("activity\t%d\t%s\t%s\t%s\t%d\t%s\t%s\n" % (row[-1], row[1], row[4], sha(row[2]), row[3], sha(row[5]), sha(row[6])))
    return "".join(lines)


def oracle(provider, output_limit):
    fixture = json.loads((FIXTURES / (provider + ".json")).read_text(encoding="utf-8"))
    templates = fixture["settledTemplates"]
    history = [expand(templates[(n - 1) % len(templates)], n) for n in range(1, fixture["settledTurns"] + 1)]
    reply = expand(fixture["replyTurn"], fixture["settledTurns"] + 1)

    files = {}
    states, rows, cut_states, cut_rows = [], [], [], []
    for index, turn in enumerate(history):
        state, turn_rows_, outputs = turn_rows(turn)
        states.append(state)
        rows.extend(row + (index,) for row in turn_rows_)
        cut = [o for o in outputs if size(o[2]) > output_limit]
        if cut:
            cut_states.append(state)
        for kind, status, output in cut:
            cut_rows.append(("activity", kind, output, size(output), status, "", "", len(cut_states) - 1))
    files["oracle-%s-history.txt" % provider] = canonical(states, rows)
    files["oracle-%s-expand.txt" % provider] = canonical(cut_states, cut_rows)
    state, rows, _ = turn_rows(reply)
    files["oracle-%s-reply.txt" % provider] = canonical([state], [row + (0,) for row in rows])
    return files


def main():
    self_check()
    match = re.findall(r"(?m)^const REMOTE_OUTPUT_LIMIT = ([0-9_]+);", HOOK.read_text(encoding="utf-8"))
    if len(match) != 1:
        sys.exit("REMOTE_OUTPUT_LIMIT must be declared exactly once")
    limit = int(match[0].replace("_", ""))
    files = {}
    for provider in ("claude", "codex"):
        files.update(oracle(provider, limit))
    stale = []
    for name, text in sorted(files.items()):
        path = EXPECTED / name
        if "--check" in sys.argv:
            if not path.exists() or path.read_text(encoding="utf-8") != text:
                stale.append(name)
            continue
        EXPECTED.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")
        counts = {k: text.count(k + "\t") for k in ("turn", "message", "activity")}
        print("%s: %d turns, %d messages, %d activities" % (name, counts["turn"], counts["message"], counts["activity"]))
    if stale:
        sys.exit("stale oracle files: " + ", ".join(stale))


if __name__ == "__main__":
    main()
