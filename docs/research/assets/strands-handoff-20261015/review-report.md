# Strands PR6517 VPS preparation

Preparation is incomplete: actual Electron checkpoint coverage is BLOCKED by disk capacity, and independent ordinary-task standing-instruction delivery is not qualified. No remote writes occurred. Skill6509 was fetched read-only and never edited. The final publisher owns any future harness push; this stage has published nothing.

## Source and existing work

Remote head at start: `7ebd0dcaac36a39557e54ceccb844c54bb1f08d4`, matching the initially clean owned checkout. Latest skill6509 head: `5af6c2306250c7b2ada2563af5247e2d2c7eddf0`; all skill files saved outside source under `skill-reference/`. Read AGENTS, architecture/status, integration-path, evidence-and-handoff, audit-journeys/local-cli-contract, ao-desktop-dev and pr-description guidance.

Reviewed PR body, comments, inline reviews and CI read-only (snapshots retained). Node22 minimum, correct documentation URLs, public capability row, saved plugin/MCP/intervention rejection, native HOME fencing, and mixed parallel permission recovery were already fixed. No duplicate adapter change was made. Missing native history still fails explicitly; no automatic fresh fallback, invented catalog or Chat support was added.

Canonical main `54b28374912cb2771f17b8cc32a9482e6d607bce` had one migration-ledger conflict. Local merge `f13d91d2207fb09a802168616b6128e8373dd74b` retains both main0198 and Strands0201. All prior ancestry/evidence remains. No merged migration was rewritten. `main-reconciliation.json`, its patch and `main-merge-analysis.txt` retain the exact conflict/resolution. Main contributed252 automatically merged files; that is upstream integration, not252 newly authored fixes. The owned checkout is clean after this local commit; no branch/remote was pushed.

## Qualification boundaries

Fresh runtime used the existing exact-head CI artifact from run38652508512/artifact11935102394, source7ebd0dca. Executable SHA256 `371f73a34e67c7bf2b1a3116affd7aa8959bf1d545fddd46d016c725dd36082f`; SOURCE_COMMIT, SHA256SUMS and BUILD_INFO are retained in `runtime-ci/`. No daemon was rebuilt: the Strands adapter/provider source is unchanged by reconciliation. The merged daemon has not been executed or qualified; `runtime-to-merged-source-files.txt` lists the differences.

Fresh native packages: CLI0.2.0 / harness0.1.1 / SDK1.20.0, existing installed dependency files read only. New private profile/data/workspaces live under this stage. Real authorized Z.ai Coding Plan `glm-5.3-flash` used the supported native `litellm/glm-5.3-flash` model, through an owned loopback transport relay forwarding actual HTTP responses. No simulated model responses or model downloads. Native mode was explicitly bypassPermissions, corresponding to AO bypass-permissions/native Auto, not an OS sandbox or default one-time approval proof. Configured credentials remain unverified by AO's auth probe despite successful provider turns.

Historical98365b809 daemon /878957f frontend screenshots remain historical. New evidence does not qualify later UI or merged-source behavior. All new execution is Linux VPS only, not native macOS/Windows proof. No routine tests, unit/race suites, lint, typechecks, formatting or CI emulation ran here.

## Completion matrix

