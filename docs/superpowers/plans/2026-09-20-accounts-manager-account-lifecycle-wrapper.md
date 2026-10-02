# Accounts Manager Codex and Claude Account Lifecycle Wrapper

## Goal

Extend the private daemon-to-runner management channel with the complete Codex
and Claude credential lifecycle while keeping the vendored CLIProxyAPI engine
pristine. This phase has no public AO API, UI, routing mutation, session launch
injection, database migration, or legacy Subscriptions cleanup.

## Tasks

- [x] Harden the private management transport for bounded JSON requests across
  GET, POST, PUT, PATCH, and DELETE, with safe typed errors and serialized
  mutations.
- [x] Add runner-owned, management-authenticated OAuth start/status/cancel
  routes and loopback-only callback listeners for Codex and Claude.
- [x] Add OAuth lifecycle methods to the daemon's private ManagementClient.
- [x] Add Codex/Claude API-key creation and validated credential JSON import.
- [x] Add safe credential listing, enable/disable, refresh, and removal by
  unique auth_index.
- [x] Add safe model, cooldown, and quota projections.
- [x] Run only the focused runner and backend account-manager tests described
  in the approved plan, then perform a final diff/security review.

## Constraints

- `accounts-manager/engine` remains unmodified.
- Returned values and errors never expose secrets, raw upstream payloads,
  filenames, filesystem paths, internal endpoints, or ID-token claims.
- OAuth callback listeners bind only to `127.0.0.1`.
- Credential persistence remains engine-owned under AO's private state tree.
- No Docker, full repository suites, full CLIProxyAPI suite, frontend build,
  full lint, or production app build is run locally.

## Planned commits

1. `refactor: harden accounts manager management transport`
2. `feat: add accounts manager OAuth callback bridge`
3. `feat: add accounts manager OAuth lifecycle client`
4. `feat: add accounts manager credential creation`
5. `feat: manage accounts manager credentials`
6. `feat: expose accounts manager credential capabilities`
