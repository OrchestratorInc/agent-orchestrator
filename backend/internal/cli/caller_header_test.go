package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/caller"
)

// An agent's own command names its session so the daemon can file the action
// under its turn; a command typed in the session's terminal tab does not.
func TestDaemonRequestsNameTheCallingAgentButNotItsShellTabs(t *testing.T) {
	for _, tc := range []struct {
		name, session, shell, want string
	}{
		{name: "agent", session: "proj-1", want: "proj-1"},
		{name: "shell tab", session: "proj-1", shell: "1", want: ""},
		{name: "outside AO", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			t.Setenv("AO_SESSION_ID", tc.session)
			t.Setenv(caller.ShellTerminalEnv, tc.shell)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get(caller.Header); got != tc.want {
					t.Errorf("caller = %q, want %q", got, tc.want)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			t.Cleanup(srv.Close)
			writeRunFileFor(t, cfg, srv)
			c := &commandContext{deps: Deps{HTTPClient: srv.Client(), ProcessAlive: func(int) bool { return true }}}
			if err := c.doJSONPathWithHeaders(context.Background(), http.MethodPost, "/api/v1/orchestrators/delegate", nil, nil, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