|Area|Supported scope|Verification|Proof or reason|
|---|---|---|---|
|Upstream identity/install|Pinned official CLI0.2.0, Node22 minimum|SOURCE REVIEW + native executable PASS|provenance.json; existing native deps, no new install|
|Agent Harness discovery/install UI|Installer plan exists|NOT_RUN / UI BLOCKED|screenshot-checkpoints.md; no genuine isolated Electron dependencies|
|Authentication|Configured-only native provider credentials|BLOCKED for verified auth|attempt-ao-001/report.json authentication gate; no fresh login claim|
|Models|Native free-form provider/model IDs; no native catalog|API catalog FAIL / manual mode source reviewed|Empty catalog retained; no fabricated list; model observed in terminal and saved spawn|
|Harness Models UI|Separate Settings surface required|NOT_RUN|No current real AO page captured; do not substitute New Task|
|New Task selection/UI creation|TUI worker/orchestrator registration|NOT_RUN|All new tasks created via daemon API, not real UI|
|Actual provider work/workspace|Registered AO TUI|PASS at7ebd0dca|New native local proof and AO_AUDIT_PROOF.json in exact owned worktree|
|Once-only initial prompt|Assisted audit fixture|PASS at7ebd0dca|runner initial_prompt_exactly_once; independent substring count has a separate caveat below|
|Private/project instructions|Assisted audit consumption|PASS at7ebd0dca|hidden_ao_instructions/project_agents_md; does not qualify ordinary-task delivery|
|Independent instructions/defaults|Ordinary fresh + restored tasks, no visible private hints|INCOMPLETE|attempt-independent-001/receipt.json and effect snapshots; bounded fresh attempt did not settle or write instruction receipts|
|Followup/activity|Daemon API + native terminal|PASS at7ebd0dca|second_message and observed active-to-idle gates; actual UI composer NOT_RUN|
|Executing-turn cancellation|Guarded foreground shell only|PASS at7ebd0dca|runner cancellation PID/kernel-start witness, mux Escape, child stopped and late file absent past scheduled write|
|Kill/archive|Daemon termination; owned process cleanup|PASS for recorded termination|runner termination + independent before/after process receipts; real Archive controls NOT_RUN|
|Executing-child Kill isolation/no late effects|Foreground child + independent control|HISTORICAL ONLY for full strong control|Earlier Kill003 retained; new control-after-independent-kill shows restored unrelated session alive but does not qualify newly witnessed executing-shell Kill/no-late-effects|
|Exact restore/history/new instructions|Exact native ID/workspace, initialized composer|PASS assisted at7ebd0dca|native_restore/post_restore_terminal_ready/history_file_continuity/system_prompt_restore; no fresh fallback|
|Archived/restore/Kanban UI|Same task through actual controls|NOT_RUN|Mandatory images absent, not inferred from API/native success|
|Reconnect/daemon replacement|Not exercised in this stage|NOT_RUN|No new recovery claim|
|Chat/reviewer/handoff|Unsupported TUI-only integration|N/A|No Chat driver/registry or capability; no fabricated features|
|Configuration guard fixes|Saved plugins/MCP/interventions/home restrictions|SOURCE REVIEW; historical tests/CI distinct|Current existing fix source inspected; no new negative native probe or local-suite claim|
|Merged-source CI|Local f13d91d2|NOT_RUN|No push/CI dispatch here; remote24green checks are7ebd0dca only|
|Real AO screenshot journey|Every latest-skill checkpoint|BLOCKED|Index created before any UI action; no actual UI launched/images generated|

## Runtime receipts and failures

`attempt-ao-001/report.json` is the latest-skill diagnostic:24PASS,1authentication BLOCKED,1empty-catalog FAIL,2catalog NOT_RUN. The top-level FAIL counts one agent; it is not one failing mandatory gate with the remaining missing gates implied successful. Cancellation has an actual started command PID/kernel-start witness, not just an accepted interrupt. Exact restore includes same native-ID digest, workspace, native empty-composer cue, provider-authored continuation, history-only token, and a new private token introduced/read back only after confirmed Kill. See retained gate evidence for generations and event times.

`--keep-daemon` finalized the report but retained Node handles. After observing `state:finished`, only exact runner PID1183175 was terminated so the already-owned daemon remained available. `runner-finished-retained-process.json` preserves this operational issue; no skill script was modified. Runner exit-15 is not reported as a clean runner-process exit or a strict PASS. All raw/sanitized first-attempt reports survive.

`attempt-independent-001` used separate project AGENTS and native domain guidance plus private project agentRules. Ordinary visible tasks were the two prompts retained verbatim in `receipt.json`; they contain no hidden tokens/receipt paths or request to read instructions. Fresh wrote the requested two-line weather.txt but remained active at140seconds and had no private/project/native receipt effects. It was killed, owned cwd processes were absent, new private instructions were stored/read back, and exact same AO session was restored for an ordinary continuation. These incomplete effects cannot be promoted using assisted-runner successes. Native terminal text captured through a read-only secondary mux attachment is diagnostic text, not an AO screenshot or UI pass.

The initial independent `ordinaryInitialPromptCount` in the receipt used substring matching across user messages including tool results. Its value2 includes a tool echo while the model inspected the local receipt; it must not be interpreted as two native task submissions. A separate exact direct-text readback should be used for dispatch counts. All original receipts remain intact.

## UI blocker and resource protection

Preflight: about887MiB disk available, 2808MiB memory available. Strands frontend has no node_modules; retained Mastra dependency reference is1.3GiB. A genuine fresh npm ci would exceed available space while preserving512MiB reserve. No tmpfs, symlinks, install retry, host tuning, swap changes, dependency cleanup in other harnesses, production restart, or broad kill/delete occurred. Minimum runtime used a25MB compressed existing CI artifact rather than rebuilding. Free space remained above512MiB.

