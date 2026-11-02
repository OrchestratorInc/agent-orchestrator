package computeruse

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// pngHeader is enough for http.DetectContentType to call it image/png.
var pngHeader = []byte("\x89PNG\r\n\x1a\n0000")

type fakeDriver struct {
	mu       sync.Mutex
	missing  bool
	daemonUp bool
	startErr error
	calls    [][]string
	onCall   func(args []string) ([]byte, []byte, error)
	started  int
	startsUp bool
}

func (f *fakeDriver) Locate(context.Context) (string, error) {
	if f.missing {
		return "", ports.ErrComputerUseDriverNotFound
	}
	return "/opt/cua-driver", nil
}

func (f *fakeDriver) Run(_ context.Context, _ string, args ...string) ([]byte, []byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, args)
	up := f.daemonUp
	f.mu.Unlock()
	switch args[0] {
	case "--version":
		return []byte("cua-driver 0.28.2\n"), nil, nil
	case "status":
		if up {
			return []byte("Cua Driver daemon is running\n  pid: 1\n"), nil, nil
		}
		return nil, []byte("Cua Driver daemon is not running.\n"), errors.New("exit status 1")
	case "check-update":
		return []byte(`{"latest_version":"0.37.0","update_available":true,"release_notes_url":"https://example.test/notes"}`), nil, nil
	case "permissions":
		return []byte(`{"accessibility":true,"screen_recording":false}`), nil, nil
	}
	if f.onCall != nil {
		return f.onCall(args)
	}
	return []byte(`{}`), nil, nil
}

func (f *fakeDriver) StartDaemon(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started++
	if f.startErr != nil {
		return f.startErr
	}
	if f.startsUp {
		f.daemonUp = true
	}
	return nil
}

func newTestService(t *testing.T, driver *fakeDriver, goos string) *Service {
	t.Helper()
	s := New(driver, t.TempDir())
	s.goos = goos
	s.SetEnabled(func(context.Context) bool { return true })
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}

func TestStatusNotInstalledIsNotAnError(t *testing.T) {
	s := newTestService(t, &fakeDriver{missing: true}, "darwin")
	status, err := s.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Installed || status.Daemon != DaemonUnknown || status.Platform != "darwin" {
		t.Fatalf("status = %+v", status)
	}
	if !strings.HasSuffix(status.ImageDirectory, "computer-use") {
		t.Fatalf("ImageDirectory = %q", status.ImageDirectory)
	}
}

func TestStatusReportsVersionDaemonUpdateAndPermissions(t *testing.T) {
	s := newTestService(t, &fakeDriver{daemonUp: true}, "darwin")
	status, err := s.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if !status.Installed || status.BinaryPath != "/opt/cua-driver" || status.Version != "0.28.2" {
		t.Fatalf("status = %+v", status)
	}
	if status.Daemon != DaemonRunning {
		t.Fatalf("Daemon = %q", status.Daemon)
	}
	if !status.UpdateAvailable || status.LatestVersion != "0.37.0" || status.ReleaseNotesURL == "" {
		t.Fatalf("update = %+v", status)
	}
	if status.Permissions == nil || status.Permissions.Accessibility != PermissionGranted || status.Permissions.ScreenRecording != PermissionMissing {
		t.Fatalf("Permissions = %+v", status.Permissions)
	}
}

// Permissions are read from the running service; with it stopped the driver
// could only report the caller's grants, which are not CuaDriver.app's.
func TestStatusPermissionsUnknownWhileDaemonStopped(t *testing.T) {
	s := newTestService(t, &fakeDriver{}, "darwin")
	status, _ := s.Status(context.Background())
	if status.Daemon != DaemonStopped {
		t.Fatalf("Daemon = %q", status.Daemon)
	}
	if status.Permissions.Accessibility != PermissionUnknown || status.Permissions.ScreenRecording != PermissionUnknown {
		t.Fatalf("Permissions = %+v", status.Permissions)
	}
}

func TestStatusOmitsPermissionsOffMacOS(t *testing.T) {
	s := newTestService(t, &fakeDriver{daemonUp: true}, "linux")
	status, _ := s.Status(context.Background())
	if status.Permissions != nil {
		t.Fatalf("Permissions = %+v, want nil on linux", status.Permissions)
	}
}

func TestGrantPermissionsOnlyOnMacOS(t *testing.T) {
	s := newTestService(t, &fakeDriver{daemonUp: true}, "windows")
	_, err := s.GrantPermissions(context.Background())
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindInvalid {
		t.Fatalf("err = %v, want invalid", err)
	}
}

// The core promise: a screenshot tool is pointed at a file under AO's data
// directory, and a path the caller supplied is replaced.
func TestCallWritesScreenshotsToFiles(t *testing.T) {
	driver := &fakeDriver{daemonUp: true}
	var sent map[string]any
	driver.onCall = func(args []string) ([]byte, []byte, error) {
		if err := json.Unmarshal([]byte(args[2]), &sent); err != nil {
			return nil, nil, err
		}
		path, _ := sent["screenshot_out_file"].(string)
		if err := os.WriteFile(path, pngHeader, 0o600); err != nil {
			return nil, nil, err
		}
		return []byte(fmt.Sprintf(`{"screenshot_file_path":%q,"elements":[]}`, path)), nil, nil
	}
	s := newTestService(t, driver, "darwin")

	result, err := s.Call(context.Background(), CallRequest{
		Tool:      "get_window_state",
		Arguments: map[string]any{"pid": 7, "window_id": 9, "screenshot_out_file": "/etc/elsewhere.png"},
		SessionID: "ao-12",
	})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	path, _ := sent["screenshot_out_file"].(string)
	wantDir, _ := filepath.EvalSymlinks(filepath.Join(s.images.root, "ao-12"))
	if filepath.Dir(path) != wantDir || !strings.HasSuffix(path, "-get_window_state-1.png") {
		t.Fatalf("screenshot_out_file = %q, want a file in %q", path, wantDir)
	}
	if len(result.Images) != 1 || result.Images[0].Path != path || result.Images[0].MimeType != "image/png" {
		t.Fatalf("Images = %+v", result.Images)
	}
}

func TestConcurrentReadsGetDistinctScreenshotFiles(t *testing.T) {
	const reads = 32
	driver := &fakeDriver{daemonUp: true}
	var mu sync.Mutex
	paths := map[string]bool{}
	driver.onCall = func(args []string) ([]byte, []byte, error) {
		var sent map[string]any
		if err := json.Unmarshal([]byte(args[2]), &sent); err != nil {
			return nil, nil, err
		}
		path, _ := sent["screenshot_out_file"].(string)
		mu.Lock()
		paths[path] = true
		mu.Unlock()
		return []byte(`{"elements":[]}`), nil, nil
	}
	s := newTestService(t, driver, "darwin")

	var wg sync.WaitGroup
	for range reads {
		wg.Go(func() {
			if _, err := s.Call(context.Background(), CallRequest{Tool: "get_window_state", Arguments: map[string]any{"pid": 7, "window_id": 9}, SessionID: "ao-12"}); err != nil {
				t.Errorf("Call() error = %v", err)
			}
		})
	}
	wg.Wait()
	if len(paths) != reads {
		t.Fatalf("%d concurrent reads used %d distinct screenshot files", reads, len(paths))
	}
}

