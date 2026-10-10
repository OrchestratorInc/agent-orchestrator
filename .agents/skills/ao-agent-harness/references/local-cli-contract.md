# Local CLI probe contract

Pass `--local-contract PATH` to run direct provider checks while the isolated AO
daemon is active. The file is runtime evidence configuration and must not
contain credentials. Keep provider-specific commands outside the generic
runner.

```json
{
  "schemaVersion": 1,
  "agents": {
    "example-agent": {
      "binary": "example-agent",
      "versionArgs": ["--version"],
      "integration": {
        "args": ["doctor", "--json"],
        "expect": "ready"
      },
      "session": {
        "args": ["run", "{prompt}"],
        "proofFile": "LOCAL_AUDIT_PROOF.json"
      },
      "models": {
        "args": ["models", "list", "--json"],
        "format": "json"
      },
      "aoModelsPath": "agents/{agent}/models",
      "interrupt": {
        "input": "\u001b",
        "settleTimeoutMs": 5000
      },
      "restoredReady": {
        "patterns": ["(?:^|\\n)>[ \\t]*\\n[-]+\\n[ \\t]*model: example-model \\| workspace: [^\\n]+\\n*$"]
      }
    }
  }
}
```

## Fields

- `binary` is an executable name resolved through `PATH`, or an absolute path.
- `versionArgs` must return exit code zero and non-empty output.
- `integration.args` runs the provider's safe local doctor/status/API probe.
  When `expect` is present, that literal must occur in stdout or stderr.
- `session.args` must select a bounded, non-interactive agent mode. `{prompt}`
  is replaced in place; when omitted, the prompt is appended. The agent must
  create `proofFile` containing `{"token":"<token from prompt>","cwd":"<cwd>"}`.
  `proofFile` must be a relative path contained by the disposable workspace.
- `models.args` invokes the provider's model-list command. Use `format: "json"`
  for a JSON array or an object containing `models`, `data`, `items`, or
  `options`; otherwise each non-empty output line is treated as one ID.
- `aoModelsPath` is relative to `/api/v1/` and supports `{agent}` substitution.
  It defaults to `agents/{agent}/models`. The resolved route must stay under
  `agents/<selected-agent>/`, contain a `models` segment, and contain no query,
  fragment, or dot segments.

Each selected agent needs its own entry for a complete result. Missing commands
are `NOT_RUN`; failed commands or empty model catalogs are `FAIL`; a missing
binary is `BLOCKED`. Local and AO model catalogs pass the consistency gate when
they share at least one exact model ID. Commands have bounded output and
timeouts with bounded process-group termination on POSIX (child-only fallback
on Windows). Reports retain sanitized command
metadata and hashes. Failed probes also retain bounded redacted stdout/stderr
for diagnosis. Explicit login errors request user help and skip the dependent
local session; see [evidence-and-handoff.md](evidence-and-handoff.md).

`--no-live` skips `session.args` and marks `local_session_spawn` `NOT_RUN` while
still running the safe binary, version, integration, and model-list checks.


## Cancellation and restored input

These optional fields configure native behavior; they are required to execute
the corresponding lifecycle gates. Derive values from the pinned native CLI,
not this illustrative `example-agent` contract.

`interrupt.input` is 1–64 UTF-8 bytes, such as `"\u001b"` for Escape or
`"\u0003"` for Ctrl-C when documented by the native agent. The runner first
observes an active AO session, opens its exact `terminalHandleId` as a secondary
WebSocket attachment at `/mux`, checks the terminal generation, and sends the
input without resizing. A mux pong proves transport processing only. The same
live session/terminal must then settle. There is no fabricated HTTP interrupt
endpoint. With no interrupt contract, cancellation is `NOT_RUN` and the long
cancellation turn is not started.

`interrupt.settleTimeoutMs` defaults to 5,000 ms and accepts an integer from
1 through 60,000, bounded by the run's timeout. Set a longer window only when
the measured AO observation cadence warrants it. The report preserves actual
settle latency and whether the default five-second window was exceeded; do not
present a 30-second AO observation as instantaneous native cancellation.

`restoredReady.patterns` contains 1–8 regular expressions, each at most 4,096
characters. `flags` may contain `i`, `m`, `s`, or `u`. All patterns must match
bounded terminal output after ANSI stripping and streaming UTF-8 decoding.
Matching is not terminal-screen emulation: design one contiguous, end-anchored
cue for the initialized empty composer plus its configured model/workspace
footer. A collection of independent words can match stale or unrelated frames.
Check the pattern against genuine startup, settled, pasted-draft, and restored
samples. A line such as `model: loading` or `Resuming session…` is provisional;
API `statusReadiness: ready` or idle alone is insufficient.

The runner attaches without sending input, then reconfirms session ID, terminal
handle/generation, liveness, and a settled state before sending continuation.
If the contract is absent, readiness and the three dependent continuity gates
are `NOT_RUN`. If the cue times out or the terminal changes, readiness is
`BLOCKED` and no continuation is sent. This tests observed input readiness;
it does not prove that an earlier provisional screen would accept a prompt.

## Profile and model preparation

Keep credentials in the execution account's existing environment or native
credential store. A host-owned launcher may populate the process environment
without printing secrets. Do not embed keys in the contract, argv, captured
logs, or committed fixtures. Native profiles/trust must cover only disposable
audit workspaces; account login and permission bypass are not implied by running
the skill.

For `--ao`, use an actual `ao`/`ao.exe` binary. A symlink to a differently named
executable may still resolve that basename through `os.Executable()`, breaking
native hook lookup. Use a canonical copy or hardlink and record its hash.

A providers list is not a model catalog. Do not parse provider rows as model
IDs. If upstream offers only free-form model selection, omit unsupported model
commands: `local_models_list` and model comparison remain `NOT_RUN`. An empty
AO catalog remains `FAIL` in this strict catalog-based audit, even if the
integration truthfully supports free-form models.
