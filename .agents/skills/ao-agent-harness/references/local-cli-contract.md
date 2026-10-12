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
requires the cancellation turn's exact foreground Node command to create a
tokenized start witness containing its PID and platform kernel process-start
identity while AO still reports the turn
active. It then opens the exact `terminalHandleId` as a secondary WebSocket
attachment at `/mux`, checks the terminal generation, reconfirms that witnessed
PID still has the same process-start identity, and sends the input without
resizing. A mux pong proves transport
processing only. The same live session/terminal must settle, the witnessed PID
and process-start identity must stop, and the command's delayed side-effect file must remain absent past
its scheduled write time. Natural settlement, command refusal, or a surviving
child process therefore cannot pass cancellation. There is no fabricated HTTP
interrupt endpoint. With no interrupt contract, cancellation is `NOT_RUN` and
the cancellation turn is not started.

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


### Optional post-cancellation draft cleanup

Some native CLIs restore the cancelled prompt into their composer. For that
documented behavior only, an explicit `interrupt.postCancelCleanup` may clear
the runner's just-submitted cancellation draft before lifecycle kill:

```json
{
  "input": "\u0003",
  "draftPattern": "(?:^|\\n)> {prompt}\\n[-]+\\n[ \\t]*model: example-model \\| workspace: [^\\n]+\\n*$"
}
```

`input` must be exactly one Ctrl+C. `draftPattern` must be at most 4,096
characters and include `{prompt}`, which the runner substitutes as escaped
literal regex text for its exact cancellation prompt. The actual matched span
must contain that full prompt; an alternative branch without it cannot authorize input. Optional `flags` accepts
`i`, `m`, `s`, or `u`. Derive a contiguous current-composer cue from native
samples, including the native draft boundary and model/workspace footer. For
cleanup, both draft and empty cues must match the absolute output tail, even
with multiline flags. Test against the audit draft and historical audit text
followed by a different current draft. A prompt anywhere in scrollback or
“Long draft” alone does not establish ownership.

This option applies only to a newly runner-created, exclusively controlled audit
session and its own cancellation turn. Do not use it on user/product sessions
or while anyone else can edit the composer. The runner requires independently
successful cancellation, the original live session/terminal generation, and a
settled `idle` or `waiting_input` state. It requires a nonempty observed `lastUserMessageAt` and rejects any change.
Missing/null metadata blocks cleanup. The timestamp does not identify unsent
human edits, so exclusive ownership and the current-composer cue are also required.

The existing `restoredReady.patterns` remains the empty-composer contract. If it
already matches, cleanup sends nothing. Otherwise the runner waits for the
owned-draft cue, revalidates identity, generation, settled state, latest user
turn, and current composer immediately before sending one Ctrl+C through the
exact secondary mux attachment. It discards earlier output, then requires new
nonempty terminal output matching the empty-composer cue and the same settled
identity. Empty-output patterns are rejected. It never retries Ctrl+C:
a second press can exit the native CLI. The mux wire has no generation argument;
API revalidation and the exact attachment provide the available fence.

If native draft recovery is asynchronous, add a metadata-only durability witness
under `postCancelCleanup`:

```json
"persistence": {
  "path": "agents/example/{sessionId}/drafts/{workspaceSha256:24}/{nativeSessionIdSha256:24}.json"
}
```

The path is relative to the AO data directory and must contain all three
placeholders. Harness-specific directories belong in this contract. Workspace
hashing uses SHA-256 of Node `path.resolve(workspacePath)`, without resolving
symlinks; native-ID hashing uses the positively observed native ID after
`trim()`. Both hashes use the first 24 lowercase hexadecimal characters. There
is no fallback identity. Paths are bounded to 1,024 characters and 32 components;
absolute paths, traversal, unknown placeholders, symlink ancestors or leaves,
and nonregular files are rejected. The runner uses `lstat` only, never reads
recovery contents, and does not treat missing parent directories as verified
leaf absence.

