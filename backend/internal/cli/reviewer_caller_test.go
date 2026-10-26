package cli

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDaemonRequestsCarryReviewerIdentity pins the CLI half of the daemon's
// reviewer gate (#6842): inside a reviewer pane every daemon request, including
// the /shutdown control route that builds its own request, names the reviewer.
func TestDaemonRequestsCarryReviewerIdentity(t *testing.T) {
	for _, reviewID := range []string{"", "rv-1"} {
		t.Run("review-"+reviewID, func(t *testing.T) {
			cfg := setConfigEnv(t)
			t.Setenv(envReviewSessionID, reviewID)
			var seen []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = append(seen, r.URL.Path)
				if got := r.Header.Get(reviewSessionHeader); got != reviewID {
					t.Errorf("%s %s: %s = %q, want %q", r.Method, r.URL.Path, reviewSessionHeader, got, reviewID)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			t.Cleanup(srv.Close)
			writeRunFileFor(t, cfg, srv)
			c := &commandContext{deps: Deps{HTTPClient: srv.Client(), ProcessAlive: func(int) bool { return true }}}
			if err := c.postJSON(context.Background(), "sessions", nil, nil); err != nil {
				t.Fatal(err)
			}
			if err := c.requestShutdown(context.Background(), srv.Listener.Addr().(*net.TCPAddr).Port); err != nil {
				t.Fatal(err)
			}
			if len(seen) != 2 {
				t.Fatalf("requests = %v, want the API call and /shutdown", seen)
			}
		})
	}
}
