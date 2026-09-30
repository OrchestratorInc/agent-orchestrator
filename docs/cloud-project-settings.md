# Cloud project settings

Cloud projects load from `GET /orgs/{orgId}/projects/{projectId}` and save through
`PATCH /orgs/{orgId}/projects/{projectId}/settings`. Local projects continue to use
the local daemon. A Cloud lookup or save failure stays a Cloud error.

The settings endpoint accepts `displayName`, `defaultBranch`, and `config`.
Repository identity is read-only. Unknown fields and explicit `null` values are
rejected. Config uses the same role structure as local project settings:

```json
{
  "worker": {"agent": "codex", "agentConfig": {"model": "worker-model", "effort": "high", "permissions": "auto"}},
  "orchestrator": {"agent": "claude-code", "agentConfig": {"model": "orchestrator-model"}},
  "reviewers": [{"harness": "claude-code", "agentConfig": {"model": "review-model", "effort": "high", "permissions": "auto"}}],
  "autoReview": true
}
```

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

Role defaults are stamped into newly created sessions. An explicit session
harness wins; matching role config supplies its model, mode, effort, and
permissions, and an explicit session model overrides the project model.
Existing sessions retain their saved launch settings.

Each review run stores its effective reviewer config before launch. It starts a
fresh process and conversation with that harness's credentials and an isolated
credential directory. Later settings changes do not alter that run. Older runs
without a snapshot retain their session agent. Missing reviewer config uses the
session agent, model, and permissions; missing `autoReview` enables reviews.

`autoReview` controls whether new AO review runs start. Session
`autoInjectReview` controls delivery of review feedback to the worker; disabling
injection does not disable reviews. Issue Intake and session prefix are not
exposed because the Cloud settings launch path does not consume them.
Local Cues settings stay in local project settings because Cloud does not consume them.
