# Session Delivery Card Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the session Summary tab accurately describe and safely advance work from local Git changes through a session-owned pull request.

**Architecture:** Extend the existing session workspace read model with a daemon-derived delivery classification rather than recomputing Git state in React. Route all delivery mutations through one session-service command boundary guarded by workspace version, per-session serialization, repository/branch ownership checks, and a provider-neutral PR publication port; adapters perform provider API calls while the service performs confined, non-force Git operations and durable PR claiming.

**Tech Stack:** Go 1.24 daemon/services/adapters, Git CLI through the existing bounded process helpers, chi/OpenAPI, React 19, TanStack Query, Vitest/Testing Library, Electron desktop verification.

**Spec:** `docs/superpowers/specs/2026-09-21-session-delivery-card-design.md`

## Global Constraints

- Keep the loopback listener and renderer/daemon trust boundary unchanged; the renderer executes no Git or provider command.
- Never force-push, reset, clean, discard, rebase, overwrite local work, bypass hooks, or guess a PR destination.
- Require an explicit user action for every commit, push, and PR creation; require a non-empty editable message before committing.
- Validate the session workspace, repository, branch, target PR ownership, and displayed workspace revision immediately before mutation.
- Serialize delivery mutations per session and preserve partial success as the next authoritative delivery state.
- Use the existing API error envelope and request ID behavior; never expose credentials, credential-bearing URLs, commit bodies, or file contents through telemetry.
- Keep commit `030f41b16` and its Scratchpad harness-default change entirely outside this branch.

## Review Focus

- A dirty worktree that changes after confirmation must return a stale-revision conflict before `git add` or `git commit`; pin this in the service mutation tests.
- A commit hook failure must leave every file and index entry intact and report the commit stage; pin this with a local Git fixture and failing hook.
- A successful commit followed by push rejection must not create another commit on retry; pin the transition to push-only in service and renderer tests.
- A successful push followed by a timed-out create-PR response must discover the existing open PR before retrying creation; pin this in provider/service reconciliation tests.
- A stored PR whose repo or source branch differs from the session workspace must be blocked without running Git; pin this in delivery classification and action tests.

---

### Task 1: Authoritative Delivery State

**Files:**
- Create: `backend/internal/service/session/delivery.go`
- Create: `backend/internal/service/session/delivery_test.go`
- Modify: `backend/internal/service/session/workspace_files.go`
- Modify: `backend/internal/httpd/controllers/dto.go`
- Modify: `backend/internal/httpd/controllers/sessions.go`
- Modify: `backend/internal/httpd/controllers/sessions_test.go`
- Modify: `backend/internal/httpd/apispec/specgen/build.go`
- Regenerate: `backend/internal/httpd/apispec/openapi.yaml`
- Regenerate: `frontend/src/api/schema.ts`

**Interfaces:**
- Produces: `DeliveryStatus` on `WorkspaceFiles`, including `state`, `action`, `blockedReason`, `workspaceVersion`, branch/repository facts, commit/change counts, and optional target PR identity.
- Consumes: existing `WorkspaceFiles`, `domain.PullRequest`, session/project records, and SCM repository parsing.

- [ ] Write table tests for all seven experience states: empty, uncommitted/no PR, committed/no PR, dirty/existing PR, ahead/existing PR, synchronized PR, and blocked/indeterminate. Include detached HEAD, no remote, behind/diverged, scratch/workspace projects, multiple open PRs, and repo/branch mismatch.
- [ ] Run `cd backend && go test ./internal/service/session -run 'Test.*Delivery' -count=1`; confirm the new symbols/state assertions fail because delivery classification is absent.
- [ ] Implement pure classification from the same snapshot used by `ListWorkspaceFiles`; derive actions only when repository, branch, remote, upstream/PR head, and ownership facts are unambiguous.
- [ ] Add the delivery DTO to `ListWorkspaceFilesResponse`, map it in `workspaceFilesResponse`, and add controller wire tests asserting stable enum values and blocker text.
- [ ] Run focused service/controller tests, then `npm run api`; confirm generated Go OpenAPI and frontend types are current.
- [ ] Commit with `feat: classify session delivery state`.

### Task 2: Provider-Neutral PR Publication and Reconciliation