// statefulDriver answers like Cua Driver's session model: a call without a
// `session` runs in a fresh implicit session, and a pixel click needs a window
// read by the same session. It refuses a `session` argument for
// sessionlessTool.
type statefulDriver struct {
	read            map[string]bool
	sessionlessTool string
	sessions        []any
}

func (d *statefulDriver) call(args []string) ([]byte, []byte, error) {
	var sent map[string]any
	if err := json.Unmarshal([]byte(args[2]), &sent); err != nil {
		return nil, nil, err
	}
	session, named := sent["session"]
	d.sessions = append(d.sessions, session)
	tool := args[1]
	if tool == d.sessionlessTool && named {
		return []byte(fmt.Sprintf(`{"status":"refused","refusal":{"code":"invalid_arguments","message":"%s: unknown argument session; accepted: "}}`, tool)), nil, errors.New("exit status 1")
	}
	label, _ := session.(string)
	switch tool {
	case "get_window_state":
		if named {
			d.read[label] = true
		}
		return []byte(`{"elements":[]}`), nil, nil
	case "click":
		if !named || !d.read[label] {
			return []byte(`{"code":"screenshot_context_missing","reason":"window_never_read"}`), nil, errors.New("exit status 1")
		}
	}
	return []byte(`{}`), nil, nil
}

func newStatefulService(t *testing.T) (*Service, *statefulDriver) {
	t.Helper()
	state := &statefulDriver{read: map[string]bool{}, sessionlessTool: "list_windows"}
	driver := &fakeDriver{daemonUp: true, onCall: state.call}
	return newTestService(t, driver, "darwin"), state
}

var snapshotArgs = map[string]any{"pid": 7, "window_id": 9, "include_screenshot": false}

// Separate calls read a window and then pixel-click it, each in its own
// driver process, so both must run in one driver session tied to the AO
// session.
func TestCallKeepsOneDriverSessionPerAOSession(t *testing.T) {
	s, state := newStatefulService(t)
	ctx := context.Background()
	pixelClick := map[string]any{"pid": 7, "window_id": 9, "x": 5, "y": 5}

	if _, err := s.Call(ctx, CallRequest{Tool: "get_window_state", Arguments: snapshotArgs, SessionID: "worker-1"}); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if _, err := s.Call(ctx, CallRequest{Tool: "click", Arguments: pixelClick, SessionID: "worker-1"}); err != nil {
		t.Fatalf("pixel click after a read in the same AO session: %v", err)
	}
	if _, err := s.Call(ctx, CallRequest{Tool: "click", Arguments: pixelClick, SessionID: "worker-2"}); err == nil || !strings.Contains(err.Error(), "screenshot_context_missing") {
		t.Fatalf("pixel click from another AO session err = %v, want screenshot_context_missing", err)
	}
	// The read, the click, AO's read after the click, then worker-2's refused click.
	want := []any{"ao-worker-1", "ao-worker-1", "ao-worker-1", "ao-worker-2"}
	if fmt.Sprint(state.sessions) != fmt.Sprint(want) {
		t.Fatalf("driver sessions = %v, want %v", state.sessions, want)
	}
}

func TestCallOutsideAOSessionUsesSharedDriverSession(t *testing.T) {
	s, state := newStatefulService(t)
	if _, err := s.Call(context.Background(), CallRequest{Tool: "get_window_state", Arguments: snapshotArgs}); err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if len(state.sessions) != 1 || state.sessions[0] != "ao-shared" {
		t.Fatalf("driver sessions = %v, want [ao-shared]", state.sessions)
	}
}

func TestCallKeepsCallerDriverSession(t *testing.T) {
	s, state := newStatefulService(t)
	arguments := map[string]any{"pid": 7, "window_id": 9, "session": "research-run"}
	if _, err := s.Call(context.Background(), CallRequest{Tool: "get_window_state", Arguments: arguments, SessionID: "worker-1"}); err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if len(state.sessions) != 1 || state.sessions[0] != "research-run" {
		t.Fatalf("driver sessions = %v, want [research-run]", state.sessions)
	}
}

// A tool whose input has no `session` is sent again without it, and later
// calls leave it out from the start.
func TestCallDropsSessionForToolsThatRefuseIt(t *testing.T) {
	s, state := newStatefulService(t)
	for range 2 {
		if _, err := s.Call(context.Background(), CallRequest{Tool: "list_windows", SessionID: "worker-1"}); err != nil {
			t.Fatalf("Call() error = %v", err)
		}
	}
	want := []any{"ao-worker-1", nil, nil}
	if fmt.Sprint(state.sessions) != fmt.Sprint(want) {
		t.Fatalf("driver sessions = %v, want %v", state.sessions, want)
	}
}

func TestCallLeavesTreeOnlySnapshotsAlone(t *testing.T) {
	driver := &fakeDriver{daemonUp: true}
	var sent map[string]any
	driver.onCall = func(args []string) ([]byte, []byte, error) {
		_ = json.Unmarshal([]byte(args[2]), &sent)
		return []byte(`{"elements":[]}`), nil, nil
	}
	s := newTestService(t, driver, "darwin")
	result, err := s.Call(context.Background(), CallRequest{Tool: "get_window_state", Arguments: map[string]any{"pid": 1, "window_id": 2, "include_screenshot": false, "screenshot_out_file": "/tmp/x.png"}})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if _, ok := sent["screenshot_out_file"]; ok {
		t.Fatalf("screenshot_out_file forwarded on a tree-only call: %v", sent)
	}
	if result.Images == nil || len(result.Images) != 0 {
		t.Fatalf("Images = %#v, want an empty list", result.Images)
	}
}

// A tool that prints an image as base64 instead of writing a file gets it
// decoded to a file; the base64 is never passed through.
func TestCallExternalizesInlineImages(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString(pngHeader)
	driver := &fakeDriver{daemonUp: true}
	driver.onCall = func([]string) ([]byte, []byte, error) {
		return []byte(fmt.Sprintf(`{"width":480,"screenshot_png_b64":%q,"content":[{"type":"image","data":%q,"mimeType":"image/png"},{"type":"text","text":"ok"}],"broken_b64":"%%%%"}`, encoded, encoded)), nil, nil
	}
	s := newTestService(t, driver, "linux")
	result, err := s.Call(context.Background(), CallRequest{Tool: "list_windows"})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	raw, _ := json.Marshal(result.Result)
	if strings.Contains(string(raw), encoded) || strings.Contains(string(raw), "_b64") {
		t.Fatalf("result still carries inline image data: %s", raw)
	}
	out := result.Result.(map[string]any)
	path, _ := out["screenshot_png_file_path"].(string)
	if data, err := os.ReadFile(path); err != nil || string(data) != string(pngHeader) {
		t.Fatalf("screenshot_png_file_path %q: %v", path, err)
	}
	block := out["content"].([]any)[0].(map[string]any)
	if _, ok := block["path"].(string); !ok {
		t.Fatalf("image block = %+v, want a path", block)
	}
	if out["broken_error"] == nil {
		t.Fatalf("undecodable image should report an error: %+v", out)
	}
	if len(result.Images) != 2 {
		t.Fatalf("Images = %+v", result.Images)
	}
}

