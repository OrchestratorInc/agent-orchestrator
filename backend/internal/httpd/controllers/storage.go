package controllers

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/storagemgr"
)

// StorageService is the controller-facing storage-management contract.
type StorageService interface {
	Report(ctx context.Context) (storagemgr.Report, error)
	StartScan()
	Clean(ctx context.Context, req storagemgr.CleanRequest) (storagemgr.CleanResult, error)
	Actions(ctx context.Context, limit int) ([]domain.StorageAction, error)
	UpdateSettings(ctx context.Context, change func(*domain.StorageSettings)) (domain.StorageSettings, error)
}

// StorageController owns the /storage routes: the disk-usage report, scans,
// cleanups, the audit log and the storage settings.
type StorageController struct {
	Svc StorageService
}

// Register mounts the storage routes.
func (c *StorageController) Register(r chi.Router) {
	r.Get("/storage", c.report)
	r.Post("/storage/scan", c.scan)
	r.Post("/storage/clean", c.clean)
	r.Get("/storage/actions", c.actions)
	r.Patch("/storage/settings", c.updateSettings)
}

func (c *StorageController) report(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/storage")
		return
	}
	report, err := c.Svc.Report(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, storageReportResponse(report))
}

func (c *StorageController) scan(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/storage/scan")
		return
	}
	c.Svc.StartScan()
	report, err := c.Svc.Report(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, storageReportResponse(report))
}

func (c *StorageController) clean(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/storage/clean")
		return
	}
	var req StorageCleanRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	cleanReq := storagemgr.CleanRequest{DryRun: req.DryRun, IncludeDev: req.IncludeDev}
	for _, kind := range req.Kinds {
		cleanReq.Kinds = append(cleanReq.Kinds, domain.StorageCleanKind(kind))
	}
	for _, id := range req.SessionIDs {
		cleanReq.SessionIDs = append(cleanReq.SessionIDs, domain.SessionID(id))
	}
	result, err := c.Svc.Clean(r.Context(), cleanReq)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	status := http.StatusOK
	if !req.DryRun {
		status = http.StatusAccepted
	}
	envelope.WriteJSON(w, status, storageCleanResponse(result))
}

func (c *StorageController) actions(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/storage/actions")
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 1000 {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "STORAGE_ACTIONS_LIMIT_INVALID", "limit must be between 1 and 1000", nil)
			return
		}
		limit = parsed
	}
	actions, err := c.Svc.Actions(r.Context(), limit)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	out := StorageActionsResponse{Actions: make([]StorageAction, 0, len(actions))}
	for _, action := range actions {
		out.Actions = append(out.Actions, storageActionDTO(action))
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *StorageController) updateSettings(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPatch, "/api/v1/storage/settings")
		return
	}
	var req UpdateStorageSettingsRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	// The service reads, patches and saves under one lock, so a concurrent
	// change to another setting is never overwritten by a stale read.
	updated, err := c.Svc.UpdateSettings(r.Context(), func(settings *domain.StorageSettings) {
		applyStorageSettingsPatch(settings, req)
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, storageSettingsDTO(updated))
}

func applyStorageSettingsPatch(settings *domain.StorageSettings, req UpdateStorageSettingsRequest) {
	setBool := func(dst, src *bool) {
		if src != nil {
			*dst = *src
		}
	}
	setInt := func(dst, src *int) {
		if src != nil {
			*dst = *src
		}
	}
	setBool(&settings.AutoClean, req.AutoClean)
	setBool(&settings.OffloadEnabled, req.OffloadEnabled)
	setInt(&settings.OffloadIdleHours, req.OffloadIdleHours)
	setInt(&settings.OffloadNoPRGraceHours, req.OffloadNoPRGraceHours)
	setInt(&settings.OffloadMergedIdleMinutes, req.OffloadMergedIdleMinutes)
	setInt(&settings.EndedWorktreeDays, req.EndedWorktreeDays)
	setInt(&settings.BackupDays, req.BackupDays)
	setInt(&settings.LogDays, req.LogDays)
	setBool(&settings.CleanDevProfiles, req.CleanDevProfiles)
	setInt(&settings.DevProfileDays, req.DevProfileDays)
	setInt(&settings.TemplateDays, req.TemplateDays)
}

