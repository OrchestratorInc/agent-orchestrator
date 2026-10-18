# Terminal harness version refresh

AO installs managed npm/Bun/uv/pipx releases side by side, switches `current`,
and retains releases referenced by live launches. An idle terminal can move to
the new executable only if its adapter explicitly implements
`AgentTerminalVersionRefresher`. This capability also enables AO's process
supervisor on future launches, supplying exact-generation workload and startup
proof. It does not require the adapter to support Chat handoff.

The scheduler retains `AO_VERSION_REFRESH_IDLE` (default ten minutes), the
four-start concurrency limit, and the existing activity, viewer, typing and
workload checks. Automatic retries use that same semaphore through startup.
A native ID must belong to the current launch. Strict resume cannot replay the
original task or fall back to a fresh conversation; failed/unknown startup
checks retain recoverable identity and stale-version state. History probes must
positively verify the exact ID when the CLI can otherwise open a fresh composer.
A confirmed-dead replacement is reconciled to stopped activity even when its
uncommitted generation's exit hook is fenced. Its retry can run immediately;
unknown liveness retains retry intent without stopping a possibly live child.

## Shipped support matrix

Every shipped adapter declares support explicitly. The registry contract tests
exercise each enabled native restore builder with isolated fake executables.
These are command/identity/option tests, not authenticated model-memory or
cross-version compatibility guarantees for every historical CLI release.

| Harness | Passive native resume | Automatic refresh |
|---|---|---|
| Claude Code | `claude --resume ID` | Yes |
| Codex | `codex resume ID` | Yes |
| OpenCode v1 | `opencode --session ID` | Yes; exact local export preflight |
| OpenCode v2 | `opencode --standalone --session ID` | Yes; isolated v2 `session export ID --standalone` preflight |
| Grok | `grok -r ID` | Yes |
| Cursor | `cursor-agent --resume ID` | Yes |
| Qwen | `qwen --resume ID` | Yes; preserves the selected runtime/hook PATH |
| Gemini | `gemini --resume ID` | Yes |
| Copilot | `copilot --resume ID` | Yes; retains native saved model |
| Kimi | `kimi --session ID` | Yes; exact `session list --all --json` preflight |
| Muse | `muse resume ID` | Yes |
| Droid | `droid -r ID` | Yes |
| Amp | `amp threads continue ID` | Yes; no latest-thread selection |
| Agy | `agy --conversation ID` | Yes |
| Crush | `crush --session ID` | Requires current-launch native identity; private live TUI test captured none |
| Goose | `goose session --resume --session-id ID` | Yes; interactive `session`, not headless `run` |
| Auggie | `auggie --resume ID` | Yes; passive resume omits `--print` |
| Devin | `devin -r ID` | Yes |
| OMP | `omp --resume ID` | Yes; does not use turn-restarting `--restore` |
| Codewhale | `codewhale --resume ID` | Yes |
| OpenHands | `openhands --resume ID` | Yes |
| Cline | `cline --id ID` | Yes; durable CLI root ID and exact local history preflight |
| Kiro | `kiro-cli chat --resume-id ID` | Yes |
| Kilo Code | `kilo --session ID` | Yes; exact export preflight |
| Vibe | `vibe --resume ID` | Yes; scoped native completion supplies fresh idle proof |
| Pi | `pi --session ID` | Yes; not the interactive `--resume` picker |
| Kimchi | `kimchi --session ID` | Yes |
| Prime Agent | `prime-agent --resume ID` | Yes |
| Autohand | `autohand resume ID` | Yes |
| FX | `fx --resume ID` | Yes |
| Command Code | `cmd --resume ID` | Yes |
| MiMo Code | `mimo --session ID` | Yes; exact export preflight |
| Aider | History-file restoration only in AO's integration | No: no captured native identity/active-turn lifecycle proof |
| Continue | `--resume` selects latest; `--fork ID` creates a new session | No: neither preserves the required exact native identity |
| DeepSeek Harness | Headless `--session-id ID TASK` | No: no TUI native-ID hooks; restore also requires a new task |
| Unreal Agent | Chat-only embedded provider | Not a terminal harness |

