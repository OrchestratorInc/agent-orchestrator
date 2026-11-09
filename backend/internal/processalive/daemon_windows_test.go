//go:build windows

package processalive_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cli"
	"github.com/aoagents/agent-orchestrator/backend/internal/daemonmeta"
	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

// TestObservationFailurePreservesDaemon 覆盖 Wait 失败经默认 Deps 接线进入状态、client 和停止轮询。
// 只执行 status/stop/version；HTTP 使用本地 fixture，不触发浏览器 Installer 或真实 daemon。
func TestObservationFailurePreservesDaemon(t *testing.T) {
	pid, stop := observationChild(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "running.json")
	t.Setenv("AO_DATA_DIR", dir)
	t.Setenv("AO_RUN_FILE", path)
	var serving atomic.Bool
	var telemetry, shutdown atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/telemetry/cli-invoked":
			telemetry.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case "/shutdown":
			shutdown.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			if !serving.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			status := "ok"
			if r.URL.Path == "/readyz" {
				status = "ready"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "service": daemonmeta.ServiceName, "pid": pid})
		}
	}))
	t.Cleanup(server.Close)
	_, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	if err := runfile.Write(path, runfile.Info{PID: pid, Port: port}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	unchanged := func() {
		t.Helper()
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Errorf("unknown observation changed run-file: %q, %v", after, err)
		}
		// 红测继续验证每个入口，避免一次误删掩盖后续边界。
		if err := os.WriteFile(path, before, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) (string, error) {
		t.Helper()
		var out bytes.Buffer
		deps := cli.DefaultDeps()
		deps.Out, deps.Err = &out, &out
		deps.HTTPClient = server.Client()
		now := time.Unix(200, 0)
		deps.Now = func() time.Time { return now }
		deps.Sleep = func(d time.Duration) { now = now.Add(d) }
		cmd := cli.NewRootCommand(deps)
		cmd.SetArgs(args)
		err := cmd.Execute()
		return out.String(), err
	}
	restore := processalive.FailProbeForTest(t, pid, true)
	if live, err := runfile.CheckStale(path); err == nil {
		t.Errorf("startup must refuse unknown observation, got live=%v err=nil", live)
	}
	if out, err := run("status", "--json"); err != nil || !strings.Contains(out, `"state": "unhealthy"`) {
		t.Errorf("unknown status = %s, %v", out, err)
	}
	if out, err := run("stop", "--json"); err == nil || strings.Contains(out, `"state": "stopped"`) || strings.Contains(fmt.Sprint(err), "is alive") {
		t.Errorf("unknown stop claimed a definite state: %s, %v", out, err)
	}
	unchanged()
	telemetry.Store(0)
	if _, err := run("version"); err != nil || telemetry.Load() != 1 {
		t.Errorf("client discarded unknown owner before HTTP: calls=%d, err=%v", telemetry.Load(), err)
	}
	serving.Store(true)
	if out, err := run("status", "--json"); err != nil || !strings.Contains(out, `"state": "ready"`) {
		t.Errorf("PID-verified health did not establish readiness: %s, %v", out, err)
	}
	if out, err := run("stop", "--json", "--timeout=1ms"); err == nil || shutdown.Load() != 1 || strings.Contains(out, `"state": "stopped"`) {
		t.Errorf("unknown exit after acknowledged shutdown: %s, %v, shutdown calls=%d", out, err, shutdown.Load())
	}
	unchanged()
	stop()
	restore()
	if live, err := runfile.CheckStale(path); live != nil || err != nil {
		t.Errorf("confirmed dead startup was blocked: %+v, %v", live, err)
	}
	if out, err := run("stop", "--json"); err != nil || !strings.Contains(out, `"state": "stopped"`) {
		t.Errorf("confirmed dead stop = %s, %v", out, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("confirmed dead run-file was not removed: %v", err)
	}
}
