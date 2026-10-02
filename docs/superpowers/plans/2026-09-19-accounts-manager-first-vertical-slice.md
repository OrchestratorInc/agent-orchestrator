# Accounts Manager First Vertical Slice

## Goal

Prove the complete local path from the AO daemon through a supervised,
authenticated Accounts Manager runner to a fake OpenAI-compatible provider and
back as a streamed response. Real provider login, account UI, session routing,
and upstream trimming remain out of scope.

## Global constraints

- Keep `accounts-manager/engine` byte-for-byte upstream; AO integration belongs
  in a separate Go 1.26 runner module and AO packages.
- Bind only to `127.0.0.1`; expose no endpoint, port, PID, token, provider key,
  credential, or filesystem path through the public AO API or logs.
- Store all Accounts Manager state below `<AO StateDir>/accounts-manager` with
  private directory/file permissions and safe-file checks.
- Accounts Manager failure must degrade only Accounts Manager; AO startup,
  `/readyz`, unrelated agents, and existing features remain available.
- Run focused local tests only. Do not run Docker, the full imported engine
  suite, the full backend suite, or a full Electron production build locally.

## Task 1: Runner and private control plane

Add the Go 1.26 `ao-accounts-manager` runner, private state validation,
   loopback-only engine configuration, authenticated identity/lease routes, and
   graceful lease expiry.

## Task 2: Daemon supervision

Add the AO daemon supervisor with attach-or-spawn behavior, safe runtime
   records, port recovery, capped restart backoff, lease renewal, internal
   endpoint credentials, and non-blocking degraded status.

## Task 3: Safe status API

Add `GET /api/v1/accounts-manager/status`, safe DTO mapping, route tests,
   OpenAPI generation, and frontend type regeneration.

## Task 4: Desktop build and packaging

Build and package the runner for dev/release, inject its resolved path into
   the daemon, include license/provenance files, and verify packaged resources.

## Task 5: End-to-end streaming proof

Add focused integration coverage proving authenticated streaming through
   the real runner to a local fake provider, plus focused runner, supervisor,
   API-redaction, and packaging tests.

## Fixed lifecycle contract

- The daemon renews a runner lease every 5 seconds; a runner exits after 45
  seconds without renewal.
- Replacement daemons authenticate and reattach to an existing runner by its
  private runtime record and control token.
- Unexpected runner exits restart at 1s, 2s, 4s, then capped at 30s; a 60s
  stable run resets the retry ladder.
- Clean daemon shutdown stops lease renewal instead of immediately killing the
  runner, allowing a replacement daemon to attach during the lease window.
- Missing binaries, invalid state, bind failures, health timeouts, and crashes
  produce safe degraded reasons without failing daemon startup.

## Verification

- Focused Go tests for runner state/control and daemon supervision.
- Focused controller/spec tests and `npm run api`.
- Focused frontend resource tests and `npm run frontend:typecheck`.
- Real runner + fake provider streaming test with secret-redaction assertions.
