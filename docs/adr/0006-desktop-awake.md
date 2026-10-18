# ADR 0006: desktop Stay Awake ownership

Status: implementation; physical acceptance pending.

## Intent and controls

Settings contains a dedicated Stay Awake page using the current grouped settings
layout. Its controls create indefinite, timed, end-at-time, running-app, or typed
AO-browser-download requests. The Summary inspector offers a quick indefinite
request for its session or for AO. Session and board toolbars offer an AO-wide
request. Info controls open the Stay Awake settings page. Mobile access is
not required. Turning off one scope never withdraws another scope's request or
Connect Mobile's lease.

Detailed native observations run only for active policy or while the Stay Awake settings page
requests them. An unused feature does not launch observation processes. The
daemon owns requests, settings and rules in a versioned singleton SQLite
aggregate. Its `/api/v1/awake` routes are loopback-only and blocked from LAN and
managed mobile tunnels, including with valid mobile authentication. Electron
only feeds typed download state; it sends IDs, never transfer URLs or filenames.
The renderer displays observations but does not decide when native ownership
expires or restore system state.

## Lifetimes and observations

Manual requests end after normal daemon shutdown. Startup durably marks
previous manual requests interrupted; it never resurrects an indefinite request.
Finite deadlines use Go's monotonic clock after creation and a native monotonic
remaining deadline after first observation. A request bound to a session,
application PID/start identity, download, or automatic rule carries a native
maximum deadline fifteen seconds after its latest successful policy check.
The single aggregate native lease takes the earliest finite contribution deadline,
even when an unrelated idle-only request is indefinite or has a later deadline.
This conservatively expires all aggregate ownership if policy reconciliation
stalls, rather than letting independent sequence renewals extend stale closed-lid
permission or other policy effects. Healthy reconciliation refreshes valid bound
contributions and removes expired ones; remaining idle-only ownership continues
with its own deadline (or no deadline when indefinite). Unchanged native sequences
still expire after sixty seconds, as in ADR 0005.

Application exit and PID reuse end an app request. Failed probes provide neither
positive ownership nor proof of termination. Download heartbeats expire after
twenty seconds; losing the heartbeat is recorded separately from completion.
Rules support all/any composition across agent activity, schedules with IANA
timezones, power source, battery level, network CIDR, connected volumes,
external display count, running applications, uptime and AO browser downloads.
Activation requires matching observations spanning five seconds. Unknown or
stale observations immediately withdraw automatic ownership. Active battery
conditions have three percentage points of hysteresis. Operator pause and
critical safety stops require explicit resumption; a manual request does not
silently resume paused rules. If a safety-latch save fails, ownership remains
stopped and reconciliation retries the pending document write, even after
observations recover. A successful save makes the acknowledgment latch durable.

On hosts without the native guard, idle-only wake uses `caffeinate` with an
OS-enforced timeout and daemon-PID ownership. Rolling policy deadlines reuse the
running process until its remaining timeout enters a 7.5-second renewal margin,
between the first and second normal policy ticks. A replacement starts
before the old assertion retires, with at most five seconds of overlap capped by
both policy deadlines. Shortened deadlines cap all pending retirements; Stop and
failed replacements release every owned idle process. Display-policy changes use
the same bounded handoff. Physical idle-sleep continuity still needs hardware
acceptance; fake-process tests do not exercise macOS assertion acquisition.

## Deferred action scope

This release rejects non-default app pausing, audio mute, display/keyboard
brightness/restoration, saved-network recovery, agent reconnect and browser-action
settings, regardless of host capability. Startup clears and durably saves opt-ins
from older PR builds before admitting automatic rules; failure to persist that
reset aborts service initialization. Deferred capabilities report unavailable.
The browser-action HTTP route, operation schema and frontend action are absent.
Internal backend/native foundations and restoration of previously owned changes
remain for the follow-up; they cannot be enabled through this release's settings
API. Core display-awake assertions and Low Power Mode are supported separately.

