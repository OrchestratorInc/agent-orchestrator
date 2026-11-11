// Package gitlab implements the ports.Tracker outbound port for GitLab
// Issues. v1 is read-only:
//
//   - Get returns a normalized snapshot of one issue (spawn-bootstrap
//     reads it to hydrate the agent prompt).
//   - List returns a filtered slice of issues in a project, paginated via
//     GitLab's Link-header pagination with state/labels/assignee filters.
//   - Preflight performs a single GET /user against GitLab to verify the
//     token is accepted; success is cached for the lifetime of the
//     Tracker, failures are not.
//
// The adapter reuses the SCM provider's TokenSource chain
// (AO_GITLAB_TOKEN / GITLAB_TOKEN / glab auth status --show-token).
//
// # Self-managed hosts
//
// AllowedHosts plus HostTokens mirror the SCM provider's host routing: a
// self-managed host must be allowlisted, and may carry its own token. A
// configuration whose only credentials are HostTokens is valid — the tracker is
// built for that host and gitlab.com lookups fail with ErrNoToken, rather than
// the whole tracker being disabled at boot.
//
// # State mapping
//
// GitLab issues have two native states: opened and closed. They map onto
// the normalized state vocabulary as follows:
//
//   - opened -> open
//   - closed -> done
//
// # Out of scope
//
//   - No Comment, no Transition.
//   - No ETag-based conditional revalidation (unlike the GitHub tracker);
//     List always fetches fresh data.
//   - No webhook receiver, no polling goroutine.
package gitlab
