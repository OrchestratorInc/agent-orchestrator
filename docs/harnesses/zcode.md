# ZCode terminal integration

AO targets the official Z.ai CLI from [ZCode v3.14.3](https://github.com/zai-org/ZCode/tree/v3.14.3), source commit `29628c9`. Installation is manual because the similarly named npm packages are not official distributions. This integration registers Terminal UI workers and orchestrators; it does not register Chat, reviewers, or interface handoff.

The interactive CLI starts without a task argument: `-p` runs a headless turn and exits. AO waits for the composer before delivering task text through the terminal. Leading dashes remain text. The adapter refuses delivery when required readiness markers are absent, rather than typing into login or trust prompts.

AO merges its hooks into `<workspace>/.zcode/config.json`, preserving user settings and unrelated hooks. ZCode's `hooks.events` nesting is required. Before launch and restore, AO calls `hooks trust status --json` and grants only the exact declaration digests matching its own event, command, source file, and empty matcher. It never grants a complete project hook bundle. Disabled or malformed hook configuration fails closed.

`UserPromptSubmit` returns standing instructions as native `additionalContext`. ZCode adds that context as a system reminder without replacing its default prompt or the visible task. Subsequent turns reread AO's current standing instructions. Native session identity comes from hook `session_id`; restore passes the exact `sess_*` identity to `--resume`. The native runtime rejects a missing or archived session. Escape cancels a turn; Ctrl+C is not a cancel key in this release.

AO default explicitly selects `build`, accept-edits selects `edit`, auto selects `build`, and bypass selects `yolo`. An explicit native mode configuration remains supported. Tool deny rules are passed through; unsupported allowlists are rejected. Model selection remains in ZCode's native `model.main` configuration. An unexpired but unverified credential reports `configured`, never `authorized`.

Native hooks report permission requests as blocked and resumed tool activity as active. Coverage remains partial because terminal exit and every background continuation are not represented by these hooks. AO does not infer process death from missing callbacks.

Source references:

- `apps/zcode-cli/packages/cli/src/run.ts`: TUI, headless prompt, mode and resume routing.
- `apps/zcode-cli/packages/cli/src/hooks-trust-command.ts`: exact declaration trust commands.
- `apps/zcode-cli/packages/bootstrap/src/workspace-hook-trust-cli.ts`: status schema and digest grants.
- `apps/zcode-cli/packages/core/src/hooks/configured-runner-input.ts`: native hook aliases.
- `apps/zcode-cli/packages/core/src/runtime/methods/hooks.ts`: hidden hook context.
- `apps/zcode-cli/packages/core/src/runtime/methods/resume.ts`: missing-session rejection.
- `apps/zcode-cli/packages/tui/src/app-keyboard.ts`: native Escape and Ctrl+C behavior.

Validation for this revision runs on the PR only, as requested. Local test, lint, build, typecheck, and provider-conformance executions were not run. API artifacts were generated. Passing unit/CI checks must not be described as authenticated provider lifecycle verification; live ZCode conformance remains unexecuted until explicitly reported.
