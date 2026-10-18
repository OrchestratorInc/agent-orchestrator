# CodeBuddy Code TUI

AO supervises Tencent's `@tencent-ai/codebuddy-code` interactive CLI (`codebuddy`,
with `cbc` as its alias). The qualified release is **2.164.0**, requiring Node
18.20.8 or newer. AO's native-agent installer pins that released npm package.
AO leaves automatic updates unavailable until a newer release is qualified.
This supersedes the closed, unmerged contract-only [PR5503](https://github.com/OrchestratorInc/agent-orchestrator/pull/5503).

Configure native CodeBuddy login or its third-party provider before creating a
session. [Native environment configuration](https://www.codebuddy.cn/docs/cli/env-vars)
and [models.json](https://www.codebuddy.cn/docs/cli/models) remain CodeBuddy-owned.
Local environment credentials report **configured**, never authorized merely
because they exist. Other CLIs' OAuth logins are not automatically imported.
Native model IDs use direct text selection; AO does not invent a model catalog.
The runtime qualification used Z.ai's OpenAI-compatible coding endpoint and
`glm-5.3-flash`; a models.json-only startup still required native Tencent login.

Native TUI rendering requires an interactive environment: native Ink uses static
output when `CI` or `CONTINUOUS_INTEGRATION` is enabled. AO refuses uninitialized
static surfaces instead of pasting into them.

Only **Default** permissions are qualified. AO preserves native permission
controls and folder trust, and refuses Auto, Accept Edits and Bypass Permissions.
Answer native decisions in the terminal. AO message delivery requires the current
rendered empty composer and refuses drafts, initialization, active work and
permission/login/trust dialogs under a serialized input lease. Native prompt
suggestions are conservatively treated as nonempty input; use the native TUI
or disable suggestions through native settings before sending an AO message.

The initial task is a single positional argument after `--`, so leading dashes
remain task data. Private standing instructions use the actual bounded text in
`--append-system-prompt`, independently of the visible task, retaining native
instructions and `AGENTS.md`. File-only instructions must be readable; payloads
above 64 KiB, invalid UTF-8 or NUL fail instead of being truncated.

AO merges lifecycle hooks into `.codebuddy/settings.local.json`, preserving
user settings/hooks and adding the sibling managed ignore file. Native callbacks
capture the real session ID. Restore requires that explicit ID and readable,
nonempty native history with matching durable workspace and identity; missing,
corrupt, cross-workspace or ambiguous history fails rather than replaying the
old task in a new conversation. Native config-dir overrides must remain stable
between launch and restore. Histories over 32 MiB are conservatively refused.
Resume reapplies current private instructions and does not replay the initial task.

AO uses supported native environment controls to keep background tasks in the
foreground, disable cron and prevent automatic CLI replacement during a session.
Foreground shell Escape cancellation and AO Kill have separate proof gates.
This contract does not promise cleanup of intentionally detached shell programs
or survive a human taking over native settings/tools inside the TUI.

Chat/ACP, TUI–Chat handoff, reviewer integration and native background scheduling
are excluded. `--acp` declarations alone do not establish conformance.

## Branding

AO intentionally uses the existing initials avatar for CodeBuddy. No upstream
brand asset is bundled because redistribution terms for a suitable official
asset have not been verified. The product name remains CodeBuddy Code; this
fallback does not claim an unofficial logo or Tencent endorsement.
