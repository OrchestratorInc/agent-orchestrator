# Reasonix Harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete the selectable Reasonix TUI harness in AO PR #5905, requiring a compatible process-scoped system-prompt contract at launch.

**Architecture:** Register a Go adapter that launches the user-owned Reasonix binary after bounded identity/capability checks, merges observation-only native hooks, and restores exact machine session IDs. Include domain, storage, API, installer, and frontend integration in this PR.

**Tech Stack:** Go, Cobra, SQLite/goose, OpenAPI, TypeScript, React/Electron

**Spec:** `docs/superpowers/specs/2026-09-26-reasonix-harness-design.md`

## Implementation status — 2026-09-27

Core launch/restore, conservative hook activity and identity, auth/model/install
metadata, domain/registry/storage/API registration, and desktop identity/settings
are implemented. Focused suites pass. Real-binary conformance, full CI validation,
visual review, and final PR publication are the remaining verification work.
The detailed steps below record the implementation plan; their individual commit
examples are not a requirement to split the final AO changes.

## Global Constraints

- The upstream prompt-file implementation is submitted in Reasonix PR #11059. Per the user's explicit 2026-09-27 instruction, complete AO registration now; the earlier wait-for-release rule is superseded.
- Reject unidentifiable binaries and builds without the required CLI capabilities, including `--resume-exact`. v1.39.2 lacks `--append-system-prompt-file`; there is no compatible release floor yet.
- Do not replace `REASONIX_HOME`, copy credentials, or write Reasonix user configuration.
- TUI only: no Chat driver, reviewer registration, interface handoff, or nested-agent sessions.
- AO standing instructions stay system-role content and are reapplied on exact restore.
- Hooks are observation-only and preserve every user entry and unknown JSON field.
- Reasonix accepts direct free-form model IDs; AO must not invent a catalog.
- Migration `0163_allow_reasonix_harness.sql` follows the current merged main's `0162` and supports current and legacy-qm schemas.

## Review Focus

- An executable named `reasonix` from another product or an older release is rejected before session side effects; Task 1 tests identity and required capabilities.
- Prompt text containing leading dashes, multiline Unicode, or terminal control characters is delivered only through the TUI and never shell-interpolated; Task 1 tests argv/readiness, and Task 4 validates strict readiness; timeout must never fall back to sending.
- Existing malformed or concurrently changed `.reasonix/settings.json` is never overwritten; Task 2 tests fail-closed merge behavior.
- A hook payload with a huge or malformed `sessionId` cannot enter metadata or suppress lifecycle reporting; Task 2 tests bounds and malformed input.
- `accept-edits` and `auto` intentionally share `workspace-write`, while bypass alone selects `danger-full-access`; Task 1 pins every mode and rejects unknown values.

---

### Task 1: Build the core adapter

**Files:**
- Create: `backend/internal/adapters/agent/reasonix/reasonix.go`
- Create: `backend/internal/adapters/agent/reasonix/reasonix_test.go`
- Create: `backend/internal/adapters/agent/reasonix/install.go`
- Create: `backend/internal/adapters/agent/reasonix/install_test.go`

**Interfaces:**
- Produces: `func New() *Plugin`, `func ResolveReasonixBinary(context.Context) (string, error)`, and implementations of `adapters.Adapter`, `ports.Agent`, `ports.AgentBinaryResolver`, `ports.AgentBinaryPresenceResolver`, and `ports.AgentPromptReadinessProvider`.
- Produces: bounded `--version` identity and `--help` capability probes; no fabricated version floor.

- [ ] **Step 1: Write failing manifest, model, and argv tests**

Assert manifest ID `reasonix`, name `Reasonix`, capability `agent`, and a string model config. Table-test fresh and restore argv for all AO permission modes, optional model, absolute `--dir`, mandatory `--append-system-prompt-file`, and exact `--resume-exact <sessionId>`. Assert no prompt text enters argv, unknown permission values are rejected, and missing prompt-file/native-ID inputs fail safely.

- [ ] **Step 2: Run the core test and confirm failure**

