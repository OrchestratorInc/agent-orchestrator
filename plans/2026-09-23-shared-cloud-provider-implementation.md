# Shared Cloud Provider Preference Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A provider selected on desktop is stored for the authenticated Cloud user and is automatically used by new desktop and mobile sessions.

**Architecture:** Add a per-user preference with an authenticated read/write API, then resolve it on the control plane when a top-level session create request omits `provider`. Desktop migrates its legacy localStorage value once and uses the Cloud preference thereafter. Mobile keeps sending provider-free create requests; linked workers and explicit overrides retain their current precedence.

**Tech Stack:** Go 1.26.5, PostgreSQL/goose, Chi HTTP, OpenAPI 3.1, generated `@aoagents/cloud-client` types, React/TanStack Query, Vitest, Expo.

**Spec:** `plans/2026-09-23-shared-cloud-provider-design.md`

## Global Constraints

- Do not add, stage, or commit any file under `docs/`.
- The preference is per authenticated user across organizations; credentials and harness selection are unchanged.
- An explicit create-session `provider` wins; a linked worker inherits its orchestrator's provider; only an omitted provider on a top-level session consults the preference.
- An unset preference uses the deployment default. A saved provider that is no longer available returns `provider_unavailable`, not a silent fallback.
- Existing session/provider records never change as part of preference migration or updates.
- Preserve unrelated dirty-worktree changes and stage exact files only. Do not publish or deploy during validation.

## Review Focus

- Two desktops migrate different legacy values concurrently: only the first atomic `initializeOnly` write succeeds; the loser reloads Cloud's value (Task 1 and Task 4 tests).
- A saved provider is removed from the deployment's available list: new top-level sessions fail with `provider_unavailable`; old sessions remain unchanged (Task 2 tests).
- A worker is created under an active orchestrator while a different user preference exists: it still inherits the orchestrator provider (Task 2 tests).
- A desktop signs out or signs into another account: preference cache and migration state must not leak between user IDs (Task 4 tests).
- A preference save fails or returns a conflict: the UI must show the authoritative value and an error/reload state, never a falsely applied selection (Task 4 tests).

---

## File map

- `cloud/internal/postgres/migrations/00040_user_sandbox_preferences.sql`: new owner-scoped preference table and reversible migration.
- `cloud/scripts/test-cloud-local.sh`: isolated PostgreSQL migration/RLS smoke assertions.
- `cloud/internal/postgres/user_preferences_store.go`: user-scoped read, normal write, atomic initialize-only write.
- `cloud/internal/httpapi/user_preferences_handlers.go`: authenticated JSON GET/PUT handlers and request validation.
- `cloud/internal/httpapi/server.go`, `resource_handlers.go`: store contract, routes, and server-side provider precedence.
- `cloud/internal/httpapi/user_preferences_handlers_test.go`, `resource_handlers_autolink_test.go`: handler and session-creation behavior with fakes.
- `contracts/cloud/openapi.yaml`, `packages/cloud-client/src/{schema,types,client}.ts`, `packages/cloud-client/test/client.test.ts`: public contract and generated/client API (`index.ts` already re-exports all types).
- `frontend/src/renderer/lib/cloud-cp/{types,client}.ts`: desktop's control-plane client for the preference API.
- `frontend/src/renderer/hooks/useCloudProviderPreference.ts`: account-keyed query, mutation, and one-time legacy migration.
- `frontend/src/renderer/components/{CloudOnboardingGate,TaskComposer}.tsx`, `frontend/src/renderer/components/settings/CloudProviderSection.tsx`, `frontend/src/renderer/lib/cloud-orchestrator.ts`, `frontend/src/renderer/stores/sandbox-provider-store.ts`: mount migration once, replace local selection, and stop overriding session creates.
- Nearby `.test.ts(x)` files plus `packages/mobile/lib/cloud/{orchestrator,source}.test.ts`: targeted cross-client regression coverage.

### Task 1: Persist one user preference safely

**Files:** Create `cloud/internal/postgres/migrations/00040_user_sandbox_preferences.sql`, `cloud/internal/postgres/user_preferences_store.go`; modify `cloud/internal/httpapi/server.go` store interface and `cloud/scripts/test-cloud-local.sh`.

