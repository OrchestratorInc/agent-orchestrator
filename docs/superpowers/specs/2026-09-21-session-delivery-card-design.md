# Session Delivery Card Design

**Date:** 2026-09-21  
**Status:** Ready for implementation planning  
**Scope:** Desktop session inspector, daemon-backed Git/SCM actions, and their API contracts

## Summary

The session inspector currently treats the absence of a pull request as an empty state, even when the session has completed useful local work. A user can see commits, changed files, and line counts in the Files view while the Summary view says only “No pull request opened yet.” There is no visible next action for publishing that work or for sending later local changes to the pull request already owned by the session.

Replace this dead end with a delivery-oriented summary that represents the full path from local work to pull request:

1. Show a high-level summary of local changes before a pull request exists.
2. Let the user publish eligible committed work and create a pull request from that summary.
3. When the session already owns a pull request, show whether local commits or uncommitted changes still need to reach it.
4. Let the user commit and/or push those changes to the session’s pull request through an explicit, safe action.
5. Preserve the existing rich pull-request status experience once local work is fully published.

The result should make the inspector the obvious place to understand and advance a session’s delivery state. It must not require the user to infer delivery readiness by cross-referencing the Files tab, terminal output, and an empty pull-request section.

## Product Intent

### User problem

After an agent finishes coding, the user wants to know:

- What changed?
- Has it been committed?
- Has it been pushed?
- Is there a pull request?
- What is the next safe action?

AO already knows much of this state, but presents it in separate places. The Summary view is authoritative for pull-request status but omits pre-PR Git state. The Files view exposes commits and line counts but does not advance the publishing workflow. This makes completed work appear unfinished or invisible and forces users back into the terminal for routine Git operations.

### Desired outcome

At a glance, a user should understand the session’s current delivery stage and have one clear primary action when AO can safely advance it.

Examples include:

- Local work exists but is uncommitted → explain that it needs a commit before publishing.
- Commits exist locally but no PR exists → offer to create a pull request.
- A PR exists and the branch has newer local commits → offer to push them to that PR.
- A PR exists and the worktree is dirty → offer an explicit commit-and-push flow.
- The branch and PR are synchronized → show the existing review, CI, and merge state.

## Design Principles

### One continuous delivery story

“Local changes” and “pull request” are not separate concepts in this surface. They are sequential states of the same work. The presentation should evolve from local progress into pull-request progress instead of switching from an empty placeholder to an unrelated card.

### State before action

The UI must describe what AO knows before asking the user to mutate anything. Counts, commit information, synchronization state, and target PR should be visible enough that the action is understandable.

### One primary next step

When an action is available, the card should emphasize the safest next step rather than exposing a toolbar of Git commands. Secondary navigation to Files or the provider is acceptable, but the main workflow should remain obvious.

### Explicit permanent history

AO must not silently create commits with an invisible or non-editable message. A commit-producing action should give the user a compact confirmation step with a sensible, editable message. The interaction may remain lightweight, but it must make the permanent history operation clear.

### Never guess the destination

AO may push to a pull request only when the session has an unambiguous association with that PR and branch. If the destination is missing, conflicting, or ambiguous, the UI must explain the blocker and avoid offering a misleading one-click action.

### Daemon-owned mutations

The renderer must not execute Git or provider CLI commands directly. Any commit, push, or PR-creation workflow belongs behind daemon/service boundaries so AO can enforce workspace ownership, validate state, serialize operations, preserve error envelopes and request IDs, and publish durable state changes.

### Creative implementation freedom

This specification defines behavior and safety constraints, not an exact component tree, endpoint naming scheme, service decomposition, or card layout. The implementation owner should use existing repository patterns and may choose the cleanest architecture that satisfies the requirements.

## Experience Model

The existing section may retain a pull-request-oriented title once a PR exists. Before that point, the product may use “Changes,” “Delivery,” “Ready to publish,” or another concise label that communicates work in progress. Exact wording and visual composition are intentionally left to product judgment.

The UI should distinguish at least the following observable states.

### 1. No local work and no pull request

The session has no meaningful changed files or commits relative to its comparison base.

Expected behavior:

