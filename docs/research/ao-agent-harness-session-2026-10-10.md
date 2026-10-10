# AO agent harness integration and audit: 10 October 2026

The [AO agent harness skill](../../.agents/skills/ao-agent-harness/SKILL.md) combines a production-integration guide with an executable audit of registered terminal agents. This session used that workflow to review 52 candidate agents, prepare three draft integrations, and test their native lifecycle through AO on an isolated Ubuntu VPS. Tau, Neovate Code, and Open Interpreter each recorded 17/17 functional lifecycle gate passes at the revisions below. Each revision also passed all 24 GitHub checks. Review while packaging this skill found that one historical gate overstated its evidence: the restore-only instruction token had already been included before the first turn, so `system_prompt_restore` did not prove refreshed or reinjected instructions.

**The historical 17/17 labels are preserved, not endorsed as proof of all 17 intended behaviors.** Refreshed instruction delivery is unproven in all three historical runs. The packaged runner now introduces the fresh token only after kill and before restore; fixture coverage of that correction is separate from live-provider validation, which has not been rerun.

**None of the three received a full strict audit PASS.** AO still reports their credentials as configured rather than verified; exact native model comparisons were unavailable, and Open Interpreter's AO model catalog was empty. A later live screenshot reproduction also confirmed that an Open Interpreter message sent during native initialization can receive HTTP 200 yet remain an unsubmitted draft for at least 60 seconds. The successful lifecycle runs waited for stronger native readiness evidence and do not resolve that early-send behavior.

This is a historical report of the integration PR revisions, audit attempts, and follow-up reproduction. It is not a test result for `main`, the new skill PR, other agent versions, or a combined branch containing all three integrations. The integration PRs were draft and unmerged when their results were recorded.

## What the skill provides

The skill is contributor tooling under `.agents/skills/ao-agent-harness/`. It is invoked through Node and does not add an AO runtime command or ship a desktop feature by itself.

| Capability | What it does | Boundary |
| --- | --- | --- |
| Integration assessment | Maps released CLI installation, instructions, permissions, model selection, activity, native identity, cancellation, and exact restore; checks existing support and PRs before adding another adapter. | A source or documentation review does not establish executable conformance. Deferred means evidence is incomplete unless a concrete incompatibility is named. |
| Production integration guide | Routes adapter, registry, installer/auth, new migration, API generation, product identity, documentation, and conformance work through the current Go architecture. | Manual engineering workflow; there is no automatic adapter generator in this PR. Terminal support does not imply structured Chat or interface handoff support. |
| Isolated API audit | Builds the selected checkout or uses an explicitly selected binary, starts its own daemon and scratch projects, waits for readiness, discovers registered harnesses, and compares direct native evidence with AO evidence. | Existing installed-daemon health cannot establish that the selected source revision works. A runtime CLI contract supplies supported native commands. |
| Lifecycle proof | Exercises actual workspace mutation, initial and second messages, instructions, activity, cancellation, termination, exact native restore, history, and refreshed standing instructions. | A successful HTTP response, process spawn, or synthetic idle status is insufficient proof. |
| Strict and diagnostic reporting | Writes JSON and Markdown with `PASS`, `FAIL`, `BLOCKED`, and `NOT_RUN`, retaining independent results and the first non-pass gate. The strict default remains; `--diagnostic-after-native-proof` explicitly permits downstream diagnosis after independent native proof. | Diagnostic mode never upgrades blocked auth or missing model evidence to PASS. Unsupported control/readiness contracts remain explicit gaps. |
| Failure evidence | Creates attempt-specific issue artifacts and retains bounded command, daemon, API, terminal, and cleanup evidence. An optional capture command can capture a real audit surface. | Screenshot availability depends on the host. Rendered archived logs are not screenshots of the original failure. |
| Credential handling | Uses the authorized existing account, distinguishes configured from verified auth, and records a user-action handoff for actual login problems. | Does not perform login, switch accounts, infer credential validity from file presence, or turn user confirmation into a passing gate. |

See the [integration path](../../.agents/skills/ao-agent-harness/references/integration-path.md), [local CLI contract](../../.agents/skills/ao-agent-harness/references/local-cli-contract.md), and [evidence contract](../../.agents/skills/ao-agent-harness/references/evidence-and-handoff.md) for operational details. Scheduled release discovery, candidate isolation, adapter generation, automatic PR management, and Discord notification were discussed as a separate autopilot design. They are not implemented or included as executable capabilities in this PR.

## Scope, authorization, and execution