func TestCallWrapsNonJSONOutput(t *testing.T) {
	driver := &fakeDriver{daemonUp: true}
	driver.onCall = func([]string) ([]byte, []byte, error) { return []byte("\x1b[32mdone\x1b[0m\n"), nil, nil }
	s := newTestService(t, driver, "linux")
	result, err := s.Call(context.Background(), CallRequest{Tool: "press_key"})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if text := result.Result.(map[string]any)["text"]; text != "done" {
		t.Fatalf("text = %q", text)
	}
}

func TestCallStartsStoppedDaemonAndRetries(t *testing.T) {
	driver := &fakeDriver{startsUp: true}
	attempts := 0
	driver.onCall = func([]string) ([]byte, []byte, error) {
		attempts++
		driver.mu.Lock()
		up := driver.daemonUp
		driver.mu.Unlock()
		if !up {
			return nil, []byte("Cua Driver daemon is not running. Start it with: open -n -g -a CuaDriver --args serve"), errors.New("exit status 1")
		}
		return []byte(`{"apps":[]}`), nil, nil
	}
	s := newTestService(t, driver, "darwin")
	if _, err := s.Call(context.Background(), CallRequest{Tool: "list_windows"}); err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if driver.started != 1 || attempts != 2 {
		t.Fatalf("started = %d, attempts = %d", driver.started, attempts)
	}
}

func TestCallReportsDaemonStartFailure(t *testing.T) {
	driver := &fakeDriver{startErr: errors.New("no desktop session")}
	driver.onCall = func([]string) ([]byte, []byte, error) {
		return nil, []byte("Cua Driver daemon is not running."), errors.New("exit status 1")
	}
	s := newTestService(t, driver, "linux")
	_, err := s.Call(context.Background(), CallRequest{Tool: "list_windows"})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "COMPUTER_USE_DAEMON_START_FAILED" || !strings.Contains(apiErr.Message, "no desktop session") {
		t.Fatalf("err = %v", err)
	}
}

func TestCallSurfacesDriverErrors(t *testing.T) {
	driver := &fakeDriver{daemonUp: true}
	driver.onCall = func([]string) ([]byte, []byte, error) {
		return nil, []byte("window_id_not_found: window 9 no longer exists"), errors.New("exit status 1")
	}
	s := newTestService(t, driver, "darwin")
	_, err := s.Call(context.Background(), CallRequest{Tool: "click", Arguments: map[string]any{"pid": 1}})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "COMPUTER_USE_CALL_FAILED" || !strings.Contains(apiErr.Message, "window_id_not_found") {
		t.Fatalf("err = %v", err)
	}
}

func TestCallValidatesInput(t *testing.T) {
	s := newTestService(t, &fakeDriver{daemonUp: true}, "darwin")
	for name, request := range map[string]CallRequest{
		"empty tool":       {},
		"flag injection":   {Tool: "--help"},
		"path tool":        {Tool: "../x"},
		"session traverse": {Tool: "list_windows", SessionID: "../../etc"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := s.Call(context.Background(), request)
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindInvalid {
				t.Fatalf("err = %v, want invalid", err)
			}
		})
	}
}

func TestCallWhenNotInstalled(t *testing.T) {
	s := newTestService(t, &fakeDriver{missing: true}, "darwin")
	_, err := s.Call(context.Background(), CallRequest{Tool: "list_windows"})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "COMPUTER_USE_NOT_INSTALLED" {
		t.Fatalf("err = %v", err)
	}
}

// The call route runs only the tools agents are offered: not Cua Driver's
// browser tools, scripts, whole-screen capture, or the step-only actions.
func TestCallRefusesToolsAONotOffers(t *testing.T) {
	driver := &fakeDriver{daemonUp: true}
	s := newTestService(t, driver, "darwin")
	for _, tool := range []string{"browser_click", "get_browser_state", "page", "run_script", "get_desktop_state", "list_apps", "zoom", "hotkey", "kill_app"} {
		t.Run(tool, func(t *testing.T) {
			_, err := s.Call(context.Background(), CallRequest{Tool: tool})
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) || apiErr.Code != "COMPUTER_USE_TOOL_NOT_OFFERED" || apiErr.Kind != apierr.KindForbidden {
				t.Fatalf("err = %v, want COMPUTER_USE_TOOL_NOT_OFFERED", err)
			}
		})
	}
	if len(driver.calls) != 0 {
		t.Fatalf("driver ran %v for a tool AO does not offer", driver.calls)
	}
}

// Calls run only while "Let agents use this computer" is on; a service the
// daemon never wired counts as off.
func TestCallRefusesWhileComputerUseIsOff(t *testing.T) {
	driver := &fakeDriver{daemonUp: true}
	s := newTestService(t, driver, "darwin")
	unwired := New(driver, t.TempDir())
	t.Cleanup(func() { _ = unwired.Close(context.Background()) })
	s.SetEnabled(func(context.Context) bool { return false })
	for name, service := range map[string]*Service{"off": s, "unwired": unwired} {
		_, err := service.Call(context.Background(), CallRequest{Tool: "list_windows"})
		var apiErr *apierr.Error
		if !errors.As(err, &apiErr) || apiErr.Code != "COMPUTER_USE_DISABLED" || apiErr.Kind != apierr.KindForbidden {
			t.Fatalf("%s: err = %v, want COMPUTER_USE_DISABLED", name, err)
		}
	}
	if len(driver.calls) != 0 {
		t.Fatalf("driver ran %v while computer use was off", driver.calls)
	}
}

// Cua Driver refuses an output path under a symlinked folder, such as macOS's
// /tmp, so the scope folder is handed over resolved.
func TestScopeResolvesSymlinks(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "data")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	dir, err := newImageStore(link).scope("ao-1")
	if err != nil {
		t.Fatalf("scope() error = %v", err)
	}
	want, _ := filepath.EvalSymlinks(filepath.Join(target, "computer-use", "ao-1"))
	if dir != want {
		t.Fatalf("scope = %q, want resolved %q", dir, want)
	}
}

func TestPruneKeepsNewestImages(t *testing.T) {
	store := newImageStore(t.TempDir())
	dir, err := store.scope("")
	if err != nil {
		t.Fatalf("scope() error = %v", err)
	}
	if filepath.Base(dir) != sharedScope {
		t.Fatalf("scope = %q", dir)
	}
	for index := 0; index < keepImages+5; index++ {
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("%05d.png", index)), nil, 0o600)
	}
	store.prune(dir)
	entries, _ := os.ReadDir(dir)
	if len(entries) != keepImages || entries[0].Name() != "00005.png" {
		t.Fatalf("kept %d, oldest %s", len(entries), entries[0].Name())
	}
}

