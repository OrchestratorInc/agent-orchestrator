package main

import (
	"io"
	"log/slog"
	"strings"
	"testing"
)

// Removing the early login branch would require normal worker session/bootstrap
// configuration before login, making the isolated auth-only sandbox unusable.
func TestRunAgentLoginModePrecedesWorkerValidation(t *testing.T) {
	t.Setenv("AO_AGENT_LOGIN_ID", "d855bbec-c4f4-40ce-a730-39add5fa2506")
	t.Setenv("AO_AGENT_LOGIN_TOKEN", "attempt-only-token")
	t.Setenv("AO_CLOUD_PUBLIC_URL", "http://127.0.0.1:1")
	t.Setenv("AO_DATA_DIR", t.TempDir())
	t.Setenv("AO_CLOUD_SESSION_ID", "")
	t.Setenv("AO_WORKER_BOOTSTRAP_TOKEN", "")
	t.Setenv("AO_WORKSPACE_DIR", "")
	err := run(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "agent login requires HTTPS") {
		t.Fatalf("login-only entry did not bypass normal worker startup: %v", err)
	}
}
