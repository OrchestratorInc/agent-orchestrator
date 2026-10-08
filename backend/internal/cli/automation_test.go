package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAutomationListFollowsAllPages(t *testing.T) {
	for _, enabled := range []string{"", "false", "true"} {
		for _, jsonOutput := range []bool{false, true} {
			t.Run(fmt.Sprintf("enabled=%s/json=%t", enabled, jsonOutput), func(t *testing.T) {
				cfg := setConfigEnv(t)
				requests := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/internal/telemetry/cli-invoked" {
						w.WriteHeader(http.StatusNoContent)
						return
					}
					requests++
					query := r.URL.Query()
					if r.Method != http.MethodGet || r.URL.Path != "/api/v1/automations" || query.Get("limit") != "100" || query.Get("projectId") != "project & one" || query.Get("enabled") != enabled || query.Has("enabled") != (enabled != "") {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					}
					start := (requests - 1) * 100
					wantCursor := ""
					if start > 0 {
						wantCursor = fmt.Sprintf("opaque+/%d=", start)
					}
					if query.Get("cursor") != wantCursor || requests > 2 {
						t.Errorf("unexpected cursor: %q on request %d", query.Get("cursor"), requests)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					page := automationListDTO{Automations: []automationDTO{}}
					for i := start; i < min(start+100, 101); i++ {
						page.Automations = append(page.Automations, automationDTO{ID: fmt.Sprintf("automation-%03d", i), ProjectID: "project & one", DisplayName: "Morning"})
					}
					if start+100 < 101 {
						page.NextCursor = fmt.Sprintf("opaque+/%d=", start+100)
					}
					_ = json.NewEncoder(w).Encode(page)
				}))
				t.Cleanup(server.Close)
				writeRunFileFor(t, cfg, server)
				args := []string{"automation", "list", "--project", "project & one"}
				if enabled != "" {
					args = append(args, "--enabled="+enabled)
				}
				if jsonOutput {
					args = append(args, "--json")
				}
				out, stderr, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...)
				if err != nil || requests != 2 {
					t.Fatalf("requests=%d err=%v stderr=%s", requests, err, stderr)
				}
				var ids []string
				if jsonOutput {
					var envelope map[string]json.RawMessage
					if err := json.Unmarshal([]byte(out), &envelope); err != nil {
						t.Fatal(err)
					}
					if len(envelope) != 1 || envelope["automations"] == nil {
						t.Fatalf("unexpected JSON envelope: %s", out)
					}
					var items []automationDTO
					if err := json.Unmarshal(envelope["automations"], &items); err != nil {
						t.Fatal(err)
					}
					for _, item := range items {
						ids = append(ids, item.ID)
					}
				} else {
					for _, line := range strings.Split(strings.TrimSpace(out), "\n")[1:] {
						ids = append(ids, strings.Fields(line)[0])
					}
				}
				if len(ids) != 101 {
					t.Fatalf("got %d automations, want 101", len(ids))
				}
				for i, id := range ids {
					if want := fmt.Sprintf("automation-%03d", i); id != want {
						t.Fatalf("item %d = %q, want %q", i, id, want)
					}
				}
			})
		}
	}
}

func TestAutomationListPaginationFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		pages  []string
		status int
		want   string
	}{
		{"first page error", []string{`{"message":"unavailable","code":"UNAVAILABLE","requestId":"req-list"}`}, http.StatusServiceUnavailable, "req-list"},
		{"later page error", []string{`{"automations":[{"id":"first"}],"nextCursor":"next"}`, `{"message":"unavailable","code":"UNAVAILABLE","requestId":"req-list"}`}, http.StatusServiceUnavailable, "req-list"},
		{"malformed later page", []string{`{"automations":[{"id":"first"}],"nextCursor":"next"}`, `{`}, http.StatusOK, "decode"},
		{"repeated cursor", []string{`{"automations":[],"nextCursor":"next"}`, `{"automations":[],"nextCursor":"next"}`}, http.StatusOK, "repeated cursor"},
		{"cursor cycle", []string{`{"automations":[],"nextCursor":"a"}`, `{"automations":[],"nextCursor":"b"}`, `{"automations":[],"nextCursor":"a"}`}, http.StatusOK, "repeated cursor"},
	} {
		for _, jsonOutput := range []bool{false, true} {
			t.Run(tc.name+"/json="+strconv.FormatBool(jsonOutput), func(t *testing.T) {
				cfg := setConfigEnv(t)
				requests := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/internal/telemetry/cli-invoked" {
						w.WriteHeader(http.StatusNoContent)
						return
					}
					requests++
					if requests > len(tc.pages) {
						t.Error("unexpected extra request")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if requests == len(tc.pages) {
						w.WriteHeader(tc.status)
					}
					_, _ = io.WriteString(w, tc.pages[requests-1])
				}))
				t.Cleanup(server.Close)
				writeRunFileFor(t, cfg, server)
				out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "automation", "list", "--json="+strconv.FormatBool(jsonOutput))
				if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), tc.want) || out != "" || requests != len(tc.pages) {
					t.Fatalf("out=%q err=%v requests=%d", out, err, requests)
				}
			})
		}
	}
}

