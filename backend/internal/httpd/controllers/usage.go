package controllers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

// UsageSummaryService is the controller-facing compact usage read contract.
type UsageSummaryService interface {
	ListCompact(context.Context, domain.ProjectID) ([]domain.CompactSessionUsage, error)
	Get(context.Context, domain.SessionID) (domain.SessionUsageSummary, error)
}

// UsageReportService builds the usage page's report.
type UsageReportService interface {
	Report(ctx context.Context, days, offset int, timeZone string, projectID domain.ProjectID) (domain.UsageReport, error)
}

// UsageLimitHistoryService reads the windows AO's own limit readings show.
type UsageLimitHistoryService interface {
	History(ctx context.Context, days int) (domain.UsageLimitHistory, error)
}

// SessionMemoryService samples resident memory per live session.
type SessionMemoryService interface {
	ListMemory(context.Context, domain.ProjectID) ([]domain.SessionMemory, error)
	// SystemMemory reports host RAM for the panel's total bar. Returns
	// procmem.ErrUnsupported where the platform can't be read; callers omit
	// the bar rather than fail the whole request.
	SystemMemory(context.Context) (domain.SystemMemory, error)
	// AppMemory is everything AO runs, for the topbar pressure indicator.
	AppMemory(context.Context) (domain.AppMemory, error)
}

// MemoryPressureReader reads the machine's memory-pressure verdict alone:
// cheap enough to poll while no other memory figure is on screen.
type MemoryPressureReader interface {
	Pressure(context.Context) (procmem.Pressure, error)
}

// SessionStepsReader lists a session's recent tool calls, oldest first.
type SessionStepsReader interface {
	Steps(id domain.SessionID) []domain.SessionStep
}

// UsageController owns compact dashboard usage routes.
type UsageController struct {
	Svc    UsageSummaryService
	Log    *slog.Logger
	Memory SessionMemoryService
	// Steps is optional: without it rows carry no activity.
	Steps SessionStepsReader
	// Pressure is optional: without it the pressure route is 501.
	Pressure MemoryPressureReader
	// Report is optional: without it the report route is 501.
	Report UsageReportService
	// LimitHistory is optional: without it the limit windows route is 501.
	LimitHistory UsageLimitHistoryService
}

// Register mounts usage routes on the supplied router.
func (c *UsageController) Register(r chi.Router) {
	r.Get("/usage/report", c.getReport)
	r.Get("/usage/limit-windows", c.getLimitWindows)
	r.Get("/usage/sessions", c.listSessions)
	r.Get("/usage/sessions/memory", c.listMemory)
	r.Get("/usage/memory/pressure", c.getPressure)
	r.Get("/usage/sessions/{sessionId}", c.getSession)
}

func (c *UsageController) getLimitWindows(w http.ResponseWriter, r *http.Request) {
	if c.LimitHistory == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/usage/limit-windows")
		return
	}
	days := 7
	if raw := r.URL.Query().Get("days"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			envelope.WriteError(w, r, apierr.Invalid("INVALID_USAGE_WINDOW", "days must be a whole number", nil))
			return
		}
		days = parsed
	}
	history, err := c.LimitHistory.History(r.Context(), days)
	if err != nil {
		c.Log.WarnContext(r.Context(), "failed to read limit windows", "error", err)
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, UsageLimitWindowsResponse{
		Since: history.Since, Until: history.Until, FirstReadingAt: history.FirstReadingAt, Logins: history.Logins,
	})
}

