package worker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// siblingLayout makes a working folder (parent/web) whose additional
// repository billing already exists beside it as the Coder template's own
// clone: a link here, as a folder another user owns cannot be made without
// root (foreignRepoFolder covers that with another uid).
func siblingLayout(t *testing.T) (workspace, templateClone string) {
	t.Helper()
	parent := t.TempDir()
	workspace = filepath.Join(parent, "web")
	templateClone = t.TempDir()
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(templateClone, filepath.Join(parent, "billing")); err != nil {
		t.Fatal(err)
	}
	return workspace, templateClone
}

// A folder is the worker's own only when it is a real folder the worker's
// user owns; a missing folder is not foreign (AO creates it).
func TestForeignRepoFolder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	own := filepath.Join(dir, "own")
	if err := os.Mkdir(own, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(own, link); err != nil {
		t.Fatal(err)
	}
	uid := os.Getuid()
	for name, tc := range map[string]struct {
		path    string
		uid     int
		foreign bool
	}{
		"own folder":          {own, uid, false},
		"another user's":      {own, uid + 1, true},
		"a link":              {link, uid, true},
		"a file":              {file, uid, true},
		"missing":             {filepath.Join(dir, "missing"), uid, false},
		"missing, other user": {filepath.Join(dir, "missing"), uid + 1, false},
	} {
		if got := foreignRepoFolder(tc.path, tc.uid); got != tc.foreign {
			t.Errorf("%s: foreign = %v, want %v", name, got, tc.foreign)
		}
	}
}

// A session whose checkouts must be AO's own records which additional
// repositories' folders are refused; any other session clears the mark.
func TestRecordOwnedRepoFolders(t *testing.T) {
	t.Parallel()
	workspace, _ := siblingLayout(t)
	dataDir := t.TempDir()
	checkouts := SessionRepoCheckouts(workspace, "https://github.com/acme/web",
		[]RepoRef{{URL: "https://github.com/acme/billing"}, {URL: "https://github.com/acme/docs"}})[1:]
	refused, err := RecordOwnedRepoFolders(dataDir, true, checkouts)
	if err != nil {
		t.Fatal(err)
	}
	if len(refused) != 1 || refused[0].URL != "https://github.com/acme/billing" || !OwnedRepoFoldersOnly(dataDir) {
		t.Fatalf("refused = %+v, marked = %v", refused, OwnedRepoFoldersOnly(dataDir))
	}
	if listed, _ := os.ReadFile(filepath.Join(dataDir, ownedRepoFoldersFile)); string(listed) != refused[0].Path+"\n" {
		t.Fatalf("record = %q", listed)
	}
	if refused, err := RecordOwnedRepoFolders(dataDir, false, checkouts); err != nil || refused != nil || OwnedRepoFoldersOnly(dataDir) {
		t.Fatalf("cleared: refused = %+v, err = %v, marked = %v", refused, err, OwnedRepoFoldersOnly(dataDir))
	}
}

// In a session whose checkouts must be AO's own, a Setup step of an
// additional repository whose folder the template made never runs there: it
// is reported as skipped with why, and the steps after it still run. Any
// other session (the workspace user, a reused checkout) runs it as before.
func TestSetupSkipsTheStepsOfARepositoryFolderThatIsNotAOs(t *testing.T) {
	t.Parallel()
	for _, owned := range []bool{true, false} {
		workspace, templateClone := siblingLayout(t)
		dataDir := t.TempDir()
		if _, err := RecordOwnedRepoFolders(dataDir, owned, nil); err != nil {
			t.Fatal(err)
		}
		recorder := &stepRecorder{}
		runner := SetupRunner{DataDir: dataDir, FirstStart: true, Publish: recorder.publish}
		commands := []string{domain.RepoScriptCommand("billing", "touch template-stack-down"), "touch main-ran"}
		summary, ran := runner.Run(t.Context(), commands, workspace, scriptEnvironment(workspace))
		if !ran || summary.Status != SetupSucceeded {
			t.Fatalf("owned %v: summary = %+v, ran = %v", owned, summary, ran)
		}
		_, statErr := os.Stat(filepath.Join(templateClone, "template-stack-down"))
		if owned == (statErr == nil) {
			t.Fatalf("owned %v: the step ran in the template's folder = %v", owned, statErr == nil)
		}
		if _, err := os.Stat(filepath.Join(workspace, "main-ran")); err != nil {
			t.Fatalf("owned %v: the step after it did not run", owned)
		}
		step := finals(recorder.all())[1]
		if !owned {
			if step.Status != SetupSucceeded {
				t.Fatalf("step 1 = %+v, want it run", step)
			}
			continue
		}
		if step.Status != ScriptStepSkipped || step.Repository != "billing" || step.Command != "touch template-stack-down" ||
			!strings.Contains(step.Hint, "billing already exists (created by your Coder template), so AO did not run this step in it.") ||
			step.Total != 2 || !ValidScriptStepEvent(step) {
			t.Fatalf("skipped step = %+v", step)
		}
	}
}

