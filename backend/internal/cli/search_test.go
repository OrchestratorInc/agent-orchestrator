package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSearchSendsQueryAndFilters(t *testing.T) {
	cfg := setConfigEnv(t)
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/internal/telemetry/cli-invoked":
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/search/sessions":
			query = r.URL.RawQuery
			_, _ = io.WriteString(w, `{"query":"flaky login","results":[{"sessionId":"proj-1","projectId":"proj","harness":"claude-code","kind":"user","messageId":"msg-1","seq":3,"snippet":"the flaky login test","rank":-0.4,"createdAt":"2026-08-09T12:00:00Z","sessionName":"Fix the login test","projectName":"proj","sessionState":"terminated","resumeCommand":"ao session resume-agent proj-1"}],"total":1,"limit":5,"offset":0,"sort":"relevance","coverage":{"state":"ready","indexedSources":2,"pendingSources":0,"unsupportedSources":0,"unavailableSources":0,"indexedDocs":9}}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)

	out, stderr, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"search", "flaky", "login",
		"--project", "proj",
		"--harness", "claude-code",
		"--machine", "mac",
		"--status", "terminated",
		"--path", "src/auth",
		"--since", "2026-08-01",
		"--until", "2026-09-01",
		"--sort", "recent",
		"--limit", "5",
	)
	if err != nil {
		t.Fatalf("search: %v stderr=%s", err, stderr)
	}
	values, parseErr := parseQuery(query)
	if parseErr != nil {
		t.Fatalf("parse query %q: %v", query, parseErr)
	}
	if values["q"] != "flaky login" {
		t.Fatalf("q = %q, want the joined words", values["q"])
	}
	if values["projectId"] != "proj" || values["harness"] != "claude-code" || values["machine"] != "mac" {
		t.Fatalf("filters = %+v", values)
	}
	if values["status"] != "killed" {
		t.Fatalf("status = %q, want the normalized durable value", values["status"])
	}
	if values["path"] != "src/auth" {
		t.Fatalf("path = %q", values["path"])
	}
	if values["from"] != "2026-08-01" || values["to"] != "2026-09-01" {
		t.Fatalf("date range = %q..%q", values["from"], values["to"])
	}
	if values["sort"] != "recent" || values["limit"] != "5" {
		t.Fatalf("sort/limit = %+v", values)
	}
	if !strings.Contains(out, "Fix the login test") || !strings.Contains(out, "flaky login test") {
		t.Fatalf("output = %q", out)
	}
	// The id is what `ao session resume-agent` and an in-app link take; a name
	// alone left a person reading the table nothing to act on.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if !strings.HasPrefix(lines[0], "ID ") || len(lines) < 2 || !strings.HasPrefix(lines[1], "proj-1 ") {
		t.Fatalf("table = %q, want the session id as the first column", out)
	}
}

func TestSearchJSONIsMachineReadable(t *testing.T) {
	cfg := setConfigEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/internal/telemetry/cli-invoked":
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/search/sessions":
			_, _ = io.WriteString(w, `{"query":"reaper","results":[{"sessionId":"proj-1","projectId":"proj","harness":"codex","kind":"command","messageId":"","seq":9,"snippet":"the reaper leaks","rank":-0.2,"createdAt":"2026-08-09T12:00:00Z","resumeCommand":"ao session resume-agent proj-1"}],"total":1,"limit":20,"offset":0,"sort":"relevance","coverage":{"state":"ready"}}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)

	out, stderr, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "search", "reaper", "--json")
	if err != nil {
		t.Fatalf("search --json: %v stderr=%s", err, stderr)
	}
	var response sessionSearchResponseDTO
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatalf("decode JSON output: %v (%q)", err, out)
	}
	if len(response.Results) != 1 || response.Results[0].Kind != "command" {
		t.Fatalf("results = %+v, want the command-output document", response.Results)
	}
	if response.Results[0].ResumeCommand != "ao session resume-agent proj-1" {
		t.Fatalf("resume command = %q", response.Results[0].ResumeCommand)
	}
	if response.Coverage.State != "ready" || response.Sort != "relevance" {
		t.Fatalf("response = %+v", response)
	}
}

