# ADR 0005: a local coordinator for mobile closed-lid wakefulness

Status: proposed; basic macOS 26 lid-close path verified; full physical validation pending.

## Context

Connect Mobile uses `caffeinate -i -w <daemon PID>` for idle sleep. Normal lid
sleep remains allowed. The new opt-in targets Apple Silicon MacBooks on macOS
13–27 without raising the desktop application's minimum OS. That range is a
capability gate, not a claim of completed physical testing.

Apple's process-owned lid-close assertion modifier requires a private Apple
entitlement. The SDK exposes `kPMSetClamshellSleepState` (selector 12) on the
root-domain user client. It is global and survives caller death, so cleanup
only on normal sidecar exit would leave the Mac awake after a crash.

## Decision

Use a fixed-purpose unprivileged Swift coordinator managed by the login's launchd
GUI domain. It has no network listener, no arbitrary commands, no administrator
setup, and no `pmset disablesleep`, sudoers or privileged-file mutation. The
daemon owns policy and mobile lifetime; Electron supplies the native resource
path. The renderer does not own recovery. Implement independently of the closed
main Amphetamine app and Insomnia's installer.

On explicit opt-in, copy the bundled helper to a content-addressed executable
under `~/.ao/data/wake`, then register its plist using `launchctl bootstrap`.
Keeping the executable outside the replaceable app bundle preserves recovery
across updates. Plist, private request files, status and journal also live here;
no files go into OS-default app-data or `~/Library/LaunchAgents`. After a
restart/login, restoring an enabled preference bootstraps an existing setup.
An unused mobile connection does not need an always-on login installation.

AO instances of the same login using the default durable data root (`~/.ao/data`)
share this coordinator. Custom or scratch `AO_DATA_DIR` roots cannot enable native
lid control: isolated roots cannot independently own the OS-global bit, and must
never write to production state or alter its power controls. Ordinary idle wake
remains available in those runs. A coordinator flock serializes helpers; a separate
setup flock serializes daemon registration. Only `wake/` and its children require
0700; the existing daemon state parent is neither changed nor rejected for 0750. Other logins and unrelated sleep
utilities do not participate in this protocol.

A registered protocol-compatible coordinator is reused regardless of the calling
build's digest. Setup never boots out a registered job or copies another version
while that job is registered. Incompatible protocol registration, or an older registered helper that cannot
pass the current host capability probe, fails with explicit upgrade instructions.
To upgrade the native helper, open the lid, disable the option in every instance,
wait for cleanup, and boot out the registration before opting in again. A fresh
registration cleans obsolete content-addressed helper copies. New jobs use
launchd's Standard process type; status freshness allows 30 seconds of timer/work
margin. Cleanup polling does not hold the status mutex.

Daemon request files contain a UUID, PID, start identity and advancing sequence.
Renew every 15 seconds; scan every two seconds; expire an unchanged sequence
after 60 monotonic seconds. Reject dead/reused process identities and stale
on-disk requests after helper restart. Status reports activation for each accepted
lease, so another daemon's lease is never mistaken for one's own.

Fsync ownership intent before native mutation. A restarted helper reconciles
owned state against fresh, identity-verified leases and safety conditions before
serving. It preserves an existing valid lease across a helper restart; clearing
the bit first while the lid is shut can immediately put the Mac to sleep. It
restores abandoned or unsafe ownership before accepting requests. A restart
does not extend an old request's remaining lease lifetime. No live leases, full quit, expiry or a
safety stop releases ownership. Normal termination attempts restoration;
launchd restarts a killed/crashed helper to recover. Failed restoration stays
journaled and is retried. A corrupt journal is a recovery error, not an empty
ownership record. A reboot clears kernel state but still reconciles the journal.

