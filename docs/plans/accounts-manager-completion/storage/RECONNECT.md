# Identity-checked reconnect increment

Contract: only a direct provider sign-in establishes replacement identity. Imported identity claims and matching email addresses cannot authorize replacement. Capture the account generation and credential fingerprint before starting sign-in; commit only if both remain current and the provider identity/context matches. Advance generation on replacement, preserve the account ID/index/label/disabled state, and fence old refresh results. A concurrent refresh winning first causes a visible conflict, not an overwrite. Failed replacement retains prior bytes without claiming its provider grant remains usable.

Order: implement vault identity/CAS and race tests, connect browser/device operations, expose safe reconnect capability and generation through private/public account DTOs, add Accounts controls, then review full integration.

- [x] C1: Identity mismatch, unverified imports, changed credentials, deletion, cancellation, and stale refresh cannot replace or resurrect an account; valid replacement preserves its identifier.
  CHECK: env GOWORK=off go test -race ./internal/runner -run '^TestCredentialReconnect' -count=20
  EXPECT: ok
  CWD: accounts-manager/runner
  EVIDENCE: Fish, runner module, credential-free environment, GOWORK=off. Storage reconnect race tests passed 20 repetitions (1.925s); integrated reconnect/browser/device tests passed 20 repetitions (3.328s). Direct-provider provenance is tested with synthetic authenticators, not real sign-in.

- [x] C2: Browser/device reconnect and public account controls expose truthful capability, progress, and conflict recovery without changing defaults or bindings.
  EVIDENCE: Private target/generation commands and safe event codes pass both provider browser flows and supported device flow. Complete daemon adapter/service race suites pass (6.050s / 1.012s). Focused component/hook tests pass, 20 tests; reconnect target/CAS and unsupported imported identity are covered. Actual desktop behavior remains C3.

- [ ] C3: Full runner/backend checks, generated API parity, UI checks, and real desktop reconnect evidence pass.
  EVIDENCE: Partial. Full runner build/vet/race passed (2.791s); API regenerated; full HTTP race suite passed, including spec parity and controllers (50.411s). Corrected the fixture missing new required fields; full frontend typecheck and 22 focused UI tests now pass. Full backend and actual desktop evidence remain open.
