package mcpserver

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/runfile"
)

type testConfig struct{ runFile string }

func setConfigEnv(t *testing.T) testConfig {
	t.Helper()
	dir := t.TempDir()
	cfg := testConfig{runFile: filepath.Join(dir, "running.json")}
	t.Setenv("AO_RUN_FILE", cfg.runFile)
	t.Setenv("AO_DATA_DIR", filepath.Join(dir, "data"))
	t.Setenv("AO_PORT", "3001")
	return cfg
}

// writeRunFileFor points the run file at srv.
func writeRunFileFor(t *testing.T, cfg testConfig, srv *httptest.Server) {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, portRaw, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil {
		t.Fatal(err)
	}
	if err := runfile.Write(cfg.runFile, runfile.Info{PID: os.Getpid(), Port: port, StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatalf("write run-file: %v", err)
	}
}

type previewCapture struct {
	body, path, method string
	called             bool
}

func renderServer(t *testing.T, status int, respBody string) (*httptest.Server, *previewCapture) {
	t.Helper()
	capture := &previewCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/renders") && !strings.HasSuffix(r.URL.Path, "/renders/check") {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		capture.called, capture.body, capture.path, capture.method = true, string(body), r.URL.Path, r.Method
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, capture
}
