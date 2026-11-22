package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRemoteHostUpdateCLI(t *testing.T) {
	for _, tc := range []struct {
		name   string
		gets   []string
		posted bool
		want   string
		err    string
	}{
		{"updates through the restart", []string{`{"version":"0.15.3","supported":true,"state":"idle"}`, `{"version":"0.15.3","supported":true,"state":"updating"}`, "", `{"version":"0.15.4","supported":true,"state":"updating"}`, `{"version":"0.15.4","supported":true,"state":"idle"}`}, true, "Updated Orchestrator to v0.15.4", ""},
		{"no newer release", []string{`{"version":"0.15.4","supported":true,"state":"idle"}`, `{"version":"0.15.4","supported":true,"state":"idle"}`}, true, "Already on the newest release: v0.15.4", ""},
		{"reports the failure", []string{`{"version":"0.15.3","supported":true,"state":"idle"}`, `{"version":"0.15.3","supported":true,"state":"failed","error":"download failed"}`}, true, "", "the update failed: download failed"},
		{"waits for a running update", []string{`{"version":"0.15.3","supported":true,"state":"updating"}`, `{"version":"0.15.4","supported":true,"state":"idle"}`}, false, "Updated Orchestrator to v0.15.4", ""},
		{"refuses an unsupported host", []string{`{"version":"0.15.3","supported":false,"state":"idle"}`}, false, "", "setup-self-hosted.sh"},
		{"gives up when the daemon stays down", []string{`{"version":"0.15.3","supported":true,"state":"idle"}`}, true, "", "hasn't answered for 10 minutes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			gets, posted := tc.gets, false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/remote-host/update" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPost {
					posted = true
					w.WriteHeader(http.StatusAccepted)
					_, _ = io.WriteString(w, `{"version":"0.15.3","supported":true,"state":"updating"}`)
					return
				}
				if len(gets) == 0 || gets[0] == "" {
					if len(gets) > 0 {
						gets = gets[1:]
					}
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				body := gets[0]
				gets = gets[1:]
				_, _ = io.WriteString(w, body)
			}))
			t.Cleanup(srv.Close)
			writeRunFileFor(t, cfg, srv)
			now := time.Unix(0, 0)
			out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }, Now: func() time.Time { return now }, Sleep: func(d time.Duration) { now = now.Add(d) }}, "remote-host", "update")
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want %q", err, tc.err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("output %q missing %q", out, tc.want)
			}
			if posted != tc.posted {
				t.Fatalf("posted = %v, want %v", posted, tc.posted)
			}
		})
	}
}
