package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCuaCallSendsToolArgumentsAndSession(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "worker-1")
	tests := []struct {
		name  string
		args  []string
		stdin string
		want  map[string]any
	}{
		{name: "no arguments", args: []string{"cua", "call", "list_windows"}},
		{name: "inline JSON", args: []string{"cua", "call", "get_window_state", `{"pid":7,"window_id":9}`}, want: map[string]any{"pid": float64(7), "window_id": float64(9)}},
		{name: "stdin JSON", args: []string{"cua", "call", "click", "-"}, stdin: `{"element_token":"s00000019:12"}`, want: map[string]any{"element_token": "s00000019:12"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			var got cuaCallRequest
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/computer-use/call" {
					return // the CLI's own telemetry ping
				}
				if r.Method != http.MethodPost {
					t.Errorf("method = %s", r.Method)
				}
				_ = json.NewDecoder(r.Body).Decode(&got)
				_, _ = io.WriteString(w, `{"tool":"get_window_state","result":{"screenshot_file_path":"/data/computer-use/worker-1/a.png"},"images":[{"path":"/data/computer-use/worker-1/a.png","mimeType":"image/png"}]}`)
			}))
			defer srv.Close()
			writeRunFileFor(t, cfg, srv)
			out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }, In: strings.NewReader(tc.stdin)}, append(tc.args, "--json")...)
			if err != nil {
				t.Fatal(err)
			}
			if got.Tool != tc.args[2] || got.SessionID != "worker-1" {
				t.Fatalf("request = %+v", got)
			}
			if len(got.Arguments) != len(tc.want) {
				t.Fatalf("arguments = %v, want %v", got.Arguments, tc.want)
			}
			for key, value := range tc.want {
				if got.Arguments[key] != value {
					t.Fatalf("arguments[%s] = %v, want %v", key, got.Arguments[key], value)
				}
			}
			var printed cuaCallResponse
			if err := json.Unmarshal([]byte(out), &printed); err != nil {
				t.Fatalf("stdout is not one JSON document: %v\n%s", err, out)
			}
			if !strings.Contains(errOut, "screenshot saved: /data/computer-use/worker-1/a.png") {
				t.Fatalf("stderr = %q", errOut)
			}
		})
	}
}

func TestCuaCallRejectsNonObjectArguments(t *testing.T) {
	for _, raw := range []string{`[1,2]`, `"text"`, `{bad`, `null`} {
		_, _, err := executeCLI(t, Deps{}, "cua", "call", "click", raw)
		if err == nil || ExitCode(err) != 2 {
			t.Fatalf("arguments %s: err=%v exit=%d", raw, err, ExitCode(err))
		}
	}
	if _, _, err := executeCLI(t, Deps{}, "cua", "call"); err == nil || ExitCode(err) != 2 {
		t.Fatalf("missing tool: err=%v", err)
	}
}

func TestCuaStatusRendersDaemonFacts(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/computer-use/status" {
			return
		}
		_, _ = io.WriteString(w, `{"platform":"darwin","installed":true,"binaryPath":"/Applications/CuaDriver.app/Contents/MacOS/cua-driver","version":"0.28.2","latestVersion":"0.37.0","updateAvailable":true,"daemon":"running","permissions":{"accessibility":"granted","screenRecording":"missing","granting":false},"imageDirectory":"/data/computer-use"}`)
	}))
	defer srv.Close()
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "cua", "status")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Cua Driver 0.28.2 (0.37.0 available)", "service:      running", "screen recording: missing", "screenshots:  /data/computer-use"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %q:\n%s", want, out)
		}
	}
}

func TestCuaStatusNotInstalled(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"platform":"linux","installed":false,"daemon":"unknown","imageDirectory":"/data/computer-use"}`)
	}))
	defer srv.Close()
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "cua", "status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Settings > Computer use agents") {
		t.Fatalf("out = %q", out)
	}
}

func TestCuaCallSurfacesDaemonErrorEnvelope(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/computer-use/call" {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"conflict","code":"COMPUTER_USE_NOT_INSTALLED","message":"Cua Driver is not installed. Install it in Settings > Computer use agents.","requestId":"req-1"}`)
	}))
	defer srv.Close()
	writeRunFileFor(t, cfg, srv)
	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "cua", "call", "list_apps")
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "Cua Driver is not installed") {
		t.Fatalf("err=%v exit=%d", err, ExitCode(err))
	}
}

// The guide is the skill's commands/cua.md, printed without a daemon.
func TestCuaGuidePrintsEmbeddedHowTo(t *testing.T) {
	out, _, err := executeCLI(t, Deps{}, "cua", "guide")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# ao cua", "ao cua call get_window_state", "## Safety", "## Errors"} {
		if !strings.Contains(out, want) {
			t.Fatalf("guide missing %q:\n%s", want, out)
		}
	}
}
