# ao doctor

Run local Orchestrator health checks. Use this to diagnose setup problems or verify the environment is correctly configured.

Fix `FAIL` lines. Under `Agent harnesses`, an agent passes when the running daemon reports it installed, even if its binary is not on this shell's PATH; a `WARN ... not found in PATH` for an agent the user does not use needs no action. `ao agent ls` adds each agent's sign-in state.

## Syntax

```
ao doctor [flags]
```

## Flags

| Flag | Meaning | Default / Required |
|---|---|---|
| `--json` | Output health checks as JSON | - |

## Examples

```bash
# Run health checks
ao doctor
```

```bash
# Get health check results as JSON
ao doctor --json
```