Run: `cd backend && go test ./internal/adapters/agent/reasonix -run 'TestManifest|TestLaunch|TestRestore|TestPermissions' -count=1`

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Implement the adapter command contract**

Implement `GetConfigSpec`, `GetLaunchCommand`, `GetRestoreCommand`, `GetPromptDeliveryStrategy`, `PromptReadinessHints`, and `SessionInfo`. Use argument slices only. Map `default` to `read-only`, `accept-edits` and `auto` to `workspace-write`; map `bypass-permissions` to `danger-full-access`; reject unsupported explicit values.

- [ ] **Step 4: Write failing binary identity tests**

Cover PATH, Homebrew, common npm locations, Windows `.cmd`/`.exe` shims, name collision, context cancellation, hanging version probe, `reasonix v<version>` parsing, and missing required CLI capabilities. The process-free presence probe must return `ErrAgentBinaryIdentityUnknown` until the normal probe confirms identity.

- [ ] **Step 5: Implement identity-validated binary resolution**

Use `binaryutil.BinarySpec` and bounded identity/capability commands. Keep presence checks process-free. Resolve Windows npm shims to official native payloads; do not invoke a command shell.

- [ ] **Step 6: Pin TUI readiness**

Add fixture-driven terminal snapshots for startup, ready composer, startup error, and permission dialog. Implement strict `TerminalActivityDetector` readiness. Task 4 must prove the tested native composer is detected; unknown startup/permission dialogs and timeout reject delivery.

- [ ] **Step 7: Run and commit**

Run: `cd backend && go test ./internal/adapters/agent/reasonix -count=1`

Expected: PASS.

```bash
git add backend/internal/adapters/agent/reasonix
git commit -m "feat: add Reasonix adapter"
```

### Task 2: Add native hooks, activity, and exact identity

**Files:**
- Create: `backend/internal/adapters/agent/reasonix/hooks.go`
- Create: `backend/internal/adapters/agent/reasonix/hooks_test.go`
- Create: `backend/internal/adapters/agent/reasonix/activity.go`
- Create: `backend/internal/adapters/agent/reasonix/activity_test.go`
- Modify: `backend/internal/adapters/agent/activitydispatch/dispatch.go`
- Modify: `backend/internal/adapters/agent/activitydispatch/dispatch_test.go`
- Modify: `backend/internal/cli/hooks.go`
- Modify: `backend/internal/cli/hooks_test.go`

**Interfaces:**
- Consumes: `reasonix.New()` and standard AO hook commands from Task 1.
- Produces: `func DeriveActivityState(event string, payload []byte) (domain.ActivityState, bool)` plus `GetAgentHooks`, `UninstallHooks`, and `AreHooksInstalled` methods.

- [ ] **Step 1: Write failing hook-merge tests**

Assert `.reasonix/settings.json` receives uniquely identifiable `ao hooks reasonix <event>` entries for SessionStart, UserPromptSubmit, PreToolUse, PostToolUse, PostToolUseFailure, PermissionRequest, Stop, and SessionEnd. Cover idempotence, preservation of user order/unknown keys, restrictive file modes, missing workspace, cancelled context, malformed JSON, concurrent replacement, uninstall, and sibling `.gitignore` entries.

- [ ] **Step 2: Run hook tests and confirm failure**

Run: `cd backend && go test ./internal/adapters/agent/reasonix -run 'Test.*Hooks' -count=1`

Expected: FAIL because hook support is absent.

- [ ] **Step 3: Implement atomic native hook management**

Merge the direct Reasonix event-array schema without routing through Claude compatibility. Use atomic writes and fail closed on malformed/concurrently changed input. AO commands always exit observationally and never emit allow/deny or context output.

- [ ] **Step 4: Write failing activity and identity tests**

Assert a valid root `sessionId` is captured on supported events; prompt-submit/pre-tool are active; permission requests do not assert blocked; successful stop becomes waiting-input; malformed/oversized and child IDs are ignored before metadata/activity updates; and raw prompt/tool/result fields never enter persisted metadata.

- [ ] **Step 5: Implement dispatch integration**

