# Accounts Manager Private Management Wrapper Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the AO daemon a secure, typed, private connection to the embedded CLIProxyAPI management API and prove it can inspect Codex/Claude credentials and routing state.

**Architecture:** The daemon creates a third private credential, `management.key`, beside the existing lifecycle and data-plane keys. The runner passes that key to CLIProxyAPI only at runtime through `WithLocalManagementPassword`; it is never persisted in `config.yaml`. The supervisor keeps the management endpoint daemon-internal, and a bounded client maps upstream responses into Codex/Claude-only internal types.

**Tech Stack:** Go 1.26, embedded CLIProxyAPI v7 SDK, loopback HTTP, existing AO runner/supervisor, `httptest`.

**Spec:** `docs/superpowers/specs/2026-09-20-accounts-manager-codex-claude-scope-design.md`

## Global Constraints

- Keep `accounts-manager/engine` pristine.
- Bind only to `127.0.0.1`; remote management and the upstream control panel remain disabled.
- Keep lifecycle, data-plane, and management credentials distinct and owner-only.
- Expose only Codex and Claude through AO integration types in this phase.
- Do not add frontend UI, public AO account routes, OAuth mutations, launch injection, or routing mutations.
- Do not log request bodies, auth-file payloads, tokens, keys, internal URLs, or raw upstream errors.
- Run only focused package tests; no Docker, full backend suite, full frontend build, or CLIProxyAPI full suite.

## Review Focus

- A missing, empty, permissive, symlinked, or replaced `management.key` must prevent runner startup without weakening other key checks.
- The data-plane client key must not authenticate to `/v0/management`, and the management key must not be returned by the public status API.
- Runner reattach must recover the same management endpoint without rotating keys or spawning a second process.
- Oversized, malformed, or non-JSON upstream responses must produce typed errors without including the response body.
- Auth-file listings must drop every provider except `codex` and `claude` and must never expose tokens, paths, raw filenames, or raw metadata.

---

### Task 1: Create and load a separate management credential

**Files:**
- Modify: `backend/internal/accountsmanager/state.go`
- Modify: `backend/internal/accountsmanager/supervisor_test.go`
- Modify: `accounts-manager/runner/internal/runner/state.go`
- Modify: `accounts-manager/runner/internal/runner/state_test.go`

**Interfaces:**
- Produces: `privateState.ManagementKey string` in the daemon.
- Produces: `runner.State.ManagementKey string` in the runner.
- Persists: `<AO StateDir>/accounts-manager/management.key` with mode `0600`.

- [ ] **Step 1: Add failing daemon state tests**

Extend the state initialization test to assert that `management.key` exists,
has mode `0600`, is non-empty, and differs from both `control.key` and the
single engine client key. Add table cases proving a symlink, empty file, and
group-readable file are rejected.

- [ ] **Step 2: Run the daemon package test and confirm failure**

Run:

```bash
cd backend
GOCACHE=/private/tmp/accounts-manager-go-cache go test ./internal/accountsmanager -run 'TestEnsureState|TestPrivate' -count=1
```

Expected: FAIL because `management.key` is not created or loaded.

- [ ] **Step 3: Extend daemon private state**

Add:

```go
const managementKeyFileName = "management.key"

type privateState struct {
    Root          string
    ConfigPath    string
    ControlKey    string
    ClientKey     string
    ManagementKey string
    Port          int
}
```

Use the existing `ensurePrivateKey` and `readPrivateFile` protections. Do not
write the management key into `config.yaml`.

- [ ] **Step 4: Add failing runner state tests**

Update valid fixtures to include `management.key`. Add cases for missing,
empty, symlinked, replaced, and permissive management-key files.

- [ ] **Step 5: Extend runner state loading**

Add `managementKeyName = "management.key"`, load it with
`readPrivateRegularFile`, reject an empty value, and return it as
`State.ManagementKey`.

- [ ] **Step 6: Run focused daemon and runner state tests**

Run:

