# Gates: explicit retry after switch interruption

Scope: M03 public recovery controls, separate from the immutable shutdown correction. No historical terminal operation is reopened. No runtime ownership, credential, containment, native switching or Subscriptions behavior changes.

Decision: expose `canRetry` as a read-time observation, never a stored session status or admission token. The manager reports true only for an idle, non-cancelled reservation matching the observed operation, an open manager lifetime, and requested/waiting/recovery_required phase. The session service delegates through an optional reader. Unsupported readers return false. Retry still rechecks its existing durable and local admission guards. The UI requires an explicit true observation and cannot infer eligibility from phase alone.

Order: failed-first HTTP and manager tests; minimal manager/service/projection wiring; midpoint self-review; renderer and CLI boundary tests; generated contracts; focused races and UI tests; full affected checks and independent review. Existing shutdown source, test files and archives remain unchanged. New evidence is kept separately under a session-owned directory.

- [x] G1: the unchanged HTTP projection fails to expose retry for a recovered requested/waiting operation; preserve failed-first output.
  EVIDENCE: /var/tmp/pr-5769-retry-controls-79.fGWJZO/red.log, ordinary race test invocation, exit 1 with 12 missing-field HTTP assertions and 14 unavailable-manager-capability assertions. ui-red.log has four expected failures for missing pre-stop retry and phase-only retry inference. cli-red-final-fixture.log has exactly four missing-output assertions against the original CLI implementation. The earlier two CLI red logs also rejected the routine invocation telemetry request and are diagnostic only.
- [x] G2: manager capability rejects active, cancelled, foreign, missing, terminal and shutdown reservations, and allows exactly the idle recovered reservation. Actual SQLite reopen and cancellation/retry controls pass.
  CHECK: env GOTOOLCHAIN=go1.27.1 GOMAXPROCS=2 go -C backend test -p 1 -race -count=3 -timeout=6m ./internal/session_manager -run '^TestAccountsManagerSwitchRetryCapability'
  EXPECT: /ok\s+.*internal\/session_manager/
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79; path=73b5dbbe765e/37 entries; output=ok  	github.com/aoagents/agent-orchestrator/backend/internal/session_manager	24.547s
- [x] G3: session service and public HTTP responses propagate the capability without exposing runtime identity, changing operation state or weakening mutation ownership checks. Missing capability fails closed.
  CHECK: env GOTOOLCHAIN=go1.27.1 GOMAXPROCS=2 go -C backend test -p 1 -race -count=3 -timeout=6m ./internal/service/session ./internal/httpd/controllers -run '^(TestAccountsManagerControlRetryCapability|TestAccountControlRetryCapability.*)$'
  EXPECT: /ok\s+.*internal\/httpd\/controllers/
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79; path=73b5dbbe765e/37 entries; output=ok  	github.com/aoagents/agent-orchestrator/backend/internal/service/session	1.011s | ok  	github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers	13.979s
- [x] G4: desktop controls offer explicit retry for recovered pre-stop work, hide it for active/terminal/unknown states, preserve operation IDs and request IDs after conflict, and never optimistically change the committed account. CLI mirrors the public field.
  EVIDENCE: final-ui-focused.log, 50 passing renderer/client tests. CLI human/JSON controls pass under race in final-focused-corrected.log. desktop-recovery-cancel.json records a real waiting operation surviving restart, visible Retry, and authoritative cancellation preserving source revision 35 and five unrelated bindings during cancellation. Live retry execution is explicitly false and remains outside this observed result.
- [x] G5: focused races, complete affected packages, frontend tests/typecheck, build/vet/lint, generated drift and 91 protected plus five guest-record hashes pass. Native-platform and live-provider results are not inferred from synthetic fixtures.
  EVIDENCE: RETRY-CONTROLS-REVIEW.md records the exact final-snapshot commands and retained failures. All affected package reruns, full frontend, both typechecks, Linux desktop package, backend build/vet/lint, API parity/generation/drift and preserved hashes pass. final-process.log adds all four direct/fallback retry/cancel process cases repeated three times, 92.805s. This closes only the bounded local checks, not native-platform or live Retry execution acceptance.
- [ ] G6: midpoint self-review and independent review cover observation/admission races; exact source/evidence manifests and remaining gaps are recorded. Actual desktop behavior requires real Electron evidence before publication of this UI slice.

