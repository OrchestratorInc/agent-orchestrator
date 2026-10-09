# Letta Code terminal harness

Node.js 22.19 or later is required. AO runs the `letta` CLI from `@letta-ai/letta-code@0.34.9`. The inspected
release is [`v0.34.9`](https://github.com/letta-ai/letta-code/releases/tag/v0.34.9),
commit `cbf9026030c74e60432c246aec2bb0ec08fd49bc`.

Use Settings → Agents → Letta Code to install and open the native setup UI.
Letta supports local model providers and Letta Cloud; Cloud stores agent memory
and conversations remotely. A new AO session creates a dedicated Letta agent
and conversation. AO does not delete those provider records when a worker is
terminated. Configure your provider in Letta before spawning an AO worker.

The adapter is terminal-only. Chat, interface switching, reviewers, reasoning
settings and AO tool allow/deny overrides are not advertised. Model choices
come from `letta model list`; custom models must first be configured in Letta.
Credential environment variables are reported as **configured**, never verified.
Cloud keychain login and other native credentials remain **unknown** to AO's
local auth check; Letta validates them during native setup and actual requests.

| AO behavior | Native contract |
| --- | --- |
| Fresh session | `--new-agent --new` avoids existing project history and the non-unique `default` conversation |
| Initial task | Delivered through the terminal after actual composer text appears; leading dashes never enter argv |
| Standing instructions | Raw stdout from a `quiet: true` UserPromptSubmit hook, wrapped by Letta as hidden system-reminder context on every turn |
| Default permissions | Explicit `--permission-mode standard`; Letta itself defaults to unrestricted |
| Accept edits | `--permission-mode acceptEdits` |
| Bypass permissions | `--permission-mode unrestricted` |
| Auto permissions | Rejected; no distinct native policy is claimed |
| Restore | Only `--conversation conv-…`; absent or invalid IDs fail; native missing history exits with an error |
| Turn cancellation | Escape, sent as raw terminal input without Enter; process termination uses AO’s supervisor |
| Activity | Submit and tool hooks → active; PermissionRequest → blocked; Stop → idle; supervisor → process exit |

AO merges its hooks into `.letta/settings.local.json`, preserves unrelated
settings and hooks, and installs the sibling `.gitignore`. Global configuration,
project AGENTS.md and provider system prompts remain native-owned. Hook output
must be quiet: Letta otherwise prints successful stdout, including context, in
the terminal. The hook timeout is milliseconds in this release. PermissionRequest observation
exits 1 deliberately: Letta interprets exit 0 as approval and exit 2 as denial.
The adapter observes the request while leaving the native decision with the user.

Startup readiness is required for this harness. Missing markers, a startup
selection dialog, a changed/customized composer, or an expired wait budget
causes spawn delivery to fail instead of typing a task into an unrelated prompt.
The normal permission footer and fresh-conversation hint text are the inspected
markers. A heavily customized Letta statusline may require adjusting these
markers in a future adapter revision.

## Source evidence

All links below are pinned to the inspected release commit:

- [CLI flags and parser](https://github.com/letta-ai/letta-code/blob/cbf9026030c74e60432c246aec2bb0ec08fd49bc/src/cli/args.ts)
- [Fresh and exact conversation startup](https://github.com/letta-ai/letta-code/blob/cbf9026030c74e60432c246aec2bb0ec08fd49bc/src/index.ts)
- [Awaited hook context and visible user text separation](https://github.com/letta-ai/letta-code/blob/cbf9026030c74e60432c246aec2bb0ec08fd49bc/src/cli/app/use-submit-handler.ts)
- [Quiet hook execution and raw UserPromptSubmit stdout](https://github.com/letta-ai/letta-code/blob/cbf9026030c74e60432c246aec2bb0ec08fd49bc/src/hooks/executor.ts)
- [Hook payload fields](https://github.com/letta-ai/letta-code/blob/cbf9026030c74e60432c246aec2bb0ec08fd49bc/src/hooks/types.ts)
- [Permission semantics](https://github.com/letta-ai/letta-code/blob/cbf9026030c74e60432c246aec2bb0ec08fd49bc/src/permissions/mode.ts)
- [Composer rendering](https://github.com/letta-ai/letta-code/blob/cbf9026030c74e60432c246aec2bb0ec08fd49bc/src/cli/components/InputRich.tsx)
- [Native JSON model catalog](https://github.com/letta-ai/letta-code/blob/cbf9026030c74e60432c246aec2bb0ec08fd49bc/src/cli/subcommands/model.ts)

## Verification status

This integration was written from released source inspection. Local tests,
builds, lints, type checks, installations and provider probes were deliberately
not executed at the user's request. Unit and integration tests are committed
for PR CI. Live hidden-context, provider authorization, terminal cancellation,
kill/restore continuity and cross-platform executable conformance are **NOT_RUN**;
a successful source review or CI unit suite is not evidence for those live gates.