**Interfaces:** Produce `GetUserSandboxProvider(context.Context, string) (string, bool, error)` and `PutUserSandboxProvider(context.Context, string, string, bool) (string, error)`. The second argument is authenticated user ID; empty provider represents JSON `null`; the boolean on `Put` is `initializeOnly`. Return `postgres.ErrConflict` when an initialize-only insert loses a race.

- [ ] **Step 1: Write a failing persistence smoke assertion.** Extend the existing local Cloud smoke (`cloud/scripts/test-cloud-local.sh`) to assert that migration `00040` creates `ao_user_sandbox_preferences` with an enabled/forced RLS policy. Run the following SQL through the script's existing local PostgreSQL connection; its relation lookup fails before the migration exists.

```sql
SELECT CASE WHEN relrowsecurity AND relforcerowsecurity THEN 1 ELSE 0 END
FROM pg_class WHERE oid = 'ao_user_sandbox_preferences'::regclass;
```

- [ ] **Step 2: Run the smoke and confirm the new assertion fails before implementation.** Run it with an isolated `AO_DATA_DIR`, never the user's real `~/.ao` data. Expect relation `ao_user_sandbox_preferences` not found. If Docker is unavailable, record that limitation and keep the assertion for CI/local verification later.

```bash
smoke_data_dir="$(mktemp -d /tmp/ao-cloud-pref-smoke.XXXXXX)"
AO_DATA_DIR="$smoke_data_dir" bash cloud/scripts/test-cloud-local.sh
smoke_status=$?
rmdir "$smoke_data_dir"
test "$smoke_status" -eq 0
```
- [ ] **Step 3: Add the new migration and store methods.** Use a user-owned row rather than modifying founding migrations. The write SQL must use `INSERT ... ON CONFLICT` for normal writes and `INSERT ... ON CONFLICT DO NOTHING RETURNING` for `initializeOnly`; no returned row means `postgres.ErrConflict`. Run reads/writes inside `withUser(ctx, userID, ...)`.

```sql
-- +goose Up
CREATE TABLE ao_user_sandbox_preferences (
    user_id UUID PRIMARY KEY REFERENCES ao_users(id) ON DELETE CASCADE,
    sandbox_provider TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE ao_user_sandbox_preferences ENABLE ROW LEVEL SECURITY;
ALTER TABLE ao_user_sandbox_preferences FORCE ROW LEVEL SECURITY;
CREATE POLICY ao_user_sandbox_preferences_owner_policy ON ao_user_sandbox_preferences
    USING (user_id = ao_current_user_id()) WITH CHECK (user_id = ao_current_user_id());
-- +goose Down
DROP TABLE ao_user_sandbox_preferences;
```

The read query is `SELECT sandbox_provider FROM ao_user_sandbox_preferences WHERE user_id = $1`; `pgx.ErrNoRows` means `("", false, nil)` while a row containing SQL `NULL` means `("", true, nil)`. For `initializeOnly`, use `INSERT ... ON CONFLICT DO NOTHING RETURNING sandbox_provider` and map no returned row to `postgres.ErrConflict`; normal writes use `INSERT ... ON CONFLICT (user_id) DO UPDATE ... RETURNING sandbox_provider`.

- [ ] **Step 4: Run `cd cloud && go test ./internal/postgres ./internal/httpapi` and rerun the isolated local Cloud migration smoke from Step 2 if Docker is available.** If Docker is unavailable, report that exact gap and rely on CI's database job; do not claim the migration ran.
- [ ] **Step 5: Commit only this task's migration/store/interface files and tests** with `feat: persist cloud provider preference`. No `docs/` files.

### Task 2: Expose the preference and apply it at session creation

**Files:** Create `cloud/internal/httpapi/user_preferences_handlers.go`; modify `cloud/internal/httpapi/{server,resource_handlers}.go`; extend `cloud/internal/httpapi/{user_preferences_handlers,resource_handlers_autolink}_test.go`.

