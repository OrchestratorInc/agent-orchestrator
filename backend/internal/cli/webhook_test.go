package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func webhookDaemon(t *testing.T, requests *[]string) {
	t.Helper()
	cfg := setConfigEnv(t)
	view := map[string]any{"name": "main", "host": "bots.example", "headerNames": []string{"Authorization"}, "projects": []string{"web"}, "events": []string{}, "status": map[string]any{"sent": 3, "lastSentAt": "2026-11-14T09:00:00Z"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*requests = append(*requests, strings.TrimSpace(r.Method+" "+r.URL.Path+" "+string(body)))
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/projects/web":
			_ = json.NewEncoder(w).Encode(map[string]any{"project": map[string]any{"id": "web"}})
		case "PUT /api/v1/webhooks/main", "PATCH /api/v1/webhooks/main":
			_ = json.NewEncoder(w).Encode(view)
		case "GET /api/v1/webhooks":
			_ = json.NewEncoder(w).Encode(map[string]any{"webhooks": []any{view}, "kinds": []string{"needs_input"}})
		case "POST /api/v1/webhooks/main/test":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "POST /api/v1/webhooks/down/test":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "HTTP 401 bad key"})
		case "DELETE /api/v1/webhooks/main":
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "NOT_FOUND", "message": "not found"})
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
}

func TestWebhookCommands(t *testing.T) {
	var requests []string
	webhookDaemon(t, &requests)
	run := func(stdin string, args ...string) (string, error) {
		out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }, In: strings.NewReader(stdin)}, append([]string{"webhook"}, args...)...)
		return out, err
	}

	out, err := run("https://bots.example/hook/abc\nAuthorization: Bearer topsecret\n", "add", "main", "--project", "web", "--events", "needs_input, finished")
	if err != nil || strings.Contains(out, "topsecret") || !strings.Contains(out, "saved webhook main (bots.example)") {
		t.Fatalf("add = %q, %v", out, err)
	}
	put := requests[len(requests)-1]
	for _, want := range []string{`"url":"https://bots.example/hook/abc"`, `"Authorization":"Bearer topsecret"`, `"projects":["web"]`, `"events":["needs_input","finished"]`} {
		if !strings.Contains(put, want) {
			t.Fatalf("PUT body %s lacks %s", put, want)
		}
	}
	if _, err := run("https://x.example\n\n", "add", "main", "--project", "nope"); ExitCode(err) != 2 || !strings.Contains(err.Error(), `no project "nope"`) {
		t.Fatalf("unknown project = %v", err)
	}
	if _, err := run("", "add", "main"); ExitCode(err) != 2 {
		t.Fatalf("empty stdin = %v", err)
	}

	if _, err := run("", "set", "main", "--all-projects", "--events", "exited"); err != nil {
		t.Fatal(err)
	}
	if patch := requests[len(requests)-1]; !strings.Contains(patch, `"projects":[]`) || !strings.Contains(patch, `"events":["exited"]`) || strings.Contains(patch, "quietAtDesk") {
		t.Fatalf("PATCH body = %s", patch)
	}
	if _, err := run("", "set", "main"); ExitCode(err) != 2 {
		t.Fatalf("set without flags = %v", err)
	}
	if _, err := run("", "set", "main", "--all-events", "--events", "exited"); ExitCode(err) != 2 {
		t.Fatalf("conflicting set = %v", err)
	}

	out, err = run("", "ls")
	if err != nil || !strings.Contains(out, "bots.example") || !strings.Contains(out, "all but worker_finished") || !strings.Contains(out, "ok, last sent") {
		t.Fatalf("ls = %q, %v", out, err)
	}
	if out, err := run("", "test", "main"); err != nil || strings.TrimSpace(out) != "OK" {
		t.Fatalf("test = %q, %v", out, err)
	}
	if _, err := run("", "test", "down"); err == nil || !strings.Contains(err.Error(), "HTTP 401 bad key") {
		t.Fatalf("failing test = %v", err)
	}
	if out, err := run("", "rm", "main"); err != nil || !strings.Contains(out, "removed webhook main") {
		t.Fatalf("rm = %q, %v", out, err)
	}
}
