# Linux managed launch: integration review boundary

This is the next M01 implementation slice. Existing namespace identity, permit and durable journal helpers remain the starting point. Their standalone tests do not enable production execution. Native launches and historical uncontained owners retain their current behaviour.

## Decision

Use an explicit managed-only PID-namespace launch, with the AO-owned namespace init blocked on its inherited one-use permit. Do not infer managed ownership from a session name or account-looking environment variable. The durable identity must exist before any provider code executes. Preserve the current fail-closed retirement of old uncontained launches.

The managed raw protocol added by the Codex foundation carries an ownership fingerprint through detached launch. It is an attachment contract, not a containment claim. A successor managed launch marker must also carry the reviewed containment requirement. The other managed Chat protocol needs the same explicit marker; existing native protocol forms remain unchanged.

## M01a: production identity and admission

1. Add a hidden trusted init entrypoint and a platform-specific launcher. Validate prerequisites before launch: supported pidfs/pidfd identity, namespace support, absolute executable/workspace/profile paths and the pinned local sandbox executable. Unsupported prerequisites return unavailable, never native fallback.
2. Persist the prepared namespace journal before starting the sandbox. Pass a read-only permit descriptor to init; init must verify PID 1 and wait for the exact identity plus EOF. No provider import, configuration hook or command runs before this point.
3. Capture the actual init's boot, start time, pidfs identity and namespace identity. Persist ready and then permitted through the existing compare-and-swap journal before releasing the permit. Init starts and reaps its provider; init exit closes the entire descendant boundary, including moved groups and nested descendants.
4. Make production exact shutdown dispatch from durable containment evidence. Retire only that immutable launch. Missing, inconsistent or corrupt evidence remains recovery-required. Do not accept descriptor replacement, lock absence or group absence as a retirement acknowledgement.
5. Handle every birth/publication cut: prepared with no helper, helper waiting for permit, ready before permission, permission committed before pipe delivery, provider running before descriptor publication, and lost retirement response. A durable cancelled/retiring state prevents later permit delivery.
6. Keep namespace receipts distinct from legacy process-group evidence. Absence of one record must never reinterpret the other as a stronger proof. Empty-identity recovery must enumerate all known owned receipts and reject unresolved ones.

First regression: real production host construction with provider-side execution marker. It must observe no marker before the durable permit and must preserve the same exact identity through host loss and cold reopen. Run the existing crash schedule with explicit managed fixture selection while retaining its assertions. Keep a separate legacy-uncontained negative control. Merely changing old positive expectations to accept recovery-required is prohibited.

## M01b: usable and restricted execution

- Mount only the selected workspace and session-private profile writable. Give the guest its own process filesystem and empty runtime/temp/home directories. Do not mount the account vault, whole AO data root, native credential home, host process filesystem or host control sockets. Required binaries, libraries and selected certificate files are read-only. Validate symlinks and workspace/profile relationships before constructing mounts.
- Use a private network namespace. A shared host network would expose unauthenticated daemon/control listeners and is not sufficient for this contract. Introduce an inherited-descriptor bridge restricted to the selected runner's model-data routes and the exact session capability. Management/control paths are not forwarding targets. No credentials are written to sandbox arguments or receipts.
- Tool network access and AO control-tool compatibility require separate reviewed allowlists/transports. Do not quietly expose a generic host execution proxy to make tools work. Unsupported operations stay explicit until their bounded transport is implemented.
- Preserve stdio, terminal sizing, private history, configured workspace roots and declared tool servers. Do not register/enable the managed driver until these requirements and switch/removal recovery are integrated.

The network/control compatibility boundary needs independent design review before implementation. This document does not assert that a production bridge already exists.

## Acceptance and review

- Failed-first production launch and permit controls, not only standalone namespace fixtures.
- Real late/moved descendants, host/init death, replacement generation publication/shutdown and unrelated native preservation.
- SQLite reopen plus fresh daemon/runner/vault recovery, cancellation cuts, revoked account admission and idempotent teardown.
- Exact path, descriptor, mount, network and control-socket negative tests. Verify no native-home or other managed-profile access through the supported interface.
- Real file/tool operations and model traffic within the contained boundary; no unmeasured readiness or responsiveness claim.
- Freeze M01a for independent ownership review before M01b product enablement, then freeze the complete recovery slice before destructive live checks. Native Windows and Mac acceptance remain separate external gates.
