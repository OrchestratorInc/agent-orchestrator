# Reasonix harness

AO can supervise [Reasonix](https://github.com/esengine/DeepSeek-Reasonix) in
Terminal UI worker and orchestrator sessions. Choose **Reasonix** with
**Terminal UI** and enter an optional model ID directly. Leaving the model blank
keeps Reasonix's configured default; AO does not maintain a model list.

## CLI compatibility

AO requires a Reasonix CLI that exposes `--append-system-prompt-file` and `--resume-exact`. AO uses
this process-scoped flag to add its standing instructions as system-role content
on both fresh launch and exact restore. The exact-resume flag selects the saved canonical identity directly, without
filename precedence, fuzzy search, or the recent-session catalog. AO rejects binaries
that lack either flag
with an actionable compatibility error.

**The current official v1.39.2 release is not compatible.** The prerequisite is
[Reasonix PR #11059](https://github.com/esengine/DeepSeek-Reasonix/pull/11059).
Until that capability ships in a verified official release, use a source build
containing the prerequisite change. There is no advertised minimum compatible
release yet. Installing an official package by itself does not establish AO
compatibility.

The tested source build is
[`9790b1c`](https://github.com/nikhilachale/DeepSeek-Reasonix/commit/9790b1c53d6d68919455b237bc0f03cfdf3b7dac),
built with Go 1.26.6 on macOS arm64 as `reasonix v1.39.2-ao.9790b1c`.
Its executable SHA-256 is
`8dc294d899121f317f6b24b8116d8439c99782e9d565407d5e77ee830de1cdb3`.
This is source-build evidence, not an official release checksum.

The opt-in AO test uses a local fake provider, a committed Git workspace, and a
private tmux server. It verifies ready-composer detection, leading-dash multiline
Unicode tasks, system-role guidance on launch and restore, native observer hooks,
exact identity despite a same-named workspace file, and cancellation cleanup:

```bash
cd backend
AO_LIVE_REASONIX=1 AO_REASONIX_TEST_BINARY=/absolute/path/to/reasonix \
  go test ./internal/adapters/agent/reasonix -run TestReasonixLiveAOConformance -count=1 -v
```

External provider authentication and native Windows/Linux runtime execution are
not covered by this macOS test.

## Install and configure

Install AO through the [desktop releases](https://github.com/aoagents/agent-orchestrator/releases).
Install Reasonix separately using the official channel for your system:

```bash
# macOS with Homebrew
brew install esengine/reasonix/reasonix

# macOS, Linux, or Windows with Node.js 18+ and a writable npm prefix
npm install -g reasonix
```

AO's Harness settings offers these package-manager methods when the local
prerequisites are available. macOS prefers Homebrew; npm is the fallback. Manual
binaries are available from the
[official Reasonix releases](https://github.com/esengine/DeepSeek-Reasonix/releases).
All channels remain subject to the CLI capability requirement above.

Configure credentials using Reasonix's own setup workflow before starting an AO
session. AO inherits the user's Reasonix environment and configuration; it does
not replace `REASONIX_HOME`, copy credentials, or write a separate credential
store. Its bounded `reasonix doctor --json` probe can report **configured** when
credentials exist locally. This does not mean a provider accepted them, so AO
presents that state as unverified.

## Session behavior

AO delivers the task through the native terminal after recognizing a ready
composer. It fails safely if readiness cannot be confirmed; it does not submit
the initial prompt blindly after a timeout. Restore requires the exact native
session ID recorded from Reasonix. AO does not resume an arbitrary latest
conversation. Legacy-only native sessions are outside this exact-restore contract.

AO merges observation hooks into the worktree's `.reasonix/settings.json`,
preserving user hook entries and unknown fields. Malformed settings or a detected
concurrent edit cause a safe failure instead of an overwrite. Standing
instructions stay in the process-scoped system prompt file, not user messages
or persisted global Reasonix configuration.

AO's `default` permission mode selects Reasonix's `read-only` preset.
`accept-edits` and `auto` select its `workspace-write` sandbox, including native
workspace-scoped operations. Only explicit `bypass-permissions` selects
`danger-full-access`.

## Current limits

- Terminal UI only: no structured Chat, reviewer execution, interface handoff,
  or nested-agent session tracking.
- Native hook coverage is partial: `SessionStart` fires lazily on the first turn,
  so it is not a process-start or composer-readiness signal.
- Hooks observe activity; they do not approve tools or prove semantic acceptance
  of a user prompt. `UserPromptSubmit` may run before another hook rejects it.
- Permission events lack a correlated tool-use ID, so they do not assert AO's
  blocked state.
- `SessionEnd` can also indicate `/new` conversation rotation. Runtime supervision
  determines process exit rather than treating that event as proof of exit.
- Synthetic subagent IDs are filtered rather than recorded as the root native
  conversation. AO persists the root identity and lifecycle observations, not
  raw prompts, tool arguments, tool output, or credentials from hook payloads.

The Reasonix avatar crops the diamond mark from upstream `docs/logo.svg` under
the MIT license, preserving its artwork. Attribution is retained in
[`LICENSE-reasonix.txt`](../../frontend/src/renderer/assets/agents/LICENSE-reasonix.txt).