The skill explicitly says “Missing required screenshots leave `AO UI evidence: BLOCKED`”. Every required checkpoint remains in `screenshot-checkpoints.md` with no image substitute. Historical retained-session screenshots cannot retroactively cover discovery, install progress, auth actions, model surfaces, UI creation, followup/cancel, Archive, restored Kanban or cleanup. No screenshot was fabricated and no unavailable product UI was invented.

## Reproduction and review artifacts

Read-only remote retrieval: `python3 /home/azureuser/.ao/audits/harness-watch-20261010/tools/as-pr-owner.py gh pr view 6517 --repo OrchestratorInc/agent-orchestrator --json headRefOid,body,comments,reviews,statusCheckRollup`. Exact stored snapshots include source head, inline comments, checks and latest skill snapshot. Artifact retrieval was `gh run download 38652508512 --repo OrchestratorInc/agent-orchestrator --name harness-proof-linux-6517 --dir ../runtime-ci` through the same read-only helper.

Runtime orchestration source is `reports/run-runtime-001.py`, exact sanitized runner argv is `attempt-ao-001/command.json`, contract is `attempt-ao-001/contract.json`, and independent-control source is `reports/independent-control.py`. Every attempt directory is unique and original logs/profiles are retained. The launcher reads an existing authorized key internally without logging it; do not publish the private profiles/databases/raw terminal logs. Reproduction creates a new attempt directory and reuses no old session IDs. Serialized compute lock, GOMAXPROCS1, GOFLAGS-p=1 and Node heap512 are explicit. All AO data/run files/tmp/private native HOME/relay records stay in this stage.

`reports/provenance.json`, native package/entrypoint and runner hashes, CI BUILD_INFO, raw gate evidence, source reconciliation receipts, category counts, local draft PR body/comment and final cleanup/result files are the review set. The draft body corrects the stale remote evidence SHA/CI attribution; it is unpublished. Code +820/-9, Tests +755/-3, Others +2747/-3 with5binary PNG assets were recalculated against actual canonical main and local merge, not summed commit churn.

## Remaining work

Provision enough disk for genuine isolated Electron dependencies while preserving512MiB reserve, then execute all latest mandatory screenshot checkpoints. Requalify independent ordinary-task instruction effects and restore continuation (including positively observed native readiness and history). Qualify active-child Kill isolation/no late effects and reconnect at the intended final source. Run changed-source checks only in authorized CI after final publisher review/push. Inspect those remote checks; existing-head success is not merged-source success. Keep strict diagnostic auth/catalog gaps and manual product model mode separate. Other OS/provider/native release claims remain unqualified.

## Final independent outcome and cleanup

The restored ordinary task settled idle and changed weather.txt to three lines, preserving the first two. Its same native-ID digest was `ae1f357713739609aaf354f46150c39c3a1aa4b72b0c2845424b908b51f3cd6f`; native config remained unchanged. All private/project/native receipt files, including the newly configured restored-private receipt, remained absent. The assistant nevertheless claimed receipt rules satisfied. This is observed instruction-effect FAIL / overall independent attempt INCOMPLETE, not a source-reviewed pass. No deterministic adapter defect was established, so no speculative production prompt/adapter change was made. `independent-final-assessment.json` independently counts exactly one direct initial user text and one direct restored user text; original misleading substring receipt count is preserved with its caveat.

The launcher exiting also removed its retained daemon, while the detached diagnostic pty/native pair remained alive. First cleanup refused to signal an absent daemon; `cleanup-attempt1.json` preserves this error. A second bounded cleanup started the exact same owned CI daemon against only its scratch data, terminated the remaining registered diagnostic session, confirmed both records terminated, and shut down with HTTP202. This is cleanup recovery, not a new provider-continuation/reconnect pass. `cleanup.json` records daemon executable/hash/PID, session states, clean daemon exit and protected-process identities. Remaining native process absence is independently checked in `final-process-check.json`. No broad kill was used.

All evidence/profiles/binaries remain. Production daemon429811, unrelated daemon625660 and tunnel1124892 retain the same executable and kernel-start identities. No other harness tree/profile or service was changed. Final free disk remains above512MiB. Merged-source CI stays NOT_RUN; remote head and24green checks are unchanged.
