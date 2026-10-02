# Gates: Accounts Manager validation increment

Scope: Remove PR-owned lint and localization failures, fix the first-launch response-contract defect found in desktop review, and reverify the runner privacy increment. This does not certify the full account lifecycle or session-switching feature.

## Plan and review sequence

1. Reproduce the exact failures. Fix backend resource handling and API-contract documentation without changing native account modules or relaxing lint rules. Review the diff and rerun the affected packages.
2. Move only Accounts Manager strings into the existing localization system. Preserve English behavior, add a non-English regression, and review interpolation and accessible labels.
3. Reverify runner privacy guards and inspect the next credential-persistence boundary. Keep unproven lifecycle capabilities out of this validation increment.
4. Run integration checks, review protected paths, and record remaining platform and feature gaps. Do not push without explicit approval.
5. Desktop review found unset routing account lists serialized as `null`, crashing the picker before any account exists. Reproduce through the real HTTP controller, normalize the response array without changing stored choices, rerun the controller/contract checks, and repeat the desktop walkthrough.

- [x] G1: The complete backend lint check reports no findings.
  CHECK: env GOMAXPROCS=2 go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2 run --path-mode=abs
  EXPECT: 0 issues.
  CWD: backend
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/backend; path=1eef3047a157/35 entries; output=0 issues.

- [x] G2: Backend build, vet, and affected account packages pass with the race detector.
  CHECK: go build ./...; and go vet ./...; and go test -race -count=1 ./internal/accountsmanager ./internal/service/accountsmanager
  EXPECT: ok
  CWD: backend
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/backend; path=1eef3047a157/35 entries; output=ok  	github.com/aoagents/agent-orchestrator/backend/internal/accountsmanager	6.019s | ok  	github.com/aoagents/agent-orchestrator/backend/internal/service/accountsmanager	1.011s

- [x] G3: Frontend typechecks, Accounts Manager tests, and localization coverage pass.
  CHECK: npm exec --yes --package=node@24 --call 'npm run typecheck && npm run typecheck:e2e && npm test -- src/renderer/components/settings/AccountsManagerSection.test.tsx src/renderer/components/settings/AccountsManagerNavigation.test.ts src/renderer/hooks/useAccountsManagerQuery.test.ts src/renderer/i18n/renderer-coverage.test.ts src/renderer/i18n/instance.test.ts --maxWorkers=2'
  EXPECT: Test Files
  CWD: frontend
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/frontend; path=1eef3047a157/35 entries; output=Start at  04:14:41 | Duration  4.16s (transform 1.63s, setup 1.17s, import 2.30s, tests 1.44s, environment 2.41s)

- [x] G4: The runner builds, passes vet, and passes the full race suite including each independent startup privacy guard.
  CHECK: env GOWORK=off go build ./...; and env GOWORK=off go vet ./...; and env GOWORK=off go test -race -count=1 ./...
  EXPECT: ok
  CWD: accounts-manager/runner
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/accounts-manager/runner; path=1eef3047a157/35 entries; output=?   	github.com/aoagents/agent-orchestrator/accounts-manager/runner/cmd/ao-accounts-manager	[no test files] | ok  	github.com/aoagents/agent-orchestrator/accounts-manager/runner/internal/runner	1.439s

- [x] G5: Post-fix review confirms protected paths and upstream engine remain unchanged, changed contracts match verified behavior, localization preserves English behavior, and remaining security gaps are explicit.
  EVIDENCE: Reviewed changed backend contracts and resource handling, the complete account component diff, and the Accounts-only settings registration. The fresh-install defect is confined to response serialization, shared by polling and events; stored routing choices are unchanged. Git comparison against 048a59775 confirms protected native modules and the engine unchanged. All eight catalogs retain every pre-existing value. API regeneration has zero drift. The actual desktop walkthrough passes after rebuilding the daemon, including translated navigation, keyboard-opened add controls, cancelled-input clearing, and an unchanged native settings view. Security limitations remain explicit; this is not full feature approval.

- [x] G6: First-launch account responses contain array-valued empty routing choices and preserve configured choices.
  CHECK: go test -race -count=1 ./internal/httpd/controllers -run TestAccountsManager
  EXPECT: ok
  CWD: backend
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79/backend; path=1eef3047a157/35 entries; output=ok  	github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers	1.020s

## Baseline

- Local rebase head: `048a59775999b60f8276a1f5d1107dbef57f5483`. Remote PR head is still `6f064d626d55dd5aa1c7996c06a1349a73198460`.
- Backend lint reproduced 139 findings. No configuration exclusions will be added to hide genuine errors.
- Existing runner changes and planning artifacts were preserved on entry. They are separate from the lint/localization fixes.
- The broad test failures recorded in the rebase report remain historical failures until a rerun establishes otherwise.

## Review checkpoints