Implement `DeriveActivityState`, add `reasonix` to `activitydispatch.Derivers`, and validate root `sessionId` values in `backend/internal/cli/hooks.go` before metadata or activity updates. UserPromptSubmit does not prove semantic acceptance; PermissionRequest has no correlation ID and does not mark blocked. SessionEnd is conversation rotation, not process exit.

- [ ] **Step 6: Run and commit**

Run:

```bash
cd backend && go test ./internal/adapters/agent/reasonix ./internal/adapters/agent/activitydispatch ./internal/cli -run 'Reasonix|reasonix' -count=1
```

Expected: PASS.

```bash
git add backend/internal/adapters/agent/reasonix backend/internal/adapters/agent/activitydispatch backend/internal/cli/hooks.go backend/internal/cli/hooks_test.go
git commit -m "feat: observe Reasonix session lifecycle"
```

### Task 3: Add truthful authentication, model, and installation support

**Files:**
- Create: `backend/internal/adapters/agent/reasonix/auth.go`
- Create: `backend/internal/adapters/agent/reasonix/auth_test.go`
- Modify: `backend/internal/adapters/agent/modelcatalog/catalog.go`
- Modify: `backend/internal/adapters/agent/modelcatalog/catalog_test.go`
- Modify: `backend/internal/service/systeminstall/systeminstall.go`
- Modify: `backend/internal/service/systeminstall/agentplans.go`
- Modify: `backend/internal/service/systeminstall/agentplans_test.go`

**Interfaces:**
- Produces: `AuthStatus(context.Context) (ports.AgentAuthStatus, error)` and direct-text model catalog policy for `reasonix`.
- Produces: `systeminstall.TargetReasonix` with official npm/Homebrew/manual plans.

- [ ] **Step 1: Write failing auth tests**

Use injected command output for `reasonix doctor --json`. Assert configured credentials report `configured`, explicit missing credentials report `unauthorized` only when the schema says so, malformed/unknown output reports `unknown`, and timeout/cancellation returns `unknown` with an error. Never assert `authorized` from `key_present` alone.

- [ ] **Step 2: Implement the bounded local probe**

Parse only documented JSON fields from a time-bounded subprocess. Do not read credential files directly or log command output.

- [ ] **Step 3: Add model and installer tests**

Assert `reasonix` returns `ModelSelectionText` with `CustomModelEntryDirect`, empty models, and source `manual`. Assert macOS prefers `brew install esengine/reasonix/reasonix` then npm package `reasonix`; Linux/Windows offer npm where the local package-manager contract supports it and otherwise return the official releases URL as manual guidance. Reinstall uses the existing safe package-manager shapes.

- [ ] **Step 4: Implement model and installer metadata**

Add `reasonix` to `customModelEntryMode`, add `TargetReasonix` to the fixed allowlists/enums, and add official documentation/install plans without a remote shell command.

- [ ] **Step 5: Run and commit**

Run:

```bash
cd backend && go test ./internal/adapters/agent/reasonix ./internal/adapters/agent/modelcatalog ./internal/service/systeminstall -count=1
```

Expected: PASS.

```bash
git add backend/internal/adapters/agent/reasonix backend/internal/adapters/agent/modelcatalog backend/internal/service/systeminstall
git commit -m "feat: add Reasonix readiness and installation metadata"
```

### Task 4: Verify a compatible native binary

**Files:**
- Create: `backend/internal/adapters/agent/reasonix/conformance_test.go`
- Create: `docs/harnesses/reasonix.md`

**Interfaces:**
- Consumes: the adapter from Tasks 1–3 and an explicitly selected compatible native build.
- Produces: opt-in real-binary conformance and exact build evidence, without claiming released compatibility.

- [ ] **Step 1: Write the opt-in conformance test**

In disposable fixture home, AO data, and workspace directories, exercise the selected native binary through AO's tmux runtime for version identity, prompt-role separation, strict TUI readiness, Unicode/leading-dash initial delivery, exact resume, and changed-file boundaries. Keep hook identity capture, all permission presets, and cancellation as separately reported coverage; do not imply that fixture-manifest identity proves daemon hook capture. Skip unless `AO_LIVE_REASONIX=1`; never copy credentials or use the real user's Reasonix profile.

