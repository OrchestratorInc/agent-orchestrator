# ao import

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
