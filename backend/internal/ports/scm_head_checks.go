package ports

import (
	"context"
	"time"
)

type SCMHeadChecks struct {
	Suites   int
	Started  int
	Statuses int
	Since    time.Time
}

type SCMHeadChecksReader interface {
	HeadChecks(ctx context.Context, repo SCMRepo, sha string) (SCMHeadChecks, error)
}
