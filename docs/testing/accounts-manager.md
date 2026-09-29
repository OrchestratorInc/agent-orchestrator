# Accounts Manager testing guide

Use a separate desktop test profile and two disposable accounts. Start with the smoke checks below. Do not use permanent account removal on a work account.

This guide accompanies PR 5769. It describes expected behavior and known gaps, not a claim that every check passes. Record the tested commit with `git rev-parse HEAD`. Tests from another commit or operating system do not certify this one.

## Current checkpoint

Status as of 2026-09-29, including the account-picker correction:

| Area | Evidence available | Still open |
| --- | --- | --- |
| Linux desktop | Real account sign-in, overlapping A/B replies, selected terminal/Chat switches, pre-stop cancellation, positive quota readings, and daemon reattachment | Complete crash recovery, permanent removal, full runner/vault cold restart and exhaustive responsiveness testing |
| Windows | Platform compilation and synthetic checks | Native execution, exact process ownership/retirement and complete desktop workflows. A compatibility runtime does not substitute for Windows |
| macOS Apple silicon | Platform compilation and bounded containment design work | Native arm64 containment and desktop acceptance |
| macOS Intel | Platform compilation and bounded containment design work | Native x64 containment and desktop acceptance. Translation on an Apple silicon machine is not an Intel-machine result |
| Current picker correction | 5,632 frontend tests passed, seven skipped; 134 shared UI tests passed; four affected backend packages passed full race tests; focused backend checks passed three repeats | Independent review and full integrated release verification |

Known release blockers include five positive retirement-recovery failures across three test groups, pending-switch restart recovery, a saved-model versus Chat-composer discrepancy after restart, incomplete alternate-provider managed Chat/profile isolation, and current-main merge conflicts. Native account switching and Subscriptions are protected compatibility paths.

The latest picker supports explicit account selection, account-scoped model/effort discovery, and quota visibility even when generation is unavailable. That does not establish account billing attribution for every request or make an unavailable account usable.

Evidence: [picker repair and commands](../plans/accounts-manager-completion/ACCOUNT-PICKER-REPAIR.md), [bounded live results](../plans/accounts-manager-completion/parallel-session-delivery/user-flow/LIVE-RESULTS.md), [earlier integrated checkpoint](../plans/accounts-manager-completion/PUBLISH-2026-09-29.md). Earlier checkpoint counts describe that earlier snapshot.

## 1. Prepare the test app

Budget about 20 to 30 minutes for smoke testing after installation. Native recovery testing takes longer and needs a maintainer-controlled test machine.

1. Use the exact PR build supplied by a maintainer, or build the PR checkout below. The installed stable app may not contain these changes.
2. Use two accounts you are authorized to test, called A and B here. A third account can cover quota exhaustion or unavailable state. Sign in separately; do not copy another machine's credential files.
3. Use a throwaway project with no valuable files. Keep permission prompts enabled. Provider requests can consume quota or incur charges.
4. Record OS version, CPU architecture, app/commit version, provider CLI version, and whether you are testing terminal or Chat.
5. Keep normal desktop data and provider configuration untouched. AO test-data isolation does not itself isolate native provider credential homes. Do not log out, replace native credentials, or change global environment keys to make a managed test pass.

### Source checkout and dependencies

From an existing repository checkout, create a new test worktree. Choose another destination if this path already exists; do not remove or overwrite it.

```text
git fetch origin pull/5769/head
git worktree add --detach ../pr5769-platform-test FETCH_HEAD
cd ../pr5769-platform-test
git rev-parse HEAD
```

Confirm this is the latest published commit before testing. An unpushed local correction is not included by these commands.

Use Node 24, matching the frontend workflow, npm, Git and the Go version declared by `backend/go.mod` (currently 1.27.1). Native module installation may require the platform's C/C++ build tools. Install the provider CLI you intend to test. Read [development setup](../development.md) for packaging prerequisites.

```text
npm ci
npm --prefix packages/product-ui ci
npm --prefix packages/cloud-client ci
npm --prefix frontend ci
```

Use real installations in this checkout. Do not share or symlink `node_modules` from another worktree. `npm run dev` builds the daemon, account runner and required desktop runtimes before launching Electron. Allow the first build to finish. Do not replace Electron with `dev:web` for these tests.

### Linux and macOS launch

Use a unique test directory per OS/architecture and choose a free loopback port. The example uses 43439. If occupied, choose another port without stopping its current owner.

The following also works in fish, zsh and bash:

