# Mastra Code

AO runs the native Mastra Code TUI. The qualified release is `mastracode@0.45.0`
with `@mastra/code-sdk@1.11.0`, released source
`8a0fd46222d8737e4c369291a3efa59e632aadbd`. Use Node >=22.19 and install the
native CLI independently. For API-backed use, the upstream-supported
`ONNXRUNTIME_NODE_INSTALL=skip` installation option omits optional CUDA downloads.

Select `mastra-code` and **Default** permissions. Auto and Accept Edits have no
qualified native mapping and are rejected. Bypass requires an explicit selection.
Native provider keys remain native; AO reports a stored key as configured, not
remotely authorized. Custom providers can be configured in
`$AO_DATA_DIR/mastra-code/profile/settings.json`. The model field accepts the
native provider/model ID (for example `deepseek/deepseek-chat`). AO does not
import Codex or another vendor's login.

AO explicitly places native application data below its data directory. It
preserves HOME, AGENTS.md, global and project hooks, plugins, unknown config
fields, and disabledPlugins. Standing instructions use an additive native
project plugin; TUI hook additionalContext is not an instruction channel.
A conflicting or disabled `ao-runtime` plugin fails instead of overwriting or
reenabling user configuration.

Fresh tasks use `--tui-initial-prompt TEXT`; a leading dash is data. Workspaces
with existing Mastra history are refused for fresh launch because the native CLI
would auto-resume and skip that initial task. Slash and `!` task strings are
refused because Mastra interprets them as native commands. Raw terminal input
remains available for native commands and permission decisions.

Exact restore requires real messages in the local native history for the same
workspace, an unlocked native thread, the saved model, and enabled Escape
cancellation. Missing, empty, foreign or uncertain history fails without a fresh
fallback. AO additionally requires the actual current generation's native thread
ID and initialized rendered composer before publishing the restored runtime.
Current private instructions are installed again before restore.

Pasted messages require an empty current rendered composer, with serialized
input admission. Native permission prompts and user drafts must be resolved in
the terminal. Activity combines native lifecycle hooks with current viewport
observations; native process exit belongs to AO's supervisor. Foreground shell
cancellation uses native Escape and the official abort-aware sandbox. Enabled
background tools/schedulers and custom/remote storage are refused pending
separate ownership qualification.

Chat, reviewer registration, TUI/Chat handoff, native background tasks, and
unsupported permission policies are outside this integration's scope.
