package computeruse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// serveRemote stands in for the other computer's /api/v1/remote-computer-use
// routes, backed by a real Service, and records the headers each request
// brought.
func serveRemote(t *testing.T, remote *Service) (*httptest.Server, *[]http.Header) {
	t.Helper()
	var headers []http.Header
	write := func(w http.ResponseWriter, value any, err error) {
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			var apiErr *apierr.Error
			status := http.StatusInternalServerError
			code := "INTERNAL"
			if errors.As(err, &apiErr) {
				code = apiErr.Code
				switch apiErr.Kind {
				case apierr.KindForbidden:
					status = http.StatusForbidden
				case apierr.KindConflict:
					status = http.StatusConflict
				case apierr.KindInvalid:
					status = http.StatusBadRequest
				}
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "x", "code": code, "message": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(value)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = append(headers, r.Header.Clone())
		switch r.URL.Path {
		case "/api/v1/remote-computer-use/agent-tools":
			tools, err := remote.RemoteAgentTools(r.Context())
			write(w, map[string]any{"tools": tools}, err)
		case "/api/v1/remote-computer-use/call":
			var request RemoteCallRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			result, err := remote.ServeRemoteCall(r.Context(), request)
			write(w, result, err)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, &headers
}

// recordingScreenshotDriver writes a PNG wherever a call asks for its screenshot and
// reports its path, as Cua Driver does.
func recordingScreenshotDriver(sent *map[string]any) *fakeDriver {
	driver := &fakeDriver{daemonUp: true}
	driver.onCall = func(args []string) ([]byte, []byte, error) {
		if err := json.Unmarshal([]byte(args[2]), sent); err != nil {
			return nil, nil, err
		}
		path, _ := (*sent)["screenshot_out_file"].(string)
		if err := os.WriteFile(path, pngHeader, 0o600); err != nil {
			return nil, nil, err
		}
		return []byte(fmt.Sprintf(`{"screenshot_file_path":%q,"elements":[]}`, path)), nil, nil
	}
	return driver
}

func setTarget(t *testing.T, s *Service, url string) {
	t.Helper()
	if _, err := s.SetRemoteTarget(context.Background(), SetRemoteTargetRequest{HostID: "host-mac", Label: "Studio Mac", URL: url, Credential: "aod_secret"}); err != nil {
		t.Fatalf("SetRemoteTarget() error = %v", err)
	}
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

// A call made with another computer chosen runs there, under every rule a
// local call has, and comes back with its screenshot saved here: the agent's
// tool server reads the path from this computer's disk.
func TestCallRunsOnTheChosenComputerAndSavesScreenshotsHere(t *testing.T) {
	var sent map[string]any
	remote := newTestService(t, recordingScreenshotDriver(&sent), "darwin")
	if _, err := remote.SetAllowRemote(context.Background(), true); err != nil {
		t.Fatalf("SetAllowRemote() error = %v", err)
	}
	server, headers := serveRemote(t, remote)

	local := newTestService(t, &fakeDriver{missing: true}, "linux")
	// Computer use runs on the other computer even with it off here and no
	// Cua Driver on this one.
	local.SetEnabled(func(context.Context) bool { return false })
	setTarget(t, local, server.URL)

	result, err := local.Call(context.Background(), CallRequest{Tool: "get_window_state", Arguments: map[string]any{"pid": 7, "window_id": 9}, SessionID: "ao-12"})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if len(result.Images) != 1 {
		t.Fatalf("Images = %+v", result.Images)
	}
	localPath := result.Images[0].Path
	wantDir, _ := filepath.EvalSymlinks(filepath.Join(local.images.root, "ao-12"))
	if filepath.Dir(localPath) != wantDir {
		t.Fatalf("image saved at %q, want under %q", localPath, wantDir)
	}
	data, err := os.ReadFile(localPath)
	if err != nil || !bytes.Equal(data, pngHeader) {
		t.Fatalf("saved image = %q, %v", data, err)
	}
	if got := result.Result.(map[string]any)["screenshot_file_path"]; got != localPath {
		t.Fatalf("screenshot_file_path = %v, want the local copy %q", got, localPath)
	}

	// The other computer kept the screenshot in a folder of its own for this
	// computer's session, and named a driver session for it.
	remotePath, _ := sent["screenshot_out_file"].(string)
	if !strings.Contains(filepath.Base(filepath.Dir(remotePath)), "remote-") || !strings.HasSuffix(filepath.Dir(remotePath), "-ao-12") {
		t.Fatalf("remote screenshot folder = %q", filepath.Dir(remotePath))
	}
	if session, _ := sent["session"].(string); !strings.HasPrefix(session, "ao-remote-") {
		t.Fatalf("driver session = %q", session)
	}

	got := (*headers)[0]
	if got.Get("Authorization") != "Bearer aod_secret" || got.Get("X-AO-Expected-Host-ID") != "host-mac" {
		t.Fatalf("headers = %v", got)
	}
}

func TestAgentToolsComeFromTheChosenComputer(t *testing.T) {
	remote := newTestService(t, &fakeDriver{daemonUp: true}, "darwin")
	server, _ := serveRemote(t, remote)
	local := newTestService(t, &fakeDriver{missing: true}, "linux")
	setTarget(t, local, server.URL)

	// Not offered yet: the other computer refuses, and the agent reads why.
	if _, err := local.AgentTools(context.Background()); err == nil || !strings.Contains(err.Error(), "On Studio Mac:") {
		t.Fatalf("AgentTools() error = %v, want the other computer's refusal", err)
	} else {
		wantCode(t, err, "COMPUTER_USE_REMOTE_OFF")
	}

	if _, err := remote.SetAllowRemote(context.Background(), true); err != nil {
		t.Fatalf("SetAllowRemote() error = %v", err)
	}
	tools, err := local.AgentTools(context.Background())
	if err != nil {
		t.Fatalf("AgentTools() error = %v", err)
	}
	if tools == nil {
		t.Fatal("AgentTools() = nil, want the other computer's list")
	}
}

// The other computer still needs "Let agents use this computer" on.
func TestRemoteCallNeedsComputerUseOnThere(t *testing.T) {
	remote := newTestService(t, &fakeDriver{daemonUp: true}, "darwin")
	if _, err := remote.SetAllowRemote(context.Background(), true); err != nil {
		t.Fatalf("SetAllowRemote() error = %v", err)
	}
	remote.SetEnabled(func(context.Context) bool { return false })
	_, err := remote.ServeRemoteCall(context.Background(), RemoteCallRequest{Tool: "list_windows"})
	wantCode(t, err, "COMPUTER_USE_DISABLED")
	if offer, _ := remote.RemoteOffer(context.Background()); offer.Offered {
		t.Fatalf("offer = %+v while computer use is off", offer)
	}
}

func TestRemoteOfferFollowsBothSettingsAndTheDriver(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t, &fakeDriver{daemonUp: true}, "darwin")
	if offer, _ := s.RemoteOffer(ctx); offer.Offered || offer.Platform != "darwin" {
		t.Fatalf("offer before allowing = %+v", offer)
	}
	if _, err := s.SetAllowRemote(ctx, true); err != nil {
		t.Fatalf("SetAllowRemote() error = %v", err)
	}
	if offer, _ := s.RemoteOffer(ctx); !offer.Offered {
		t.Fatalf("offer after allowing = %+v", offer)
	}
	if _, err := s.ServeRemoteCall(ctx, RemoteCallRequest{Tool: "list_windows", SessionID: "ao-1", Caller: "abc"}); err != nil {
		t.Fatalf("ServeRemoteCall() error = %v", err)
	}
	if _, err := s.SetAllowRemote(ctx, false); err != nil {
		t.Fatalf("SetAllowRemote(false) error = %v", err)
	}
	_, err := s.ServeRemoteCall(ctx, RemoteCallRequest{Tool: "list_windows"})
	wantCode(t, err, "COMPUTER_USE_REMOTE_OFF")

	missing := newTestService(t, &fakeDriver{missing: true}, "darwin")
	_, err = missing.SetAllowRemote(ctx, true)
	wantCode(t, err, "COMPUTER_USE_NOT_INSTALLED")
}

// The settings survive a restart, the credential is kept out of every
// answer, and the file only its owner can read holds it.
func TestRemoteSettingsPersistWithoutExposingTheCredential(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	s := New(&fakeDriver{daemonUp: true}, dataDir)
	settings, err := s.SetRemoteTarget(ctx, SetRemoteTargetRequest{HostID: "host-mac", Label: "  Studio Mac ", URL: "https://h-abc.useao.dev/", Credential: "aod_secret"})
	if err != nil {
		t.Fatalf("SetRemoteTarget() error = %v", err)
	}
	if settings.Target == nil || *settings.Target != (RemoteTarget{HostID: "host-mac", Label: "Studio Mac", URL: "https://h-abc.useao.dev"}) {
		t.Fatalf("Target = %+v", settings.Target)
	}
	encoded, _ := json.Marshal(settings)
	if strings.Contains(string(encoded), "aod_secret") {
		t.Fatalf("settings carry the credential: %s", encoded)
	}
	path := filepath.Join(dataDir, remoteFileName)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("settings file = %v, %v; want mode 0600", info, err)
	}

	reopened := New(&fakeDriver{daemonUp: true}, dataDir)
	if label := reopened.RemoteTargetLabel(ctx); label != "Studio Mac" {
		t.Fatalf("RemoteTargetLabel() after restart = %q", label)
	}
	cleared, err := reopened.ClearRemoteTarget(ctx)
	if err != nil || cleared.Target != nil {
		t.Fatalf("ClearRemoteTarget() = %+v, %v", cleared, err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "aod_secret") {
		t.Fatalf("cleared settings still hold the credential: %s", data)
	}
	if label := reopened.RemoteTargetLabel(ctx); label != "" {
		t.Fatalf("RemoteTargetLabel() after clearing = %q", label)
	}
}

func TestSetRemoteTargetRejectsBadInput(t *testing.T) {
	s := newTestService(t, &fakeDriver{}, "darwin")
	for name, request := range map[string]SetRemoteTargetRequest{
		"no host id":    {URL: "https://h.useao.dev", Credential: "x"},
		"file url":      {HostID: "h", URL: "file:///etc/passwd", Credential: "x"},
		"user info":     {HostID: "h", URL: "https://me:pw@h.useao.dev", Credential: "x"},
		"path":          {HostID: "h", URL: "https://h.useao.dev/api", Credential: "x"},
		"no credential": {HostID: "h", URL: "https://h.useao.dev"},
	} {
		if _, err := s.SetRemoteTarget(context.Background(), request); err == nil {
			t.Errorf("%s: SetRemoteTarget() accepted %+v", name, request)
		} else {
			wantCode(t, err, "COMPUTER_USE_REMOTE_INVALID")
		}
	}
}

func TestRemoteFailuresSayWhatToDo(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		code   string
	}{
		"credential refused": {http.StatusUnauthorized, `{"code":"UNAUTHORIZED","message":"bad"}`, "COMPUTER_USE_REMOTE_UNAUTHORIZED"},
		"older release":      {http.StatusNotFound, `{"code":"NOT_FOUND","message":"no route"}`, "COMPUTER_USE_REMOTE_UNSUPPORTED"},
		"another machine":    {http.StatusMisdirectedRequest, `{"code":"HOST_ID_MISMATCH","message":"other"}`, "COMPUTER_USE_REMOTE_HOST_CHANGED"},
		"driver failure":     {http.StatusConflict, `{"code":"COMPUTER_USE_DAEMON_STOPPED","message":"start it"}`, "COMPUTER_USE_DAEMON_STOPPED"},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			s := newTestService(t, &fakeDriver{}, "linux")
			setTarget(t, s, server.URL)
			_, err := s.Call(context.Background(), CallRequest{Tool: "list_windows"})
			wantCode(t, err, tc.code)
			if !strings.Contains(err.Error(), "Studio Mac") {
				t.Fatalf("error %q does not name the computer", err)
			}
		})
	}

	s := newTestService(t, &fakeDriver{}, "linux")
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close()
	setTarget(t, s, url)
	_, err := s.Call(context.Background(), CallRequest{Tool: "list_windows"})
	wantCode(t, err, "COMPUTER_USE_REMOTE_UNREACHABLE")
}