**Interfaces:** Consume Task 1's store methods. Produce `GET /api/cloud/v1/me/preferences -> {"sandboxProvider": string|null}` and `PUT /api/cloud/v1/me/preferences` accepting `{sandboxProvider: string|null, initializeOnly?: boolean}`. Both require `server.authenticate` and use `principalFrom(r).UserID`.

- [ ] **Step 1: Write failing HTTP tests** for unauthenticated access, missing/unknown JSON fields, null/default, valid `coder`, unavailable provider (422 `provider_unavailable`), and initialize-only conflict (409 `preference_conflict`). Use a fake embedding `Store` and implementing the two Task 1 preference methods, including conflict when its configured flag is already true. Extend `stubAutolinkStore` with `GetUserSandboxProvider` and table-test omitted provider for orchestrator/top-level worker, explicit override, linked worker inheritance, and saved-provider-unavailable failure.

```go
type preferenceFakeStore struct {
    Store
    provider string
    configured bool
}
func (s *preferenceFakeStore) GetUserSandboxProvider(_ context.Context, _ string) (string, bool, error) {
    return s.provider, s.configured, nil
}
func (s *stubAutolinkStore) GetUserSandboxProvider(_ context.Context, _ string) (string, bool, error) {
    return s.preference, s.preferenceConfigured, nil
}
```

- [ ] **Step 2: Confirm red.** Run `cd cloud && go test ./internal/httpapi -run 'TestUserSandboxPreference|TestCreateSession.*Provider' -count=1` and confirm the new assertions fail for the intended missing behavior.
- [ ] **Step 3: Register authenticated GET/PUT routes and implement handlers.** Decode the PUT with `json.RawMessage` for `sandboxProvider` so omitted differs from explicit `null`; reject empty/whitespace provider strings. Validate against `s.availableSandboxProviders` before storing. On `postgres.ErrConflict`, return the standard 409 envelope with `preference_conflict`.

```go
router.With(server.authenticate).Get("/me/preferences", server.getUserPreferences)
router.With(server.authenticate).Put("/me/preferences", server.putUserPreferences)

type putUserPreferencesRequest struct {
    SandboxProvider json.RawMessage `json:"sandboxProvider"`
    InitializeOnly bool `json:"initializeOnly,omitempty"`
}
```

- [ ] **Step 4: Resolve omitted provider in `createSession` after worker auto-link and before availability/provisioning validation.** Do not query or apply the preference when `request.Provider` is already set by the client or inherited from an orchestrator. Keep the provider stamped into `domain.CreateSession` as today.

```go
if request.Provider == "" {
    preferred, configured, err := s.store.GetUserSandboxProvider(r.Context(), principalFrom(r).UserID)
    if err != nil { s.writeStoreError(w, r, err); return }
    if configured { request.Provider = preferred }
}
```

- [ ] **Step 5: Green tests and commit.** Run `cd cloud && go test ./internal/httpapi/... ./internal/postgres/...`; commit only touched Cloud code/tests with `feat: apply account provider to new cloud sessions`.

### Task 3: Publish the Cloud contract and both typed clients

**Files:** Modify `contracts/cloud/openapi.yaml`, `packages/cloud-client/src/{types,client}.ts`, generated `packages/cloud-client/src/schema.ts`, `packages/cloud-client/test/client.test.ts`, and `frontend/src/renderer/lib/cloud-cp/{types,client,client.test}.ts`.

**Interfaces:** Produce `UserCloudPreferences { sandboxProvider: string | null }`, `PutUserCloudPreferencesInput { sandboxProvider: string | null; initializeOnly?: boolean }`, and matching `getUserPreferences`/`putUserPreferences` client methods. Add optional `provider?: string` to `CreateSessionInput`, matching the already-served Go request.

- [ ] **Step 1: Add failing client tests.** In both client test files, assert GET and PUT hit `/api/cloud/v1/me/preferences`, send bearer auth through existing transport, serialize `initializeOnly` correctly, and preserve the standard error envelope on 409/422.

```ts
await expect(client.getUserPreferences()).resolves.toEqual({ sandboxProvider: "coder" });
await client.putUserPreferences({ sandboxProvider: "coder", initializeOnly: true });
expect(fetchMock).toHaveBeenCalledWith(
  "https://cloud.example.com/api/cloud/v1/me/preferences",
  expect.objectContaining({ method: "PUT" }),
);
```

