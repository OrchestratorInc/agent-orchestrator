package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestArtifactAddPostsPathsAndSummarises(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "worker-1")
	cfg := setConfigEnv(t)
	var gotPath string
	var got artifactRegisterRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(artifactRegisterResponse{
			Registered: []string{"brand.html"}, OtherFiles: 42, OtherRootFiles: []string{"a.md", "b.md", "c.md", "d.md", "e.md", "f.md"},
		})
	}))
	defer srv.Close()

	writeRunFileFor(t, cfg, srv)
	stdout, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "artifact", "add", "brand.html")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/sessions/worker-1/artifact-registrations" {
		t.Fatalf("path = %q", gotPath)
	}
	if !reflect.DeepEqual(got.Paths, []string{"brand.html"}) {
		t.Fatalf("paths = %v", got.Paths)
	}
	for _, want := range []string{
		"Registered brand.html.",
		"42 other files stay in the folder but won't be listed as artifacts.",
		"a.md, b.md, c.md, d.md, e.md and 1 more",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout %q lacks %q", stdout, want)
		}
	}
}

func TestArtifactAddValidation(t *testing.T) {
	t.Run("no session", func(t *testing.T) {
		t.Setenv("AO_SESSION_ID", "")
		if _, _, err := executeCLI(t, Deps{}, "artifact", "add", "a.md"); err == nil || ExitCode(err) != 2 {
			t.Fatalf("err=%v exit=%d", err, ExitCode(err))
		}
	})
	t.Run("no paths", func(t *testing.T) {
		t.Setenv("AO_SESSION_ID", "worker-1")
		if _, _, err := executeCLI(t, Deps{}, "artifact", "add"); err == nil {
			t.Fatal("want an error")
		}
	})
}

func TestArtifactAddSummaryWithNothingElse(t *testing.T) {
	got := artifactAddSummary(artifactRegisterResponse{Registered: []string{"a.md", "b.md"}})
	if got != "Registered a.md, b.md.\n" {
		t.Fatalf("summary = %q", got)
	}
	got = artifactAddSummary(artifactRegisterResponse{Registered: []string{"a.md"}, OtherFiles: 1})
	if !strings.Contains(got, "1 other file stays") {
		t.Fatalf("summary = %q", got)
	}
}
