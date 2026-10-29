package telemetry

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestExperimentCohortAssignedOnceAndSticky(t *testing.T) {
	dir := t.TempDir()
	first, ok := loadOrAssignExperimentCohort(dir)
	if !ok || first < 0 || first >= experimentCohortBuckets {
		t.Fatalf("first = %d, %v", first, ok)
	}
	for i := 0; i < 5; i++ {
		if again, ok := loadOrAssignExperimentCohort(dir); !ok || again != first {
			t.Fatalf("reload = %d, %v; want %d", again, ok, first)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("data dir = %v (%v), want only the cohort file, no temp leftovers", entries, err)
	}
}

func TestExperimentCohortConcurrentFirstRunsAgree(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	got := make([]int, 8)
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i], _ = loadOrAssignExperimentCohort(dir)
		}(i)
	}
	wg.Wait()
	for _, v := range got {
		if v != got[0] {
			t.Fatalf("concurrent first runs disagree: %v", got)
		}
	}
}

func TestExperimentCohortKeepsDesktopAssignment(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, experimentCohortFile), []byte("42\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, ok := loadOrAssignExperimentCohort(dir); !ok || got != 42 {
		t.Fatalf("cohort = %d, %v; want the desktop's 42", got, ok)
	}
}

func TestExperimentCohortInvalidFileIsOmittedNotReplaced(t *testing.T) {
	for _, content := range []string{"", "abc", "100", "-1"} {
		dir := t.TempDir()
		file := filepath.Join(dir, experimentCohortFile)
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, ok := loadOrAssignExperimentCohort(dir); ok {
			t.Fatalf("content %q: cohort = %d, want omitted", content, got)
		}
		if raw, _ := os.ReadFile(file); string(raw) != content {
			t.Fatalf("content %q was rewritten to %q", content, raw)
		}
	}
}

func TestPostHogPropertiesCarryExperimentCohort(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, experimentCohortFile), []byte("7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sink, err := NewPostHogSink(dir, "phc_test", "https://us.i.posthog.com", "0.20.0", "", roundTripClient(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sink.Close(context.Background()) }()
	props := sink.properties(ports.TelemetryEvent{Name: "ao.session.ended", Source: "delivery_telemetry", Payload: map[string]any{"experiment_cohort": 99}})
	if props["experiment_cohort"] != 7 {
		t.Fatalf("experiment_cohort = %#v, want 7 from the install, not the payload", props["experiment_cohort"])
	}
}