```bash
cd backend
GOCACHE=/private/tmp/accounts-manager-go-cache go test ./internal/accountsmanager -run 'TestEnsureState|TestPrivate' -count=1
cd ../accounts-manager/runner
GOCACHE=/private/tmp/accounts-manager-runner-cache go test ./internal/runner -run 'TestLoadState' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit the private management credential**

```bash
git add backend/internal/accountsmanager/state.go backend/internal/accountsmanager/supervisor_test.go accounts-manager/runner/internal/runner/state.go accounts-manager/runner/internal/runner/state_test.go
git commit -m "feat: add accounts manager management credential"
```

### Task 2: Enable CLIProxyAPI's loopback management API

**Files:**
- Modify: `accounts-manager/runner/internal/runner/serve.go`
- Modify: `accounts-manager/runner/internal/runner/integration_test.go`

**Interfaces:**
- Consumes: `runner.State.ManagementKey` from Task 1.
- Produces: authenticated `/v0/management/*` routes on the existing runner port.

- [ ] **Step 1: Add a failing real-runner management authentication test**

Extend the focused integration test to prove:

```text
GET /v0/management/routing/strategy with no token           -> 401
GET /v0/management/routing/strategy with client.key         -> 401
GET /v0/management/routing/strategy with management.key     -> 200
GET /management.html                                        -> 404
```

Decode the successful response and assert the strategy is one of the engine's
supported values. Do not print either key on failure.

- [ ] **Step 2: Run the integration test and confirm failure**

Run:

```bash
cd accounts-manager/runner
GOCACHE=/private/tmp/accounts-manager-runner-cache go test ./internal/runner -run 'TestServe.*Management' -count=1
```

Expected: FAIL because the management routes are not enabled.

- [ ] **Step 3: Enable the existing upstream management API**

Update the builder chain:

```go
service, err := cliproxy.NewBuilder().
    WithConfig(state.Config).
    WithConfigPath(state.ConfigPath).
    WithLocalManagementPassword(state.ManagementKey).
    WithServerOptions(sdkapi.WithRouterConfigurator(func(router *gin.Engine, _ *handlers.BaseAPIHandler, _ *sdkconfig.Config) {
        router.GET("/ao/internal/identity", gin.WrapH(control))
        router.POST("/ao/internal/lease", gin.WrapH(control))
    })).
    Build()
```

Do not populate `remote-management.secret-key`; the credential must remain
runtime-only and localhost-only.

- [ ] **Step 4: Run the focused runner tests**

Run:

```bash
cd accounts-manager/runner
GOCACHE=/private/tmp/accounts-manager-runner-cache go test ./internal/runner -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit management API activation**

```bash
git add accounts-manager/runner/internal/runner/serve.go accounts-manager/runner/internal/runner/integration_test.go
git commit -m "feat: enable private accounts manager control API"
```

### Task 3: Carry private management connection material through supervision

**Files:**
- Modify: `backend/internal/accountsmanager/supervisor.go`
- Modify: `backend/internal/accountsmanager/supervisor_test.go`
- Modify: `backend/internal/httpd/controllers/accounts_manager_test.go`

**Interfaces:**
- Consumes: `privateState.ManagementKey` from Task 1.
- Produces:

```go
type Endpoint struct {
    BaseURL        string
    ClientToken    string
    ManagementToken string
}
```

`Endpoint` remains daemon-internal and is never serialized.

- [ ] **Step 1: Add failing spawn and reattach tests**

Assert that both a newly spawned runner and an authenticated reattach return
the same management token loaded from state. Extend the public status
redaction test to seed recognizable URL, client-token, and management-token
strings and verify none appear in JSON.

- [ ] **Step 2: Run focused supervisor/controller tests and confirm failure**

Run:

```bash
cd backend
GOCACHE=/private/tmp/accounts-manager-go-cache go test ./internal/accountsmanager ./internal/httpd/controllers -run 'AccountsManager|Reattach' -count=1
```

Expected: FAIL because `Endpoint` does not carry the management token.

- [ ] **Step 3: Extend the private endpoint**

Add `ManagementToken` to `Endpoint`, populate it only after successful
identity, health, and lease checks, and clear it whenever supervisor status is
not ready. Do not include it in `Status` or `RuntimeRecord`.

- [ ] **Step 4: Run focused supervisor/controller tests**

Run the command from Step 2.

Expected: PASS.

- [ ] **Step 5: Commit the supervisor bridge**

```bash
git add backend/internal/accountsmanager/supervisor.go backend/internal/accountsmanager/supervisor_test.go backend/internal/httpd/controllers/accounts_manager_test.go
git commit -m "feat: expose private accounts manager management endpoint"
```

### Task 4: Add a bounded Codex/Claude management client

**Files:**
- Create: `backend/internal/accountsmanager/management_client.go`
- Create: `backend/internal/accountsmanager/management_client_test.go`

**Interfaces:**
- Consumes:

```go
type EndpointSource interface {
    Endpoint() (Endpoint, bool)
}
```

- Produces:

```go
type Provider string

const (
    ProviderCodex  Provider = "codex"
    ProviderClaude Provider = "claude"
)

type CredentialSummary struct {
    Ref          string
    Provider     Provider
    Kind         string
    Email        string
    Status       string
    Disabled     bool
    ObservedAt   time.Time
}

type RoutingStrategy string

const (
    RoutingRoundRobin         RoutingStrategy = "round-robin"
    RoutingWeightedRoundRobin RoutingStrategy = "weighted-round-robin"
    RoutingFillFirst          RoutingStrategy = "fill-first"
)

type ManagementClient struct {
    source EndpointSource
    client *http.Client
}

func NewManagementClient(source EndpointSource, client *http.Client) *ManagementClient
func (c *ManagementClient) ListCredentials(ctx context.Context) ([]CredentialSummary, error)
func (c *ManagementClient) RoutingStrategy(ctx context.Context) (RoutingStrategy, error)
```

`Ref` is an opaque daemon-internal locator derived from upstream `auth_index`;
it is not yet a public AO account ID.

- [ ] **Step 1: Write failing transport and parsing tests**

Use `httptest.Server` and a fake `EndpointSource` to cover:

- ready endpoint sends `Authorization: Bearer <management token>`;
- unavailable source returns `ErrUnavailable` without making a request;
- non-2xx returns a typed status error without copying the response body;
- responses larger than 1 MiB are rejected;
- malformed JSON is rejected;
- cancellation and a five-second client timeout propagate;
- a mixed auth-file response returns only `codex` and `claude` entries;
- token, path, filename, and raw metadata values do not appear in summaries or errors;
- routing accepts exactly the three upstream strategies and rejects unknown values.

- [ ] **Step 2: Run the client tests and confirm failure**

Run:

```bash
cd backend
GOCACHE=/private/tmp/accounts-manager-go-cache go test ./internal/accountsmanager -run 'TestManagementClient' -count=1
```

Expected: FAIL because the client does not exist.

- [ ] **Step 3: Implement the bounded transport**

Create a single private request helper that:

```go
req.Header.Set("Authorization", "Bearer "+endpoint.ManagementToken)
req.Header.Set("Accept", "application/json")
```

It must build URLs only by joining the verified loopback `BaseURL` with
hardcoded management paths. It must use `io.LimitReader` with a 1 MiB limit,
close response bodies, and return stable sentinel/typed errors that contain
only operation name and HTTP status.

- [ ] **Step 4: Implement read-only proof methods**

`ListCredentials` calls `/v0/management/auth-files`, maps only provider values
`codex` and `claude`, and copies only the fields represented by
`CredentialSummary`. `RoutingStrategy` calls
`/v0/management/routing/strategy` and validates the exact enum.

- [ ] **Step 5: Run focused client tests**

Run the command from Step 2.

Expected: PASS.

- [ ] **Step 6: Commit the management client**

```bash
git add backend/internal/accountsmanager/management_client.go backend/internal/accountsmanager/management_client_test.go
git commit -m "feat: add accounts manager management client"
```

### Task 5: Prove daemon-to-real-runner management access

**Files:**
- Modify: `backend/internal/accountsmanager/supervisor_test.go`
- Modify: `accounts-manager/runner/internal/runner/integration_test.go`

**Interfaces:**
- Consumes: ready `Supervisor.Endpoint()` and `NewManagementClient` from Tasks 3–4.
- Produces: an end-to-end regression proving the private management channel.

- [ ] **Step 1: Add the end-to-end focused test**

Start the real runner with temporary private state, attach a supervisor/client,
then assert:

1. the runner becomes ready;
2. `ListCredentials` returns an empty Codex/Claude list for the empty auth dir;
3. `RoutingStrategy` returns the configured default;
4. the lifecycle key and client key both fail against the management route;
5. public `Status` contains only safe state/version data;
6. stopping the daemon-side context leaves the runner available for lease-window reattach.

Keep provider network access disabled; this test must not start OAuth or contact
OpenAI/Anthropic.

- [ ] **Step 2: Run focused runner and backend tests**

Run:

```bash
cd accounts-manager/runner
GOCACHE=/private/tmp/accounts-manager-runner-cache go test ./internal/runner -count=1
cd ../../../backend
GOCACHE=/private/tmp/accounts-manager-go-cache go test ./internal/accountsmanager ./internal/httpd/controllers -run 'AccountsManager|ManagementClient|Reattach' -count=1
```

Expected: PASS.

- [ ] **Step 3: Verify formatting and platform compilation**

Run:

```bash
gofmt -d backend/internal/accountsmanager/*.go accounts-manager/runner/internal/runner/*.go
cd backend
GOCACHE=/private/tmp/accounts-manager-go-cache GOOS=windows GOARCH=amd64 go test -c -o /private/tmp/accountsmanager-windows.test.exe ./internal/accountsmanager
git diff --check
```

Expected: no formatting diff, Windows compile succeeds, and `git diff --check`
prints nothing.

- [ ] **Step 4: Commit integration coverage**

```bash
git add backend/internal/accountsmanager/supervisor_test.go accounts-manager/runner/internal/runner/integration_test.go
git commit -m "test: prove accounts manager management channel"
```

## Completion Boundary

This phase is complete when the daemon can privately and safely read the
embedded engine's Codex/Claude credential inventory and routing strategy from
a real runner. No renderer or public AO account-management API is added.

The following phase will add Codex/Claude account commands—OAuth start/status/
cancel, API-key management, enable/disable, refresh, remove, quota reads, and
routing mutations—on top of this private client, followed by the Accounts
Manager UI.