- [ ] **Step 2: Run conformance against the compatible build**

Run: `cd backend && AO_LIVE_REASONIX=1 AO_REASONIX_TEST_BINARY=/absolute/path/to/reasonix go test ./internal/adapters/agent/reasonix -run TestReasonixLiveAOConformance -count=1 -v`

Expected: PASS with the pinned build, isolated fixture home/workspaces, a private tmux socket, and a local fake provider; no real user profile is used.

- [ ] **Step 3: Record evidence and limits**

Document the tested version, commit, checksums, platforms, install methods, authentication classification, prompt mechanism, permission mapping, hook schema, session ID, restore command, cancellation, free-form model behavior, and explicit non-support for Chat/reviewer/handoff/nested sessions.

- [ ] **Step 4: Fix failed conformance checks**

```bash
git add backend/internal/adapters/agent/reasonix/conformance_test.go docs/harnesses/reasonix.md
git commit -m "test: prove Reasonix harness conformance"
```

Fix failures in the AO integration before handoff. Unsupported binaries continue to fail closed at launch; upstream release publication is not required to finish this PR.

### Task 5: Register Reasonix across domain, storage, and API

**Files:**
- Modify: `backend/internal/domain/harness.go`
- Modify: `backend/internal/domain/harness_test.go`
- Modify: `backend/internal/domain/projectconfig_test.go`
- Modify: `backend/internal/adapters/agent/registry/registry.go`
- Modify: `backend/internal/adapters/agent/registry/registry_test.go`
- Modify: `backend/internal/daemon/wiring_test.go`
- Modify: `backend/internal/session_manager/manager.go`
- Modify: `backend/internal/session_manager/manager_test.go`
- Create: `backend/internal/storage/sqlite/migrations/<next>_allow_reasonix_harness.sql`
- Modify: `backend/internal/storage/sqlite/migrate_burned_versions_test.go`
- Modify: `backend/internal/storage/sqlite/migrate_test.go`
- Modify: `backend/internal/httpd/controllers/dto.go`
- Modify: `backend/internal/httpd/controllers/sessions_test.go`
- Generated: `backend/internal/httpd/apispec/openapi.yaml`
- Generated: `frontend/src/api/schema.ts`

**Interfaces:**
- Consumes: `reasonix.New()` and successful live conformance.
- Produces: `domain.HarnessReasonix`, production adapter resolution, persistent session validation, and worker/orchestrator API acceptance.

- [ ] **Step 1: Write failing registration tests**

Assert `HarnessReasonix == "reasonix"`, membership exactly once in `AllHarnesses`, registry manifest/agent pairing, daemon wiring, project config acceptance, and HTTP spawn decoding. Assert reviewer enums and Chat registries do not contain Reasonix.

- [ ] **Step 2: Add domain and registry registration**

Add the constant/list entry and one `reasonix.New()` constructor in stable order. Add Reasonix to `systemPromptFileRequired` so AO fails closed if its private prompt artifact cannot be created. Make no reviewer or Chat changes.

- [ ] **Step 3: Write the migration and tests**

Choose one above the highest migration on current `main`, widen only the latest `sessions.harness` CHECK for both known schema variants, provide the inverse Down rewrite, and append the exact filename to `shippedMigrations`. Test fresh migration, upgrade, downgrade, and a persisted Reasonix row.

- [ ] **Step 4: Update API source enums and regenerate**

Add `reasonix` only to worker/orchestrator/session/delegation enum tags in `dto.go`, update route tests, then run `npm run api`. Do not hand-edit generated OpenAPI or TypeScript output.

- [ ] **Step 5: Run and commit**

Run:

```bash
cd backend && go test ./internal/domain ./internal/adapters/agent/registry ./internal/daemon ./internal/session_manager ./internal/storage/sqlite ./internal/httpd/... -count=1
npm run api
git diff --check
```

Expected: PASS and regeneration is clean.

```bash
git add backend/internal/domain backend/internal/adapters/agent/registry backend/internal/daemon backend/internal/session_manager backend/internal/storage/sqlite backend/internal/httpd frontend/src/api/schema.ts
git commit -m "feat: register the Reasonix harness"
```