func TestParseVersion(t *testing.T) {
	for input, want := range map[string]string{"cua-driver 0.28.2\n": "0.28.2", "v1.2.3": "1.2.3", "": ""} {
		if got := parseVersion(input); got != want {
			t.Errorf("parseVersion(%q) = %q, want %q", input, got, want)
		}
	}
}

// Actions that name no window or field would land wherever the user is
// working; they are refused before the driver runs.
func TestCallRefusesActionsWithoutATarget(t *testing.T) {
	driver := &fakeDriver{daemonUp: true}
	s := newTestService(t, driver, "darwin")
	for name, tc := range map[string]struct {
		tool      string
		arguments map[string]any
		code      string
	}{
		"pixel click without window": {"click", map[string]any{"x": 275, "y": 351}, "COMPUTER_USE_WINDOW_REQUIRED"},
		"pixel click with pid only":  {"click", map[string]any{"pid": 7, "x": 275, "y": 351}, "COMPUTER_USE_WINDOW_REQUIRED"},
		"drag without window":        {"run_steps", map[string]any{"steps": []any{map[string]any{"drag": map[string]any{"from_x": 1, "from_y": 1, "to_x": 9, "to_y": 9}}}}, "COMPUTER_USE_WINDOW_REQUIRED"},
		"desktop scope":              {"click", map[string]any{"scope": "desktop", "x": 1, "y": 1}, "COMPUTER_USE_DESKTOP_SCOPE_DISABLED"},
		"desktop target":             {"press_key", map[string]any{"key": "return", "target": map[string]any{"kind": "desktop", "display_id": "primary"}}, "COMPUTER_USE_DESKTOP_SCOPE_DISABLED"},
		"desktop scope with token":   {"type_text", map[string]any{"element_token": "s1:2", "text": "hi", "scope": "desktop"}, "COMPUTER_USE_DESKTOP_SCOPE_DISABLED"},
		"type into focus":            {"type_text", map[string]any{"pid": 7, "window_id": 9, "text": "Nihal"}, "COMPUTER_USE_TEXT_TARGET_REQUIRED"},
		"run_steps pixel step":       {"run_steps", map[string]any{"steps": []any{map[string]any{"tool": "click", "args": map[string]any{"x": 1, "y": 1}}}}, "COMPUTER_USE_WINDOW_REQUIRED"},
		"run_steps type into focus":  {"run_steps", map[string]any{"app": "WhatsApp", "steps": []any{map[string]any{"type_text": map[string]any{"text": "hi"}}}}, "COMPUTER_USE_TEXT_TARGET_REQUIRED"},
		"run_steps desktop scope":    {"run_steps", map[string]any{"steps": []any{map[string]any{"press_key": map[string]any{"key": "a", "scope": "desktop"}}}}, "COMPUTER_USE_DESKTOP_SCOPE_DISABLED"},
		// A step is checked as the tool the driver runs it as.
		"run_steps type alias into focus": {"run_steps", map[string]any{"pid": 7, "window_id": 9, "steps": []any{map[string]any{"tool": "type", "args": map[string]any{"text": "hi"}}}}, "COMPUTER_USE_TEXT_TARGET_REQUIRED"},
		"run_steps press alias pixel":     {"run_steps", map[string]any{"steps": []any{map[string]any{"press": map[string]any{"x": 1, "y": 1}}}}, "COMPUTER_USE_WINDOW_REQUIRED"},
		"run_steps triple_click pixel":    {"run_steps", map[string]any{"steps": []any{map[string]any{"Triple_Click": map[string]any{"x": 1, "y": 1}}}}, "COMPUTER_USE_WINDOW_REQUIRED"},
		"run_steps keypress desktop":      {"run_steps", map[string]any{"steps": []any{map[string]any{"keypress": map[string]any{"key": "a", "scope": "desktop"}}}}, "COMPUTER_USE_DESKTOP_SCOPE_DISABLED"},
		"run_steps key name desktop":      {"run_steps", map[string]any{"steps": []any{map[string]any{"tool": "Down", "args": map[string]any{"scope": "desktop"}}}}, "COMPUTER_USE_DESKTOP_SCOPE_DISABLED"},
		"run_steps unknown step":          {"run_steps", map[string]any{"pid": 7, "window_id": 9, "steps": []any{map[string]any{"launch_app": map[string]any{"bundle_id": "com.apple.calculator"}}}}, "COMPUTER_USE_STEP_UNSUPPORTED"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := s.Call(context.Background(), CallRequest{Tool: tc.tool, Arguments: tc.arguments})
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) || apiErr.Code != tc.code || apiErr.Kind != apierr.KindInvalid {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
		})
	}
	if len(driver.calls) != 0 {
		t.Fatalf("driver ran %v for a refused action", driver.calls)
	}
}

func TestCallAllowsTargetedActions(t *testing.T) {
	driver := &fakeDriver{daemonUp: true}
	s := newTestService(t, driver, "darwin")
	for name, tc := range map[string]struct {
		tool      string
		arguments map[string]any
	}{
		"click by token":                {"click", map[string]any{"element_token": "s1:2"}},
		"pixel click in window":         {"click", map[string]any{"pid": 7, "window_id": 9, "x": 275, "y": 351}},
		"pixel click via target":        {"click", map[string]any{"target": map[string]any{"kind": "window", "pid": 7, "window_id": 9}, "x": 1, "y": 1}},
		"type into token":               {"type_text", map[string]any{"element_token": "s1:2", "text": "hi"}},
		"type at field pixel":           {"type_text", map[string]any{"pid": 7, "window_id": 9, "x": 5, "y": 5, "text": "hi"}},
		"key to app":                    {"press_key", map[string]any{"pid": 7, "key": "f", "modifiers": []any{"cmd"}}},
		"overlay cursor":                {"run_steps", map[string]any{"steps": []any{map[string]any{"move_cursor": map[string]any{"x": 1, "y": 1}}}}},
		"window read":                   {"get_window_state", map[string]any{"pid": 7, "window_id": 9}},
		"run_steps batch window":        {"run_steps", map[string]any{"pid": 7, "window_id": 9, "steps": []any{map[string]any{"click": map[string]any{"x": 1, "y": 1}}}}},
		"run_steps type by name":        {"run_steps", map[string]any{"steps": []any{map[string]any{"tool": "type_text", "args": map[string]any{"app": "WhatsApp", "role": "textfield", "name": "Search", "text": "Nihal"}}}}},
		"run_steps check-only step":     {"run_steps", map[string]any{"steps": []any{map[string]any{"expect": []any{map[string]any{"text": "Nihal"}}}}}},
		"run_steps type alias by token": {"run_steps", map[string]any{"steps": []any{map[string]any{"type": map[string]any{"element_token": "s1:2", "text": "hi"}}}}},
		"run_steps press by token":      {"run_steps", map[string]any{"steps": []any{map[string]any{"press": map[string]any{"element_token": "s1:2"}}}}},
		"run_steps key name in window":  {"run_steps", map[string]any{"pid": 7, "window_id": 9, "steps": []any{map[string]any{"tool": "down"}}}},
		"run_steps reads between steps": {"run_steps", map[string]any{"pid": 7, "window_id": 9, "steps": []any{map[string]any{"get_window_state": map[string]any{}}, map[string]any{"click": map[string]any{"element_token": "s1:2"}}, map[string]any{"zoom": map[string]any{}}, map[string]any{"take_screenshot": map[string]any{}}}}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.Call(context.Background(), CallRequest{Tool: tc.tool, Arguments: tc.arguments}); err != nil {
				t.Fatalf("Call() error = %v", err)
			}
		})
	}
}

