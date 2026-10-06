# Harness Manager-Native Update Checks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make harness update advisories use the owning package manager's non-mutating checks, handle supported layouts and version formats safely, and retry inconclusive results after five minutes.

**Architecture:** Split version parsing, manager ownership, and manager-specific latest-version lookup into focused files under `systeminstall`. `UpdateAdvisory` remains the orchestrator: verify the running binary, select its proven owner, invoke that owner's checker, then use the existing vendor channel only for an unowned non-manager binary. The wire contract keeps three statuses and adds an optional stable unknown reason.

**Tech Stack:** Go, `ports.CommandRunner`, `net/http`, OpenAPI code generation, React, TanStack Query, Vitest.

**Spec:** `docs/superpowers/specs/2026-10-06-harness-manager-native-update-checks-design.md`

## Global Constraints

- Advisory discovery must never invoke a mutating install, update, upgrade, or uninstall operation.
- The running binary's proven owner wins over AO's recorded install method.
- Unproved ownership, ambiguous output, mismatched prerelease channels, and failed lookups return `unknown`, never `current`.
- Package names, IDs, and command shapes come only from server-owned `Plan` values.
- Definitive results cache for one hour; unknown results cache and refetch after five minutes.
- pnpm and Yarn are advisory-only and do not gain maintenance operations.

## Review Focus

- An npm `outdated` process that exits non-zero after emitting valid JSON must still produce an advisory.
- A Windows shim beside npm's global `node_modules` must name the expected package before ownership is accepted.
- Localized or ambiguous Winget table output must remain `unknown` instead of selecting another package row.
- A prerelease installed from one channel must not be compared with stable or a different prerelease channel.
- An empty manager result for an already-current package must fall back to an authoritative read-only version query, not be treated as a failure or as proof of `current`.

---

### Task 1: Parsed Update Versions

**Files:**
- Create: `backend/internal/service/systeminstall/update_version.go`
- Create: `backend/internal/service/systeminstall/update_version_test.go`
- Modify: `backend/internal/service/systeminstall/update_advisory.go`
- Modify: `backend/internal/service/systeminstall/official_versions.go`
- Modify: `backend/internal/service/systeminstall/operation_checks.go`

**Interfaces:**
- Produces: `findUpdateVersion(text string) (updateVersion, bool)` for CLI output.
- Produces: `parseUpdateVersion(text string) (updateVersion, bool)` for exact source values.
- Produces: `compareUpdateVersions(installed, latest updateVersion) (order int, comparable bool)`.
- `updateVersion` retains the display string, two to four numeric components, prerelease identifiers, and build metadata.

- [ ] **Step 1: Write failing parser and comparison tests**

Add table tests named `TestFindUpdateVersionAcceptsSupportedForms` and `TestCompareUpdateVersionsRequiresCompatibleChannels`. Assert support for `v1.2`, `1.2.3`, `1.2.3.4`, `1.2.3-beta.2+build.7`, numeric prerelease ordering, ignored build metadata, stable-versus-prerelease ordering, and `comparable=false` for different named prerelease channels or malformed input.

- [ ] **Step 2: Run the new tests and verify RED**

Run: `cd backend && go test ./internal/service/systeminstall -run 'Test(Find|Compare)UpdateVersion'`

Expected: FAIL because `updateVersion`, `findUpdateVersion`, and `compareUpdateVersions` do not exist.

- [ ] **Step 3: Implement the version value and parser**

Implement the three interfaces in `update_version.go`. Pad missing numeric components for comparison, include a fourth numeric component when present, apply SemVer identifier precedence to compatible prereleases, and ignore build metadata for precedence.

- [ ] **Step 4: Replace regexp-submatch callers**

Update advisory computation, official-version extraction, and post-update verification to use parsed values. Preserve each source's original normalized display string and the rule that an installed version ahead of its source yields `unknown`.

- [ ] **Step 5: Run focused and package tests**