- Show a quiet, honest empty state.
- Do not show a create-PR action when there is nothing to publish.
- Avoid implying that the user has done something wrong.

### 2. Uncommitted local work and no pull request

The worktree contains staged, unstaged, or untracked changes, with no publishable local commit.

Expected behavior:

- Summarize changed files and additions/deletions when available.
- Make the uncommitted status clear.
- Offer a path that commits the work and continues toward PR creation, or a clear commit-first action followed by PR creation.
- Before creating a commit, present an editable proposed commit message.
- Never discard or overwrite local changes as part of this flow.

### 3. Local commits and no pull request

One or more commits exist relative to the comparison base and no session-owned PR exists.

Expected behavior:

- Show commit count, file count, additions/deletions, and a useful commit summary.
- Offer a primary action to publish the branch and create a pull request.
- The flow should be effectively one action when prerequisites are satisfied, while still surfacing meaningful confirmation or failure states.
- If the branch is already pushed but no PR exists, create the PR without redundantly pushing.

### 4. Pull request exists and uncommitted work remains

The session owns a PR, and staged, unstaged, or untracked changes exist beyond the PR head.

Expected behavior:

- Keep the PR identity visible.
- Explain that local changes are not yet included in the PR.
- Offer a commit-and-push action targeted explicitly at that PR.
- Require an editable commit message before committing.
- Refresh both workspace and PR state after success.

### 5. Pull request exists and local commits are not pushed

The worktree is clean, but local branch commits are ahead of the configured upstream or known PR head.

Expected behavior:

- State how many commits are waiting to update the PR.
- Offer a push action that names the target PR.
- Do not ask for a commit message because no new commit is being created.

### 6. Pull request and branch are synchronized

There are no local changes or unpushed commits relevant to the PR.

Expected behavior:

- Preserve the existing PR card behavior for CI, reviews, mergeability, comments, and merge actions.
- Do not add a disabled or redundant push action.

### 7. Blocked or indeterminate delivery state

Examples include no remote, detached HEAD, missing provider authentication, ambiguous PR ownership, branch divergence, merge/rebase requirements, unsupported provider behavior, or a workspace whose state changes during the operation.

Expected behavior:

- Do not present the normal action as though it will succeed.
- Explain the blocker in plain language and provide the most useful recovery route available.
- Preserve all local work.
- Do not automatically force-push, reset, discard, rebase, or resolve conflicts.

## Functional Requirements

### Delivery summary

The Summary view must consume authoritative daemon state rather than reconstructing Git facts from renderer heuristics.

The summary should expose, when applicable:

- Whether meaningful local changes exist.
- Whether changes are committed or uncommitted.
- Commit count and at least one useful commit subject.
- Changed-file count.
- Aggregate additions and deletions.
- Whether local commits are ahead of their upstream or PR head.
- The associated PR identity and state, if one exists.
- Whether AO can safely offer the next delivery action.
- A reason when it cannot.

The existing workspace-files response already provides commits, file sections, summary counts, and optional ahead/behind facts. The implementation may extend that contract, introduce a purpose-built delivery-status response, or compose existing service results, provided the daemon remains authoritative and the renderer receives a coherent state.

### Create pull request

When eligible, the user can ask AO to publish the session branch and create a pull request.

The workflow must:

- Validate that the session is active/eligible and owns the target workspace.
- Validate repository, branch, remote, and provider prerequisites before mutation.
- Avoid duplicate PR creation if an equivalent open PR already exists.
- Push only the intended session branch.
- Create the PR through the configured provider integration or an established repository mechanism.
- Associate the resulting PR with the session using the existing durable ownership model.
- Return enough structured information for the UI to transition immediately into PR state.
- Be safe to retry after uncertain network outcomes without creating duplicate PRs.

The worker may choose how title and body suggestions are produced. At minimum, the resulting PR should have a meaningful title, a concise summary, and tests/verification information when that information is available. The user should not be forced through a large form for the common path.

### Commit and push to an existing PR

When a session-owned PR exists and local changes remain, the user can advance those changes to that PR.

The workflow must:

- Name or otherwise clearly identify the target PR before confirmation.
- Require a non-empty commit message when creating a commit.
- Include staged, unstaged, and untracked session-worktree changes that the user confirms, following existing workspace safety rules.
- Refuse to operate if the session-to-PR branch relationship is not unambiguous.
- Avoid pushing unrelated branches or repositories.
- Surface non-fast-forward, authentication, hooks, and provider failures without losing work.
- Refresh workspace, session, SCM summary, CI, and review facts after success.

The implementation may provide one combined “Commit & push” operation or a short staged interaction, as long as the common path is concise and failures are recoverable.

### Push existing commits to an existing PR

When commits already exist and no uncommitted work remains, the UI should offer a direct push without presenting commit controls.

The operation must validate the same ownership and target constraints as commit-and-push and must not force-push automatically.

### State refresh and concurrency

Git state can change between display and action. Mutation requests must carry or validate an appropriate workspace/branch revision so stale actions fail safely rather than acting on a different state than the user reviewed.

Only one delivery mutation for a session/repository should execute at a time. While an action is pending, the UI should prevent duplicate submissions and show progress. After completion or failure, it should reconcile with daemon state rather than relying solely on optimistic assumptions.

## Safety and Authorization

- Creating a PR, creating a commit, and pushing are explicit user-triggered external mutations.
- No operation may use force push by default.
- No operation may discard, reset, clean, or overwrite local work.
- No operation may push a branch based only on a guessed PR number or matching branch name.
- Provider credentials and Git authentication remain outside renderer state and error messages.
- Errors must use the existing API envelope conventions and preserve request IDs.
- Hooks and repository policies must be respected; hook failures are reported, not bypassed silently.
- If a push succeeds but PR creation has an uncertain outcome, retry/reconciliation must first discover whether the PR now exists.
- If a commit succeeds but push fails, the UI must accurately transition to “committed, not pushed,” allowing a safe retry without creating a duplicate commit.

## Information Architecture and Interaction

The Summary tab is the primary home for delivery state. The Files tab remains the detailed inspection surface.

The Summary experience should:

- Show a compact overview rather than duplicate the full file explorer.
- Provide a direct path to inspect changes in Files before publishing.
- Keep local-delivery status and PR status visually connected.
- Use progressive disclosure for commit message, PR metadata, errors, and advanced recovery.
- Avoid showing session policies above the primary delivery action when doing so obscures the next step.

Exact hierarchy, card styling, icons, button labels, and whether the section title changes across states are left to the implementer. The completed experience should remain consistent with existing inspector components and density.

## Accessibility Requirements

- Every delivery action must have a clear accessible name that includes its effect; target PR identity should be included where relevant.
- Status must not rely on color alone.
- Pending, success, and failure transitions must be announced to assistive technology using existing application patterns.
- Confirmation controls must be keyboard accessible and have deterministic focus placement and restoration.
- Commit-message validation and mutation failures must be associated with the relevant control and remain visible long enough to act on.
- Compact controls must retain adequate target sizes and visible focus states.
- The experience must remain understandable at narrow inspector widths and under text zoom.

## Error and Recovery Experience

Errors should preserve the user’s mental model of what completed and what did not.

Examples:

- Commit created; push failed → show the new commit as waiting to push and offer retry.
- Push completed; PR creation failed → show the branch as published and offer PR creation retry.
- PR actually created despite a timeout → reconcile and show the PR rather than creating another.
- Authentication missing → explain which capability is unavailable and route to the established setup surface where possible.
- Non-fast-forward/diverged branch → explain that automatic push is unsafe; do not force-push.
- Worktree changed after confirmation → reject the stale operation and ask the user to review the updated summary.

Generic “Something went wrong” messaging is insufficient when AO can identify the failed stage.

## Telemetry

Add renderer intent and outcome telemetry consistent with existing conventions. Events should distinguish at least:

- Create-PR requested, succeeded, and failed.
- Commit-and-push requested, succeeded, and failed.
- Push-only requested, succeeded, and failed.
- Recovery/retry action selected.

Telemetry must not include commit bodies, file contents, credentials, remote URLs containing secrets, or other sensitive repository data. Stable categorical failure stages are preferable to raw error text.

## Testing Strategy