```sh
mkdir -p "$HOME/.ao/dev/pr5769-platform-test"
env AO_DATA_DIR="$HOME/.ao/dev/pr5769-platform-test/data" AO_RUN_FILE="$HOME/.ao/dev/pr5769-platform-test/running.json" AO_DEV_ELECTRON_DIR="$HOME/.ao/dev/pr5769-platform-test/electron" AO_PORT=43439 npm --prefix frontend run dev
```

Run in a graphical desktop session. Keep the launcher terminal open. On macOS, install Command Line Tools if native builds require them. Linux packaging may additionally require zip and distribution packaging tools; a source launch is not an installation/update test.

### Windows launch (PowerShell)

Run on native Windows, not WSL or a compatibility runtime. Use a free port and a unique test directory.

```powershell
$accountsTestRoot = Join-Path $env:USERPROFILE '.ao/dev/pr5769-platform-test'
New-Item -ItemType Directory -Force -Path $accountsTestRoot | Out-Null
$env:AO_DATA_DIR = Join-Path $accountsTestRoot 'data'
$env:AO_RUN_FILE = Join-Path $accountsTestRoot 'running.json'
$env:AO_DEV_ELECTRON_DIR = Join-Path $accountsTestRoot 'electron'
$env:AO_PORT = '43439'
npm --prefix frontend run dev
```

Use a native Windows Go toolchain and provider CLI. Race tests also need a Go-compatible C compiler. If dependencies or the runtime cannot start, record a setup failure rather than marking Windows passed.

### Verify isolation before signing in

The window must be the dev app from this checkout. The launcher should report a daemon listening on `127.0.0.1` and the chosen port. A fresh profile should not show your normal AO sessions. Stop if the wrong data appears.

Do not change listener binding or enable LAN access for these checks. Do not print the entire environment or run-file contents in a bug report. Restart using the same three data/profile/run-file paths when checking persistence.

## 2. Everyday smoke checks

### S1. Add and identify accounts

1. Open **Settings > Accounts**, choose the intended provider, and add A with **Browser sign-in** or **Device sign-in**, according to the offered capability.
2. Check the identity displayed on the provider's sign-in page before authorizing. Wait for AO to show the completed sign-in and verified account.
3. Add B with a separate browser profile or an explicitly selected provider identity. Confirm two distinct account IDs and the expected emails or labels. Repeat for C if available.
4. If the browser does not open, use **Open sign-in page** or **Copy sign-in link** for that attempt. Do not share the link. For an expired attempt, cancel it and start a new one.
5. For a genuine API key, leave **Base URL** blank to use the provider default. Supply a custom URL only for an endpoint you trust to receive that credential. Do not paste a subscription setup-token into the API-key form. Explicit token migration/reconnect remains a separate acceptance gap.

Pass: distinct verified identities, no secret displayed, no false success from merely submitting a form. A cancelled sign-in acknowledgement alone does not prove an observed terminal state or revoke a saved credential. Native Harness login status is a separate connection and is not proof of managed-account readiness.

### S2. Create a session with an explicit account

1. Open **New task**. Keep the normal provider, model and effort controls. Find the separate account dropdown underneath them.
2. Choose A explicitly. Confirm its email/label and quota summary if supported. Choose a model advertised for A and an offered effort level.
3. Start a throwaway session with: `Reply only TEST_A_READY. Do not use tools or modify files.`
4. Open the session account control. Record the committed account ID, mode and revision. A proposed or pending target is not the committed account.
5. Repeat with B in a second session, using `TEST_B_READY`. Changing the account must clear incompatible stale model/effort choices. Unavailable accounts remain visible but cannot be selected for a managed start.

Pass: each session has the explicitly chosen binding and produces an actual response. No implicit native choice, silent account replacement or unsupported-model fallback. A native test requires explicitly choosing **Native credentials** and an existing native login. Do not assume every provider supports managed Chat: record an unsupported mode as blocked, not a managed success.

### S3. Keep A and B running in parallel

1. Leave both sessions open. Send a short, harmless request to each while the other is still responding.
2. Record response start/end times and both committed binding IDs. Repeat in terminal and Chat only where managed mode is supported.
3. Confirm responses overlap, each session retains its own selected account, and neither session interrupts or repins the other.
4. Keep an unrelated native session open as a control if a native login already exists.

Pass: overlapping real responses with unchanged A/B bindings and an unaffected native control. A session answering "I use Account A" or inventing a token budget is not routing evidence. Provider-side billing attribution needs independent provider records where available.

