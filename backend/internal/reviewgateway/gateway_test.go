package reviewgateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testManifest(t *testing.T) Manifest {
	t.Helper()
	root := t.TempDir()
	promptRoot := filepath.Join(root, "prompts")
	if err := os.Mkdir(promptRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	return Manifest{
		ReviewerID:      "reviewer-1",
		WorkerSessionID: "worker-1",
		WorkspacePath:   filepath.Join(root, "source"),
		TaskPromptRoot:  promptRoot,
		Tasks: []Task{{
			RunID: "run-1", PRURL: "https://github.com/acme/widgets/pull/42",
			TargetSHA:      "0123456789abcdef",
			TaskPromptFile: filepath.Join(promptRoot, "run-1.md"),
		}},
	}
}

func TestPrepareEnvironmentUsesPrivateAOOwnedDirectoriesAndImmutableManifest(t *testing.T) {
	dataDir := privatePathTestRoot(t)
	manifest := testManifest(t)
	env, err := PrepareEnvironment(dataDir, manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Dir(env.Root), env.Root, env.WorkingDirectory, env.ConfigRoot, env.StateRoot, env.CacheRoot, env.TempRoot} {
		rel, err := filepath.Rel(dataDir, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			t.Fatalf("path %q is outside data dir %q", path, dataDir)
		}
		assertPrivatePath(t, path, true)
	}
	if got := env.TUIEnvironment(); got["HOME"] != env.ConfigRoot || got["TMPDIR"] != env.TempRoot || got["XDG_CACHE_HOME"] != env.CacheRoot {
		t.Fatalf("TUI environment = %#v", got)
	}
	assertPrivatePath(t, env.ManifestPath, false)

	again, err := PrepareEnvironment(dataDir, manifest)
	if err != nil || again.ManifestPath != env.ManifestPath {
		t.Fatalf("idempotent prepare = %+v, %v", again, err)
	}
	manifest.Tasks[0].TargetSHA = "1111111111111111"
	changed, err := PrepareEnvironment(dataDir, manifest)
	if err != nil || changed.ManifestPath == env.ManifestPath {
		t.Fatalf("changed authority must create a new immutable manifest: %+v, %v", changed, err)
	}
}

func TestPrepareHostTrustedEnvironmentUsesPrivateAOOwnedDirectories(t *testing.T) {
	dataDir := privatePathTestRoot(t)
	env, err := PrepareHostTrustedEnvironment(dataDir, "review-worker-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Dir(env.Root), env.Root, env.WorkingDirectory, env.ConfigRoot, env.StateRoot, env.CacheRoot, env.TempRoot} {
		rel, relErr := filepath.Rel(dataDir, path)
		_, statErr := os.Stat(path)
		if relErr != nil || strings.HasPrefix(rel, "..") || statErr != nil {
			t.Fatalf("private AO-owned directory %q: rel=%q relErr=%v statErr=%v", path, rel, relErr, statErr)
		}
		assertPrivatePath(t, path, true)
	}
	got := env.TUIEnvironment()
	if got["HOME"] != env.ConfigRoot || got["XDG_STATE_HOME"] != env.StateRoot || got["TMPDIR"] != env.TempRoot {
		t.Fatalf("TUI environment = %#v", got)
	}
	if _, err := PrepareHostTrustedEnvironment("relative", "review-worker-1"); err == nil {
		t.Fatal("relative AO data directory accepted")
	}
	if _, err := PrepareHostTrustedEnvironment(dataDir, "../escape"); err == nil {
		t.Fatal("unsafe reviewer id accepted")
	}
}

func TestPrepareEnvironmentRejectsTraversalAndInvalidAuthorization(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"reviewer traversal", func(m *Manifest) { m.ReviewerID = "../escape" }},
		{"duplicate run", func(m *Manifest) { m.Tasks = append(m.Tasks, m.Tasks[0]) }},
		{"ref injection", func(m *Manifest) { m.Tasks[0].TargetSHA = "--help" }},
		{"non github PR", func(m *Manifest) { m.Tasks[0].PRURL = "https://evil.invalid/acme/widgets/pull/42" }},
		{"prompt escape", func(m *Manifest) { m.Tasks[0].TaskPromptFile = filepath.Join(m.TaskPromptRoot, "..", "escape") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := testManifest(t)
			tt.mutate(&manifest)
			if _, err := PrepareEnvironment(t.TempDir(), manifest); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestPrepareEnvironmentRequiresAbsoluteDataDir(t *testing.T) {
	if _, err := PrepareEnvironment("relative", testManifest(t)); err == nil {
		t.Fatal("relative AO data directory was accepted")
	}
}
