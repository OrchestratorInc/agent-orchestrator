# Mastra Code

AO runs the native Mastra Code TUI. The qualified release is `mastracode@0.45.0`
with `@mastra/code-sdk@1.11.0`, released source
`8a0fd46222d8737e4c369291a3efa59e632aadbd`. Use Node >=22.19 and install the
native CLI independently. For API-backed use, the upstream-supported
`ONNXRUNTIME_NODE_INSTALL=skip` installation option omits optional CUDA downloads.

Select `mastra-code` and **Default** permissions. Auto and Accept Edits have no
qualified native mapping and are rejected. Bypass requires an explicit selection.
Use **Log in** to open `mastracode login`, then choose an OAuth account or
**Add an API key**. This native chooser accepts API-key provider IDs; the
`--provider` login flag is for OAuth providers and must not be used for
`zai` or `minimax`. Native credentials and custom providers use the same
`MASTRA_APP_DATA_DIR` as sessions (default
`$AO_DATA_DIR/mastra-code/profile`). AO does not import other vendors' logins.

The model field accepts exact native provider/model IDs. Z.AI uses
`ZHIPU_API_KEY` (not `ZAI_API_KEY`), with IDs such as `zai/glm-5.2` or
`zai-coding-plan/glm-5.2`. MiniMax uses `MINIMAX_API_KEY`, with IDs such as
`minimax/MiniMax-M3` or `minimax-coding-plan/MiniMax-M3`. Choose the provider
route and model covered by your account's plan; a catalog entry does not prove
entitlement. Keys can be saved through native login instead of environment
variables. Custom endpoints remain native `customProviders` settings.

AO explicitly places native application data below its data directory. It
preserves HOME, AGENTS.md, global and project hooks, plugins, unknown config
fields, and disabledPlugins. Standing instructions use an additive native
project plugin; TUI hook additionalContext is not an instruction channel.
A conflicting or disabled `ao-runtime` plugin fails instead of overwriting or
reenabling user configuration. AO refuses to modify tracked Mastra configuration
files; untrack the managed files before enabling this integration.

Fresh tasks use `--tui-initial-prompt TEXT` with a new local native-history
database under AO data. A durable session descriptor preserves its path across
worktree recreation; earlier history databases are retained on fresh restart.
A leading dash is data. Slash and `!` task strings are
refused because Mastra interprets them as native commands. Raw terminal input
remains available for native commands and permission decisions.

Exact restore requires real messages in the local native history for the same
workspace, an unlocked native thread, the native saved model (including in-TUI changes), and enabled Escape
cancellation. Missing, empty, foreign or uncertain history fails without a fresh
fallback. AO additionally requires the actual current generation's native thread
ID and initialized rendered composer before publishing the restored runtime.
Current private instructions are refreshed after native thread hydration.

Pasted messages require an empty current rendered composer, with serialized
input admission. Native permission prompts and user drafts must be resolved in
the terminal. Activity combines native lifecycle hooks with current viewport
observations; native process exit belongs to AO's supervisor. Foreground shell
cancellation uses native Escape and the official abort-aware sandbox. Enabled
background tools/schedulers and custom/remote storage are refused pending
separate ownership qualification.

Chat, reviewer registration, TUI/Chat handoff, native background tasks, and
unsupported permission policies are outside this integration's scope.

Authentication uses Mastra's native `mastracode login` chooser for OAuth or API
keys, with the same `MASTRA_APP_DATA_DIR` as AO sessions. AO checks the installed
SDK's credential resolution and verifies an explicitly selected native build
model with a small provider request, without creating a conversation. Credentials
without a selected build model remain configured. After adding an API key, open
Mastra Code in build mode in the same AO profile and use `/model` or `/setup` to
select the build model, then check auth again. A model entered for one AO task
does not change the native build default used by this generic check.
Provider limits and transport failures
do not mark credentials rejected. The verification currently supports the reviewed
Mastra Code 0.45.0 / SDK 1.11.0 / core 1.75.0 interface; other installed versions
retain native login and report an inconclusive check.

Unattended terminal version refresh remains unsupported pending qualification;
manual native restore does not imply automatic refresh support.
