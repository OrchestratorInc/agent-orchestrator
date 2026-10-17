# Scope: CLI command for canonical repository migration

Status: read only design and migration scope. No code or AO project state was changed.

## Decision in one sentence

Add a common partial project update command, with canonical repository migration as its first use case. Do not make users use `ao project set-config`, because that command replaces the entire config and can erase unrelated settings.

## Proposed user surface

```text
ao project update <project-id> [field flags] [--dry-run] [--yes] [--json]
```

The first fields include `--canonical-repo-url <url>` and `--default-branch <branch>`. The command should cover the existing project settings surface over time, including session prefix, model and permission settings, worker and orchestrator agents, rules, environment values, symlinks, post-create commands, tracker intake, reviewers, container reap, and auto-review. Future fields can use the same command without creating one verb per setting. Clearing a field, if required later, should be an explicit field-level operation, not a separate top-level command.

The command must fetch the project through the daemon, display current settings and proposed changes, then require the project id as confirmation for a real write. `--yes` is required for noninteractive use. `--dry-run` validates the same typed patch and prints the exact diff without writing. Agents can run `ao project get <id> --json` first when they need current context. Optimistic compare-and-set is out of scope for this first version.

Suggested text output:

```text
project: agent-orchestrator
origin: https://github.com/Untrivial-ai/agent-orchestrator.git
canonical PR repository: https://github.com/Untrivial-ai/agent-orchestrator
new canonical PR repository: https://github.com/OrchestratorInc/agent-orchestrator
updated canonical repository for project agent-orchestrator
```

`--json` should expose the old and new values and the returned project read model. No Git remote is changed, no checkout is rebased, and no existing PR row is rewritten.

The dry-run result should use a machine-readable change list, for example:

```json
{
  "projectId": "agent-orchestrator",
  "dryRun": true,
  "changes": [
    { "path": "config.canonicalRepoURL", "from": "", "to": "https://github.com/OrchestratorInc/agent-orchestrator" },
    { "path": "config.defaultBranch", "from": "auto", "to": "main" }
  ]
}
```

Text output can render the same shape as `changes = { path: { from: ..., to: ... } }`. A no-op update returns an empty `changes` list and performs no write.

## Why a common partial mutation is required

Current CLI and daemon path:

| Layer | Current source or symbol | Consequence for this feature |
| --- | --- | --- |
| CLI registration | `backend/internal/cli/project.go`, `newProjectCommand` | Add `update` beside `set-config`; follow `usageError` for argument mistakes and existing `getJSON` plus `patchJSON` transport helpers. |
| Existing CLI config flag | `projectSetConfigOptions.canonicalRepoURL`, `buildProjectConfig` | `--canonical-repo-url` is embedded in a wholesale replacement. Keep it for compatibility, but document `project update --canonical-repo-url` as the safe field-level path. |
| CLI wire mirror | `projectConfig.CanonicalRepoURL`, `setConfigRequest` | Do not reuse this request for `update`. A partial request must not require the full config mirror. |
| HTTP route | `backend/internal/httpd/controllers/projects.go`, `ProjectsController.Register` | Add `PATCH /api/v1/projects/{id}/config` for typed field-level updates and dry-run diffs. The controller must decode strictly and call only the project manager. |
| Service contract | `backend/internal/service/project/service.go`, `Manager` | Add a partial update method such as `UpdateConfigFields(ctx, id, input)`. It loads the active row, validates fields against the stored origin, computes a typed diff, and writes only when the patch is not a dry run. |
| Service request DTO | `backend/internal/service/project/dto.go` | Add a typed partial input with optional field values and `DryRun bool`. Pointers distinguish clear from omitted. Return a typed change list so CLI text and JSON output agree. |
| Durable field | SQLite `projects.config` JSON, key `canonicalRepoURL` | There is no new SQL column. The existing `repo_origin_url` remains checkout origin and must never be overwritten by this command. |
| Atomic persistence | `backend/internal/storage/sqlite/store/project_store.go`, `SetProjectPermissions` pattern | Add a transactionally serialized focused read-modify-write. Do not implement read, merge, then `UpsertProject` in the service because it can clobber a concurrent config update. Add a store interface method and a query or transaction helper. |
| API contract | `backend/internal/httpd/apispec/specgen/build.go`, `backend/internal/httpd/apispec/openapi.yaml` | Register the operation and named request/response schemas. Run `npm run api`, committing OpenAPI and frontend `schema.ts` together with implementation. |
| Generated frontend contract | `frontend/src/api/schema.ts` | Generated only. No renderer change is needed for a CLI-only feature. |
| Skill documentation | `backend/internal/skillassets/using-ao/commands/project.md`, `docs/cli/README.md` | Describe the safe command and retain the warning that `set-config` replaces all fields. |

