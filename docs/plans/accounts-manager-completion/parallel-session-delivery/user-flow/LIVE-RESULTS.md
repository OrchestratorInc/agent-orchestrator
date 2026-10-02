# Live account verification, 2026-09-29

Verdict: partial success, not release-ready. The user supplied three OAuth accounts in the isolated native desktop. Tests used short prompts and four disposable sessions. No stored secret was requested or copied; no account was removed. Product source and prior review archives were left unchanged.

Evidence root: `/var/tmp/pr-5769-user-flow-79.nBnXAa`. The checked-out HEAD is `ec7efde0652f21690d8e98e2dcfa71d5b132e422`; the existing uncommitted integrated source is separately covered by its preserved manifest. This is not a clean-HEAD test or a fresh full-suite run.

## Passed with actual accounts

| Boundary | Observed result |
| --- | --- |
| Inventory and quota | Three verified accounts. All three public quota requests returned 200 with real windows. Initial five-hour remaining values were A 78%, B 100%, C 0%. |
| Parallel terminal execution | A and B answered their exact test markers. Their prompt-to-response intervals overlapped by 1695 ms, with distinct explicit durable bindings. Both required the supported-model workaround below. |
| Native desktop terminal handoff | A-to-B reached `ready`, revision 9, in approximately 12 seconds. A later reply recalled the original conversation marker. The return switch reached revision 17. |
| Pre-stop cancellation | The daemon returned 202 with `cancelled`. Binding A and revision 17 were unchanged. Retry returned 409 and did not restart the cancelled operation. |
| Local disable isolation | Temporarily disabling A blocked its request while B completed a simultaneous control request. A's binding did not fall back. A was re-enabled in a cleanup block. This is not provider-side credential revocation. |
| Managed Chat | The selected B account completed a real Chat request. |
| Chat queue and cancellation | A second message was observed `queued` behind an active turn. After cancelling a pending drain switch, that message completed with the original B binding and revision 27. |
| Native desktop Chat handoff | B-to-A reached `ready`, revision 35, in approximately one second. The next real reply repeated `CHAT_QUEUE_B_OK` from before the switch and answered `SWITCH_CHAT_A_OK`. |
| Daemon restart, bounded | Daemon PID changed from 185 to 11887. All three account IDs and existing binding revisions survived. B answered another prompt afterward. The existing runner reattached; this was not a runner or vault cold restart. |
| Public control boundaries | Stale revision returned 409; wrong-session operation lookup returned 404. Error request IDs survived. Checked account-control DTOs did not expose runtime handles, secrets or private endpoints. |
| Removal impact API | The actual daemon enumerated affected bindings and the required revision. No deletion was submitted. |
| Removal confirmation UI | Keyboard activation opened the real preview. It listed A's two current bindings at impact revision 38. Confirmation was unchecked and the submission button disabled. Closing made zero account writes. Pointer access was clipped at the current window width, as recorded below. |

Parallel response intervals plus the disable control establish live routing behavior with distinct saved bindings. They do not independently capture the upstream provider's account identity on each request. Terminal prompt echoes alone were not used as a success oracle: exact assistant replies were read from these disposable sessions' transcripts.

## Reproduced defects

1. **Managed model selection uses the wrong catalog.** A model returned by the selected account's model endpoint failed explicit session creation with HTTP 400, `MODEL_CAPABILITIES_UNAVAILABLE`, request ID `Arch/x749CD9Gi9-000716`. The native harness catalog reported stale discovery with no resolved native credential. `manager.go` still calls the native model catalog during `resolveAgentConfig`.
2. **Default terminal model is absent from the embedded catalog.** Initial requests from installed client version 2.1.283 returned 400 with an unknown-model routing error. A and B worked only after selecting an existing small model in their terminals. Successful later tests do not prove that default first-run behavior works.
3. **Exhaustion becomes a misleading authorization failure and hides usage.** C started with zero five-hour capacity. Model validation returned 429, then generation retried with 401. The account remained verified but became `error` and `unavailable`. Its public quota endpoint still returned 200 with zero remaining, while the UI said usage was unavailable. `AccountUsage.tsx` suppresses quota fetching and display whenever `account.unavailable` is true. C did not silently use another account. Retries were interrupted.
4. **A pending switch does not recover across graceful daemon restart.** Operation `3f8bff25-c8bc-4db8-82e5-098dc76af6b0` was `waiting` before restart and `failed` afterward. Its original A binding and revision 17 survived, but retry returned 409 with a request ID. The pre-stop worker finish path maps shutdown interruption to terminal failure. This must not be counted as successful pending-operation recovery.
5. **Settings actions are clipped at the current desktop width.** At a 699 by 1037 renderer size, the first remove action occupied x=725.5 through 757.5, outside the viewport. Ordinary pointer activation timed out. Keyboard activation verified the confirmation boundary, but does not establish pointer usability. The shared settings layout also extends offscreen; whether this is inherited or introduced is not yet classified. No force-click, layout patch or reconstructed page was used.