Run: `cd backend && go test ./internal/service/systeminstall`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/service/systeminstall/update_version.go backend/internal/service/systeminstall/update_version_test.go backend/internal/service/systeminstall/update_advisory.go backend/internal/service/systeminstall/official_versions.go backend/internal/service/systeminstall/operation_checks.go
git commit -m "fix: parse harness release versions safely"
```

### Task 2: Manager Layout and Ownership Proof

**Files:**
- Create: `backend/internal/service/systeminstall/manager_ownership.go`
- Create: `backend/internal/service/systeminstall/manager_ownership_test.go`
- Modify: `backend/internal/service/systeminstall/operation_checks.go`
- Modify: `backend/internal/service/systeminstall/update_advisory.go`
- Modify: `backend/internal/service/systeminstall/systeminstall.go`
- Modify: `backend/internal/service/systeminstall/update_advisory_test.go`

**Interfaces:**
- Produces: `managerOwnsBinary(commands ports.CommandRunner) func(context.Context, string, string, string, bool) (bool, error)`.
- Produces: `advisoryPackageSources(plans []Plan, preferred, layout string) []Plan`.
- Produces: `nodeShimTargetsPackage(binaryPath, packageRoot string) (bool, error)`.
- Consumes: server-owned package names from `Plan.Package` and the current `packageLayout` classification.

- [ ] **Step 1: Write failing layout and ownership tests**

Cover npm symlinks, Windows npm `.cmd`/`.ps1` shims that do and do not reference the expected package, Homebrew formula/cask roots, Bun global roots, uv and pipx tool paths, Winget exact-ID listing plus layout, pnpm global roots, Yarn Classic global roots, and unresolved version-manager shims. Assert the exact read-only argv for every manager.

- [ ] **Step 2: Run ownership tests and verify RED**

Run: `cd backend && go test ./internal/service/systeminstall -run 'Test(ManagerOwnsBinary|NodeShim|PackageLayout|AdvisoryPackageSources)'`

Expected: FAIL because the generalized ownership boundary and Yarn layout do not exist.

- [ ] **Step 3: Implement manager ownership**

Move npm/Homebrew ownership from `update_advisory.go` into `manager_ownership.go` and add the supported manager probes. Require both the expected package identity and containment/launcher evidence; path layout alone is insufficient for npm, Winget, pnpm, and Yarn.

- [ ] **Step 4: Implement advisory-only candidates**

Build manager candidates from installation plans. Include npm, Homebrew, Winget, Bun, uv, and pipx plans directly. When the verified layout is pnpm or Yarn, clone only the matching npm package identity into an advisory-only plan whose method is the detected manager. Keep the recorded method first without bypassing ownership checks.

- [ ] **Step 5: Wire ownership into operation safety**

Rename the injected `Service.ownsInstallation` implementation to the generalized manager owner while preserving its test seam and ensuring automatic update/uninstall plans are accepted only for methods the operation catalog already supports.

- [ ] **Step 6: Run focused and package tests**

Run: `cd backend && go test ./internal/service/systeminstall`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/service/systeminstall/manager_ownership.go backend/internal/service/systeminstall/manager_ownership_test.go backend/internal/service/systeminstall/operation_checks.go backend/internal/service/systeminstall/update_advisory.go backend/internal/service/systeminstall/update_advisory_test.go backend/internal/service/systeminstall/systeminstall.go
git commit -m "fix: detect harness package manager ownership"
```

### Task 3: Manager-Native Version Providers

**Files:**
- Create: `backend/internal/service/systeminstall/manager_versions.go`
- Create: `backend/internal/service/systeminstall/manager_versions_test.go`
- Modify: `backend/internal/service/systeminstall/update_advisory.go`
- Modify: `backend/internal/service/systeminstall/update_advisory_test.go`
- Modify: `backend/internal/service/systeminstall/systeminstall.go`

**Interfaces:**
- Produces: `type managedVersionResult struct { Latest string; Channel string }`.
- Produces: `type managedVersionChecker func(context.Context, Plan, updateVersion) (managedVersionResult, error)`.
- Produces: `newManagedVersionChecker(commands ports.CommandRunner, client *http.Client) managedVersionChecker`.
- Consumes: an ownership-proven `Plan` and parsed installed version.

