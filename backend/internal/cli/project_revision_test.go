package cli

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestProjectGetRevisionPresence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		field   string
		present bool
	}{
		{name: "old daemon omission"},
		{name: "new daemon authoritative zero", field: `,"revision":0`, present: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			srv, capture := projectServer(t, http.StatusOK, `{"status":"ok","project":{"id":"demo","path":"/repo/demo"`+tc.field+`}}`)
			writeRunFileFor(t, cfg, srv)
			out, stderr, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "project", "get", "demo", "--json")
			if err != nil {
				t.Fatalf("get: %v stderr=%s", err, stderr)
			}
			if capture.method != http.MethodGet || capture.path != "/api/v1/projects/demo" {
				t.Fatalf("unexpected dispatch: %s %s", capture.method, capture.path)
			}
			var response struct {
				Project map[string]json.RawMessage `json:"project"`
			}
			if err := json.Unmarshal([]byte(out), &response); err != nil {
				t.Fatal(err)
			}
			revision, present := response.Project["revision"]
			if present != tc.present || (present && string(revision) != "0") {
				t.Fatalf("revision presence=%v value=%s, want presence=%v zero when present; output=%s", present, revision, tc.present, out)
			}
		})
	}
}

func TestProjectSetConfigLegacyBodyAndRevisionResponse(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"project":{"id":"demo","path":"/repo/demo","revision":7}}`)
	writeRunFileFor(t, cfg, srv)
	out, stderr, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "project", "set-config", "demo", "--clear", "--json")
	if err != nil {
		t.Fatalf("legacy config PUT: %v stderr=%s", err, stderr)
	}
	var request map[string]json.RawMessage
	if err := json.Unmarshal(capture.body, &request); err != nil || len(request) != 1 || request["config"] == nil {
		t.Fatalf("legacy request shape changed: %s err=%v", capture.body, err)
	}
	var response struct {
		Project struct {
			Revision *int64 `json:"revision"`
		} `json:"project"`
	}
	if err := json.Unmarshal([]byte(out), &response); err != nil || response.Project.Revision == nil || *response.Project.Revision != 7 {
		t.Fatalf("committed revision was dropped: %s err=%v", out, err)
	}
}