// A redirect would carry the credential to an address the user never paired.
func TestRemoteCallsDoNotFollowRedirects(t *testing.T) {
	var reached bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	defer elsewhere.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	s := newTestService(t, &fakeDriver{}, "linux")
	setTarget(t, s, server.URL)
	if _, err := s.Call(context.Background(), CallRequest{Tool: "list_windows"}); err == nil {
		t.Fatal("Call() followed a redirect")
	}
	if reached {
		t.Fatal("the credential reached the redirect target")
	}
}

func TestRemoteScopeStaysWithinScopeRules(t *testing.T) {
	for _, tc := range []struct{ caller, session, want string }{
		{"abc123", "ao-12", "remote-abc123-ao-12"},
		{"", "", "remote"},
		{"../../x", "a/b c", "remote-x-a-b-c"},
		{strings.Repeat("c", 40), strings.Repeat("s", 200), "remote-" + strings.Repeat("c", 16) + "-" + strings.Repeat("s", 100)},
	} {
		got := remoteScope(tc.caller, tc.session)
		if got != tc.want || !scopePattern.MatchString(got) {
			t.Errorf("remoteScope(%q, %q) = %q, want %q", tc.caller, tc.session, got, tc.want)
		}
	}
}

// A call that ran on another computer shows in this session's timeline,
// named after that computer, with its screenshot saved here; one the other
// computer refused shows too, with why.
func TestRemoteCallsShowInTheLocalTimeline(t *testing.T) {
	ctx := context.Background()
	var sent map[string]any
	remote := newTestService(t, recordingScreenshotDriver(&sent), "darwin")
	server, _ := serveRemote(t, remote)
	local := newTestService(t, &fakeDriver{missing: true}, "linux")
	setTarget(t, local, server.URL)

	_, err := local.Call(ctx, CallRequest{Tool: "list_windows", SessionID: "ao-12"})
	wantCode(t, err, "COMPUTER_USE_REMOTE_OFF")
	if _, err := remote.SetAllowRemote(ctx, true); err != nil {
		t.Fatalf("SetAllowRemote() error = %v", err)
	}
	result, err := local.Call(ctx, CallRequest{Tool: "get_window_state", Arguments: map[string]any{"pid": 7, "window_id": 9}, SessionID: "ao-12"})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}

	entries, err := local.Timeline(ctx, "ao-12")
	if err != nil || len(entries) != 2 {
		t.Fatalf("Timeline() = %+v, %v", entries, err)
	}
	refused, read := entries[0], entries[1]
	if refused.OK || refused.RanOn != "Studio Mac" || !strings.Contains(refused.Error, "On Studio Mac:") {
		t.Fatalf("refused step = %+v", refused)
	}
	if !read.OK || read.RanOn != "Studio Mac" || read.Tool != "get_window_state" || len(read.Screenshots) != 1 ||
		read.Screenshots[0] != filepath.Base(result.Images[0].Path) {
		t.Fatalf("read step = %+v, images %+v", read, result.Images)
	}
	if _, err := local.ScreenshotPath(ctx, "ao-12", read.Screenshots[0]); err != nil {
		t.Fatalf("ScreenshotPath() error = %v", err)
	}
}