func TestSearchReportsIndexStatus(t *testing.T) {
	cfg := setConfigEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/internal/telemetry/cli-invoked":
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/search/index-status":
			_, _ = io.WriteString(w, `{"coverage":{"state":"backfilling","indexedSources":4,"pendingSources":2,"unavailableSources":1,"unsupportedSources":3,"indexedDocs":900,"unindexedHarnesses":["aider","cursor"],"backfilledAt":"2026-08-09T12:00:00Z"}}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)

	out, stderr, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "search", "--index-status")
	if err != nil {
		t.Fatalf("search --index-status: %v stderr=%s", err, stderr)
	}
	for _, want := range []string{"backfilling", "900", "4 indexed", "2 pending", "aider, cursor"} {
		if !strings.Contains(out, want) {
			t.Fatalf("index status output %q missing %q", out, want)
		}
	}
}

func TestSearchIndexStatusJSON(t *testing.T) {
	cfg := setConfigEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/internal/telemetry/cli-invoked":
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/search/index-status":
			_, _ = io.WriteString(w, `{"coverage":{"state":"ready","indexedDocs":42}}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)

	out, stderr, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "search", "--index-status", "--json")
	if err != nil {
		t.Fatalf("search --index-status --json: %v stderr=%s", err, stderr)
	}
	var response sessionSearchIndexStatusDTO
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatalf("decode index status: %v (%q)", err, out)
	}
	if response.Coverage.IndexedDocs != 42 {
		t.Fatalf("coverage = %+v", response.Coverage)
	}
}

func TestSearchRejectsEmptyQuery(t *testing.T) {
	_, _, err := executeCLI(t, Deps{}, "search")
	if err == nil {
		t.Fatal("empty query accepted")
	}
	if ExitCode(err) != 2 {
		t.Fatalf("exit = %d, want 2 for a usage error", ExitCode(err))
	}
}

func TestSearchAcceptsFilterOnlyBrowse(t *testing.T) {
	cfg := setConfigEnv(t)
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/internal/telemetry/cli-invoked":
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/search/sessions":
			query = r.URL.RawQuery
			_, _ = io.WriteString(w, `{"query":"","results":[],"total":0,"limit":20,"offset":0,"sort":"recent","coverage":{"state":"ready"}}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)

	out, stderr, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "search", "--project", "proj")
	if err != nil {
		t.Fatalf("search --project: %v stderr=%s", err, stderr)
	}
	if !strings.Contains(query, "projectId=proj") {
		t.Fatalf("query = %q", query)
	}
	if !strings.Contains(out, "No sessions matched.") {
		t.Fatalf("output = %q", out)
	}
}

func TestSearchRejectsUnparseableDate(t *testing.T) {
	_, _, err := executeCLI(t, Deps{}, "search", "reaper", "--since", "last tuesday")
	if err == nil {
		t.Fatal("unparseable date accepted")
	}
	if ExitCode(err) != 2 {
		t.Fatalf("exit = %d, want 2 for a usage error", ExitCode(err))
	}
	if !strings.Contains(err.Error(), "--since") {
		t.Fatalf("err = %v, want it to name the flag", err)
	}
}

func TestSearchRejectsUnknownSortAndStatus(t *testing.T) {
	for _, args := range [][]string{
		{"search", "reaper", "--sort", "alphabetical"},
		{"search", "reaper", "--status", "merged"},
	} {
		_, _, err := executeCLI(t, Deps{}, args...)
		if err == nil {
			t.Fatalf("%v accepted", args)
		}
		if ExitCode(err) != 2 {
			t.Fatalf("%v exit = %d, want 2", args, ExitCode(err))
		}
	}
}

func TestSearchKeepsDaemonErrorEnvelope(t *testing.T) {
	cfg := setConfigEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/internal/telemetry/cli-invoked":
			w.WriteHeader(http.StatusAccepted)
		case "/api/v1/search/sessions":
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":"unavailable","code":"SESSION_SEARCH_UNAVAILABLE","message":"Session search is not available on this daemon","requestId":"request-9"}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "search", "reaper")
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("err=%v exit=%d", err, ExitCode(err))
	}
	if !strings.Contains(err.Error(), "SESSION_SEARCH_UNAVAILABLE") || !strings.Contains(err.Error(), "request-9") {
		t.Fatalf("err = %v, want the daemon envelope", err)
	}
}

func TestSearchReportsCoverageWhenIndexIsIncomplete(t *testing.T) {
	cfg := setConfigEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/internal/telemetry/cli-invoked":
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/search/sessions":
			_, _ = io.WriteString(w, `{"query":"reaper","results":[],"total":0,"limit":20,"offset":0,"sort":"relevance","coverage":{"state":"backfilling","pendingSources":3,"unsupportedSources":1,"unindexedHarnesses":["aider"]}}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)

	out, stderr, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "search", "reaper")
	if err != nil {
		t.Fatalf("search: %v stderr=%s", err, stderr)
	}
	if !strings.Contains(out, "No sessions matched") {
		t.Fatalf("output = %q", out)
	}
	if !strings.Contains(out, "backfilling") || !strings.Contains(out, "aider") {
		t.Fatalf("output does not explain the index gap: %q", out)
	}
}