## Native protocol and recovery

Protocol 5 extends the existing unprivileged launchd coordinator rather than
installing a second owner of the global clamshell control. Each desktop daemon
has a lease separate from its mobile lease. Idle and display wake use
process-owned IOPM assertions. Closed-lid ownership is independent of optional
audio and app effects; legacy mobile leases omit the new flag and retain their
closed-lid meaning. A coordinator watchdog terminates a stalled coordinator so
launchd can replay its already-fsynced journals. Scratch/custom data directories
cannot join or install this coordinator.

A protocol-2, protocol-3 or protocol-4 registration is not silently replaced. Open
the lid, end awake requests and disable Low Power Mode policies in every AO
instance, allow owned changes to restore, and run:

```sh
launchctl bootout gui/$(id -u)/com.aoagents.mobile-wake
```

Then enable the requested feature again. The same exact procedure is included
in upgrade errors. Preserve unfinished recovery journals and complete pending
Low Power Mode recovery after upgrade before revoking authorization.

Below the configured battery threshold (minimum 10%), unknown native battery
state, or critical thermal state, the helper latches affected leases and releases
ownership. The daemon persists the stop and requires fresh safe observations
plus explicit Resume. Normal display sleep and lock-screen authentication
remain macOS responsibilities; this feature does not unlock an authenticated
screen.

Optional closed-lid audio muting and selected background-app pausing use a
separate private recovery journal. Audio ownership records the device UID and
original mute value. Restore never targets a different default output and
respects detectable user changes. Pausing records same-user PID/start identities
before SIGSTOP, then acknowledges successful application. AO, agents, terminals,
IDEs, supported browsers, foreground apps and recording activity are protected.
App groups with protected descendants are skipped. Groups and journals are
bounded. Prepared but unacknowledged pauses after a crash require the explicit
recovery confirmation before SIGCONT. Already stopped apps are not adopted.
Expired and abandoned effects restore before releasing owned lid control.
Unrecoverable or unreadable state is preserved and reported, never erased as
though cleanup succeeded.

Hotspot recovery is opt-in and limited to saved macOS Wi-Fi profiles. Credentials
remain with macOS. Only a known disconnect lasting ten seconds permits an
attempt; attempts have a thirty-second cooldown and a three-attempt budget per
outage. Stopping wake, disabling recovery, another working network, or user
network changes cancel handoff. Outcomes are durable; an unconfirmed association
is never reported as success.

Browser relaunch is opt-in and separately confirmed because it closes windows.
Only installed Chrome and Brave bundles satisfying their expected Apple code
signing/team requirements are supported. AO/agent-managed browser processes are
excluded; a browser that refuses graceful termination is not force-quit.
Requests journal intent before relaunch. Replaying an uncertain request ID cannot
close the browser again automatically. Tab recovery belongs to the browser.

## Settings surface and unfinished capabilities

The current settings page exposes session modes, uptime, lid/display defaults,
automatic activation, battery/heat protection and Low Power Mode controls. Optional display/keyboard/audio
effects, app pausing, hotspot recovery, agent reconnection, browser relaunch,
and explicit recovery/history operations have backend/native contracts but no
settings controls in this version. Their presence in the API is not a claim that
the desktop settings expose the complete original PR2 feature list.

## Low Power Mode and native acceptance

**Revoke authorization…** durably disables both Low Power Mode policies, updates
this owner's live lease, requests owned-change recovery, and then uses an
administrator dialog to remove only the account's exact sudoers fragment.
Canceling or failing removal leaves automation disabled. Another live power
policy or unresolved journal blocks removal; user-owned power preferences remain
untouched. The coordinator and authorization actions share a nonblocking power
lock, so no new claim can race removal while lid/thermal monitoring continues.
A fsynced revocation barrier survives a helper interruption; complete Revoke
again before authorization or new power claims can resume.
Protocol 5 is required for this locking guarantee; older registered helpers need
the documented upgrade before revocation. Core awake sessions remain available.
The uninstall FAQ documents the action and the manual fallback if AO cannot run.

