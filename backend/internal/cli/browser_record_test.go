package cli

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type browserRecordServer struct {
	mu       sync.Mutex
	requests []browserCommandRequestDTO
	video    []byte
	chunk    int
	// Ends the recording one chunk early to check the CLI notices.
	truncate bool
}

func (s *browserRecordServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/browser/commands" {
			http.NotFound(w, r)
			return
		}
		var body browserCommandRequestDTO
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode command: %v", err)
			return
		}
		s.mu.Lock()
		s.requests = append(s.requests, body)
		s.mu.Unlock()
		var result any
		switch body.Action {
		case "record-start":
			result = map[string]any{"recording": true, "id": "rec-1", "fps": body.Args["fps"]}
			if body.Args["size"] == "iphone-12-pro" {
				result = map[string]any{"recording": true, "id": "rec-1", "viewport": map[string]any{"width": 390, "height": 844, "mobile": true}}
			}
		case "record-status":
			result = map[string]any{"recording": true, "id": "rec-1", "durationMs": 4200, "frames": 31, "fps": 10}
		case "record-stop":
			result = map[string]any{
				"id": "rec-1", "bytes": len(s.video), "durationMs": 4200, "frames": 31,
				"width": 1280, "height": 720, "mimeType": "video/webm", "stopReason": "stopped",
			}
		case "record-read":
			offset := int(body.Args["offset"].(float64))
			end := min(offset+s.chunk, len(s.video))
			done := end == len(s.video)
			if s.truncate && end < len(s.video) && offset > 0 {
				done = true
			}
			result = map[string]any{
				"id": body.Args["id"], "offset": offset, "nextOffset": end, "bytes": len(s.video), "done": done,
				"data": base64.StdEncoding.EncodeToString(s.video[offset:end]),
			}
		default:
			t.Errorf("unexpected action %s", body.Action)
		}
		encoded, _ := json.Marshal(map[string]any{"requestId": "r1", "sessionId": "ao-1", "action": body.Action, "result": result})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(encoded)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (s *browserRecordServer) actions() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.requests))
	for _, request := range s.requests {
		out = append(out, request.Action)
	}
	return out
}

func TestBrowserRecordStartPassesLimits(t *testing.T) {
	setBrowserIdentity(t)
	cfg := setConfigEnv(t)
	server := &browserRecordServer{}
	writeRunFileFor(t, cfg, server.start(t))
	deps := Deps{ProcessAlive: func(int) bool { return true }}

	out, errOut, err := executeCLI(t, deps, "browser", "record", "start", "--fps", "24", "--max-seconds", "90")
	if err != nil {
		t.Fatalf("record start err=%v stderr=%s", err, errOut)
	}
	if !strings.Contains(out, "in its own window") || !strings.Contains(out, "24 fps") || !strings.Contains(out, "90 s") || !strings.Contains(out, "record stop") {
		t.Fatalf("record start output = %q", out)
	}
	args := server.requests[0].Args
	if args["fps"] != float64(24) || args["maxSeconds"] != float64(90) {
		t.Fatalf("record start args = %#v", args)
	}
}

func TestBrowserRecordStartLaysThePageOutAtASize(t *testing.T) {
	setBrowserIdentity(t)
	cfg := setConfigEnv(t)
	server := &browserRecordServer{}
	writeRunFileFor(t, cfg, server.start(t))
	deps := Deps{ProcessAlive: func(int) bool { return true }}

	out, errOut, err := executeCLI(t, deps, "browser", "record", "start", "--size", "iphone-12-pro")
	if err != nil {
		t.Fatalf("record start --size err=%v stderr=%s", err, errOut)
	}
	if !strings.Contains(out, "in its own window at 390x844 (mobile)") {
		t.Fatalf("record start --size output = %q", out)
	}
	args := server.requests[0].Args
	if args["size"] != "iphone-12-pro" || args["mobile"] != false {
		t.Fatalf("record start --size args = %#v", args)
	}
	if _, _, err := executeCLI(t, deps, "browser", "record", "start", "--size", "800x600", "--mobile"); err != nil {
		t.Fatalf("record start --size --mobile: %v", err)
	}
	if args := server.requests[1].Args; args["size"] != "800x600" || args["mobile"] != true {
		t.Fatalf("record start --mobile args = %#v", args)
	}
}

