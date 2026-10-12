# ZCode native Chat integration

AO uses the official [ZCode v3.15.1 source](https://github.com/zai-org/ZCode/tree/aac4755666d09fdcd70272fcf063c077a639015f), commit `aac4755666d09fdcd70272fcf063c077a639015f`, through `zcode app-server`. Its CLI reports `0.16.9`; the upstream product and internal CLI versions differ. The protocol is native ZCode NDJSON, not ACP. Terminal UI, reviewers and TUI/Chat handoff are unsupported. Explicit Terminal requests fail before creating durable resources.

**Draft qualification:** pinned stock-source CI proves additive private context and exact native history with a synthetic provider. Authenticated standalone Z.ai proof additionally demonstrates correct-workspace edits, separate project rules, leading-dash task delivery, a second message, refreshed private instructions after native process kill, unknown-history rejection, offered permission decisions and executing-child cancellation. Actual registered AO manager/Electron proof and final current-head CI remain pending; standalone proof does not establish integration completion.

## Instructions and native controls

AO writes its per-session standing instructions under its private data directory and installs only its managed declarations into the workspace `.zcode/config.json`. The native trust CLI grants exact matching declaration digests, never an entire project hook bundle. Existing user settings, rules and unrelated hooks are preserved. AO does not change HOME or global native configuration.

The app-server explicitly enables native workspace-hook trust. `UserPromptSubmit` reads the current private input independently of the visible task and returns native `additionalContext`. ZCode stores it as a `hook_context` attachment. The ordinary fallback reaches the provider as **user-role `<system-reminder>` context**, not a system/developer append. Eligible models can promote this context through native mid-conversation system projection. Source conformance captures synthetic payload roles; authenticated receipts establish consumption without intercepting third-party requests. Native defaults and user/project instructions remain native-owned. AO never uses replacement `customSystemPrompt` for its private input.

Private input is limited to 20,000 valid UTF-8 bytes, reserving room under native's aggregate 24,000-character hook-context cap. Oversized/missing/invalid refreshed input is refused, including before each Chat turn; the AO hook returns a supported blocking result if its file changes after preflight. Competing configured `UserPromptSubmit` hooks are refused without changing them because their output cannot be bounded statically. Native hook execution itself fails open when a hook is absent or crashes; this integration does not promise universal fail-closed delivery or confidentiality against model echo/history.

AO default/auto permissions select native `build`, accept-edits selects `edit`, and bypass selects `yolo`. Restores explicitly reapply the selected approval policy. Offered decisions are forwarded exactly; forged, duplicate and stale choices are rejected. Ordinary messages cannot consume pending permissions. A private, host-generation-bound request ledger preserves outstanding reverse RPC identities across daemon attachment changes. Native user-input/browser/auth delegation methods without an AO mapping return an explicit unsupported-method error.

## History and lifecycle

A detached AO host owns the native stdio process and fences its workspace, approval policy, provider scope and MCP configuration. Reconnect attaches only to that host; unknown/incompatible ownership never causes replacement. Cold restore validates the exact native ID, durable user history and workspace using read-only native SQLite inspection, then calls native `session/resume`. Missing/archived/foreign history fails; no latest-session or fresh-history fallback exists.

Native events provide active/settled turn boundaries, final assistant replies and tool activity. This initial driver does not advertise streamed prose, live model controls, images, additional roots, read-only sandbox, compaction or fork. Models remain in the native provider profile; AO accepts native stdio MCP servers. Native `session/stop` interrupts a turn; explicit AO Kill terminates the owned host separately. Background work and detached-child cleanup require independent runtime evidence, not assumptions from a settled turn.

## Install and authentication

The vendor releases currently provide desktop installers rather than a standalone Linux CLI installer. Do not install similarly named unrelated npm packages. Build the pinned official source with Node `24.14.0`, pnpm `10.33.2` and the committed lockfile through `pnpm --filter '@zcode/cli...' build`. Keep its runtime assets together. AO's CI artifact is a **source-built stock protocol bundle**, not a vendor release; it includes CLI/provider companion/browser assets and explicitly excludes the TUI dependency closure. The upstream SEA collector currently expects a nonexistent root `@zcode/shared/dist` directory.

Expose a `zcode` wrapper on PATH that runs the stock bundle with Node. Native OAuth uses `zcode login`. Existing authorized API credentials may use documented native provider configuration paths, including `ZCODE_PERSONAL_PROVIDER_CONFIG_FILE`; AO does not import another vendor's login. Credential presence is configured/unknown evidence, never proof of remote authorization.

## Retained failures

- v3.14.3 stock TUI reached its provider with defaults/project rules/task, but omitted AO private context: [historical conformance failure](https://github.com/OrchestratorInc/agent-orchestrator/actions/runs/38010927686).
- v3.15.1 rejects the contributor's nonexistent `--trust-workspace-hooks` before startup: [separate strict failure](https://github.com/OrchestratorInc/agent-orchestrator/actions/runs/38255274887). The stock TUI still omits workspace trust admission. Production code no longer launches this recipe.
- The historical positive TUI fixture remains available behind `AO_ZCODE_REQUIRE_TUI` for a future supported upstream surface; the shipped Chat-only surface instead has an explicit stock-parser negative control and independent app-server/provider-role conformance. No hidden-context assertion was weakened into a TUI pass.
- The first authenticated protocol attempt selected disabled thinking; the provider refused that setting. A separate `low` reasoning control passed. The failed receipt remains distinct.

Routine suites run only in PR CI for this audit. No local routine tests, lint, typechecks, vet, race or workflows were executed on the VPS.
