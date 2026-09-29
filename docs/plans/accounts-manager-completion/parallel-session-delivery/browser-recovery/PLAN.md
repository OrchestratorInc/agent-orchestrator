# Browser handoff recovery

Scope: W4/W6 renderer sign-in recovery. Current behavior cancels a valid pending operation when the native browser opener rejects. The waiting panel has neither an authorization link nor an opener retry. This conflicts with the accepted manual-link fallback requirement.

1. Preserve the current desktop snapshot. Add failed-first tests for browser and device login with an opener failure before the inventory event arrives, recovered opening of the same operation, and explicit cancellation.
2. Keep the accepted start response in component memory only until a server observation replaces it. Do not copy credentials, change daemon contracts or invent terminal operation state. A browser handoff error must not cancel a valid operation.
3. Provide a read-only sign-in link, explicit native-opener retry and clipboard copy for pending operations. Reject missing or unsafe links. Show clipboard/opener errors without raw exception text. Reuse the operation rather than starting another one.
4. Preserve late-start cancellation, terminal observation, expiry, reconnect identity, cancellation acknowledgements and large-revision cache behavior. Clear the transient link on cancellation or authoritative terminal state. Keep expiration controls conservative without fabricating daemon completion.
5. Review midway, then run focused tests repeatedly, localization checks, typecheck, full frontend tests and build. Rebuild the isolated desktop snapshot and inspect real captures, distinguishing opener mechanics from actual provider authorization. Seal exact files and ask for independent review.

Evidence root: `/var/tmp/pr-5769-browser-recovery-79.AAbpSt`. No native switching, Subscriptions, runtime, vault or generated contract changes. Prior backend archives remain unchanged.