func TestBrowserRecordStartRejectsABadSize(t *testing.T) {
	setBrowserIdentity(t)
	setConfigEnv(t)
	deps := Deps{ProcessAlive: func(int) bool { return true }}
	for _, args := range [][]string{{"--size", "390 x 844"}, {"--size", "Big Phone"}, {"--mobile"}} {
		full := append([]string{"browser", "record", "start"}, args...)
		if _, _, err := executeCLI(t, deps, full...); err == nil {
			t.Fatalf("record start %v accepted", args)
		}
	}
}

func TestBrowserRecordStartRejectsOutOfRangeFlags(t *testing.T) {
	setBrowserIdentity(t)
	setConfigEnv(t)
	deps := Deps{ProcessAlive: func(int) bool { return true }}
	for _, flags := range [][]string{{"--fps", "0"}, {"--fps", "31"}, {"--max-seconds", "0"}, {"--max-seconds", "1801"}} {
		args := append([]string{"browser", "record", "start"}, flags...)
		if _, _, err := executeCLI(t, deps, args...); err == nil {
			t.Fatalf("record start %v accepted", flags)
		}
	}
}

func TestBrowserRecordStopSavesTheVideoFromChunks(t *testing.T) {
	setBrowserIdentity(t)
	cfg := setConfigEnv(t)
	server := &browserRecordServer{video: []byte("webm-video-bytes"), chunk: 5}
	writeRunFileFor(t, cfg, server.start(t))
	deps := Deps{ProcessAlive: func(int) bool { return true }}
	target := filepath.Join(t.TempDir(), "run.webm")

	out, errOut, err := executeCLI(t, deps, "browser", "record", "stop", target)
	if err != nil {
		t.Fatalf("record stop err=%v stderr=%s", err, errOut)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "webm-video-bytes" {
		t.Fatalf("saved data=%q err=%v", data, err)
	}
	if !strings.Contains(out, "Saved "+target) || !strings.Contains(out, "4.2 s") || !strings.Contains(out, "1280x720") {
		t.Fatalf("record stop output = %q", out)
	}
	want := []string{"record-stop", "record-read", "record-read", "record-read", "record-read"}
	if got := server.actions(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("actions = %v, want %v", got, want)
	}
	// It never overwrites a file.
	if _, _, err := executeCLI(t, deps, "browser", "record", "stop", target); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("overwrite error = %v", err)
	}
}

func TestBrowserRecordStopJSONReturnsFileMetadata(t *testing.T) {
	setBrowserIdentity(t)
	cfg := setConfigEnv(t)
	server := &browserRecordServer{video: []byte("abc"), chunk: 8}
	writeRunFileFor(t, cfg, server.start(t))
	deps := Deps{ProcessAlive: func(int) bool { return true }}
	target := filepath.Join(t.TempDir(), "run.webm")

	out, _, err := executeCLI(t, deps, "browser", "record", "stop", target, "--json")
	if err != nil {
		t.Fatalf("record stop --json: %v", err)
	}
	var result browserRecordFileResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if result.Path != target || result.Size != 3 || result.DurationMs != 4200 || result.Frames != 31 || result.StopReason != "stopped" {
		t.Fatalf("result = %#v", result)
	}
}

func TestBrowserRecordStopRemovesAPartialFile(t *testing.T) {
	setBrowserIdentity(t)
	cfg := setConfigEnv(t)
	server := &browserRecordServer{video: []byte("0123456789abcdef"), chunk: 5, truncate: true}
	writeRunFileFor(t, cfg, server.start(t))
	deps := Deps{ProcessAlive: func(int) bool { return true }}
	target := filepath.Join(t.TempDir(), "run.webm")

	if _, _, err := executeCLI(t, deps, "browser", "record", "stop", target); err == nil || !strings.Contains(err.Error(), "ended early") {
		t.Fatalf("truncated recording error = %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("partial file left behind: %v", err)
	}
}

func TestBrowserRecordStatusDescribesTheRecording(t *testing.T) {
	setBrowserIdentity(t)
	cfg := setConfigEnv(t)
	server := &browserRecordServer{}
	writeRunFileFor(t, cfg, server.start(t))
	deps := Deps{ProcessAlive: func(int) bool { return true }}

	out, _, err := executeCLI(t, deps, "browser", "record", "status")
	if err != nil || !strings.Contains(out, "Recording: 4.2 s, 31 frames at up to 10 fps.") {
		t.Fatalf("record status out=%q err=%v", out, err)
	}
}
