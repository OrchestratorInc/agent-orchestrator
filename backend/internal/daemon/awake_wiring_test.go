package daemon

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
)

type unreadableAwakeStore struct {
	raw     string
	err     error
	saveErr error
	writes  int
}

func (s *unreadableAwakeStore) LoadAwake(context.Context) (string, error) { return s.raw, s.err }
func (s *unreadableAwakeStore) SaveAwake(context.Context, string) error   { s.writes++; return s.saveErr }

func TestAwakeStartupFailureDisablesOnlyOptionalService(t *testing.T) {
	for _, tt := range []struct {
		name, raw  string
		err        error
		saveErr    error
		wantWrites int
	}{
		{name: "load failure", err: errors.New("awake storage unavailable")},
		{name: "corrupt document", raw: `{"version":`},
		{name: "newer document", raw: `{"version":2,"settings":{},"rules":[]}`},
		{name: "interrupted intent save failure", raw: `{"version":1,"sessions":[{}]}`, saveErr: errors.New("storage write failed"), wantWrites: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := &unreadableAwakeStore{raw: tt.raw, err: tt.err, saveErr: tt.saveErr}
			var logs bytes.Buffer
			// Failed initialization must not touch a host or start a policy loop.
			svc, stop := startAwake(context.Background(), store, nil, nil, slog.New(slog.NewTextHandler(&logs, nil)))
			defer stop()
			if svc != nil {
				t.Fatal("failed optional service was mounted")
			}
			if store.writes != tt.wantWrites {
				t.Fatalf("unexpected intent writes: %d, want %d", store.writes, tt.wantWrites)
			}
			if !strings.Contains(logs.String(), "its API will report unimplemented") || !strings.Contains(logs.String(), "error=") {
				t.Fatal("startup failure not logged")
			}
			router := httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(&logs, nil)), nil, httpd.APIDeps{Awake: svc}, httpd.ControlDeps{})
			for _, probe := range []struct {
				path   string
				status int
			}{
				{"/healthz", http.StatusOK}, {"/readyz", http.StatusOK}, {"/api/v1/awake", http.StatusNotImplemented},
			} {
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, probe.path, nil))
				if w.Code != probe.status {
					t.Fatalf("%s status = %d, want %d", probe.path, w.Code, probe.status)
				}
			}
		})
	}
}
