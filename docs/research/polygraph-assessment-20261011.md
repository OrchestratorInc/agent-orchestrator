# Polygraph assessment — 2026-10-11

Verdict: orchestration/context connector candidate, not a new AO coding-agent adapter.

The user identified https://trypolygraph.com/. Its official homepage explicitly says “Polygraph isn't an agent” and calls it a meta-harness. Documentation describes wrapping Claude Code, Codex, and OpenCode while coordinating repository graphs, checkouts, PRs, CI, session logs, and handoff. CLI existence does not make it an independent coding runtime.

## Verified published interfaces

- Standalone Linux/macOS CLI; `polygraph session start` selects `claude`, `codex`, `opencode`, `auto`, or `skip`; it delegates coding to the selected agent.
- Session start can use the current checkout (`--primary-checkout in-place`) and has a multiplexer `none` mode. These may be useful for a future explicitly designed connector; they do not establish ownership-safe nesting inside AO.
- Native plugins expose Polygraph MCP tools and skills to existing agents.
- Separate Polygraph account login uses OAuth device authorization (`polygraph auth login`), followed by account selection. Model-provider API keys do not replace that account login.
- Background session capture is separately opt-in at organization level. Explicit Polygraph sessions upload transcripts according to its documentation; this evaluation did not connect repositories, log in, install plugins, or upload data.

## AO recommendation and limits

Do not register Polygraph as another agent harness in the Hermes/OpenJarvis integrations. Evaluate an optional connector separately if cross-repo context/session linking is desired. A design would need to assign repository/worktree ownership, map parent/child identity and cancellation, and avoid two controllers independently managing the same sessions.

This is official-documentation research only. No Polygraph binary, login, provider turn, AO session, screenshot, or live lifecycle claim was produced. License/source availability and exact released runtime behaviour remain unverified. The native coding agents it wraps still require their own authorization.

## Primary sources read on the VPS

- https://trypolygraph.com/
- https://docs.trypolygraph.com/
- https://docs.trypolygraph.com/getting-started/quickstart
- https://docs.trypolygraph.com/getting-started/installation.md
- https://docs.trypolygraph.com/getting-started/agent-plugin.md
- https://docs.trypolygraph.com/cli/session.md
- https://docs.trypolygraph.com/features/using-multiple-agents
- https://docs.trypolygraph.com/features/background-session-capture
