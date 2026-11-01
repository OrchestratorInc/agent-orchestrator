// Package github observes GitHub pull requests for AO's SCM integrations.
//
// The compatibility exported surface is:
//
//	(*Provider).Observe(ctx, prURL) (ports.PRObservation, error)
//
// It performs a REST GET on /repos/{o}/{r}/pulls/{n} for the authoritative
// state booleans (draft / merged / closed / head SHA), one GraphQL query
// for the reviewDecision + mergeStateStatus + statusCheckRollup + review
// threads, and (only for CheckRuns that concluded failure-class) a REST
// GET on /repos/{o}/{r}/actions/jobs/{job_id}/logs to splice the last 20
// lines of the failed job into the observation.
//
// The provider-neutral SCM observer uses the same Provider for lower-level
// primitives: repo/commit ETag guards, branch-to-PR detection, GraphQL PR
// batching, failed-job log tails, and review-thread pagination. The polling
// loop itself is intentionally not in this package; it lives in
// internal/observe/scm.
//
// # State mapping
//
// Each ports.PRObservation field is derived as follows:
//
//   - Fetched:      false if any required REST/GraphQL call fails; true
//     only once all the calls have succeeded. Log-tail
//     fetch failures are best-effort: the LogTail is
//     stamped with a "<log fetch failed: ...>" sentinel
//     and the observation still surfaces as Fetched=true.
//
//   - URL, Number:  the URL the caller passed (validated) plus the
//     number from REST pulls/{n}.
//
//   - Draft:        REST pulls/{n}.draft.
//
//   - Merged:       REST pulls/{n}.merged OR a non-null merged_at.
//
//   - Closed:       REST pulls/{n}.state == "closed" AND NOT Merged.
//     (Closed and Merged are mutually exclusive.)
//
//   - CI: derived from the latest commit's statusCheckRollup contexts
//     (CheckRun + StatusContext). Failed if ANY context concluded in a
//     failure class (failure / cancelled / timed_out / action_required /
//     error). Pending if any context is still running / queued.
//     Passing if all non-skipped contexts concluded SUCCESS / NEUTRAL.
//     Unknown otherwise. Empty rollup falls back to the rollup-level
//     "state" field. A null rollup on a head commit (no check runs and no
//     statuses exist) is None: settled, as opposed to Unknown (not observed).
//
//   - Review: from GraphQL pullRequest.reviewDecision:
//
//     | GraphQL                | domain.ReviewDecision   |
//     |------------------------|-------------------------|
//     | APPROVED               | ReviewApproved          |
//     | CHANGES_REQUESTED      | ReviewChangesRequest    |
//     | REVIEW_REQUIRED        | ReviewRequired          |
//     | null / unknown         | ReviewNone              |
//
//   - Mergeability: composed in priority order; the first rule that
//     matches wins. The primary signal is the GraphQL pullRequest
//     payload; the REST pulls/{n} response is consulted only as a
//     tiebreaker when GraphQL is empty or has not yet been computed.
//     Rules:
//     (1) mergeStateStatus == DIRTY           -> MergeConflicting
//     (2) mergeStateStatus == BLOCKED         -> MergeBlocked
//     (3) mergeStateStatus == UNSTABLE        -> MergeUnstable
//     (4) GraphQL mergeable == CONFLICTING    -> MergeConflicting
//     (5) reviewDecision == changes_requested -> MergeBlocked
//     (6) CI == failing                       -> MergeBlocked
//     (7) REST mergeable_state pin — a TIE-BREAKER, not a terminal
//     step: "dirty"->MergeConflicting, "blocked"->MergeBlocked,
//     "unstable"->MergeUnstable, "clean"->MergeMergeable ONLY if
//     GraphQL says MERGEABLE or REST mergeable bool is true
//     (otherwise stays unknown — REST lags GraphQL).
//     (8) mergeable == MERGEABLE AND mergeStateStatus == CLEAN
//     -> MergeMergeable
//     (9) otherwise                           -> MergeUnknown
//
//   - Checks[]: one entry per rollup context. For CheckRun rows we use
//     name + conclusion + detailsUrl + the head SHA as the CommitHash;
//     for StatusContext rows we use context + state + targetUrl. LogTail
//     is populated ONLY for failure-class CheckRun entries, by fetching
//     /actions/jobs/{job_id}/logs and tailing to the last 20 lines.
//
//   - Comments[]: one entry per unresolved review-thread comment.
//     Resolved threads are skipped client-side (Resolved on the
//     observation is therefore always false). Bot authors are detected
//     via GitHub's __typename == "Bot" or User.Type == "Bot" and
//     dropped — the legacy strings.Contains(login, "bot") fallback was
//     intentionally NOT carried forward (it false-positives on logins
//     like "robothon" / "lambot123"; aa-18's review of PR #28 flagged
//     this).
//
// # Errors
//
// The Client classifies HTTP failures into three sentinels:
//
//   - ErrNotFound      — 404 (PR doesn't exist or token can't see it)
//   - ErrAuthFailed    — 401, or 403 without rate-limit signals
//   - ErrRateLimited   — 403 with X-RateLimit-Remaining=0, 403 with the
//     secondary "abuse detection" body, or 429
//     (also returns *RateLimitError with ResetAt /
//     RetryAfter — match via errors.As)
//
// All other transport failures (decode errors, network failures, GraphQL
// "errors" array) bubble up as wrapped errors with Fetched=false on the
// observation, so the PR Manager keeps the prior row rather than
// fabricating a closed/merged transition from a failed observation.
//
// # Caching
//
// The legacy Observe path's Client maintains an in-memory ETag cache per
// (method, path, query).
// On the second observation of the same PR the REST GET sends
// If-None-Match and replays the cached body on a 304 — GraphQL is always
// re-fetched because it doesn't expose ETag-based revalidation.
//
// The provider-neutral observer owns its own ETag cache and calls explicit
// provider guard methods that do not mutate the legacy Client cache.
//
// # Out of scope (intentionally — these are different PRs / lanes)
//
//   - Webhook ingestion (this package is polling-only).
//   - Linear / GitLab providers (separate PRs).
//   - Issue tracking (separate lane, see internal/adapters/tracker).
//   - Comment-injection-into-session-context (Messenger lane, not SCM).
package github