Only this coordinator manages AO's global override. Release clears its bit;
normal external-display docking remains macOS's responsibility. Never force a
replacement bit from an external-display-count heuristic. The bit has no owner
token, and its registry property updates only on lid/power events (including
stale values from AO's previous lease), so it cannot reliably identify another
writer or gate rearming. Concurrent lid utilities are not supported. Refresh
owned control for charger/powerd transitions, subject to real-device validation.

The causes-sleep registry property updates on lid/power events, not synchronously
with command acceptance. `lidActive` means the coordinator accepted the native
command for a live lease ("armed"), not that a physical lid test passed. User
sleep and screen-lock authentication remain macOS behavior.

Below 10% battery, unknown battery state, or critical thermal state, release lid
ownership and latch the lease. Renewals and helper restart do not clear a stop;
an explicit off/on creates a new identity. The ordinary idle assertion remains
separate. While lid-closed and undocked, a bounded `pmset displaysleepnow`
request darkens the display once per transition into an owned closed/undocked
state; failure stops lid control. Brightness restoration,
audio, Low Power Mode customization and app freezing remain phase two.

## Validation and release gate

Automated tests cover sequence expiry, multiple owners, PID reuse, clock changes,
journal write ordering, recovery failures, safety thresholds, API compatibility
and mobile-listener exclusion. CI builds arm64/x64 binaries and runs native
policy tests without changing power settings; only arm64 MacBooks pass the
runtime capability probe.

Local mechanism evidence: native arm64/x64 compilation with an 11.0 deployment
target, policy/recovery tests, and a read-only probe on a macOS 26.4 Apple Silicon
MacBook. This is not physical lid-close verification.

Reviewer-reported physical evidence (14 October 2026): Prasad tested a MacBook
on macOS 26. With the lid closed, the Mac stayed awake and the phone stayed
connected. The report does not identify the MacBook model, power source, or
whether interactive agent work was exercised. See the
[manual test report](https://github.com/OrchestratorInc/agent-orchestrator/pull/6589#issuecomment-6173318260).

| Scenario | Physical evidence |
| --- | --- |
| Basic lid close and continued phone connection | Passed on one macOS 26 MacBook, per reviewer |
| Interactive agent work; panel darkness and locking | Not yet reported |
| Battery operation and charger transitions | Not yet reported |
| Multiple daemons; disable and full quit | Not yet reported |
| Daemon/helper SIGKILL and recovery | Not yet reported; highest-priority acceptance check |
| Login/reboot and update/removal | Not yet reported |
| macOS 13/14/15/27 hardware coverage | Not yet reported |

macOS 27 is included in the capability range to permit validation on current
hardware. The reviewer confirmed selector 12 remains in the macOS 27 SDK and
reported that the former upper bound rejected a Mac16,7 running macOS 27.0.1.
This gate change is not a claim of completed macOS 27 physical testing. See the
[compatibility review](https://github.com/OrchestratorInc/agent-orchestrator/pull/6589#issuecomment-6172817303).

Before acceptance, verify interactive mobile agent work on macOS 13/14/15/26/27
MacBooks: lid closed on AC/battery, charger transitions, panel darkness, locking,
multiple daemons, disable/full quit, daemon/helper SIGKILL, login/reboot and
update/removal. Keep the PR draft until that gate passes or an explicit product
decision narrows the advertised range. Record unavailable machines honestly.

## References

- [Apple power assertion entitlement checks](https://github.com/apple-oss-distributions/PowerManagement/blob/main/pmconfigd/PMAssertions.c)
- [Apple kernel clamshell policy](https://github.com/apple-oss-distributions/xnu/blob/main/iokit/Kernel/IOPMrootDomain.cpp)
- [Apple IOKitUser SDK sources](https://github.com/apple-oss-distributions/IOKitUser)
- [Amphetamine documented behavior](https://apps.apple.com/us/app/amphetamine/id937984704)

Native protocol version 2 stores coordinator state inside the default durable
data root. An existing version-1 registration is never replaced automatically;
open the lid, stop all old wake sessions and unload its registration before
opting in again. Retain any unfinished legacy recovery journal under
`~/.ao/wake` until recovery has completed.
