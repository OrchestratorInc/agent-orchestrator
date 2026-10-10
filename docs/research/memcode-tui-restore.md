> Recovery: [actual registered AO TUI restore, decision protection, executing-shell controls and real Electron proof](memcode-tui-recovery-2026-10-11.md). Original native-only blockers below remain preserved.

# memcode TUI: historical mount-time restore failure

This section records the original unregistered candidate. Recovery now uses the supported interactive `/resume` route through strict AO initialization; the original mount-time failure below remains unchanged evidence.

At the original checkpoint, memcode was **not registered or tested inside AO**. The official Linux x86-64
v0.38.1 binary starts its native TUI, but an exact native resume exits 2 with
`panic: ui: AppendString called without primary screen support`. A new native
reproduction with a valid synthetic two-message transcript reproduces this
without credentials or provider turns. There is no AO screenshot or passing
AO integration claim.

![Native memcode resume failure; AO not registered or tested](memcode-native-resume-failure.png)

This is a real `scrot` capture of an xterm attached read-only to the retained
terminal pane from the new reproduction, captured on 2026-10-10 at 10:43 UTC.
It shows the native process's panic after exit, not AO's desktop, a historical
log rendered as an image, or the original earlier provider-backed attempt.

## Reproduce

Use an independently installed official **memcode v0.38.1**, Python 3, Git, and
tmux on Linux. The script creates an isolated home and Git workspace under the
requested report directory, does not inherit provider credentials, submits no
model turn, and stops its own tmux server. It disables self-update and seeds
the native update-notice cache. Endpoint overrides point at loopback port 9.
It does not patch the native binary.

~~~sh
python3 test/harness/memcode_conformance.py \
  --binary /absolute/path/to/memcode \
  --report "$HOME/.ao/audits/memcode-restore-attempt-001" \
  --screenshots
~~~

`--screenshots` additionally needs Xvfb, xterm, and scrot. Missing capture tools
are reported as unavailable. Every report directory must be new. Startup is
bounded by `--timeout` (default 30 seconds per launch, maximum 120). The expected
v0.38.1 result is exit 1 with `NATIVE_RESTORE_GATE_FAIL`: fresh native readiness
passes, exact resume fails with process exit 2, and the saved transcript hash
is unchanged. A future passing result proves only this native fixture's
startup/restore gate, not provider continuity, permissions, hidden instructions,
or AO conformance.

When sharing a host, use a lock without passing its descriptor to detached
terminal processes:

~~~sh
flock --close "$HOME/.ao/audits/compute.lock" \
  python3 test/harness/memcode_conformance.py \
  --binary /absolute/path/to/memcode \
  --report "$HOME/.ao/audits/memcode-restore-attempt-002"
~~~

The credential-free fixture tests do not need an installed memcode:

~~~sh
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover \
  -s test/harness -p 'test_memcode_conformance.py'
~~~

## Pinned source and failure boundary

