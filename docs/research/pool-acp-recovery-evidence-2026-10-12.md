# Pool native ACP recovery

Continues [AO PR6551](https://github.com/OrchestratorInc/agent-orchestrator/pull/6551), without a duplicate PR. The earlier [Z.ai hold](pool-acp-hold-2026-10-11.md) and all older evidence remain unchanged. The released [Pool1.0.16](https://github.com/poolsideai/pool/releases/tag/v1.0.16) Linux amd64 binary SHA256 is `db70340b41c8bd76dbe2b5926a6e46cda804789b9774b3d4cfba3591f0de02f2`. No native code/binary patch or security bypass was used.

The documented standalone root `https://api.deepseek.com` enabled bounded real native provider/tool turns using the already authorized private credential. Inventory auth remains **configured**, not remotely verified merely from credential presence. The actual catalog selected `deepseek-flash`; unavailable `deepseek-chat` was ignored at startup and explicitly rejected by native model selection. AO uses native config options rather than inventing a model catalog.

## Admitted scope and implementation

Unix **Chat only**, through AO nativeacp and the persistent detached host. Registry metadata, project/task defaults and labels, daemon/API enums, generated types, migration208 and its burned-version ledger entry are wired in the existing PR. Historical schema repair executes before Pool reconciliation; older repair recognizers remain unchanged. Explicit TUI, Auto permission mode, reviewer and handoff are not admitted. Optional unqualified images, embedded context, resource links, elicitation, nested agents and terminal output are scoped off.

Native tools/default instructions/user hooks remain intact. Private unnamed SessionStart settings are additive; they never replace `prompts.system_message` or project AGENTS.md. The required matcher is included and serialized UTF-8 context is bounded below32KiB. Native hooks fail open. AO checks the session/workspace witness before publishing successful prompt completion; **this observes context initialization after the turn and is not a tool-execution security gate**. New sessions and loads after an interrupted Kill can initialize hooks lazily on the first prompt. Live host adoption keeps its existing context. Manual/accept-edits/allow-all map to native default/accept-edits/always-allow; native mode negotiation passed for all three, while detailed permission approve/reject/cancel evidence uses manual.

## Live native and AO evidence

[Curated proof and process/screenshot receipts](assets/pool-recovery-20261012/proof.json) include native identity/version/hash, actual model, file contents, provider-ID digest, generations, host/native PID and kernel-start markers, executing parent/child markers and final cleanup. Credentials, DBs, native profiles and complete raw logs are omitted.

Native proof passed leading-dash initial task once, correct-workspace edit, independent private and project instructions, second message, exact terminated native load/replay with freshly changed private instructions, unknown-history failure, cross-workspace load refusal, approve/reject and executing cancellation without a delayed file.

Actual AO attempt003 passed initial and second active-to-settled edits, preserved AGENTS.md and an existing user hook, permission reject and pending cancellation, executing parent plus nested sleep-child interruption and no late artifact, another edit after cancel, and independent Kill while preserving an unrelated control session. Exact history restored with a new generation and a fresh private token introduced only after confirmed Kill; the initial leading-dash message still appears once.

Final source **`fb60bd4097ebd4109bbe7ee68e908f722e6ee03f`** and UI commit match. Electron replaced the daemon; serving PID959053, kernel start79898518, executable SHA256 `b30f2dfa7b1f1c796c5772cb96d7d5c32b4bfb53c39f2c4ff7ca669f2cca8ded`, clean embedded VCS identity. The same native session digest and host/native process survived; a new real edit through the reconnected daemon completed and the unrelated control remained intact. The final controller generation was `18fc958c-5ee6-4220-9718-c877b3f5eca1`. Earlier full lifecycle proof used source `a7eadca3d2f4ccbd7dd376933fc8a57793430209`; subsequent changes fixed lint/test admission/install metadata only.

### Actual Electron active and settled captures

Captured from the actual Electron renderer with `Page.captureScreenshot`, visually inspected for secrets. AO navigation, live Pool tool activity, model and manual approval mode are visible. Audit receipt tokens are deliberate test data. Capture UTC2026-10-11T21:15:25.790Z and21:15:33.141Z respectively; recovery run paths use20261012. PNG hashes are in the proof receipt.

![Actual AO Pool actively editing after detached-host reconnect](assets/pool-recovery-20261012/ui-final-active.png)

![Actual AO Pool settled with restored instructions and reconnected edit](assets/pool-recovery-20261012/ui-final-settled.png)

[Earlier inspected screenshot](assets/pool-recovery-20261012/ui-earlier-backend.png) is retained explicitly as UI281bbf588 with backend a7eadca3d, before the final successful rebuild. It is not final-head proof.

## Failures and cleanup retained

The Z.ai404, missing SessionStart matcher, unavailable requested model, eager restored-hook witness failure, post-rejection model refusal of a similar operation, receipt tuple/list mismatch and restart-generation assertion remain separate receipts. Distinct authorized operations and corrected observation timing subsequently passed. A resource guard stopped an earlier build below750MiB; verified regenerable owned caches and a byte-identical duplicate native archive were reclaimed. Installed native artifacts/profiles/source were preserved; the final serial build succeeded within the floor. No routine local suite was run.

Both exact owned AO sessions were Killed, their hosts/native/MCP children disappeared, Electron exited and the daemon acknowledged shutdown202. The API Kill response has no numeric freed count; no count is invented. The auto-created auth PTY was not in the shell-terminal API (DELETE404), so its exact verified owned process group was terminated without any sign-in input. Later `/proc` ownership checks show all named owned processes gone. Older audit daemon625660 remained alive; no production service was touched.

Routine tests, lint, typechecking and platform workflows run only in each PR’s GitHub Actions. Source current-head CI status is recorded in PR6551, and the evidence/skill current-head checks in PR6509. Historical green fixture counts do not substitute for this evidence head.
