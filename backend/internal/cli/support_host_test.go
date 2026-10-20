package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestSupportHostBootAndArgumentErrorsDoNotContactDaemon(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dir)
	t.Setenv("AO_PORT", "invalid-for-daemon")
	t.Setenv("AO_RUN_FILE", filepath.Join(dir, "missing-running.json"))
	for _, args := range [][]string{
		{"support-host"}, {"support-host", "--data-dir", dir},
		{"support-host", "extra"}, {"support-host", "--unknown-private-flag"},
		{"support-host", "--data-dir"}, {"support-host", "--data-dir", "relative"},
		{"support-host", "--tool"}, {"support-host", "--run", "report"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			called := false
			var out bytes.Buffer
			err := executeWithDeps(Deps{In: strings.NewReader(""), Out: &out, Err: io.Discard,
				HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					t.Fatal("support invocation contacted the daemon or telemetry")
					return nil, nil
				})},
				SupportHost: func(_ context.Context, _ io.Reader, _ io.Writer, dataDir string) error {
					called = true
					if dataDir != dir {
						t.Fatalf("data dir=%s", dataDir)
					}
					return nil
				},
			}, args)
			valid := len(args) == 1 || len(args) == 3 && args[2] == dir
			if valid && (err != nil || !called) {
				t.Fatalf("independent boot failed: %v", err)
			}
			if !valid && (called || ExitCode(err) != 2) {
				t.Fatalf("invalid invocation = called %v, error %v", called, err)
			}
		})
	}
}