- [ ] **Step 1: Write failing provider tests**

Add one table per provider and assert these command shapes: npm global `outdated --json` followed by `view` when empty; Homebrew `outdated --json=v2` followed by `info --json=v2` when empty; Winget `upgrade` listing without a package selector, filtering the exact ID in output; Bun `pm view`; pnpm global `outdated --format json` followed by `view`; Yarn Classic `global outdated --json` followed by `info`. Cover valid output written alongside a non-zero npm exit and ambiguous Winget output. An exact-ID `winget upgrade` performs an upgrade and must never be used for discovery.

For Node managers, cover the injected HTTP client's exact escaped npm registry
fallback and dist-tag parsing. For uv and pipx, assert their read-only list
output proves the tool before the client requests the exact escaped PyPI project
URL. Cover non-200, oversized, and malformed responses.

- [ ] **Step 2: Run provider tests and verify RED**

Run: `cd backend && go test ./internal/service/systeminstall -run 'TestManagedVersion'`

Expected: FAIL because the provider registry does not exist.

- [ ] **Step 3: Implement bounded command execution and parsers**

Use manager-specific timeouts and `capturedOutput`. Parse only machine-readable output where available. For Winget, require one row containing the exact allowlisted ID and two parseable version columns; otherwise return an ambiguity error. Parse command output before classifying a non-zero exit as failure so npm's outdated status is retained. After ownership is proved, allow the exact escaped public npm registry endpoint as the Node-manager fallback; keep PyPI as the uv/pipx fallback.

- [ ] **Step 4: Implement stable and prerelease channel selection**

Use the manager's returned dist-tag/channel when available. Stable installations select `latest`. Prereleases require a manager dist-tag or channel whose published version matches the installed channel name; a missing or different channel returns an error that the service maps to `unknown`.

- [ ] **Step 5: Integrate the checker into `UpdateAdvisory`**

Replace `latestVersion` with `managedVersionChecker` in `Service`. Call it only after ownership succeeds, compare parsed values, keep official vendor lookup as the final eligible fallback, and preserve single-flight and caching behavior.

- [ ] **Step 6: Run focused and package tests**

Run: `cd backend && go test ./internal/service/systeminstall`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/service/systeminstall/manager_versions.go backend/internal/service/systeminstall/manager_versions_test.go backend/internal/service/systeminstall/update_advisory.go backend/internal/service/systeminstall/update_advisory_test.go backend/internal/service/systeminstall/systeminstall.go
git commit -m "feat: check harness updates through package managers"
```

### Task 4: Unknown Reasons and Generated API Contract

**Files:**
- Modify: `backend/internal/service/systeminstall/update_advisory.go`
- Modify: `backend/internal/service/systeminstall/update_advisory_test.go`
- Modify: `backend/internal/httpd/controllers/systeminstall_test.go`
- Modify: `backend/internal/httpd/apispec/openapi.yaml`
- Modify: `frontend/src/api/schema.ts`

**Interfaces:**
- Produces: optional `UpdateAdvisory.Reason string` serialized as `reason`.
- Produces stable values: `ownership_unconfirmed`, `unsupported_source`, `version_unparseable`, `channel_unconfirmed`, and `lookup_failed`.

- [ ] **Step 1: Write failing service and controller tests**

Assert each inconclusive branch returns the expected stable reason without command output or a local path, and that definitive results omit `reason`.

- [ ] **Step 2: Run focused tests and verify RED**

Run: `cd backend && go test ./internal/service/systeminstall ./internal/httpd/controllers -run 'Test.*UpdateAdvisory'`

Expected: FAIL because the field and reason assignments do not exist.

- [ ] **Step 3: Add the reason field and assignments**

Keep `status=unknown` as the behavior contract and treat the reason only as diagnostic metadata. Clear it whenever the result becomes `current` or `behind_latest`.

- [ ] **Step 4: Regenerate API artifacts**

Run: `npm run api`

Expected: `openapi.yaml` and `schema.ts` include optional `reason` and no unrelated drift.

- [ ] **Step 5: Run API parity tests**

Run: `cd backend && go test ./internal/httpd/...`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/service/systeminstall/update_advisory.go backend/internal/service/systeminstall/update_advisory_test.go backend/internal/httpd/controllers/systeminstall_test.go backend/internal/httpd/apispec/openapi.yaml frontend/src/api/schema.ts
git commit -m "feat: explain unknown harness update advisories"
```

