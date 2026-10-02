# Durable diagnostic display

Direct captures from the real Linux Electron app on 2026-09-30. The detached checkout contains published `b0378b674` plus the reviewed cold-retry correction and the 25-file public projection slice. Its dependencies were installed with `npm ci`; the production daemon, bundled runner and real provider catalog are used. Home, application data and browser state are isolated. No credentials were inherited.

The session and removal records are explicitly synthetic durable SQLite fixtures. No component mock, intercepted API response or replacement UI is used. These captures establish diagnostic rendering, persistence across an app/daemon restart, and authoritative cancellation readback. They do not demonstrate live provider execution, in-use credential deletion, successful account switching or account usage.

- `switch-durable-cause.png`: after restart, the pending operation retains its diagnostic and offers the daemon-observed retry/cancel controls. The committed selection remains native.
- `removal-durable-cause.png`: after restart, removal remains recovery-required, with a diagnostic and no cancellation or completion claim.
- `cancelled-no-stale-cause.png`: the same switch is cancelled through the public route; durable readback confirms revision 1 and the original native binding. No stale diagnostic or replacement account is shown.
- `durable-diagnostics.mp4`: 2.04-second direct desktop recording of the switch and removal diagnostic surfaces before restart.

The narrow compact popup has an existing unwrapped retry/cancel row, visible in the restart capture. This slice does not alter that layout. All images and representative recording frames were inspected. No secret, real account identity or private endpoint appears in the published captures.

Capture scripts and results: `/var/tmp/pr-5769-errors-desktop-79.KrysKu`. The first cancellation capture expected 200 instead of the documented 202; its failure is preserved, and corrected readback verifies the successful mutation without resetting the fixture. Earlier navigation attempts selected a stale menu or the disabled submit button. Those script failures are not product test passes. The final before-restart and cancellation/removal readback checks pass.
