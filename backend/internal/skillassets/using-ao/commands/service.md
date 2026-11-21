# ao service

Keep `ao serve` running across logins and reboots as a user service. It never needs `sudo`.

| Platform | Service |
|---|---|
| macOS | launchd user agent `~/Library/LaunchAgents/dev.agent-orchestrator.serve.plist` |
| Linux | systemd user unit `~/.config/systemd/user/agent-orchestrator-serve.service` |
| Windows | Not supported yet |

## Syntax

```
ao service install [flags]
ao service status [--json]
ao service uninstall [--json]
```

## Flags (`install`)

| Flag | Meaning | Default / Required |
|---|---|---|
| `--port int` | Loopback port the service's daemon uses | `AO_PORT`, else `3001` |
| `--no-wait` | Return once the service is loaded, without waiting for the daemon | - |
| `--timeout duration` | How long to wait for the daemon to become ready | `2m` |
| `--json` | Output the result as JSON | - |

## Notes

- The service runs `ao serve --foreground` with this shell's `PATH`, `AO_DATA_DIR`, `AO_RUN_FILE`, `AO_PORT`, `DO_NOT_TRACK` and `AO_TELEMETRY_*`. Run `install` again after changing them.
- The service restarts the daemon after a crash, and when the desktop app's own daemon quits with the app. `ao stop` stops it until the next login.
- If a daemon is already running when you install, the service watches it instead of starting a second one, and `install` says so.
- `install` and `uninstall` only touch a file that `ao service install` wrote. `uninstall` stops the service and removes that file.
- On Linux the service starts at login; `loginctl enable-linger` starts it at boot.

## Examples

```bash
# Install, then check it
ao service install
ao service status --json
```

```bash
# Remove it
ao service uninstall
```