The session was authorized to inspect candidates, finish suitable existing integrations, create separate PRs, use the provided Ubuntu 24.04 VPS, install the required tooling there, and test with existing credentials. The user explicitly prohibited local checks. Source inspection/edits, Git/GitHub operations, and sanitized artifact transfers used the development Mac; tests, builds, formatting, native CLI execution, and provider calls ran on the VPS or GitHub CI. No checks or provider turns ran on the Mac.

All three native CLIs used the existing Z.ai coding-plan credential with `glm-5.3-flash`. A separate preflight received HTTP 200 and the requested proof text; each CLI also completed a real provider turn that wrote its scratch workspace. Existing Codex login and the supplied DeepSeek key were not used. No login or account switch occurred. Credentials, native profiles, raw terminal captures, native session IDs, and host connection details are excluded from the committed evidence.

Tau used its custom-provider/base-URL/model/environment-key options. Neovate used an isolated custom-provider config. Open Interpreter used an isolated native profile whose selected custom Chat Completions provider referenced an environment key. These tests cover **Open Interpreter Rust 0.0.56**, not the older Python distribution. Its first direct write probe hit the VPS's bubblewrap networking restriction; the later disposable audit used the native explicit bypass option. Tau's integration also supports explicit bypass only. Successful bypass runs do not establish other permission modes.

Native profiles, daemon state, tools, and workspaces were scoped under a dedicated audit directory inside `~/.ao`. The user's regular AO sessions and unrelated dirty checkout were left alone. Real-provider VPS tests used AO's loopback HTTP API and actual terminal WebSocket mux. CI's released-CLI conformance used local fixture providers and required no real provider credentials.

## Three integration PRs and their final recorded results

