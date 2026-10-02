# Isolated desktop evidence

Date: 2026-09-29. Local only. Evidence root: `/var/tmp/pr-5769-desktop-final-79.yrWkBr`. Checkout: `checkout`, detached at `ec7efde0652f21690d8e98e2dcfa71d5b132e422`, plus the archived local delta and explicit frontend correction overlay. No commits, PR edits or publication.

The initial archive has 60 changed files and SHA256 `b593054b9c2b7e14ed3bec8796bef6e70e2aae7ba20c4e77e90fab3c90a8976a`. The 5758-file source manifest SHA256 is `97436bcf96947c38c34e11577508524e119d8a083bd68d82cb017b1391e8f582`. The private cloud gitlink is recorded, not fetched or imported. Frontend dependencies were genuinely installed in this checkout, not symlinked from another checkout.

The desktop-development skill required a real app, separate profile/data/run file, exact-process shutdown and inspected captures. The foreground launcher uses a private home and PID namespace, read-only host filesystem outside the task lab, and stripped credential environment. Its data/profile are under `/home/ghoul/.ao/desktop` inside that private home, backed only by the lab's `ao-home/desktop`. No real user data was mounted into that home.

Initial and full-restart daemon listeners used loopback ports 39677 and 40755. The later browser recovery run used 39617. Exact launch metadata and logs are retained. The real Electron user agent, preload bridge, fresh daemon/runner, successful app API calls and 30-entry provider catalog were observed. Each owned app was stopped through its foreground launcher; unrelated desktop instances remained untouched.

## Observed flows

- Empty saved-account inventory, disabled routing and truthful unavailable state. No invented account rows.
- Wrong-method synthetic token input rejected with HTTP 400 and `ACCOUNTS_MANAGER_CREDENTIAL_METHOD_UNSUPPORTED`; the public request ID is shown and the secret input cleared. No credential was saved or route enabled.
- Device login status remains separate from managed inventory. The requested provider is unauthenticated in scratch data; the initial task dialog therefore does not establish a positive managed/native account selection.
- Full app/daemon restart preserves settings, empty accounts, disabled routes, no sessions and catalog identities. Installation readiness is a fresh observation and was not assumed identical for every provider.
- The corrected browser flow accepts one operation, exposes its real HTTPS link, restores it after renderer reload, reopens without another POST, and displays a cancellation acknowledgement separately from observed terminal state. Each run ends with zero saved accounts.

The initial capture script used the wrong field locator; `capture.log` is failed harness evidence. `capture-corrected.log` uses the actual field label. A restart precheck initially expected Escape to dismiss a nested menu; the corrected precheck reloads the actual renderer. Neither error required a product change.

## Evidence and limits

`capture-result.json`, `restart-before.json`, `restart-after.json` and corresponding logs record assertions. Inspected `captures/first-window.png`, six `captures-corrected` screenshots and frames from `captures-corrected/local-controls.mp4`. The latter is a 1.52-second real-app recording. Browser recovery has three screenshots and a recording in each of `/var/tmp/pr-5769-browser-recovery-79.AAbpSt/desktop` and `desktop-reload`; the first recording is 3.08 seconds. These are native app captures, not component reconstructions.

The browser opener did not observably throw in the real app. Unit tests establish failure/clipboard/expiry handling. Actual provider authorization, positive quota, live A/B sessions, in-use removal, restart of authenticated sessions, and release responsiveness have not been demonstrated. No native Mac or Windows evidence follows from these Linux runs.

The exact 12-file browser correction, final Linux package build, 5626 frontend test passes (six pre-existing skips), 60 renderer smoke passes, typechecks and shared-package checks are in [the browser review](../browser-recovery/REVIEW.md). Its final manifest includes this report and the desktop gate ledger. Three desktop gates met, two remain open for eligible-account interaction and independent review. The parent product gates remain unchanged.
