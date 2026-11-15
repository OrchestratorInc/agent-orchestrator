# Desktop release architecture

Canonical desktop releases are conducted from the private
`Untrivial-ai/ao-releases` repository. That conductor is the only system allowed
to publish stable, nightly, or preview releases. The public
`Untrivial-ai/agent-orchestrator` repository supplies source and unsigned build
artifacts; it does not hold signing credentials or publish canonical releases.

Do not create release tags or try to publish from this repository. Operators
start and monitor releases in the private conductor, whose runbook contains the
authorized commands and recovery procedures.

## Release flow

At a high level, the conductor:

1. selects a public source commit and records its full SHA and release version;
2. dispatches `.github/workflows/build-artifacts.yml` in this repository with
   that pinned SHA and version;
3. downloads the resulting unsigned, short-lived workflow artifacts and
   `digests.json`, then verifies every artifact against the recorded digest;
4. performs signing, macOS notarization, packaging, and updater-feed generation
   in the private release repository;
5. uploads the complete release as a draft;
6. runs remote verification against the draft artifacts and feeds;
7. publishes the verified release atomically; and
8. updates Homebrew where the channel requires it.

Publication does not proceed when a digest, signature, notarization result,
feed, artifact set, or remote verification check differs from what the
conductor expects. A failed run is resumed or replaced from the conductor; it
is never bypassed by publishing directly from this repository.

## Public unsigned build boundary

`.github/workflows/build-artifacts.yml` remains intentionally dispatchable. It
accepts an explicit public ref/SHA and version, builds the four supported
desktop targets, and uploads unsigned workflow artifacts plus their SHA-256
digests. It has read-only repository permissions, does not use release signing
secrets, and does not create, edit, or publish GitHub Releases.

The private conductor pins the immutable source SHA before dispatch and treats
the returned digests as the handoff boundary. Signing credentials and release
publication permissions stay in `Untrivial-ai/ao-releases`.

Windows installers follow the same boundary (#4502): the NSIS maker
(`frontend/makers/maker-nsis.ts`) activates electron-builder code signing only
when signing credentials are present in the environment, so the public build
stays unsigned while the conductor signs with its own credentials downstream.

## Channels

- **Stable** releases are deliberate production cuts. After all verification
  gates pass, publication updates the stable updater feeds and the Homebrew
  distribution metadata.
- **Nightly** releases are conductor-scheduled builds from the configured public
  source line. They remain prereleases and update only nightly feeds.
- **Preview** releases are conductor-requested builds for an isolated candidate
  or change. They remain prereleases on their own preview channel and cannot
  replace stable or nightly feeds.

Version selection, channel naming, retention, and promotion policy belong to
the conductor. `frontend/package.json` may be stamped during an unsigned build,
but changing it in this repository is not a release trigger.

## Artifact verification

The conductor owns the authoritative verification and publication gates. For a
local diagnosis of a downloaded macOS artifact, use the repository's supported
verification script:

```bash
frontend/scripts/verify-mac-artifact.sh <artifact>
```

The script preserves the archive's code-signing metadata while extracting and
runs the expected signature, Gatekeeper, and notarization checks. Do not use a
plain `unzip` result to judge a macOS signature.

Stable macOS releases continue to include both a DMG for first installation and
a ZIP for electron-updater. The ZIP and `latest-mac.yml` must remain available:
electron-updater cannot install an update from a DMG. Nightly and preview
channels omit the first-install DMG.

The conductor chooses asset names. Since OrchestratorInc/ao-releases#50 the
stable DMG is `Orchestrator-darwin-<arch>.dmg`, and the conductor rejects the old
`agent-orchestrator-darwin-<arch>.dmg`. After each stable is published,
`release-latest-guard.yml` fails if an asset linked from `README.md`,
`translations/` or the docs `InstallDownloads` table is missing. It does not
check the Grok setup skill, orchestrator.inc or other docs pages.

## Local lifecycle logs

Two more local records sit in the same `logs/` directory (normally `~/.ao/logs`):

- `desktop-lifecycle.jsonl`: Electron main's start (with the Linux cgroup and
  OOM score), quit sequence and process exit code, uncaught exceptions and
  unhandled rejections, `render-process-gone` and `child-process-gone`, a
  `previous_run_unclean` record when the last run never reached a clean exit,
  and every daemon spawn, exit (code, signal, uptime), restart decision and
  cancellation. A respawn's reason is written before the respawn.
