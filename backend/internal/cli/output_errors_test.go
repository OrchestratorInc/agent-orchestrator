package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// runCLIForError runs args through executeWithDeps, as the binary does, and
// returns what the entrypoint would print for the error.
func runCLIForError(t *testing.T, deps Deps, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	deps.Out = &out
	deps.Err = &errOut
	deps.Sleep = func(time.Duration) {}
	err := executeWithDeps(deps, args)
	var printed bytes.Buffer
	PrintError(&printed, err)
	return printed.String(), err
}

func decodeErrorEnvelope(t *testing.T, printed string) cliErrorBody {
	t.Helper()
	if strings.Count(strings.TrimSpace(printed), "\n") != 0 {
		t.Fatalf("envelope must be one line: %q", printed)
	}
	var env cliErrorEnvelope
	if err := json.Unmarshal([]byte(printed), &env); err != nil {
		t.Fatalf("stderr is not a JSON envelope: %v\n%s", err, printed)
	}
	return env.Error
}

func TestJSONErrorCarriesDaemonEnvelope(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := projectServer(t, http.StatusNotFound, `{"message":"project not found","code":"PROJECT_NOT_FOUND","requestId":"req-7"}`)
	writeRunFileFor(t, cfg, srv)

	printed, err := runCLIForError(t, Deps{ProcessAlive: func(int) bool { return true }}, "project", "get", "missing", "--json")
	if ExitCode(err) != 1 {
		t.Fatalf("exit code = %d, want 1 (err=%v)", ExitCode(err), err)
	}
	got := decodeErrorEnvelope(t, printed)
	want := cliErrorBody{Code: "PROJECT_NOT_FOUND", Message: "project not found", RequestID: "req-7", Status: 404, ExitCode: 1}
	if got != want {
		t.Fatalf("envelope = %+v, want %+v", got, want)
	}
}

func TestJSONErrorForDaemonNotRunning(t *testing.T) {
	setConfigEnv(t)
	printed, err := runCLIForError(t, Deps{}, "project", "ls", "--json")
	if !errors.Is(err, errDaemonUnavailable) {
		t.Fatalf("err = %v, want daemon unavailable", err)
	}
	if got := decodeErrorEnvelope(t, printed); got.Code != errCodeDaemonUnavailable || got.ExitCode != 1 {
		t.Fatalf("envelope = %+v", got)
	}
}

func TestJSONErrorForUsageMistakes(t *testing.T) {
	setConfigEnv(t)
	for name, args := range map[string][]string{
		"missing argument":   {"project", "get", "--json"},
		"unknown flag":       {"project", "ls", "--json", "--nope"},
		"flag before error":  {"--json", "project", "ls", "--nope"},
		"command without it": {"report", "--json", "hello"},
	} {
		t.Run(name, func(t *testing.T) {
			printed, err := runCLIForError(t, Deps{}, args...)
			if ExitCode(err) != 2 {
				t.Fatalf("exit code = %d, want 2 (err=%v)", ExitCode(err), err)
			}
			if got := decodeErrorEnvelope(t, printed); got.Code != errCodeUsage || got.ExitCode != 2 || got.Message == "" {
				t.Fatalf("envelope = %+v", got)
			}
		})
	}
}

func TestUnsupportedJSONIsRefusedBeforeRunning(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "proj-1")
	_, err := runCLIForError(t, Deps{}, "report", "--json", "hello")
	if err == nil || !strings.Contains(err.Error(), "ao report does not support --json yet") {
		t.Fatalf("err = %v", err)
	}
}

func TestErrorsStayPlainTextWithoutJSON(t *testing.T) {
	setConfigEnv(t)
	for _, args := range [][]string{{"project", "ls"}, {"project", "ls", "--json=false"}} {
		printed, _ := runCLIForError(t, Deps{}, args...)
		if strings.HasPrefix(printed, "{") || !strings.Contains(printed, "not running") {
			t.Fatalf("%v: printed = %q, want plain text", args, printed)
		}
	}
}
