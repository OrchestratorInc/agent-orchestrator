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
)

type fakeDisk struct {
	report  domain.DiskReport
	project domain.ProjectID
}

func (f *fakeDisk) ListDisk(_ context.Context, project domain.ProjectID) (domain.DiskReport, error) {
	f.project = project
	return f.report, nil
}

func getDisk(t *testing.T, c *UsageController, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	c.Register(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestDiskRouteReturnsTheCachedSizes(t *testing.T) {
	measuredAt := time.Date(2026, 11, 12, 9, 30, 0, 0, time.UTC)
	disk := &fakeDisk{report: domain.DiskReport{
		Sessions:   []domain.SessionDisk{{SessionID: "s-1", SizeBytes: 1 << 30, MeasuredAt: measuredAt}},
		TotalBytes: 1 << 30, Measuring: 2,
	}}
	rec := getDisk(t, &UsageController{Disk: disk}, "/usage/sessions/disk?projectId=p-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	var got ListSessionDiskResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := SessionDiskResponse{SessionID: "s-1", SizeBytes: 1 << 30, MeasuredAt: measuredAt}
	if len(got.Sessions) != 1 || got.Sessions[0] != want || got.TotalBytes != 1<<30 || got.Measuring != 2 {
		t.Fatalf("got %+v", got)
	}
	if disk.project != "p-1" {
		t.Fatalf("project = %q, want the projectId query", disk.project)
	}
}

// Before the first measurement the list is an empty array, never null.
func TestDiskRouteWritesNoRowsAsAnEmptyArray(t *testing.T) {
	rec := getDisk(t, &UsageController{Disk: &fakeDisk{report: domain.DiskReport{Measuring: 3}}}, "/usage/sessions/disk")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"sessions":[]`) {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
}

func TestDiskRouteIsNotImplementedWithoutAReader(t *testing.T) {
	if rec := getDisk(t, &UsageController{}, "/usage/sessions/disk"); rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d", rec.Code)
	}
}
