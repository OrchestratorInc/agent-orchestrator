# ao status

Show Orchestrator daemon status. Use this to verify the daemon is up and check which port it is bound to.

## Syntax

```
ao status [flags]
```

## Flags

| Flag | Meaning | Default / Required |
|---|---|---|
| `--json` | Output status as JSON | - |

## Examples

```bash
# Check daemon status
ao status
```

```bash
# Get status as JSON (e.g. to check port programmatically)
ao status --json
```
