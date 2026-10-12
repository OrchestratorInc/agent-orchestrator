# CodeBuddy Code native TUI — 2026-10-11

Integration: https://github.com/OrchestratorInc/agent-orchestrator/pull/6546
Final source, daemon and UI: 5827f13d75b639eac6ed76f6d572b6752c060da5.
Released npm `@tencent-ai/codebuddy-code@2.164.0`; Node24.21.0 / Ubuntu.
Npm shasum f37c043c61cb6fbd1d28d9df6da9d750c0ccfde9; launcher SHA256
426186a82e36466d5be485f335ced1b0d664539c53b72a5f10ba3a267e60dab6.
[Release pin](assets/codebuddy-20261011/release.json).
Supersedes closed, unmerged contract-only AO5503, pinned2.151.0; no newer duplicate
was found. Canonical main c087cb02ee remained current during the final refresh;
374 open PR heads were scanned and migration207 had no other conflict.

The supported scope is foreground TUI with native Default/manual permissions,
direct native model IDs, actual bounded additive `--append-system-prompt` text,
workspace lifecycle hooks, serialized current-viewport input and exact history
restore. Native provider success used the authorized Z.ai OpenAI-compatible coding
endpoint and `glm-5.3-flash`. Credential inventory remains configured, not a general
remotely verified native-account authorization claim. No other vendor OAuth was
imported. ACP/Chat, handoff, reviewers, Auto/Accept Edits/Bypass and background work
are excluded. Missing/corrupt/ambiguous/cross-workspace history fails hard; over32MiB
histories and private instructions over64KiB are conservatively refused.

Native005 passed the leading-dash positional task, correct cwd file edit, additive
private token independent of visible task, AGENTS.md token/preservation and second
edit with manual native approval. Exact same-ID restore after termination passed
in006 with fresh additive instructions. Native007 approved a foreground shell,
recorded child921850/kernel start77518131, cancelled, reaped it, and observed no
late side effect after the deadline. Native hooks preserved an independent user
SessionStart callback. [Curated lifecycle evidence](assets/codebuddy-20261011/lifecycle.json).

Actual AO initial and second turns at3246768a passed the same workspace/token/
AGENTS.md and active-to-idle controls. Source revisions afterward fixed CI contracts,
read-close handling/comments, test count and generated install-target enum, not the
runtime ownership boundary. The native CLI kept the initial prompt exactly once.
AO refused an owned user draft with409 and preserved it, and refused sends into
native permission decisions. An executing child925890/kernel start77688350 in the
lifecycle receipt was cancelled/reaped with no late effect. A second Escape opened
native Rewind; the owned dialog was observed and dismissed, without clearing user
data. The first subsequent Kill-script assertion retained that dialog failure.

Independent AO Kill terminated main host924380, supervisor924385, native924395 and
executing child926370/start77704820. Unrelated control host926070/native926088 stayed
alive with the same kernel identities. The optional `freed` response was omitted;
actual terminated/kernel readbacks established cleanup and no delayed side effect.
A failed assertion expecting `freed=true` is retained, not silently converted into
an API claim. Removing only the owned exact history temporarily made restore500
with no fresh generation; the unchanged envelope hides the internal cause, while
the controlled missing file and unchanged terminated state establish failure.
The native history was restored immediately and its raw identity is never published.

The first desktop launcher used CI=1, which enables bundled Ink static rendering.
Its resumed history had no initialized composer, so AO correctly refused input.
The final lab used CI=false without native modifications or permission bypass.
Exact native digest9854a6a5bc6a408f7562d3f99321d1bfd8528aae3141815a8075104f21f7e73e
resumed in a fresh generation on final5827f13d. A subsequent native edit preserved
standing/project, changed count2→3, identified historyCount2, and consumed fresh
RESTORE_CB_AO_6509 from private instructions after confirmed Kill. The visible task
contained no token and requested no private config/file read. AO observed active
then idle. Native prompt suggestions are conservatively nonempty; an earlier send
was refused and explicit owned native typing was used. The final owned profile
merged supported promptSuggestionEnabled=false and AO send then worked normally.

The final screenshot below shows actual AO navigation, live native edit/history and
fresh-token result. Permission screenshot shows native Yes/No choices; native reject
left the side-effect file absent. Both were inspected for secrets. Their provenance
records contain serving PID928665, executable SHA256
1cb98f502fa15c2d6abce2a0078a35f2cc5e8f59460c208ffabc830b285c71f8,
exact Go VCS/UI commit5827f13d, runfile, native digest, generation, UTC, PNG hash and
before/after equality. No synthetic or log-generated image substituted for AO.

![Actual restored AO CodeBuddy TUI](assets/codebuddy-20261011/restored-tui-final.png)

![Actual native permission card in AO](assets/codebuddy-20261011/native-permission-visible.png)

[Restore provenance](assets/codebuddy-20261011/restored-tui-final.provenance.json) ·
[Permission provenance](assets/codebuddy-20261011/native-permission-visible.provenance.json).
The startup-services capture and preceding active-frame capture are preserved as
limited/failed capture controls, not relabeled as permission evidence. The earlier
initial/second-turn screenshot predates final provenance instrumentation.

Earlier native failures remain separate: trust dialog in001; models.json-only route
still selected Tencent login in002; incompatible Anthropic-shaped Z.ai route in003;
manual write approval left unanswered in004. The supported OpenAI coding route then
passed005–007. Native005 restore initially lost Enter during initialization;006 used
a stable composer and passed independently. These failures are not retroactive passes.

Routine tests/lint/typecheck/vet/race/workflows ran only in GitHub Actions. Meaningful
adapter, hook, admission, migration/reconciliation, installer and auth tests were
written but not run locally. Essential package installation, generated API artifacts,
serial resource-guarded daemon/UI builds and actual live proof were the only local
execution. The first artifact build stopped at the750MiB disk guard; inactive owned
generator/build/compiler/optimizer caches were reclaimed with receipts. No native
model downloads, Mac work, production sessions, release, deployment or merge occurred.

Exact-head CI and exact-owned cleanup are appended in the integration PR handoff.
This is a limited native/AO qualification, not a strict catalog/authorization audit
runner pass: native model discovery is freeform and AO auth is configured/unknown.

Final source CI: all23 exact-head checks green at5827f13d75b639eac6ed76f6d572b6752c060da5.
[CI receipt](assets/codebuddy-20261011/source-ci.json). No local suites ran.
The final empty-prompt native control initialized after manually trusting only its
exact throwaway folder; main Kill left that control's native kernel identities and
generation unchanged, then all three owned sessions were terminated and their
host/supervisor/native processes disappeared. Electron and the owned daemon were
stopped; daemon shutdown202 and later exact PID disappearance are recorded. Dirty
worktrees, native profiles/install, private DBs/evidence and production625660 were
preserved. [Cleanup receipts](assets/codebuddy-20261011/cleanup.json).
[Initial-once transcript control](assets/codebuddy-20261011/initial-once.json) and
[visible permission/rejection control](assets/codebuddy-20261011/permission-capture.json).
