# Pending-removal process recovery proof

Preserve the 13-file removal-drain seal at `/var/tmp/pr-5769-w4-drain-79.S6e2Ns/final/FREEZE.md`. Own only one new runner test and this leaf's documentation. Do not alter production or earlier tests during read-only review.

The remaining D3 question is whether the encrypted removal fence survives actual runner death and fresh production Serve construction, rather than only an in-process store reopen. Use ordinary Go subprocess tests and synthetic credentials. Every provider request is intercepted by a test transport or executor; no real account or external request is authorized.

1. Seed two encrypted accounts, start Serve in a separate test process, and block A's refresh, usage or manual recheck cleanup. Observe the request entering the actual worker.
2. Begin removal through the real loopback HTTP route. Observe cancellation after its durable fence, require no deletion acknowledgement, keep A resolvable/disabled, and prove B's usage succeeds while A is held.
3. Kill and join only that owned subprocess. Start a fresh Serve on the same state. Require A to remain disabled and denied without provider calls. B remains available. Retry deletion by the original reference, require explicit daemon completion and idempotent repetition.
4. Restart once more after final erasure. A remains missing, B remains usable and no fallback occurs. Inspect the encrypted tombstone and redaction controls. Keep a negative control that demonstrates the recovery oracle detects a missing fence.

Review the harness after its first green result. Run the focused process table under race three times, full runner race, build/vet/lint, supported cross-compilation and all prior preservation checks. Freeze this test-only proof separately and request independent review. Native containment, actual provider credentials and whole-product release remain separate unmet gates.
