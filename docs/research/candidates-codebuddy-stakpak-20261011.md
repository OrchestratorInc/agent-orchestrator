# CodeBuddy Code and Stakpak — candidate triage (2026-10-11)

Research-only VPS assessment. No installs, native execution, provider calls, tests or source edits. Both are plausible TUI-first candidates; conformance and real AO screenshots remain unproved. Routine validation belongs in PR CI, with only essential actual AO runtime evidence on the authorized VPS.

## CodeBuddy Code

Current published npm identity is @tencent-ai/codebuddy-code2.164.0, executables codebuddy/cbc. Closed, unmerged AO PR5503 pinned2.151.0 contract fixtures and deliberately omitted registration. A new implementation should explicitly supersede that older work.

Official CLI docs expose --append-system-prompt TEXT for TUI/print, preserving native defaults, plus exact --resume ID and --acp stdio. A native append-file flag is not documented. Prefer passing the actual bounded AO instruction content through the supported append-text flag; merely asking the model to read an AO-private file is not native instruction-delivery proof. ACP inheritance of that flag needs independent validation.

Official model docs demonstrate custom OpenAI-compatible DeepSeek with an environment-referenced key; generic custom providers make Z.ai plausible. Existing Codex-login reuse remains unproven. Published2.164.0 ACP declarations expose initialize/auth/new/load/permissions/cancel/model selection, with replay declarations. Type declarations and old fixtures do not establish current runtime behaviour.

Gates: pin installed artifact, initial/follow-up delivery, native identity and exact restore, effective append instructions preserving defaults/project rules, provider/tool compatibility, permissions, executing tool cancellation/Kill, real AO screenshots. ACP is separate from TUI qualification.

Sources:

- https://registry.npmjs.org/@tencent-ai%2fcodebuddy-code/2.164.0
- https://github.com/OrchestratorInc/agent-orchestrator/pull/5503
- https://www.codebuddy.cn/docs/cli/cli-reference
- https://www.codebuddy.cn/docs/cli/models
- https://www.codebuddy.cn/docs/cli/iam
- https://unpkg.com/@tencent-ai/codebuddy-code@2.164.0/dist-server/types/node/acp/acp-protocol.d.ts
- https://unpkg.com/@tencent-ai/codebuddy-code@2.164.0/dist-server/types/node/acp/session-replay.d.ts

## Stakpak

Official native Rust stakpak v0.3.88, released June10; release tree d3eba6f502a79b993b826ef3ab4065eec9e38dfa. No matching canonical AO integration PR found. Current inspected main had no registered Stakpak/CodeBuddy adapter.

TUI --session/-s UUID resolves that session's active checkpoint, while -c UUID selects an exact checkpoint. Source reloads real history/tool state and errors if unavailable. --system-prompt-file inserts an additional system message after history load. Provider TaskBoardContextHook always prepends its native embedded base prompt and retains existing messages, so this is additive; repo AGENTS/APPS discovery remains separate. The initial concern that it replaced native defaults was corrected by tracing this hook.

Custom OpenAI-compatible providers are documented. Native OpenAI Codex OAuth exists, but automatic reuse of an existing Codex CLI login was not established.

ACP hold: load_session only records UUID and does not reload/replay history, despite advertised capability. ACP startup force-updates and prints instruction-file status on protocol stdout. Do not register ACP from capability flags alone. A terminal adapter is the better current path.

TUI gates: actual identity observation, resume/history/tool-state continuity, renewed instructions without duplication, permissions/cancellation/owned descendants, provider compatibility and real AO screenshots.

Sources:

- https://github.com/stakpak/agent/releases/tag/v0.3.88
- https://github.com/stakpak/agent/blob/v0.3.88/cli/src/main.rs#L128
- https://github.com/stakpak/agent/blob/v0.3.88/cli/src/commands/agent/run/checkpoint.rs#L176
- https://github.com/stakpak/agent/blob/v0.3.88/cli/src/commands/agent/run/mode_interactive.rs#L469
- https://github.com/stakpak/agent/blob/v0.3.88/libs/api/src/local/hooks/task_board_context/mod.rs#L80
- https://github.com/stakpak/agent/blob/v0.3.88/README.md
- https://github.com/stakpak/agent/blob/v0.3.88/libs/shared/src/oauth/providers/openai_codex.rs
- https://github.com/stakpak/agent/blob/v0.3.88/cli/src/commands/acp/server.rs#L1779
- https://github.com/stakpak/agent/blob/v0.3.88/cli/src/commands/mod.rs#L552
