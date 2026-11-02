# Side-chat reliability: issue #6564

Base: upstream `464fa1ee943ddc2b5fd356c6b6ccb1ffe6ab9af8`.
Rebased before PR onto upstream `beaf8950b` (the two subsequent storage fixes).
Branch: `codex/fix-6564-side-chat-reliability`.

Severity measures user impact; complexity measures implementation and verification
effort. Both use a scale of 1–10. No P0 is established.

| ID | Issue | Priority | Severity | Complexity | Status |
| --- | --- | --- | --- | --- | --- |
| SIDE-01 | Stale running-turn recovery blocks its queue | Historical P1 | 8 historically | 4 | Existing fix verified through public recovery API; not reproduced on this base |
| SIDE-02 | Side-local commands duplicate delivery | P2 | 6 | 7 | Frozen source-owned handoff implemented; regression tests pass |
| SIDE-03 | Side-local commands omit staged files | P2 | 5 | 5 | Files/images included in frozen receipt; regression tests pass |
| SIDE-04 | Close loses cleanup ownership during a crash | P2 | 6 | 7 | Electron cleanup ledger and closing-tab retry implemented; focused regressions pass |

## Phase 1

The integration regression serializes an older running snapshot, restores it via
`RecoverSideChatLaunch`, and supplies fresh terminal history from the resumed
provider. The completed turn is settled and only the queued question dispatches.
An unknown outcome visibly fails recovery, preserves queued work, and sends no
prompt. Both cases pass on Windows; no production recovery change was needed.

## Phase 2

Source-side commands reserve submission ownership before preparing attachments.
Their version-1 draft retains a creation key, destination, and frozen message
receipt. Lost replies and remounts reuse those identities and the original files,
images, and supported references. New draft edits remain editable, while another
submission waits for the original handoff to finish. Cross-side references are
rejected before a destination is created.

## Phase 3

Electron pins a minimal cleanup-only record before DELETE. Snapshot refreshes
cannot replace it with an older open record. Uncertain failures keep a closing
tab and retry the same side. Definitive rejection restores the saved open record;
confirmed host shutdown hides the tab while native-fork cleanup remains owned.
Renderer IPC summaries contain only the side ID, session ID, and label.

## Phase 4 validation

Passed on Windows with Node 24.15.0 and Go 1.27.1:

- Backend build and vet; daemon development build.
- All side/boundary Chat tests, including stale-running launch recovery.
- 58 focused frontend tests across seven files: handoff ownership, files/images,
  lost replies, remounts, session changes/unmount, ordered drafts, closing state,
  already-closed reconciliation, and translation coverage.
- Renderer production build, main/preload bundle compilation, and E2E TypeScript
  check. Bundle compilation does not establish native Electron startup.
- SQL and API regeneration with no content drift; changed Go formatting.
- Pinned golangci-lint restricted to new issues reports zero issues. This is not
  a pass for the complete lint gate.

The complete backend and Chat suites fail on this Windows environment. The Chat
suite's four failures are unchanged tests involving invalid Windows fixture paths,
symlink privileges, and a POSIX `0600` assertion. Broader backend failures also
include missing POSIX shell commands and platform-specific fixtures. The complete
lint run reports 2,036 issues (mostly CRLF-sensitive goimports plus unchanged
Windows code findings) and times out. These gates have not passed.

Race checks cannot run with CGO disabled and no C compiler. Local workflow
validation fails because native Windows tar cannot open `/dev/null`; Docker's
Linux engine is also unavailable. Native macOS checks require a macOS runner.

The isolated Electron lab uses `~/.ao/dev/fix-6564/{data,electron}` and its own
run file. Launch is blocked by rebuilding `better-sqlite3`: Electron 44.7.0 has
no matching prebuilt binary and Visual Studio C++ build tools are absent. No
native-window or real-provider crash-window verification is claimed. No lab
Electron process remains running.

A broad frontend run before final handoff hardening finished with 8,738 passing
tests, 215 failures across
26 failing files, 10 skipped tests, and one worker error. Failures include native
macOS fixtures, POSIX permissions/sockets and Windows path differences, plus the
unchanged ContextMeter/ChatWorkspace assertions expecting US grouping while this
machine uses `en-IN` (`258,400` versus `2,58,400`). The complete ChatWorkspace
suite separately passes 211 of 212 tests, with only that locale assertion failing.
These are reported failures, not a green frontend gate.
The affected suites were rerun after hardening and all 58 tests pass.

The first main frontend typecheck found an optional tab-ID error, corrected by
checking the active ID in the retry handler. The subsequent full typecheck passes;
an incremental recheck covers the final hardening changes.

Developer Mode gating and launch-scoped recovery remain unchanged. Performance,
visual redesign, and long-history reconstruction are outside this branch.


## Ronish review follow-up (#7088)

Rebased the existing six commits onto upstream main
`c7a99e1ad83c0ea9bfc84e35f679fa2a08e6169f`. Other PR branches and review
checkouts are unchanged.

- P1: idle command handoffs now offer Cancel handoff. The current draft, files,
  references and newer edits stay intact. Cancellation records a version-1
  `cancelRequested` marker before clearing ownership; retries/remounts finish
  cancellation without dispatching the frozen request. Failed persistence or
  capture remains retryable. Subsequent questions get fresh identities.
- P2: one Electron-main recovery controller owns each daemon attachment. Claim
  retains its five-second timeout; restore has sixty seconds. Network errors,
  HTTP 408/429 and 5xx retry at 1/2/4/8/16/30 seconds, capped at thirty seconds.
  Permanent rejection stops retrying. Retry bodies stay frozen within the
  attachment, and stale responses cannot mutate the replacement's state.
- Cleanup ownership is retained before DELETE. A successful DELETE does not wait
  for its background snapshot to finish; 404 reconciliation still requires a
  fresh same-daemon snapshot. The existing five-second backstop remains singular.
  Captures share one read plus a follow-up for newer mutations. Recovery adds no
  healthy-state polling or repeated provider/history reconstruction.

All 89 focused frontend tests pass across
seven suites. Targeted Go tests confirm idempotent restore, one provider resume,
closed-side tombstones and stale-running-turn recovery. Frontend/E2E typechecks,
renderer build, Electron main/preload bundle compilation, Go build/vet and API/SQL
contract drift checks pass. The native Electron lab was attempted with its own
npm installation and isolated AO data/profile, but better-sqlite3 rebuilding is
blocked by missing Visual Studio C++ build tools. No full-app or real-provider
crash-window pass is claimed. Full Windows suites have platform limitations; see
[the PR validation notes](https://github.com/OrchestratorInc/agent-orchestrator/pull/7088)
for complete-suite counts and final-head CI results.