Implementation should be test-driven and cover behavior at each boundary.

### Service and adapter tests

Cover:

- Eligibility and state classification.
- Clean branch publication and PR creation.
- Already-pushed branch PR creation.
- Duplicate/open-PR reconciliation.
- Commit followed by successful push.
- Commit success followed by push failure.
- Push success followed by uncertain PR-creation outcome.
- Missing remote/auth/provider prerequisites.
- Ambiguous or mismatched PR ownership.
- Non-fast-forward rejection.
- Stale workspace revision rejection.
- Concurrent mutation rejection or serialization.
- Preservation of uncommitted work on every failure path.

Use fakes and local Git fixtures; do not add live network dependencies to ordinary tests.

### HTTP/API tests

Cover successful responses, validation failures, durable error envelopes, request IDs, and retry-safe outcomes. Regenerate and commit the OpenAPI specification and frontend schema for contract changes.

### Renderer tests

Cover every experience-model state, including:

- Summary counts and commit subjects agree with daemon facts.
- Correct primary action appears for each state.
- No action appears when nothing is publishable.
- Commit confirmation requires a valid message.
- Target PR is visible for update actions.
- Pending actions cannot be submitted twice.
- Partial success transitions into the correct recoverable state.
- Successful PR creation replaces pre-PR delivery state with the PR card.
- Files navigation remains available.
- Keyboard focus and accessible status behavior.

### Visual verification

Exercise the real desktop inspector at representative widths for:

- Ready-to-create-PR state.
- Uncommitted changes with commit confirmation.
- Existing PR with changes waiting to push.
- Fully synchronized PR.
- At least one actionable failure state.

## Acceptance Criteria

The work is complete when all of the following are true:

1. A session with committed changes and no PR no longer shows only “No pull request opened yet.”
2. The Summary view shows a meaningful high-level commit/change summary consistent with the Files view.
3. An eligible session can publish its branch and create a PR from the inspector without using the terminal.
4. The created PR becomes durably associated with the session and appears in the existing PR experience.
5. A session-owned PR with unpushed commits offers a direct, clearly targeted push action.
6. A session-owned PR with uncommitted work offers an explicit commit-and-push flow with an editable commit message.
7. Partial failures leave the repository intact and produce an accurate recoverable state without duplicate commits or PRs.
8. Ambiguous, unsafe, or unsupported states do not offer misleading one-click mutations.
9. The renderer performs no direct Git or provider command execution.
10. API contracts, generated artifacts, focused tests, complete relevant suites, and real desktop visual verification are all current and passing.

## Non-Goals

- A general-purpose Git client in the inspector.
- Interactive staging or per-hunk commit composition.
- Automatic conflict resolution, rebasing, history rewriting, or force pushing.
- Editing arbitrary remotes or branch tracking configuration.
- Managing PRs not owned or explicitly claimed by the session.
- Replacing the detailed Files/Changes review experience.
- Redesigning the existing CI, review, comment, or merge cards beyond what is needed to integrate delivery state.
- Automatically publishing agent work without an explicit user action.

## Suggested Delivery Shape

This may be implemented as one cohesive worker task, but the implementation owner should establish safe vertical slices. A reasonable progression is:

1. Introduce authoritative delivery-state classification and render the pre-PR/local-change summary.
2. Add retry-safe publish-and-create-PR behavior.
3. Add existing-PR push and commit-and-push behavior.
4. Complete recovery states, accessibility, telemetry, and real-app verification.

This sequence is advisory rather than prescriptive. The worker may reorganize it if repository boundaries or testability suggest a better path.

## Implementation Freedom

The worker is explicitly encouraged to make sound implementation choices within these constraints. In particular, this spec does **not** mandate:

- A specific endpoint count or URL structure.
- A specific Go service/package decomposition.
- A particular React component name or hierarchy.
- Whether delivery state extends an existing DTO or uses a dedicated response.
- Exact labels, icons, card styling, or confirmation presentation.
- A specific provider command, as long as the chosen integration follows current port/adapter boundaries and repository policy.

Where details are not specified, prefer the existing architecture, reuse established patterns, keep the common path concise, and optimize for a coherent user experience rather than minimum code movement.