Automatic Low Power Mode uses an explicit macOS authorization step. It installs
one root-owned sudoers fragment granting only four exact `pmset` on/off commands
for the current UID's battery and charger profiles. Each root command has a
five-second sudoers timeout. The AO executable remains unprivileged. The approved
installer uses fixed OS commands; the policy loop never prompts and receives no
arbitrary-command grant. Authorization only installs permission; already-saved
active policies may resume afterward. The UI exposes a battery threshold, serious-heat toggle, current effective mode and explicit
recovery. Battery activation has three-point hysteresis; serious heat holds
through fair and releases at nominal. Critical heat still stops wake requests.

The shared native coordinator owns a separate fsynced Low Power Mode journal.
It records intent before mutation, verifies success, never adopts a pre-existing
on preference, and restores only AO-owned changes after their last owner ends.
A detectable manual off suppresses reapplication for those leases. Unknown/High
Power profiles are refused. Unacknowledged writes and previous-boot claims
require explicit recovery; failed restores preserve the journal. Writing the
same value as AO is not distinguishable as a separate user change. The permission
fragment persists independently of awake sessions; removal and revocation recovery
are documented in [the physical test handoff](../stay-awake-physical-tests.md).

Display-brightness and keyboard-backlight controls use runtime-gated private
APIs only on macOS 26–27; older supported macOS versions retain core wake/lid
support. Keyboard selectors must match their measured ABI. Missing symbols,
unsupported hardware and missing fresh open-lid brightness samples prevent
mutations. A journal records original brightness, display UUID/keyboard identity
and boot UUID before dimming. Reopening restores only same-identity AO-owned zero
values; detectable user changes are preserved. Reboot identity mismatches remain
explicit recovery warnings. The private bridge declarations are informed by
[Insomnia's source](https://github.com/krishhgg/Insomnia/blob/main/Sources/Insomnia/System/DisplayPower.swift),
with its MIT notice included beside the helper. These paths still need physical
validation.

Guarded agent reconnection is limited to a positively observed transport loss
from an AO-managed Chat controller. Explicit close, termination, hibernation,
agent/interface switching and changed controller generations exclude recovery.
With the option enabled, an active awake request and a known recovered network
permit one durable attempt per interrupted generation. The session manager
rechecks durable ownership under its normal mutation gate. The Chat controller
gate consumes the original transport-loss evidence before provider I/O; explicit
stop invalidates it. Recovery uses only
`ReconnectOnly`; it never restores a worktree, starts a replacement provider or
manufactures a new prompt. Provider adapters without a reconnect-only contract
fail explicitly. Uncertain attempts are not automatically replayed. Terminal
agents and independently managed CLI conversations are not restarted.

The remaining advanced Settings controls are deferred to a future PR; see
[the follow-up scope](../awake-follow-up-controls.md).

Automated tests and compilation do not establish physical MacBook behavior.
The ADR 0005 matrix remains a release gate: AC/battery and charger transitions,
closed-lid panel darkness and screen lock, overlapping owners, daemon/helper
SIGKILL, helper stall recovery, login, updates and shutdown. Optional pause,
audio, network and browser actions need their own device tests, including
user changes, recording, crashes between journal and mutation, and failed
restoration. Do not infer macOS 13/14/15/27 device coverage from compilation.

## Optional service startup failures

If the persisted Awake document cannot be read or decoded, uses an unsupported
version, or cannot durably end interrupted intent, the daemon logs a warning and
disables the Awake service for that run. Awake routes return 501; other daemon
services continue starting. No wake policy loop or host action starts, and the
original document is retained for repair or a compatible newer build rather than
being reset. Restart after correcting the storage/document problem.
