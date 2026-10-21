# Local testing target

`New()` prepares the pinned revision in its warm checkout and launches the real,
unpackaged Electron executable. It builds that revision's Go daemon into the
private attempt directory and passes its exact path through `AO_DAEMON_COMMAND`.
Electron manages that owned daemon. Static Forge/Vite assets avoid
extra Vite and HMR listeners. This is not an Electron package or installed app.

## Start from a PR URL

From an AO worker, run `ao testing start --pr <https://github.com/owner/repo/pull/number>`.
AO infers the project, snapshots metadata with `gh`, fetches the exact base and
fork-head SHAs into the warm cache, and saves the full pinned PR patch plus its
SHA-256 outside the repository. Intake does not launch a target or change HEAD.
One comparison worker starts base and head with `ao testing leg start base|head`.
The `local-ao` recipe is available without a manual checkout environment setting.
Manual issue/run setup still accepts `AO_TESTING_TARGET_CHECKOUT`.

## Reuse a warm target

The controller runs preparation automatically before launch. For manual setup
diagnostics from this worker checkout, run:

```sh
python3 backend/internal/skillassets/testing/scripts/prepare.py --commit <base-sha>
# After the base attempt has completed cleanup:
python3 backend/internal/skillassets/testing/scripts/prepare.py --commit <head-sha>
```

The script removes inherited `AO_*` variables before every subprocess. It
keeps one checkout per repository at `~/.ao/dev/agentic-target/repos/{repo-hash}/checkout`.
Use `--repository <path>` for a different source checkout. It clones once,
then fetches and checks out each exact revision in the same folder. Tracked
edits and an active target reservation prevent preparation. It never runs
`git clean` or deletes `node_modules`. Each package's last installed lockfile
hash is retained beside the checkout; `npm ci --prefer-offline` runs
when that hash changes or dependencies are missing. Go/npm use the user's normal cache settings.
The controller builds Forge/Vite assets and an attempt-owned daemon without
packaging. Workers may run checks in the owned checkout. Do not symlink dependencies.
Preflight checks the effective Go version against `go.mod`, the Node version
against any `package.json` engine constraint, and the revision's actual launch
resolver with an explicit daemon command. Unsupported revisions retain the
exact reason before Electron starts. The launcher requires an explicit private
port other than 3001 and checks the live daemon's recorded and kernel identity
before startup, after readiness and after cleanup. The manifest pins the revision, hashes
the frontend launch artifacts and records those runtime facts. For manual
issue/run setup, set `AO_TESTING_TARGET_CHECKOUT` to the printed checkout path.

## Slice B wiring

Pass one `TestingTargetSpec` with:

- `AttemptID`: one safe path component, used for a fresh private state directory.
- `Generation`: positive ownership generation.
- `CheckoutPath`: the absolute prepared checkout path under
  `~/.ao/dev/agentic-target/`.
- `CommitSHA`: exact full SHA recorded in `frontend/.vite/testing-target.json`.
- `StateRoot`: empty for the default, or the exact
  `~/.ao/dev/agentic-target/{AttemptID}` path.
- `Deadline`: a future time. Startup also has a three-minute limit.
- `RecipeSnapshot`: JSON. The optional `visualMarker: true` fixture adds a badge.
  Other recipe fields belong to the testing service.

Keep the returned identity even when startup fails so cleanup can be retried.
The adapter keeps ownership in memory and does not reattach after a restart.
Slice D binds a window using `ElectronPID` and its exact kernel start time.
The window identifier is accepted back with the same launch identity.

All inherited `AO_*` values are removed. Owned overrides select fresh data,
run file, Electron profile, loopback port, launch ID and fake harness. Telemetry
is disabled. Allowed origins are `app://renderer` and the private loopback
origin, avoiding the dev helper's opaque `null` origin for static assets. Tool callers cannot select a target endpoint or log file.
Readiness checks kernel identities, the run file, `/readyz` executable, startup cwd and live data cwd,
and successful projects and sessions GETs. Log output is bounded and remains
readable after cleanup. ReadLogs reads only captured files and does no process
inventory or PID lookup. It includes optional data/daemon.log and
data/logs/daemon.log sinks, using a cursor per file. The current managed daemon
logs through Electron stdout, so those optional sinks can be absent. No fixture oracle is exposed by either observation port.

