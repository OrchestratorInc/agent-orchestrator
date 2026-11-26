# ao stop

Stop the Orchestrator daemon.

## Syntax

```
ao stop [flags]
```

## Flags

| Flag | Meaning | Default / Required |
|---|---|---|
| `--json` | Output stop result as JSON | - |
| `--timeout duration` | How long to wait for daemon shutdown | `10s` |

## Examples

```bash
# Stop the daemon
ao stop
```

```bash
# Stop with a longer timeout
ao stop --timeout 30s
```