| Agent and native version | PR | Audited head | Recorded lifecycle gates | Recorded audit gate totals | GitHub CI |
| --- | --- | --- | --- | --- | --- |
| Tau 0.4.7 | [#6493](https://github.com/OrchestratorInc/agent-orchestrator/pull/6493) | `189ca905dee3264ba5c682590ae04bcd8da88adf` | 17/17 PASS, diagnostic attempt 6 | 25 PASS, 1 BLOCKED, 2 NOT_RUN | 24/24 PASS |
| Neovate Code 0.28.5 | [#6494](https://github.com/OrchestratorInc/agent-orchestrator/pull/6494) | `e82a48fa09c07e7d70cdd19a26aea0ae1bed7e1f` | 17/17 PASS, diagnostic attempt 3 | 25 PASS, 1 BLOCKED, 2 NOT_RUN | 24/24 PASS |
| Open Interpreter Rust 0.0.56 | [#6496](https://github.com/OrchestratorInc/agent-orchestrator/pull/6496) | `88c7431c427dda8c08802e9612042545a4febaf3` | 17/17 PASS, diagnostic attempt 5 | 24 PASS, 1 FAIL, 1 BLOCKED, 2 NOT_RUN | 24/24 PASS |

CI was recorded at **07:22:03 UTC on 10 October 2026**. The archived reports total 51 lifecycle PASS labels and 72 successful PR checks; one lifecycle label per agent has the instruction-refresh evidence gap explained above. These are different kinds of evidence and are not a combined test count. The [curated evidence summary](ao-agent-harness-session-2026-10-10/summary.json) records exact revisions, binary/report fingerprints, CI links, attempt timestamps, and remaining gaps. The 91/91 diagnostic-runner fixtures recorded in that historical export validate that runner revision only. Any newer skill fixture count belongs in this PR's own validation record.

The three PRs provide terminal adapters, exact native continuation, session-owned instruction/hook delivery, installation/auth/model metadata, registry and stored-harness wiring, API/product identity, and pinned native conformance as supported by each upstream. Tau's released terminal requires bypass mode. Neovate uses an explicit native plugin. Open Interpreter uses narrowly scoped native hook trust and bounded typed history/workspace checks; its Chat Completions compatibility may map native developer context to the wire user role and does not promise immediate provider socket closure on cancellation.

The complete gate and failure reports were published separately so a later success would not erase earlier failures:

| PR | Final gate report | Historical failures and follow-up |
| --- | --- | --- |
| Tau | [28-gate report](https://github.com/OrchestratorInc/agent-orchestrator/pull/6493#issuecomment-6095292928) | [Failure report](https://github.com/OrchestratorInc/agent-orchestrator/pull/6493#issuecomment-6095921916) |
| Neovate | [28-gate report](https://github.com/OrchestratorInc/agent-orchestrator/pull/6494#issuecomment-6095293549) | [Failure report](https://github.com/OrchestratorInc/agent-orchestrator/pull/6494#issuecomment-6095922325) |
| Open Interpreter | [28-gate report](https://github.com/OrchestratorInc/agent-orchestrator/pull/6496#issuecomment-6095294120) | [Failure report and screenshot reproduction](https://github.com/OrchestratorInc/agent-orchestrator/pull/6496#issuecomment-6095922723) |

## What the lifecycle evidence establishes

All three final diagnostic runs recorded PASS for the following 17 gates. The categories below group the checks; they do not collapse their individual report entries. The recorded `system_prompt_restore` label is a false-positive risk: token availability after restore is established, but freshly delivered instructions are not.

| Area | Gates and required evidence |
| --- | --- |
| Fresh session | TUI spawn; proof-file creation; correct working directory; initial prompt exactly once. The native agent had to write the requested evidence in the actual AO workspace. |
| Instruction delivery | Hidden AO standing instructions and project `AGENTS.md` independently influenced the proof file. Creating a configuration file alone was insufficient. |
| Ongoing work | Actual activity/status transition and a second message that completed. The runner must observe active-to-settled behavior, not count active-to-active as success. |
| Stop | Turn cancellation, kill/termination confirmation, and a persisted native session ID. Cancellation used Escape through the real mux, not an invented interrupt route. |
| Restore | Native restore with the exact prior native ID; same AO session/workspace; native terminal ready; post-restore message completion; original history/file continuity; instruction-token availability after restore. The historical token setup cannot establish refreshed standing instructions. |

Native identity was compared using the actual stored ID because AO's public session DTO does not expose it. The runner's narrowly scoped local database read retained the raw value only for equality and wrote a digest to reports. Restore HTTP 200 and a returned idle state did not establish native readiness or continuity. The continuation recalled a history-only token and appended a standing-instruction token while preserving the original proof file. Although named a restore-only token, the historical runner put it in initial `agentRules` before the first turn and did not change those rules before restore. A provider could therefore recover it from prior context. The result establishes token availability and file continuity, not refreshed configuration delivery.

For Open Interpreter's final passing run, native cancellation was observed after **22 ms**, while AO idle confirmation took **29,928 ms** under the existing approximately 30-second observation cadence. The restored task started **14 ms after the send API response**. These measurements distinguish native cancellation from AO's later observation; the work did not change production polling frequency. Saved styled samples bracketed the final readiness boundary but did not capture the exact initialized-empty frame immediately before that successful send.

Instruction consumption is also narrower than instruction secrecy. Open Interpreter echoed an audit-only instruction marker in tool output. The tests prove the intended standing instructions arrived and influenced execution; they cannot promise a model will never repeat context it has received.

## Strict audit gaps retained

| Gate | Tau | Neovate | Open Interpreter |
| --- | --- | --- | --- |
| AO verified authentication | BLOCKED: configured only | BLOCKED: configured only | BLOCKED: configured only |
| Supported native model-list command | NOT_RUN | NOT_RUN | NOT_RUN |
| AO non-empty model catalog | PASS | PASS | FAIL: HTTP 200 with empty catalog |
| Exact native/AO model-ID comparison | NOT_RUN | NOT_RUN | NOT_RUN |

Real Z.ai requests independently established that the test credential worked. AO's own probe still did not verify authorization, so the auth gate remained blocked. This metadata limitation is not evidence of an expired login and did not justify another login attempt. Open Interpreter's custom-provider observer was improved from unknown to configured for an unambiguous environment-key selection; competing native authentication settings still remain conservative.

Tau's `providers` output is an eleven-column provider TSV. Earlier parsing incorrectly treated its 29 provider rows as model IDs. That apparent model evidence and its derived mismatch were withdrawn, and the final runtime contract omitted the unsupported listing command. Neovate exposed no supported native model-list command in the inspected contract. Open Interpreter accepts free-form models; a working explicitly selected model did not make its empty AO catalog a passing catalog gate.

## Failed attempts, corrections, and controls

The original reports were retained. Some early report labels were wrong or incomplete; the corrections below and the supplemental evidence take precedence over those labels. Each retry used a separate attempt. A setup failure was not relabeled as an upstream defect, and a later passing diagnostic did not erase a failed attempted behavior.

### Tau

| Attempt | Observed result | Classification and resolution |
| --- | --- | --- |
| Strict 1 | Stopped on configured-only auth; dependent lifecycle work was not run. | Correct strict gate behavior, independently followed by authorized diagnosis. |
| Diagnostic 1–2 | Native composer appeared, but AO startup timed out. | Real adapter readiness defect: SGR styling split `Ask Tau…`. The production matcher now strips ANSI before matching. An earlier ring-tail hypothesis was disproved. |
| Diagnostic 3–4 | Startup stayed pending and native hooks could not find canonical `ao`. | Audit launcher defect: the renamed executable did not place `ao` on the child PATH. A symlink still resolved to the renamed binary through `os.Executable()`; a canonical hardlink corrected the launch. |
| Diagnostic 5 | Post-restore send received `409 SESSION_STARTUP_PENDING`. | Runner sent before the native startup hook. History and post-restore instruction-token availability remained unproven; the response was not a successful continuation. |
| Diagnostic 6 | All 17 lifecycle gates were recorded PASS; instruction-refresh proof remains unproven. | Used the fixed adapter, canonical executable, and native readiness wait. Auth/model gaps remained. |

### Neovate

| Attempt | Observed result | Classification and resolution |
| --- | --- | --- |
| Strict 1 | Configured-only auth blocked dependent gates. | Preserved without an expired-login claim. |
| Diagnostic 1 | Cancellation hit HTTP 404; restored continuation was unproven. | The runner invented an interrupt endpoint AO does not expose. Replaced with actual terminal-mux Escape. |
| Diagnostic 2 | Native cancellation worked, but restored input became unsubmitted pasted text. | Send occurred before the composer mounted. Post-restore message, history continuity, and the instruction-token check failed. |
| Control 1 | Stopped before sending. | Control searched for the PTY registry beside old data state; the registry lives beside the current run file. |
| Control 2 | Identical message completed after observing the actual composer. | Same native identity, history, and initial instruction-token availability passed in the controlled comparison; refreshed delivery was not independently isolated. |
| Diagnostic 3 | All 17 lifecycle gates were recorded PASS; instruction-refresh proof remains unproven. | Readiness-aware runner; no Neovate production fix was needed during the VPS follow-up. |

### Open Interpreter

| Attempt or investigation | Observed result | Classification and resolution |
| --- | --- | --- |
| Direct native write probe | Bubblewrap reported `RTM_NEWADDR: Operation not permitted`. | VPS sandbox/network limitation. The isolated audit explicitly selected native bypass; original failure retained. |
| Strict 1 | Selected custom-provider auth was unknown despite working provider calls. | Production observer now recognizes an unambiguous selected environment key as configured/fresh. Strict verified-auth gate remains blocked. |
| Diagnostic 1 | Missing canonical `ao` on PATH and scratch-folder trust; downstream proof/restore failures. | Audit fixture setup defects. These cascading failures did not independently prove each downstream feature broken. |
| Diagnostic 2 | Symlink still resolved the renamed executable. Runner was deliberately stopped after its API kill. | Partial run preserved; no completion claim. |
| Diagnostic 3 | Second message did not settle in AO and cancellation confirmation failed. Restore request remained draft. | Real observer defect: plain output did not identify the current styled idle surface. Original runner also falsely called active-to-active an activity PASS; supplemental findings override that label. Audit marker echo was recorded. |
| Production observer and review | Styled surface observation fixed activity/cancellation classification. | Shared observer now prefers styled output for surface-aware adapters while retaining fallback/error behavior. Review fixed styled Claude expired-login phrase handling and conflicting native auth configurations. |
| CI at `b34c3413c2fd75099d98a4e40781f8340a2c9115` | `errcheck` rejected an unchecked read-only config `Close` result. | Fixed in `ca612f7c4ca8f06e327dc6cf11c74fde2664724a`. |
| CI at `0eceedc6a203680fff41389091881988b383dae4` | Native hook-review decline shortcut was lost after menu render. | Native TUI quarantines typeahead for up to one second. Fixture now waits through that interval, rechecks the complete menu, sends one checked decline shortcut, and retains trust/no-provider assertions. Fixed in final head `88c7431…`. |
| Diagnostic 4 | Activity/cancellation passed; continuation entered `model: loading / Resuming session…` and stayed draft. | Weak readiness cue: the provisional screen already contained a composer placeholder. Three continuity gates failed. |
| Bracketed/raw settled controls | Both forms submitted after initialization. | Their fixed delay was an explicit confound; they did not establish a general readiness detector. |
| Separate fixture-provider backlog control | Suspending/resuming native execution showed raw pasted input could lose Enter; bracketed paste submitted. | Bounded control, not a real-provider lifecycle pass. No production paste-policy workaround was added. |
| Diagnostic 5 | All 17 lifecycle gates were recorded PASS; instruction-refresh proof remains unproven. | Stronger contiguous initialized-empty composer and configured model/workspace cue. Strict auth/catalog gaps remained. |
| Later screenshot reproduction | Ordinary raw send during initialization returned HTTP 200; no new native task began within 60 seconds. | Still reproducible at the final PR head. Readiness-aware success does not resolve the early-send path; screenshots and timing follow. |

The diagnostic runner evolved to require real active-to-settled transitions, use the actual mux, verify native restore generations, distinguish initialization from a ready composer, and allow bounded observer latency for cancellation confirmation. The preserved original strict runner had SHA-256 `66271ceb4a9fdd5b9db23edec4ee8505730f6723b8802306862b09b1fed4c297`. The final historical Open Interpreter diagnostic runner had SHA-256 `37e6d6f8901ba080ec1a41cc36d6a253a56abad8003f753a4357fedc7f0170d7`; Tau and Neovate used the intermediate revision recorded in the JSON. These fingerprints describe the session tools, not the final contents of the new skill PR. During packaging, review additionally found the instruction-token timing flaw described above. The new runner moves that token into configuration only after kill and before restore; the historical reports remain unchanged and a fresh live audit is still needed to validate the corrected gate.

## Fresh live screenshots of the unresolved early send

The earlier audit attempts had real ANSI/text captures but no PNG screenshots. On **10 October 2026, 09:01–09:02 UTC**, a new bounded reproduction used Open Interpreter at PR head `88c7431…`, the original weak `Ask Codex` cue, and an ordinary raw AO API send. It did not replay an archived failure log.

The provisional cue was observed at `09:01:23.924Z`; send started at `09:01:23.936Z` and returned HTTP 200 at `09:01:24.256Z`. The first capture began at `09:01:23.945Z`, overlapping the send while the native model was still loading. No new native task began during the 60-second observation. The final capture at `09:02:24.935Z` still showed the new request in the composer. Old successful turns visible above it belong to restored history and are not execution of the newly sent request.

These are actual X-window screenshots of an xterm window displaying a **read-only live mirror of AO PTY styled frames** in Xvfb. They are not direct-attached native terminal pixels, Electron screenshots, or screenshots of the original historical attempts. The mirror's source frames were credential/native-identity checked before capture; a few native icon glyphs render imperfectly. The committed copies redact incidental delivery IDs and scratch-path text, with no change to the behavior shown.

![Open Interpreter live provisional composer while the model is loading and the session is resuming](ao-agent-harness-session-2026-10-10/01-provisional-live.png)

![Open Interpreter live state after sixty seconds, with the latest request still in the composer](ao-agent-harness-session-2026-10-10/04-final-live-state.png)

Original and committed image provenance is recorded in the [evidence summary](ao-agent-harness-session-2026-10-10/summary.json). The original later three captures had identical hashes, consistent with the unchanged draft. Only the provisional and final frames are included here.

## Intake decisions beyond the three PRs

The supplied candidate list contained 52 entries. Existing registry support and prior open/closed PRs were checked before new integrations were proposed. Five candidates were already supported. Two had existing PRs with insufficient upstream contracts, and Reasonix already had a fork integration PR. Letta's new prototype was withdrawn after review. Most other candidates lacked sufficient evidence for private additive instructions, faithful permission/model mapping, persistent interactive identity, exact restore, or an appropriate terminal process lifecycle.

[ZCode #6003](https://github.com/OrchestratorInc/agent-orchestrator/pull/6003) was updated rather than duplicated. Its source-built official v3.14.3 TUI conformance demonstrated that the TUI never enabled workspace hook trust: a trust grant could succeed while hidden AO instructions still failed to arrive. The PR was left draft/upstream-blocked, and source-build installation guidance was corrected because desktop releases omitted the TUI. The recorded handoff at `922c642c085af4ca0ecbc7f1c65e330397e0fec5` had 22/24 CI successes, failed native hidden-instruction conformance, and frontend tests still running; this is not a final green status. Native restore/cancellation were not run after that gate failed. [Recorded conformance run](https://github.com/OrchestratorInc/agent-orchestrator/actions/runs/38010927686).

[Letta prototype #6492](https://github.com/OrchestratorInc/agent-orchestrator/pull/6492) was closed after review found that released hook aggregation could reveal hidden instructions when another hook blocked, and predecision callbacks could not establish acceptance/completion. No safe isolated additive module channel was established. It was not reopened or counted as a fourth completed integration.

The full appendix below preserves every candidate's disposition. Intake-time wording about unverified authenticated runs for the three new PRs is superseded by the later VPS evidence above; the appendix and curated JSON distinguish that historical assessment from final test results.

## Cleanup and retained evidence

At **07:25:25 UTC**, an independent process scan found zero remaining processes owned by the original audit. Each final session had confirmed termination and its daemon was stopped. The later screenshot reproduction separately confirmed session termination, daemon exit code 0, GUI/controller shutdown, and absence of all its recorded owned processes. Cleanup did not target unrelated user processes.

The original failed, partial, control, and successful artifacts remained available outside the repository. The initial sanitized export contained 166 files and had no matches in its exact-credential/API-key/JWT scan. This commit intentionally includes only a curated JSON summary and two redacted actual screenshots. It excludes raw logs, daemon data, worktrees, credential stores, provider profiles, native IDs, connection details, and binaries.

The evidence establishes working readiness-aware terminal task, cancellation, identity, and history/file-continuity paths at pinned revisions, with refreshed instruction delivery still unproven. It demonstrates why a version check, green fixture suite, or successful API response alone is insufficient. Remaining work includes a fresh live audit of the corrected instruction gate, the verified-auth/model metadata contract, early-send readiness behavior, portable real capture helpers, real macOS/Windows native lifecycle validation, and any future structured Chat/interface-handoff support. Scheduled autopilot remains a separate implementation project.

## Appendix: all 52 candidate dispositions

The following rows are the recorded 10 October intake decisions, not claims that every deferred candidate is permanently unsuitable. Upstream changes require a new pinned assessment and duplicate check.

### Already supported (5)

| Candidate | Recorded decision and evidence |
| --- | --- |
| [Gemini CLI](https://github.com/google-gemini/gemini-cli) | Canonical HarnessGemini on main |
| [OpenHands CLI](https://github.com/OpenHands/OpenHands-CLI) | Canonical HarnessOpenHands on main |
| [Codewhale](https://github.com/Hmbown/CodeWhale) | Canonical HarnessCodewhale on main |
| [MiMo Code](https://github.com/XiaomiMiMo/MiMo-Code) | Canonical HarnessMiMoCode on main |
| [Command Code](https://github.com/CommandCodeAI/command-code) | Canonical HarnessCommandCode on main |

### New draft integrations and withdrawn prototype (4)

| Candidate | Recorded decision and evidence |
| --- | --- |
| [Letta Code](https://github.com/letta-ai/letta-code) | Draft closed: released hook aggregation can reveal hidden instructions when another hook blocks, and predecision callbacks cannot establish acceptance/completion. No safe isolated additive mod channel found. [PR #6492](https://github.com/OrchestratorInc/agent-orchestrator/pull/6492). |
| [Open Interpreter](https://github.com/OpenInterpreter/open-interpreter) | Rust TUI integration with scoped hook trust, native developer context, exact typed history/workspace restore and modal-aware observation. Later VPS report: 17/17 recorded with readiness-aware input; instruction refresh unproven, early-send failure still reproduced, strict auth/catalog gaps retained. [PR #6496](https://github.com/OrchestratorInc/agent-orchestrator/pull/6496). |
| [Tau](https://github.com/huggingface/tau) | Terminal integration with explicit bypass only; pinned native conformance and session-owned prompt-file delivery. Later VPS report: 17/17 recorded; instruction refresh unproven and strict auth/model gaps retained. [PR #6493](https://github.com/OrchestratorInc/agent-orchestrator/pull/6493). |
| [Neovate Code](https://github.com/neovateai/neovate-code) | Terminal integration through an explicit native plugin and pinned released-TUI conformance. Later VPS report: 17/17 recorded; instruction refresh unproven and strict auth/model gaps retained. [PR #6494](https://github.com/OrchestratorInc/agent-orchestrator/pull/6494). |

### Existing PRs and upstream-blocked work (3)

| Candidate | Recorded decision and evidence |
| --- | --- |
| [Reasonix](https://github.com/esengine/DeepSeek-Reasonix) | Existing tested fork integration; official v1.39.2 lacks append-system-prompt-file and resume-exact. No duplicate PR. [PR #5905](https://github.com/OrchestratorInc/agent-orchestrator/pull/5905). |
| [Deep Agents Code](https://github.com/langchain-ai/deepagents) | Latest CLI 0.1.83 still lacks isolated per-process additive prompt/hooks input. Experimental --extension also discovers user/config/plugin/entry-point code; disabling discovery disables the explicit extension too. Exact resume still falls back to fresh on missing ID/DB errors. Native identity and TUI cancel exist; ACP cancellation is now session-scoped but not bounded. Existing PR #5735 remains a gate-only audit of 0.1.72, not a production integration. [PR #5735](https://github.com/OrchestratorInc/agent-orchestrator/pull/5735). |
| [ZCode](https://github.com/zai-org/ZCode) | Prepared integration fixes and source-built native conformance in existing PR, then marked it draft after conformance proved that official v3.14.3 TUI never enables workspaceHookTrustEnabled. Native trust grants succeed but TUI ignores them and AO hidden instructions never arrive. Desktop releases omit TUI; manual source-build guidance corrected. Await a supported upstream private instruction/hook path. No duplicate PR. [PR #6003](https://github.com/OrchestratorInc/agent-orchestrator/pull/6003). |

### Deferred pending a stronger contract or more evidence (31)

| Candidate | Recorded decision and evidence |
| --- | --- |
| [Hermes Agent](https://github.com/NousResearch/hermes-agent) | The latest environment reference now documents additive HERMES_AGENT_HELP_GUIDANCE and ephemeral prompt input; the old prompt blocker is partly superseded. An isolated per-process observer loader that loads only AO-owned code remains unproven. Current documented plugin loading uses user/profile or opt-in project directories. |
| [jcode](https://github.com/1jehuang/jcode) | No safe additive standing-instruction channel established; TUI lifecycle conformance not performed. |
| [Trae Agent](https://github.com/bytedance/trae-agent) | Official interactive interface lists task/status/help/clear/exit and trajectory recording, without documented exact session restore or private additive standing instructions. Earlier gate-only PR rejected because it had no production consumer. |
| [Plandex](https://github.com/plandex-ai/plandex) | Prior conformance PR found missing private per-process instructions, observer hooks, machine-readable identity/exact resume and compatible cancellation. Cloud is winding down; standalone gate-only PR was explicitly rejected as having no production consumer. No evidence found superseding that contract. |
| [Codebuff](https://github.com/CodebuffAI/codebuff) | Redirects to same upstream as Freebuff; avoid two adapters for identical source without variant evidence. CLI has --continue ID but hidden additive context preserving default agent remains unproven; custom --agent skips local .agents overrides. |
| [Freebuff](https://github.com/CodebuffAI/freebuff) | Same upstream as Codebuff. Freebuff variant has --continue ID but no positional task or --agent option; hidden context injection preserving native defaults remains unproven. |
| [ForgeCode](https://github.com/antinomyhq/forge) | --prompt selects one-shot mode; TUI must use after-start delivery. --conversation-id supports exact identity. Additive custom_rules exists through agent/workflow config, but per-session overlay preserving native defaults/settings needs design. |
| [gptme](https://github.com/gptme/gptme) | Exact --resume --name identity supported. --system replaces base instructions; hidden additive agent profiles are loaded from native config profiles directory. Isolated per-session profile/config overlay retaining user provider settings not established. |
| [OpenSquilla](https://github.com/opensquilla/opensquilla) | chat --session exact restore checks missing identity explicitly. Hidden additive per-session instructions, readiness, activity, cancellation and gateway lifecycle not fully established; not a proven incompatibility. |
| [Kode CLI](https://github.com/shareAI-lab/Kode-cli) | Exact native UUID preflight is implementable and hidden append-system-prompt-file is sound. The material released-code blocker is TUI flag wiring: --model and --permission-mode/tool restrictions are sent only to runPrintMode, not renderRepl; --settings is parsed but unused. TUI starts native yolo mode even with --safe. A full AO model/permission mapping would be false. Recommend waiting for working TUI wiring/per-session config, rather than making a misleading production registration. A narrower native-default-only terminal would have to reject these overrides and preserve explicit permission disclosures. |
| [Maki](https://github.com/tontinton/maki) | --append-system-prompt is SDK-only, not wired to TUI. Native hooks only load global/project .maki/init.lua with shared plugin.toml run/env trust; no per-process plugin/config channel established. |
| [DeepCode](https://github.com/HKUDS/DeepCode) | Current TUI has exact resume, model/access/trust options and native sessions, but runs against a shared background service where closing the client leaves work running. TUI CLI lacks documented additive per-process hidden instruction input. Requires dedicated service lifecycle design; not a simple TUI process adapter. |
| [Claude Engineer](https://github.com/Doriandarko/claude-engineer) | Source-run ce3.py self-improving assistant with generated tools and history; reviewed docs do not establish bounded native restore, private additive instructions or truthful approval controls. Repository API reports no recognized license. |
| [Claurst](https://github.com/Kuberwastaken/claurst) | Active Rust TUI and released binaries merit later investigation. README's ACP surface names initialize/new/prompt/cancel but not load; exact TUI restore plus private per-process instructions and permission semantics remain unproven. No claims made from clean-room branding alone. |
| [Every Code](https://github.com/just-every/code) | No modern developer_instructions configuration; experimental instructions file replaces defaults, changing CODE_HOME replaces whole user profile. Safe additive per-process channel not established. |
| [Devon](https://github.com/entropy-research/Devon) | Documented TUI requires separately installed devon_agent backend and devon-tui client. No demonstrated private instructions/exact native resume contract in the reviewed launch documentation; backend lifecycle must be separately designed. |
| [Atomic Agent](https://github.com/AtomicBot-ai/atomic-agent) | Native TUI and persisted sessions exist, but CLI/native ID restore and additive hidden instruction surface not fully established. README --system-prompt references delegated Claude adapter, not evidence for Atomic own TUI. |
| [Nanocoder](https://github.com/Nano-Collective/nanocoder) | Exact --resume UUID, hooks and systemPrompt append configuration exist. TUI initial prompt needs after-start delivery. NANOCODER_CONFIG_DIR/DATA_DIR overlay must preserve native provider settings/trust and prove composer readiness before integration. |
| [open-codex](https://github.com/ymichael/open-codex) | Legacy TypeScript Codex fork using shared ~/.codex instructions and providers; reviewed CLI reference does not expose exact resume or per-process private instructions. Do not inherit capabilities from modern Rust Codex by name. |
| [BitFun](https://github.com/GCWing/BitFun) | Cross-platform terminal distribution and safe native instruction/session lifecycle contracts not established in this pass. |
| [RA.Aid](https://github.com/ai-christianson/RA.Aid) | Real CLI with research/planning/implementation and cowboy-mode approval bypass, but reviewed docs do not establish private additive per-process instructions plus exact persisted session resume; not enough for registration. |
| [VT Code](https://github.com/vinhnx/vtcode) | Additive hidden instructions preserving defaults and full terminal startup/resume contract not established in this pass; no runtime evidence. |
| [hax](https://github.com/OleksandrChekhovskyi/hax) | Release binaries found only for Linux. TUI initial-task and native hook contracts not established in this pass; no runtime evidence. |
| [Groq Code CLI](https://github.com/build-with-groq/groq-code-cli) | CLI parser exposes temperature/system/debug/proxy only; --system supplies replacement custom prompt and exact persisted native restore is not exposed in inspected CLI. |
| [Dexto](https://github.com/truffle-ai/dexto) | Interactive --prompt and --resume sessionId exist. --agent ID/path selects config; additive hidden instruction overlay preserving selected default agent and exact installable CLI package pin remain unproven. |
| [Tura](https://github.com/Tura-AI/tura) | Inspected prompt runtime uses replacement instructions; additive hidden per-process instruction and exact resume contracts not established in this pass. |
| [agentty](https://github.com/1ay1/agentty) | Hidden additive instructions, identity, startup and restore contracts not established in this pass; no runtime evidence. |
| [g3](https://github.com/dhanji/g3) | No released pin was identified. No runtime conformance or complete source contract established. |
| [Orca](https://github.com/echoVic/orca-agent) | No per-process additive hidden-instruction channel established in src/cli.rs and crates/orca-runtime/src/instructions.rs; no runtime evidence. |
| [Coro Code](https://github.com/Blushyes/coro-code) | CLI argument declaration exposes config/model/working-dir/task and Tools/Test subcommands, with no exact native resume selector. Internal restore_from_history is not an exposed TUI resume contract. |
| [zot](https://github.com/patriceckhart/zot) | Custom parser rejects -- delimiter and unknown leading-dash prompts; --session path silently creates fresh history if missing. Safe exact task delivery and fail-closed native restore need upstream-compatible work. |

### Skipped for this integration scope (9)

| Candidate | Recorded decision and evidence |
| --- | --- |
| [Roo Code CLI](https://github.com/RooCodeInc/Roo-Code) | Repository metadata is archived=true. No new adapter proposed for this archived distribution. |
| [SWE-agent](https://github.com/SWE-agent/SWE-agent) | Documented product is research-oriented autonomous issue/task execution and benchmarking, not a demonstrated persistent interactive worker with native exact conversational restore. |
| [Amazon Q Developer CLI](https://github.com/aws/amazon-q-developer-cli) | Official README says no longer actively maintained, critical security fixes only, and directs users to Kiro CLI. AO already has Kiro; avoid redundant legacy integration. |
| [Claw Code](https://github.com/ultraworkers/claw-code) | Upstream explicitly calls this an agent-managed museum exhibit rather than a serious production product and says source-build only; the similarly named crate installs a different binary. ACP is expressly a discoverability/status stub. |
| [Free Code](https://github.com/paoloanzn/free-code) | README expressly describes a fork of exposed proprietary source with guardrail removal; repository API reports no recognized license. Distribution/supportability and trustworthy permission defaults are unresolved; avoid introducing it into AO. |
| [AutoCodeRover](https://github.com/AutoCodeRoverSG/auto-code-rover) | Documented product runs github-issue/local-issue patch-generation jobs using source checkout/conda or Docker. No persistent interactive conversation restore contract demonstrated. |
| [Codel](https://github.com/semanser/codel) | Docker/web application with PostgreSQL history and container task execution. Not a demonstrated terminal-native persisted conversational worker executable. |
| [Agentless](https://github.com/OpenAutoCoder/Agentless) | Research repair pipeline/benchmark workflow, not a demonstrated persistent interactive worker lifecycle. |
| [Smol Developer](https://github.com/smol-ai/developer) | Prompt-driven whole-program synthesis library/scaffolder; main.py generates code and human edits prompt/runs results. Native interactive restore/activity/permission contract absent from reviewed product interface. |