func (c *UsageController) getReport(w http.ResponseWriter, r *http.Request) {
	if c.Report == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/usage/report")
		return
	}
	query := r.URL.Query()
	days, offset := 7, 0
	for name, target := range map[string]*int{"days": &days, "offset": &offset} {
		if raw := query.Get(name); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil {
				envelope.WriteError(w, r, apierr.Invalid("INVALID_USAGE_WINDOW", name+" must be a whole number", nil))
				return
			}
			*target = parsed
		}
	}
	report, err := c.Report.Report(r.Context(), days, offset, query.Get("tz"), domain.ProjectID(query.Get("projectId")))
	if err != nil {
		c.Log.WarnContext(r.Context(), "failed to read usage report", "error", err)
		envelope.WriteError(w, r, err)
		return
	}
	out := UsageReportResponse{
		Days: report.Days, TimeZone: report.TimeZone, Resolution: report.Resolution,
		Since: report.Since, Until: report.Until, Periods: report.Periods,
		Rows: make([]UsageReportRowResponse, 0, len(report.Rows)), Sessions: make([]UsageReportSessionResponse, 0, len(report.Sessions)),
	}
	if out.Periods == nil {
		out.Periods = []string{}
	}
	for _, row := range report.Rows {
		out.Rows = append(out.Rows, UsageReportRowResponse{
			Period: row.Period, Harness: string(row.Harness), Model: row.ModelID, Speed: row.Speed,
			Events: row.Events, PricedEvents: row.PricedEvents,
			InputTokens: row.InputTokens, CacheReadTokens: row.CacheReadTokens, CacheWriteTokens: row.CacheWriteTokens, OutputTokens: row.OutputTokens,
			InputCostNanos: row.InputCostNanos, CacheReadCostNanos: row.CacheReadCostNanos, CacheWriteCostNanos: row.CacheWriteCostNanos,
			OutputCostNanos: row.OutputCostNanos, CostNanos: row.CostNanos, PremiumNanos: row.PremiumNanos, CacheSavingsNanos: row.CacheSavingsNanos,
		})
	}
	for _, session := range report.Sessions {
		out.Sessions = append(out.Sessions, UsageReportSessionResponse{
			SessionID: string(session.SessionID), ProjectID: string(session.ProjectID), Harness: string(session.Harness), Title: session.Title,
			Events: session.Events, PricedEvents: session.PricedEvents, Tokens: session.Tokens, CostNanos: session.CostNanos, LastUsedAt: session.LastUsedAt,
		})
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *UsageController) listSessions(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/usage/sessions")
		return
	}
	items, err := c.Svc.ListCompact(r.Context(), domain.ProjectID(r.URL.Query().Get("projectId")))
	if err != nil {
		c.Log.WarnContext(r.Context(), "failed to list compact session usage", "error", err)
		envelope.WriteError(w, r, err)
		return
	}
	out := make([]CompactSessionUsageResponse, 0, len(items))
	for _, item := range items {
		var totalTokens int64
		if item.ProcessedTokens != nil {
			totalTokens = *item.ProcessedTokens
		}
		out = append(out, CompactSessionUsageResponse{
			SessionID: item.SessionID, ProcessedTokens: item.ProcessedTokens,
			TotalTokens: totalTokens, Incomplete: item.Incomplete,
			EstimatedCost: estimatedCostResponse(item.EstimatedCost),
		})
	}
	envelope.WriteJSON(w, http.StatusOK, ListCompactSessionUsageResponse{Sessions: out})
}

func (c *UsageController) listMemory(w http.ResponseWriter, r *http.Request) {
	if c.Memory == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/usage/sessions/memory")
		return
	}
	items, err := c.Memory.ListMemory(r.Context(), domain.ProjectID(r.URL.Query().Get("projectId")))
	if err != nil {
		if errors.Is(err, procmem.ErrUnsupported) {
			err = apierr.NotImplemented("MEMORY_UNSUPPORTED", err.Error())
		}
		envelope.WriteError(w, r, err)
		return
	}
	out := make([]SessionMemoryResponse, 0, len(items))
	for _, item := range items {
		row := sessionMemoryResponse(item)
		if c.Steps != nil {
			row.Activity = sessionActivityResponse(c.Steps.Steps(item.SessionID))
		}
		out = append(out, row)
	}
	var system *SystemMemoryResponse
	if sys, sysErr := c.Memory.SystemMemory(r.Context()); sysErr == nil {
		system = &SystemMemoryResponse{
			TotalBytes: sys.TotalBytes, AvailableBytes: sys.AvailableBytes,
			SwapTotalBytes: sys.SwapTotalBytes, SwapUsedBytes: sys.SwapUsedBytes, SwapBytesPerSec: sys.SwapBytesPerSec,
			CPUCount: sys.CPUCount, Load1: sys.Load1, CPUPercent: sys.CPUPercent, CPUMeasured: sys.CPUMeasured,
			PressureRaw: sys.PressureRaw, PressureSource: sys.PressureSource,
		}
	}
	var app *AppMemoryResponse
	if a, appErr := c.Memory.AppMemory(r.Context()); appErr == nil {
		app = &AppMemoryResponse{RSSBytes: a.RSSBytes, ProcessCount: a.ProcessCount, CPUPercent: a.CPUPercent, CPUMeasured: a.CPUMeasured}
		if a.Own.ProcessCount > 0 {
			own := sessionMemoryResponse(a.Own)
			app.Own = &own
		}
		for _, rv := range a.Reviewers {
			app.Reviewers = append(app.Reviewers, ReviewerMemoryResponse{
				ReviewID: rv.ReviewID, SessionID: rv.SessionID, Harness: string(rv.Harness),
				Memory: sessionMemoryResponse(rv.Memory),
			})
		}
	}
	envelope.WriteJSON(w, http.StatusOK, ListSessionMemoryResponse{Sessions: out, System: system, App: app})
}

