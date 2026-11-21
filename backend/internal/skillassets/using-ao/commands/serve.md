# ao serve

Run the Orchestrator daemon without the desktop app. Use this on a server, in a script, or when you set up Orchestrator for a user and the app is not open.

`ao serve` first looks for a daemon that already serves this data directory. If one is ready, it reports it and exits 0 without starting another. Otherwise it starts the daemon in the background, waits until it is ready, and prints its URL.

## Syntax

```
ao serve [flags]
```

## Flags

| Flag | Meaning | Default / Required |
|---|---|---|
| `--background` | Start the daemon in the background and return once it is ready | default |
| `--foreground` | Run the daemon in this process, or watch the one already running, until no daemon is left | - |
| `--port int` | Loopback port for a new daemon; ignored, with a warning, when one is already running | `AO_PORT`, else `3001` |
| `--timeout duration` | How long to wait for a new daemon to become ready | `2m` |
| `--json` | Output the result as JSON | - |

## JSON output

`--json` prints one object. Paths are absolute. Fields are only ever added, never renamed or removed:

```json
{"status":"ready","port":3001,"pid":12345,"url":"http://127.0.0.1:3001","reused":false,"runFile":"/Users/you/.ao/running.json","dataDir":"/Users/you/.ao/data"}
```

`reused` is `true` when an existing daemon was found. Failures exit 1 with the reason on stderr; bad flags exit 2.

## Notes

- Stop the daemon with `ao stop`.
- If the run file names a daemon that serves a different data directory (only `AO_DATA_DIR` was changed), `ao serve` exits 1. Set `AO_RUN_FILE` and `AO_PORT` too for a separate daemon.
- When the desktop app opens, it swaps in its own daemon on the same data directory, which keeps running after the app quits.
- Usage analytics follow the app's **Share usage analytics** setting. `DO_NOT_TRACK=1` or `AO_TELEMETRY_REMOTE=off` turns them off.
- To keep the daemon running across reboots, use [`ao service install`](service.md).

## Examples

```bash
# Make sure a daemon is running, and get its port
ao serve --json
```

```bash
# Run a separate daemon for a scratch data directory
AO_DATA_DIR=/tmp/ao-scratch/data AO_RUN_FILE=/tmp/ao-scratch/running.json AO_PORT=3101 ao serve
```