- `daemon-lifecycle.jsonl`: the daemon's start, supervisor-link loss, the
  shutdown cause (signal name, request, or server error) and the error `Run`
  returned. `daemon-crash.log` receives Go's fatal crash output (panics,
  runtime faults such as SIGBUS) through `debug.SetCrashOutput`.

Each JSONL file rotates at 2 MiB and keeps two older generations; the crash log
rotates at 1 MiB. Files are mode `0600`. Records hold fixed event names and
scalar facts; error text is scrubbed of URLs, credentials, long opaque tokens and
long quoted strings, and the home directory is shown as `~`. Prompts and terminal
output are never written. Nothing is uploaded.

On Linux, `AO_DAEMON_SCOPE=systemd` (experimental) starts the daemon in its own
transient user scope in `app-ao.slice`, so the daemon, tmux and agents leave the
desktop app's scope. `AO_DAEMON_MEMORY_HIGH` / `AO_DAEMON_MEMORY_MAX` (systemd
values such as `12G` or `80%`) limit that scope. Without a reachable systemd user
manager (Flatpak, containers, non-systemd distros) it raises the daemon's
`oom_score_adj` to 300 instead, which agents inherit. The probe uses the same
slice and memory properties as the launch; if a scoped daemon still exits before
it is ready, AO stops using the scope for that run and retries once with the
`oom_score_adj` fallback.

## Local updater diagnostics

AO writes updater decisions to `logs/updater.jsonl` beside `staged-update.json`
(normally `~/.ao/logs/updater.jsonl`, using the configured run-file directory).
These local records survive relaunches even when a packaged GUI
launch discards stdout/stderr. They are not sent through renderer telemetry.
The log retains one rotated file, `updater.jsonl.1`, with each file capped at
1 MiB and created with mode `0600` on macOS/POSIX. Network URLs and authorization
text are redacted; native error messages, stacks, codes, and causes remain for
diagnosis. Treat the files as local diagnostics and inspect them before sharing.

For a preparation failure, preserve both files before retrying. Correlate their
timestamps, process IDs, running/target versions, and operation/request context
with `~/Library/Caches/dev.agent-orchestrator.desktop.ShipIt/ShipIt_stderr.log`
and `ShipItState.plist`. `native-preparation-watchdog` identifies the exact
timeout branch and last progress sample; `native-error` preserves an original
native error before its message is rewritten for the UI. Preparation samples
include elapsed/inactive time, bytes, free space, and update-directory creation
time. Probe records include read errors and traversal-budget exhaustion. Late
native completion records whether AO accepted it.
`power-suspend` / `power-resume` and process uptime help correlate a timeout
with laptop sleep or a wall-clock change. The macOS preparation watchdog pauses
on suspend, excludes sleep from its elapsed-time caps, and grants a fresh
inactivity window on resume. Its active-time limits remain unchanged.

A watchdog timeout does not cancel Squirrel's native staging. A later native
success for the same owned target and channel clears that timeout and restores
the install confirmation. It never resumes an install request that already
failed. A native error or discarded target invalidates this recovery. If native
staging remains blocked, the dialog offers **Relaunch AO**, which starts a fresh
process rather than trying to reset Squirrel in place.

## Isolated macOS differential v2

Current AO uses full-ZIP macOS updates. It explicitly sets
`disableDifferentialDownload = true` before checks. The local rollout stop is
false and the v2 signing keyring is empty. Windows/Linux updater and feed
behavior is unchanged. No bridge release is part of this design.

Legacy macOS feeds permanently remain free of `blockMapSize`, blockmap or v2
references, and conventional `.zip.blockmap` assets. Old clients derive that
conventional suffix regardless of Developer Mode and may seed their next cache
cycle even after full fallback. Their discovery paths must remain empty.

The separately gated v2 resolver uses signed `ao-diff-v2-mac.json` and versioned
`.zip.aoblockmap` assets on the same GitHub release. Those names are never
requested by legacy clients. No public build/feed/publish hook generates them.
A future independently reviewed conductor change must verify exact signed
metadata and asset inventories without relaxing the legacy prohibition.

The v2 MacUpdater subclass uses the dependency's declared protected extension
and exclusively owned handles. Range failures, including 416, settle before
MacUpdater starts one full fallback. Stock 6.8.9's unsafe differential worker
is never invoked. Current AO remains on the stock full-only path.