func storageReportResponse(report storagemgr.Report) StorageReportResponse {
	out := StorageReportResponse{
		ScannedAt:        optionalTime(report.ScannedAt),
		ScanDurationMs:   report.ScanDuration.Milliseconds(),
		Scanning:         report.Scanning,
		ScanError:        report.ScanError,
		TotalBytes:       report.TotalBytes,
		Categories:       make([]StorageCategoryUsage, 0, len(report.Categories)),
		Worktrees:        make([]StorageWorktree, 0, len(report.Worktrees)),
		Plan:             make([]StoragePlannedAction, 0, len(report.Plan)),
		Settings:         storageSettingsDTO(report.Settings),
		AutoCleanAllowed: report.AutoAllowed,
		CopyOnWrite:      report.CloneSupport,
		Cleaning:         report.Cleaning,
		Disk: StorageDisk{
			Path:       report.Disk.Path,
			TotalBytes: report.Disk.TotalBytes,
			FreeBytes:  report.Disk.FreeBytes,
			Level:      string(report.Disk.Level),
			CheckedAt:  optionalTime(report.Disk.CheckedAt),
			Error:      report.Disk.Err,
		},
		Database: StorageDatabase{
			FileBytes:   report.Database.FileBytes,
			WALBytes:    report.Database.WALBytes,
			FreeBytes:   report.Database.FreeBytes,
			Tables:      make([]StorageTableSize, 0, len(report.Database.Tables)),
			TablesKnown: report.Database.TablesKnown,
		},
	}
	if out.Disk.Level == "" {
		out.Disk.Level = string(domain.DiskPressureNormal)
	}
	for _, category := range report.Categories {
		out.Categories = append(out.Categories, StorageCategoryUsage{
			Category: string(category.Category), Bytes: category.Bytes, ReclaimableBytes: category.ReclaimableBytes,
		})
	}
	for _, wt := range report.Worktrees {
		dto := StorageWorktree{
			Path: wt.Path, ProjectID: string(wt.ProjectID), SessionID: string(wt.SessionID), State: string(wt.State),
			Bytes: wt.Bytes, DependencyBytes: wt.DependencyBytes, LastUsedAt: optionalTime(wt.LastUsed),
		}
		if sess, ok := report.Sessions[wt.SessionID]; ok && wt.SessionID != "" {
			dto.Offloaded, dto.OffloadedBytes = sess.Offloaded, sess.OffloadedBytes
			dto.OffloadedAt, dto.RestoreError = optionalTime(sess.OffloadedAt), sess.RestoreError
		}
		out.Worktrees = append(out.Worktrees, dto)
	}
	for _, table := range report.Database.Tables {
		out.Database.Tables = append(out.Database.Tables, StorageTableSize{Name: table.Name, Bytes: table.Bytes})
	}
	for _, action := range report.Plan {
		out.ReclaimableBytes += action.Bytes
		if action.Automatic {
			out.AutoReclaimableBytes += action.Bytes
		}
		out.Plan = append(out.Plan, StoragePlannedAction{
			Kind: string(action.Kind), Target: action.Target, SessionID: string(action.SessionID),
			Reason: action.Reason, Bytes: action.Bytes, Automatic: action.Automatic,
		})
	}
	if report.LastClean != nil {
		last := storageCleanResponse(*report.LastClean)
		out.LastClean = &last
	}
	return out
}

func storageSettingsDTO(settings domain.StorageSettings) StorageSettings {
	return StorageSettings{
		AutoClean:                settings.AutoClean,
		OffloadEnabled:           settings.OffloadEnabled,
		OffloadIdleHours:         settings.OffloadIdleHours,
		OffloadNoPRGraceHours:    settings.OffloadNoPRGraceHours,
		OffloadMergedIdleMinutes: settings.OffloadMergedIdleMinutes,
		EndedWorktreeDays:        settings.EndedWorktreeDays,
		BackupDays:               settings.BackupDays,
		LogDays:                  settings.LogDays,
		CleanDevProfiles:         settings.CleanDevProfiles,
		DevProfileDays:           settings.DevProfileDays,
		TemplateDays:             settings.TemplateDays,
		UpdatedAt:                optionalTime(settings.UpdatedAt),
	}
}

func storageCleanResponse(result storagemgr.CleanResult) StorageCleanResponse {
	out := StorageCleanResponse{
		StartedAt:  result.StartedAt.UTC(),
		FinishedAt: optionalTime(result.FinishedAt),
		Running:    result.Running,
		DryRun:     result.DryRun,
		FreedBytes: result.FreedBytes,
		Actions:    make([]StorageAction, 0, len(result.Actions)),
	}
	for _, action := range result.Actions {
		out.Actions = append(out.Actions, storageActionDTO(action))
	}
	return out
}

func storageActionDTO(action domain.StorageAction) StorageAction {
	return StorageAction{
		ID: action.ID, At: action.At.UTC(), Kind: string(action.Kind), Target: action.Target,
		SessionID: string(action.SessionID), Reason: action.Reason, Bytes: action.Bytes,
		DryRun: action.DryRun, Result: string(action.Result), Error: action.Error,
	}
}