// The driver's own advice for a windowless pixel click is scope "desktop";
// AO says to name the window instead.
func TestCallExplainsDesktopScopeRefusal(t *testing.T) {
	driver := &fakeDriver{daemonUp: true}
	driver.onCall = func([]string) ([]byte, []byte, error) {
		return []byte(`{"status":"refused","refusal":{"code":"desktop_scope_disabled","message":"pass scope=\"desktop\""}}`), nil, errors.New("exit status 1")
	}
	s := newTestService(t, driver, "darwin")
	_, err := s.Call(context.Background(), CallRequest{Tool: "press_key", Arguments: map[string]any{"pid": 7, "window_id": 9, "key": "a"}})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "COMPUTER_USE_CALL_FAILED" || !strings.Contains(apiErr.Message, "pass the pid and window_id") {
		t.Fatalf("err = %v", err)
	}
}

// observingDriver answers get_window_state with a snapshot of window 9 of pid
// 7, writing the requested screenshot file, and records every call.
type observingDriver struct {
	sent    []map[string]any
	tools   []string
	readErr bool
}

func (o *observingDriver) driver() *fakeDriver {
	driver := &fakeDriver{daemonUp: true}
	driver.onCall = func(args []string) ([]byte, []byte, error) {
		var arguments map[string]any
		_ = json.Unmarshal([]byte(args[2]), &arguments)
		o.tools = append(o.tools, args[1])
		o.sent = append(o.sent, arguments)
		if args[1] != "get_window_state" {
			return []byte(`{"effect":"unverifiable"}`), nil, nil
		}
		if o.readErr {
			return nil, []byte("window_id_not_found"), errors.New("exit status 1")
		}
		if path, ok := arguments["screenshot_out_file"].(string); ok {
			_ = os.WriteFile(path, pngHeader, 0o600)
		}
		return []byte(`{"snapshot_id":"s1","pid":7,"window_id":9,"tree_diff":"since s0: 1 changed"}`), nil, nil
	}
	return driver
}

func observation(t *testing.T, result CallResult) map[string]any {
	t.Helper()
	output, _ := result.Result.(map[string]any)
	observed, ok := output["observation"].(map[string]any)
	if !ok {
		t.Fatalf("result has no observation: %#v", result.Result)
	}
	return observed
}

func TestActionsAreFollowedByAWindowRead(t *testing.T) {
	recorder := &observingDriver{}
	s := newTestService(t, recorder.driver(), "darwin")
	result, err := s.Call(context.Background(), CallRequest{Tool: "click", Arguments: map[string]any{"pid": 7, "window_id": 9, "x": 5, "y": 5}, SessionID: "worker-1"})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if fmt.Sprint(recorder.tools) != "[click get_window_state]" {
		t.Fatalf("driver tools = %v", recorder.tools)
	}
	read := recorder.sent[1]
	if read["pid"] != float64(7) || read["window_id"] != float64(9) || read["since"] != "latest" || read["session"] != "ao-worker-1" || read["screenshot_out_file"] == nil {
		t.Fatalf("read arguments = %v", read)
	}
	if observed := observation(t, result); observed["ok"] != true {
		t.Fatalf("observation = %v", observed)
	}
	if len(result.Images) != 1 || result.Images[0].Path != read["screenshot_out_file"] {
		t.Fatalf("images = %v, want the read's screenshot", result.Images)
	}
	if output := result.Result.(map[string]any); output["effect"] != "unverifiable" {
		t.Fatalf("action result changed: %v", output)
	}
}

// A token names a snapshot, not a window; the window comes from the read that
// returned the snapshot.
func TestTokenActionsReadTheSnapshotWindow(t *testing.T) {
	recorder := &observingDriver{}
	s := newTestService(t, recorder.driver(), "darwin")
	ctx := context.Background()
	if _, err := s.Call(ctx, CallRequest{Tool: "get_window_state", Arguments: map[string]any{"pid": 7, "window_id": 9}, SessionID: "worker-1"}); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if _, err := s.Call(ctx, CallRequest{Tool: "type_text", Arguments: map[string]any{"element_token": "s1:4", "text": "hi"}, SessionID: "worker-1"}); err != nil {
		t.Fatalf("type_text: %v", err)
	}
	if read := recorder.sent[2]; recorder.tools[2] != "get_window_state" || read["pid"] != float64(7) || read["window_id"] != float64(9) {
		t.Fatalf("read after token action = %s %v", recorder.tools[2], read)
	}

	// Snapshots belong to a driver session: another AO session's token is unknown.
	result, err := s.Call(ctx, CallRequest{Tool: "click", Arguments: map[string]any{"element_token": "s1:4"}, SessionID: "worker-2"})
	if err != nil {
		t.Fatalf("click: %v", err)
	}
	if observed := observation(t, result); observed["ok"] != false || observed["skipped"] == nil {
		t.Fatalf("observation = %v, want skipped", observed)
	}
	if recorder.tools[len(recorder.tools)-1] != "click" {
		t.Fatalf("driver tools = %v, want no read of an unknown window", recorder.tools)
	}
}

// The action already ran, so a failed read is reported, not returned.
func TestFailedReadKeepsTheActionResult(t *testing.T) {
	recorder := &observingDriver{readErr: true}
	s := newTestService(t, recorder.driver(), "darwin")
	result, err := s.Call(context.Background(), CallRequest{Tool: "press_key", Arguments: map[string]any{"pid": 7, "window_id": 9, "key": "return"}})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if observed := observation(t, result); observed["ok"] != false || !strings.Contains(fmt.Sprint(observed["error"]), "window_id_not_found") {
		t.Fatalf("observation = %v", observed)
	}
}

