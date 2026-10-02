# Accounts Manager Codex and Claude Scope Design

## Purpose

AO will build Accounts Manager on the vendored CLIProxyAPI engine. The first
product scope is deliberately limited to Codex and Claude, but it will use the
engine's complete account-management and routing capabilities for those two
providers. AO will not reimplement OAuth, credential storage, token refresh,
account pooling, quota handling, request translation, or routing.

The existing device-global Subscriptions implementation remains available
while Accounts Manager is built in parallel. Accounts Manager must not depend
on that implementation so it can replace the old system cleanly later.

## Provider Boundary

The initial AO provider allowlist contains:

- Codex / OpenAI
- Claude / Anthropic

The vendored CLIProxyAPI source remains pristine and retains support for its
other providers. AO's integration layer, public API, settings UI, and launch
configuration expose only Codex and Claude in this phase. Adding another
provider later should require an AO capability/UI addition, not an engine
rewrite.

## Included CLIProxyAPI Services

### 1. Local API gateway

Accounts Manager runs CLIProxyAPI as an AO-supervised, loopback-only service.
AO uses the Codex/OpenAI and Claude/Anthropic request surfaces required by
Codex CLI and Claude Code. Streaming, non-streaming, tool calls, supported
multimodal payloads, and relevant WebSocket flows remain engine-owned.

### 2. Protocol translation

CLIProxyAPI owns request and response translation between supported client
protocols and the selected upstream credential/provider. AO does not create a
second translation layer.

### 3. Account and credential management

AO exposes the CLIProxyAPI credential mechanisms that are valid for Codex or
Claude, including OAuth, supported device/browser flows, provider API keys,
and valid credential-file operations. The engine owns credential validation,
serialization, token refresh, and provider-specific metadata.

Credentials remain under `<AO StateDir>/accounts-manager/auth`. Device-global
Codex and Claude credential files are not the Accounts Manager source of truth
and are not mutated by this system.

### 4. Credential watcher and hot reload

CLIProxyAPI's watcher keeps the runtime credential pool synchronized with the
private auth directory. Add, update, refresh, enable/disable, and remove
operations must become visible without restarting AO or the runner.

### 5. Multi-account pools

Codex and Claude maintain independent pools containing all locally configured
credentials for that provider. AO consumes the engine's credential identity,
availability, health, and cooldown state instead of creating a parallel
account-state database.

### 6. Routing and reliability

AO uses the engine's routing strategy, account selection, retries, cooldowns,
quota-aware fallback, model aliases, model exclusions, and provider proxy
configuration for Codex and Claude. New AO sessions use the routing policy
that exists when they start. Once session pinning is implemented, an existing
conversation remains pinned to its already-selected account so a routing
change cannot switch identity mid-conversation.

### 7. Quota and capacity signals

AO displays the Codex and Claude quota, rate-limit, cooldown, and reset data
that CLIProxyAPI can actually provide. Provider capabilities may differ; AO
must not invent symmetric fields or treat missing quota data as an invalid
credential. Quota and routing health may influence future request selection,
but they do not delete accounts.

CLIProxyAPI does not provide a complete persistent historical usage analytics
product. Historical dashboards and cost accounting are outside this scope.

### 8. Private management API

The runner enables CLIProxyAPI's existing management API with an AO-owned,
runtime-only management credential. The daemon accesses it through a private
client and exposes only safe AO contracts. The renderer never receives the
runner port, management credential, raw token, credential path, auth filename,
or unfiltered management response.

The AO facade covers the Codex/Claude portions of:

- OAuth start, status, completion, and cancellation;
- API-key and credential-file management;
- account listing, metadata, enable/disable, refresh, and removal;
- account models and safe provider capabilities;
- quota fetch/reset where supported;
- routing, retry, cooldown, model alias, model exclusion, and provider proxy
  settings.

## AO Integration Boundary

- `accounts-manager/engine` stays an unmodified upstream snapshot.
- `accounts-manager/runner` embeds the public CLIProxyAPI SDK and owns only
  AO lifecycle/control integration.
- The AO daemon supervises the runner and translates private management data
  into validated, redacted AO types.
- The frontend is a thin Accounts Manager UI over the AO daemon API.
- Existing Subscriptions account management and Accounts Manager remain
  isolated until the new flow reaches functional parity and the old system is
  removed in a separate cleanup phase.

## Explicitly Deferred

This scope does not expose or productize:

- Gemini, Antigravity, Kimi, xAI, Devin, Meta, Vertex, or custom providers;
- the CLIProxyAPI plugin system or plugin store;
- the CLIProxyAPI TUI or web management panel;
- raw logs, debug configuration, or upstream update management;
- LAN discovery, remote management, or Home/control-plane integration;
- persistent historical usage/cost analytics;
- automatic import or mutation of device-global Codex or Claude credentials;
- removal of the existing Subscriptions implementation before Accounts
  Manager reaches the required replacement milestone.

The deferred engine code remains in the pristine upstream snapshot. AO simply
does not expose those capabilities yet.

## Security and Failure Rules

- The runner binds only to `127.0.0.1` and requires separate lifecycle,
  management, and client credentials.
- Private files remain owner-only and unsafe paths, symlinks, or replacements
  are rejected.
- Account secrets never enter AO's public API, logs, telemetry, or frontend
  state.
- Accounts Manager failure degrades only Accounts Manager; the AO daemon,
  unrelated agents, and existing sessions remain available.
- Provider, quota, or routing failures preserve stored credentials and return
  typed, retryable status where appropriate.

## Delivery Sequence

1. Complete the private CLIProxyAPI management wrapper for Codex and Claude.
2. Add the Accounts Manager account/catalog UI using the safe AO API.
3. Add routing configuration and Codex/Claude launch integration.
4. Add durable per-session account pinning before allowing routing changes to
   affect newly launched work.
5. Validate functional parity, then remove the superseded Subscriptions
   account-management implementation in a separate cleanup change.

## Success Criteria

- AO can add and manage multiple Codex and Claude credentials using
  CLIProxyAPI's native mechanisms.
- Credential changes hot-reload into independent Codex and Claude pools.
- Requests can be routed, retried, streamed, and failed over using the engine's
  existing behavior.
- AO exposes safe account, quota, and routing controls without leaking private
  runner details or secrets.
- Existing device-global account management continues to work during the
  parallel-build period.
- No Codex/Claude authentication, translation, routing, or quota subsystem is
  duplicated in AO.
