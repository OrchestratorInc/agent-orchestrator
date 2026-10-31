package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var remindNow = time.Date(2026, time.October, 31, 6, 0, 0, 0, time.UTC)

type remindRequest struct {
	Method, Path, Query string
	Body                reminderCreateDTO
}

// remindDaemon answers the reminder routes and records what the CLI sent.
func remindDaemon(t *testing.T, status int, body string) *[]remindRequest {
	t.Helper()
	cfg := setConfigEnv(t)
	var got []remindRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/reminders") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		req := remindRequest{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery}
		if r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&req.Body)
		}
		got = append(got, req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	return &got
}

const remindCreated = `{"reminder":{"id":"rem_1","sessionId":"worker-1","projectId":"p","message":"check CI","dueAt":"2026-10-31T06:30:00Z","createdBy":"agent","status":"pending"}}`

func remindDeps() Deps {
	return Deps{ProcessAlive: func(int) bool { return true }, Now: func() time.Time { return remindNow }}
}

func TestParseRemindAt(t *testing.T) {
	local := time.FixedZone("IST", 5*60*60+30*60)
	now := time.Date(2026, time.October, 31, 10, 0, 0, 0, local)
	tests := []struct {
		in   string
		want time.Time
	}{
		{"+30m", now.Add(30 * time.Minute)},
		{"+1h30m", now.Add(90 * time.Minute)},
		{"+2d", now.Add(48 * time.Hour)},
		{"2026-11-01T04:00:00Z", time.Date(2026, time.November, 1, 4, 0, 0, 0, time.UTC)},
		{"11:15", time.Date(2026, time.October, 31, 11, 15, 0, 0, local)},
		// A clock time already past today means tomorrow.
		{"09:00", time.Date(2026, time.November, 1, 9, 0, 0, 0, local)},
	}
	for _, tc := range tests {
		got, err := parseRemindAt(tc.in, now)
		if err != nil || !got.Equal(tc.want) {
			t.Errorf("parseRemindAt(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{"", "soon", "+", "+-5m", "+0m", "+xd", "25:00"} {
		if _, err := parseRemindAt(bad, now); err == nil {
			t.Errorf("parseRemindAt(%q) accepted", bad)
		}
	}
}

func TestRemindValidation(t *testing.T) {
	tests := []struct {
		name    string
		session string
		args    []string
	}{
		{"no session", "", []string{"remind", "--at", "+5m", "--message", "x"}},
		{"missing at", "worker-1", []string{"remind", "--message", "x"}},
		{"missing message", "worker-1", []string{"remind", "--at", "+5m"}},
		{"blank message", "worker-1", []string{"remind", "--at", "+5m", "--message", "  "}},
		{"bad at", "worker-1", []string{"remind", "--at", "later", "--message", "x"}},
		{"message too long", "worker-1", []string{"remind", "--at", "+5m", "--message", strings.Repeat("x", 4001)}},
		{"cancel without id", "worker-1", []string{"remind", "cancel"}},
		{"ls without session", "", []string{"remind", "ls"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AO_SESSION_ID", tc.session)
			_, _, err := executeCLI(t, remindDeps(), tc.args...)
			if err == nil || ExitCode(err) != 2 {
				t.Fatalf("err=%v exit=%d", err, ExitCode(err))
			}
		})
	}
}

func TestRemindSchedulesIntoTheCurrentSession(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "worker-1")
	got := remindDaemon(t, http.StatusCreated, remindCreated)
	stdout, _, err := executeCLI(t, remindDeps(), "remind", "--at", "+30m", "--message", "check CI", "--name", "CI")
	if err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 {
		t.Fatalf("requests = %+v", *got)
	}
	req := (*got)[0]
	want := reminderCreateDTO{SessionID: "worker-1", Message: "check CI", Name: "CI", DueAt: remindNow.Add(30 * time.Minute)}
	if req.Method != http.MethodPost || req.Path != "/api/v1/reminders" || req.Body.SessionID != want.SessionID ||
		req.Body.Message != want.Message || req.Body.Name != want.Name || !req.Body.DueAt.Equal(want.DueAt) {
		t.Fatalf("request = %+v, want %+v", req, want)
	}
	if !strings.Contains(stdout, "scheduled rem_1 for 2026-10-31T06:30:00Z") || !strings.Contains(stdout, "ao remind cancel rem_1") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestRemindListAndCancelAreScopedToTheSession(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "worker 1")
	got := remindDaemon(t, http.StatusOK, `{"reminders":[{"id":"rem_1","message":"check\nCI","name":"CI","dueAt":"2026-10-31T06:30:00Z"}],"reminder":{"id":"rem_1"}}`)
	stdout, _, err := executeCLI(t, remindDeps(), "remind", "ls")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "rem_1") || !strings.Contains(stdout, "check CI") {
		t.Fatalf("ls stdout = %q", stdout)
	}
	stdout, _, err = executeCLI(t, remindDeps(), "remind", "cancel", "rem_1")
	if err != nil || !strings.Contains(stdout, "cancelled rem_1") {
		t.Fatalf("cancel: err=%v stdout=%q", err, stdout)
	}
	if len(*got) != 2 || (*got)[0].Method != http.MethodGet || (*got)[0].Path != "/api/v1/reminders" || (*got)[0].Query != "sessionId=worker+1" ||
		(*got)[1].Method != http.MethodDelete || (*got)[1].Path != "/api/v1/reminders/rem_1" || (*got)[1].Query != "sessionId=worker+1" {
		t.Fatalf("requests = %+v", *got)
	}
}

