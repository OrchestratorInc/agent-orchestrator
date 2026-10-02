# Public controls: client midpoint

OWNS: backend/internal/cli/accounts_manager*.go, backend/internal/cli/session_account*.go, backend/internal/cli/root.go, backend/internal/cli/session.go, backend/internal/telemetrymeta/cli.go, docs/plans/accounts-manager-completion/PC3-MIDPOINT-GATES.md, docs/plans/accounts-manager-completion/REVIEW-82-PC3.md

Scope: complete the thin command-line boundary, then stop for independent midpoint review before mounting desktop controls or changing runtime admission. The latest continuation permits this independent slice while the guest design is reviewed. No publication.

## Design review and inventory

The corrected PC2 API covers session binding and switch start/status/retry/cancel, removal impact and coordinated removal status/retry/cancel. Existing account routes cover inventory, sign-in/cancellation, import, refresh, rename and disable. The independent PC2 re-review is CLEAR at `/tmp/pr-5769-pc2-rereview-82.txt`. All thirteen PC2 source files remain frozen. No contract changes are planned in this midpoint. The latest direction explicitly limits this increment to CLI; no UI implementation or desktop execution is included.

Use `ao session account` for per-session selection and `ao accounts` for managed inventory and maintenance. Require explicit target, timing, observed revision and operation ID for switching. Removal requires the observed impact revision, explicit confirmation and a stable operation ID; revision zero is valid when explicitly supplied. Recovery addresses the original operation, never selects a replacement account and never substitutes the old direct DELETE route.

Credential input is bounded standard input only, never command arguments. Sign-in instructions appear only in the explicit login response, not inventory/diagnostic output. Shared HTTP transport remains the boundary. Decode only public response fields. Suppress raw daemon error text for these commands while retaining the standard error code and request ID. Do not infer readiness from catalog availability or enable missing control dependencies.

Desktop inventory/add/login/refresh already exist in the managed settings section. Missing desktop work: session picker, committed versus pending state, switch recovery, impact-bound removal, recovery operation visibility and stale-response guards. The existing removal button still uses direct DELETE and must be replaced during the reviewed desktop slice. Session integration, real desktop evidence, provider checks and performance acceptance remain open. No protected native hooks or Subscriptions edits are needed for this midpoint.

## Order and acceptance

1. Verify preserved source inventories, reproduce the retained session CLI failure, and add account command boundary regressions before production edits.
2. Implement the smallest HTTP-only commands. Review identity checks, error output, secret input, confirmation and recovery semantics before broader tests.
3. Run focused race checks, CLI package verification, build/vet and changed-scope lint. Recheck preservation and freeze exact files and evidence for reviewer82.

- [x] C1: failed-first session and account command regressions compile and fail on missing commands, with request counts and safe output assertions.
  EVIDENCE: isolated Go tests in backend CWD, Fish shell, exit 1 for each expected pre-implementation failure. `session-red.log` SHA256 `737be7d5aed3f0e5733e0a165c754cd16c66768202ba89833fea8e7f61d2b2b0`; `accounts-red.log` SHA256 `7687cb9500943110bf998d3866f397d9184d91a154194a773a88fb61e39ebb80`, under `/tmp/pr-5769-pc3-79.SHopnR`. The retained session test bytes remain unchanged.
- [x] C2: session get/switch/status/retry/cancel delegate only to managed routes with explicit choices and exact response ownership.
  CHECK: go test -mod=readonly -p=1 -race ./internal/cli -run '^Test(SessionAccount|ManagedAccounts|TelemetryMetaClassifiesRegisteredCommandPaths)' -count=3 -timeout=3m -v
  EXPECT: ok
  CWD: backend
  EVIDENCE: final combined focused command, isolated environment, backend CWD, Fish, exit 0, three repeats, 2.299s. `final-focused-race.log` includes exact session/operation response ownership, nested pending identity, both modes and policies, request-ID preservation, correlated errors, repeatable recovery and actual HTTP-router/controller round trips. No runtime was started.