// The Cleanup hook of a session whose checkouts must be AO's own skips, at
// run time, a step of an additional repository whose folder is not the hook
// user's own, whether the worker installed the commands or the control plane
// passed the project's current ones in; the control plane reads that step as
// skipped with why. Without the mark the step runs as before.
func TestCleanupSkipsTheStepsOfARepositoryFolderThatIsNotAOs(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is unavailable")
	}
	for _, tc := range []struct{ owned, current bool }{{true, false}, {true, true}, {false, false}} {
		owned := tc.owned
		workspace, templateClone := siblingLayout(t)
		dataDir := t.TempDir()
		if _, err := RecordOwnedRepoFolders(dataDir, owned, nil); err != nil {
			t.Fatal(err)
		}
		commands := []string{"touch main-first", domain.RepoScriptCommand("billing", "touch volumes-removed"),
			"echo owned-only=$" + ownedRepoFoldersEnv}
		installed := commands
		if tc.current {
			installed = []string{"echo stale"}
		}
		if err := WriteCleanupHook(dataDir, installed, workspace, scriptEnvironment(workspace)); err != nil {
			t.Fatal(err)
		}
		if tc.current {
			// What the control plane installs for the project's current Cleanup.
			script := CleanupCommandsScript(commands, NewScriptMarkerNonce())
			if err := os.WriteFile(filepath.Join(dataDir, CleanupCommandsFile), []byte(script), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		// Exercise ownership decisions through the hook's declared interpreter.
		// Direct exec of a freshly written fixture can fail with ETXTBSY on Linux;
		// executable-hook dispatch is covered separately in project_scripts_test.go.
		output, err := exec.Command("sh", filepath.Join(dataDir, CleanupHookFile)).CombinedOutput()
		if err != nil {
			t.Fatalf("owned %v: hook: %v (%s)", owned, err, output)
		}
		_, statErr := os.Stat(filepath.Join(templateClone, "volumes-removed"))
		if owned == (statErr == nil) {
			t.Fatalf("owned %v: the step ran in the template's folder = %v; output %s", owned, statErr == nil, output)
		}
		events := CleanupStepEvents(string(output), CleanupRun{RunID: "r", StartedAt: time.Now()})
		if len(events) != 3 || events[0].Status != SetupSucceeded || events[2].Status != SetupSucceeded ||
			events[2].Output != "owned-only=" {
			t.Fatalf("owned %v: events = %+v", owned, events)
		}
		skipped := events[1]
		if !owned {
			if skipped.Status != SetupSucceeded {
				t.Fatalf("step 2 = %+v, want it run", skipped)
			}
			continue
		}
		if skipped.Status != ScriptStepSkipped || skipped.Repository != "billing" || skipped.Command != "touch volumes-removed" ||
			skipped.Hint != ForeignRepoFolderHint(filepath.Join(filepath.Dir(workspace), "billing")) || skipped.LogPath != "" || skipped.ExitCode != nil {
			t.Fatalf("skipped step = %+v", skipped)
		}
	}
}

// A skipped step's hint names the refused folder the same way in Setup and
// in Cleanup: below the workspace user's home with "~/", else absolute. A
// commands script from before folder markers names it beside the main
// checkout.
func TestSetupAndCleanupNameARefusedFolderAlike(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is unavailable")
	}
	workspace, _ := siblingLayout(t)
	home := filepath.Dir(filepath.Dir(workspace))
	t.Setenv(UserHomeEnv, home+"/")
	commands := []string{domain.RepoScriptCommand("billing", "touch volumes-removed")}

	setupData := t.TempDir()
	if _, err := RecordOwnedRepoFolders(setupData, true, nil); err != nil {
		t.Fatal(err)
	}
	recorder := &stepRecorder{}
	runner := SetupRunner{DataDir: setupData, FirstStart: true, Publish: recorder.publish}
	if _, ran := runner.Run(t.Context(), commands, workspace, scriptEnvironment(workspace)); !ran {
		t.Fatal("setup did not run")
	}
	setupHint := finals(recorder.all())[1].Hint
	want := ForeignRepoFolderHint("~/" + filepath.Base(filepath.Dir(workspace)) + "/billing")
	if setupHint != want {
		t.Fatalf("setup hint = %q, want %q", setupHint, want)
	}

	cleanupData := t.TempDir()
	if _, err := RecordOwnedRepoFolders(cleanupData, true, nil); err != nil {
		t.Fatal(err)
	}
	if err := WriteCleanupHook(cleanupData, commands, workspace, scriptEnvironment(workspace)); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("sh", filepath.Join(cleanupData, CleanupHookFile)).CombinedOutput()
	if err != nil {
		t.Fatalf("hook: %v (%s)", err, output)
	}
	events := CleanupStepEvents(string(output), CleanupRun{RunID: "r", StartedAt: time.Now()})
	if len(events) != 1 || events[0].Status != ScriptStepSkipped || events[0].Hint != setupHint {
		t.Fatalf("cleanup events = %+v, want the hint %q", events, setupHint)
	}
	if strings.Contains(string(output), cleanupUserHomeEnv) {
		t.Fatalf("the hook printed its internal variable: %s", output)
	}

	// An older commands script prints no folder marker.
	nonce := NewScriptMarkerNonce()
	older := "@@ao-step-" + nonce + " 1 1 skip " + scriptSkipForeignFolder + " billing touch volumes-removed\n"
	if events := CleanupStepEvents(older, CleanupRun{RunID: "r"}); len(events) != 1 || events[0].Hint != ForeignRepoFolderHint("../billing") {
		t.Fatalf("older script events = %+v", events)
	}
}