- [ ] **Step 2: Confirm red.** Run `npm --prefix packages/cloud-client test -- client.test.ts` and `npm --prefix frontend test -- cloud-cp/client.test.ts`; expect missing methods.
- [ ] **Step 3: Add OpenAPI path/schemas and both client methods.** Use the existing `request`/`requestJson` transport; do not create a separate authentication path. Export new public type aliases from `packages/cloud-client/src/types.ts` (the existing `index.ts` wildcard exports them). Regenerate `schema.ts` with `npm --prefix packages/cloud-client run generate`, never hand-edit generated types.

```ts
getUserPreferences(options: RequestOptions = {}): Promise<UserCloudPreferences> {
  return this.request("/api/cloud/v1/me/preferences", options);
}
putUserPreferences(input: PutUserCloudPreferencesInput, options: RequestOptions = {}): Promise<UserCloudPreferences> {
  return this.request("/api/cloud/v1/me/preferences", { method: "PUT", body: input, signal: options.signal });
}

// In the renderer's createCloudCpClient return object:
getUserPreferences: (o) => requestJson("GET", "/me/preferences", { signal: o?.signal }),
putUserPreferences: (body, o) => requestJson("PUT", "/me/preferences", { body, signal: o?.signal }),
```

- [ ] **Step 4: Green/typecheck and commit.** Run `npm --prefix packages/cloud-client run typecheck`, `npm --prefix packages/cloud-client test`, `npm --prefix frontend run typecheck`; commit only contract/client files with `feat: expose cloud provider preference API`.

### Task 4: Migrate desktop once and make Cloud authoritative

**Files:** Create `frontend/src/renderer/hooks/{useCloudProviderPreference.ts,useCloudProviderPreference.test.ts}` and `frontend/src/renderer/components/CloudOnboardingGate.test.tsx`; modify `frontend/src/renderer/components/CloudOnboardingGate.tsx`, `frontend/src/renderer/components/settings/{CloudProviderSection.tsx,CloudProviderSection.test.tsx}`, and `frontend/src/renderer/stores/{sandbox-provider-store.ts,sandbox-provider-store.test.ts}`.

**Interfaces:** Consume Task 3's desktop `getUserPreferences`/`putUserPreferences`. The hook returns `{ provider: string|null, loading: boolean, saving: boolean, error: string|null, setProvider(value: string|null): Promise<void> }`; its query key includes control-plane base URL and authenticated `session.user.id`.

- [ ] **Step 1: Write failing hook/UI tests.** Cover Cloud value winning over legacy localStorage, valid legacy value seeded with `initializeOnly`, migration conflict reloading remote value, unavailable legacy value not seeded, sign-out/account switch clearing query and migration state, and a failed PUT leaving the displayed provider unchanged while showing an error.

```ts
expect(cloudMocks.putUserPreferences).toHaveBeenCalledWith({
  sandboxProvider: "coder", initializeOnly: true,
});
expect(screen.getByText("Coder")).toBeInTheDocument();
```

- [ ] **Step 2: Confirm red.** Run `npm --prefix frontend test -- CloudProviderSection.test.tsx CloudOnboardingGate.test.tsx useCloudProviderPreference.test.ts`.
- [ ] **Step 3: Implement the account-keyed query/mutation and one-time migration.** Mount migration in `CloudOnboardingGate`, which is already mounted once at renderer root. Read legacy key only for migration, not future selection. On 409, refetch Cloud and clear legacy local value; on network error keep legacy value for a later retry but do not present it as applied. Remove the old `selectedProvider` store state only after usages are migrated in Task 5.

```ts
const preferenceQueryKey = (baseUrl: string, userId: string) =>
  ["cloud-provider-preference", baseUrl, userId] as const;
```

- [ ] **Step 4: Wire selector to server data/save state.** The existing available-provider query still limits options. Disable selection during a save, show a save error, and avoid optimistic success when the PUT fails. Keep the signed-out and single-provider presentation behavior.