func TestAutomationListEmptyJSON(t *testing.T) {
	cfg := setConfigEnv(t)
	server, _ := automationServer(t, http.StatusOK, `{"automations":[]}`)
	writeRunFileFor(t, cfg, server)
	out, stderr, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "automation", "list", "--json")
	if err != nil || strings.Join(strings.Fields(out), "") != `{"automations":[]}` {
		t.Fatalf("out=%s err=%v stderr=%s", out, err, stderr)
	}
}

type automationCapture struct {
	method, path string
	body         []byte
}

func automationServer(t *testing.T, status int, response string) (*httptest.Server, *automationCapture) {
	t.Helper()
	capture := &automationCapture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.method, capture.path = r.Method, r.URL.Path
		capture.body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(server.Close)
	return server, capture
}

// The CLI must remain a thin HTTP client and preserve camel-case wire fields.
func TestAutomationCreatePostsDaemonContract(t *testing.T) {
	cfg := setConfigEnv(t)
	server, capture := automationServer(t, http.StatusCreated, `{"automation":{"id":"automation-1","displayName":"Morning","nextRunAt":"2026-08-26T03:30:00Z"}}`)
	writeRunFileFor(t, cfg, server)
	out, stderr, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }, Now: func() time.Time { return time.Now() }}, "automation", "create", "--project", "demo", "--name", "Morning", "--prompt", "Review", "--cron", "0 9 * * *", "--timezone", "Asia/Calcutta")
	if err != nil {
		t.Fatalf("create: %v stderr=%s", err, stderr)
	}
	if capture.method != http.MethodPost || capture.path != "/api/v1/automations" || !strings.Contains(out, "automation-1") {
		t.Fatalf("request=%s %s output=%s", capture.method, capture.path, out)
	}
	var body map[string]any
	if err := json.Unmarshal(capture.body, &body); err != nil {
		t.Fatal(err)
	}
	if body["projectId"] != "demo" || body["displayName"] != "Morning" || body["cron"] != "0 9 * * *" {
		t.Fatalf("body=%s", capture.body)
	}
}

func TestAutomationDeleteRequiresMatchingConfirmation(t *testing.T) {
	setConfigEnv(t)
	out, _, err := executeCLI(t, Deps{In: strings.NewReader("wrong\n")}, "automation", "delete", "automation-1")
	if err != nil || !strings.Contains(out, "aborted") {
		t.Fatalf("out=%s err=%v", out, err)
	}
}

func TestAutomationDeleteSendsDeleteAfterMatchingConfirmation(t *testing.T) {
	cfg := setConfigEnv(t)
	server, capture := automationServer(t, http.StatusOK, `{}`)
	writeRunFileFor(t, cfg, server)

	out, stderr, err := executeCLI(t, Deps{In: strings.NewReader("automation-1\n"), ProcessAlive: func(int) bool { return true }}, "automation", "delete", "automation-1")
	if err != nil {
		t.Fatalf("delete: %v stderr=%s", err, stderr)
	}
	if capture.method != http.MethodDelete || capture.path != "/api/v1/automations/automation-1" {
		t.Fatalf("request=%s %s", capture.method, capture.path)
	}
	if !strings.Contains(out, "deleted automation automation-1") {
		t.Fatalf("out=%s", out)
	}
}

func TestAutomationGetRequiresIDAsUsageError(t *testing.T) {
	setConfigEnv(t)
	_, _, err := executeCLI(t, Deps{}, "automation", "get")
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("err=%v exit=%d", err, ExitCode(err))
	}
}
