package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/storagemgr"
)

type fakeStorageService struct {
	settings  domain.StorageSettings
	cleanReq  storagemgr.CleanRequest
	scanned   bool
	saved     *domain.StorageSettings
	cleanErr  error
	lastLimit int
}

func (f *fakeStorageService) Report(context.Context) (storagemgr.Report, error) {
	return storagemgr.Report{
		ScannedAt:  time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC),
		TotalBytes: 300,
		Disk:       storagemgr.DiskStatus{Path: "/data", TotalBytes: 1000, FreeBytes: 400, Level: domain.DiskPressureLow},
		Categories: []storagemgr.CategoryUsage{{Category: domain.StorageWorktrees, Bytes: 300, ReclaimableBytes: 200}},
		Worktrees:  []storagemgr.WorktreeUsage{{Path: "/w/p-1", SessionID: "p-1", State: storagemgr.WorktreeQuiet, Bytes: 300, DependencyBytes: 200}},
		Sessions:   map[domain.SessionID]storagemgr.SessionStorage{"p-1": {Offloaded: true, OffloadedBytes: 50}},
		Plan: []storagemgr.PlannedAction{
			{Kind: domain.StorageCleanOffload, Target: "/w/p-1", SessionID: "p-1", Bytes: 200, Automatic: true},
			{Kind: domain.StorageCleanBackups, Target: "/d/b", Bytes: 10},
		},
		Settings:    f.settings,
		AutoAllowed: true,
	}, nil
}

func (f *fakeStorageService) StartScan() { f.scanned = true }

func (f *fakeStorageService) Clean(_ context.Context, req storagemgr.CleanRequest) (storagemgr.CleanResult, error) {
	f.cleanReq = req
	if f.cleanErr != nil {
		return storagemgr.CleanResult{}, f.cleanErr
	}
	return storagemgr.CleanResult{StartedAt: time.Now(), DryRun: req.DryRun, Running: !req.DryRun}, nil
}

func (f *fakeStorageService) Actions(_ context.Context, limit int) ([]domain.StorageAction, error) {
	f.lastLimit = limit
	return []domain.StorageAction{{ID: 1, At: time.Now(), Kind: domain.StorageCleanLogs, Target: "/l", Result: domain.StorageActionDone}}, nil
}

func (f *fakeStorageService) Settings(context.Context) (domain.StorageSettings, error) {
	return f.settings, nil
}

func (f *fakeStorageService) UpdateSettings(_ context.Context, s domain.StorageSettings) (domain.StorageSettings, error) {
	f.saved = &s
	return s, nil
}

func storageRouter(svc StorageService) http.Handler {
	r := chi.NewRouter()
	(&StorageController{Svc: svc}).Register(r)
	return r
}

func TestStorageReportMapsTotalsAndOffloadState(t *testing.T) {
	svc := &fakeStorageService{settings: domain.DefaultStorageSettings()}
	rec := httptest.NewRecorder()
	storageRouter(svc).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/storage", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var body StorageReportResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ReclaimableBytes != 210 || body.AutoReclaimableBytes != 200 || body.Disk.Level != "low" || len(body.Plan) != 2 {
		t.Fatalf("body = %+v", body)
	}
	if len(body.Worktrees) != 1 || !body.Worktrees[0].Offloaded || body.Worktrees[0].OffloadedBytes != 50 {
		t.Fatalf("worktrees = %+v", body.Worktrees)
	}
	if body.Settings.OffloadIdleHours != 48 || body.Database.Tables == nil {
		t.Fatalf("settings=%+v tables=%v", body.Settings, body.Database.Tables)
	}
}

func TestStorageCleanDryRunAndStartStatuses(t *testing.T) {
	svc := &fakeStorageService{}
	rec := httptest.NewRecorder()
	storageRouter(svc).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/storage/clean",
		strings.NewReader(`{"dryRun":true,"kinds":["offload"],"sessionIds":["p-1"],"includeDev":true}`)))
	if rec.Code != http.StatusOK || !svc.cleanReq.DryRun || !svc.cleanReq.IncludeDev ||
		len(svc.cleanReq.Kinds) != 1 || svc.cleanReq.Kinds[0] != domain.StorageCleanOffload || svc.cleanReq.SessionIDs[0] != "p-1" {
		t.Fatalf("status=%d req=%+v", rec.Code, svc.cleanReq)
	}
	rec = httptest.NewRecorder()
	storageRouter(svc).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/storage/clean", strings.NewReader(`{}`)))
	if rec.Code != http.StatusAccepted || svc.cleanReq.DryRun {
		t.Fatalf("start status=%d req=%+v", rec.Code, svc.cleanReq)
	}
	svc.cleanErr = storagemgr.ErrCleanInProgress
	rec = httptest.NewRecorder()
	storageRouter(svc).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/storage/clean", strings.NewReader(`{}`)))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "STORAGE_CLEAN_IN_PROGRESS") {
		t.Fatalf("conflict status=%d body=%s", rec.Code, rec.Body)
	}
}

func TestStorageSettingsPatchKeepsOmittedFields(t *testing.T) {
	svc := &fakeStorageService{settings: domain.DefaultStorageSettings()}
	rec := httptest.NewRecorder()
	storageRouter(svc).ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/storage/settings",
		strings.NewReader(`{"offloadEnabled":false,"endedWorktreeDays":0}`)))
	if rec.Code != http.StatusOK || svc.saved == nil {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	want := domain.DefaultStorageSettings()
	want.OffloadEnabled, want.EndedWorktreeDays = false, 0
	if *svc.saved != want {
		t.Fatalf("saved = %+v, want %+v", *svc.saved, want)
	}
}

func TestStorageActionsLimitValidationAndScan(t *testing.T) {
	svc := &fakeStorageService{}
	rec := httptest.NewRecorder()
	storageRouter(svc).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/storage/actions?limit=0", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("limit=0 status=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	storageRouter(svc).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/storage/actions?limit=7", nil))
	if rec.Code != http.StatusOK || svc.lastLimit != 7 {
		t.Fatalf("status=%d limit=%d", rec.Code, svc.lastLimit)
	}
	rec = httptest.NewRecorder()
	storageRouter(svc).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/storage/scan", nil))
	if rec.Code != http.StatusAccepted || !svc.scanned {
		t.Fatalf("scan status=%d scanned=%v", rec.Code, svc.scanned)
	}
}

func TestStorageRoutesWithoutServiceAnswerNotImplemented(t *testing.T) {
	rec := httptest.NewRecorder()
	storageRouter(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/storage", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status=%d", rec.Code)
	}
}
