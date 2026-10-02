# W2 Linux durable start boundary

Base: `ec7efde0652f21690d8e98e2dcfa71d5b132e422`. The W1 streaming seal and nine-file retirement correction remain unchanged. This is the start/identity part of W2, not production adoption of a contained workspace. Evidence root: `/var/tmp/pr-5769-w2-start-79.9S9kdc`.

## Decision and scope

Use a trusted namespace init with a positive, exact, single-use start message. Pipe closure is denial. The namespace tool's `--block-fd` ignores the read result, so EOF must not be the provider execution gate. Its namespace setup and status reporting can still precede the trusted helper. [Pinned implementation](https://raw.githubusercontent.com/containers/bubblewrap/v0.11.0/bubblewrap.c).

Keep the new launch journal separate from legacy group evidence. Never upgrade an existing uncontained process into a contained receipt. The identity includes the launch, session, boot, namespace-init host PID, start time, PID-namespace inode and stable kernel process identifier. A ready record is published only while exact init identity is live. Before grant, the writer must match init's dedicated read-only pipe descriptor. The start transition is synced before sending the permit. Cancellation/retirement is a durable one-way fence. Restart never resends a possibly consumed permit.

The local reuse experiment showed an old and replacement namespace receiving the same inode. Cold proof therefore requires a 64-bit `pidfs` process identifier, validated by filesystem type, not a namespace number or start tick alone. Unsupported kernels fail closed in this new boundary; no native path is changed. This prerequisite still needs packaging/capability integration before adoption. [Kernel identity contract](https://raw.githubusercontent.com/torvalds/linux/v6.17/fs/pidfs.c).

Namespace init death closes descendant execution, including children that change process group/session. Exact signaling uses a process descriptor after revalidation, never a reusable PID alone. A persisted record does not by itself certify a closed-access workspace; the adapter remains unwired until containment, workspace, network and packaging gates pass. [Kernel lifetime contract](https://man7.org/linux/man-pages/man7/pid_namespaces.7.html).

## Implementation sequence

1. Preserve a failed-first EOF assertion against the raw launcher candidate using a synthetic command. Add deterministic durable-state and positive-permit tests. Missing behavior must fail at an assertion, not merely fail compilation.
2. Implement strict bounded journal decoding, sync/rename persistence, cross-process admission, exact revision checks and one-way retirement. Cover partial/lost permit writes, stale retry after cancellation, corrupt evidence and reopen.
3. Implement and test Linux namespace-init identity capture and exact teardown. Reproduce host loss, moved/late descendants, a concurrent replacement and an unrelated native control with ordinary Go helpers. No personal credentials or host-service changes.
4. Self-review after initial green. Run focused races three times, build/vet/lint and preservation checks. Freeze this leaf for independent review while continuing disjoint authorized work.

## Acceptance

- [x] W2S1: raw blocking-pipe EOF has a preserved failing assertion; corrected helper never executes after empty, truncated, wrong-identity or trailing permit input.
  EVIDENCE: `block-pipe-red.log` contains the real EOF execution failure. The exact-message and cancellation matrix passes three races in `final2-default.log`; full namespace process controls pass in `final2-focused.log`.
- [x] W2S2: durable ready/start/fence transitions survive reopen; winning cancellation blocks every later permit; lost responses and partial messages cannot authorize a second attempt.
  EVIDENCE: six host-crash cuts, before/after-rename fault injection, cold reopen, stale identity reuse and a waiting-grant cancellation interleaving pass three races. No second permit is issued after uncertain publication.
- [x] W2S3: incomplete, duplicate, oversized, symlinked and inaccessible/nonregular evidence fails closed. State transitions require exact expected revision and launch identity.
  EVIDENCE: strict evidence, exact 4096-byte/one-byte-over boundaries, missing files, symlink/FIFO/directory rejection, lock/context and stale-revision tests pass in `final2-default.log`. The directory fixture is not proof of every OS permission failure.
- [x] W2S4: exact namespace-init teardown retires moved/late descendants after host loss and preserves replacement/native controls. Unknown evidence never signals.
  EVIDENCE: `final2-focused.log` has 60 actual-process scenario executions across three repetitions, plus 105 unit leaf executions and three inactive helper entries. No skips. PID/group/session changes, keeper death, host death, kernel identity mismatch and modeled reused coordinates preserve the unrelated controls.
- [x] W2S5: focused races, build/vet/lint and all preservation manifests pass on one final snapshot; midpoint findings are addressed and exact source/logs are sealed.
  EVIDENCE: `W2-LINUX-START-REVIEW.md` and `final/source.sha256` record the six-file snapshot and final commands. Full package race still has exactly the three pre-existing recovery groups failing; this gate does not claim full package or product completion.
- [ ] W2S6: independent review accepts the bounded protocol and identity proof. Production launch integration, legacy recovery, usable workspace/networking and platform/live-provider gates remain separate.
  EVIDENCE: requested through the orchestrator; no verdict observed.

## Midpoint findings

The first process command used an unavailable launcher flag. Its log and the initial wrong-pipe run are diagnostic only. The native-control fixture also needed a cleanup-owned context because test context cancellation precedes cleanup. Both were corrected before the valid wrong-pipe reproduction.

`pipe-owner-red-valid.log` proves a permit could reach init A while the journal named B. The corrected grant compares the host writer with the recorded init's read-only descriptor and rechecks the pinned process before mutation. `kernel-identity-red.log` proves the first record lacked the stable kernel identity required for durable reuse discrimination.

`namespace-reuse-diagnosis.log` records an old init and replacement with different PIDs/start times but the same namespace inode. The initial test oracle misclassified the replacement as a survivor. It now tracks the known original fixture process identities, and the production identity additionally carries the boot-scoped `pidfs` identifier. The negative control models every reusable coordinate matching while kernel identity differs and requires replacement survival.

The existing production launch remains unchanged. A complete workspace/network broker, packaged helper, capability checks, deletion-journal integration, SQLite coordinator reopen, legacy uncontained recovery and independent approval are still required. No native platform or live-account success is inferred from this leaf.