### Task 5: Frontend Unknown Retry Cadence

**Files:**
- Modify: `frontend/src/renderer/components/settings/HarnessSettingsSection.tsx`
- Modify: `frontend/src/renderer/components/settings/HarnessSettingsSection.test.tsx`

**Interfaces:**
- Produces: `updateAdvisoryRefreshInterval(advisory?: AgentUpdateAdvisory): number` returning `300000` for missing/unknown data and `3600000` for definitive data.

- [ ] **Step 1: Write failing cadence tests**

Use fake timers or direct helper tests to assert five minutes for missing/unknown results, one hour for `current` and `behind_latest`, and immediate query invalidation after a successful harness operation.

- [ ] **Step 2: Run the focused frontend test and verify RED**

Run: `npm --prefix frontend test -- HarnessSettingsSection.test.tsx`

Expected: FAIL because the query always uses one hour.

- [ ] **Step 3: Implement status-aware query timing**

Use TanStack Query's query-aware `staleTime` and `refetchInterval` callbacks with `updateAdvisoryRefreshInterval`; keep `retry: false` because backend unknown results are successful advisory responses.

- [ ] **Step 4: Run focused tests and typecheck**

Run: `npm --prefix frontend test -- HarnessSettingsSection.test.tsx`

Run: `npm run frontend:typecheck`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/renderer/components/settings/HarnessSettingsSection.tsx frontend/src/renderer/components/settings/HarnessSettingsSection.test.tsx
git commit -m "fix: retry unknown harness updates promptly"
```

### Task 6: Update-Advisory Verification

**Files:**
- Verify all files changed in Tasks 1-5.

**Interfaces:**
- Consumes: all previous task outputs.
- Produces: a verified update-advisory workstream ready to combine with the maintenance-gate workstream.

- [ ] **Step 1: Run backend package and HTTP tests**

Run: `cd backend && go test ./internal/service/systeminstall ./internal/httpd/...`

Expected: PASS.

- [ ] **Step 2: Run frontend tests and typecheck**

Run: `npm --prefix frontend test -- HarnessSettingsSection.test.tsx`

Run: `npm run frontend:typecheck`

Expected: PASS.

- [ ] **Step 3: Verify generated API drift and formatting**

Run: `npm run api`

Run: `git diff --exit-code -- backend/internal/httpd/apispec/openapi.yaml frontend/src/api/schema.ts`

Run: `gofmt -w backend/internal/service/systeminstall/update_version.go backend/internal/service/systeminstall/update_version_test.go backend/internal/service/systeminstall/manager_ownership.go backend/internal/service/systeminstall/manager_ownership_test.go backend/internal/service/systeminstall/manager_versions.go backend/internal/service/systeminstall/manager_versions_test.go backend/internal/service/systeminstall/update_advisory.go backend/internal/service/systeminstall/update_advisory_test.go backend/internal/service/systeminstall/official_versions.go backend/internal/service/systeminstall/operation_checks.go backend/internal/service/systeminstall/systeminstall.go backend/internal/httpd/controllers/systeminstall_test.go`

Run: `git diff --check`

Expected: no generated drift and no whitespace errors.

- [ ] **Step 4: Run the complete local CI-equivalent validation**

Run: `npm run lint`

Run: `cd backend && go build ./... && go test -race ./... && go vet ./...`

Run: `npm --prefix frontend test`

Run: `npm --prefix frontend run build`

Run: `npx @redwoodjs/agent-ci run --all`

Expected: PASS. If Docker, credentials, or a native runner prevents a job from
running locally, record that exact gap and verify the corresponding remote check
instead of calling it locally passed.