// The checkouts AO uses leave out, in a session whose checkouts must be AO's
// own, an additional repository whose folder is the template's own clone; a
// missing folder (AO checks the repository out there) is kept. Any other
// session, and a caller without a data directory, uses every checkout.
func TestUsedSessionRepoCheckouts(t *testing.T) {
	t.Parallel()
	extras := []RepoRef{{URL: "https://github.com/acme/billing"}, {URL: "https://github.com/acme/docs"}}
	names := func(checkouts []RepoCheckout) string {
		var listed []string
		for _, checkout := range checkouts {
			listed = append(listed, checkout.Name)
		}
		return strings.Join(listed, ",")
	}
	for _, tc := range []struct {
		name  string
		owned bool
		data  bool
		want  string
	}{
		{"owned repo folders", true, true, "web,docs"},
		{"any other session", false, true, "web,billing,docs"},
		{"no data directory", true, false, "web,billing,docs"},
	} {
		workspace, _ := siblingLayout(t)
		dataDir := t.TempDir()
		if _, err := RecordOwnedRepoFolders(dataDir, tc.owned, nil); err != nil {
			t.Fatal(err)
		}
		if !tc.data {
			dataDir = ""
		}
		if got := names(UsedSessionRepoCheckouts(workspace, dataDir, "https://github.com/acme/web", extras)); got != tc.want {
			t.Errorf("%s: checkouts = %s, want %s", tc.name, got, tc.want)
		}
	}
}