| Input | Value |
| --- | --- |
| Upstream | [memcode-ai/memcode](https://github.com/memcode-ai/memcode) |
| Released source | [v0.38.1 / afc80cf1718e](https://github.com/memcode-ai/memcode/tree/afc80cf1718eaaf2b5abd9c6a0b753ba63f3d186) |
| Executed platform | Linux x86-64 |
| Official archive SHA256 | `6f06f155922c592f6bbd4f642e0404299772f3fbd6bae335358309604a0bf544` |
| Executable SHA256 | `89cd4375fce660d6c365163745ef83d78e76ad36e803fe4cebb952644fe4ddff` |
| Screenshot SHA256 | `c14da3a27c990a3271f5e3784355f9bbfd20acf2418624ae92c7e339e4d65327` |

The archive matches the release's published checksums.
[StartChat](https://github.com/memcode-ai/memcode/blob/afc80cf1718eaaf2b5abd9c6a0b753ba63f3d186/internal/agent/runtime/chat.go#L163)
prints a resumed-session banner during
[InitState](https://github.com/memcode-ai/memcode/blob/afc80cf1718eaaf2b5abd9c6a0b753ba63f3d186/internal/vxui/app.go#L319).
Its already-installed output callback calls `EventContext.AppendString`.
[NewApp](https://github.com/memcode-ai/memcode/blob/afc80cf1718eaaf2b5abd9c6a0b753ba63f3d186/internal/forks/vaxis/ui/app.go#L68)
still has the panic placeholder while mounting the widget tree, before backend
scrollback appenders attach. A session-start hook can report the correct native
ID before this panic, so that hook is not input-readiness evidence.

Source inspection of official v0.38.0, v0.37.0, v0.36.0, v0.31.0, v0.29.0, and
v0.20.0 found the same synchronous startup chain, banner, panic placeholder, and
session-start hooks. No practical earlier release was identified as a safe pin.
**Only v0.38.1 was executed**; those releases received source inspection, not
binary conformance verdicts.

## Evidence and limits

These separate attempts ran on the authorized VPS. Original reports and captures
remain in their individual audit directories; later controls replace no failures.

| Attempt | Observation | Classification |
| --- | --- | --- |
| `memcode-native-001` | Composer accepted a leading-dash task; a hook-only token was written to `proof.txt`; active → idle and matching native transcript ID observed. Escape interrupted a subsequent active turn. Restore then exited before capture. | Several native gates passed; restore failed. Not an AO audit. |
| `memcode-native-restore-control-001` | Retained terminal reproduced exact `--resume` exit 2 and initialization panic on the real saved transcript. | Confirmed upstream native failure. |
| `memcode-native-slash-resume-control-001` | After fresh readiness, `/resume <full-id>` recovered the exact ID; a continuation appended a token introduced only after kill. Report then recorded `Broken pipe`. | Individual native gates passed; original overall report remains non-pass. |
| `memcode-committed-gate-001` | Initial fixture omitted required `models.coder` and native startup rejected its configuration. Screenshot provenance mistakenly called this a panic; a separate correction accompanies the preserved report. | Reproducer setup failure, not additional upstream panic evidence. |
| `memcode-committed-gate-002` | Corrected credential-free fixture: fresh TUI ready, exact resume exit 2, unchanged transcript, actual native screenshot above. | Repeatable native restore blocker. |

The slash-command control saved `fresh_restore_token: true` before its progress
`print(..., flush=True)` raised `BrokenPipeError` after transport stdout closed.
The terminal independently shows the appended token and settled composer. This
runner output failure does not erase those gates or make the original report pass.

Cancellation was narrower: the active turn stopped, but the resumed agent later
finished the earlier `sleep 60` / `cancelled.txt` request before applying the
continuation. The audit did not prove cancellation of an already-running tool
or removal of cancelled intent from model history.

## Integration decision

A fresh TUI followed by `/resume <full-id>` requires separate startup readiness,
native load, exact identity validation, and restored readiness. AO's current
native restore path treats a successful `GetRestoreCommand` as an in-command
resume and skips `BuildAfterStartPrompt`. Existing adapter boundaries cannot
safely express this two-stage workaround. A fresh TUI must not be labeled as
restored. This investigation adds no adapter or shared lifecycle workaround.

Other future integration gates remain:

- Taskless `run` opens the TUI; positional or piped text selects one-shot mode.
  Initial tasks require verified initialized composer readiness.
- TUI dispatch ignores `--model`. Endpoint selection must account for remembered
  `LastModel` taking precedence over `MEMCODE_ENDPOINT_MODEL`.
- Session-start stdout appends standing context but is capped at 8192 bytes per
  hook. Preserve user hooks/default instructions and reject or safely split
  oversized instructions. Installation is not instruction-consumption proof.
- Missing, corrupt, or empty history may silently start fresh. Validate the full
  ID and matching nonempty transcript, then verify restored identity.
- `--ask`, `--auto`, and `--allow-all` differ; the last still asks about
  catastrophic commands. AO accept-edits behavior is unsupported.
- Hooks lack prompt-submit, turn-settled, and permission-pending events.
  Terminal chrome and durable transcript boundaries need independent validation.
- Credential presence remains configured, not verified authorization. The
  successful native Z.ai turn is not evidence of an AO auth probe.
- Chat, reviewers, AO lifecycle/UI behavior, and AO screenshots remain untested.