During a terminal handoff, new public message submissions returned 409 `AGENT_SWITCH_IN_PROGRESS`. They were explicitly rejected, not acknowledged and lost. Existing Chat queue preservation passed; accepting new terminal messages during handoff remains a separate UX/contract decision.

## Evidence and fixture review

- `live-events.jsonl` records chronological assertions and observations using account aliases. It is local evidence, not an uploaded artifact.
- `live-switch-observed.png`, `live-chat-switch-ready.png` and `live-chat-switch-response.png` show the real native desktop. The Chat ready screenshot shows committed revision 35 and actual usage bars. The response screenshot shows the remembered pre-switch marker. These images were inspected.
- `live-desktop.mp4` is a seven-second recording assembled from 28 actual Electron renderer captures at four frames per second, with no reconstructed UI. `live-recording-info.json` records its codec and dimensions. A decoded frame was inspected in addition to the source screenshots.
- `live-usage-menu.png` captures positive A/B quota labels and the incorrect unavailable state for C. The native status-screen diagnostic can contain a private proxy address and is excluded from the shareable evidence selection.
- External harness mistakes were kept separate from product failures: cancel succeeds with 202, not 200; the daemon status DTO intentionally omits PID; this app uses hash routing; a session header is a tab rather than an ordinary button. The original logs remain preserved. The PID oracle was corrected to the actual run file and daemon listener logs.
- `live-final-boundaries-keyboard.log` exited zero after reproducing the failed-operation retry conflict, checking C's successful quota response, and verifying removal preview without mutation. Its passing exit describes those bounded assertions, not recovery of the failed switch. Earlier pointer/shortcut attempts remain in the other `live-final-boundaries*.log` files. `live-removal-diagnostic.png` contains personal account labels and stays local.
- Self-review narrowed the restart claim to daemon reattachment, the revocation claim to reversible local disable, and the concurrency claim to real replies plus durable bindings. No destructive test is implied by an impact preview.

## Not established

- Safe permanent account deletion, deletion crash cuts and original-provider containment remain blocked by the retained retirement failures. Testing did not delete a real account to bypass those gates.
- Full runner/vault cold restart, recovery of an in-flight billed request, queue adoption across process death and permanent credential revocation were not tested with these accounts.
- These three accounts are OAuth accounts. This run does not verify setup-token migration, API-key validation, or the other provider's managed Chat and native profile isolation.
- Native Windows execution and native Mac arm64/x64 containment remain unavailable in this Linux environment. Compile checks do not substitute for them.
- Existing full-suite failures, independent review, main integration and final publication gates remain open. No commit, push or PR modification occurred in this test pass.

## Preservation and handoff

Final SHA256 verification passed for all 85 integrated source entries, all 91 protected paths, all 33 generated artifacts and all five guest-design files. `git diff --check` also passed. Exact output is in `live-preservation.log`. This pass changes only test helpers outside the repository and this user-flow evidence documentation.

The bounded live ledger has 2 met, 3 unmet and 0 abandoned gates. The six parent delivery gates retain 2 met and 4 unmet. They intentionally remain open despite the successful individual interactions.

The selected local evidence is sealed under `/var/tmp/pr-5769-live-79.23xRMX`, with its exact file list and hashes in `manifest.sha256`. The active daemon log and personal-label/private-endpoint diagnostics are excluded. The native app remains running with all three stored accounts enabled; C is still capacity-limited. Disposable sessions are left available for inspection.

Follow-up order: repair managed model discovery/default compatibility, quota/error reporting and pending-switch restart behavior; classify and correct the narrow-window control clipping; then repeat the affected live paths. Permanent deletion still requires its separate reviewed containment correction before destructive testing.