## Existing semantics that the command must preserve

### Origin versus canonical repository

`domain.ProjectRecord.RepoOriginURL` is persisted in `projects.repo_origin_url`. It identifies the checkout and remains the push and workspace root repository. Workspace child origins are in `workspace_repos.repo_origin_url` and are independently trusted for full child PR URLs.

`domain.ProjectConfig.CanonicalRepoURL` is a single explicit upstream allowlist for PR claims. It is not a replacement origin and is not a workspace child allowlist. `promptProjectContext` in `backend/internal/session_manager/manager.go` passes only `RepoOriginURL` into worker prompts, so changing canonical trust does not change worker checkout instructions or prompts.

### Validation and normalization

`backend/internal/domain/repository_identity.go` currently parses repository URLs and compares provider, host, namespace, and name. `ValidateCanonicalRepository` requires HTTPS, rejects credentials, query, fragment, PR or MR paths, and requires the same provider and host as origin. Explicit ports are part of the host identity. GitHub repositories must have exactly owner and repository path segments.

The current code normalizes only for comparison: host is lowercased and `.git` is removed while parsing. The stored string is not rewritten. The new command should validate with the same function and normalize only harmless spelling differences before storing: HTTPS, no trailing slash, no `.git`, and the origin's exact authority. Do not silently follow redirects, change owner or repository case, convert `www.github.com` or `api.github.com` to `github.com`, or infer an upstream from any Git remote. If normalization is deferred for compatibility, return the exact accepted string and add tests that comparison remains case insensitive where GitHub permits it.

Scratch projects must continue to reject canonical URLs. A missing or archived project returns `PROJECT_NOT_FOUND`. Invalid URL or origin mismatch returns `INVALID_PROJECT_CONFIG` or a new specific `INVALID_CANONICAL_REPOSITORY` code, consistently documented in the CLI.

### Claim and PR attachment behavior

`backend/internal/service/session/claim_pr.go` chooses `CanonicalRepoURL` before origin for numeric PR references. `requireProjectPRRepository` accepts a PR matching origin, canonical, or a registered workspace child origin. Full URLs are parsed by the CLI and daemon; numeric references are expanded by `backend/internal/cli/pr_ref.go` and `session.NormalizePRRef` against canonical when present.

Therefore, after setting the canonical URL to `https://github.com/OrchestratorInc/agent-orchestrator`, `ao session claim-pr <session-id> 6152` and `ao spawn --claim-pr 6152` resolve to PR 6152 in OrchestratorInc. A full old Untrivial-ai PR URL remains attachable when the project's persisted origin is still Untrivial-ai, because origin remains an accepted identity. Existing claimed PR rows, their URLs, checks, comments, and worktrees are not changed by this setting.

If an operator also changes the checkout remote and AO's persisted `RepoOriginURL` becomes OrchestratorInc, the old Untrivial-ai URL is no longer accepted by the current single-canonical model. Do not combine remote replacement with this command. If old URLs must remain attachable after an origin migration, the product needs an explicit repository alias list or stable provider repository identity, which is a separate schema and claim-policy change. A second canonical URL must not be smuggled into this command.

## API shape and authorization

Recommended request:

```json
{
  "canonicalRepoURL": "https://github.com/OrchestratorInc/agent-orchestrator",
  "defaultBranch": "main",
  "dryRun": false
}
```

For clearing a supported field, send its explicit null value. The response should use the same change-list shape for every field, for example:

```json
{
  "project": { "id": "agent-orchestrator", "repo": "https://github.com/Untrivial-ai/agent-orchestrator.git", "config": { "canonicalRepoURL": "https://github.com/OrchestratorInc/agent-orchestrator" } },
  "dryRun": false,
  "changes": [
    { "path": "config.canonicalRepoURL", "from": "https://github.com/Untrivial-ai/agent-orchestrator", "to": "https://github.com/OrchestratorInc/agent-orchestrator" },
    { "path": "config.defaultBranch", "from": "auto", "to": "main" }
  ],
  "project": { "id": "agent-orchestrator", "repo": "https://github.com/Untrivial-ai/agent-orchestrator.git", "config": { "canonicalRepoURL": "https://github.com/OrchestratorInc/agent-orchestrator", "defaultBranch": "main" } }
}
```

The loopback listener is already the unauthenticated local control boundary, so the CLI needs no new credential. The route must remain under the normal project API router. If exposed through the opt-in LAN listener, it inherits the existing bearer-password middleware. Do not add a new network listener or a URL-based bypass.

