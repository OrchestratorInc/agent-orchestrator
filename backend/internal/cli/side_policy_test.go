package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDaemonRequestsCarrySideScopeWithoutChangingMainRequests(t *testing.T) {
	for _, sideID := range []string{"", "side-1"} {
		t.Run("scope-"+sideID, func(t *testing.T) {
			cfg := setConfigEnv(t)
			t.Setenv("AO_SIDE_CONVERSATION_ID", sideID)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("X-AO-Side-Conversation"); got != sideID {
					t.Errorf("scope=%q want=%q", got, sideID)
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
			if sideID != "" {
				if err := c.doJSONPathWithHeaders(context.Background(), http.MethodPost, "/api/v1/orchestrators/delegate", nil, nil, map[string]string{"X-AO-Side-Conversation": "override"}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