For a nonempty owned composer, the exact regular file must first be present.
After the one clear and fresh empty UI, that same path must become absent before
lifecycle kill. An already-empty composer requires absence and receives no
input. Both waits share the existing cleanup deadline (at most 30 seconds),
and session/generation/latest-user metadata and the current composer are checked
again after waiting. A failed metadata check or timeout blocks kill/restore.
Evidence records the relative path and ordered `beforeInput`/`afterEmpty` states
with timestamps, without file contents. Omitting `persistence` retains the
UI-only contract; enable it whenever an empty editor is not a native durability
barrier.

The separate `post_cancel_draft_cleanup` gate preserves the cancellation result.
Unsafe cleanup, missing cues, or a remaining draft block lifecycle kill/restore
and leave dependent gates `NOT_RUN`; normal final teardown may still terminate
the disposable session. With this option absent, behavior is unchanged. Never
weaken restored readiness to accept a draft, rewrite a failed attempt, or clear
a different user's content to obtain a pass.

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


## CodeBuddy Code native TUI qualification

Pin the released npm package and launcher hash; 2.164.0 was qualified on Ubuntu.
Use a positional task after `--` and actual bounded additive text in
`--append-system-prompt`; a request to read an instruction file is not equivalent.
`--resume ID` needs explicit durable ID/cwd validation before launch because AO's
`ok=false` restore path can otherwise replay the initial task fresh. Respect the
effective runtime `CODEBUDDY_CONFIG_DIR`, not just the daemon environment.

Preserve native manual permissions. Supported foreground-only environment controls
are `CODEBUDDY_CODE_DISABLE_BACKGROUND_TASKS=1`, `CODEBUDDY_DISABLE_CRON=1` and
`DISABLE_AUTOUPDATER=1`; these do not authorize bypass permissions. Only default
permissions, TUI and bounded histories are qualified; ACP declarations are not
provider conformance. Native model selection is freeform and credential presence
is configured, not a general authorization claim.

The native bundled Ink renderer emits static output with `CI=1` or
`CONTINUOUS_INTEGRATION` enabled. A desktop proof launcher must use an interactive
environment (the final isolated lab used `CI=false`). Retain static-output failures
and never authorize input from a stale ring tail. Native suggested prompts also
look nonempty; conservatively refuse AO pastes until the native surface is empty.
If disabling suggestions for an owned audit, merge the supported native
`promptSuggestionEnabled=false` setting rather than modifying upstream code.

One Escape cancelled the observed foreground shell. A second Escape can open native
Rewind; observe the current screen before any following input, and dismiss only an
owned audit dialog. Do not equate HTTP kill acknowledgement or an omitted optional
`freed` field with kernel cleanup: corroborate host/supervisor/native and child PID
plus kernel start, unrelated control ownership, and delayed side-effect absence.
See the [CodeBuddy report](../../../../docs/research/codebuddy-tui-2026-10-11.md) for
separate earlier failures, later controls, exact UI/daemon capture provenance and CI.


## Stakpak native qualification hold

Released Stakpak0.3.88 accepts a positional task without submitting it in the
interactive TUI. An eventual adapter must use current-viewport readiness and
serialized after-start input. `--system-prompt-file` is additive on begin, but
its system message persists in checkpoints: applying renewed instructions on
`--session UUID` added a second system message in live proof. Do not count a
fresh-token response as instruction deduplication.

Native Escape settled the composer while an executing `run_command` shell and
sleep descendant survived and wrote their delayed marker. Record PID plus kernel
start and the complete delayed-effect window; settled TUI does not prove reaping.
Native `cancel_task` targets background tasks and cannot qualify foreground
cancellation. Do not narrow the probe to a single-process command, monkeypatch
upstream, or edit native history to erase these failures. Keep Stakpak
unregistered pending a released native fix, then requalify actual AO. ACP load
flags alone still do not prove history replay. See the
[qualification hold](../../../../docs/research/stakpak-tui-hold-2026-10-11.md).


