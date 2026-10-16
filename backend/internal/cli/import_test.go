package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/legacyimport"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func writeLegacyProject(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), ".agent-orchestrator")
	if err := os.MkdirAll(filepath.Join(root, "projects", "alpha", "sessions"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"),
		[]byte("projects:\n  alpha:\n    path: /repos/alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestImportCommand_NoLegacyData(t *testing.T) {
	setConfigEnv(t)
	empty := filepath.Join(t.TempDir(), "nope")
	out, _, err := executeCLI(t, Deps{}, "import", "--from", empty, "--yes")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !strings.Contains(out, "Nothing to import") {
		t.Fatalf("out = %q, want 'Nothing to import'", out)
	}
}

func TestImportCommand_ImportsProjectJSON(t *testing.T) {
	setConfigEnv(t)
	root := writeLegacyProject(t)

	out, _, err := executeCLI(t, Deps{}, "import", "--from", root, "--yes", "--json")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	var rep legacyimport.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("parse report %q: %v", out, err)
	}
	if rep.ProjectsImported != 1 {
		t.Fatalf("projectsImported = %d, want 1", rep.ProjectsImported)
	}
}

// ao import opens the database itself, so it must treat a database a newer AO
// migrated like the daemon does: refuse it, unless AO_ALLOW_NEWER_SCHEMA is on.
func TestImportCommand_NewerSchemaDatabase(t *testing.T) {
	// setConfigEnv leaves this variable alone, and a developer may export it.
	// Blank counts as unset, so the refusal half below always starts refusing.
	t.Setenv("AO_ALLOW_NEWER_SCHEMA", "")
	cfg := setConfigEnv(t)
	sqlitetest.MustCreateNewerSchemaAt(t, cfg.dataDir)
	root := writeLegacyProject(t)

	_, _, err := executeCLI(t, Deps{}, "import", "--from", root, "--yes", "--json")
	if !errors.Is(err, sqlite.ErrSchemaNewer) || !strings.HasPrefix(err.Error(), "open store: ") {
		t.Fatalf("err = %v, want an open-store refusal wrapping sqlite.ErrSchemaNewer", err)
	}

	t.Setenv("AO_ALLOW_NEWER_SCHEMA", "1")
	out, _, err := executeCLI(t, Deps{}, "import", "--from", root, "--yes", "--json")
	if err != nil {
		t.Fatalf("import with AO_ALLOW_NEWER_SCHEMA=1: %v", err)
	}
	var rep legacyimport.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("parse report %q: %v", out, err)
	}
	if rep.ProjectsImported != 1 {
		t.Fatalf("projectsImported = %d, want 1", rep.ProjectsImported)
	}
}

func TestImportCommand_SurfacesParseErrorOnce(t *testing.T) {
	setConfigEnv(t)
	// A tab-indented line is a YAML syntax error (not a *yaml.TypeError), so
	// loadLegacyConfig surfaces it exactly like issue #2186 describes.
	root := filepath.Join(t.TempDir(), ".agent-orchestrator")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"),
		[]byte("projects:\n\talpha:\n  path: /repos/alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, stderr, err := executeCLI(t, Deps{}, "import", "--from", root, "--yes")
	if err == nil {
		t.Fatal("import: want non-nil error for a config.yaml that fails to parse")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("ExitCode = %d, want 1 (runtime failure)", got)
	}
	// The parse error must reach the user exactly once. main.go prints the
	// returned error, so the command itself must not also Fprintf it; that
	// doubled the output before this fix.
	count := strings.Count(stderr, "parse legacy config.yaml") + strings.Count(err.Error(), "parse legacy config.yaml")
	if count != 1 {
		t.Fatalf("parse error appeared %d time(s) (stderr=%q, err=%q), want exactly 1", count, stderr, err)
	}
}

func TestImportCommand_RefusesWhenDaemonRunning(t *testing.T) {
	cfg := setConfigEnv(t)
	root := writeLegacyProject(t)

	// A run-file owned by this (alive) process makes the daemon look live.
	if err := runfile.Write(cfg.runFile, runfile.Info{PID: os.Getpid(), Port: 3001, StartedAt: time.Now()}); err != nil {
		t.Fatalf("write run-file: %v", err)
	}

	_, _, err := executeCLI(t, Deps{}, "import", "--from", root, "--yes")
	if err == nil || !strings.Contains(err.Error(), "daemon is running") {
		t.Fatalf("err = %v, want refusal because daemon is running", err)
	}
}