See [the v2 protocol and acceptance contract](mac-differential-v2.md) for exact
schema, naming, signing, generation/verification, rollback and runtime limits.
The production client reauthorizes marked final-cache hits against a fresh signed
manifest and byte-verifies them before handoff. Cancellation remains terminal
through the protected downloaded-event boundary immediately before Squirrel.
The feature stays locally disabled until real packaged macOS acceptance and the
private conductor inventory, signing, draft-redownload, and rollback gates are
complete. No public repository workflow publishes or activates v2 assets.

## Incident rule

Exactly one publisher is a correctness requirement. If the conductor is
unavailable or a release is partially staged, stop and recover through the
private runbook. Do not dispatch a public publishing workflow, create a
substitute release, mutate an existing release, or publish from a second
identity.

## macOS repair installer

The repair `.pkg` is a separate, user-initiated installation path for Macs whose
installed updater cannot complete an update. It replaces
`/Applications/Agent Orchestrator.app` using macOS Installer; it does not invoke
Electron's updater, download a payload at install time, or touch AO user data.
It is an additional release artifact: the existing ZIP and update feeds remain
required. It does not add a remote repair button to old installed versions.

Users must quit AO before running the package. If AO's ShipIt installer is
still running, the repair must wait; restart the Mac and run the repair before
opening AO. The installer must not terminate processes or install concurrently
with ShipIt. After installation, users open AO normally. Installs in custom
locations and unreadable or unrelated destination bundle metadata require
separate support; the repair must not guess which app to replace.

### Conductor integration required before distribution

The public packaging script is `frontend/scripts/build-mac-repair-installer.mjs`.
It consumes an already signed, notarized, stapled app and verifies the app with
`verify-mac-artifact.sh`. The production path requires a **Developer ID
Installer** identity, distinct from the **Developer ID Application** identity
used for the app and DMG, and a notarytool keychain profile. Follow the script's
usage for exact arguments. Unsigned testing packages are not user downloads.

Example invocation from the private conductor after configuring its keychain:

```bash
node public/frontend/scripts/build-mac-repair-installer.mjs \
  --app '/path/to/Agent Orchestrator.app' \
  --output '/path/to/dist/Agent.Orchestrator-repair-arm64.pkg' \
  --identity "$APPLE_INSTALLER_SIGNING_IDENTITY" \
  --keychain-profile AO_REPAIR_NOTARY
```

The builder refuses an existing output file. It verifies the input, copied app
and expanded package payload before notarizing the signed package. Neither the
public CI test job nor this command publishes artifacts.

The private conductor must:

1. Import the Installer certificate/private key into its temporary keychain and
   configure its notarytool profile. Keep credentials in the conductor.
2. Invoke the script from the pinned public source checkout after each app is
   signed, notarized and stapled. Build each intended channel and architecture
   explicitly; do not replace the stable download with a nightly repair.
3. Include the resulting signed/notarized package in the artifact inventory,
   local and remote verification, checksums and atomic publication. Account for
   the additional notarization submissions in the job timeout.
4. Provide a direct, architecture-appropriate download link from the AO website
   and recovery announcement. This avoids GitHub navigation but still requires
   users to download and run the package. Do not expose the link before its
   artifact is published and verified.

At implementation time, the conductor imported the Application certificate;
Installer signing was not wired into its workflow. Source support in this PR
alone does **not** produce or publish a usable recovery download.

### Repair release gates

An unsigned package build/expansion and mocked guard tests establish packaging
structure, not a working signed recovery. Before distribution, test a signed
candidate on isolated Macs or VMs, on Apple Silicon and Intel:

- Upgrade from stable 0.12.10 and 0.12.11 and the reported nightly builds; confirm
  the chosen target version launches and existing AO data is preserved.
- Repair the same version with damaged app contents; reject a newer installed
  version, wrong architecture, unrelated destination and symlink destination.
- Reject active AO and active AO ShipIt, including AO quit triggering a staged
  update. The repair must not race that update.
- Verify the package's signature, Gatekeeper acceptance and staple with
  `verify-mac-artifact.sh`; verify the expanded and installed app with the same
  script. Do not run an unsigned fixture on a user's Mac.
- Confirm the repaired installation can subsequently update through the normal
  updater, including filesystem ownership after macOS Installer runs.

Test the normal old-version-to-candidate update path too. A repair download is
only necessary for affected installs that cannot complete that path.