```tsx
<SettingsOptionMenu
  aria-label={t("settings.cloudProvider.label")}
  value={effective}
  options={options}
  disabled={loading || saving}
  onChange={(next) => { void setProvider(next); }}
/>
{error ? <p role="alert">{error}</p> : null}
```
- [ ] **Step 5: Green tests and commit.** Run `npm --prefix frontend test -- CloudProviderSection.test.tsx CloudOnboardingGate.test.tsx useCloudProviderPreference.test.ts` and `npm --prefix frontend run typecheck`; commit only this task's files with `feat: sync desktop cloud provider preference`.

### Task 5: Remove desktop-only create overrides and prove mobile inheritance

**Files:** Modify `frontend/src/renderer/lib/{cloud-orchestrator.ts,cloud-orchestrator.test.ts}`, `frontend/src/renderer/components/{TaskComposer.tsx,TaskComposer.test.tsx}`, and `frontend/src/renderer/stores/sandbox-provider-store.ts`; extend `packages/mobile/lib/cloud/{orchestrator.test.ts,source.test.ts}`.

**Interfaces:** Consume Task 2's server-side preference resolution. Both desktop create paths and both mobile create paths omit `provider`; the Cloud server decides it. Do not add a mobile picker.

- [ ] **Step 1: Change tests first.** Assert desktop orchestrator and task creates omit `provider` even after a preference is selected; mobile orchestrator and top-level worker creates also omit it. Assert the server-side Task 2 tests cover provider assignment, so the mobile tests do not mock a false local selection.

```ts
expect(cloudMocks.createSession).toHaveBeenCalledWith("org-1", expect.not.objectContaining({ provider: expect.any(String) }));
```

- [ ] **Step 2: Confirm red.** Run `npm --prefix frontend test -- cloud-orchestrator.test.ts TaskComposer.test.tsx` and `npm --prefix packages/mobile test -- orchestrator.test.ts source.test.ts`.
- [ ] **Step 3: Remove `readSelectedSandboxProvider()` and store subscriptions from these create paths.** Keep harness selection, prompt, and all other request fields unchanged. Replace the old Zustand selection store with a small legacy-key read/clear helper used only by Task 4's migration.

```ts
const { session } = await client.createSession(orgId, {
  projectId, kind: "orchestrator", harness, displayName: "Orchestrator", prompt: "",
});
```

- [ ] **Step 4: Green focused tests and typechecks.** Run `npm --prefix frontend test -- cloud-orchestrator.test.ts TaskComposer.test.tsx`, `npm --prefix packages/mobile test -- orchestrator.test.ts source.test.ts`, `npm --prefix frontend run typecheck`, and `npm --prefix packages/mobile run typecheck`.
- [ ] **Step 5: Commit only this task's code/tests** with `fix: use account provider for desktop and mobile starts`.

### Task 6: Cross-layer validation and handoff

**Files:** No product files unless a check exposes a scoped defect.

- [ ] **Step 1: Re-read the spec and inspect the final diff.** Check the migration, API shape, provider precedence, one-time legacy seeding, existing-session non-mutation, and no `docs/` paths staged. Run `git diff --check` and `git diff --name-only` against the task's baseline.
- [ ] **Step 2: Run complete relevant suites.** Run `cd cloud && go test ./...`, `npm --prefix packages/cloud-client run typecheck`, `npm --prefix packages/cloud-client test`, `npm --prefix frontend run typecheck`, `npm --prefix frontend test`, `npm --prefix packages/mobile run typecheck`, and `npm --prefix packages/mobile test`. Run the isolated local Cloud smoke from Task 1 if Docker is available; otherwise report it as unverified and check CI.
- [ ] **Step 3: Verify without deploying.** In a local Cloud fixture, set Coder on desktop, confirm the preference endpoint returns Coder, create an orchestrator and standalone worker from mobile, and read their `sandboxProvider` as `coder`. Confirm a linked worker inherits its orchestrator's provider even after switching the user preference. Do not use production projects for this check.
- [ ] **Step 4: Report outcome and gaps.** State which suites and local Cloud checks actually ran, that existing failed sessions were not repaired, and whether a deployment is still needed before the user's installed desktop/mobile builds benefit.