func TestRunStepsObservesWithAScreenshot(t *testing.T) {
	recorder := &observingDriver{}
	s := newTestService(t, recorder.driver(), "darwin")
	ctx := context.Background()
	if _, err := s.Call(ctx, CallRequest{Tool: "get_window_state", Arguments: map[string]any{"pid": 7, "window_id": 9}, SessionID: "worker-1"}); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	tokenStep := []any{map[string]any{"tool": "click", "args": map[string]any{"element_token": "s1:5"}}}
	for name, tc := range map[string]struct {
		arguments map[string]any
		session   string
		want      func(observe map[string]any, present bool) bool
	}{
		"token batch gets its window": {map[string]any{"steps": tokenStep}, "worker-1", func(o map[string]any, ok bool) bool {
			return ok && o["pid"] == float64(7) && o["window_id"] == float64(9) && o["include_screenshot"] == true && o["screenshot_out_file"] != nil
		}},
		"unknown window gets no observe": {map[string]any{"steps": tokenStep}, "worker-2", func(_ map[string]any, ok bool) bool { return !ok }},
		"nested window target gets its window": {map[string]any{"steps": []any{map[string]any{"click": map[string]any{"target": map[string]any{"kind": "window", "pid": 18439, "window_id": 4372}, "element_token": "s00000020:2"}}}}, "worker-2", func(o map[string]any, ok bool) bool {
			return ok && o["pid"] == float64(18439) && o["window_id"] == float64(4372) && o["include_screenshot"] == true
		}},
		"last step's window wins": {map[string]any{"steps": append(append([]any{}, tokenStep...), map[string]any{"tool": "click", "args": map[string]any{"target": map[string]any{"kind": "window", "pid": 8, "window_id": 10}, "element_index": 2}})}, "worker-1", func(o map[string]any, ok bool) bool {
			return ok && o["pid"] == float64(8) && o["window_id"] == float64(10)
		}},
		"named app is left to the driver": {map[string]any{"steps": []any{map[string]any{"click": map[string]any{"app": "Calculator", "role": "button", "name": "Clear"}}}}, "worker-1", func(o map[string]any, ok bool) bool {
			return ok && o["pid"] == nil && o["include_screenshot"] == true
		}},
		"caller opt-out is kept": {map[string]any{"pid": 7, "window_id": 9, "steps": tokenStep, "observe": map[string]any{"include_screenshot": false}}, "worker-1", func(o map[string]any, ok bool) bool {
			return ok && o["include_screenshot"] == false && o["screenshot_out_file"] == nil
		}},
		"caller file is dropped with an opt-out": {map[string]any{"pid": 7, "window_id": 9, "steps": tokenStep, "observe": map[string]any{"include_screenshot": false, "screenshot_out_file": "/etc/elsewhere.png"}}, "worker-1", func(o map[string]any, ok bool) bool {
			return ok && o["include_screenshot"] == false && o["screenshot_out_file"] == nil
		}},
		"caller file is dropped for an unknown window": {map[string]any{"steps": tokenStep, "observe": map[string]any{"screenshot_out_file": "/etc/elsewhere.png"}}, "worker-2", func(o map[string]any, ok bool) bool {
			return ok && o["screenshot_out_file"] == nil
		}},
		"caller file is replaced by AO's": {map[string]any{"pid": 7, "window_id": 9, "steps": tokenStep, "observe": map[string]any{"screenshot_out_file": "/etc/elsewhere.png"}}, "worker-1", func(o map[string]any, ok bool) bool {
			path, _ := o["screenshot_out_file"].(string)
			return ok && o["include_screenshot"] == true && path != "" && !strings.HasPrefix(path, "/etc/")
		}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.Call(ctx, CallRequest{Tool: "run_steps", Arguments: tc.arguments, SessionID: tc.session}); err != nil {
				t.Fatalf("Call() error = %v", err)
			}
			observe, present := recorder.sent[len(recorder.sent)-1]["observe"].(map[string]any)
			if !tc.want(observe, present) {
				t.Fatalf("observe = %v (present %v)", observe, present)
			}
		})
	}
}

func TestCallDropsCallerImageFiles(t *testing.T) {
	recorder := &observingDriver{}
	s := newTestService(t, recorder.driver(), "darwin")
	ctx := context.Background()
	if _, err := s.Call(ctx, CallRequest{Tool: "click", Arguments: map[string]any{"pid": 7, "window_id": 9, "element_index": 3, "debug_image_out": "/etc/click.png"}}); err != nil {
		t.Fatalf("click: %v", err)
	}
	if sent := recorder.sent[0]; has(sent, "debug_image_out") {
		t.Fatalf("click sent %v", sent)
	}

	given := map[string]any{"pid": 7, "window_id": 9, "element_index": 3, "debug_image_out": "/etc/step.png"}
	steps := []any{
		map[string]any{"tool": "click", "args": given},
		map[string]any{"click": map[string]any{"pid": 7, "window_id": 9, "element_index": 4, "debug_image_out": "/etc/step.png"}},
	}
	if _, err := s.Call(ctx, CallRequest{Tool: "run_steps", Arguments: map[string]any{"pid": 7, "window_id": 9, "steps": steps}}); err != nil {
		t.Fatalf("run_steps: %v", err)
	}
	batch := recorder.sent[len(recorder.sent)-1]
	sentSteps, _ := batch["steps"].([]any)
	for index, raw := range sentSteps {
		_, arguments := stepAction(raw.(map[string]any))
		if arguments == nil || has(arguments, "debug_image_out") {
			t.Fatalf("step %d sent %v", index+1, raw)
		}
	}
	if given["debug_image_out"] != "/etc/step.png" {
		t.Fatalf("caller's step arguments changed: %v", given)
	}

	// A trailing get_window_state becomes the batch's end read, and its file
	// would force a capture the batch's observe turned off.
	read := map[string]any{"screenshot_out_file": "/etc/outside.png"}
	steps = []any{
		map[string]any{"click": map[string]any{"element_token": "s1:2"}},
		map[string]any{"tool": "get_window_state", "args": read},
		map[string]any{"get_window_state": map[string]any{"screenshot_out_file": "/etc/outside.png"}},
	}
	if _, err := s.Call(ctx, CallRequest{Tool: "run_steps", Arguments: map[string]any{"pid": 7, "window_id": 9, "steps": steps, "observe": map[string]any{"include_screenshot": false}}}); err != nil {
		t.Fatalf("run_steps with reads: %v", err)
	}
	batch = recorder.sent[len(recorder.sent)-1]
	sentSteps, _ = batch["steps"].([]any)
	for index, raw := range sentSteps {
		_, arguments := stepAction(raw.(map[string]any))
		if has(arguments, "screenshot_out_file") {
			t.Fatalf("step %d sent %v", index+1, raw)
		}
	}
	if observe, _ := batch["observe"].(map[string]any); has(observe, "screenshot_out_file") {
		t.Fatalf("observe = %v", observe)
	}
	if read["screenshot_out_file"] != "/etc/outside.png" {
		t.Fatalf("caller's step arguments changed: %v", read)
	}
}

// An idle driver session ends, and the driver refuses calls naming it until
// start_session revives the name.
func TestCallRevivesAnEndedDriverSession(t *testing.T) {
	driver := &fakeDriver{daemonUp: true}
	ended := true
	var tools []string
	driver.onCall = func(args []string) ([]byte, []byte, error) {
		tools = append(tools, args[1])
		switch {
		case args[1] == "start_session":
			if !strings.Contains(args[2], `"session":"ao-worker-1"`) {
				t.Errorf("start_session arguments = %s", args[2])
			}
			ended = false
			return []byte(`{"active":true,"revived":true,"session":"ao-worker-1"}`), nil, nil
		case ended:
			return nil, []byte("session has ended; tool call 'list_windows' was rejected. Call start_session with session 'ao-worker-1' to start it again, or use a new session label.\n"), errors.New("exit status 1")
		}
		return []byte(`{"windows":[]}`), nil, nil
	}
	s := newTestService(t, driver, "darwin")
	result, err := s.Call(context.Background(), CallRequest{Tool: "list_windows", SessionID: "worker-1"})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if fmt.Sprint(tools) != "[list_windows start_session list_windows]" {
		t.Fatalf("driver tools = %v", tools)
	}
	if output, _ := result.Result.(map[string]any); output["windows"] == nil {
		t.Fatalf("result = %v, want the retried call's output", result.Result)
	}
}

