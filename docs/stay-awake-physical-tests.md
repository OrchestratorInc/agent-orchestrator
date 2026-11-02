# Stay Awake physical acceptance — colleague handoff

Status: **not performed by the implementation agent**. Automated policy tests
use fake power/device drivers; compilation and passing tests do not establish
physical lid or Low Power Mode behavior. Record tester, Mac model, Apple Silicon
chip, macOS build, AO build/commit, each result and any recovery notes. Missing
hardware/OS coverage remains **not run**, rather than passed.

## Current PR2 scope

Desktop/session awake controls, timers/app/AO-browser-download lifetimes,
automatic rules, closed-lid operation, battery/heat stopping, and automatic Low
Power Mode. Mobile access is not required. Other advanced UI is deferred in
[the follow-up document](awake-follow-up-controls.md).

## Low Power Mode authorization and ownership

1. On a test account, record `pmset -g custom` before testing. Keep the lid open
   for authorization and recovery. Cancel **Authorize…** in Stay Awake settings:
   no settings should change, no wake rule should enable, and cancellation should
   remain actionable. The UI must remain responsive while approval is pending.
2. Approve authorization. Verify `/etc/sudoers.d/ao-low-power-<numeric uid>` is a
   regular root-owned `0440` file accepted by `sudo visudo -c`. Its only new grants
   are `/usr/bin/pmset -b lowpowermode 0|1` and `-c lowpowermode 0|1` for that UID.
   Each command has a five-second sudoers timeout. No AO executable, shell,
   arbitrary arguments, or unrelated command is granted
   root execution. Authorization alone must not modify power settings.
3. Set a battery threshold safely above the current battery percentage, within
   10–90%, and start an awake request on battery. Confirm the Battery Power
   profile changes to Low Power Mode, while the AC profile remains unchanged.
   Check current effective mode in macOS settings as well as `pmset -g custom`.
4. End the request. Only AO-owned changes should restore. Repeat with Low Power
   Mode already enabled before starting: AO must not turn that user preference
   off. Repeat with simultaneous session/AO requests and two AO owners: ending
   one owner must not restore while another owner still requires the mode.
5. Verify battery hysteresis: recovery is three percentage points above the
   configured threshold. Plug/unplug the charger and verify profile-specific
   restoration and current-source behavior. Exercise threshold disable and
   serious-heat policy disable while a request is active.
6. Enable the serious-heat policy. Observe it during ordinary workloads if a
   serious thermal state occurs: it should affect only the current source and
   remain through fair until nominal. **Do not deliberately overheat the Mac**.
   If serious/critical heat is unavailable, mark physical thermal coverage not
   run; the injected automated tests cover the policy states. Critical heat must
   continue stopping wake requests.
7. While AO owns a change, turn Low Power Mode off yourself. AO must respect the
   detectable manual override for those leases. Record the unavoidable limitation
   that writing the same value as AO is not observable as a separate user action.
8. Kill/restart the daemon, then separately kill the native coordinator. Check
   lease expiry, independent launchd recovery and restoration. An unacknowledged
   native write or earlier-boot journal must report recovery required. The Low
   Power Mode recovery button requires confirmation and ends desktop requests;
   verify it never alters another live owner's settings.
9. Use **Revoke authorization…** after disabling Low Power Mode policies in
   other AO instances. Verify this instance saves both automation options as off
   before prompting, restores AO-owned changes, and preserves normal awake
   sessions. Another live power-policy owner or unresolved journal must refuse
   removal. Cancel the administrator dialog: automation stays off, the grant
   remains, and retry works. Approve it: only
   `/etc/sudoers.d/ao-low-power-<uid>` disappears, permission reports off, and
   repeated revocation is safe. Test concurrent authorize/revoke and attempts to
   re-enable policies while pending; the coordinator must keep lid/thermal
   monitoring responsive and cannot acquire new power claims during removal.
   Interrupt the helper during the dialog on the test account: new power claims
   must remain blocked until Revoke is completed again. Confirm reauthorization
   works afterward. If the app cannot run, use only
   `sudo rm -f /etc/sudoers.d/ao-low-power-$(id -u)` after owned changes restore,
   then `sudo visudo -c`. Preserve journals if permission unexpectedly disappears;
   reauthorize and recover before removing it again. This whole physical flow is
   pending colleague validation; automated tests never execute a grant/removal.
10. Test logout/login, normal quit, updater replacement and shutdown/reboot.
    Same-value or earlier-boot ambiguity must remain explicit. Preserve recovery
    journals until the changes are reconciled. On upgrade from helper protocol 2,
    3 or 4, follow ADR 0006's lid-open/off/unload/re-enable instructions.

## Core wake regression matrix

- Test AC and battery, charger changes, lid open/closed and docked/undocked states.
  Confirm the internal panel sleeps on lid close and screen-lock authentication
  remains required on reopening. Keep ventilation unobstructed.
- Test standalone use without mobile; session versus AO scope; simultaneous
  desktop/mobile owners; stopping one source while another remains active.
- Test indefinite, duration, end-at-time, app process exit/PID identity, and AO
  browser download completion/interrupted heartbeat. Verify expired sources end.
- On a Mac without the native guard (for example a lidless Mac or an isolated
  data directory), test a live session/app/download binding and automatic rule
  while already idle past the normal sleep threshold. Observe repeated renewal
  windows with `pmset -g assertions`; verify no idle-sleep interruption, normal
  expiry, display-policy changes, and cleanup after Stop/daemon exit. This
  fallback continuity test is pending physical verification.
- Test enabled automatic rules, pause/resume, network/device observation loss,
  schedule/timezone boundaries, and unknown battery readings.
- Test configured battery stop and injected critical-heat policy in automated
  tests. Do not intentionally exhaust the battery or force dangerous heat.
- Test daemon/coordinator SIGKILL, stalled coordinator, last owner disappearing,
  an update with an old registered helper, logout and shutdown. Confirm normal
  sleep and owned settings recover; preserve and report uncertain state.

Cover representative supported macOS versions on Apple Silicon. Core wake is
capability-gated for macOS 13–27; a successful compiler target does not substitute
for that device matrix. Do not test the deferred optional app/audio/network/
browser actions as though their Settings UI shipped in this PR.
