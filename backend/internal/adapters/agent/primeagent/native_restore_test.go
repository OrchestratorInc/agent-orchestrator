package primeagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const primeSourceID = "f395a38e-7815-4849-ae63-74e445dfc830"
const primeForkID = "7e30cc6c-ab67-42bf-abfb-9f68ef5499b1"

func writePrimeNativeFixture(t *testing.T, root, id, cwd, parent string, rows ...string) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{"type": "session", "version": 3, "id": id, "cwd": cwd, "parentSession": parent})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, id+".jsonl")
	data := string(header) + "\n" + strings.Join(rows, "\n") + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func primeNativeFixtureRows() []string {
	return []string{
		`{"type":"message","id":"user-1","parentId":null,"message":{"role":"user","content":[{"type":"text","text":"synthetic question"}]}}`,
		`{"type":"git_state","id":"git-1","parentId":"user-1","git":{"branch":"source"}}`,
		`{"type":"message","id":"assistant-1","parentId":"git-1","message":{"role":"assistant","content":[{"type":"text","text":"synthetic reply"}]}}`,
	}
}

func primeForkFixtureRows() []string {
	rows := primeNativeFixtureRows()
	return []string{rows[0], strings.ReplaceAll(rows[2], `"parentId":"git-1"`, `"parentId":"user-1"`)}
}

func primeTestPlugin() *Plugin {
	return &Plugin{resolvedBinary: "/synthetic/prime-agent", extensionFlag: map[string]bool{"/synthetic/prime-agent": false}}
}

func primeTestRestoreConfig(t *testing.T, root, workspace, id string) ports.RestoreConfig {
	t.Helper()
	return ports.RestoreConfig{DataDir: t.TempDir(), Session: ports.SessionRef{WorkspacePath: workspace, Metadata: map[string]string{
		ports.MetadataKeyAgentSessionID:       id,
		ports.MetadataKeyImportSourceNativeID: primeSourceID,
		ports.MetadataKeyImportResumeNativeID: id,
		ports.MetadataKeyImportConfigDir:      root,
	}}}
}

func TestPrimeNativeForkAdoptsExactHistoryAndLaterResumesWithoutFork(t *testing.T) {
	ctx := context.Background()
	root, sourceCWD, workspace := t.TempDir(), t.TempDir(), t.TempDir()
	source := writePrimeNativeFixture(t, root, primeSourceID, sourceCWD, "", primeNativeFixtureRows()...)
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	plugin := primeTestPlugin()
	ref, err := plugin.PrepareImportedSession(ctx, ports.ImportedSessionRelocation{NativeSessionID: primeSourceID, ConfigDir: root, TranscriptPath: source, SourceCWD: sourceCWD, LaunchCWD: workspace})
	if err != nil || ref.NativeSessionID != primeSourceID || ref.TranscriptPath != source {
		t.Fatalf("preparation = %#v, %v", ref, err)
	}
	plan, err := plugin.PrepareNativeRestore(ctx, primeTestRestoreConfig(t, root, workspace, primeSourceID))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(plan.Argv(), "--fork") || !slices.Contains(plan.Argv(), source) || slices.Contains(plan.Argv(), "--extension") || plan.ResumeInput() != "" {
		t.Fatalf("unsafe initial native invocation: %v", plan.Argv())
	}
	if ready, err := plan.Ready(ctx, "", ""); err != nil || ready || plan.TargetID() != "" {
		t.Fatalf("zero forks = %v, %v, id=%q", ready, err, plan.TargetID())
	}
	fork := writePrimeNativeFixture(t, root, primeForkID, workspace, source, primeForkFixtureRows()...)
	if ready, err := plan.Ready(ctx, "", ""); err != nil || ready {
		t.Fatalf("unopened fork accepted = %v, %v", ready, err)
	}
	appendPrimeActive(t, fork)
	if ready, err := plan.Ready(ctx, "", ""); err != nil || !ready || plan.TargetID() != primeForkID {
		t.Fatalf("exact fork = %v, %v, id=%q", ready, err, plan.TargetID())
	}
	if restored, err := plan.Restored(ctx, "", ""); err != nil || !restored {
		t.Fatalf("restored = %v, %v", restored, err)
	}
	after, err := os.ReadFile(source)
	if err != nil || string(after) != string(before) {
		t.Fatalf("native source changed: %v", err)
	}
	later, err := plugin.PrepareNativeRestore(ctx, primeTestRestoreConfig(t, root, workspace, primeForkID))
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(later.Argv(), "--fork") || !slices.Contains(later.Argv(), "--resume") || !slices.Contains(later.Argv(), fork) || later.TargetID() != primeForkID {
		t.Fatalf("later reopen forked or lost identity: %v", later.Argv())
	}
	if ready, err := later.Ready(ctx, "", ""); err != nil || ready {
		t.Fatalf("prior active marker accepted for new launch = %v, %v", ready, err)
	}
	appendPrimeActive(t, fork)
	if ready, err := later.Ready(ctx, "", ""); err != nil || !ready {
		t.Fatalf("later exact resume = %v, %v", ready, err)
	}
}