// A batch whose step failed still reports every step and the window after it;
// a batch refused before running is an error.
func TestCallReturnsFailedRunStepsReport(t *testing.T) {
	driver := &fakeDriver{daemonUp: true}
	report := `{"ok":false,"executed":1,"failed_step":0,"total":1,"steps":[{"index":0,"tool":"click","ok":false,"phase":"find","message":"no button"}]}`
	driver.onCall = func(args []string) ([]byte, []byte, error) {
		if strings.Contains(args[2], "refuse") {
			return []byte(`{"ok":false,"code":"invalid_batch","detail":"bad step"}`), nil, errors.New("exit status 1")
		}
		return []byte(report), nil, errors.New("exit status 1")
	}
	s := newTestService(t, driver, "darwin")
	step := map[string]any{"click": map[string]any{"app": "Calculator", "role": "button", "name": "Nope"}}
	result, err := s.Call(context.Background(), CallRequest{Tool: "run_steps", Arguments: map[string]any{"steps": []any{step}}})
	if err != nil {
		t.Fatalf("Call() error = %v, want the step report", err)
	}
	if output, _ := result.Result.(map[string]any); output["ok"] != false || output["steps"] == nil {
		t.Fatalf("result = %v", result.Result)
	}
	_, err = s.Call(context.Background(), CallRequest{Tool: "run_steps", Arguments: map[string]any{"steps": []any{step}, "window": "refuse"}})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "COMPUTER_USE_CALL_FAILED" {
		t.Fatalf("refused batch err = %v", err)
	}
}

func TestAgentToolsReadSchemasFromTheDriver(t *testing.T) {
	driver := &fakeDriver{daemonUp: true}
	var described []string
	driver.onCall = func(args []string) ([]byte, []byte, error) {
		if args[0] != "describe" {
			return []byte(`{}`), nil, nil
		}
		described = append(described, args[1])
		switch args[1] {
		case "click":
			return []byte("name: click\n\ndescription:\nClick a target pid.\n\ninput_schema:\n" +
				`{"type":"object","properties":{"element_token":{"type":"string"},"scope":{"enum":["window","desktop"]},"session":{"type":"string"},"x":{"type":"number"}},"required":["session","x"]}` + "\n"), nil, nil
		case "zoom":
			return []byte("Unknown tool. Available tools: ..."), nil, nil
		}
		return nil, []byte("no such tool"), errors.New("exit status 1")
	}
	s := newTestService(t, driver, "darwin")
	tools, err := s.AgentTools(context.Background())
	if err != nil {
		t.Fatalf("AgentTools() error = %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "click" || tools[0].Description != "Click a target pid." {
		t.Fatalf("tools = %+v, want only click", tools)
	}
	properties := tools[0].InputSchema["properties"].(map[string]any)
	if _, ok := properties["session"]; ok {
		t.Fatalf("session left in schema: %v", properties)
	}
	if _, ok := properties["scope"]; ok {
		t.Fatalf("scope left in schema: %v", properties)
	}
	if fmt.Sprint(tools[0].InputSchema["required"]) != "[x]" {
		t.Fatalf("required = %v", tools[0].InputSchema["required"])
	}
	if len(described) != len(agentToolNames) {
		t.Fatalf("described %v", described)
	}

	// The same driver version is read once.
	if _, err := s.AgentTools(context.Background()); err != nil || len(described) != len(agentToolNames) {
		t.Fatalf("second AgentTools() re-described: err=%v, %d calls", err, len(described))
	}
}

// Actions reached only as run_steps steps have their arguments listed in
// run_steps' description, since its schema types a step's args as any object.
func TestAgentToolsSummarizeStepOnlyArguments(t *testing.T) {
	driver := &fakeDriver{daemonUp: true}
	driver.onCall = func(args []string) ([]byte, []byte, error) {
		switch args[1] {
		case "run_steps":
			return []byte("name: run_steps\n\ndescription:\nRun action tools in one call.\n\ninput_schema:\n" + `{"type":"object","properties":{"steps":{"type":"array"}},"required":["steps"]}`), nil, nil
		case "hotkey":
			return []byte("name: hotkey\n\ndescription:\nPress a key combination.\n\ninput_schema:\n" + `{"type":"object","properties":{"keys":{"type":"array"},"pid":{"type":"integer"},"element_token":{"type":"string"},"session":{"type":"string"}},"required":["keys"]}`), nil, nil
		}
		return nil, []byte("no such tool"), errors.New("exit status 1")
	}
	tools, err := newTestService(t, driver, "darwin").AgentTools(context.Background())
	if err != nil || len(tools) != 1 {
		t.Fatalf("tools = %+v, err = %v", tools, err)
	}
	if want := "Run action tools in one call.\n\nThese actions are run_steps steps only"; !strings.HasPrefix(tools[0].Description, want) {
		t.Fatalf("description = %q", tools[0].Description)
	}
	if !strings.HasSuffix(tools[0].Description, "\n- hotkey: keys*, element_token, pid") {
		t.Fatalf("description = %q, want hotkey's args without session", tools[0].Description)
	}
}

func TestInstalledLocatesTheDriverOnly(t *testing.T) {
	for name, tc := range map[string]struct {
		driver *fakeDriver
		want   bool
	}{
		"installed": {&fakeDriver{}, true},
		"missing":   {&fakeDriver{missing: true}, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := newTestService(t, tc.driver, "darwin").Installed(context.Background())
			if err != nil || got != tc.want {
				t.Fatalf("Installed() = %v, %v; want %v", got, err, tc.want)
			}
			if len(tc.driver.calls) != 0 {
				t.Fatalf("Installed ran the driver: %v", tc.driver.calls)
			}
		})
	}
}