### S4. Switch one live session

1. Give session A a harmless conversation marker, then request a switch to B through its session account control.
2. Choose timing explicitly: wait for the current turn, or stop now. Save the operation ID and source revision.
3. Watch pending progress without treating acceptance as success. Wait for the daemon to report readiness and the committed binding to change.
4. Confirm the conversation marker remains available when resuming history. Start a new conversation only if you explicitly choose that option.
5. Check that the original B session and unrelated native session still respond. Switch back only through another explicit operation.

Pass: one session changes account, operation/revision reflect the transition, and no unrelated session changes. If recovery is required, keep the operation ID and inspect its status. Do not create repeated new operations to work around an unresolved one.

### S5. Read usage without inventing token totals

1. Expand each account in Settings and refresh usage. Compare the dropdown summary with the expanded observation.
2. Record the reported percentage/window, reset time and observation time for A, B and C. Quota can change between reads.
3. An account with a generation error may still have readable quota. Confirm the error and quota can both appear without claiming generation works.
4. Unsupported or failed usage must remain unavailable or show an error with a request ID. It must not become a fabricated zero, full balance or exact remaining-token count.

Pass: the UI reports provider-supplied usage accurately. A native terminal `/usage` view is not integrated with this managed quota panel. Use the account panel for this test, not a conversational answer about tokens.

## 3. Negative and recovery checks

Use disposable sessions. Do not deliberately crash or delete accounts during the everyday smoke run.

| ID | Action | Required outcome |
| --- | --- | --- |
| R1 | Submit an obviously invalid dummy key through the API-key form | Rejected or explicitly unverified/unavailable; never usable merely because it was saved. Preserve the request ID |
| R2 | Request a wait-for-turn switch while busy, queue a second harmless message, then cancel before stopping begins | Daemon confirms cancellation; original binding remains; queued text survives. Do not assume cancellation succeeds after the boundary |
| R3 | Retry the same recoverable switch using its existing operation ID; repeat the request | No duplicate controller or silent account change. Status remains tied to the same session and operation. A terminal conflict can be a correct rejection |
| R4 | Gracefully restart with no pending operations, using the same isolated profile | Account IDs, bindings and revisions survive; subsequent responses work. Also compare model/effort/permission controls with saved choices. The known composer mismatch remains a failure |
| R5 | Temporarily disable disposable A while B stays enabled; try new requests to both; re-enable A afterward | A cannot authorize new work, B continues, and no fallback occurs. This is local disablement, not provider-side credential revocation |

An already accepted request may have a different lifetime from a new authorization. Record whether the request began before or after disablement; do not infer behavior from a spinner alone.

Restarting while a switch is waiting currently has a known failure: recovery can report failure and reject retry. Reproduce only on a disposable session and record it as a failure. Do not clear journals or edit the database to force progress.

### Removal preview and removal recovery

Safe preview: open **Remove** for a disposable account, inspect affected active and inactive bindings and the exact revision, then close the dialog without confirming. Opening the preview must not stop a session or remove credentials.

**Removal recovery** tracks unfinished removal operations. It does not restore a deleted account. "0 saved" is the local count of saved operation references, not an authoritative count of every backend journal. Closing a dialog does not cancel an operation that has already started.

Permanent removal is a maintainer-only acceptance test while retirement gates are open. On disposable credentials, the required contract is:

1. Preview includes every positively matched managed binding, including inactive records. Unrelated native bindings stay unchanged.
2. Exact-revision confirmation starts one durable operation. A stale revision requires a new preview; it must not delete using old impact information.
3. Affected queues remain preserved and paused. Every bound runtime must be proven stopped and authorization revoked before stored credentials and bindings are finalized.
4. Retry/restart resumes the same operation. Cancellation is offered only while safe. Unknown ownership remains recovery-required; never bypass the safety guard.
5. Completion is shown only when the daemon reports it. The removed account cannot authorize or relaunch a session, B/native controls survive, and no fallback account is selected.

Removing a saved credential from AO does not delete the provider account/subscription or sign out other applications. Do not mark this workflow passed on the current snapshot merely because the confirmation dialog works.

## 4. Native platform acceptance

Run the common checks on every machine and record terminal and Chat separately. A build success is only a build result.

