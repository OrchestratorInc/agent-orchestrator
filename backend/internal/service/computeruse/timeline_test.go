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
		s.recordCall(scope, "press_key", map[string]any{"key": fmt.Sprintf("%d-%s", index, padding)}, nil, nil)
	}
	info, err := os.Stat(filepath.Join(scope, timelineFile))
	if err != nil || info.Size() > compactTimelineBytes+4096 {
		t.Fatalf("timeline size = %v, %v", info, err)
	}
	entries, _ := s.Timeline(context.Background(), "ao-7")
	if len(entries) > keepTimelineEntries || len(entries) == 0 {
		t.Fatalf("kept %d entries", len(entries))
	}
	last, _ := entries[len(entries)-1].Arguments["key"].(string)
	if !strings.HasPrefix(last, fmt.Sprintf("%d-", total-1)) {
		t.Fatalf("newest entry = %.20q", last)
	}
}

func TestTimelineLeavesOutTypedText(t *testing.T) {
	s := newTestService(t, &fakeDriver{daemonUp: true}, "darwin")
	scope, err := s.images.scope("ao-9")
	if err != nil {
		t.Fatal(err)
	}
	steps := []any{
		map[string]any{"tool": "type_text", "args": map[string]any{"text": "hunter2", "pid": 7}},
		map[string]any{"set_value": map[string]any{"value": "hunter3", "name": "Password"}},
		map[string]any{"tool": "click", "args": map[string]any{"name": "Sign in"}},
	}
	s.recordCall(scope, "type_text", map[string]any{"text": "hunter1", "pid": 7}, nil, nil)
	s.recordCall(scope, "set_value", map[string]any{"value": "hunter4", "name": "Password"}, nil, nil)
	s.recordCall(scope, "run_steps", map[string]any{"pid": 7, "steps": steps}, nil, nil)

	data, err := os.ReadFile(filepath.Join(scope, timelineFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "hunter") {
		t.Fatalf("typed text was logged: %s", data)
	}
	entries, _ := s.Timeline(context.Background(), "ao-9")
	if len(entries) != 3 || entries[0].Arguments["pid"] == nil || entries[1].Arguments["name"] != "Password" {
		t.Fatalf("entries = %+v", entries)
	}
	logged, _ := entries[2].Arguments["steps"].([]any)
	click, _ := logged[2].(map[string]any)
	if args, _ := click["args"].(map[string]any); len(logged) != 3 || args["name"] != "Sign in" {
		t.Fatalf("steps = %v", logged)
	}
	// The agent's own arguments are left as they were sent.
	if first, _ := steps[0].(map[string]any)["args"].(map[string]any); first["text"] != "hunter2" {
		t.Fatalf("request arguments changed: %v", steps)
	}
}

func TestTimelineIsDatedWhenTheCallFinished(t *testing.T) {
	driver := screenshotDriver()
	next := driver.onCall
	driver.onCall = func(args []string) ([]byte, []byte, error) {
		time.Sleep(50 * time.Millisecond)
		return next(args)
	}
	s := newTestService(t, driver, "darwin")
	started := time.Now()
	if _, err := s.Call(context.Background(), CallRequest{Tool: "list_windows", SessionID: "ao-10"}); err != nil {
		t.Fatal(err)
	}
	entries, _ := s.Timeline(context.Background(), "ao-10")
	if len(entries) != 1 || entries[0].At.Sub(started) < 50*time.Millisecond {
		t.Fatalf("entry dated %v, call started %v", entries, started)
	}
}

func TestTimelineRereadsOnlyWhatChanged(t *testing.T) {
	s := newTestService(t, &fakeDriver{daemonUp: true}, "darwin")
	scope, err := s.images.scope("ao-11")
	if err != nil {
		t.Fatal(err)
	}
	s.recordCall(scope, "list_windows", nil, nil, nil)
	first, _ := s.Timeline(context.Background(), "ao-11")
	again, _ := s.Timeline(context.Background(), "ao-11")
	if len(first) != 1 || len(again) != 1 || again[0].ID != first[0].ID {
		t.Fatalf("first = %+v, again = %+v", first, again)
	}
	s.recordCall(scope, "click", nil, nil, nil)
	after, _ := s.Timeline(context.Background(), "ao-11")
	if len(after) != 2 || after[1].Tool != "click" {
		t.Fatalf("after a new call = %+v", after)
	}
	// A screenshot pruned from the folder drops out of the entry that named it.
	name := "shot.png"
	if err := os.WriteFile(filepath.Join(scope, name), pngHeader, 0o600); err != nil {
		t.Fatal(err)
	}
	s.recordCall(scope, "get_window_state", nil, []Image{{Path: filepath.Join(scope, name)}}, nil)
	withShot, _ := s.Timeline(context.Background(), "ao-11")
	if got := withShot[len(withShot)-1].Screenshots; len(got) != 1 {
		t.Fatalf("screenshots = %v", got)
	}
	if err := os.Remove(filepath.Join(scope, name)); err != nil {
		t.Fatal(err)
	}
	pruned, _ := s.Timeline(context.Background(), "ao-11")
	if got := pruned[len(pruned)-1].Screenshots; len(got) != 0 {
		t.Fatalf("pruned screenshot still listed: %v", got)
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