### Task 6: Add product identity and user-facing documentation

**Files:**
- Modify: `packages/product-ui/src/agents.ts`
- Modify: `packages/product-ui/src/agents.test.ts`
- Create: `frontend/src/renderer/assets/agents/reasonix.svg`
- Create: `frontend/src/renderer/assets/agents/LICENSE-reasonix.txt`
- Modify: `frontend/src/renderer/components/AgentAvatar.tsx`
- Modify: `frontend/src/renderer/components/AgentAvatar.test.tsx`
- Modify: `frontend/src/renderer/components/settings/HarnessSettingsSection.tsx`
- Modify: `frontend/src/renderer/components/settings/HarnessSettingsSection.test.tsx`
- Modify: `frontend/src/renderer/i18n/*.json`
- Modify: `README.md`
- Modify: `docs/README.md`
- Modify: `docs/STATUS.md`

**Interfaces:**
- Consumes: registered `reasonix` ID and installer metadata.
- Produces: selectable Reasonix identity, licensed logo, settings/install copy, and current documentation.

- [ ] **Step 1: Write failing product identity tests**

Assert `reasonix` is an `AgentId`, label is `Reasonix`, logo resolution uses the imported asset, fallback initials remain unaffected, and settings show the supported installation/authentication state without claiming Chat or reviewer support.

- [ ] **Step 2: Add the licensed asset and identity**

Copy the upstream MIT-licensed `docs/logo.svg` without altering its provenance, add the upstream copyright/license notice, and register it in product UI and renderer mappings.

- [ ] **Step 3: Update settings, translations, and docs**

Add concise localized labels using the repository's existing translation pattern. Add Reasonix to the README supported-agent table, docs index, and status list, linking `docs/harnesses/reasonix.md` and stating the required build capability and TUI-only limitation.

- [ ] **Step 4: Run and commit**

Run:

```bash
npm run frontend:typecheck
cd frontend && npm test -- AgentAvatar HarnessSettingsSection
```

Expected: PASS.

```bash
git add packages/product-ui frontend/src/renderer README.md docs/README.md docs/STATUS.md
git commit -m "feat: expose Reasonix in the desktop UI"
```

### Task 7: Complete verification and visual review

**Files:**
- Modify only files required to fix failures introduced by Tasks 1–6.

**Interfaces:**
- Consumes: the complete production integration.
- Produces: a locally verified branch ready for review; remote CI supplies unavailable platform/auth coverage.

- [ ] **Step 1: Run focused adapter and contract tests**

Run:

```bash
cd backend && go test ./internal/adapters/agent/reasonix ./internal/adapters/agent/activitydispatch ./internal/adapters/agent/registry ./internal/service/systeminstall ./internal/storage/sqlite ./internal/httpd/... -count=1
```

Expected: PASS.

- [ ] **Step 2: Run the complete local gate**

Run:

```bash
cd backend && go test ./...
cd backend && go test -race ./...
cd backend && go vet ./...
npm run api
npm run lint
npm run frontend:typecheck
cd frontend && npm run build
```

Expected: every command passes. Do not run `npx @redwoodjs/agent-ci run --all` locally.

- [ ] **Step 3: Audit generated and safety boundaries**

Confirm no reviewer/Chat enums changed, no old migration changed, no prompt/credential/runtime artifacts are tracked, no user Reasonix profile path is written, and `git diff --check` passes.

- [ ] **Step 4: Visually verify the real desktop app**

Use the isolated desktop-lab workflow with a fresh `npm ci` and scratch `AO_DATA_DIR`. Verify inventory, settings/install state, project default, task creation, TUI launch, restore, kill, and daemon-restart behavior. Capture exact gaps if authentication or an OS target is unavailable.

- [ ] **Step 5: Request review and verify remote CI**

Use the repository `pr-description` skill before creating/updating a PR. After push, inspect every required GitHub check and report unavailable authenticated-provider or OS coverage precisely.

- [ ] **Step 6: Commit any verification-only fixes**

```bash
git add <only-files-changed-to-fix-verification>
git commit -m "fix: complete Reasonix harness verification"
```

Skip this commit when verification required no changes.
