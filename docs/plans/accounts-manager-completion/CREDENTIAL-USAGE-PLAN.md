# Credential verification and account usage

Scope: correct manual credential acceptance and expose supported account usage through the existing managed-account service and desktop controls. User choice remains authoritative. No automatic selection, fallback, native switching changes, Subscriptions changes, or publication.

## Delivery order

1. Credential boundary. Reproduce arbitrary-key acceptance with a provider rejection fixture. Validate manual keys and imported tokens before committing or exposing them as usable. Use bounded, cancellable, non-generating requests; never follow redirects or return provider bodies, secrets, or private URLs. Distinguish invalid credentials from temporary inability to verify. Preserve operation idempotency and encrypted storage.
2. Stored credential safety. Expose durable verification facts, recheck already saved manual credentials explicitly, and prevent unverified manual credentials from new route authorization. Successful browser/device exchange remains provider-authenticated. Do not silently delete legacy credentials or silently send their secrets on inventory reads.
3. Usage service. Fetch provider-reported quota only for supported credential types, inside the runner that owns the secret. Return bounded normalized windows and freshness through existing DTOs. Unknown, malformed, offline, and unsupported readings must never become zero usage. Coalesce overlapping checks and avoid a network request per render. Keep local counters disabled rather than presenting them as plan quota.
4. Desktop controls. Show verification and usage beside the chosen account and in expanded account details. Include reset time, refresh, pending/error/unavailable, and observation time. Ignore stale responses after account changes. Preserve explicit account/mode choices and existing removal controls. Add locale keys through the existing catalogs.
5. Integration and handoff. Review each slice after initial green, run affected race suites, frontend tests/typecheck/build, generated contract drift, build/vet/lint and protected-path audit. Verify the actual isolated desktop app after reading the desktop skill. Record unavailable live-provider and native-platform acceptance separately. Freeze exact changed-file hashes and report for independent review.

## Acceptance decisions

- A key-shaped string is not proof of authentication. A successful authenticated provider response is required before a manually entered credential can authorize work.
- A rejected credential is not saved as active. Connectivity, permission ambiguity, rate limits, and unsupported probes produce a verification-unavailable result, not a false invalid-key diagnosis.
- No model generation or paid validation prompt is sent. Unsupported non-generating verification remains explicit rather than being guessed from a prefix.
- Existing saved credentials are preserved. Missing verification proof is visible and blocks new managed authorization until an explicit recheck or reconnect succeeds.
- Quota is observational and must not change bindings, revisions, defaults, or selection. Reset buttons cannot imply that provider billing or subscription limits can be reset.
- Only requests tied to a current credential generation can publish a check result. Concurrent disable, delete, reconnect, cancellation, and restart must not revive stale credentials.
- Synthetic tests establish protocol and lifecycle behavior, not live-provider or native-platform acceptance.

## Review checkpoints

- Review transport destinations, response validation, cancellation, secret boundaries, and commit ordering after the credential regression turns green.
- Review observation freshness and account/generation causality after usage service and UI tests turn green.
- Independently review the frozen bounded delta before broader release completion. Existing platform containment and final production-runtime gates remain open.

## Current state

The five slices are implemented locally. Failed-first credential, usage, rendering and credential-replacement regressions are preserved. Midpoint review added exact-credential proof binding, atomic admission, private protocol version fencing and cache identity checks. Final bounded tests and real desktop rejection checks pass. See CREDENTIAL-USAGE-GATES.md and CREDENTIAL-USAGE-REVIEW.md for commands, artifacts and unresolved full-branch gates.

The starting tree accepted arbitrary manual keys without a provider check and discarded quota observations. Its archive is preserved for comparison. The expanded daemon-readiness fixture fails on both that archive and the corrected tree, so it remains a separate production-controls gate. Existing native switching and subscription paths are unchanged. No commit or publication was performed.