The prior push proposal contains only the three shutdown source/test files and six existing readiness documents. This separate slice is excluded from that proposal.

## Midpoint self-review

The initial 26 manager/HTTP cases pass three times under race detection in initial-green.log. Fifty renderer/client tests pass in ui-green.log. This is bounded synthetic evidence, not independent clearance or live-provider acceptance.

- The capability inspects the manager lifetime and matching local reservation under existing locks. It performs no I/O, launches no worker and writes no journal. The phase comes from the already ownership-checked durable operation read.
- The observation can become stale immediately. Existing retry admission, cancellation compare-and-swap and exact-owner teardown remain mandatory and unchanged. The renderer keeps the original operation ID and committed binding after a conflict.
- Active workers, cancelled reservations, another operation in the same session, absent reservations, shutdown and terminal/unknown phases return false. Startup must reconstruct the reservation before the public action appears.
- Unsupported service/manager implementations return false. Older daemon responses without the new field remain readable, but the renderer cannot infer permission to retry. Malformed field types are rejected.
- The first expanded run passed the manager, service and HTTP checks but exposed a CLI fixture error. Initial investigation attributed the extra request to readiness, but the diagnostic identifies POST /internal/telemetry/cli-invoked. The final fixture accepts that exact instrumentation request and GET /healthz, and rejects every unexpected account request. The original implementation was restored temporarily to capture a clean missing-output regression, then the two-line CLI correction was reapplied. The account mutation assertion remains intact.
- Historical failed-record repair remains open. Neither this projection nor the shutdown patch supplies missing durable pre-stop proof for old terminal records.

The frozen shutdown package remains unchanged. Final local verification and actual desktop evidence are recorded below; independent review remains open.

## Final-snapshot observations

Source seal: `/var/tmp/pr-5769-retry-controls-79.fGWJZO/retry-final`. Sixteen files, including two generated contracts. Manifest SHA256: `7a83c56699cd5c0024e266e18391a747aeefa3e902d193afbbd3b982ae1e7a42`. All 91 protected paths, five guest records and the original three shutdown files match their preserved manifests.

- The final full frontend run passes all 352 files: 5,647 tests passed, seven existing skips, 581.91s. Both TypeScript configurations, API generation/drift and the complete Linux desktop package build pass. The original run without the fixture zip tool and the later single diff-view timeout remain recorded; the unchanged diff-view file passes isolated and in the final full run. No timeout was increased and no unrelated test was edited.
- The first affected backend race run has two manager failures: a worker settlement timeout and a SQLite lock error. Their four-case isolated selection passes three times. The complete manager rerun passes on unchanged source. These are retained stability findings, not proven root causes or production fixes.
- Real Electron runs from the isolated desktop checkout with scratch data and the existing real catalog/accounts. The daemon binary was rebuilt from this worktree; the three renderer/contract files were mirrored exactly. No other lab source was overwritten, and the lab source tree is not claimed to equal this entire branch.
- The idle restart check preserves six bindings. The active Chat check creates a waiting operation, restarts the daemon and observes the same operation and `canRetry=true`. Restart closes the dialog, so the first harness wait for an already-open Retry button times out. Reopening the actual controls displays Retry. Cancellation preserves the original account/revision and does not remove credentials. A four-second, 16-frame recording and two inspected screenshots capture the actual desktop interaction.
- Explicit Resume then restores the existing Chat controller, preserves all six turn IDs and completes the pending turn. desktop-resume-verified.json confirms the expected bounded reply, ready controller, cancelled switch and unchanged revision. This does not prove exactly-once external tool execution, every queue schedule or cold runner/vault restart.
- Two terminal-input attempts produce no observed activity or switch operation. The initial terminal displayed its native settings screen. The terminal automation result remains unresolved; it is not counted as a working terminal retry flow.
- Actual live Retry execution, native Windows and Mac checks, historical failed-record repair, containment/deletion completion and independent review remain open. No credentials or runtime identities were added to the public projection. No publication occurred.

The executable verification commands, exact hashes and independent review request are recorded in RETRY-CONTROLS-REVIEW.md. Five gates have current bounded evidence. G6 remains open until independent review names this seal. There are no abandoned gates.