func appendPrimeActive(t *testing.T, path string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("{\"type\":\"session_state\",\"id\":\"active\",\"state\":{\"status\":\"active\"}}\n")
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("append active: %v, close: %v", err, closeErr)
	}
}

func TestPrimeNativeForkRefusesWrongOrAmbiguousProof(t *testing.T) {
	for _, scenario := range []string{"wrong cwd", "wrong parent session", "changed message", "changed ancestry", "multiple forks"} {
		t.Run(scenario, func(t *testing.T) {
			root, workspace := t.TempDir(), t.TempDir()
			source := writePrimeNativeFixture(t, root, primeSourceID, t.TempDir(), "", primeNativeFixtureRows()...)
			plan, err := primeTestPlugin().PrepareNativeRestore(context.Background(), primeTestRestoreConfig(t, root, workspace, primeSourceID))
			if err != nil {
				t.Fatal(err)
			}
			rows, cwd, parent := primeForkFixtureRows(), workspace, source
			switch scenario {
			case "wrong cwd":
				cwd = t.TempDir()
			case "wrong parent session":
				parent = filepath.Join(root, "absent.jsonl")
			case "changed message":
				rows[1] = strings.ReplaceAll(rows[1], "synthetic reply", "different reply")
			case "changed ancestry":
				rows[1] = strings.ReplaceAll(rows[1], `"parentId":"user-1"`, `"parentId":null`)
			}
			fork := writePrimeNativeFixture(t, root, primeForkID, cwd, parent, rows...)
			appendPrimeActive(t, fork)
			if scenario == "multiple forks" {
				other := writePrimeNativeFixture(t, root, "51381da5-ea93-465a-9506-97868d30cc43", workspace, source, primeForkFixtureRows()...)
				appendPrimeActive(t, other)
			}
			ready, err := plan.Ready(context.Background(), "", "")
			if ready || plan.TargetID() != "" || (scenario == "multiple forks" && err == nil) {
				t.Fatalf("unsafe fork proof accepted: ready=%v id=%q err=%v", ready, plan.TargetID(), err)
			}
		})
	}
}

func TestPrimeImportedMissingHistoryCannotFallBack(t *testing.T) {
	cfg := primeTestRestoreConfig(t, t.TempDir(), t.TempDir(), primeForkID)
	_, err := primeTestPlugin().PrepareNativeRestore(context.Background(), cfg)
	if err == nil || errors.Is(err, ports.ErrNativeHistoryUnavailable) {
		t.Fatalf("imported missing history permitted fresh fallback: %v", err)
	}
	delete(cfg.Session.Metadata, ports.MetadataKeyImportSourceNativeID)
	_, err = primeTestPlugin().PrepareNativeRestore(context.Background(), cfg)
	if !errors.Is(err, ports.ErrNativeHistoryUnavailable) {
		t.Fatalf("ordinary missing native history = %v", err)
	}
}

func TestPrimeNativeSourcePathAndRootAreExact(t *testing.T) {
	ctx := context.Background()
	root, cwd := t.TempDir(), t.TempDir()
	source := writePrimeNativeFixture(t, root, primeSourceID, cwd, "", primeNativeFixtureRows()...)
	for _, ref := range []ports.ImportedSessionRelocation{
		{NativeSessionID: primeForkID, ConfigDir: root, TranscriptPath: source},
		{NativeSessionID: primeSourceID, ConfigDir: t.TempDir(), TranscriptPath: source},
		{NativeSessionID: primeSourceID, ConfigDir: root, TranscriptPath: filepath.Join(root, "different.jsonl")},
	} {
		if _, err := primeTestPlugin().PrepareImportedSession(ctx, ref); err == nil {
			t.Fatal("wrong native source identity/root/path accepted")
		}
	}
}
