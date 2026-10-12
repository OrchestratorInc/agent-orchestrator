# Mastra Code feasibility — 2026-10-11

Verdict: suitable next AO coding-harness integration candidate. Research-only; no Mastra install, provider call, AO integration, or screenshot was produced.

Released mastracode 0.45.0 and @mastra/code-sdk 1.11.0 npm provenance identify source commit 8a0fd46222d8737e4c369291a3efa59e632aadbd in mastra-ai/mastra. Apache-2.0; Node >=22.19. This is the terminal CLI, not the separate mastra-code-ui desktop application.

## Supported interfaces found in released source

- CLI `mastracode`, interactive `--tui-initial-prompt TEXT` (fresh only), `--tui-prompt TEXT` (also on restore), `mastracode resume THREAD_ID`. Prompts follow typed-input semantics including slash and ! commands; these are not automatically literal-only task arguments.
- TUI model uses MASTRACODE_MODEL_ID, permissive setting MASTRACODE_YOLO. Headless mode has its own prompt/thread/model/permission/output contracts; do not infer TUI permission mapping from headless flags.
- Native DeepSeek accepts DEEPSEEK_API_KEY. Z.ai can be a settings.customProviders entry using its authorized compatible endpoint and slug/model. Codex authentication uses Mastra's own browser/device OAuth; inspected source does not import the existing Codex CLI login.
- MASTRA_APP_DATA_DIR isolates settings/auth/database, with MASTRA_DB_PATH override. It does not redirect global/project hooks or plugin registries.
- Native plugins expose additive instructions string/resolver after native instructions. AO may register a private plugin through project .mastracode/plugins/plugins.json, preserving existing entries and disabledPlugins. TUI currently discards hook additionalContext, so hooks alone are not a private-instruction channel.
- Hooks include session/agent lifecycle, permission request/result, interrupt, tools, stop and subagents, with session_id/cwd/run_id. Mostly asynchronous; require identity/sequence fencing.
- Escape aborts active session only if per-thread escapeAsCancel permits it. Foreground shell forwards abortSignal to process-group cleanup; source evidence is not live cancellation proof.

## Required live qualification

1. Missing/locked requested resume ID falls back to a pending-new-thread UI. Reject this through prevalidated native history and verified observed identity; generic readiness/exit code is insufficient.
2. Preserve native settings, hooks, plugins, instructions and tool restrictions. Reapply additive AO instructions on restore.
3. Verify initial/follow-up input, current permissions, exact restore/history, active shell cancellation with PID/start-time/no-late-side-effect controls, and independent AO Kill.
4. Qualify background tools/schedulers, ownership and signal behaviour; capture actual AO Electron screenshots with source/binary/session provenance.

## Duplicate scan

AO GitHub PR search across all states for Mastra and mastracode returned no matches on 2026-10-11. This is a point-in-time search, not a guarantee against later parallel work.

## Primary sources

- https://www.npmjs.com/package/mastracode/v/0.45.0
- https://github.com/mastra-ai/mastra/tree/8a0fd46222d8737e4c369291a3efa59e632aadbd
- https://github.com/mastra-ai/mastra/blob/8a0fd46222d8737e4c369291a3efa59e632aadbd/mastracode/tui/src/main.ts
- https://github.com/mastra-ai/mastra/blob/8a0fd46222d8737e4c369291a3efa59e632aadbd/mastracode/tui/src/tui/thread-startup.ts
- https://github.com/mastra-ai/mastra/blob/8a0fd46222d8737e4c369291a3efa59e632aadbd/packages/core/src/workspace/tools/execute-command.ts
- https://github.com/mastra-ai/mastra/blob/8a0fd46222d8737e4c369291a3efa59e632aadbd/packages/core/src/workspace/sandbox/local-process-manager.ts