| Platform | Additional checks | Required evidence |
| --- | --- | --- |
| Linux | Direct PTY and tmux fallback; account runner restart; host-parent death, late/moved descendants and replacement runtime preservation | Real processes plus durable coordinator reopen, with no false removal completion. Current retirement failures remain blocking |
| Windows | Native terminal/ConPTY; sign-in browser/callback; file locks; creation-time Job Object containment; publication/resume crash cuts and replacement preservation | Native kernel contract and recovery tests plus real desktop workflows. Compatibility-runtime failures or cross-compilation cannot satisfy this gate |
| macOS arm64 | Native desktop, browser/callback, terminal and Chat; containment across host/keeper death, group escape, restart and reboot | Native arm64 execution with unrelated guests/processes preserved. Guest feasibility/design approval is not production acceptance |
| macOS x64 | Repeat the arm64 scenarios on native Intel hardware; verify matching app/runtime architecture | Separate native x64 results. Neither an arm64 run nor cross-compilation satisfies this gate |

On every platform, test a narrow window, keyboard focus through model/effort/account controls, long account labels, quota refresh, and existing Subscriptions/native switching. Report clipped settings actions or inaccessible confirmation as failures. Record measured delays rather than calling the UI responsive based on appearance.

Maintainer crash scenarios must use the repository's test helpers and disposable processes. Do not broadly terminate desktop, provider, terminal or runner processes on a user's machine.

## 5. Automated checks for maintainers

Run in a separate verification checkout from the active desktop, with test-owned data. Do not place live credentials in fixture files or log the environment. Native SQLite modules built for Electron may need rebuilding for the Node test runtime; use separate dependency installations instead of disrupting the running app. The frontend test environment also needs the repository's browser-runtime prerequisites and zip utility.

From the repository root:

```text
npm --prefix frontend run typecheck
npm --prefix frontend run typecheck:e2e
npm --prefix frontend test -- --maxWorkers=1
npm run product-ui:check
npm run api
npm run sqlc
git diff --exit-code -- backend/internal/httpd/apispec/openapi.yaml frontend/src/api/schema.ts backend/internal/storage/sqlite/gen
```

Run generation on a committed checkout so the diff tests actual drift. Include the other applicable jobs in [Go CI](../../.github/workflows/go.yml) and [frontend CI](../../.github/workflows/frontend.yml) for release certification; this list is the feature-oriented entry point, not a substitute for all CI jobs.

From `backend`:

```text
go build ./...
go vet ./...
go test -race -count=3 ./internal/accountsmanager ./internal/service/accountsmanager ./internal/httpd/controllers ./internal/session_manager -run 'Test(CredentialModels|ManagedLaunch|ManagedModel|AgentAccountModels|AccountModelHTTP)'
go test -race -count=1 -timeout=20m ./...
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2 run --timeout=10m ./...
```

From `accounts-manager/runner`:

```text
go build ./...
go vet ./...
go test -race -count=1 ./...
```

The full backend suite is expected to expose the documented retirement blockers until corrected. Keep its failed output; do not exclude those cases to claim a full pass. Passing the four affected picker packages does not close failures in other packages.

Native Windows ownership checks, from `backend` on Windows:

```text
go test -race -count=3 -timeout=10m ./internal/adapters/chatdriver/persistenthost -run 'TestProviderOwnerWindows'
go test -race -count=1 -timeout=20m ./internal/adapters/chatdriver/persistenthost ./internal/session_manager
```

Inspect individual skips and platform build constraints. No matching tests or a skipped case is not a passed platform check. Linux/macOS recovery additionally needs the full persistent-host and session-manager suites on their native OS. Run packaged installation/update checks separately; a source launch does not test signing, updater behavior or OS packaging.

## Report a result

Use `PASS`, `FAIL`, `BLOCKED`, or `NOT RUN` for each scenario. Include this small template:

```text
Commit/build:
OS/version/architecture:
Provider CLI version and terminal/Chat mode:
Scenario ID and status:
Expected / observed:
Account aliases A/B/C and public IDs (no credentials):
Session ID / operation ID / revision / request ID:
Timestamp and measured duration:
Restart type: window / daemon reattachment / runner cold restart / reboot:
Redacted screenshot or short recording:
```

Do not attach API keys, setup-tokens, credential files, authorization links/codes, route capabilities, raw environment output or private runner endpoints. Mask personal emails in shared captures. Preserve failing operation IDs and report them before retrying destructive work.

When finished, close only the test app and its own launcher. Keep the test profile until failures are investigated. Do not delete normal AO data, global provider homes or unrelated processes as cleanup.
