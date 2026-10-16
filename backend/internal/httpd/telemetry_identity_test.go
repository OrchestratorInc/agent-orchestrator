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

// A sign-out whose install ID rotation fails must not leave the phone on the
// ID that is still linked to the previous account.
func TestTelemetryIdentityRouteSuppressesPhoneAfterFailedRotation(t *testing.T) {
	dir := t.TempDir()
	ident, err := telemetryadapter.NewIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRouterWithControl(config.Config{DataDir: dir}, discardLogger(), nil, APIDeps{TelemetryIdentity: ident}, ControlDeps{})
	post := func(body string) int {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:3001/internal/telemetry/identity", strings.NewReader(body))
		req.Host = "127.0.0.1:3001"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	get := func() map[string]any {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3001/api/v1/telemetry/identity", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	if c := post(`{"cloudUserId":"user_A"}`); c != http.StatusNoContent {
		t.Fatalf("sign-in = %d", c)
	}
	// A non-empty directory where the install ID file lives makes the rename fail.
	idPath := filepath.Join(dir, "telemetry_install_id")
	if err := os.Remove(idPath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(idPath, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if c := post(`{"cloudUserId":""}`); c != http.StatusInternalServerError {
		t.Fatalf("failed rotation = %d, want 500", c)
	}
	got := get()
	if got["optedOut"] != true {
		t.Fatalf("phone must be told to stop after a failed rotation: %#v", got)
	}
	for _, k := range []string{"distinctId", "cloudUserId", "githubLogin"} {
		if v, ok := got[k]; ok && v != "" {
			t.Fatalf("%s leaked while suppressed: %#v", k, got)
		}
	}

	// Recovery lifts the suppression and hands out the retired-and-replaced ID.
	if err := os.RemoveAll(idPath); err != nil {
		t.Fatal(err)
	}
	if c := post(`{"cloudUserId":""}`); c != http.StatusNoContent {
		t.Fatalf("retry = %d, want 204", c)
	}
	got = get()
	if got["optedOut"] != false || got["distinctId"] == "" {
		t.Fatalf("suppression not lifted after recovery: %#v", got)
	}
}

// A request delayed past the client's timeout can reach the daemon after a newer
// one. The daemon must keep the newer identity.
func TestTelemetryIdentityRouteRejectsAnOlderSequence(t *testing.T) {
	dir := t.TempDir()
	ident, err := telemetryadapter.NewIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRouterWithControl(config.Config{DataDir: dir}, discardLogger(), nil, APIDeps{TelemetryIdentity: ident}, ControlDeps{})
	var lastBody string
	post := func(body string) int {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:3001/internal/telemetry/identity", strings.NewReader(body))
		req.Host = "127.0.0.1:3001"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		lastBody = rec.Body.String()
		return rec.Code
	}

	if c := post(`{"cloudUserId":"user_B","sequence":20}`); c != http.StatusNoContent {
		t.Fatalf("newer request = %d", c)
	}
	if c := post(`{"cloudUserId":"user_A","sequence":10}`); c != http.StatusConflict {
		t.Fatalf("late older request = %d, want 409", c)
	}
	// The rejection tells a client whose clock regressed where the daemon is.
	if !strings.Contains(lastBody, `"lastSequence":20`) {
		t.Fatalf("409 body should carry the daemon's sequence: %s", lastBody)
	}
	if got := ident.Snapshot().CloudUserID; got != "user_B" {
		t.Fatalf("daemon ended on %q, want user_B", got)
	}
	// Sign-out that was sent after B wins; the same sequence is an idempotent retry.
	if c := post(`{"cloudUserId":"","sequence":30}`); c != http.StatusNoContent {
		t.Fatalf("sign-out = %d", c)
	}
	if c := post(`{"cloudUserId":"","sequence":30}`); c != http.StatusNoContent {
		t.Fatalf("retry of the same sequence = %d", c)
	}
	if c := post(`{"cloudUserId":"user_B","sequence":25}`); c != http.StatusConflict {
		t.Fatalf("late request after sign-out = %d, want 409", c)
	}
	if got := ident.Snapshot().CloudUserID; got != "" {
		t.Fatalf("daemon ended on %q, want signed out", got)
	}
}
