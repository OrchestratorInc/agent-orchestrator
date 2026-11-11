// Package github implements the ports.Tracker outbound port for GitHub
// Issues. v1 is read-only:
//
//   - Get returns a normalized snapshot of one issue (spawn-bootstrap
//     reads it to hydrate the agent prompt).
//   - List returns a filtered slice of issues in a repo (one page, no
//     auto-pagination in v1; PRs are filtered out client-side because
//     GitHub's /issues endpoint conflates them).
//   - Preflight performs a single GET /user against GitHub to verify the
//     token is accepted; success is cached for the lifetime of the
//     Tracker, failures are not.
//
// List keeps ETag validators so repeat sweeps revalidate in one conditional
// request. Those validators are keyed by request path AND credential: GitHub
// scopes both visibility and ETags per token, so a cached list is never
// revalidated under a different credential. The cache is bounded, and evicting
// an entry only costs a full fetch.
//
// Writing back to the tracker (Comment, Transition) is deferred to issue
// #40. The observer/polling loop is deferred to issue #35.
//
// # Reverse state mapping
//
// GitHub Issues only have two native states (open, closed) plus a
// state_reason on closed issues (completed, not_planned, reopened). Get
// projects them onto the normalized state vocabulary as follows:
//
//   - closed + state_reason=not_planned       -> cancelled
//   - closed + (completed | empty | other)    -> done
//   - open   + "in-review" label               -> review        (wins when
//     both status labels are present; the workflow is progress -> review)
//   - open   + "in-progress" label             -> in_progress
//   - otherwise                                -> open
//
// The "in-progress" and "in-review" labels are recognized because humans
// (and other tooling) commonly apply them. The adapter does NOT write them
// in v1 — see issue #40 for the write-side work.
//
// # Out of scope
//
//   - No Comment, no Transition (issue #40).
//   - No List pagination beyond a single page (callers requesting more than
//     100 results need to wait for the observer/polling work in issue #35).
//   - No webhook receiver, no polling goroutine, no fact projection into
//     the PR service (issue #35).
//   - No richer per-provider metadata on Issue (milestones, project boards,
//     reactions); the port only carries fields all v1 providers can fill.
package github