- [x] C3: inventory/login/add/import/refresh and coordinated removal commands preserve explicit intent, secret-safe diagnostics and recovery IDs.
  CHECK: go test -mod=readonly -p=1 -race ./internal/cli -run '^Test(SessionAccount|ManagedAccounts|TelemetryMetaClassifiesRegisteredCommandPaths)' -count=3 -timeout=3m -v
  EXPECT: ok
  CWD: backend
  EVIDENCE: same final focused race log, exit 0. Account command tests cover explicit zero removal revision, omitted/negative/unsafe revisions, confirmation, foreign removal ownership before and after mutation, empty recovery bodies, bounded stdin, exact-size input, redacted errors, login state and all removal phases. The combined run has 19 distinct top-level tests and 119 distinct named subtests, each repeated three times; those are separate counts, not additive independent scenarios. No skips or race warnings.
- [x] C4: affected verification passes on one source snapshot, and protected, lifecycle, guest and PC2 inventories remain identical.
  CHECK: go test -mod=readonly -p=1 -race ./internal/cli ./internal/telemetrymeta -count=1 -timeout=5m
  EXPECT: ok
  CWD: backend
  EVIDENCE: final affected race run exited 0 (CLI 46.932s, metadata 1.006s). Full backend build and full vet exited 0. Pinned changed-scope lint reports 0 issues. In-memory frontend contract regeneration is byte-identical; no API/generated file was edited. Final SHA256 audit passed for 113 lifecycle/design files, 91 protected files, the five-entry guest review package and 13 PC2 source files. The 12-file PC3 source manifest and archive match every live byte. `final-preservation.json` SHA256 `48e9ebac884b2d77f0796a04ade954a720e5d82d4b387d3c82f8e27af8e6bbf1`. Exact commands, environment and evidence paths are in REVIEW-82-PC3.md.
- [ ] C5: independent midpoint review clears this boundary before desktop and daemon integration.
  EVIDENCE: pending. Native Windows execution, guest containment, production capability wiring and real desktop/provider evidence remain release gaps.

The sealed guest design, lifecycle records and prior review handoffs are excluded from this slice. This ledger supplements, rather than rewrites, the earlier frozen public-control ledger.

## Midpoint self-review

The untrusted parked draft was compared against the corrected HTTP DTOs and routes before use. It lacked nested pending-session ownership validation and returned raw transport/daemon error text. The live candidate adds the nested identity guard and shared safe error projection, preserving typed daemon status, error code and bounded request ID. Credential input never uses arguments, and recovery has no default account selection or native endpoint fallback.

Initial public command matrix passed. A human-output regression then demonstrated that all four native/managed and drain/interrupt combinations omitted the mode and policy. `midpoint-output-red.log` preserves the four failures; the corrected renderer includes modes, policy, conversation choice, phase and recovery state. The expanded three-repeat race matrix passed, followed by actual HTTP-router/controller round trips with synthetic dependencies.

The first full CLI race check failed only `TestTelemetryMetaClassifiesRegisteredCommandPaths`: the command registry did not classify 22 new command paths. The existing test supplies the failed-first evidence in `cli-full-race.log`. The fix adds exactly those static paths to the registration metadata, without changing telemetry payloads or classification defaults. This small integration file is not in any protected or frozen inventory. Final checks must include its package and supersede the initial full-package failure.

## Freeze

Source root: `/tmp/pr-5769-pc3-79.SHopnR`. The source manifest is `pc3-source.sha256`, 12 entries, SHA256 `f282f65e6b2dc98131a90ff10e72a6f6b5aa711b0decdbfaf0e97d1a7272e523`. It includes the unchanged retained CLI regression plus eleven newly added or modified files. The source archive has the same twelve entries and hashes, SHA256 `c4ddddb034fdbd01ed5bbc715f3eada389c78de2101cc13959c11282a2885c04`.

Gate count: four met, one awaiting independent review, none abandoned. Implementation stops here. No UI, guest/lifecycle, desktop, commit or publication work follows this freeze without the required review direction. These CLI checks do not certify native runtime or full-product completion.
