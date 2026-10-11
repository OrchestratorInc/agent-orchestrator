# Warp, Rovo Dev and Hermes ACP — candidate triage (2026-10-11)

Research only; no installs, tests, builds, native/provider calls, AO proof or source changes. Routine validation remains PR CI only. These findings do not supersede active Hermes TUI PR6536 or OpenJarvis PR6537.

## Hermes ACP

Pinned official Hermes v0.21.6 commit818c13be1dc4fd28987e1e881a9408224afd4535 includes genuine stdio hermes acp/hermes-acp. Chat instruction delivery remains a separate blocker from the TUI adapter.

ACP new/load ignore extra kwargs; _make_agent does not pass ephemeral_system_prompt. HERMES_EPHEMERAL_SYSTEM_PROMPT is consumed in the classic CLI init mixin, not this ACP path, so the TUI integration's instruction injection cannot be reused as-is. platform_hints supports append configuration, but no process-local config override separate from HERMES_HOME was established. Redirecting the entire home would need proven preservation of credentials, memory, skills, profiles and restore storage.

ACP provides structured text/thinking/tool/permission events, so Chat can observe native events. That does not establish TUI observer or child-reaping guarantees. session/load replays requested history, but restore accepts only source=acp; it cannot load a native TUI conversation. session/resume may create a new session for unknown IDs; replay failure can be best effort. Exact identity/history must be verified.

Permission timeout/error denies; permanent allow writes native policy. ACP modes concern file-edit policy, not unrestricted shell. Cancel requests hard interruption/delegated-task interruption, with descendant proof still pending. Native Z.ai/DeepSeek providers exist; auth_codex can read/import valid existing Codex credentials, then manages its own state. These are source facts, not a new successful ACP provider run.

Sources:

- https://github.com/NousResearch/hermes-agent/tree/v0.21.6/acp_adapter
- https://github.com/NousResearch/hermes-agent/blob/v0.21.6/acp_adapter/server.py#L616
- https://github.com/NousResearch/hermes-agent/blob/v0.21.6/acp_adapter/session.py#L501
- https://github.com/NousResearch/hermes-agent/blob/v0.21.6/acp_adapter/session.py#L455
- https://github.com/NousResearch/hermes-agent/blob/v0.21.6/hermes_cli/cli_init_mixin.py#L255
- https://github.com/NousResearch/hermes-agent/blob/v0.21.6/hermes_cli/auth_codex.py#L567

## Warp Agent CLI

Official artifact endpoint resolves to v0.2026.10.07.08.29.stable_00 without downloading/running the binary. Native warp TUI exposes --resume CONVERSATION_TOKEN, /status identity, single Ctrl+C cancel/double Ctrl+C exit. No documented ACP, structured observer hooks or private per-session additive instruction flag was found.

Rules come from repo AGENTS.md/WARP.md and global ~/.agents/AGENTS.md. This leaves private instruction isolation unproved. Requires Warp account/browser login or WARP_API_KEY. Product-level compatible endpoints explicitly include Z.ai and transit Warp's backend, but the CLI-specific configuration route is unestablished. ChatGPT app linking does not establish existing Codex auth-file reuse.

Sources:

- https://releases.warp.dev/stable/v0.2026.10.07.08.29.stable_00/tui/linux/x86_64/warp-tui-stable-linux-x86_64.tar.gz
- https://docs.warp.dev/agents/cli/reference/
- https://docs.warp.dev/agents/cli/agent-conversations/
- https://docs.warp.dev/agents/cli/configuration/
- https://docs.warp.dev/agents/inference/custom-inference-endpoint/
- https://docs.warp.dev/agents/inference/chatgpt-subscription/

## Rovo Dev CLI

Official ACLI formula1.3.39-stable is pinned, but the separate Rovo engine version is unresolved; a server docs example0.11.26 is not release evidence. Native TUI is acli rovodev run; separate HTTP/SSE server mode is not ACP.

Public --config-file /private/file.yml and additive agent.additionalSystemPrompt are promising. Copy/merge effective existing config and append existing instructions; do not replace user permission/MCP preferences with defaults. --restore UUID, /status and session_context.json/metadata.json support documented native persistence. Server routes expose restore/replay/cancel and SSE permissions; bind boundaries, event completeness and cleanup still need conformance.

Requires an enabled Atlassian Rovo Dev site, scoped API token and credits. Existing Z.ai/DeepSeek keys or Codex login have no documented replacement role. Conditional enterprise candidate once auth and exact engine pin are available.

Sources:

- https://github.com/atlassian/homebrew-acli/blob/main/Formula/acli.rb
- https://support.atlassian.com/rovo/docs/manage-rovo-dev-cli-settings/
- https://support.atlassian.com/rovo/docs/manage-sessions-in-rovo-dev-cli/
- https://support.atlassian.com/rovo/docs/use-server-mode-in-rovo-dev-cli/
- https://support.atlassian.com/rovo/docs/install-and-run-rovo-dev-cli-on-your-device/

## Duplicate research

Canonical current main had no adapters for these candidates. Hermes TUI is already in progress in6536; historical closed work includes gate5504 and legacy plugin1485. No relevant Warp Agent CLI/Rovo integration PR/issues were found. Old218's Warp mention was a terminal plugin, not this agent CLI.

- https://github.com/OrchestratorInc/agent-orchestrator/pull/6536
- https://github.com/OrchestratorInc/agent-orchestrator/pull/5504
- https://github.com/OrchestratorInc/agent-orchestrator/pull/1485
