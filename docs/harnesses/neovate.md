# Neovate Code terminal harness

AO launches the official `neovate` Terminal UI for workers and orchestrators.
Harness Settings installs `@neovate/code@0.28.5` with npm and offers the native
`/login` setup flow. Configure a provider/model in Neovate before spawning.
This integration does not register Chat, reviewers, or interface handoff.

The implementation targets [release 0.28.5](https://github.com/neovateai/neovate-code/releases/tag/0.28.5),
source commit [`d4f89bff`](https://github.com/neovateai/neovate-code/tree/d4f89bff889e6488f907fc19cfe045f23ca8d02e).
Its npm archive is integrity-pinned in the released-TUI CI workflow. Other
installed versions are not claimed as verified compatible.

## Launch and instructions

Tasks are one positional argument after `--`, preserving leading dashes,
quotes, shell metacharacters, and embedded newlines. Neovate's native parser
still treats an exact command name (such as `server` or `config`) as a
subcommand and interprets leading `!` or `/` as native commands. AO rejects
these initial-task forms with an error; phrase those tasks as ordinary text.
Interactive terminal input retains Neovate's own command behavior.

AO loads one session-specific plugin with `--plugin`. Its `systemPrompt` hook
appends standing instructions to the provider's existing system prompt. The
visible initial task remains a separate user turn. This deliberately does not
use Neovate's parsed-but-unwired `--append-system-prompt` flag. Configured
plugins, native defaults and project `AGENTS.md` rules remain in place.

The plugin and native-session binding marker live under `.neovate/ao/` in the
session workspace, with a self-ignoring `.gitignore`. AO replaces only its own
sentinel-marked plugin and never edits the user's Neovate configuration. Hooks
are reinstalled on restore so changed standing instructions take effect.

## Identity and restore

The native user-prompt hook stages the expected task; the subsequent provider
resolution hook confirms a newly persisted native user row before reporting
acceptance and recording the eight-character session ID, workspace and
transcript path. Startup, a displayed composer, and a pre-write callback do not
count as accepted input. Missing model configuration blocks the task with an
actionable error. Resume requires the recorded ID and a
nonempty native conversation whose message IDs and structure are intact.
Missing, corrupt, foreign or mismatched history returns an error instead of
silently starting a new conversation. The native plugin repeats the check at
initialization and before accepting a resumed prompt, using Neovate's own path
resolver. It never selects the latest session.

Neovate owns transcripts under `~/.neovate/projects/`. AO's workspace marker
binds each transcript to the original workspace because native message rows do
not contain a workspace field. This binding is not a cryptographic guarantee
against deliberate local file tampering.

## Permissions, activity and cancellation

| AO mode | Neovate policy |
| --- | --- |
| default | `default`: native confirmations, with native safe-read exceptions |
| accept-edits | `autoEdit`: edits allowed, shell commands still reviewed |
| auto | `autoEdit`: conservative mapping; shell commands still reviewed |
| bypass | `yolo`: native approval bypass |

Saved native `approvalTools` grants block every non-bypass restore; saved
`autoEdit` blocks a restore requesting default permissions. Neovate otherwise
retains these grants despite stricter argv flags. AO checks them before launch
and again at native initialization, without modifying the provider history.
Revoke incompatible grants in Neovate before restoring.

Tool allow/deny lists are unsupported and fail explicitly. Native ask-user tools
can still request interaction in bypass mode.

Native accepted prompts and tool events report active; native stop reports
idle. Coverage is **partial**: Neovate has no permission-request hook, so AO
does not infer a permission state or treat silence as a broken callback stream.
Native accepted prompts carry AO's coordination delivery identity.

AO's cancel action sends **Escape**, Neovate's native cancellation key, without
Enter. Ctrl-C remains Neovate's clear-input/double-press-exit action. Runtime
kill tears down the process; a subsequent restore uses the exact saved native
conversation.

## Authentication and models

AO checks canonical provider-key environment variables and API keys in
`~/.neovate/config.json` without executing the provider or sending network
requests. Presence means **configured**, not authorized. OAuth-only or other
provider-specific credential sources may remain unknown until support is
added; AO never labels a key as verified from its presence alone.

The model picker reads explicit global and project Neovate configuration:
selected model fields and configured `provider.<name>.models` entries. Neovate
has no model-list command. AO does not invent a model catalog, and arbitrary
custom names must first be configured in Neovate. With no explicit entries,
select a model in Neovate or provide an explicit AO model override.

No reusable licensed upstream logo was identified in the pinned release; the
existing product avatar fallback is used.

## Validation status

Per the task instruction, no local tests, builds, lint, typechecks or provider
executions were run. Source review is not runtime conformance. Unit tests cover
argv separation, restore rejection, owned hook files, auth classification,
model discovery, registry, migration and native cancellation dispatch.

`.github/workflows/neovate-conformance.yml` installs the integrity-pinned
release and runs the opt-in `TestReleasedNeovateTUIConformance` in a Linux PTY
against a localhost fake model provider. It exercises initial delivery, native
rules plus hidden instructions, exact resume with refreshed instructions,
hook identity and cancellation followed by continued input. That workflow and
normal CI must pass before treating the implementation as validated. Native
macOS/Windows TUI runs, authenticated provider behavior, permission dialogs and
full daemon/Electron lifecycle coverage remain separate validation gaps.
