# Cloud project settings

Cloud projects load from `GET /orgs/{orgId}/projects/{projectId}` and save through
`PATCH /orgs/{orgId}/projects/{projectId}/settings`. Local projects continue to use
the local daemon. A Cloud lookup or save failure stays a Cloud error.

Cloud and local settings render one `ProjectSettingsEditor` for General and
Agents. It owns the draft, inline fields, role approval controls, reviewer rows,
validation, and debounced autosave. Separate adapters load and save through
the control plane or daemon and provide each project's supported capabilities.
Local readiness, scratch/workspace fields, orchestrator replacement, Issue
Intake, session prefix, and Cues retain their existing local behavior. Pending
writes disable the shared editor, and the dialog waits for completion before
closing. Save errors retain the draft and allow retry. Model catalogs come
from the existing agent discovery API, without a local project lookup or a
Cloud project ID. OpenCode uses the connected Cloud credential type as its
catalog scope. Custom Cloud model IDs remain editable when the local catalog
does not list them, and effort choices are limited to the Cloud API values.
Project roles carry approval (permissions) only. A cloud project has no fixed
worker or orchestrator agent, and the editor shows no agent, model, effort, or
mode for those roles. The desktop app chooses the agent when it starts a cloud
session: the first connected provider in the order claude-code, codex, cursor,
opencode unless the user picks another, and it always sends that harness
explicitly. Reviewers keep their own agent, model, effort, permissions, and
Cursor mode. Cloud saves explicit reviewer model and effort selections even when
the local catalog marks them as defaults, since Cloud runtime defaults may
differ. Cursor model and mode selections are independent; changing either
preserves the other. Codex and Claude effort can be selected without a model
override. Project reads and writes stay on the control plane.

The settings endpoint accepts `displayName`, `defaultBranch`, and `config`.
Repository identity is read-only. `config.worker: null` or
`config.orchestrator: null` removes that role's settings. Other explicit `null` values and unknown fields are rejected.
Config uses the same role structure as local project settings:

```json
{
  "worker": {"agentConfig": {"permissions": "auto"}},
  "orchestrator": {"agentConfig": {"permissions": "default"}},
  "reviewers": [{"harness": "claude-code", "agentConfig": {"model": "review-model", "effort": "high", "permissions": "auto"}}],
  "autoReview": true
}
```

Role `agent` and `agentConfig.model`, `mode`, and `effort` are deprecated and
ignored. They are still accepted and returned so older clients and stored
projects keep working, but new clients neither write nor read them.

Cloud currently supports one project reviewer. Model, effort, mode, and
permissions are passed to the selected harness. Effort is supported for Codex
and Claude Code; Cursor supports plan and ask modes. OpenCode uses its AO prompt
agent and has no separate mode control. Empty strings select harness or session
defaults; an empty `reviewers` array restores the session-agent reviewer.

PATCH merges nested role objects under a project row lock. Omitted fields,
including unrelated config such as sandbox templates and agent rules, survive.
Reviewer arrays replace the previous array. Legacy `workerAgent` and
`orchestratorAgent` fields are normalized to nested roles on reads and project
writes. An existing nested `agent` wins over its flat alias. Flat aliases are
never emitted or stored by new project writes and are rejected by settings PATCH.
Creation retries compare canonical configs against historical command payloads,
so a legacy flat request or omitted config still returns the original project.
The stored command payload stays unchanged, and different choices still conflict.

A new session takes its agent, model, mode, and effort from its own create
request. Role permissions from project config still apply as the session's
approval policy. Existing sessions retain their saved launch settings.

Each review run stores its effective reviewer config before launch. It starts a
fresh process and conversation with that harness's credentials and an isolated
credential directory. Claude reviewers load only their isolated user settings,
leaving workspace hooks active for the worker alone. Later settings changes do
not alter that run. Older runs without a snapshot retain their session agent.
Missing reviewer config uses the session agent, model, and permissions.

`autoReview` seeds each new worker session's automatic-review toggle, as local
projects do; missing means off. Afterwards the session's own toggle alone
decides whether automatic reviews start. Session `autoInjectReview` controls
delivery of review feedback to the worker; disabling injection does not disable
reviews. Issue Intake and session prefix are not
exposed because the Cloud settings launch path does not consume them.
Local Cues settings stay in local project settings because Cloud does not consume them.
