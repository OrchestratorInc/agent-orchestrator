package computeruse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func screenshotDriver() *fakeDriver {
	driver := &fakeDriver{daemonUp: true}
	driver.onCall = func(args []string) ([]byte, []byte, error) {
		var sent map[string]any
		if err := json.Unmarshal([]byte(args[2]), &sent); err != nil {
			return nil, nil, err
		}
		if path, _ := sent["screenshot_out_file"].(string); path != "" {
			if err := os.WriteFile(path, pngHeader, 0o600); err != nil {
				return nil, nil, err
			}
		}
		return []byte(`{"elements":[]}`), nil, nil
	}
	return driver
}

func TestCallRecordsTimelineWithScreenshots(t *testing.T) {
	s := newTestService(t, screenshotDriver(), "darwin")
	result, err := s.Call(context.Background(), CallRequest{
		Tool:      "get_window_state",
		Arguments: map[string]any{"pid": 7, "window_id": 9, "screenshot_out_file": "/etc/elsewhere.png"},
		SessionID: "ao-12",
	})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}

	entries, err := s.Timeline(context.Background(), "ao-12")
	if err != nil {
		t.Fatalf("Timeline() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %+v", entries)
	}
	entry := entries[0]
	if entry.Tool != "get_window_state" || !entry.OK || entry.Error != "" || entry.ID == "" || entry.At.IsZero() {
		t.Fatalf("entry = %+v", entry)
	}
	if _, ok := entry.Arguments["screenshot_out_file"]; ok {
		t.Fatalf("AO's own argument recorded: %v", entry.Arguments)
	}
	if _, ok := entry.Arguments["session"]; ok {
		t.Fatalf("driver session recorded: %v", entry.Arguments)
	}
	if entry.Arguments["pid"] != float64(7) && entry.Arguments["pid"] != 7 {
		t.Fatalf("arguments = %v", entry.Arguments)
	}
	want := filepath.Base(result.Images[0].Path)
	if len(entry.Screenshots) != 1 || entry.Screenshots[0] != want {
		t.Fatalf("screenshots = %v, want [%s]", entry.Screenshots, want)
	}
	path, err := s.ScreenshotPath(context.Background(), "ao-12", want)
	if err != nil || filepath.Base(path) != want {
		t.Fatalf("ScreenshotPath() = %q, %v", path, err)
	}
}

func TestCallRecordsFailedCalls(t *testing.T) {
	driver := &fakeDriver{daemonUp: true}
	driver.onCall = func([]string) ([]byte, []byte, error) {
		return nil, []byte("window_id_not_found: window 9 no longer exists"), errors.New("exit status 1")
	}
	s := newTestService(t, driver, "darwin")
	if _, err := s.Call(context.Background(), CallRequest{Tool: "click", Arguments: map[string]any{"pid": 1}, SessionID: "ao-3"}); err == nil {
		t.Fatal("Call() error = nil")
	}
	entries, err := s.Timeline(context.Background(), "ao-3")
	if err != nil {
		t.Fatalf("Timeline() error = %v", err)
	}
	if len(entries) != 1 || entries[0].OK || !strings.Contains(entries[0].Error, "window_id_not_found") || len(entries[0].Screenshots) != 0 {
		t.Fatalf("entries = %+v", entries)
	}
}

// A call refused before it reaches the driver is not a step the agent took.
func TestCallLeavesRefusedCallsOutOfTheTimeline(t *testing.T) {
	s := newTestService(t, &fakeDriver{daemonUp: true}, "darwin")
	_, _ = s.Call(context.Background(), CallRequest{Tool: "zoom", SessionID: "ao-4"})
	entries, err := s.Timeline(context.Background(), "ao-4")
	if err != nil || len(entries) != 0 {
		t.Fatalf("Timeline() = %+v, %v", entries, err)
	}
}

func TestTimelineOfSessionWithoutCalls(t *testing.T) {
	s := newTestService(t, &fakeDriver{daemonUp: true}, "darwin")
	entries, err := s.Timeline(context.Background(), "ao-never")
	if err != nil || entries == nil || len(entries) != 0 {
		t.Fatalf("Timeline() = %#v, %v", entries, err)
	}
	if _, err := os.Stat(filepath.Join(s.images.root, "ao-never")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reading a timeline created the session folder: %v", err)
	}
	var apiErr *apierr.Error
	if _, err := s.Timeline(context.Background(), "../etc"); !errors.As(err, &apiErr) || apiErr.Code != "COMPUTER_USE_INVALID_SESSION" {
		t.Fatalf("Timeline(../etc) error = %v", err)
	}
}