// The driver session can end between an action and AO's read of its window;
// the read revives it like Call does instead of reporting the session ended.
func TestReadAfterActionRevivesAnEndedSession(t *testing.T) {
	driver := &fakeDriver{daemonUp: true}
	ended := false
	var tools []string
	driver.onCall = func(args []string) ([]byte, []byte, error) {
		tools = append(tools, args[1])
		switch {
		case args[1] == "click":
			ended = true // the session expires right after the action lands
			return []byte(`{"summary":"clicked"}`), nil, nil
		case args[1] == "start_session":
			ended = false
			return []byte(`{"active":true,"revived":true}`), nil, nil
		case ended:
			return nil, []byte("session has ended; tool call 'get_window_state' was rejected. Call start_session with session 'ao-worker-1' to start it again, or use a new session label.\n"), errors.New("exit status 1")
		}
		return []byte(`{"snapshot_id":"s2","pid":7,"window_id":9,"element_count":30}`), nil, nil
	}
	s := newTestService(t, driver, "darwin")
	result, err := s.Call(context.Background(), CallRequest{Tool: "click", Arguments: map[string]any{"pid": 7, "window_id": 9, "x": 5, "y": 5}, SessionID: "worker-1"})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	observation := result.Result.(map[string]any)["observation"].(map[string]any)
	if observation["ok"] != true {
		t.Fatalf("observation = %v, want the revived read", observation)
	}
	if fmt.Sprint(tools) != "[click get_window_state start_session get_window_state]" {
		t.Fatalf("driver tools = %v", tools)
	}
}

// A driver refusal or rejected arguments is the caller's request (400); an
// error with a code is the screen not matching the call (409); only a driver
// that is not working is unavailable (503). The outputs are Cua Driver 0.37.0's.
func TestCallFailureStatusFollowsTheDriverAnswer(t *testing.T) {
	for name, tc := range map[string]struct {
		stdout, stderr string
		want           apierr.Kind
	}{
		"missing argument": {stdout: "Missing required integer field: pid\n", want: apierr.KindInvalid},
		"refused": {stdout: `{"hint":"An argument is missing","refusal":{"code":"invalid_arguments","message":"run_steps: unknown argument scope; accepted: app, steps"},"status":"refused"}`,
			want: apierr.KindInvalid},
		"window gone": {stdout: `{"code":"window_id_not_found","hint":"No window matches that target now.","pid":1,"window_id":1}`,
			want: apierr.KindConflict},
		"window gone, Windows": {stdout: "No window with window_id 1 exists. Call `list_windows({\"pid\": 1})` for candidates.\n",
			want: apierr.KindConflict},
		"crashed": {stderr: "thread 'main' panicked at src/main.rs:1:1\n", want: apierr.KindUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			driver := &fakeDriver{daemonUp: true}
			driver.onCall = func([]string) ([]byte, []byte, error) {
				return []byte(tc.stdout), []byte(tc.stderr), errors.New("exit status 1")
			}
			_, err := newTestService(t, driver, "darwin").Call(context.Background(), CallRequest{Tool: "get_window_state", Arguments: map[string]any{"pid": 1, "window_id": 1}})
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) || apiErr.Code != "COMPUTER_USE_CALL_FAILED" || apiErr.Kind != tc.want {
				t.Fatalf("err = %#v, want COMPUTER_USE_CALL_FAILED with kind %v", err, tc.want)
			}
		})
	}
}

// A type_text the driver refuses for the field, such as a web composer that
// takes no background input, points the agent to set_value and its caveat;
// one refused for its arguments does not.
func TestRefusedTypeTextAdvisesSetValue(t *testing.T) {
	for name, tc := range map[string]struct {
		tool, stdout string
		want         bool
	}{
		"field refused": {tool: "type_text", stdout: `{"refusal":{"code":"background_delivery_unsupported","message":"web content refuses background delivery"},"status":"refused"}`, want: true},
		"bad arguments": {tool: "type_text", stdout: `{"refusal":{"code":"invalid_arguments","message":"type_text: unknown argument foo"},"status":"refused"}`},
		"window gone":   {tool: "type_text", stdout: `{"code":"window_id_not_found","pid":7,"window_id":9}`},
		"other tool":    {tool: "click", stdout: `{"refusal":{"code":"background_delivery_unsupported","message":"refused"},"status":"refused"}`},
	} {
		t.Run(name, func(t *testing.T) {
			driver := &fakeDriver{daemonUp: true}
			driver.onCall = func([]string) ([]byte, []byte, error) {
				return []byte(tc.stdout), nil, errors.New("exit status 1")
			}
			_, err := newTestService(t, driver, "windows").Call(context.Background(), CallRequest{Tool: tc.tool, Arguments: map[string]any{"element_token": "s00000001:4", "text": "hi"}})
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %#v, want an API error", err)
			}
			if got := strings.Contains(apiErr.Message, webTextAdvice); got != tc.want {
				t.Fatalf("advice in message = %v, want %v: %q", got, tc.want, apiErr.Message)
			}
		})
	}
}

// The driver reports text it typed into web content as unverifiable; the
// result carries the same advice, since the page may not have seen it.
func TestUnverifiableTypeTextNotesSetValueCaveat(t *testing.T) {
	recorder := &observingDriver{}
	s := newTestService(t, recorder.driver(), "darwin")
	result, err := s.Call(context.Background(), CallRequest{Tool: "type_text", Arguments: map[string]any{"pid": 7, "window_id": 9, "element_token": "s00000001:4", "text": "hi"}})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if note := result.Result.(map[string]any)["ao_note"]; note != webTextAdvice {
		t.Fatalf("ao_note = %v, want the web text advice", note)
	}
	result, err = s.Call(context.Background(), CallRequest{Tool: "click", Arguments: map[string]any{"pid": 7, "window_id": 9, "x": 5, "y": 5}})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if note, ok := result.Result.(map[string]any)["ao_note"]; ok {
		t.Fatalf("click ao_note = %v, want none", note)
	}
}

// deadlineDriver records how long each driver call was given.
type deadlineDriver struct {
	*fakeDriver
	budgets map[string]time.Duration
}

func (d *deadlineDriver) Run(ctx context.Context, binary string, args ...string) ([]byte, []byte, error) {
	if deadline, ok := ctx.Deadline(); ok && args[0] == "call" {
		d.budgets[args[1]] = time.Until(deadline)
	}
	return d.fakeDriver.Run(ctx, binary, args...)
}

func TestRunStepsGetsTheDriverBatchBudget(t *testing.T) {
	driver := &deadlineDriver{fakeDriver: &fakeDriver{daemonUp: true}, budgets: map[string]time.Duration{}}
	s := New(driver, t.TempDir())
	s.goos = "darwin"
	s.SetEnabled(func(context.Context) bool { return true })
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	ctx := context.Background()
	if _, err := s.Call(ctx, CallRequest{Tool: "get_window_state", Arguments: map[string]any{"pid": 7, "window_id": 9}}); err != nil {
		t.Fatalf("get_window_state: %v", err)
	}
	steps := []any{map[string]any{"click": map[string]any{"pid": 7, "window_id": 9, "element_index": 3}}}
	if _, err := s.Call(ctx, CallRequest{Tool: "run_steps", Arguments: map[string]any{"pid": 7, "window_id": 9, "steps": steps}}); err != nil {
		t.Fatalf("run_steps: %v", err)
	}
	if read := driver.budgets["get_window_state"]; read > callTimeout {
		t.Fatalf("get_window_state budget = %v, want at most %v", read, callTimeout)
	}
	if batch := driver.budgets["run_steps"]; batch <= 60*time.Second {
		t.Fatalf("run_steps budget = %v, want more than the driver's 60 s batch limit", batch)
	}
}