## Visual fixture and action

The generated main-process wrapper creates a random `SCREEN-` marker and inserts
a badge outside React after the shell WebContentsView renderer loads. Its private oracle is
`fixture-oracle.json` in the attempt directory. It never logs the marker.
The fixed success log only says that the fixture rendered. The fixture is
opt-in and does not change frontend source. Projects/sessions queries cannot
return DOM text, and `ReadLogs` reads only `target.log`.

A later investigator can click the AO Settings gear and observe the Settings
pane in a new screenshot. Slice E's proof takes one screenshot and sends no
input. This action remains unverified until the later input test.

## Cleanup and proof

`Stop` captures descendants, signals only exact captured process identities,
and checks process absence, native windows, the port and run file. Reused PIDs,
failed observations, surviving processes and foreign run files fail cleanup.
Typed process.ErrNotRunning identifies missing and zombie processes. These
descendants are gone; a bare ESRCH/EIO lookup needs another inventory to confirm
absence. A descendant disappearing before signal delivery is also gone. Events
during inventory are noted in target.log. Live PID reuse still prevents signals.
The Electron main and daemon remain required for ordinary live observations.
`target_control` can explicitly stop only the owned daemon. In that state,
observations require the same Electron birth identity, positive daemon absence,
an absent private listener and no foreign run file. Restart uses the owned
Electron's existing daemon-start handler through private attempt files. The
replacement must pass the same descendant, executable, cwd and run-file checks.
Logs and retained evidence remain. After absence checks pass, only the owned
data, Electron profile, fixtures, setup scripts and daemon binary are removed;
the checkout and normal
caches remain. A checkout reservation prevents another preparation or launch
from changing live code. No kill-by-name or kill-by-port is used. A stale owned
run file is removed only after other absence checks pass.

Default tests use injected process operations and HTTP transports. The live
proof is opt-in, macOS only, and needs the previously installed Cua Driver. Run:

```sh
python3 - <<'PY'
import os, subprocess
for key in list(os.environ):
    if key.startswith('AO_'):
        del os.environ[key]
os.environ.update(LOCAL_TARGET_LIVE='1', GOFLAGS='-p=2', GOMAXPROCS='2')
subprocess.run(['go', 'test', '-p', '2', '-v', '-run', '^TestLiveTargetProof$',
                '-timeout', '8m', './internal/adapters/testingtarget/local'],
               cwd='backend', check=True)
PY
```

It launches one target, records memory pressure and owned-process RSS, verifies
that the marker is absent from API results and logs, captures exactly one target
window through a private Cua Unix socket, and records cleanup. Inspect that PNG
before claiming the marker is visible. Evidence stays beneath `~/.ao`; copy
requested report artifacts out separately. This proof starts no Cua MCP server.

## Target terminal windows

Call `target_control` with `arm_terminal` immediately before the target's native
terminal trigger. Call `admit_terminal` afterward, within two minutes. Admission
requires exactly one new Terminal.app window with one tab, the target's private
state paths in its visible launch command, its native window ID, PID birth time
and tty processes. Existing windows, ambiguous additions and unrelated commands
are refused. Baseline inventory does not read existing terminal contents.

Admission selects that terminal for the ordinary observe/click/type/key tools.
It receives its own Cua binding and window recording in the same attempt journal.
`read_terminal` retains bounded text from that exact window and tty. `select_app`
returns observation and input to the owned Electron window. A fresh observation
is required after switching. The controller records terminal tty process births,
terminates only those captured processes and closes only the admitted window at
cleanup. Terminal.app itself and its other windows never become owned.

Terminal may retain a hidden native window after its last tab closes. Inventory records that placeholder only when the tab list is null or empty and visibility is explicitly false. Cleanup accepts this positive closed state for the exact admitted window and original PID birth. Hidden windows with live tabs and unavailable metadata still fail closed. Placeholders cannot be admitted for input.

For hosts with several macOS Spaces, the supervisor can opt into
`AO_TESTING_STICKY_WINDOWS=1`. The recipe snapshots this setting and the owned
Electron makes only its own windows visible on all workspaces, including
fullscreen workspaces. It changes no host app assignment or global Space. Cua's
PID, window, current-Space and focus checks still run without exceptions.