func (c *UsageController) getPressure(w http.ResponseWriter, r *http.Request) {
	if c.Pressure == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/usage/memory/pressure")
		return
	}
	p, err := c.Pressure.Pressure(r.Context())
	if err != nil {
		if errors.Is(err, procmem.ErrUnsupported) {
			err = apierr.NotImplemented("MEMORY_UNSUPPORTED", err.Error())
		}
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, MemoryPressureResponse{PressureRaw: p.Raw, PressureSource: p.Source})
}

// recentStepsShown is how many finished steps a row lists; the window is a
// glance, not a log.
const recentStepsShown = 5

// sessionActivityResponse splits the ring into the step still running and the
// last few finished ones, newest first. Nil when the harness reported none.
func sessionActivityResponse(steps []domain.SessionStep) *SessionActivityResponse {
	if len(steps) == 0 {
		return nil
	}
	out := &SessionActivityResponse{Recent: []SessionStepResponse{}}
	for i := len(steps) - 1; i >= 0; i-- {
		step := stepResponse(steps[i])
		if steps[i].EndedAt.IsZero() {
			if out.Current == nil {
				out.Current = &step
			}
			continue
		}
		if len(out.Recent) < recentStepsShown {
			out.Recent = append(out.Recent, step)
		}
	}
	return out
}

func stepResponse(step domain.SessionStep) SessionStepResponse {
	out := SessionStepResponse{Tool: step.Tool, StartedAt: step.StartedAt, Failed: step.Failed}
	if !step.EndedAt.IsZero() {
		ended := step.EndedAt
		out.EndedAt = &ended
	}
	return out
}

func sessionMemoryResponse(item domain.SessionMemory) SessionMemoryResponse {
	procs := make([]SessionMemoryProcessResponse, 0, len(item.Processes))
	for _, p := range item.Processes {
		procs = append(procs, SessionMemoryProcessResponse{PID: p.PID, PPID: p.PPID, RSSBytes: p.RSSBytes, CPUPercent: p.CPUPercent, Command: p.Command})
	}
	return SessionMemoryResponse{
		SessionID: item.SessionID, RSSBytes: item.RSSBytes, ProcessCount: item.ProcessCount, CPUPercent: item.CPUPercent,
		SampledAt: item.SampledAt, Processes: procs,
	}
}

func (c *UsageController) getSession(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/usage/sessions/{sessionId}")
		return
	}
	summary, err := c.Svc.Get(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")))
	if err != nil {
		c.Log.WarnContext(r.Context(), "failed to get session usage", "error", err)
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, sessionUsageResponse(summary))
}

func sessionUsageResponse(summary domain.SessionUsageSummary) SessionUsageResponse {
	harnesses := make([]UsageHarnessResponse, 0, len(summary.Harnesses))
	for _, harness := range summary.Harnesses {
		models := make([]UsageModelResponse, 0, len(harness.Models))
		for _, model := range harness.Models {
			models = append(models, UsageModelResponse{
				ModelID: model.ModelID, Totals: usageTotalsResponse(model.Totals),
			})
		}
		harnesses = append(harnesses, UsageHarnessResponse{
			Harness: string(harness.Harness), Totals: usageTotalsResponse(harness.Totals), Models: models,
		})
	}
	return SessionUsageResponse{
		SessionID: summary.SessionID, Incomplete: summary.Incomplete,
		Totals: usageTotalsResponse(summary.Totals), Harnesses: harnesses,
	}
}

func usageTotalsResponse(totals domain.UsageMetricTotals) UsageTotalsResponse {
	return UsageTotalsResponse{
		InputTokens: totals.InputTokens, CachedInputTokens: totals.CachedInputTokens,
		UncachedInputTokens: totals.UncachedInputTokens,
		OutputTokens:        totals.OutputTokens, ProcessedTokens: totals.ProcessedTokens,
		CacheReadTokens: totals.CachedInputTokens,
		EstimatedCost:   estimatedCostResponse(totals.EstimatedCost),
	}
}

func estimatedCostResponse(cost *domain.EstimatedCost) *EstimatedCostResponse {
	if cost == nil {
		return nil
	}
	return &EstimatedCostResponse{
		TotalNanos: cost.TotalNanos, InputNanos: cost.InputNanos,
		CachedInputNanos: cost.CachedInputNanos, OutputNanos: cost.OutputNanos,
		Coverage:            string(cost.Coverage),
		ProviderAttribution: string(cost.ProviderAttribution),
	}
}