### Stakpak recovery source boundary

Release0.3.88 and upstream main were rechecked in recovery; foreground executor
ownership is unchanged. `--tool` filters advertised definitions, while dispatch
retains the full native tool list; empty selection means no filtering. Reserved
native MCP servers cannot be replaced. Do not claim an AO command broker enforces
exclusion based on those controls. ACP uses the same command executor. Deny-all
permissions cannot qualify a workspace-edit coding surface.

Native cancellation notifications return before process cleanup acknowledgement.
A repair must own jobs independently of cancellable stream futures, cover both
cancel branches/timeout/drop/disconnect, fence process identities and acknowledge
bounded reaping before admitting the next turn. Merely sending group kill is not
a complete drop/PID-reuse repair. AO Kill terminates the session and cannot stand
in for same-session turn cancellation. Fresh-only scope removes restore claims
but retains this ownership prerequisite. Keep prior failures; do not rerun the
same Escape probe without a changed source-grounded hypothesis.

See the [source patch and regression proposal](../../../../docs/research/stakpak-recovery-proposal-2026-10-12.md).
It is a proposal, not an installed patch or a passing stock-native proof.


### Stakpak native foreground source fix qualification

Native source PR767 now contains a small owned-child Unix group fix, real executor regressions and ECHILD signal disarming; this is source implementation, not a proposal or repaired stock release. Keep source/artifact hashes and original stock failures separate. The exact rmcp cancellation handler retains the request future on ordinary token cancellation; a synchronous child-owning Drop guard is sufficient for aborted execution futures without a generic job framework. Do not retain armed numeric PGIDs after wait/try_wait or a loss-of-ownership error, and avoid deferred direct kills after ECHILD. Cancellation-safe line reads and draining both pipes matter for reliable subsequent output.

Source62bd883's pinned CI artifact passed native foreground Escape and same-session continuation in an actual AO Electron standalone shell; this does not register or qualify an AO harness. Full hosted workspace CI remained unavailable/cancelled, so the fix remains draft. The standalone-shell runtime-close control is not agent-session Kill; unmanaged shells have no agent launch generation. A forced renderer reload earlier caused OSC11 reply text in the composer; retain that failed control and never blindly clear permission/draft input. A fresh terminal is a separate control. Default secret scanning can classify harmless assignment-shaped audit text; prefer plain token wording rather than disabling native security. See [curated source-fix evidence](../../../../docs/research/stakpak-native-fix-2026-10-12.md).


## Freebuff creation snapshots and native source qualification

[Native PR1505](https://github.com/CodebuffAI/freebuff/pull/1505) adds an explicit
private file input on public source; it does not repair stock0.2.26. A creation
snapshot persisted in native history avoids false claims that deleting or
replacing one knowledge-map entry refreshes cached system prompts/compaction.
Omission preserves the snapshot; changed/retrofitted input must fail before
inference. Constructor/run options already spread through CodebuffClient.
Test actual provider-request composition, not only the file loader. Preserve
project/home/native rules, fail closed on bounded Unicode/file validation and
sanitize filesystem errors without mislabelling them as authentication.
Normal saved history/traces retain instructions; do not promise secrecy.

Attach the actual AO UI primary before starting the native TUI so native terminal
color detection sees the correct surface. A secondary-before-primary empty
startup is a separate control; never force renderer reload or clear arbitrary
composer text to make evidence pass. Send task text and Enter separately after
verified readiness; grouped paste can remain a draft. Arm an execution watcher
before submission, record PID/kernel start, and keep the original missed-window
late artifact even when a later identically timed control passes. Standalone
terminal close is not registered agent-session Kill or restore. See the
[curated prerelease report](../../../../docs/research/freebuff-native-fix-2026-10-12.md).