// An index whose backfill failed is not "still backfilling": the note must say
// it could not be built, so an empty result is not read as "never happened".
func TestCoverageNoteExplainsAnIndexThatCouldNotBeBuilt(t *testing.T) {
	note := coverageNote(sessionSearchCoverageDTO{State: "unavailable"})
	if !strings.Contains(note, "could not be built") || strings.Contains(note, "backfilling") {
		t.Fatalf("unavailable note = %q", note)
	}
	if note := coverageNote(sessionSearchCoverageDTO{State: "ready"}); note != "" {
		t.Fatalf("ready note = %q, want none", note)
	}
}

// parseQuery splits a raw query string into a single-value map for assertions.
func parseQuery(raw string) (map[string]string, error) {
	parsed, err := url.ParseQuery(raw)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for key, list := range parsed {
		if len(list) > 0 {
			values[key] = list[0]
		}
	}
	return values, nil
}

// Each session is listed once and says how many of its messages matched;
// --per-message asks the daemon for every matching message instead.
func TestSearchGroupsBySessionUnlessPerMessage(t *testing.T) {
	cfg := setConfigEnv(t)
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/internal/telemetry/cli-invoked":
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/search/sessions":
			queries = append(queries, r.URL.RawQuery)
			_, _ = io.WriteString(w, `{"query":"flaky","results":[{"sessionId":"proj-1","projectId":"proj","harness":"claude-code","kind":"user","messageId":"msg-1","seq":3,"snippet":"the flaky login test","rank":-0.4,"createdAt":"2026-08-09T12:00:00Z","sessionName":"Fix the login test","sessionMatches":4}],"total":1,"limit":20,"offset":0,"sort":"relevance","group":"session","coverage":{"state":"ready","indexedSources":2,"pendingSources":0,"unsupportedSources":0,"unavailableSources":0,"indexedDocs":9}}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)

	out, stderr, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "search", "flaky")
	if err != nil {
		t.Fatalf("search: %v stderr=%s", err, stderr)
	}
	if !strings.Contains(out, "(4 matches)") {
		t.Fatalf("output does not say how many of the session's messages matched: %q", out)
	}
	if _, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "search", "flaky", "--per-message"); err != nil {
		t.Fatalf("search --per-message: %v", err)
	}
	if len(queries) != 2 {
		t.Fatalf("requests = %v", queries)
	}
	first, _ := parseQuery(queries[0])
	second, _ := parseQuery(queries[1])
	if first["group"] != "" || second["group"] != "message" {
		t.Fatalf("group = %q then %q, want the default then message", first["group"], second["group"])
	}
}
