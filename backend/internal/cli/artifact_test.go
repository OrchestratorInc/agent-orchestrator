package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workingDir := t.TempDir()
	t.Chdir(workingDir)
	stdout, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "artifact", "add", "brand.html", "~/notes/plan.md")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/sessions/worker-1/artifact-registrations" {
		t.Fatalf("path = %q", gotPath)
	}
	if !reflect.DeepEqual(got.Paths, []string{"brand.html", filepath.Join(home, "notes", "plan.md")}) {
		t.Fatalf("paths = %v", got.Paths)
	}
	// The daemon decides whether this folder counts; the CLI only reports it.
	if resolved, _ := filepath.EvalSymlinks(workingDir); got.WorkingDir != workingDir && got.WorkingDir != resolved {
		t.Fatalf("workingDir = %q, want %q", got.WorkingDir, workingDir)
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

func TestExpandHome(t *testing.T) {
	home := filepath.Join("/", "home", "jane")
	for in, want := range map[string]string{
		"~":                  home,
		"~/report.html":      filepath.Join(home, "report.html"),
		`~\report.html`:      filepath.Join(home, "report.html"),
		"report.html":        "report.html",
		"~jane/report.html":  "~jane/report.html",
		"/abs/~/report.html": "/abs/~/report.html",
	} {
		if got := expandHome(in, home); got != want {
			t.Errorf("expandHome(%q) = %q, want %q", in, got, want)
		}
	}
	if got := expandHome("~/report.html", ""); got != "~/report.html" {
		t.Errorf("without a home = %q", got)
	}
}