func TestTimelineDropsPrunedScreenshots(t *testing.T) {
	s := newTestService(t, screenshotDriver(), "darwin")
	result, err := s.Call(context.Background(), CallRequest{Tool: "get_window_state", Arguments: map[string]any{"pid": 1, "window_id": 2}, SessionID: "ao-5"})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if err := os.Remove(result.Images[0].Path); err != nil {
		t.Fatal(err)
	}
	entries, _ := s.Timeline(context.Background(), "ao-5")
	if len(entries) != 1 || len(entries[0].Screenshots) != 0 {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestScreenshotPathRefusesOtherFiles(t *testing.T) {
	s := newTestService(t, screenshotDriver(), "darwin")
	if _, err := s.Call(context.Background(), CallRequest{Tool: "get_window_state", Arguments: map[string]any{"pid": 1, "window_id": 2}, SessionID: "ao-6"}); err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	for _, name := range []string{timelineFile, "../ao-6/x.png", "a/b.png", "..png", "missing.png", ""} {
		var apiErr *apierr.Error
		if _, err := s.ScreenshotPath(context.Background(), "ao-6", name); !errors.As(err, &apiErr) || apiErr.Code != "COMPUTER_USE_SCREENSHOT_NOT_FOUND" {
			t.Fatalf("ScreenshotPath(%q) error = %v", name, err)
		}
	}
}

func TestTimelineIsCompactedToTheNewestEntries(t *testing.T) {
	s := newTestService(t, &fakeDriver{daemonUp: true}, "darwin")
	scope, err := s.images.scope("ao-7")
	if err != nil {
		t.Fatal(err)
	}
	padding := strings.Repeat("x", 2048)
	total := compactTimelineBytes/2048 + keepTimelineEntries
	for index := 0; index < total; index++ {
		s.recordCall(scope, "type_text", map[string]any{"text": fmt.Sprintf("%d-%s", index, padding)}, time.Now(), nil, nil)
	}
	info, err := os.Stat(filepath.Join(scope, timelineFile))
	if err != nil || info.Size() > compactTimelineBytes+4096 {
		t.Fatalf("timeline size = %v, %v", info, err)
	}
	entries, _ := s.Timeline(context.Background(), "ao-7")
	if len(entries) > keepTimelineEntries || len(entries) == 0 {
		t.Fatalf("kept %d entries", len(entries))
	}
	last, _ := entries[len(entries)-1].Arguments["text"].(string)
	if !strings.HasPrefix(last, fmt.Sprintf("%d-", total-1)) {
		t.Fatalf("newest entry = %.20q", last)
	}
}

func TestPruneKeepsTheTimeline(t *testing.T) {
	store := newImageStore(t.TempDir())
	dir, err := store.scope("ao-8")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, timelineFile), []byte("{}\n"), 0o600)
	for index := 0; index < keepImages+5; index++ {
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("%05d.png", index)), nil, 0o600)
	}
	store.prune(dir)
	if _, err := os.Stat(filepath.Join(dir, timelineFile)); err != nil {
		t.Fatalf("timeline pruned: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != keepImages+1 {
		t.Fatalf("kept %d files", len(entries))
	}
}

// A batch whose step failed still hands the agent its report, but the
// timeline shows the step as failed, with which step and why.
func TestTimelineRecordsAFailedBatchAsFailed(t *testing.T) {
	driver := &fakeDriver{daemonUp: true}
	report := `{"ok":false,"executed":1,"failed_step":1,"total":2,"steps":[{"index":0,"tool":"click","ok":true},{"index":1,"tool":"type_text","ok":false,"phase":"find","message":"no text field"}]}`
	driver.onCall = func(args []string) ([]byte, []byte, error) {
		if args[1] == "run_steps" {
			return []byte(report), []byte("step 2 failed"), errors.New("exit status 1")
		}
		return []byte(`{"elements":[]}`), nil, nil
	}
	s := newTestService(t, driver, "darwin")
	result, err := s.Call(context.Background(), CallRequest{
		Tool:      "run_steps",
		Arguments: map[string]any{"pid": 7, "window_id": 9, "steps": []any{map[string]any{"tool": "click", "args": map[string]any{}}}},
		SessionID: "ao-12",
	})
	if err != nil {
		t.Fatalf("Call() error = %v, want the report", err)
	}
	if _, ok := result.Result.(map[string]any)["steps"]; !ok {
		t.Fatalf("result = %+v, want the step report", result.Result)
	}
	entries, err := s.Timeline(context.Background(), "ao-12")
	if err != nil || len(entries) != 1 {
		t.Fatalf("Timeline() = %+v, %v", entries, err)
	}
	if entries[0].OK || !strings.Contains(entries[0].Error, "step 2 (type_text): no text field") {
		t.Fatalf("entry = %+v, want a failed step with its reason", entries[0])
	}
}
