# Gates: deletion after persistent host parent death

Scope: the original provider group must be proven stopped before deletion is acknowledged, even after a replacement publishes and removes its own ownership files. Preserve the existing frozen review package and all 91 protected paths. No public controls, UI, or publication.

Current HOLD: preliminary independent review at `/tmp/pr-5769-deletion-d1-preliminary-review-82.txt` identified a late-fork census race. All v3 validation is diagnostic, including the partial `accepted-full-race-final.log`. Its owned test command was stopped without affecting other services.

- [x] F1: A channel barrier after process enumeration lets the recorded leader fork an unlisted same-group child and exit. Neither teardown, normal completion, nor retry may acknowledge stopped while the child survives. The red ordinary Go test is `/tmp/pr-5769-deletion-recheck-79.UWxSzb/late-fork-unit-red.log`: three invariant failures before the correction. The only pre-fix production edit was the nil-by-default observation hook.
  EVIDENCE: `late-fork-composed-race-v3.log`, exit 0, 54.310s, both census paths passed three times under race. Frozen persistent-host full race also passed in 54.684s. Independent review remains pending.
- [x] F2: Compose real host-parent death and durable coordinator SQLite reopen. No stop acknowledgement or credential-removal boundary call on inconclusive ownership. Preserve replacement and unrelated native processes.
  EVIDENCE: `late-fork-composed-race-v3.log`, two replacement states passed three times under race. Each physically reopens SQLite, constructs cold coordinators, retains the blocked selection and proves completion only after original-group absence. Uses a finalization spy, not a live vault/provider credential.

Decision: a zero-member census is advisory. A stopped transition also requires a kernel group-existence probe returning ESRCH (or a different boot identity). This probe is non-destructive. A surviving but unanchored group retains the obligation and is never signalled by group number. Signalling still requires an exact recorded birth identity and a pidfd recheck. A second census alone is not a proof.

Bounded D2 freeze: persistent-host full race passed 54.684s; focused coordinator deletion recovery passed 38.516s (25 leaf scenarios, three repeats, 75 executions); backend and runner scoped lint each report 0 issues. Source remained unchanged across checks. All 91 protected paths match the baseline directly, all 32 generated files match the captured inventory, formatting and diff checks pass. Exact manifests and verification are in [REVIEW-82-DELETION-D2.md](REVIEW-82-DELETION-D2.md). The broader H1-H4/R1-R2 checklist below and product release gates remain open; these bounded checks do not replace a full final-snapshot run.

Plan:

1. Reproduce parent death, surviving provider descendants, replacement publication, and replacement shutdown with actual owned processes. Preserve the failing log before changing production code.
2. Persist launch-specific, non-secret teardown evidence independently of the reusable host slot. Require exact original group termination evidence, retaining uncertainty when identity cannot be proven. Preserve unrelated processes.
3. Review crash cuts and identity reuse midway. Recheck attachment, cold retry, missing/corrupt evidence, normal shutdown, and existing deletion behavior under race.
4. Reverify the affected full suites and process matrices. Audit all protected paths and freeze new correction manifests without overwriting historical evidence. Return to independent review.

- [ ] H1: The exact reported crash sequence fails before the correction and passes afterward without stopping replacement or unrelated processes.
  CHECK: go test -mod=readonly -p=1 -race -count=3 -timeout=180s ./internal/adapters/chatdriver/persistenthost -run '^TestRemovalHostCrash' -v
  EXPECT: /^ok\s+.*internal\/adapters\/chatdriver\/persistenthost\s/m
  CWD: backend
  EVIDENCE: pending

- [ ] H2: Full persistent-host, affected Chat, and coordinator suites preserve ownership, queue, revocation, and cancellation behavior.
  CHECK: go test -mod=readonly -p=1 -race -count=1 -timeout=900s ./internal/adapters/chatdriver/persistenthost ./internal/adapters/chatdriver/acp ./internal/service/chat ./internal/session_manager ./internal/daemon
  EXPECT: /^ok\s+.*internal\/daemon\s/m
  CWD: backend
  EVIDENCE: pending

- [ ] H3: Repeated actual-process deletion recovery preserves unrelated direct and fallback runtimes.
  CHECK: go test -mod=readonly -p=1 -race -tags=e2e -count=3 -timeout=600s ./internal/session_manager -run '^TestAccountsManagerRemovalReal' -v
  EXPECT: /^ok\s+.*internal\/session_manager\s/m
  CWD: backend
  EVIDENCE: pending

- [ ] H4: Build, vet, scoped lint, compatibility checks, protected audit, exact manifests, and the independent handoff describe one verified snapshot with explicit platform gaps.
  EVIDENCE: pending

Execute the checks through the reviewed isolated-environment wrapper with private scratch paths. The original deletion freeze is historical evidence, not proof of this correction. Missing identity or unsupported platform proof must never be reported as confirmed provider death.

## Runner model ownership correction

The isolation failure is a production ordering defect. The SDK starts accepting
requests before its late model refresh. That refresh derives API-key models from
configuration, which deliberately contains no provider credentials. It removes
the runner's vault-backed registrations. A registry-event barrier reproduces the
loss after actual startup without relying on a sleep.

Decision: the runner owns the credential execution manager and its model catalog.
The SDK service retains an empty, non-persistent manager for its built-in executor
construction. Before serving requests, copy the supported executor references to
the vault-backed manager and attach that manager to the public request handlers.
Start and stop its refresh worker explicitly. SDK startup and catalog refreshes
then have no managed credentials to overwrite. Keep exact route selection and
vault admission unchanged. Do not copy credentials into configuration, relabel
credential kinds, retry across accounts, or edit engine/native paths.

- [ ] R1: The startup-barrier isolation regression fails before correction and
  passes repeatedly under race, including account deletion and runner restart.
- [ ] R2: Missing executor setup fails closed. Unsupported routes remain blocked,
  encrypted persistence and exact selection remain enforced, and full runner
  race/build/vet checks pass after the final edit.
