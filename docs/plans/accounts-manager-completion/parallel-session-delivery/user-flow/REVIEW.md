# Desktop launch correction and product handoff

Scope: external developer launcher only. No production runtime, protected path, earlier source archive or guest design changed. The active remaining-work list is in PLAN.md; GATES.md deliberately retains the incomplete product outcomes.

Evidence: `/var/tmp/pr-5769-user-flow-79.nBnXAa`.

## Failed-first and correction

`launcher-red.log` records a real daemon readiness assertion failing with `not_installed` instead of `installed`. The installed executable was under the personal home, which the previous launcher deliberately masked. The executable was also absent from its restricted PATH.

The correction resolves the installed executable, rejects non-regular/non-executable or other-user-writable inputs, and mounts only that file read-only into a scratch executable directory. The personal home remains replaced by the existing scratch home. The launcher strips ambient authorization and installed-app identity variables. It uses the same scratch app data and preserves the old launcher and logs.

`launcher-probe.log` executes the real installed binary inside the boundary, proves the personal worktree path is inaccessible and the mounted executable is not writable. `launcher-unit-final.log` records three passing cases covering symlink resolution, narrow mount scope, rejected inputs, environment isolation and unchanged caller state. An initial fixture incorrectly used a randomized directory basename; its original failure remains in `launcher-unit.log` and the corrected directory fixture preserves the production assertion.

## Midpoint self-review

- Changed log creation to exclusive synchronous opening before spawn, so a duplicate launch fails before starting another app.
- Kept executable access distinct from credentials. No token, personal configuration, login store or full executable directory was copied.
- The readiness assertion alone does not establish the desktop. The first renderer was killed during startup; the daemon continued serving correctly. The failed native connection is preserved in `desktop-check.log`. No kernel out-of-memory event was observed in the bounded diagnostic, but the cause is not established.
- Reloading the existing native renderer restored it. `desktop-check-reload.log` verifies the Electron preload bridge and the actual Harness/Accounts panels. Both screenshots were inspected. This is recovery evidence, not a claim that the startup failure is fixed.
- The generic existence probe contains no credentials or model request. Three unit cases are not presented as the full product suite. Real A/B sessions and usage still require authorized accounts and the remaining implementation.

## Running test instance

Foreground execution session: `22486`. Host launcher PID: `2149978`. Boundary PID: `2149985`. Daemon: `127.0.0.1:43429`. Renderer: `http://localhost:5173/`, inside the real Electron window. Runtime and support source matched the current worker tree in the prior 5,523-file read-only comparison.

Physical scratch account data: `/var/tmp/pr-5769-desktop-final-79.yrWkBr/ao-home/desktop/data`. Physical Electron profile: the sibling `electron` directory. Inside the boundary these resolve below `/home/ghoul/.ao/desktop`. Personal accounts and the installed app remain untouched.

The original launcher handoff left the app at Accounts for user-authorized test sign-in. Subsequent testing used the three accounts added by the user. The current live results, restart outcome and retained sessions are recorded in LIVE-RESULTS.md. Check current app state before restarting it again.

## Outstanding dependencies

The independent containment and integrated review request was relayed to the orchestrator with delivery handle `am79-live-blockers-review-capacity-20260929`. Receipt is accepted but provider delivery and reviewer action are not confirmed. It is not a review verdict.

The five positive retirement failures, production containment integration, other-provider managed Chat/profile/token migration, native Windows and both Mac platforms remain open. Actual account tests now establish bounded A/B, usage and Chat outcomes, but expose model selection, exhausted-account status and pending-switch restart defects. They do not close the complete workflow gate. The last remote inspection reported main conflicts with no checks in its rollup; this testing pass did not refresh or change the PR. No publication occurred.
