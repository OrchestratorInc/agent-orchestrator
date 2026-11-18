# Docker Agent native ACP integration — 2026-10-11

Integration PR: https://github.com/OrchestratorInc/agent-orchestrator/pull/6540
Canonical base: fbce28759c4d152d0973ce03a86079b571377987.
Source under qualification: 738b7d36385e41e5305bb723657734a9ad0ca26e.

Official docker/docker-agent v1.150.0 (Apache-2.0), release commit
5277e7842058fc3fbb9943a6e88bc05a1c8e6fe2. Standalone Linux amd64 binary
SHA-256 791fa6bac9f829847f00ab948f838bf54d11e0830988a5e5bce88d65ea65f2d1.
Docker Desktop is optional. Historical closed TypeScript cagent PRs217/218 do
not supply a current Go adapter; alias searches found no duplicate current PR.

The adapter uses AO nativeacp/persistent-host boundaries, the existing native YAML
selected by AO_DOCKER_AGENT_CONFIG, and a private owner-only session-start hook.
The native hook appends context to preserved config/hooks/tools. Native shell
permissions remain provider-owned; AO does not advertise client filesystem or
terminal execution. Auth inventory stays unknown; real authorized Z.ai-compatible
API requests with glm-5.3-flash establish this profile's runtime proof only.

Scope: Unix, pinned v1.150.0, ACP Chat only. Explicit TUI and terminal fallback are
rejected before allocation. Accept-edits and reasoning-effort overrides are refused.
No reviewer registration or TUI/Chat handoff. Configured native model options are
used; optional negotiated capabilities are not independently qualified here.
Migration0205 was reserved only after scanning all open PR files; exact old repair
recognizers remain intact and historical incomplete/burned schemas are reconciled.

Native attempt001 passed initialization/auth/new, correct-workspace file editing,
leading-dash prompt as data once, independent private instructions, preserved
AGENTS/native config, second message, persisted exact load/replay, fresh restore
instructions, unknown-history and mismatched-cwd rejection, permission approve,
reject/cancel and executing-shell cancellation. Child PID887340/kernel start75578296
was gone after cancellation; no late file appeared after22seconds.

Earlier failures remain distinct: AO001 at f73913565 wrote into the source project
rather than the session worktree. Its real Electron screenshot is retained; the
adapter now includes the actual session workspace in private context. AO002 had an
audit-only malformed YAML hook and failed before creating a session. AO003 proved
correct worktree/standing context but exposed silently dropped effort overrides;
738b7d363 rejects them. AO004 lifecycle observer initially expected `idle` instead
of the actual settled Chat `ready` state; its no-tool control also exhausted native
reasoning tokens. The corrected observer and file-edit control run are separate.

Routine validation is GitHub Actions only by user instruction. No VPS tests, lint,
typecheck, vet, race or workflow suites ran. Minimum OpenAPI/TS generation and AO
builds support actual runtime evidence. No Mac work, release, deployment or merge.

Current-head AO lifecycle, screenshot, CI and cleanup receipts are appended below
when complete; this report does not treat pending gates as passes.


AO004 passed permission rejection, pending permission cancellation, executing
Interrupt (child892873/start75750331), follow-up recovery, independent AO Kill
(child892924/start75754343), and exact persisted restore with fresh post-Kill
instructions. Both children were reaped and produced no late side effects after
22seconds. Kill returned200/ok, terminated readback true, with freed=false because
the dirty worktree was correctly preserved. The unrelated control retained its
native identity/generation and host892787/native892792 across Kill. Restore retained
the native ID digest, changed generation and host to893190/native893195, consumed
FRESH_RESTORE_DOCKER_6509 and left the initial leading-dash user prompt count at1.

Electron replaced daemon892156 with893602. Reconnection preserved native host893190
and child893195 (kernel start75766301); RECONNECTED_DOCKER_AO_6509 was appended in a
further real AO turn. Screenshots have matching before/after native digest, generation,
serving executable SHA and UTC. Both UI and actual serving daemon VCS revision are
aa7705614e6f91041ff5f1e71de6315104505a66, vcs.modified=false. Initial lifecycle
source738b7d363 differs only by subsequent lint/import/error-message fixes.

The local reconnect helper overwrote lifecycle.json with an empty observer report;
lifecycle-curated.json explicitly reconstructs the already observed passing gates
from retained launch receipts and contemporaneous observations. No gate was inferred
from declarations. Original logs/DB/workspaces remain private and preserved.

![AO native shell after exact restore and detached-host reconnect](assets/docker-agent-20261011/native-shell-reconnected.png)

![Earlier actual wrong-workspace failure, preserved separately](assets/docker-agent-20261011/wrong-source-workspace-failure.png)

Owned final sessions returned Kill200/ok and terminated readback true. Exact owned
Electron893373, daemon893602, hosts/provider processes and marked children were
absent on later checks. Dirty worktrees and private profiles remain preserved;
production service and unowned audit daemon were untouched.