**Files:**
- Modify: `backend/internal/ports/scm_actions.go`
- Create: `backend/internal/adapters/scm/github/publish_action.go`
- Create: `backend/internal/adapters/scm/github/publish_action_test.go`
- Create: `backend/internal/adapters/scm/gitlab/publish_action.go`
- Create: `backend/internal/adapters/scm/gitlab/publish_action_test.go`
- Create: `backend/internal/adapters/scm/multi/publisher.go`
- Create: `backend/internal/adapters/scm/multi/publisher_test.go`
- Modify: `backend/internal/daemon/daemon.go` or the current daemon composition file that constructs SCM adapters.

**Interfaces:**
- Produces: `SCMPullRequestPublisher.ReconcileOrCreatePullRequest(ctx, SCMPublishRequest) (SCMPublishResult, error)` where the request contains normalized repo, exact source/target branch, head SHA, title, and bounded body; the result contains canonical PR ref/observation and whether it already existed.
- Consumes: existing authenticated provider clients and `SCMRepo` parsing/routing.

- [ ] Write adapter tests proving existing-open-PR reconciliation, creation, source/target filtering, auth/not-found/conflict mapping, retry after an uncertain response, and provider routing.
- [ ] Run `cd backend && go test ./internal/adapters/scm/... -run 'Test.*Publish|Test.*Reconcile' -count=1`; confirm failures identify the missing publication port/implementations.
- [ ] Add the provider-neutral request/result types and implement GitHub/GitLab API calls using existing authenticated clients, response bounds, error mapping, and canonical normalized observations.
- [ ] Implement multi-provider routing by normalized provider key and wire the composite publisher into daemon composition.
- [ ] Run `cd backend && go test ./internal/adapters/scm/... -count=1`.
- [ ] Commit with `feat: add retry-safe pull request publication`.

### Task 3: Guarded Delivery Mutations

**Files:**
- Create: `backend/internal/service/session/delivery_actions.go`
- Create: `backend/internal/service/session/delivery_actions_test.go`
- Modify: `backend/internal/service/session/service.go`
- Modify: `backend/internal/service/session/claim_pr.go` only if a shared claim helper is needed after a normalized creation result.
- Modify: `backend/internal/httpd/controllers/dto.go`
- Modify: `backend/internal/httpd/controllers/sessions.go`
- Modify: `backend/internal/httpd/controllers/sessions_test.go`
- Modify: `backend/internal/httpd/apispec/specgen/build.go`
- Regenerate: `backend/internal/httpd/apispec/openapi.yaml`
- Regenerate: `frontend/src/api/schema.ts`

**Interfaces:**
- Produces: `AdvanceDelivery(ctx, sessionID, DeliveryActionInput) (DeliveryActionResult, error)` for `publish_pr`, `commit_and_publish_pr`, `commit_and_push`, and `push`; every input carries `expectedWorkspaceVersion`, and commit actions carry `commitMessage`.
- Consumes: Task 1 classification, Task 2 publisher, existing PR claimer/store, and a focused Git command runner injected for service tests.

- [ ] Write service tests with local Git fixtures/fakes covering clean publish, already-pushed PR creation, duplicate PR reconciliation, commit+push, push-only, commit success/push failure, push success/create failure, stale revision, concurrent action rejection, missing remote/auth, detached/diverged branch, ambiguous/mismatched PR, non-fast-forward, failing hooks, empty message, inactive/foreign workspace, and preservation of dirty files/index on every failure.
- [ ] Run `cd backend && go test ./internal/service/session -run 'TestAdvanceDelivery' -count=1`; verify the tests fail for the missing command.
- [ ] Implement per-session keyed locking, reload-and-compare workspace version under the lock, exact current branch/remote validation, `git add --all`, normal `git commit`, and refspec-limited non-force `git push`; after each irreversible stage invalidate/reload authoritative state.
- [ ] Reconcile open PRs before creation, claim the normalized created/found PR durably, and return stage-aware results/errors so commit-only and push-only partial successes are accurately recoverable.
- [ ] Add `POST /api/v1/sessions/{sessionId}/delivery` with typed request/response DTOs, validation, OpenAPI registration, and HTTP tests for success, validation, conflicts, concurrent rejection, partial-stage errors, envelopes, and request IDs.
- [ ] Run focused service/controller tests and `npm run api`.
- [ ] Commit with `feat: advance session delivery safely`.

