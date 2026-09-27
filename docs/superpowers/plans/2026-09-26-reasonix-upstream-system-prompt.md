# Reasonix Upstream System Prompt Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a process-scoped `--append-system-prompt-file` option to Reasonix so hosts can provide private system-role guidance without replacing user configuration.

**Architecture:** Parse the option at the interactive and `run` CLI boundaries, carry the path through existing rebuild options, and append validated UTF-8 content after Reasonix's configured prompt and project memory are composed. Keep the option ephemeral and make every rebuild/resume re-read the same file.

**Tech Stack:** Go, Cobra/pflag, Reasonix boot/controller tests

**Spec:** AO repository `docs/superpowers/specs/2026-09-26-reasonix-harness-design.md`

## Implementation status — 2026-09-27

The upstream implementation is submitted in
[draft Reasonix PR #11059](https://github.com/esengine/DeepSeek-Reasonix/pull/11059),
committed as
[`642173c`](https://github.com/nikhilachale/DeepSeek-Reasonix/commit/642173c)
on `feat/append-system-prompt-file`, based on CLI branch `main-v2` at
`2a3855f16a09eabc1596c1d9bc93dba77038e786`. Upstream's contribution guide
requires `main-v2`; its default `studio` branch is a separate release line.
Tasks 1–3 use one consolidated feature commit.

The root TUI, `chat`, and `run` now accept the flag. Validation rejects missing,
unreadable, nonregular, empty, and invalid UTF-8 files without exposing the path
or contents. Relative paths resolve after `--dir`; `--` ends option parsing.
Boot appends the exact text after project memory and before extension replacement,
and re-reads it on reload, model changes, and exact resume. Canonical resume
preserves the complete visible transcript after compaction while replacing the
model's standing instructions. Native session storage may retain an earlier
composed prompt on an unflagged resume; hosts must pass the flag each time.

Validation on macOS arm64 with Go 1.26.6:

- Complete root `go test -p 1 ./...`, `go build ./...`, and `go vet ./...` passed.
  The initial parallel suite hit existing subprocess timing failures; the
  complete sequential rerun passed.
- Pinned golangci-lint v2.12.2 passed for the native macOS root module and
  Windows build tags in both root and desktop modules. Repolint, cache/docs
  PR metadata guards, and `git diff --check` passed.
- `REASONIX_RELEASE_CACHE_GUARD=1 go test ./internal/agent -run TestReleaseCacheHitGuard -count=1`
  passed, including a second run with `-race`.
- All 33 packages selected by upstream's PR race job are covered: the first 30
  completed in the full sweep; `go test -race -p 1 ./internal/tool/...` passed
  for the remaining three. The separately enabled release cache guard also passed.
- The extra full `go test -race -p 1 ./...` sweep hit its ten-minute package
  timeout in unchanged `internal/session` test `TestCatalogReducerLargeHistoryRetainedHeap`
  while JSON-encoding the large-history fixture. CLI, boot, and control passed
  under race detection; no data race was reported. The optional sweep was stopped
  after this timeout; full race coverage is not claimed.
- The real CLI/fake-provider contract and Unix PTY test passed against the patch.
  Exact resume uses the same native identity and updated file content.
- The same contract rejects both released v1.39.1 and v1.39.2: the required flag
  is unknown. The v1.39.2 macOS arm64 archive SHA-256 is
  `f1d56e2d2694377584bd817c9018ebdb8eb49cec2ed595c7601633e6c8cab654`, matching
  the published `SHA256SUMS`; its executable reports `reasonix v1.39.2` and its
  tag points to `7574f8b4c9b1690acb3090bec25bba071ae81db9`.

Native Linux/Windows execution, desktop runtime/frontend suites, and an
authenticated external model provider were not verified locally. Upstream CI
must supply applicable platform/module checks; fake-provider coverage does not
claim authenticated-provider conformance.

[Upstream CI](https://github.com/esengine/DeepSeek-Reasonix/actions/runs/36324372215),
CodeQL, and App memory screening report `action_required` for the fork PR;
maintainer approval is required before those workflows run. The automatic label
jobs passed, but they do not provide code validation.

The native headless `run_done` event still omits `session_id`. The upstream
contract test reads the sole disposable native manifest to check exact identity;
this does not prove AO hook identity capture or TUI restore conformance.

**Release gate remains closed.** No AO adapter, registration, migration, API enum,
installer entry, or selectable UI has been added. A tagged upstream release must
include the flag, and the AO harness plan must then qualify that actual artifact
for prompt roles, readiness, hooks, identity, permissions, and cancellation.
Do not infer a minimum compatible version from this source commit.

## Global Constraints

- Work in a separate checkout of `esengine/DeepSeek-Reasonix`, based on upstream CLI branch `main-v2` as required by its contribution guide.
- The public flag is exactly `--append-system-prompt-file <path>` unless upstream maintainers request a rename with equivalent semantics.
- The file is process-scoped and must never update `reasonix.toml`, Reasonix home, project instruction files, or hook context.
- Preserve Reasonix's built-in/configured prompt, core policies, and hierarchical project instructions.
- Missing, unreadable, invalid-UTF-8, and empty files fail before a model turn.
- Prompt contents and the supplied path must not appear in diagnostics, JSONL events, or error strings.
- Do not claim AO compatibility until this change exists in a tagged release and the release asset passes live conformance.

## Review Focus

- A relative path after `--dir` resolves deterministically against the effective workspace, while AO's absolute path remains unchanged; Task 1 tests both.
- A token after `--` remains user prompt text even when it resembles the new flag; Task 1 pins parser termination.
- TUI model switches and `/reload` do not lose or duplicate the appended text; Task 2 exercises rebuilds.
- A full-trust extension replacing the system-prompt slot retains its existing contract; Task 2 verifies the append occurs before extension replacement rather than bypassing it.
- Errors never echo sensitive path or file contents; Tasks 1 and 2 assert sanitized failures.

---

### Task 1: Parse and validate the process-scoped file option

**Files:**
- Create: `internal/cli/append_system_prompt.go`
- Create: `internal/cli/append_system_prompt_test.go`
- Modify: `internal/cli/cli.go`
- Modify: `internal/cli/shell_completion.go`
- Modify: `internal/cli/shell_completion_test.go`

**Interfaces:**
- Produces: `resolveAppendSystemPromptFile(path string) (string, error)` returning a canonical absolute path without returning or persisting its content.
- Produces: `cliBuildOverrides.AppendSystemPromptFile string` and `boot.Options.AppendSystemPromptFile string` for Task 2.

- [x] **Step 1: Write failing path-validation tests**

Add `TestResolveAppendSystemPromptFile` cases for an absolute readable UTF-8 file, a relative file, missing file, directory, empty file, and invalid UTF-8. Assert success returns an absolute path and every failure omits both the input path and file contents.

- [x] **Step 2: Run the focused test and confirm failure**

Run: `go test ./internal/cli -run TestResolveAppendSystemPromptFile -count=1`

Expected: FAIL because `resolveAppendSystemPromptFile` does not exist.

- [x] **Step 3: Implement the validator**

Implement `resolveAppendSystemPromptFile(path string) (string, error)` in `internal/cli/append_system_prompt.go`. Use direct filesystem APIs and UTF-8 validation; return fixed, non-sensitive error text. Do not copy the file or mutate configuration.

- [x] **Step 4: Write failing CLI parsing tests**

Extend CLI tests to assert interactive and `run` accept the option, a missing value exits 2, invalid files exit before controller/model execution, and `-- --append-system-prompt-file` remains ordinary prompt text. Extend shell-completion tests to include the path-valued flag for root and `run` commands.

- [x] **Step 5: Wire the flag into both command paths**

Register the path-valued flag in `runAgent` and `chatREPL`, resolve it after `--dir` handling, and store the canonical path in `cliBuildOverrides.AppendSystemPromptFile`. Map that field in `cliProfileBuildOptions` to `boot.Options.AppendSystemPromptFile`.

- [x] **Step 6: Run the CLI suite**

Run: `go test ./internal/cli -count=1`

Expected: PASS.

- [x] **Step 7: Commit**

```bash
git add internal/cli/append_system_prompt.go internal/cli/append_system_prompt_test.go internal/cli/cli.go internal/cli/shell_completion.go internal/cli/shell_completion_test.go
git commit -m "feat(cli): accept an external system prompt file"
```

### Task 2: Compose the prompt and preserve it across rebuilds

**Files:**
- Create: `internal/boot/append_system_prompt.go`
- Create: `internal/boot/append_system_prompt_test.go`
- Modify: `internal/boot/boot.go`

**Interfaces:**
- Consumes: `boot.Options.AppendSystemPromptFile string` from Task 1.
- Produces: `appendExternalSystemPrompt(base, path string) (string, error)`; the existing CLI rebuild closures retain `cliBuildOverrides.AppendSystemPromptFile`.

- [x] **Step 1: Write failing composition tests**

Add table tests proving the helper appends exact UTF-8 content after configured prompt, core policies, and project `AGENTS.md`; uses exactly one blank-line boundary; leaves the base unchanged when no path is supplied; and returns sanitized errors for files changed to missing, empty, or invalid UTF-8 between validation and boot.

- [x] **Step 2: Run the focused boot test and confirm failure**

Run: `go test ./internal/boot -run 'TestAppendExternalSystemPrompt|TestBuildAppendsExternalSystemPrompt' -count=1`

Expected: FAIL because the composition helper and option plumbing do not exist.

- [x] **Step 3: Implement prompt composition**

Implement `appendExternalSystemPrompt(base, path string) (string, error)` in `internal/boot/append_system_prompt.go`. Invoke it in `build` immediately after `memory.Compose` and before extension snapshot assembly. The CLI's captured `cliBuildOverrides` must pass the path into each model switch and `/reload`, so every rebuild re-reads and appends it once.

- [x] **Step 4: Add rebuild and resume regression tests**

Assert fresh interactive, fresh `run`, `--resume`, model switch, and `/reload` all contain one appended block. Assert a replacement extension still owns the final system-prompt slot according to the existing extension contract.

- [x] **Step 5: Run focused and complete Reasonix tests**

Run:

```bash
go test ./internal/boot ./internal/cli -count=1
go test ./...
```

Expected: PASS.

- [x] **Step 6: Commit**

```bash
git add internal/boot/append_system_prompt.go internal/boot/append_system_prompt_test.go internal/boot/boot.go
git commit -m "feat(agent): append process-scoped standing instructions"
```

### Task 3: Document and prove the public contract

**Files:**
- Modify: `README.md`
- Modify: `reasonix.example.toml`
- Modify: `docs/CLI.md`
- Create: `docs/CLI_HOST_INTEGRATION.md` (the CLI branch has no `site/` tree)
- Modify: `internal/i18n/messages_en.go`
- Modify: `internal/i18n/messages_zh.go`
- Modify: `internal/i18n/messages_zh_tw.go`
- Create: `cmd/reasonix/append_system_prompt_contract_test.go` and platform terminal helpers

**Interfaces:**
- Consumes: the released CLI behavior from Tasks 1–2.
- Produces: a documented host-integration contract and a release version usable as AO's minimum version.

- [x] **Step 1: Add a black-box CLI contract test**

Build the real `reasonix` command against a fake provider and assert the flag is accepted in TUI and `run`, composed as system-role content, absent from the user message, and absent from `--events-jsonl`. Assert missing/invalid files fail with sanitized output.

- [x] **Step 2: Run the black-box test and confirm it passes**

Run: `go test ./cmd/reasonix -run TestAppendSystemPromptFileContract -count=1`

Expected: PASS.

- [x] **Step 3: Update user-facing documentation**

Document the flag as a host/automation option, including composition order, process-only lifetime, resume behavior, absolute-path support, and failure/redaction rules. Update localized usage strings and shell-completion expectations.

- [ ] **Step 4: Run release-quality verification**

Run the repository's documented formatting, lint, test, and build commands. Confirm `reasonix --help` and `reasonix run --help` advertise the flag and no generated documentation is stale.

- [x] **Step 5: Commit**

```bash
git add README.md reasonix.example.toml docs/CLI.md docs/CLI_HOST_INTEGRATION.md internal/i18n cmd/reasonix/append_system_prompt*_test.go
git commit -m "docs(cli): document external standing instructions"
```

- [ ] **Step 6: Stop for upstream release**

Upstream handoff is complete in PR #11059; maintainer review, release, and artifact qualification remain pending. Do not begin AO production registration until a tagged release contains the contract. Record its tag, commit, asset checksums, supported platforms, and exact `reasonix --version` output in the AO conformance evidence.
