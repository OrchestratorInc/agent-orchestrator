# Browser sign-in recovery: bounded review handoff

Date: 2026-09-29. HEAD: `ec7efde0652f21690d8e98e2dcfa71d5b132e422`. Local only, no commit or publication. Read-only review requested for this correction; broader product acceptance remains open.

Evidence root: `/var/tmp/pr-5769-browser-recovery-79.AAbpSt`. Exact source/archive, documentation, integrated inventory and preservation digests are recorded in `final/FREEZE.md` and `final/manifest.json`. The 12-entry source manifest SHA256 is `f7fb8d65fa1329e1f1d0400869e0992233816aa9203a544596e2d5cf23257fd7`.

## Changes and review contract

- `AccountsManagerSection.tsx` retains an accepted operation until authoritative inventory observes it. A valid HTTPS handoff failure no longer cancels it. Manual retry uses the same operation, and later terminal inventory withdraws the transient link even when large revisions round equally.
- `SignInBrowserRecovery.tsx` exposes an explicitly opened or copied link, rejects unsafe schemes and URL credentials, joins repeated clicks, handles clipboard errors without exception text, and removes unusable or expired links. Links are not written to storage or logs.
- The two matching test files cover these boundaries. Eight locale catalogs contain five new message keys each. No daemon, runtime, vault, native switching, Subscriptions or generated public contract changes occur in this leaf.

The source manifest lists the complete paths: the two components, their two tests, and `frontend/src/renderer/i18n/{de,en,es,fr,ja,ko,pt-BR,zh-CN}.json`.

Review cancellation acknowledgements separately from authoritative operation status. Review the transient POST result versus inventory, stale browser completion versus a newer form generation, terminal/pruned records, rounded-equal revisions, expiry, URL safety and repeated clicks. A failed browser handoff must not delete a credential or claim failed authorization.

## Failed-first and midpoint evidence

From the repository frontend, with Node 24.21.0 and credential environment stripping:

1. `npm test -- --maxWorkers=2 src/renderer/components/settings/AccountsManagerSection.test.tsx -t 'keeps the accepted'`: two failed assertions before production edits. `red.log` SHA256 `d505dd69f87f0baf686413e5e71820e67b08ce55f399b0fe85ed5fc8267d5c71`. `red-source.tar.gz` SHA256 `cd632e35a7af2a8a8136e5f23341f483ebac4f8790ad31faa5794dea191bcdaa`.
2. Self-review reproduced a second defect: an old opener completion could release the busy flag of a newer request. `-t 'does not unlock'` failed before generation-bound completion/error publication. `late-handoff-red.log` SHA256 `43a94dd54933765a14ef3d7e63961847e5603d0c830c8ae394a80f3e8c409ebd`.
3. An incomplete old fixture was updated to the actual required operation DTO. A pre-existing test awaited mock invocation rather than completed secret clearing; it now awaits the input being empty, preserving that assertion. An intermediate rerender-helper syntax error is preserved in `midpoint.log`. None of these failed runs is passing evidence.

## Final verification

The isolated checkout is `/var/tmp/pr-5769-desktop-final-79.yrWkBr/checkout`. It has its own installed dependencies, matching native modules, scratch home/data/profile and source-hash proof. Terminal commands use Fish. `run-isolated.mjs` strips credential-bearing environment variables; `verify-ci.fish` records exact commands and exit statuses. Node is 24.21.0; the desktop Go builds explicitly select 1.27.1.

| Check | Observed result and log |
| --- | --- |
| Focused components, inventory causality and locales | 89 tests, zero skips, three repeats: `lab-checks/focused-{1,2,3}.log` |
| Frontend and e2e typechecks | Both exit 0: `ci-checks/typecheck.log`, `typecheck-e2e.log` |
| Full frontend with pinned browser runtime | 352 files, 5626 passed, six skipped, zero failed, 270.26 seconds: `ci-checks/frontend-full.{log,json}` |
| Renderer smoke | 60 passed, 59.6 seconds: `ci-checks/renderer-smoke.log`. Fixture-based, not native platform or full daemon proof |
| Shared UI | Typecheck, 133 tests and package dry-run pass: `ci-checks/product-*.log` |
| Cloud client | Generation, no drift, typecheck, 22 tests and package dry-run pass: `ci-checks/cloud-*.log` |
| Docs and desktop | Docs build and complete Linux x64 `npm run build` pass, including packaged resources and post-package checks: `ci-checks/docs-build.log`, `desktop-build.log` |
| Snapshot | `verify-lab-source.mjs` confirms 5760 recorded lab files and exact current 12-file source bytes. Prior 58, protected 91, generated 33 and guest five-entry manifests match |

The six remaining source-declared skips are in `ConnectMobileModal.test.tsx`: dropdown width, one code across modes, unavailable private-network helper, address selection, private hostname exclusion, and missing-cert setup. They are not new skips and are not claimed as passing.

Preserved diagnostics: the first worktree full run failed 124 cases because zip was absent and its native SQLite module targeted Electron rather than Node. The isolated lab then passed those four suites (132 tests), and its complete suite passed. Its first typecheck lacked the sibling UI package's own dependencies; installing that package's lockfile resolved it without source edits. Final verification follows the frontend CI dependency setup and pinned runtime. The pinned archive utility is task-local, checksum-verified, and not installed globally. No test was skipped or weakened to obtain the final result.

## Actual desktop evidence

The desktop-development skill supplied isolation and native-app verification requirements. Real Electron, its preload, the freshly built daemon/runner and the real 30-entry provider catalog were observed. No mock page or invented account was used for captures.

`desktop-result.json` and `desktop-result-reload.json` each record one real POST, manual reopening without another POST, one explicit DELETE 204, observed expired/cancelled state and zero saved accounts. The second run recovers the same pending operation after renderer reload. Inspected `pending-link.png`, `same-operation-retry.png`, `cancel-acknowledged.png` and a recording frame in each capture directory. `desktop/browser-recovery.mp4` and `desktop-reload/browser-recovery.mp4` contain real app frames. All attempted operations were cancelled; the owned desktop processes were stopped.

The native opener acknowledged both attempts, including the test environment intended to reject opening. Therefore the actual OS rejection branch is not established by desktop evidence. Its deterministic exception, clipboard, URL and expiry controls are unit tests. No sign-in callback or provider credential was supplied. Clipboard copying was not exercised in the real desktop run.

## Remaining gates

Four bounded gates met, one remains open for independent review. Initial selection with an eligible account, positive quota, actual A/B provider work, switching/removal with live credentials, and release latency remain open. Production containment still requires independent review and integration; the three retained positive retirement groups remain unresolved. Managed Chat/profiles, native Mac arm64/x64, native Windows and final integrated release checks are not completed by this frontend seal. Prior backend review requests and archives remain intact.
