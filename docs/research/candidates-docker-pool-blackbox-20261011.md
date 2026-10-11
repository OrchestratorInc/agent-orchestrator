# Docker Agent, Pool and Blackbox — candidate triage (2026-10-11)

Research only. Official docs, released source/metadata and GitHub duplicate searches were read from the VPS. No new CLI installation, execution, provider call, compatibility test or AO integration proof was produced. Routine validation is PR CI only; essential live AO evidence is a separate authorized stage.

| Candidate | Decision | Auth and main gap |
| --- | --- | --- |
| Docker Agent v1.150.0 | Strong next ACP candidate | Native DeepSeek key; custom OpenAI-compatible Z.ai plausible. Live lifecycle and instruction retention unverified. |
| Pool v1.0.16 | Conditional candidate | Documented compatible-endpoint configuration; ACP permission/cancel details and private hook merge need qualification. Proprietary distribution. |
| Blackbox CLI v2 | Hold | Current installer metadata returned 404; exact released artifact/version and additive instructions remain unverified. Requires documented Blackbox auth. |

## Docker Agent

Official docker/docker-agent v1.150.0 (October 9 release); standalone docker-agent or Docker plugin docker agent; Apache-2.0. Docker Desktop is optional for ordinary native operation. This is an existing project/alias candidate, not proof of a new project launch on that date.

ACP supports documented new/load with history replay, resume without replay, list/close/delete, SQLite storage, per-session cwd, permission requests and cancellation. Host-credentials validation checks configuration presence, not remote token validity. Source Run/Stop cancel operations and join shutdown. Native/client-backed shell choice needs an explicit AO capability decision.

Private additive instructions have a public route: `docker-agent serve acp <existing-config-or-alias> --hook-session-start <private-helper>`. MergeHooks appends CLI hooks after existing hooks; session_start additional_context persists for the session and runs on begin/resume. One ACP process per AO session scopes the launch hook. Verify fresh and restored token consumption, replay/duplication, default/user instruction preservation, permissions and child cleanup before registration.

DEEPSEEK_API_KEY is supported natively. Custom base_url/token_key/openai_chatcompletions config offers a plausible Z.ai route, not tested. ChatGPT auth uses its own cagent file/login; automatic existing Codex-login reuse was not established.

Sources:

- https://github.com/docker/docker-agent/releases/tag/v1.150.0
- https://docs.docker.com/ai/docker-agent/features/acp/
- https://github.com/docker/docker-agent/blob/v1.150.0/pkg/acp/agent.go#L714
- https://github.com/docker/docker-agent/blob/v1.150.0/pkg/acp/run.go
- https://github.com/docker/docker-agent/blob/v1.150.0/pkg/acp/lifecycle.go#L210
- https://github.com/docker/docker-agent/blob/v1.150.0/cmd/root/flags.go#L30
- https://github.com/docker/docker-agent/blob/v1.150.0/pkg/config/hooks.go#L52
- https://docs.docker.com/ai/docker-agent/configuration/hooks/#context-contributing-events
- https://docs.docker.com/ai/docker-agent/providers/deepseek/
- https://docs.docker.com/ai/docker-agent/providers/custom/
- https://docs.docker.com/ai/docker-agent/providers/chatgpt/
- https://docs.docker.com/ai/docker-agent/getting-started/installation/

## Pool

Official poolsideai/pool v1.0.16, August 14; native platform artifacts for macOS/Linux/Windows amd64/arm64. The repo distributes docs/binaries under a proprietary EULA, not the agent implementation source. User-installed binary support avoids assuming redistribution rights.

Use stdio `pool acp`; its experimental HTTP serve mode is separate. Docs expose session/list/load, session config and steering. `pool acp --settings <private-YAML>` layers over workspace/global settings. Unnamed hooks combine; same-name hooks replace. A private unnamed SessionStart hook can return additional_context on start/resume, preserving the native prompt. prompts.system_message replaces the default and should not implement this path. Hook output is capped at32KiB and fails open; verify both in actual conformance.

Permissions document default, accept-edits, auto, always-allow with deny precedence. Exact ACP auth/method/permission IDs and cancellation/descendant guarantees remain unestablished. POOLSIDE_STANDALONE_BASE_URL, POOLSIDE_API_KEY and POOLSIDE_STANDALONE_MODEL document compatible endpoints, making Z.ai/DeepSeek plausible but unverified. Existing Codex-login reuse not documented.

Sources:

- https://github.com/poolsideai/pool/releases/tag/v1.0.16
- https://docs.poolside.ai/cli/cli-reference#pool-acp
- https://docs.poolside.ai/hooks#configuration-reference
- https://docs.poolside.ai/permissions
- https://docs.poolside.ai/get-started/log-in
- https://github.com/poolsideai/pool/blob/main/LICENSE.md
- https://poolside.ai/eula

## Blackbox

Official ACP docs specify blackbox --experimental-acp and BLACKBOX_API_KEY, but not the full new/load/replay/permission/cancel contract. Official installer identifies Blackbox CLI v2, product blackbox-cli-v2, wrapper blackbox, Node>=20. Its derived release metadata endpoints returned404 for linux-x64 and mac-arm64. Exact version/artifact cannot be pinned from this evidence.

Do not substitute npm @blackboxai/cli1.0.2: its executable is cli and it is not established as the documented ACP product. No supported private additive instruction route or direct Z.ai/DeepSeek/existing-Codex credential route was established. Hold pending release identity and those contracts.

Sources:

- https://docs.blackbox.ai/features/blackbox-cli/acp-integration
- https://blackbox.ai/install.sh
- https://docs.blackbox.ai/features/blackbox-cli/commands-reference

## Duplicate research

Canonical AO all-state PR searches included aliases docker-agent, Docker Agent, cagent, poolside, pool and blackbox. cagent history found closed/unmerged old TypeScript ecosystem/plugin PRs #217 and #218; neither is a current Go adapter. No current Poolside/Blackbox integration PR found; generic pool results concerned unrelated worker/database pools. Current source inspection found no candidate adapter.

- https://github.com/OrchestratorInc/agent-orchestrator/pull/217
- https://github.com/OrchestratorInc/agent-orchestrator/pull/218