AO reapplies pinned working directory, permission policy, model/provider and
standing instructions through each adapter's native flags, environment or
workspace configuration. Native saved settings are retained where the provider
owns them. Pi, OMP, Prime and Kimchi reapply configured thinking effort. Kimi
Code 2.2's current CLI accepts model/approval flags with `--session`; older CLI
contracts without the positive local list probe fail closed before stopping.

## Contract evidence

On 2026-10-18, installed CLI help was checked using isolated homes, closed stdin
and no provider credentials for Amp, Auggie, Agy, Autohand, Cline, Crush, Devin,
Droid, FX, Goose, Kilo, Kimchi, Kimi, Muse, OMP, Qwen, Vibe and Prime Agent.
Nested help was checked for Amp `threads continue`, Autohand `resume`, Goose
`session`, FX `session resume`, and Kimi `session list`.

Primary references for providers and the unsupported identity limitation:

- [Amp threads](https://ampcode.com/docs/threads): `threads continue ID`.
- [OpenCode v1 CLI](https://opencode.ai/docs/cli/) and [v2 commands](https://dev.opencode.ai/v2/docs/cli/commands/): v1 `export ID` versus v2 `session export ID`.
- [OpenCode v2 export handler](https://github.com/anomalyco/opencode/blob/dev/packages/cli/src/commands/handlers/session/export.ts) and [transfer schema](https://github.com/anomalyco/opencode/blob/dev/packages/schema/src/session-transfer.ts): `--standalone` selects the private server; exported JSON identifies the conversation through `info.id`.
- [OpenCode v1 session route](https://github.com/anomalyco/opencode/blob/v1.2.27/packages/opencode/src/cli/cmd/tui/routes/session/index.tsx): a failed session load navigates home, so liveness alone is insufficient.
- [Grok CLI](https://docs.x.ai/build/cli/reference), [Cursor CLI](https://prod.cursor.com/docs/cli/overview), [Gemini sessions](https://geminicli.com/docs/cli/session-management/), [Copilot sessions](https://docs.github.com/en/copilot/how-tos/copilot-cli/use-copilot-cli/work-with-multiple-sessions).
- [Factory CLI](https://github.com/Factory-AI/factory/blob/main/docs/reference/cli-reference.mdx), [Kiro session management](https://kiro.dev/docs/cli/chat/session-management/).
- [Kimi session flags](https://moonshotai.github.io/kimi-cli/en/reference/kimi-command.html): some versions create a session for an unknown ID. The exact local list probe prevents that during automatic refresh.
- [Prime usage](https://github.com/PrimeIntellect-ai/prime-agent/blob/main/packages/coding-agent/docs/usage.md), [Command Code sessions](https://commandcode.ai/docs/sessions), [MiMo CLI](https://github.com/XiaomiMiMo/MiMo-Code/blob/main/packages/web/src/content/docs/cli.mdx).
- [OpenHands parser](https://github.com/OpenHands/OpenHands-CLI/blob/main/openhands_cli/argparsers/util.py): native `--resume` and approval aliases.
- [Continue initialization](https://github.com/continuedev/continue/blob/main/extensions/cli/src/commands/chat.ts): `--fork` calls `startNewSession`, while `--resume` loads latest.
- [Aider options](https://aider.chat/docs/config/options.html): `--chat-history-file` and `--restore-chat-history` exist; AO does not incorrectly advertise exact-native-ID automation for that file-history path.

Cline CLI hooks report an internal SDK conversation as `taskId`; current CLI
hooks additionally carry the durable resume handle in
`sessionContext.rootSessionId`. AO prefers that root ID. A real
`3.0.72 → 3.0.73` test demonstrated that passing the internal `conv_…` task ID
to `--id` silently creates fresh history. Before stopping an old process, AO
therefore requires matching saved CLI manifest/message IDs, a nonempty message
history, and the same working directory under the launch home. Missing,
unreadable or oversized history fails closed; files above 16 MiB require manual
recovery rather than an automatic restart. The contract is visible in
[Cline hook serialization](https://github.com/cline/cline/blob/main/apps/cli/src/utils/hooks.ts).

Qwen's remote-input shell does not load login profiles: those can reset AO's
selected Node and hook PATH. A real 0.25.1 launch failed on an older Node's
missing `node:fs.openAsBlob` before this correction; the corrected launch and
0.25.1 → 0.25.2 native conversation migration were exercised privately.

Vibe profiles carry standing rules in the native `instructions` field. In
2.26.0, resolving an AO custom `system_prompt_id` while validating the profile
failed before its additional prompt directory was available. Tool-free Vibe
turns only emit `post_agent`, so a newer completion for the exact current launch
and native conversation updates idle proof even if activity remained idle.
Unscoped, stale-generation and duplicate boundaries remain deduplicated; the
existing typing barrier and idle threshold still apply.

A private Kilo 7.8.10 → 7.8.12 run exposed native history incompatibility:
7.8.10 exported the exact conversation from `kilo-rc.db`, whereas 7.8.12 reported
`Session not found` with the same home and working directory. AO's export
preflight retained the old process and reported failure. Native resume flags
alone do not guarantee compatible history across arbitrary CLI releases.

Chat uses its own provider contract, independent of terminal refresh eligibility.
Pi Chat runs the separately installed `pi-acp` helper, which embeds its own Pi
SDK; upgrading the `pi` executable does not upgrade that SDK. The installed
0.17.1 helper exercised privately resolved Pi SDK 0.75.5. It exposes no ACP
approvals, so AO rejects default/auto Chat admission; a disposable bypass-only
control required its existing custom model definitions. That control does not
prove a Pi CLI upgrade changes the Chat SDK. Kimi ACP accepts only native-default
approval policy; unsupported policies and unadvertised model selections are
reported instead of silently ignored. Neither limitation changes global policy.

## Release and recovery boundaries

Copilot TUI launches, native resumes and ACP Chat launches include the verified
`--no-auto-update` option. This prevents its vendor updater from changing the
selected release outside AO's reference tracking. The flag was checked in the
actual cached 1.0.97 CLI payload and installed 1.0.98 help during private live
testing; the first unguarded run advanced itself before AO's update request.

Managed installers keep each release immutable. OpenCode v1/v2 use distinct
managed launchers despite sharing the native executable name. Unmanaged
in-place installers are blocked while a live session/reviewer depends on the
files; their update remains available after those processes exit.
The in-use advisory resolves the same update recipe as the operation for its
ownership-checked maintenance method, so eligible managed updates show `safe`;
disabled management, rejected capabilities and in-place recipes stay blocked.

Release cleanup also checks live process references independently of session
rows. A real Cline hub remained alive after its foreground session moved and
exposed why leases alone are insufficient. Managed releases and deferred
Homebrew kegs stay on disk while a detached process references them; mutable
launcher aliases are retained conservatively until the process exits. Failed,
empty, malformed or truncated inventories defer cleanup. Windows uses local
process command/image metadata, retaining opaque script-runtime references;
that branch is fixture-tested here, not claimed as native Windows validation.

A durable `.unsettled` intent precedes switching `current`. Startup repairs a
missing Windows junction using the validated intended or last published release,
then records the actual selection before allowing launches. Failed repair or
publication is reported instead of silently accepting a broken launcher. The
intent remains conservative about old live release references. A forced-crash
fixture covers the remove/mklink gap; executing that Windows branch with POSIX
junction fixtures is not a native Windows E2E test.

Existing unsupervised processes cannot supply exact workload proof and are
left alone until a manual native resume creates a supervised launch. No global
harness installation changes or authenticated provider turns are needed by the
regressions. The startup interval proves a surviving child, not a protocol-level
conversation-loaded acknowledgement; later external history corruption cannot
be made transactional with AO's database.
