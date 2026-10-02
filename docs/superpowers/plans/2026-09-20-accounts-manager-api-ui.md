# Accounts Manager AO API and Accounts UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Connect the private Codex/Claude Accounts Manager lifecycle wrapper to a safe loopback-only AO API and a dedicated event-driven Accounts settings UI.

**Architecture:** The pristine CLIProxy engine remains behind the supervised runner. The runner emits authenticated OAuth lifecycle events, a daemon service projects safe opaque account state and publishes snapshots over AO SSE, and the frontend renders those snapshots without OAuth polling. Existing Subscriptions and device-global credentials remain isolated.

**Tech Stack:** Go 1.26, Chi/Gin HTTP, SSE, React, TanStack Query, TypeScript, OpenAPI generation.

**Spec:** `docs/superpowers/specs/2026-09-20-accounts-manager-codex-claude-scope-design.md`

## Global Constraints

- Keep `accounts-manager/engine` pristine.
- Support only Codex and Claude.
- Do not persist account catalog, OAuth sessions, credentials, or quota in SQLite.
- Never expose secrets, raw refs, filenames, paths, runner coordinates, or raw provider responses.
- Keep all account-management routes loopback-only.
- Do not modify existing Subscriptions behavior or device-global credentials.
- Do not add routing/session injection in this phase.
- Run focused local checks only; no Docker, full repository suites, full build, or full lint.

## Review Focus

- OAuth callback succeeds but token exchange later fails: publish a safe failed event, not completed.
- Daemon reconnects while runner OAuth remains pending: reconstruct the current pending operation from runner replay.
- Runner degrades after a successful list: preserve the safe list as stale and reject mutations.
- Stale or forged public account IDs: resolve against a fresh private catalog and mutate nothing on zero/multiple matches.
- Renderer loses SSE during a mutation: reconnect with one GET and accept only a newer revision.

---

### Task 1: Runner OAuth event stream

- [ ] Add failing runner tests for authenticated initial replay, terminal events, expiry, cancellation, reconnect, heartbeats, and redaction.
- [ ] Add bounded runner-owned lifecycle observation and `GET /ao/internal/oauth/events` SSE.
- [ ] Keep existing status endpoint as fallback and run focused runner tests.
- [ ] Commit `feat: stream accounts manager OAuth events`.

### Task 2: Daemon Accounts Manager service

- [ ] Add failing tests for safe projections, HMAC IDs, snapshot revision, stale preservation, mutations, and OAuth replay/reconnect.
- [ ] Add the daemon orchestration service and runner event client.
- [ ] Run focused backend service tests.
- [ ] Commit `feat: add accounts manager account service`.

### Task 3: Loopback-only AO API

- [ ] Add failing controller and LAN-block tests for every account lifecycle route, validation, redaction, and SSE.
- [ ] Add DTOs, controller routes, daemon wiring, stable error mapping, and API-spec registrations.
- [ ] Run focused controller tests and regenerate OpenAPI/frontend types.
- [ ] Commit `feat: expose accounts manager account API`.

### Task 4: Accounts settings UI

- [ ] Add failing focused frontend tests for snapshot/SSE flow, inline add methods, OAuth completion/cancel, secret clearing, account actions, details, and degraded state.
- [ ] Add the dedicated Accounts settings entry, hooks, API client, event transport, and inline Codex/Claude management UI.
- [ ] Keep Subscriptions untouched and run focused frontend tests plus typecheck.
- [ ] Commit `feat: add accounts manager settings UI`.

### Task 5: Focused integration and security verification

- [ ] Extend focused real-runner coverage through runner events, daemon service, and safe account mutations.
- [ ] Verify route/response/log redaction and engine pristine state.
- [ ] Run only the approved focused commands and perform a final whole-branch review.
- [ ] Commit any review fixes with focused RED-to-GREEN tests.
