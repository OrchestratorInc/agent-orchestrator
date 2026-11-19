# Project script path-variable help

Research snapshot: 2026-10-09. Public source is pinned below. These are UI component and runtime findings; no live competitor UI or script execution was tested. Public `main` may differ from released builds.

## UI comparison

| App | Where the help appears | Variable interaction and examples |
| --- | --- | --- |
| Codex desktop | The installed full local-environment editor places a **Variables** button beside **Setup script**. Its popover is titled **Setup script environment variables**. Compact environment creation puts the full editor behind **Advanced options**, initially closed. | Two descriptions above plain code text: `CODEX_SOURCE_TREE_PATH` means **Source workspace path**; `CODEX_WORKTREE_PATH` means **New worktree path**. The component has no copy or insert handler. The default Unix setup placeholder begins `cd "$CODEX_WORKTREE_PATH"`, followed by dependency installation and a setup script. Cleanup has no separate Variables button. [Installed bundle](#codex-evidence) |
| Superset | The existing editor has **Project lifecycle scripts**, **Setup / Teardown / Run** tabs, and a **Docs** link. Both existing and V2 editors show no inline variable list or variable disclosure. | Commands are typed or imported from a script file. The existing editor has **Import file**; V2 has **Import** inside the field. Setup examples use Bun installation; teardown uses Docker Compose. Neither reviewed editor copies or inserts variables. [Existing editor][superset-editor], [V2 editor][superset-v2], [V2 field][superset-field] |
| T3 Code | Project settings contains **Actions**. The action dialog labels its editor **Command**, with `bun test` as its placeholder and **Run automatically on worktree creation** as a switch. | There is no path-variable list, explanatory popover, or variable copy/insert control in the reviewed settings and dialog. Other switches let setup hold the agent until completion and run an action when a thread settles; that lifecycle is different from AO's removal cleanup. [Actions settings][t3-settings], [Action editor][t3-editor] |

## Documentation and runtime contracts

- **Codex:** Official documentation establishes settings-based setup scripts, automatic execution for newly created worktrees, platform overrides, and an `npm install` / `npm run build` example. It does not describe path variables or their controls. The installed bundle establishes the UI details above. [Official local environments documentation][codex-docs]
- **Superset:** Official docs list `SUPERSET_ROOT_PATH` (root repository), `SUPERSET_WORKSPACE_PATH` (worktree), and `SUPERSET_WORKSPACE_NAME` (workspace name). Setup and teardown run in the workspace directory. Its practical example copies `.env` with `cp "$SUPERSET_ROOT_PATH/.env" .env`. These are documentation claims, not inline editor help. [Project Lifecycle Scripts][superset-docs]
- **T3 Code:** Runtime code supplies `T3CODE_PROJECT_ROOT` from the project's checkout and supplies `T3CODE_WORKTREE_PATH` when a worktree exists. Setup runs with the worktree as its working directory. The variables' existence does not establish visible UI help. [Environment helper][t3-env], [Setup runner][t3-runner]

## Recommendation for AO

Keep AO's existing, initially closed **Path variables** disclosure. Explain the project checkout and target worktree separately. Use `$NAME` references for macOS/Linux and `%NAME%` for Windows; do not present one shell's syntax as portable. Include one quoted example for the detected shell. For macOS/Linux:

```sh
cp "$AO_SOURCE_TREE_PATH/.env" "$AO_WORKTREE_PATH/.env"
```

This is a design recommendation: Codex supports keeping reference help next to the editor, and Superset's `.env` example gives the paths a concrete use. AO already provides the disclosure in both setup and cleanup; cleanup should describe the worktree being removed. [AO component](../../frontend/src/renderer/components/ProjectScriptsSettings.tsx), [AO labels](../../frontend/src/renderer/i18n/en.json)

## Codex evidence

After searching and opening official OpenAI documentation, inspected the installed [ChatGPT/Codex bundle](/Applications/ChatGPT.app/Contents/Resources/app.asar), version **26.1002.52244**, build **13536**, read from [Info.plist](/Applications/ChatGPT.app/Contents/Info.plist). Relevant archive entries:

- `webview/assets/local-environment-editor-7829f73134e0.js`: `Ct` defines the popover, `wt` renders its non-interactive code rows, `yt` renders script fields, and `Dt` wires Variables only to setup and initializes compact Advanced options closed.
- `webview/assets/app-shared-6c00c2afcf84.js`: `uEt` and `dEt` define the two variable names.

These bundle findings are version-specific UI implementation evidence, not observations of the running settings pane.

[codex-docs]: https://learn.chatgpt.com/docs/environments/local-environment
[superset-docs]: https://docs.superset.sh/setup-teardown-scripts
[superset-editor]: https://github.com/superset-sh/superset/blob/fc44d327a79cb87f55762f8ec3913d1a366ada26/apps/desktop/src/renderer/routes/_authenticated/settings/project/%24projectId/components/ProjectSettings/components/ScriptsEditor/ScriptsEditor.tsx#L358-L442
[superset-v2]: https://github.com/superset-sh/superset/blob/fc44d327a79cb87f55762f8ec3913d1a366ada26/apps/desktop/src/renderer/routes/_authenticated/settings/v2-project/%24projectId/components/V2ProjectSettings/components/V2ScriptsEditor/V2ScriptsEditor.tsx#L293-L367
[superset-field]: https://github.com/superset-sh/superset/blob/fc44d327a79cb87f55762f8ec3913d1a366ada26/apps/desktop/src/renderer/routes/_authenticated/settings/v2-project/%24projectId/components/V2ProjectSettings/components/V2ScriptsEditor/components/ScriptField/ScriptField.tsx#L41-L113
[t3-settings]: https://github.com/pingdotgg/t3code/blob/a0066d5cb7d18abd786c1c23bf835f898490fd37/apps/web/src/components/settings/ProjectActionsSettings.tsx#L136-L230
[t3-editor]: https://github.com/pingdotgg/t3code/blob/a0066d5cb7d18abd786c1c23bf835f898490fd37/apps/web/src/components/projectScriptEditor.tsx#L332-L472
[t3-env]: https://github.com/pingdotgg/t3code/blob/a0066d5cb7d18abd786c1c23bf835f898490fd37/packages/shared/src/projectScripts.ts#L41-L70
[t3-runner]: https://github.com/pingdotgg/t3code/blob/a0066d5cb7d18abd786c1c23bf835f898490fd37/apps/server/src/project/ProjectSetupScriptRunner.ts#L368-L395