// The computer that is used knows which other computers' agents are using
// it, by the name they send, until they have been quiet for a while.
func TestUsedComputerSeesWhoIsUsingIt(t *testing.T) {
	ctx := context.Background()
	remote := newTestService(t, &fakeDriver{daemonUp: true}, "darwin")
	if _, err := remote.SetAllowRemote(ctx, true); err != nil {
		t.Fatalf("SetAllowRemote() error = %v", err)
	}
	server, _ := serveRemote(t, remote)
	local := newTestService(t, &fakeDriver{missing: true}, "linux")
	if _, err := local.SetRemoteTarget(ctx, SetRemoteTargetRequest{HostID: "host-mac", Label: "Studio Mac", URL: server.URL, Credential: "aod_secret", CallerName: "Build box"}); err != nil {
		t.Fatalf("SetRemoteTarget() error = %v", err)
	}
	if settings, _ := remote.RemoteSettings(ctx); len(settings.Activity) != 0 {
		t.Fatalf("activity before any call = %+v", settings.Activity)
	}
	if _, err := local.Call(ctx, CallRequest{Tool: "list_windows", SessionID: "ao-1"}); err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	settings, err := remote.RemoteSettings(ctx)
	if err != nil || len(settings.Activity) != 1 || settings.Activity[0].Name != "Build box" || settings.Activity[0].Caller == "" {
		t.Fatalf("activity = %+v, %v", settings.Activity, err)
	}
	started := settings.Activity[0].Started

	// A call soon after continues the same run; one after a long pause starts a new one.
	remote.noteRemoteCall(settings.Activity[0].Caller, "", started.Add(time.Minute))
	remote.mu.Lock()
	if got := remote.activity[settings.Activity[0].Caller]; !got.Started.Equal(started) || got.Name != "Build box" {
		t.Fatalf("continued run = %+v", got)
	}
	remote.mu.Unlock()
	remote.noteRemoteCall(settings.Activity[0].Caller, "", started.Add(10*time.Minute))
	remote.mu.Lock()
	if got := remote.activity[settings.Activity[0].Caller]; !got.Started.Equal(started.Add(10 * time.Minute)) {
		t.Fatalf("new run = %+v", got)
	}
	if recent := remote.recentActivityLocked(started.Add(20 * time.Minute)); len(recent) != 0 {
		t.Fatalf("quiet computer still listed: %+v", recent)
	}
	remote.mu.Unlock()
}
