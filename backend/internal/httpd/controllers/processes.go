package controllers

import (
	"context"
	"errors"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/procinventory"
)

// ProcessService is the process-footprint surface the daemon wires from
// backend/internal/procinventory. Nil keeps routes registered but returns
// OpenAPI-backed 501s, matching the other optional surfaces. Disabled (the
// AO_NO_PROCINVENTORY runtime gate) answers the same 501s while the list
// route still reports the disabled state the status bar keys off.
type ProcessService interface {
	Inventory(ctx context.Context) (procinventory.Inventory, error)
	Kill(ctx context.Context, targets []procinventory.KillTarget) (procinventory.KillReport, error)
	Disabled() bool
}

// ProcessController owns the /system/processes routes.
type ProcessController struct {
	Processes ProcessService
}

// Register mounts the process routes on the supplied router.
func (c *ProcessController) Register(r chi.Router) {
	r.Get("/system/processes", c.list)
	r.Post("/system/processes/kill", c.kill)
}

func processInventoryDTO(inv procinventory.Inventory) ProcessInventoryResponse {
	// Heaviest first: every client (CLI table, status bar) presents the list
	// in this order, and a swapped-out tree whose RSS collapsed mid-scan still
	// lands deterministically by its own snapshot value.
	treesDTO := make([]procinventory.Tree, len(inv.Trees))
	copy(treesDTO, inv.Trees)
	sort.SliceStable(treesDTO, func(i, j int) bool { return treesDTO[i].RSSBytes > treesDTO[j].RSSBytes })

	trees := make([]ProcessTreeDTO, 0, len(treesDTO))
	for _, tree := range treesDTO {
		trees = append(trees, ProcessTreeDTO{
			SessionID:      tree.SessionID,
			RootPID:        tree.RootPID,
			RootLstart:     tree.RootLstart,
			PIDCount:       tree.PIDCount,
			RSSBytes:       tree.RSSBytes,
			Kind:           tree.Kind,
			State:          tree.State,
			Attached:       tree.Attached,
			ActivityState:  tree.ActivityState,
			LastActivityAt: tree.LastActivityAt,
		})
	}
	remnants := make([]ProcessRemnantDTO, 0, len(inv.Remnants))
	for _, remnant := range inv.Remnants {
		remnants = append(remnants, ProcessRemnantDTO{
			SessionID: remnant.SessionID,
			PID:       remnant.PID,
			RSSBytes:  remnant.RSSBytes,
		})
	}
	response := ProcessInventoryResponse{
		GeneratedAt: inv.GeneratedAt,
		Daemon: ProcessGroupSummaryDTO{
			PID:      inv.Daemon.PID,
			Present:  inv.Daemon.Present,
			RSSBytes: inv.Daemon.RSSBytes,
		},
		Tmux: ProcessGroupSummaryDTO{
			PID:      inv.Tmux.PID,
			Present:  inv.Tmux.Present,
			RSSBytes: inv.Tmux.RSSBytes,
		},
		Trees:    trees,
		Remnants: remnants,
		Totals: ProcessTotalsDTO{
			SessionsCount:    inv.Totals.SessionsCount,
			SessionsRSSBytes: inv.Totals.SessionsRSSBytes,
			OrphansCount:     inv.Totals.OrphansCount,
			OrphansRSSBytes:  inv.Totals.OrphansRSSBytes,
			ForeignCount:     inv.Totals.ForeignCount,
			ForeignRSSBytes:  inv.Totals.ForeignRSSBytes,
			DaemonRSSBytes:   inv.Totals.DaemonRSSBytes,
			TmuxRSSBytes:     inv.Totals.TmuxRSSBytes,
		},
	}
	if inv.Host != nil {
		response.Host = &ProcessHostMemoryDTO{
			TotalBytes:          inv.Host.TotalBytes,
			UsedBytes:           inv.Host.UsedBytes,
			FreeBytes:           inv.Host.FreeBytes,
			CachedBytes:         inv.Host.CachedBytes,
			WiredBytes:          inv.Host.WiredBytes,
			AppBytes:            inv.Host.AppBytes,
			CompressedBytes:     inv.Host.CompressedBytes,
			SwapTotalBytes:      inv.Host.SwapTotalBytes,
			SwapUsedBytes:       inv.Host.SwapUsedBytes,
			SwapFreeBytes:       inv.Host.SwapFreeBytes,
			SwapMaxBytes:        inv.Host.SwapMaxBytes,
			PressureFreePercent: inv.Host.PressureFreePercent,
		}
	}
	return response
}

func (c *ProcessController) list(w http.ResponseWriter, r *http.Request) {
	if c.Processes == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/system/processes")
		return
	}
	if c.Processes.Disabled() {
		// Still a 200: the status bar must learn "off" (and stop polling) in
		// one round trip, not treat it as an unavailable surface.
		envelope.WriteJSON(w, http.StatusOK, ProcessInventoryResponse{Disabled: true})
		return
	}
	inv, err := c.Processes.Inventory(r.Context())
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "PROCESS_SCAN_FAILED", "Process table scan failed", nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, processInventoryDTO(inv))
}

func (c *ProcessController) kill(w http.ResponseWriter, r *http.Request) {
	if c.Processes == nil || c.Processes.Disabled() {
		apispec.NotImplemented(w, r, "POST", "/api/v1/system/processes/kill")
		return
	}
	var in ProcessKillRequest
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	targets := make([]procinventory.KillTarget, 0, len(in.Targets))
	for _, t := range in.Targets {
		targets = append(targets, procinventory.KillTarget{
			SessionID:  t.SessionID,
			RootPID:    t.RootPID,
			RootLstart: t.RootLstart,
		})
	}
	report, err := c.Processes.Kill(r.Context(), targets)
	if errors.Is(err, procinventory.ErrKillInProgress) {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "KILL_IN_PROGRESS", "Another kill is already running", nil)
		return
	}
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "PROCESS_SCAN_FAILED", "Process table scan failed", nil)
		return
	}
	results := make([]ProcessKillResultDTO, 0, len(report.Results))
	for _, result := range report.Results {
		results = append(results, ProcessKillResultDTO{
			SessionID: result.SessionID,
			RootPID:   result.RootPID,
			Status:    result.Status,
			Detail:    result.Detail,
		})
	}
	envelope.WriteJSON(w, http.StatusOK, ProcessKillResponse{Results: results})
}
