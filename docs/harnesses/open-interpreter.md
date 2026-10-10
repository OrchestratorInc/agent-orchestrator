# Open Interpreter terminal contract

AO integrates the Rust terminal release [`rust-v0.0.56`](https://github.com/openinterpreter/openinterpreter/releases/tag/rust-v0.0.56), inspected at [`cc054cf52fa3585a3de50e0d4e0be6f9ee6677e8`](https://github.com/openinterpreter/openinterpreter/tree/cc054cf52fa3585a3de50e0d4e0be6f9ee6677e8). This is a TUI integration. Chat, reviewer, interface handoff, provider usage accounting, and semantic prompt acknowledgements are intentionally unavailable.

The executable is `interpreter`, never the ambiguous `i` alias. Runtime discovery checks Rust-specific help features to reject the older Python executable. Models are native free-form IDs. `login status` only proves local credential configuration, so AO reports `configured`, never `authorized`; other outputs remain unknown because custom providers can obtain credentials separately.

## Launch and native configuration

- `--no-daemon` disables shared background-server reuse and auto-start. AO supervises the terminal process. See `codex-rs/tui/src/daemon_startup.rs` and `startup_orchestration.rs` in the pinned source.
- The exact initial task follows `--` as a positional argument. Native startup queues it until setup completes; AO never types the task into auth or trust dialogs. See `tui/src/chatwidget/input_restore.rs`.
- `INTERPRETER_HOME`, native user/project configuration, AGENTS.md discovery, MCP configuration, provider credentials, and native project trust remain owned by Open Interpreter. Default permission mode emits no policy override. Accept edits selects workspace-write/on-request; auto selects native auto-review; bypass requires AO's explicit bypass mode. Unsupported tool allow/deny lists are rejected.
- Standing AO instructions travel through SessionStart `hookSpecificOutput.additionalContext`, not the visible task or `developer_instructions` replacement. The native implementation adds a developer-role message and filters Context entries from terminal hook cells (`core/src/context/hook_additional_context.rs`, `tui/src/history_cell/hook_cell.rs`).
- AO enables native hooks for the invocation and installs three SessionFlags definitions: SessionStart, UserPromptSubmit, PostToolUse. Exact canonical definition hashes authorize only these definitions. It never uses `--dangerously-bypass-hook-trust`, and it neither rewrites native files nor grants trust to unrelated hooks. See `hooks/src/engine/discovery.rs`, `hooks/src/config_rules.rs`, and `config/src/fingerprint.rs`.

## Identity, activity, and restore

SessionStart records the provider UUID without declaring work active. Child markers are rejected before metadata capture. UserPromptSubmit is not semantic acceptance because later hooks can still block it. Stop is not idle because native continuation/compaction may still run. PermissionRequest is not blocked because the native guardian can handle it without user input. PostToolUse can report active work; native terminal chrome supplies continuous active/idle/waiting-input observations through AO's existing observer.

The observer recognizes the current composer, queue/status area, and permission/question footers. It fails closed when native chrome is missing or customized beyond the inspected forms. It does not infer death from unknown output. Process exit belongs to the existing supervisor.

Restore requires an exact nonzero native UUID and a matching active rollout's `session_meta.id` and `session_meta.cwd`. AO compares filesystem identity with the current workspace before passing `--cd`; the native flag otherwise bypasses historical-directory checks. Names, latest-session selection, missing history, foreign workspaces, malformed metadata, and archived-only sessions are rejected rather than starting a fresh conversation.

Open Interpreter compresses cold rollouts after seven days. AO reads `.jsonl` and `.jsonl.zst` metadata without modifying native state: at most 50,000 directory entries, 256 KiB for the first line, 2 MiB compressed input, and a 16 MiB decoder memory/window bound. Compressed native history is eligible for restore; the native append path materializes it. Shared transcript consumers only receive plain JSONL paths.

## Validation scope

Unit tests cover launch/restore boundaries, permissions, config preservation, private context, exact trust hashes, child identity guards, activity frames, and compressed-history limits. `.github/workflows/open-interpreter-conformance.yml` downloads the checksum-pinned Linux x86_64 release and exercises production-generated argv and hook trust under a PTY against a local fake Chat Completions provider. It checks a dash-leading task, private-context delivery without terminal disclosure, preserved AGENTS.md, untrusted-hook isolation, and exact UUID/history resume. Native macOS and Windows execution are not covered by this conformance job.

The initial implementation was prepared without local tests, checks, builds, or provider execution, at the user's request. API artifacts were regenerated. Remote CI results in the draft PR are the executable validation record.

## Asset attribution

The avatar is the upstream `logo/light.svg` from the pinned source, distributed under the upstream Apache-2.0 license.