## Exact implementation file set

Code and generated contract changes:

1. `backend/internal/cli/project.go` and `backend/internal/cli/project_test.go` for the common update command, field flags, confirmation, dry-run output, JSON output, and daemon error propagation.
2. `backend/internal/service/project/dto.go`, `service.go`, `store.go`, and service tests for the manager method, validation, scratch and archived behavior.
3. `backend/internal/storage/sqlite/store/project_store.go`, `queries/projects.sql`, generated `backend/internal/storage/sqlite/gen/projects.sql.go`, and store tests for serialized partial config mutation.
4. `backend/internal/httpd/controllers/projects.go`, `projects_test.go`, `backend/internal/httpd/apispec/specgen/build.go`, `backend/internal/httpd/apispec/openapi.yaml`, and `frontend/src/api/schema.ts` for the route and contract.
5. `backend/internal/domain/repository_identity.go` and `repository_identity_test.go` only if normalization is added. Existing validation tests are the baseline.
6. `docs/cli/README.md` and `backend/internal/skillassets/using-ao/commands/project.md` for operator documentation. No worker prompt change is needed because prompts intentionally show origin, not canonical trust.

No changes are needed to GitHub API adapters or provider clients. They receive a normalized `ports.SCMPRRef` after claim validation. No changes are needed to release provenance checks or release URLs for this CLI feature. Those are separate repository-link constants and should be migrated in a separate inventory so a CLI patch does not mix product links with claim authorization.

## Focused test matrix

1. CLI sends only the partial request to the PATCH route, preserves a fixture containing env, reviewers, agent modes, and container settings, and never calls `set-config`.
2. CLI rejects missing project id or URL as exit code 2, confirms replacement by project id, supports `--yes`, and makes `--dry-run` perform no mutation.
3. CLI prints and returns daemon envelopes for not found, invalid config, no-op, and transport failure with existing exit code conventions.
4. Service accepts a GitHub fork or organization transfer on the same host, rejects cross host, cross provider, PR URL, credentials, query, fragment, and invalid origin, and rejects scratch projects.
5. Store tests prove partial updates preserve every other JSON config key and are serialized with concurrent writes.
6. Controller and API parity tests cover strict JSON, status codes, named schema registration, and generated spec drift.
7. Claim tests cover numeric `6152` resolving to OrchestratorInc, a full old Untrivial-ai URL still matching an unchanged origin, and rejection of an unrelated repository.
8. Workspace tests prove the command changes only root project config and does not modify `workspace_repos` child origins.
9. Regression test confirms already claimed old PR rows remain unchanged and do not need session restart.

## Safe ordered migration for PR 6152

1. Inspect the target project with `ao project get <id> --json`. Record `project.repo` and `project.config.canonicalRepoURL`; do not edit the Git remote.
2. Confirm the root origin is still the old Untrivial-ai repository. If it is already OrchestratorInc, stop and decide whether old URL compatibility requires a future alias feature.
3. Run `ao project update <id> --canonical-repo-url https://github.com/OrchestratorInc/agent-orchestrator --dry-run` and review the diff.
4. Apply the same command with `--yes`. This changes only `projects.config.canonicalRepoURL`.
5. Verify `ao project get <id> --json` shows the new canonical URL and the unchanged origin, workspace children, and unrelated config.
6. Attach PR 6152 using `ao session claim-pr <session-id> 6152` or spawn with `--claim-pr 6152`. Verify the resulting PR URL is `https://github.com/OrchestratorInc/agent-orchestrator/pull/6152`.
7. If an existing old Untrivial-ai PR must be attached, pass its full URL. It should match the unchanged origin. Do not rely on numeric resolution for old PR numbers after the canonical switch.
8. Run the focused CLI, controller, service, store, and claim tests, then the normal backend and API generation checks before publishing an implementation.

## One-time operator actions versus code changes

One-time operator action: update the registered project's canonical config with `ao project update`, leaving Git remotes and existing PR rows alone. Repeat for each local AO project that represents this repository. This is not a repository migration and does not update another operator's machine.

Required code change: ship the dedicated command, daemon route, atomic persistence method, API contract, tests, and docs. Do not add a migration that rewrites every existing canonical URL. Existing explicit values are user intent and cannot be safely guessed. Migration 0126 already initializes a missing JSON key without discovering remotes; it should remain unchanged.

Separate code or operator inventories: README, translation, release, provenance, GitHub API, cloud, and UI links that still contain Untrivial-ai. They are not required to make PR 6152 attachable and should not be mixed into this CLI feature unless the release owner requests a repository-wide URL cutover.
