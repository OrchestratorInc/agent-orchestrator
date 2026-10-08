package httpd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	telemetryadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/telemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

func TestTelemetryIdentityRoutes(t *testing.T) {
	dir := t.TempDir()
	ident, err := telemetryadapter.NewIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRouterWithControl(config.Config{DataDir: dir}, discardLogger(), nil, APIDeps{TelemetryIdentity: ident}, ControlDeps{})

	post := func(host, origin, body string) int {
		req := httptest.NewRequest(http.MethodPost, "http://"+host+"/internal/telemetry/identity", strings.NewReader(body))
		req.Host = host
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	get := func() map[string]any {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3001/api/v1/telemetry/identity", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET identity = %d: %s", rec.Code, rec.Body)
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	// Install ID until someone signs in.
	if got := get(); got["distinctId"] != ident.Snapshot().InstallID || got["optedOut"] != false {
		t.Fatalf("anonymous identity = %#v", got)
	}

	// Only loopback callers without an Origin (Electron main) may set the user.
	if c := post("127.0.0.1:3001", "app://renderer", `{"cloudUserId":"user_01H"}`); c != http.StatusForbidden {
		t.Fatalf("renderer origin = %d, want 403", c)
	}
	if c := post("evil.example:3001", "", `{"cloudUserId":"user_01H"}`); c != http.StatusForbidden {
		t.Fatalf("non-loopback host = %d, want 403", c)
	}
	if c := post("127.0.0.1:3001", "", `{"cloudUserId":"a@b.com"}`); c != http.StatusBadRequest {
		t.Fatalf("email-shaped id = %d, want 400", c)
	}
	if c := post("127.0.0.1:3001", "", `{"cloudUserId":"user_01H","email":"a@b.com"}`); c != http.StatusBadRequest {
		t.Fatalf("unknown field (email) = %d, want 400", c)
	}
	if c := post("127.0.0.1:3001", "", `{"cloudUserId":"user_01H"}`); c != http.StatusNoContent {
		t.Fatalf("valid sign-in = %d, want 204", c)
	}
	got := get()
	if got["distinctId"] != "user_01H" || got["cloudUserId"] != "user_01H" {
		t.Fatalf("signed-in identity = %#v", got)
	}
	if _, ok := got["email"]; ok {
		t.Fatal("identity response leaks email")
	}

	// Opt-out hides everything from the phone.
	if err := os.WriteFile(filepath.Join(dir, telemetryadapter.OptOutFile), []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got = get()
	if got["optedOut"] != true || got["distinctId"] != "" {
		t.Fatalf("opted-out identity = %#v", got)
	}
}
