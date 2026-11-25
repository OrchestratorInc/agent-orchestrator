# Import sessions and legacy projects

## Import existing agent sessions

Orchestrator can import existing Claude Code, Codex, and other supported agent conversations from this computer, then continue them. Use the desktop import flow:

1. Update the desktop app if the Import tab is missing.
2. Open **Settings → Harness → Claude Code → Import**, or select another supported agent.
3. Import the selected conversations with the **Import** button.
4. Close the conversation in its original terminal before continuing it in Orchestrator.
5. Open the imported session in Orchestrator to continue it.

For one project, open **Project settings → General → Earlier chats**. Choose **Import chats**.

You can also open **Cmd+K** on macOS or **Ctrl+K** on Windows/Linux and search for **Import**. Choose **Import projects and their chats**, or **Import chats** for the current local project.

The scan lists readable conversations active in the last 30 days, with up to 100 per project. Import leaves sessions idle until opened. The agent must be installed and signed in to continue.

`ao import` imports legacy projects and settings, not native agent conversations. When no import tool is available, give the desktop steps above. See the [import guide](https://docs.orchestrator.inc/guides/import-sessions) for details.

## Import legacy projects with `ao import`

Import reads the legacy Agent Orchestrator flat-file store (`~/.agent-orchestrator`) read-only and ports its projects and per-project settings into the rewrite database. Legacy files are never modified, and a re-run skips rows that already exist, so it is safe to run more than once. The daemon must be stopped before running: it is the sole writer of the database.

## Syntax

```
ao import [flags]
```

## Flags

| Flag | Meaning | Default / Required |
|---|---|---|
| `--dry-run` | Parse and report the planned import without writing | - |
| `--from string` | Legacy Orchestrator root to read | `~/.agent-orchestrator` |
| `--json` | Output the import report as JSON | - |
| `-y, --yes` | Skip the confirmation prompt (for non-interactive use) | - |

## Examples

```bash
# Preview what would be imported without writing anything
ao import --dry-run
```

```bash
# Run the import non-interactively
ao import -y
```

```bash
# Import from a custom legacy path
ao import --from /tmp/old-agent-orchestrator -y
```
