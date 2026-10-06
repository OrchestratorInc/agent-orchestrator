# Harness Maintenance Gates Design

## Goal

Close PR #5848's time-of-check/time-of-use race between harness session launches
and reinstall, update, or uninstall operations. Once maintenance begins, no new
same-harness launch may resolve or start the executable until maintenance and
post-operation verification finish. Once a launch begins, same-harness
maintenance must wait or fail before it enumerates sessions.

## Existing Problem

`StartAgentOperation` checks the current session list before starting an
asynchronous worker. Except for Droid and fx, `TryBeginHarnessUse` does not hold a
real lock. A new session can therefore start after the session-list snapshot and
race with replacement or removal of the executable it is launching.

## Design

Replace the Droid/fx-specific locks with lazily created per-harness
`sync.RWMutex` gates owned by `systeminstall.Service`.

- Session spawn, restore, and resume acquire the harness gate's read side before
  binary resolution or launch. The existing session-manager integration releases
  it after the launch boundary completes.
- Reinstall, update, and uninstall acquire the same harness gate's write side
  before session enumeration. The write-side release is transferred to the
  asynchronous operation worker and held until command execution and
  post-operation verification complete.
- Droid and fx install operations retain their current write-side protection
  because their installers can replace files used by sessions. First-time
  installs for other harnesses retain existing behavior.
- Gates are keyed by `domain.AgentHarness`, so maintenance for one harness does
  not block a different harness.

The active-job check remains under the service mutex and runs before attempting
the harness gate. Once the write gate is held, session enumeration is the final
authority: any non-terminated same-harness session rejects maintenance and
releases the gate without starting a worker.

## Error and Lifecycle Behavior

- If the write side cannot be acquired immediately because a launch is in
  progress, return `ErrHarnessActive` with the existing HTTP conflict mapping.
- If session enumeration fails, return the error and release the write gate.
- If operation planning, ownership verification, or worker setup fails, release
  the write gate exactly once.
- After the worker starts, every success, failure, cancellation, and panic-safe
  exit path releases the write gate exactly once.
- Service shutdown continues to wait for operation workers; it does not bypass
  the gate lifecycle.

## Testing

Deterministic backend tests will:

- hold a same-harness launch read lease and prove reinstall/update/uninstall are
  rejected before session enumeration;
- pause an update worker and prove a same-harness launch cannot acquire its read
  lease until the worker finishes;
- create a session before releasing a launch lease and prove the subsequent
  maintenance snapshot sees and rejects it;
- prove terminated and different-harness sessions do not block maintenance;
- prove two different harness gates can be held concurrently;
- preserve Droid and fx install protection; and
- cover setup/error paths that must release the write gate.

Run focused `systeminstall` and `session_manager` tests first, followed by the
backend full and race suites required by `AGENTS.md`.
