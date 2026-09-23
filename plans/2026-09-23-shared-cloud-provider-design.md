# Shared Cloud sandbox-provider preference

## Intent and scope

The desktop Cloud sandbox-provider selection should determine where new sessions run when the same person starts them on mobile. Mobile should not need a second provider picker or a live desktop connection. The preference is per authenticated user across that user's organizations, matching the existing global desktop setting. This change covers new orchestrator and top-level worker sessions; it does not move or repair existing sessions. Agent/harness selection and provider credentials remain separate.

Do not add or commit files under `docs/` for this work.

## Current behavior

Desktop stores the selected provider only in `localStorage` (`ao.cloud.sandboxProvider`) and includes it in orchestrator and task create requests. Mobile omits `provider`, so the control plane uses its deployment default. The control plane already accepts an optional `provider`, but the shared Cloud OpenAPI contract does not describe that field. Session records stamp the resolved provider at creation, so opening one on another device cannot change it.

## Decision

Use an account-scoped preference stored by the Cloud control plane, not a desktop-to-phone connection and not an inference from the most recent session. A direct desktop connection would fail when the desktop is offline; inferring from sessions is ambiguous for a new project or a user in multiple organizations. The control plane is the source of truth for future starts.

## Contract and storage

- Add an authenticated `GET /api/cloud/v1/me/preferences` returning `{ sandboxProvider: string | null }` and an authenticated `PUT` on the same path accepting `{ sandboxProvider: string | null, initializeOnly?: boolean }`. `null` means use the deployment default. A normal `PUT` replaces the user's choice. `initializeOnly: true` atomically writes only if the user has no preference row; otherwise it returns a `preference_conflict` error. This is for migrating legacy desktop settings without overwriting a choice already made elsewhere.
- Validate non-null values against `availableSandboxProviders` on write. An unavailable value returns the existing `provider_unavailable` error envelope; no preference is changed.
- Store the preference keyed by authenticated user ID in a new PostgreSQL migration and owner-scoped row-level-security policy. Use the existing `withUser` transaction pattern. Do not modify old migrations.
- Add both operations and types to the Cloud OpenAPI contract and regenerate the shared Cloud client. Also document the existing optional session-create `provider` field in that contract so desktop's explicit override is represented accurately.
- Keep explicit `provider` in a create-session request as the highest-precedence override for older clients and other callers. For a top-level session with no explicit provider, resolve the authenticated user's preference; if unset, use the deployment default. A child worker linked to an orchestrator still inherits its parent's provider before validation, regardless of client or preference.
- If a saved preference later becomes unavailable on a deployment, fail new creation clearly with `provider_unavailable` rather than silently start on a different provider. Existing sessions continue using their stamped provider.

## Desktop and mobile behavior

- On the first authenticated desktop load after upgrade, read the Cloud preference. If it is unset and the legacy localStorage value is non-empty and still available, attempt an `initializeOnly` write. If another device has already configured the preference, reload and use the Cloud value. A stored `null` is distinct from no row, so an intentional reset to the deployment default cannot be overwritten by a late migration. Clear the legacy local value once migration succeeds or the Cloud preference is confirmed; after that, localStorage is not the source of truth.
- The desktop provider selector reads the Cloud preference and saves changes through the authenticated `PUT`. It presents pending/error feedback and does not show a failed save as applied.
- Updated desktop orchestrator and task creation omit their local provider override, allowing the control plane to resolve the shared preference. An explicit override remains supported by the API for older clients.
- Mobile continues to omit `provider` for orchestrators and top-level workers; the server applies the shared preference. No mobile picker or desktop pairing is introduced.
- If the preference API is temporarily unreachable, desktop should not silently overwrite the Cloud choice with a stale local value. The selection UI shows an error; creation can proceed only through the server's current preference resolution when the create API is reachable.

## Rollout and verification

Deploy the control-plane migration and API before shipping clients that depend on it. Older desktop clients continue to send explicit providers; older mobile clients automatically benefit from server-side preference resolution. New clients continue to work with the control-plane default when the preference is unset.

Tests should cover authenticated ownership, invalid/unavailable values, unset/default behavior, atomic migration conflicts, explicit override precedence, linked-worker inheritance, a saved provider becoming unavailable, one-time desktop migration, save failure feedback, desktop session-create requests without local overrides, and mobile orchestrator/worker requests inheriting the server preference. Verify existing session provider fields remain unchanged.
