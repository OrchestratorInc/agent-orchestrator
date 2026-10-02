# Deletion correction handoff for reviewer 82

Status: superseded by the independent late-fork HOLD and [D2 handoff](REVIEW-82-DELETION-D2.md). This draft records the v3 candidate, not the current source. The v3 full validation was stopped and is diagnostic only. No publication or public controls/UI work is authorized by this handoff.

## Corrected boundaries

The original Chat host's descriptor is a reusable slot, not evidence that its
provider descendants stopped. The correction persists a launch-specific,
non-secret owner record before publishing the host descriptor. On Linux this
record binds boot identity, process group, process session, and process birth
identities. A starting record is persisted before spawning the provider; an
incomplete launch remains recoverably blocked.

Exact shutdown may request graceful termination only from the matching host.
Regardless of acknowledgement, it must independently prove the original group
has no live members. Cold teardown requires a surviving recorded identity,
records discovered group members before signalling, and uses process descriptors
with fresh birth/group/session validation. A replacement descriptor, its later
removal, or the absence of the reusable lock cannot satisfy the original
obligation. Replacement and unrelated native groups are preserved.

The failed-first real-process regression kills the original host parent, proves
its provider and descendant survive, then launches a replacement. It exercises
both a live replacement and a replacement whose descriptor and lock have been
removed. The original two failures are preserved as `host-crash-red.log` in the
evidence directory below.

The runner had a second production ordering defect: its SDK starts serving
before a later registry refresh that derives API-key models from configuration.
The encrypted integration intentionally has no provider keys in configuration,
so that refresh removed managed model registrations. During the transition the
observed response was `auth_not_found`; afterward it was `model_not_found`.

The runner now owns a separate vault-backed request manager and model catalog.
The SDK service's empty, non-persistent manager constructs the provider
executors. Their public references are attached before serving; supported
requests use the runner-owned manager. Exact selection, vault admission,
encrypted storage and disabled legacy writers are unchanged. Refresh workers
have explicit startup and shutdown ownership. Missing or incomplete executor
sets reject startup without publishing a partial set. No engine or protected
native paths were changed.

The deterministic runner regression uses an observed registry event barrier,
not an arbitrary startup delay. It retains actual loopback requests, both
accounts, deletion, binding changes, three process starts, exact upstream request
counts, secret checks, and continued account-B authorization after deletion and
restart. `runner-model-barrier-red-v3.log` preserves the pre-correction failure.

## Evidence and source snapshot

Evidence directory: `/tmp/pr-5769-deletion-recheck-79.UWxSzb`.

The final source candidate has 12 correction files, 52 combined deletion-slice
files, and 1,808 integrated source files. The 91-path protected inventory matches
the original baseline. Final manifests, archive hashes, commands, package
durations and verification exit statuses will be recorded after all commands
finish. Results from commands started before the last Linux path-expression
lint correction are superseded, including otherwise passing checks.

## Limits and remaining gates

- Linux process proof is implemented and tested with synthetic workloads.
  Non-Linux exact managed-host deletion currently fails closed because the
  required crash-stable process proof is not implemented. This is a feature
  compatibility gap, not merely missing native test execution. Existing broad
  native shutdown is unchanged. No supported-platform completion is claimed.
- Starting, missing, corrupt, inaccessible, or unanchored owner evidence must
  remain blocked. Dedicated corruption, identity-reuse and every owner-record
  crash-cut schedule are not established by the reported two-mode regression.
- Existing SQLite reopen/cold-identity tests and actual terminal-runtime
  recovery are separate from the new real Chat-parent-crash fixture. A single
  real Chat parent-death plus SQLite coordinator-reopen integration is not
  claimed by this snapshot.
- The runner tests use local synthetic upstreams. Live sign-in, native
  supported-platform execution, real desktop coexistence, public routes/CLI,
  session controls/UI and measured responsiveness remain release gates.
- No visual surface changed in this correction; screenshots do not establish
  these backend ownership invariants. Product desktop evidence remains open.

## Independent review request

Pending final verification and exact manifests. Review must cover original
provider-group proof after parent death and replacement shutdown, conservative
identity handling, runner manager ownership and refresh lifecycle, absence of
account fallback, protected-path integrity, and the explicit evidence limits.
Keep review local and retain HOLD unless the exact bounded correction is clear.