### Task 4: Delivery Card and Mutation UX

**Files:**
- Create: `frontend/src/renderer/components/SessionDeliveryCard.tsx`
- Create: `frontend/src/renderer/components/SessionDeliveryCard.test.tsx`
- Create: `frontend/src/renderer/hooks/useSessionDelivery.ts`
- Create: `frontend/src/renderer/hooks/useSessionDelivery.test.tsx`
- Modify: `frontend/src/renderer/components/SessionInspector.tsx`
- Modify: `frontend/src/renderer/components/SessionInspector.test.tsx`
- Modify: `frontend/src/renderer/hooks/useSessionWorkspaceFiles.ts`
- Modify: `frontend/src/renderer/i18n/en.json`
- Modify: other checked-in locale files only through the repository's established fallback convention.

**Interfaces:**
- Produces: a compact summary card driven solely by `delivery` and workspace summary fields; its mutation hook posts Task 3 inputs and invalidates workspace, session, SCM summary, review, and CI queries.
- Consumes: generated delivery schemas, existing Files-tab navigation callback, query keys, button/input primitives, live workspace invalidation, and renderer telemetry.

- [ ] Write renderer tests for every classified state, counts/subject rendering, Files navigation, targeted PR labels, editable/non-empty commit confirmation, keyboard focus/restoration, accessible live status, blocker presentation, pending duplicate prevention, partial-success recovery, and successful transition to the existing PR card.
- [ ] Run `npm --prefix frontend test -- --run src/renderer/components/SessionDeliveryCard.test.tsx src/renderer/components/SessionInspector.test.tsx`; confirm failures are caused by the absent card/hook.
- [ ] Implement the mutation hook with intent/success/failure/retry telemetry containing only action and categorical failure stage; reconcile query state after both success and failure.
- [ ] Implement the card using existing inspector density/primitives: one primary next step, progressive commit-message confirmation, explicit target PR, compact file/line/commit facts, Files link, blocked recovery copy, and no redundant action when synchronized.
- [ ] Replace only the Summary empty-PR branch with the delivery card while preserving synchronized PR CI/review/merge cards and the Reviews-tab empty state.
- [ ] Run focused renderer tests, frontend typecheck, and frontend build.
- [ ] Commit with `feat: add session delivery card`.

### Task 5: Integration, Accessibility, and Visual Verification

**Files:**
- Modify: focused backend/frontend test files discovered by full-suite failures only when directly related.
- Modify: `docs/STATUS.md` if shipped-feature inventory requires the new capability.

**Interfaces:**
- Consumes: all prior task contracts.
- Produces: verified end-to-end delivery behavior without publishing the branch or opening a repository PR.

- [ ] Run `cd backend && go test ./internal/service/session ./internal/httpd/... ./internal/adapters/scm/... -count=1` and fix only delivery-related failures.
- [ ] Run `npm run lint`, `npm run frontend:typecheck`, `npm --prefix frontend test`, and `npm --prefix frontend run build`; regenerate API artifacts once more and require `git diff --exit-code` after regeneration.
- [ ] Run `cd backend && go test -race ./...`; record any platform/credential gap exactly rather than labeling it passed.
- [ ] Use the AO desktop development workflow with isolated scratch `AO_DATA_DIR`; create throwaway local repositories/sessions for ready-to-publish, dirty confirmation, PR-with-ahead-commits, synchronized PR, and actionable failure states.
- [ ] At narrow and normal inspector widths, verify keyboard-only confirmation, visible focus, live pending/error announcements, text zoom wrapping, Files navigation, and that no renderer Git/provider process is invoked.
- [ ] Inspect `git log --oneline --all --ancestry-path 030f41b16..HEAD` and `git diff origin/main...HEAD` to confirm the Scratchpad fix was not incorporated; review all changed files for scope.
- [ ] Send AO a final evidence report with commands/results and blockers. Do not push or open a PR.
- [ ] Commit any final test/docs adjustments with `test: verify session delivery workflow`.
