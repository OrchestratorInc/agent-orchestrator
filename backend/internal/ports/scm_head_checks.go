package ports

import (
	"context"
	"time"
)

// SCMHeadChecks is what a provider knows about the checks on one commit beyond
// its status rollup. A rollup with nothing in it reads the same for a commit
// nothing checks and for one pushed seconds ago whose checks haven't started;
// these facts tell the two apart.
type SCMHeadChecks struct {
	// Suites counts the commit's check suites. Started counts those that ran a
	// check or are still deciding to: an installed app that never runs on the
	// repository leaves its suite queued with no checks, and so does a run
	// that hasn't begun yet, which is why Since matters.
	Suites  int
	Started int
	// Statuses counts the commit's commit statuses.
	Statuses int
	// Since is when this provider first observed the repository/head. Commit
	// dates cannot establish when a push occurred. Zero when unknown.
	Since time.Time
}

// SCMHeadChecksReader reads SCMHeadChecks for a repository's commit.
type SCMHeadChecksReader interface {
	HeadChecks(ctx context.Context, repo SCMRepo, sha string) (SCMHeadChecks, error)
}
