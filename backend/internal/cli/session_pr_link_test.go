package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const linkPRTestURL = "https://github.com/aoagents/agent-orchestrator/pull/8020"

// linkPRServer answers the requests link-pr and unlink-pr make and records the
// link request body.
func linkPRServer(t *testing.T) (*httptest.Server, *sessionRequestLog, *linkPRRequest) {
	t.Helper()
	log := &sessionRequestLog{}
	var got linkPRRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.append(r)
		w.Header().Set("Content-Type", "application/json")
		link := `{"url":` + jsonQuote(linkPRTestURL) + `,"provider":"github","host":"github.com","repo":"aoagents/agent-orchestrator","number":8020,"relation":"%s","title":"Refactor sidebar update UI","state":"open","author":"contributor","linkedAt":"2026-11-12T10:00:00Z"}`
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/demo-1":
			_, _ = io.WriteString(w, `{"session":`+sessionJSON("demo-1", "demo", "worker", "working", false)+`}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","repo":"https://github.com/aoagents/agent-orchestrator","defaultBranch":"main"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/pr/link":
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","link":`+strings.Replace(link, "%s", got.Relation, 1)+`}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/pr/unlink":
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","link":`+strings.Replace(link, "%s", "reviewing", 1)+`}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, log, &got
}

func TestSessionLinkPR_PostsRelationAndSaysNoOwnershipTaken(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log, got := linkPRServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "link-pr", "demo-1", linkPRTestURL, "--as", "contributing")
	if err != nil {
		t.Fatalf("link-pr failed: %v stderr=%s", err, errOut)
	}
	if got.PR != linkPRTestURL || got.Relation != "contributing" {
		t.Fatalf("request = %#v", got)
	}
	if !strings.Contains(out, "session demo-1 is contributing to PR #8020 (no ownership taken)") {
		t.Fatalf("output = %s", out)
	}
	want := []string{
		"GET /api/v1/sessions/demo-1",
		"GET /api/v1/projects/demo",
		"POST /api/v1/sessions/demo-1/pr/link",
	}
	if requests := log.all(); !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests=%#v want %#v", requests, want)
	}
}

func TestSessionLinkPR_UsesCurrentSessionFromEnv(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "demo-1")
	cfg := setConfigEnv(t)
	srv, _, got := linkPRServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "link-pr", linkPRTestURL, "--as", "reviewing", "--json")
	if err != nil {
		t.Fatalf("link-pr failed: %v stderr=%s", err, errOut)
	}
	var res linkPRResponse
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.SessionID != "demo-1" || res.Link.Relation != "reviewing" || res.Link.Number != 8020 {
		t.Fatalf("json err=%v res=%#v out=%s", err, res, out)
	}
	if got.Relation != "reviewing" {
		t.Fatalf("request = %#v", got)
	}
}

func TestSessionLinkPR_RelationIsRequiredBeforeAnyRequest(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log, _ := linkPRServer(t)
	writeRunFileFor(t, cfg, srv)

	for _, args := range [][]string{
		{"session", "link-pr", "demo-1", linkPRTestURL},
		{"session", "link-pr", "demo-1", linkPRTestURL, "--as", "owner"},
	} {
		_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...)
		if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "--as must be reviewing or contributing") {
			t.Fatalf("%v: err=%v exit=%d, want usage error", args, err, ExitCode(err))
		}
	}
	if requests := log.all(); len(requests) != 0 {
		t.Fatalf("requests = %#v, want none", requests)
	}
}

func TestSessionUnlinkPR(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log, _ := linkPRServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "unlink-pr", "demo-1", "8020")
	if err != nil {
		t.Fatalf("unlink-pr failed: %v stderr=%s", err, errOut)
	}
	if !strings.Contains(out, "session demo-1 no longer shows PR #8020") {
		t.Fatalf("output = %s", out)
	}
	want := []string{"GET /api/v1/sessions/demo-1", "POST /api/v1/sessions/demo-1/pr/unlink"}
	if requests := log.all(); !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests=%#v want %#v", requests, want)
	}
}

func TestWriteSessionPRLinksLabelsRelationAndOwnership(t *testing.T) {
	var out bytes.Buffer
	if err := writeSessionPRLinks(&out, []sessionPRLinkDTO{
		{URL: linkPRTestURL, Number: 8020, Relation: "contributing", State: "open", Author: "contributor", Title: "Refactor sidebar"},
		{URL: "https://github.com/acme/app/pull/7", Number: 7, Relation: "reviewing"},
	}); err != nil {
		t.Fatal(err)
	}
	want := "related prs (not owned):\n" +
		"  Contributing to #8020 (open, by contributor) Refactor sidebar " + linkPRTestURL + "\n" +
		"  Reviewing #7 https://github.com/acme/app/pull/7\n"
	if out.String() != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out.String(), want)
	}
	out.Reset()
	if err := writeSessionPRLinks(&out, nil); err != nil || out.Len() != 0 {
		t.Fatalf("empty links wrote %q err=%v", out.String(), err)
	}
}
