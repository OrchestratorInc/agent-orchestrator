package zcodeappserver

import (
	"context"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/zcodeappserver/zcodeproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Asserted because idle hibernation requires ChatCapabilityResume plus this
// interface; without it the hibernate loop silently never considers zcode.
var _ ports.ChatProviderHibernator = (*conversation)(nil)

// hibernateReadTimeout bounds the session/read behind CanHibernate.
const hibernateReadTimeout = 3 * time.Second

// CanHibernate reports whether nothing provider-owned would be lost by
// stopping the app-server: the projection is idle with no active tool calls,
// background jobs or pending permissions (PROVEN fields, p9-usage-read-mcp.ndjson)
// and the server lists no pending interaction requests. An error leaves the
// provider running.
func (c *conversation) CanHibernate(ctx context.Context) (bool, error) {
	if c.turns.active() != "" {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, hibernateReadTimeout)
	defer cancel()
	snapshot, err := c.readSnapshot(ctx, 1)
	if err != nil {
		return false, fmt.Errorf("hibernate check: %w", err)
	}
	projection := snapshot.Projection
	if projection.Status != zcodeproto.ProjectionIdle {
		return false, nil
	}
	return len(projection.ActiveToolCalls) == 0 &&
		len(projection.BackgroundJobs) == 0 &&
		len(projection.PendingPermissions) == 0 &&
		len(snapshot.Runtime.PendingRequestIDs) == 0 &&
		snapshot.Runtime.ActiveTurnID == "", nil
}

// Hibernate stops the app-server but leaves its native session on disk for
// Resume (session/resume in a replacement process keeps the memory, PROVEN
// p8-resume.ndjson).
func (c *conversation) Hibernate() error { return c.Terminate() }
