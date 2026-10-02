# First implementation review: drain and managed Codex foundation

Baseline: `03647048a9ff51d215af1c065ac95752ace39123`. Local implementation only. This is not a release-completion or capability-enablement request.

## Changes and self-review

- Passive terminal capture discards OSC strings with streaming state across arbitrary byte splits. Raw PTY output is untouched. SGR intensity parsing now skips indexed/RGB color payloads, preserving real drafts and dim placeholders.
- The managed driver uses explicit route configuration and one private profile per session. It strips ambient credential selectors, does not invoke native login/account APIs, and does not advertise device-account quota as managed quota.
- Explicit loopback proxy exclusions preserve both inherited exclusion lists while keeping route capabilities away from external proxies. The installed-process fixture supplies a hostile ambient proxy and asserts that it receives no managed authorization.
- Managed raw hosts use an explicit protocol tag so launch fingerprints survive the actual detached command boundary. Native empty-fingerprint compatibility remains intact. Reattachment retains the existing process, authorization and request sequence; foreign generations cannot attach.
- Fresh protocol failures stop their exact managed owner and keep their original error classification. Resume never silently starts a fresh conversation. Exact teardown errors remain errors.
- No production registry or managed Chat admission guard was enabled. Native switching and Subscriptions files remain unchanged.

The failed-first logs are `surface-title-red.log`, `composer-intensity-red.log`, `managed-profile-red.log`, `managed-host-owner-red.log`, `managed-host-publication-red.log`, `managed-start-classification-red.log` and `managed-loopback-proxy-red.log` in `/var/tmp/pr-5769-implementation-79.5HnQwE`.

## What the tests establish

Real direct PTY and fallback processes exercise the production supervisor and account-switch coordinator. Active work retains A; idle with an empty composer commits B; a real unsent draft retains A and its process. No drain interrupt/input occurs. Four cases passed three times under race detection.

The installed Codex app-server test starts two actual processes in a network-isolated fixture. Distinct synthetic route tokens overlap at the HTTP endpoint. A's private history survives process restart. Revoking A's synthetic endpoint authorization produces a failed turn without native fallback; B continues. Native profile files remain byte-identical. The fixture injects process lifetime and therefore does not prove persistent-host containment, full daemon restart, vault revocation or UI integration.

Unit/adapter controls cover route validation, missing explicit authorization/generation, profile separation, symlink refusal, fingerprint stability across token renewal, fingerprint changes across generation/endpoint/scope, failed resume, no reinitialization during live adoption, detached host fingerprint publication and foreign-owner refusal.

Exact commands/results and the immutable source/archive hashes are recorded in the evidence directory's `seal-report.md`. The 18-file source manifest is `first-slice-v1-source.sha256`, SHA256 `5dbddfdfc98626cfc4874d8f5772a9a0e76e9b7ceb46c60df0bc65a8e5545b30`. The source archive is `first-slice-v1-source.tar.gz`, SHA256 `2de498e276f1ae3c1da1eda173e229ecf43573107d8066fcc11f2cb6dd77a0a2`. RESUME-GATES.md tracks the bounded acceptance results.

## Remaining dependencies

1. M01/M02 remain blocked by uncontained exact retirement. The full host race suite still fails five cases in three known groups with containment-required evidence. A missing process group cannot be used to clear them. New managed launch containment and recovery must be implemented and independently reviewed.
2. Production managed Chat still needs registry/service selection, controller generation propagation, durable host recording for the selected provider, authorization leases and complete switch/removal/restart integration. Additional directories and configured tool servers currently return unsupported rather than being ignored.
3. Ordinary thread protocol operations are inherited from the existing adapter. This slice exercises start, streaming, resume/history and authorization rejection; it does not newly prove every advertised extension with managed accounts.
4. Native Windows and both Mac architectures require actual execution. Cross-compilation is not kernel or desktop evidence. No personal/live credentials were used in this slice.
5. Existing failed-operation repair, full cold runner/vault settings and queues, explicit legacy credential migration/reconnect, live usage, desktop performance, base reconciliation and final integrated review remain open.

## Independent review request

Review the sealed source for false-empty composer observations, ambient credential fallback, cross-generation host adoption, fingerprint loss, history fallback, inaccurate capability claims and unsafe teardown acknowledgements. Keep production enablement and deletion on HOLD until their independent gates are satisfied. Do not edit this worktree or publish during review.
