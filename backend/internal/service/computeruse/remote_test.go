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

// asHost answers the unauthenticated identity check as hostID, then hands
// every other request to next, as a remote host does.
func asHost(hostID string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/identity" {
			if r.Header.Get("Authorization") != "" {
				http.Error(w, "the identity check must not carry the credential", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"hostId":"` + hostID + `","apiVersion":1}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

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
	server := httptest.NewServer(asHost("host-mac", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	})))
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
	if folder := filepath.Base(filepath.Dir(remotePath)); !strings.HasPrefix(folder, "remote-") || !strings.Contains(folder, "-ao-12-") {
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
	if _, label := reopened.CurrentTarget(ctx); label != "Studio Mac" {
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
	if _, label := reopened.CurrentTarget(ctx); label != "" {
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
			server := httptest.NewServer(asHost("host-mac", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})))
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
	server := httptest.NewServer(asHost("host-mac", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusTemporaryRedirect)
	})))
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
	for _, tc := range []struct{ caller, session, prefix string }{
		{"abc123", "ao-12", "remote-abc123-ao-12-"},
		{"", "", "remote-"},
		{"../../x", "a/b c", "remote-x-a-b-c-"},
		{strings.Repeat("c", 40), strings.Repeat("s", 200), "remote-" + strings.Repeat("c", 16) + "-" + strings.Repeat("s", 90) + "-"},
	} {
		got := remoteScope(tc.caller, tc.session)
		if !strings.HasPrefix(got, tc.prefix) || !scopePattern.MatchString(got) || got != remoteScope(tc.caller, tc.session) {
			t.Errorf("remoteScope(%q, %q) = %q, want a stable scope starting %q", tc.caller, tc.session, got, tc.prefix)
		}
	}
	// Pairs that clean to the same text still get their own scope.
	if remoteScope("work.a", "s") == remoteScope("work.a-", "s") || remoteScope("c", "a/b") == remoteScope("c", "a b") {
		t.Fatal("distinct caller and session pairs share a scope")
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

// The credential goes only to a connection that proved it is the chosen
// host: another Orchestrator, or anything else, at the saved address gets
// neither the credential nor the call.
func TestRemoteCallsOnlyGoToTheChosenHost(t *testing.T) {
	var sawCredential bool
	impostor := httptest.NewServer(asHost("someone-else", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			sawCredential = true
		}
		w.WriteHeader(http.StatusOK)
	})))
	defer impostor.Close()
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			sawCredential = true
		}
		http.NotFound(w, r)
	}))
	defer plain.Close()
	for _, server := range []*httptest.Server{impostor, plain} {
		s := newTestService(t, &fakeDriver{}, "linux")
		setTarget(t, s, server.URL)
		_, err := s.Call(context.Background(), CallRequest{Tool: "list_windows"})
		wantCode(t, err, "COMPUTER_USE_REMOTE_HOST_CHANGED")
	}
	if sawCredential {
		t.Fatal("the credential reached a server that is not the chosen host")
	}
}

// Another computer cannot name a driver session: its calls stay in their
// own, whatever arguments.session says.
func TestRemoteCallerCannotPickADriverSession(t *testing.T) {
	var sent map[string]any
	remote := newTestService(t, recordingScreenshotDriver(&sent), "darwin")
	if _, err := remote.SetAllowRemote(context.Background(), true); err != nil {
		t.Fatalf("SetAllowRemote() error = %v", err)
	}
	_, err := remote.ServeRemoteCall(context.Background(), RemoteCallRequest{
		Tool: "get_window_state", Arguments: map[string]any{"pid": 7, "window_id": 9, "session": "ao-local-1"}, SessionID: "ao-1", Caller: "abc",
	})
	if err != nil {
		t.Fatalf("ServeRemoteCall() error = %v", err)
	}
	if session, _ := sent["session"].(string); session != driverSession(remoteScope("abc", "ao-1")) {
		t.Fatalf("driver session = %q, want the caller's own", session)
	}
}

// A refused call is not activity: only a call that passed every check marks
// the other computer as using this one.
func TestOnlyAdmittedCallsCountAsActivity(t *testing.T) {
	ctx := context.Background()
	remote := newTestService(t, &fakeDriver{daemonUp: true}, "darwin")
	if _, err := remote.SetAllowRemote(ctx, true); err != nil {
		t.Fatalf("SetAllowRemote() error = %v", err)
	}
	_, err := remote.ServeRemoteCall(ctx, RemoteCallRequest{Tool: "zoom", Caller: "abc", CallerName: "Build box"})
	wantCode(t, err, "COMPUTER_USE_TOOL_NOT_OFFERED")
	if settings, _ := remote.RemoteSettings(ctx); len(settings.Activity) != 0 {
		t.Fatalf("activity after a refused call = %+v", settings.Activity)
	}
	for index := range maxRemoteCallers + 5 {
		remote.noteRemoteCall(fmt.Sprintf("caller-%d", index), "", time.Now())
	}
	remote.mu.Lock()
	kept := len(remote.activity)
	remote.mu.Unlock()
	if kept != maxRemoteCallers {
		t.Fatalf("kept %d callers, want at most %d", kept, maxRemoteCallers)
	}
}

