# ao service

Keep `ao serve` running across logins and reboots as a user service. It never needs `sudo`. On Linux it keeps running while you are logged out only with lingering (see Notes).

| Platform | Service |
|---|---|
| macOS | launchd user agent `~/Library/LaunchAgents/dev.agent-orchestrator.serve.plist` |
| Linux | systemd user unit `~/.config/systemd/user/agent-orchestrator-serve.service` |
| Windows | Not supported yet |

With a custom `AO_DATA_DIR` or `AO_RUN_FILE`, the name gets a short hash suffix (for example `dev.agent-orchestrator.serve-1a2b3c4d`), so it never replaces the main service.

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
- Exception: when the watched daemon is the desktop app's own, the service cannot tell `ao stop` from the app quitting, so it starts its own daemon. To keep Orchestrator stopped in that case, run `ao service uninstall` first.
- If `scripts/setup-self-hosted.sh` already installed its host service (`dev.aoagents.self-hosted` / `ao-self-hosted.service`) for the same data directory, `install` refuses and prints how to remove that service; that service already keeps the daemon running. A different data directory is fine. `status` reports the self-hosted service when there is one.
- `install` and `uninstall` only touch a file that `ao service install` wrote. `uninstall` stops the service and removes that file.
- On Linux a user service starts when you log in and stops when you log out, unless lingering is on. `install` and `status` report it (`"linger"` in `--json`) with the command to turn it on, `loginctl enable-linger <user>`. On a headless server, turn it on so the service runs from boot.
- On Linux, `journalctl --user -u agent-orchestrator-serve` shows early `ao serve` failures; the daemon's own log is `daemon.log` in the data directory.
- Stopping or uninstalling the service stops the daemon only when the service runs it itself; it leaves running session processes alone, as `ao stop` does, so the next daemon can adopt them. A daemon the service only watches (the desktop app's, or one started by `ao serve`) keeps running after `ao service uninstall`: stop it with `ao stop`, using the same `AO_DATA_DIR`, `AO_RUN_FILE` and `AO_PORT`.

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
