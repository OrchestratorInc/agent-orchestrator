package daemon

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	usagepipeline "github.com/aoagents/agent-orchestrator/backend/internal/observe/usage"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/agentaccounts"
	usagesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/usage"
)

type freshUsageSummary struct {
	*usagesvc.SummaryReader
	pipeline *usagepipeline.Pipeline
}

func (u freshUsageSummary) ListCompact(ctx context.Context, projectID domain.ProjectID) ([]domain.CompactSessionUsage, error) {
	u.pipeline.Flush(ctx)
	return u.SummaryReader.ListCompact(ctx, projectID)
}

type freshUsageReport struct {
	*usagesvc.ReportReader
	pipeline *usagepipeline.Pipeline
}

func (u freshUsageReport) Report(ctx context.Context, days, offset int, timeZone string, projectID domain.ProjectID) (domain.UsageReport, error) {
	u.pipeline.Flush(ctx)
	return u.ReportReader.Report(ctx, days, offset, timeZone, projectID)
}

type freshAccountUsage struct {
	*agentaccounts.Service
	pipeline *usagepipeline.Pipeline
}

func (u freshAccountUsage) Usage(ctx context.Context, days int) ([]agentaccounts.AccountUsage, error) {
	u.pipeline.Flush(ctx)
	return u.Service.Usage(ctx, days)
}