// A session started with computer use on one computer is refused, not
// redirected, once the user picks another.
func TestCallsAreBoundToTheSessionsTarget(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t, &fakeDriver{daemonUp: true}, "darwin")
	if _, err := s.Call(ctx, CallRequest{Tool: "list_windows", Target: LocalTarget}); err != nil {
		t.Fatalf("local call error = %v", err)
	}
	if _, err := s.Call(ctx, CallRequest{Tool: "list_windows"}); err != nil {
		t.Fatalf("unbound call error = %v", err)
	}
	_, err := s.Call(ctx, CallRequest{Tool: "list_windows", Target: "host-mac"})
	wantCode(t, err, "COMPUTER_USE_TARGET_CHANGED")
	if !strings.Contains(err.Error(), "this computer") {
		t.Fatalf("error = %q, want it to name where computer use runs now", err)
	}
	setTarget(t, s, "http://127.0.0.1:1")
	_, err = s.Call(ctx, CallRequest{Tool: "list_windows", Target: LocalTarget})
	wantCode(t, err, "COMPUTER_USE_TARGET_CHANGED")
}

// Screenshots that would push one answer over the limit stay behind; the
// result still goes back, and says which screenshot is missing.
func TestRemoteResultStaysWithinTheResponseLimit(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.png")
	data := make([]byte, maxRemoteImageBytes)
	copy(data, pngHeader)
	if err := os.WriteFile(big, data, 0o600); err != nil {
		t.Fatal(err)
	}
	images := []Image{}
	for range 8 {
		images = append(images, Image{Path: big, MimeType: "image/png"})
	}
	result := remoteResult(CallResult{Tool: "run_steps", Result: map[string]any{"screenshot_file_path": big}, Images: images})
	encoded, _ := json.Marshal(result)
	if len(encoded) > maxRemoteResponseBytes {
		t.Fatalf("answer is %d bytes, over %d", len(encoded), maxRemoteResponseBytes)
	}
	if len(result.Images) == 0 || len(result.Images) == len(images) {
		t.Fatalf("sent %d of %d screenshots, want some but not all", len(result.Images), len(images))
	}
}

// A damaged settings file still lets the user turn things off.
func TestDamagedRemoteSettingsCanBeReset(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, remoteFileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(&fakeDriver{daemonUp: true}, dataDir)
	s.SetEnabled(func(context.Context) bool { return true })
	if _, err := s.Call(ctx, CallRequest{Tool: "list_windows"}); err == nil {
		t.Fatal("Call() with damaged settings ran somewhere instead of reporting the damage")
	}
	if _, err := s.SetAllowRemote(ctx, true); err == nil {
		t.Fatal("SetAllowRemote(true) overwrote damaged settings")
	}
	settings, err := s.ClearRemoteTarget(ctx)
	if err != nil || settings.Target != nil || settings.AllowRemote {
		t.Fatalf("ClearRemoteTarget() = %+v, %v", settings, err)
	}
	if _, err := s.Call(ctx, CallRequest{Tool: "list_windows"}); err != nil {
		t.Fatalf("Call() after reset error = %v", err)
	}
}

// A caller that gives up on a slow first read of the tools (another
// computer's request deadline, say) does not stop it: the read completes and
// fills the cache, so the next caller gets the full list at once.
func TestToolReadOutlivesTheCallerThatStartedIt(t *testing.T) {
	release := make(chan struct{})
	driver := &fakeDriver{daemonUp: true}
	driver.onCall = func(args []string) ([]byte, []byte, error) {
		if args[0] != "describe" {
			return []byte(`{}`), nil, nil
		}
		<-release
		if args[1] == "click" {
			return []byte("name: click\n\ndescription:\nClick.\n\ninput_schema:\n" + `{"type":"object","properties":{}}` + "\n"), nil, nil
		}
		return nil, []byte("no such tool"), errors.New("exit status 1")
	}
	s := newTestService(t, driver, "darwin")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.AgentTools(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("AgentTools(cancelled) error = %v, want context.Canceled", err)
	}
	close(release)
	tools, err := s.AgentTools(context.Background())
	if err != nil || len(tools) != 1 || tools[0].Name != "click" {
		t.Fatalf("AgentTools() after the read = %+v, %v", tools, err)
	}
	s.mu.Lock()
	cached := len(s.agentTools.tools)
	s.mu.Unlock()
	if cached != 1 {
		t.Fatalf("cached %d tools, want the completed read's 1", cached)
	}
}

// A batch whose step failed on the other computer returns its report to the
// agent and shows as failed, with the step, in this computer's timeline.
func TestRemoteFailedBatchShowsAsFailedHere(t *testing.T) {
	ctx := context.Background()
	driver := &fakeDriver{daemonUp: true}
	driver.onCall = func(args []string) ([]byte, []byte, error) {
		if args[1] == "run_steps" {
			return []byte(`{"ok":false,"steps":[{"index":0,"tool":"click","ok":false,"message":"no button"}]}`), nil, errors.New("exit status 1")
		}
		return []byte(`{}`), nil, nil
	}
	remote := newTestService(t, driver, "darwin")
	if _, err := remote.SetAllowRemote(ctx, true); err != nil {
		t.Fatalf("SetAllowRemote() error = %v", err)
	}
	server, _ := serveRemote(t, remote)
	local := newTestService(t, &fakeDriver{missing: true}, "linux")
	setTarget(t, local, server.URL)
	result, err := local.Call(ctx, CallRequest{Tool: "run_steps", Arguments: map[string]any{"steps": []any{map[string]any{"tool": "click", "args": map[string]any{}}}}, SessionID: "ao-3"})
	if err != nil {
		t.Fatalf("Call() error = %v, want the report", err)
	}
	if _, ok := result.Result.(map[string]any)["steps"]; !ok {
		t.Fatalf("result = %+v, want the step report", result.Result)
	}
	entries, err := local.Timeline(ctx, "ao-3")
	if err != nil || len(entries) != 1 || entries[0].OK || !strings.Contains(entries[0].Error, "step 1 (click): no button") || entries[0].RanOn != "Studio Mac" {
		t.Fatalf("Timeline() = %+v, %v", entries, err)
	}
}