func TestRemindPreservesAPIEnvelopeAsRuntimeError(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "worker-1")
	remindDaemon(t, http.StatusConflict, `{"message":"Session already has 10 pending reminders; cancel one first","code":"REMINDER_LIMIT","requestId":"req-9"}`)
	_, _, err := executeCLI(t, remindDeps(), "remind", "--at", "+5m", "--message", "x")
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "(REMINDER_LIMIT) [request req-9]") {
		t.Fatalf("err=%v", err)
	}
}

func TestMCPRemindSchedulesListsAndCancels(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "worker-1")
	got := remindDaemon(t, http.StatusCreated, remindCreated)
	replies := runMCP(t,
		mcpCall("remind", map[string]any{"message": "check CI", "delay_minutes": 30}),
		mcpCall("remind", map[string]any{"message": "check CI", "at": "2026-11-01T04:00:00Z"}),
		mcpCall("remind", map[string]any{"message": "check CI"}),
		mcpCall("remind_list", nil),
		mcpCall("remind_cancel", map[string]any{"id": "rem_1"}),
		mcpCall("remind_cancel", map[string]any{}),
	)
	if len(replies) != 6 {
		t.Fatalf("replies = %+v", replies)
	}
	if replies[0].Result.IsError || !strings.Contains(replies[0].Result.Content[0].Text, "Scheduled rem_1") {
		t.Fatalf("remind = %+v", replies[0].Result)
	}
	if !replies[2].Result.IsError || !strings.Contains(replies[2].Result.Content[0].Text, "exactly one of at or delay_minutes") {
		t.Fatalf("remind without a time = %+v", replies[2].Result)
	}
	if replies[4].Result.IsError || !strings.Contains(replies[4].Result.Content[0].Text, "Cancelled rem_1") {
		t.Fatalf("remind_cancel = %+v", replies[4].Result)
	}
	if !replies[5].Result.IsError {
		t.Fatalf("remind_cancel without id = %+v", replies[5].Result)
	}
	requests := *got
	if len(requests) != 4 || requests[0].Path != "/api/v1/reminders" || !requests[1].Body.DueAt.Equal(time.Date(2026, time.November, 1, 4, 0, 0, 0, time.UTC)) ||
		requests[2].Method != http.MethodGet || requests[3].Method != http.MethodDelete || requests[3].Path != "/api/v1/reminders/rem_1" {
		t.Fatalf("requests = %+v", requests)
	}
}
