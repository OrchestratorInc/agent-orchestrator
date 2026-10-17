//go:build !windows

package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/creack/pty"
)

func TestGHProxyPreservesInteractiveTerminal(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "worker-1")
	t.Setenv("AO_REVIEW_SESSION_ID", "")
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("interactive command must not register") }))
	defer srv.Close()
	writeRunFileFor(t, cfg, srv)
	master, terminal, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer terminal.Close()
	c := commandContext{deps: Deps{Out: terminal, Err: io.Discard, ProcessAlive: func(int) bool { return true }, RunInteractiveCommand: func(_ context.Context, _ string, _ []string, _ io.Reader, stdout, _ io.Writer) error {
		if stdout != terminal {
			t.Error("wrapper replaced terminal with a pipe")
		}
		_, err := io.WriteString(stdout, "https://github.com/example/frontend/pull/123\n")
		return err
	}}.withDefaults()}
	if code := c.runGH(context.Background(), "gh", []string{"pr", "create"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
}
