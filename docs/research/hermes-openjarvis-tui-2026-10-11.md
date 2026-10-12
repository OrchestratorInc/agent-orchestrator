# Hermes and OpenJarvis: actual scoped AO TUI integrations

VPS execution only, 2026-10-11. No developer Mac work, merge, release or production deployment. Separate source PRs: [Hermes #6536](https://github.com/OrchestratorInc/agent-orchestrator/pull/6536), [OpenJarvis #6537](https://github.com/OrchestratorInc/agent-orchestrator/pull/6537). Canonical main base: `fbce28759c4d152d0973ce03a86079b571377987`. Final scan covered all 369 open PRs: migrations 0203/0204 were unclaimed by other branches.

## Current source and validation

| Integration | Exact source head | Proof executable SHA256 | Remote CI |
|---|---|---|---|
| Hermes | `c5615c8494821d572649ae3847977b8aaa4fd4b8` | `cc4c54c3cd45d7a104cb3f1b177915c80c0c598b7254ba259ec87a7710bd1e03` | 23/23 successful |
| OpenJarvis | `a4bc98eb0a759b4bcf38cf7a6481c45a9ac77315` | `6028d413e91ed316a7eb11da70cfc2010812e5510c961b28fa1372759fb0e0fb` | 23/23 successful; unchanged-head full affected-job rerun after initial TestKillReq failure |

Both binaries report these revisions with an unmodified source tree. Routine tests, lint, typechecks, race and native-OS validation ran in PR CI only, per explicit user instruction. VPS work was essential artifact generation/builds and real native/provider/AO/GUI proof. The initial OpenJarvis backend-core-1 failure remains retained; it was not a software fix. Full fixture validation for the final evidence PR is recorded separately after its final push.

## Native contracts and actual results

Hermes official v0.21.6 source `818c13be1dc4fd28987e1e881a9408224afd4535` uses classic interactive `chat --cli --in <workspace>`. Its native banner dates the version 2026.9.24; release metadata identifies 2026-10-08. Additive per-process `HERMES_EPHEMERAL_SYSTEM_PROMPT` preserves native personality and safety. Modern `--tui` does not qualify for this prompt route. Sticky/named profiles, managed overlays and unresolved environment-expanded configuration fail closed. Native permissions remain native; no upstream internals or global plugins were modified.

OpenJarvis is the official CLI **1.0.4**, fixed source `0eb7811df96cf139e6bd6d7fcc9b5cc4ff1418ff`, not desktop-v1.0.2. Interactive `jarvis chat --agent orchestrator` uses existing coding tools and a private additive config prefix; it never replaces SystemPromptBuilder with `--system`. Native history is in memory: no native ID or restore is advertised or fabricated. Native startup warns that its Rust rate limiter is unavailable; this limitation is not hidden or bypassed.

Actual compatible cloud provider: authorized Z.ai coding endpoint, `glm-5.3-flash`; no local inference model downloads. Configuration is not verified authorization or model-catalog discovery.

| Gate | Hermes final attempt 008 | OpenJarvis final attempt 008 |
|---|---|---|
| Model writes real file in actual AO worktree | PASS | PASS |
| Initial task exactly once; hidden arithmetic instruction plus AGENTS.md | PASS: 437, PROJECT6509, count 1 | PASS: 437, PROJECT6509, count 1 |
| Second AO message; active to settled; current empty composer | PASS, count 2 | PASS, count 2 |
| Executing-shell native Ctrl+C, PID/start-time reap, no late file after 32 seconds | PASS | PASS |
| Independent AO Kill with unrelated control unchanged | PASS, HTTP 200 | PASS, HTTP 200; fresh independent conversation |
| Exact native restore after Kill, empty composer, fresh instructions | PASS: same native identity, new generation, 837, count 3 | NOT_RUN: unsupported; restore refused HTTP 500 |
| Native permission guard without accepting approval | PASS: current native `/new` modal retained | PASS: current native Confirm retained; AO becomes blocked after observer latency |
| Current `/new` identity replaces stale startup lease | PASS: new breadcrumb identity, same AO generation, count 4 | NOT_RUN: no native identity surface |
| Native shell AO routing, persisted checkpoint and HTTP readback | Not separately qualified in final proof | PASS: `rpt_c649a292-9313-49ca-9600-b2854dd0c5e5` |

Hermes exact restored identity digest: `b420afc756aa8d5c3c5ac71f51286869ff0401f39c16c1f173fcdd200b538581`; restored generation `ecc6f9c2-73ed-4532-9300-807bd29c64bb`. `/new` digest `fb5422827ea64e2d373f5d183aa3314c973f0680e84613767ead4f7fa72ccc31` matches the actual native PTY breadcrumb while the startup lease retains the old digest. Native `/new` rows can persist NULL cwd: live identity is supported through corroborated process/PTY evidence, but those conversations are not restorable under this adapter. Persisted restore requires durable matching workspace/history and fails closed after breadcrumb ownership loss.

OpenJarvis has no native identity digest. Primary generation `eff158dd-7f1a-4325-8c92-fc8f78d4e744`; permission target generation `1683d763-f9bf-439a-b9b4-29fef18e9e19`. Input is scoped to plain single lines up to 3000 UTF-8 bytes; control characters, slash commands, ambiguous prompt markers and uncertain pending delivery are refused. Chat/switch/handoff are unavailable. Permission/input refusal currently surfaces a generic HTTP 500 with preserved request ID; safety passes, error UX is a documented limitation. AO's default observer interval is 30 seconds, separate from native idle time.

The common terminal change preserves current cursor-bearing normal-screen rows on height shrink, including wide-cell boundaries. Actual post-Electron-resize current `You>` and AO idle were observed. This does not establish full width reflow or alternate-screen parity. No historical ring replay or synthetic prompt was used.

## Original actual AO Electron screenshots

These original PNGs are revision-pinned; adjacent provenance JSON records UTC, serving executable hash/PID, source/UI revision, session generation and identity before/after. The UI lab used real npm-ci dependencies in the same physical checkout, isolated AO data, and the matching source revision. Images were inspected for secrets.

### Hermes final exact restore

2026-10-11 08:19:59 UTC; backend/UI c5615c8; serving PID 882470. SHA256 7f93912f6137220510d84bc15eabb9f0ac8b8b2e714d1a7e795fef61a16b71e0. Native italic composer text is an empty-input hint.

![Hermes final exact restore](assets/hermes-openjarvis-20261011/hermes-008-restored.png)

### OpenJarvis final native routing and cancellation

2026-10-11 08:04:16 UTC; backend/UI a4bc98e; serving PID 878715. SHA256 e0f75f011ba78116cb5bf6bb79f269dadf7103724c42943c52f04aadb172ca2d.

![OpenJarvis final native routing and cancellation](https://raw.githubusercontent.com/nikhilachale/agent-orchestrator/019e960a15d6356110213f776d885c556e0ca32e/docs/research/assets/hermes-openjarvis-20261011/openjarvis-008-routing-and-cancel.png)

### OpenJarvis final native permission

2026-10-11 08:05:03 UTC; backend/UI a4bc98e; serving PID 878715. SHA256 7ca46aa8c61d603e836da3ab2e5288f72cc1367bf4a948ff6751f31be1590a08. Native approval was not accepted.

![OpenJarvis final native permission](https://raw.githubusercontent.com/nikhilachale/agent-orchestrator/019e960a15d6356110213f776d885c556e0ca32e/docs/research/assets/hermes-openjarvis-20261011/openjarvis-008-native-permission.png)

### Earlier Hermes token refusal

Earlier failure: built-in policy refused the private token request. This is not hidden-instruction success.

![Earlier Hermes token refusal](assets/hermes-openjarvis-20261011/hermes-004-token-refusal.png)

### Earlier OpenJarvis routing and resize gap

Earlier failure: native shell dropped AO env; current rendered prompt was lost on resize. This capture retains its older UI/source provenance.

![Earlier OpenJarvis routing and resize gap](https://raw.githubusercontent.com/nikhilachale/agent-orchestrator/019e960a15d6356110213f776d885c556e0ca32e/docs/research/assets/hermes-openjarvis-20261011/openjarvis-004-routing-and-resize-gap.png)

### Earlier incomplete blank AO capture

Original incomplete capture has no identity before/after or verified capture UTC; no later success provenance is borrowed.

![Earlier incomplete blank AO capture](assets/hermes-openjarvis-20261011/hermes-004-incomplete-blank.png)

## Retained failures and diagnostic limits

Native Hermes attempts 001–005 and OpenJarvis 001–003 remain retained, including telemetry/rapid-input/confirmation fixture failures. AO Hermes 003 italic readiness failure, 004 token refusal, 005 dirty-build proof, and 006 `/new` NULL-cwd identity failure remain separate from final 008. Hermes 007 was a clean intermediate revision, not the final head. OpenJarvis 004 routing/resize failure, 006 later model re-request of a previously cancelled command followed by an approved rerun that produced a late artifact, and 007 model write in the wrong configured project remain retained. The fresh 008 Kill control does **not** fix or erase the stricter 006 failure. No generic Escape before shell execution is counted as executing-shell cancellation.

Installed unmodified audit runner SHA256 `8887aa2e7101b9e6d3950bfca08ffddf2e1e6c1c3f1d030c8dec2676443aecea` produced diagnostic-only reports: Hermes activity/second-message audit failures and configured-unverified auth/missing model catalog; OpenJarvis direct-engine model-discovery failure and installation timeout. These are not full strict PASS; custom supported TUI proof is separately qualified. No runner was changed to force PASS. Untouched runner was not locally fixture-retested. Historical 80ms fixture 144/145 then unchanged 145/145 is not a fix. Previous Tau001 capture gap, OpenInterpreter early-send failure and all earlier diagnostic restrictions remain unchanged.

## Cleanup and research scope

Final Hermes target Kill HTTP 200, PTY host absent, exact serving daemon 882470 terminated and absent. Final OpenJarvis primary and permission targets Kill HTTP 200, hosts absent, exact serving daemon 878715 terminated and absent; independent control/Kill targets were already cleaned with numeric receipts. Exact owned Electron main processes were stopped. Evidence, proof binaries, profiles and dirty scratch worktrees are preserved; the production ao-self-hosted service was untouched. No broad process kill or dirty worktree deletion.

Research only: [Mastra](mastra-assessment-20261011.md), [Polygraph](polygraph-assessment-20261011.md), [Docker Agent/Pool/Blackbox](candidates-docker-pool-blackbox-20261011.md), [CodeBuddy/Stakpak](candidates-codebuddy-stakpak-20261011.md), [Warp/Rovo/Hermes ACP](candidates-warp-rovo-hermes-acp-20261011.md). These candidates were not installed or provider/AO-qualified by this worker.
