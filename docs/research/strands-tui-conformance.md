> Recovery: [registered AO TUI proof, real Electron captures and preserved diagnostic gaps](strands-tui-recovery-2026-10-11.md). The unregistered status below describes the original checkpoint.

# Strands CLI terminal conformance — blocked candidate

Strands is **not registered in AO**. The candidate adapter remains isolated in
`backend/internal/adapters/agent/strands`; there are no domain, registry,
activity-dispatch, migration, OpenAPI, Chat, reviewer, installer, or picker changes.
Native CLI success does not establish AO lifecycle support. AO spawn, restore,
kill, readiness, activity and UI screenshots remain **BLOCKED / NOT RUN**.

## Release and execution environment

- Official executable/package: `strands`, `@strands-agents/cli@0.2.0`.
- Upstream: [strands-agents/harness-sdk](https://github.com/strands-agents/harness-sdk),
  tag `harness-cli/v0.2.0`, commit `e519d67021b9c009e56e9309edcd94ab87e7156e`.
- Installed dependencies resolved to `@strands-agents/harness@0.1.1` and
  `@strands-agents/sdk@1.20.0`; the VPS installation lockfile is retained.
- Entrypoint SHA-256:
  `987e32867f74496f26075d4e5a59d1f7af291162650d2db73b277be7fbd949a0`.
- Linux VPS only, Node 24.21.0, isolated profiles and workspaces. Native setup
  retained default permissions and each tested shell/write approval was
  explicitly accepted once. Real account configuration was not changed.
- Authorized provider: Z.ai `glm-5.3-flash` through its existing coding endpoint.
  Strands' official `modelModule` configuration instantiated the shipped
  `OpenAIModel` with the exact authorized base URL. This was necessary because
  its `litellm` convenience builder unconditionally appends `/v1`. No provider
  source, native CLI source, or endpoint proxy was substituted. Credentials
  were passed only through the existing VPS credential wrapper and child
  environment; they are absent from source, argv, reports and profiles.

## Native result

| Gate | Evidence |
| --- | --- |
| Real TUI / initial task | PASS: native Ink TUI executed a task beginning `-Audit request` through `--prompt`. |
| Private standing instructions | PASS: provider wrote both the configured domain token and the separate AO instruction token. The visible task did not contain either value. |
| Exact session restore | PASS: the same `ao-native-001` persisted snapshot loaded after SIGKILL. The provider recovered the prior `INITIAL_001` token without reading files. |
| Fresh restore instructions | PASS: a fresh random token was generated only after the first process was killed and reaped, supplied through `--instructions`, and written to the restored proof. |
| Native activity / identity | PASS for tested root invocation: the injected supported SDK plugin emitted `session-start`, `active`, then `stop` for the pinned native session. Cancellation exposed the delayed stop described below. |
| Escape cancellation | **FAIL**: after accepting the shell command once and letting it start, Escape displayed “Cancelled” but the shell ran another **28.105 s** and created the delayed file. |
| Ctrl+C cancellation | **FAIL**: independent fresh run, same result in **28.090 s**. Both the shell and its sleep child were still alive five seconds after Ctrl+C. |
| Authentication/model catalog | Local configured model was exercised successfully. AO authorization and catalogs are NOT RUN; arbitrary model modules remain auth-unknown and cannot truthfully accept a `--model` override. |

The cancellation command was exactly:

```sh
sleep 30; printf done > CANCEL_SHOULD_NOT_EXIST
```

Each cancellation was sent approximately two seconds after accepting the native
permission prompt. The delayed file existed after both attempts. The native
`AfterInvocationEvent` arrived only after the shell finished. The instantaneous
“Cancelled” label was therefore not proof that execution or side effects stopped.

The shipped SDK's
[`makeShell`](https://github.com/strands-agents/harness-sdk/blob/e519d67021b9c009e56e9309edcd94ab87e7156e/strands-ts/src/vended-tools/shell/make-shell.ts)
passes `{ timeout }` to `sandbox.execute` without an abort signal. The CLI
workspace sandbox can terminate a process tree when it receives a signal, but
this call does not supply one. This is consistent with the observed live process
and delayed side effect. The previous CLI `0.1.4` tag
`4a42ded67431c45db51624391a4afa8faba3d454` has the same call and SDK/harness
version ranges; it was source-inspected only, not separately live-qualified.

## Actual native screenshots

These are live xterm captures from the separate `native-005` reproduction,
not AO screenshots. The run again created the forbidden delayed file.

At 10:56:28 UTC, five seconds after Escape, Strands shows both “Cancelled” and
“Interrupting” while its shell is still running:

![Native Strands cancellation still interrupting; AO not registered or tested](assets/strands/native-cancelled-tool-still-running.png)

At 10:56:56 UTC the composer has settled. The run's delayed artifact now exists
with contents `done`, despite the retained “Cancelled” label:

![Native Strands settled after the delayed side effect; AO not registered or tested](assets/strands/native-after-late-write.png)

The image SHA-256 values are respectively
`89dac2c9ff297f5f47e544ba21a72d183f21f583e29c2130fa0058aa76247cce` and
`4faad8828d8375c8737dfb8db94a3ce2b688fcf3325c7e54df440cb3279e8b67`.

## Candidate boundaries

The unregistered package tests the intended thin adapter boundaries:

- Use a separate visible `--prompt` and private `--instructions` argument.
  `--instructions` replaces saved `profile.instructions`, so concatenate the
  configured domain string with AO's launch instructions while preserving the
  harness's built-in contract. Restore resolves the current AO instructions again.
- Preserve the saved model and plugin configuration. Add one private native
  activity plugin under AO data. Do not write workspace rules or global config.
- Keep native session snapshots and launch-to-workspace identity under AO data.
  Reject missing/malformed history and workspace mismatches before constructing
  `--session-id`, since the native CLI otherwise creates missing IDs silently.
- Support Ask Permissions only with native default permissions and no saved
  always-allowed tools. A requested bypass can only match an explicitly already
  configured native bypass; AO never writes that setting. Accept-edits and auto
  have no qualified mapping. The live runs used Ask Permissions only.
- Reject authored agent projects and model overrides shadowed by native model
  modules. Do not invent a model catalog or report credential presence as a
  verified login. The generic audit runner's hardcoded bypass request cannot
  establish the default-permission AO workflow.

This is a candidate, not a completed integration. Native in-TUI session switching,
custom root agent IDs/session managers, provider/platform coverage, and AO
lifecycle delivery have not been qualified. Hook/locator handling needs to be
revalidated against those cases before any production registration. ACP, Chat,
reviewers and interface handoff are explicitly out of scope.

## Reproduce and evidence

The opt-in, standard-library-only
[`native_cancel_probe.py`](../../backend/internal/adapters/agent/strands/testdata/native_cancel_probe.py)
starts the real pinned CLI in a PTY, approves the exact disposable shell command
once, sends the chosen native cancellation key, records the owned process tree,
and detects the delayed artifact. It never runs in the default unit suite.
Use an already prepared disposable native profile and the authorized credentials
in the execution host's environment:

```sh
python3 backend/internal/adapters/agent/strands/testdata/native_cancel_probe.py \
  --binary /absolute/path/to/strands \
  --home /absolute/path/to/disposable-profile \
  --report /absolute/path/to/new-report-directory \
  --interrupt escape
# Repeat with a fresh report directory and --interrupt ctrl-c.
```

VPS evidence root:
`/home/azureuser/.ao/audits/harness-watch-20261010/reports/strands/`.

- `native-001`: leading-dash/private/domain proof and the first restore-runner
  failure. Its readiness check ran only on incoming bytes and missed a quiescent
  ready screen. That failure is preserved.
- `native-002`: exact native restore and fresh post-kill instruction proof. The
  functional turn passed, while the runner's post-proof exit condition also
  waited on terminal bytes and reached its 150-second cleanup timeout. No full
  strict audit PASS is claimed.
- `native-003`: first real Escape failure, native hook events, permission and
  active terminal captures, delayed artifact.
- `native-004`: independent Ctrl+C failure, same owned shell and sleep PIDs
  before and five seconds after cancellation, delayed artifact.
- `native-005`: separate actual Xvfb/xterm capture run. Images are live native
  terminal captures, clearly labeled **AO NOT REGISTERED / NOT TESTED**. They
  are neither original-run screenshots nor AO screenshots. Capture metadata and
  cleanup status are in its `result.json`.

Audit tokens are nonsecret fixtures. The model echoed them in its final answer;
instruction consumption is proved, confidentiality against model echo is not.
Original failures, later controls, and screenshot reproduction are separate.

## Validation

The candidate's unit tests, race test and `go vet` passed; `go build ./...` passed.
Focused golangci-lint 2.13.2 validation is recorded separately in
`final-validation-003.log` after the review's malformed-message-history fix.
The full `go test ./...` run was executed on the VPS and failed in existing
packages outside this unregistered candidate. The complete log is
`backend-validation-001.log`; it includes e2e startup retry, fake hook-file,
OpenCode v2 binary resolution, integration Codex reconciliation and service/agent
private-file ancestor failures. These have not been labeled passing or fixed by
this candidate. Full-repository race/lint, frontend/native-OS workflows and
remote CI remain outstanding. No API generation is required because no API or
registration source was changed.

All probe-owned processes were terminated and native evidence retained. Do not
register Strands until a pinned upstream release or a separately reviewed,
existing containment boundary proves bounded cancellation without late side
effects, followed by independent AO lifecycle and real AO UI evidence.