- Backend: reproduced 139 lint findings, then reached zero without changing the lint configuration. Reviewed the owner-only configuration write, explicit read-body cleanup, checked listener close, exported contracts, and unchanged API shapes.
- Localization: a Spanish error/accessible-label regression failed before the fix. Eight catalogs now cover the new account controls, including plurals and dynamic errors. All pre-existing translation values are unchanged; only 441 `accountsManager.*` entries were added across the catalogs.
- Navigation: review found the new Accounts menu label outside the JSX coverage scan. A separate two-locale regression failed for Spanish before the one-line registration fix. Other settings registrations are unchanged.
- First launch: the actual desktop crashed on `policy.accountIds.map` because the daemon returned `null` for each unset policy. The new HTTP regression failed for both providers before the response-only fix. All controller and API-schema packages subsequently passed with the race detector; API regeneration had no drift. The shared response converter covers polling and event snapshots without altering routing policy or persistence.
- Verification reliability: the first combined frontend gate failed in the coverage scan. An isolated coverage run and the complete four-file, 27-test suite passed without source changes. The final command bounds parallelism and includes the new navigation regression; it does not relax assertions or timeouts.
- Security: startup policy does not prevent live management mutations or external configuration reloads. The SDK's default token store, raw import, API-key configuration, and device-login subprocess have distinct persistence paths. Encrypted lifecycle operations and revisioned route revocation remain unfinished; none is certified by this increment.

## Broader verification

- Backend: the full native-account service, session-manager, chat service, controller, API-schema, and SQLite store packages passed with the race detector in a credential-scrubbed test environment. Controller and API-schema suites passed again after the first-launch fix. This is not a claim that every backend package or platform was tested.
- Frontend: the first full run passed 5,327 tests with six skipped. A later run, including the navigation regression, passed 5,311 tests and failed 18 browser-import tests because Forge had rebuilt the shared native SQLite dependency for Electron ABI 130 while the Node test runtime requires ABI 137. After desktop verification, rebuilt that dependency for Node 24 without changing application source. The final full run passes all 337 files: 5,330 tests passed, six skipped, in 250.17 seconds. Log: `/tmp/accounts-manager-increment.jF3TIM/frontend-restored-final.log`.
- Desktop: the real app builds and launches with the real provider catalog, private ports, and scratch data. An initial broader filesystem/device mount retry was rejected by the safety review. A narrower allowlist launch succeeded without personal credential directories or physical-device access. Its supported daemon-command override runs the freshly built daemon; missing sandbox toolchain certificates are not bypassed.

## Desktop evidence

- Checkout: session branch `ao/agent-orchestrator-79/accounts-manager`; scratch state `/home/ghoul/.ao/dev/accounts-manager-79.rrw77i`. Renderer `http://localhost:5173`, daemon `http://127.0.0.1:39179`. Real Electron, real preload, real daemon and runner, built-in provider catalog, software rendering. No fixture bridge or reconstructed screen.
- After the response fix, Accounts opens on an empty installation. Switching the interface to Spanish translates the navigation, counts, forms, and accessible controls. Keyboard activation opens Add. Cancelling unsaved placeholder text clears it on reopen. The final daemon snapshot confirms zero saved gateway accounts and zero login operations. No provider authentication or account-switch claim is made.
- Subscriptions was inspected before and after the walkthrough without signing in or changing native account state. This establishes rendering continuity in scratch mode, not full live native-switch coexistence.
- Inspected local captures: `/tmp/accounts-manager-increment.jF3TIM/desktop-accounts-en.png`, `desktop-accounts-es.png`, `desktop-add-es.png`, `desktop-subscriptions-baseline.png`, and `desktop-subscriptions-after.png` in the same directory. The 24-frame, 5-fps native-window capture is `accounts-localization-reviewed.webm` (4.8 seconds, VP9, 1908 by 1037). These are local evidence, not uploaded PR attachments or performance measurements. Earlier failed-attempt captures are not review evidence.
- The recording passes a full FFmpeg decode check. The scratch app was stopped after capture, and the Node test dependency was restored before the clean full-suite rerun. No publication is approved or performed.

## Final scope review

- All six increment gates are met. Five automated gates were rerun after the final source change; the sixth records the source and real-desktop review.
- A final parsed comparison confirms all existing values across eight locale catalogs are preserved, with 441 Accounts-only additions. No protected native account paths or vendored engine paths changed relative to the local rebase head. `git diff --check` and generated API artifact comparisons are clean.
- The recorded checks do not establish all-backend coverage, full engine coverage, cross-platform packaging, remote application CI, real provider authentication, concurrent A/B identity, live switching/removal races, encrypted persistence, or performance targets. M0 and M1-M5 remain incomplete. The next increment must prove persistence and supported-mode boundaries before enabling lifecycle operations.
