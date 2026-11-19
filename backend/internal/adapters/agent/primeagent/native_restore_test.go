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

func TestPrimeNativePreparationAdoptsBeforeLaunchAndLaterResumesExactID(t *testing.T) {
	ctx := context.Background()
	root, sourceCWD, workspace := t.TempDir(), t.TempDir(), t.TempDir()
	source := writePrimeNativeFixture(t, root, primeSourceID, sourceCWD, "", primeNativeFixtureRows()...)
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	targetRoot, err := sessionDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	plugin := primeTestPlugin()
	calls := 0
	plugin.runCommand = func(_ context.Context, binary, cwd string, env map[string]string, args ...string) ([]byte, error) {
		calls++
		want := []string{"--mode", "rpc", "--offline", "--session-dir", targetRoot, "--cwd", workspace, "--fork", source}
		if binary != "/synthetic/prime-agent" || cwd != workspace || !slices.Equal(args, want) || env["PRIME_AGENT_SESSION_DIR"] != targetRoot || env["SYNTHETIC_CLEAR"] != "" {
			t.Fatalf("native preparation invocation = %q %q %v", binary, cwd, args)
		}
		env["SYNTHETIC_SECRET"] = "changed-by-runner"
		writePrimeNativeFixture(t, targetRoot, primeForkID, workspace, source, primeForkFixtureRows()...)
		return nil, nil
	}
	env := map[string]string{"SYNTHETIC_SECRET": "original", "SYNTHETIC_CLEAR": ""}
	request := ports.ImportedSessionRelocation{NativeSessionID: primeSourceID, ConfigDir: root, TranscriptPath: source, SourceCWD: sourceCWD, LaunchCWD: workspace, DataDir: dataDir, Env: env}
	ref, err := plugin.PrepareImportedSession(ctx, request)
	if err != nil || ref.NativeSessionID != primeForkID || ref.ConfigDir != targetRoot || env["SYNTHETIC_SECRET"] != "original" {
		t.Fatalf("preparation = %#v, %v", ref, err)
	}
	retry, err := plugin.PrepareImportedSession(ctx, request)
	if err != nil || retry != ref || calls != 1 {
		t.Fatalf("checkpoint retry forked again: %#v, %v, calls=%d", retry, err, calls)
	}
	cfg := primeTestRestoreConfig(t, ref.ConfigDir, workspace, ref.NativeSessionID)
	cfg.DataDir = dataDir
	argv, restored, err := plugin.GetRestoreCommand(ctx, cfg)
	if err != nil || !restored || !slices.Contains(argv, "--resume") || !slices.Contains(argv, ref.TranscriptPath) || !slices.Contains(argv, workspace) || slices.Contains(argv, "--fork") || slices.Contains(argv, "--extension") {
		t.Fatalf("prepared native restore = %v, %v, %v", argv, restored, err)
	}
	if _, interactive := any(plugin).(ports.AgentInteractiveNativeRestorer); interactive {
		t.Fatal("taskless Prime restore must not enter the terminal-input initializer")
	}
	after, err := os.ReadFile(source)
	if err != nil || string(after) != string(before) {
		t.Fatalf("source changed: %v", err)
	}
	file, err := os.OpenFile(ref.TranscriptPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteString("\n" + `{"type":"message","id":"ao-turn","parentId":"assistant-1","message":{"role":"user","content":"later AO turn"}}` + "\n")
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("append managed turn: %v, %v", writeErr, closeErr)
	}
	argv, restored, err = plugin.GetRestoreCommand(ctx, cfg)
	if err != nil || !restored || slices.Contains(argv, "--fork") || calls != 1 {
		t.Fatalf("later reopen forked or lost identity: %v, %v, %v", argv, restored, err)
	}
}

func TestPrimePreparationRefusesMissingWrongAmbiguousOrFailedFork(t *testing.T) {
	for _, scenario := range []string{"zero forks", "wrong cwd", "wrong parent", "changed message", "changed ancestry", "multiple forks", "source changed", "command failed"} {
		t.Run(scenario, func(t *testing.T) {
			root, sourceCWD, workspace := t.TempDir(), t.TempDir(), t.TempDir()
			source := writePrimeNativeFixture(t, root, primeSourceID, sourceCWD, "", primeNativeFixtureRows()...)
			dataDir := t.TempDir()
			targetRoot, err := sessionDir(dataDir)
			if err != nil {
				t.Fatal(err)
			}
			plugin := primeTestPlugin()
			plugin.runCommand = func(context.Context, string, string, map[string]string, ...string) ([]byte, error) {
				if scenario == "command failed" {
					return nil, errors.New("synthetic provider failure")
				}
				if scenario == "zero forks" {
					return nil, nil
				}
				rows, cwd, parent := primeForkFixtureRows(), workspace, source
				switch scenario {
				case "wrong cwd":
					cwd = t.TempDir()
				case "wrong parent":
					parent = filepath.Join(root, "missing.jsonl")
				case "changed message":
					rows[1] = strings.ReplaceAll(rows[1], "synthetic reply", "different reply")
				case "changed ancestry":
					rows[1] = strings.ReplaceAll(rows[1], `"parentId":"user-1"`, `"parentId":null`)
				case "source changed":
					writePrimeNativeFixture(t, root, primeSourceID, sourceCWD, "", rows[0])
				}
				writePrimeNativeFixture(t, targetRoot, primeForkID, cwd, parent, rows...)
				if scenario == "multiple forks" {
					writePrimeNativeFixture(t, targetRoot, "51381da5-ea93-465a-9506-97868d30cc43", cwd, parent, rows...)
				}
				return nil, nil
			}
			ref, err := plugin.PrepareImportedSession(context.Background(), ports.ImportedSessionRelocation{NativeSessionID: primeSourceID, ConfigDir: root, TranscriptPath: source, SourceCWD: sourceCWD, LaunchCWD: workspace, DataDir: dataDir})
			if err == nil || ref.NativeSessionID != "" {
				t.Fatalf("unsafe native fork accepted: %#v, %v", ref, err)
			}
		})
	}
}

func TestPrimePreparationPreservesRunnerError(t *testing.T) {
	root, sourceCWD, workspace := t.TempDir(), t.TempDir(), t.TempDir()
	source := writePrimeNativeFixture(t, root, primeSourceID, sourceCWD, "", primeNativeFixtureRows()...)
	runnerErr := errors.New("synthetic provider startup failed")
	plugin := primeTestPlugin()
	plugin.runCommand = func(context.Context, string, string, map[string]string, ...string) ([]byte, error) {
		return nil, runnerErr
	}
	ref, err := plugin.PrepareImportedSession(context.Background(), ports.ImportedSessionRelocation{NativeSessionID: primeSourceID, ConfigDir: root, TranscriptPath: source, SourceCWD: sourceCWD, LaunchCWD: workspace, DataDir: t.TempDir()})
	if !errors.Is(err, runnerErr) || ref.NativeSessionID != "" {
		t.Fatalf("runner error lost or native identity returned: %#v, %v", ref, err)
	}
	if !strings.Contains(err.Error(), "prime native fork preparation failed: "+runnerErr.Error()) {
		t.Fatalf("native preparation diagnostic lost context or cause: %v", err)
	}
}

func TestPrimePreparedProofRefusesChangedNativeCopy(t *testing.T) {
	root, cwd, workspace := t.TempDir(), t.TempDir(), t.TempDir()
	source := writePrimeNativeFixture(t, root, primeSourceID, cwd, "", primeNativeFixtureRows()...)
	request := ports.ImportedSessionRelocation{NativeSessionID: primeSourceID, ConfigDir: root, TranscriptPath: source, SourceCWD: cwd, LaunchCWD: workspace, DataDir: t.TempDir()}
	plugin := primeTestPlugin()
	calls := 0
	plugin.runCommand = func(context.Context, string, string, map[string]string, ...string) ([]byte, error) {
		calls++
		targetRoot, err := sessionDir(request.DataDir)
		if err != nil {
			t.Fatal(err)
		}
		writePrimeNativeFixture(t, targetRoot, primeForkID, workspace, source, primeForkFixtureRows()...)
		return nil, nil
	}
	ref, err := plugin.PrepareImportedSession(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ref.TranscriptPath, []byte("torn native copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := plugin.PrepareImportedSession(context.Background(), request); err == nil || calls != 1 {
		t.Fatalf("changed prepared copy was reused or replaced: %v, calls=%d", err, calls)
	}
}

func TestPrimeImportedRestoreRefusesMissingIdentityRootOrWorkspace(t *testing.T) {
	root, workspace := t.TempDir(), t.TempDir()
	writePrimeNativeFixture(t, root, primeForkID, workspace, "", primeForkFixtureRows()...)
	for _, scenario := range []string{"source id", "missing root", "missing file", "wrong workspace"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := primeTestRestoreConfig(t, root, workspace, primeForkID)
			switch scenario {
			case "source id":
				cfg.Session.Metadata[ports.MetadataKeyAgentSessionID] = primeSourceID
			case "missing root":
				delete(cfg.Session.Metadata, ports.MetadataKeyImportConfigDir)
			case "missing file":
				cfg.Session.Metadata[ports.MetadataKeyAgentSessionID] = "51381da5-ea93-465a-9506-97868d30cc43"
				cfg.Session.Metadata[ports.MetadataKeyImportResumeNativeID] = cfg.Session.Metadata[ports.MetadataKeyAgentSessionID]
			case "wrong workspace":
				cfg.Session.WorkspacePath = t.TempDir()
			}
			if _, restored, err := primeTestPlugin().GetRestoreCommand(context.Background(), cfg); err == nil || restored {
				t.Fatalf("unsafe imported native restore accepted: restored=%v, err=%v", restored, err)
			}
		})
	}
}

func TestPrimeImportProofPublishesCompleteWithoutReplacing(t *testing.T) {
	root := t.TempDir()
	const marker = ".ao-import-synthetic.json"
	want := []byte(`{"Version":1,"TargetID":"synthetic"}`)
	if err := publishPrimeImportProof(root, marker, want); err != nil {
		t.Fatal(err)
	}
	if err := publishPrimeImportProof(root, marker, []byte("replacement")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing proof was not protected: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, marker))
	if err != nil || string(got) != string(want) {
		t.Fatalf("published proof = %q, %v", got, err)
	}
	info, err := os.Stat(filepath.Join(root, marker))
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("proof is not a regular file: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != marker {
		t.Fatalf("staging file leaked: %v, %v", entries, err)
	}
}

func TestPrimeForkMatchesCurrentAndLegacyNativeIDs(t *testing.T) {
	root, workspace := t.TempDir(), t.TempDir()
	source := filepath.Join(root, "synthetic-source.jsonl")
	if err := os.WriteFile(source, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	proof := primeImportProof{SourceID: primeSourceID, SourcePath: source, Workspace: workspace}
	for _, tc := range []struct {
		id   string
		want bool
	}{
		{primeForkID, true},
		{"7e30cc6c-ab67-72bf-abfb-9f68ef5499b1", true},
		{"7e30cc6c-ab67-12bf-abfb-9f68ef5499b1", false},
		{primeSourceID, false},
		{"not-a-native-uuid", false},
	} {
		t.Run(tc.id, func(t *testing.T) {
			history := primeNativeHistory{id: tc.id, cwd: workspace, parent: source}
			if got := primeForkMatches(history, proof, primeNativeHistory{}); got != tc.want {
				t.Fatalf("native ID accepted = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPrimePreparationMatchesProviderDecimalRoundTrip(t *testing.T) {
	for _, changedCost := range []bool{false, true} {
		t.Run(map[bool]string{false: "native numeric round trip", true: "changed cost refused"}[changedCost], func(t *testing.T) {
			root, cwd, workspace := t.TempDir(), t.TempDir(), t.TempDir()
			row := `{"type":"message","id":"assistant-1","parentId":null,"message":{"role":"assistant","content":[{"type":"text","text":"synthetic tool result"}],"usage":{"input":9122,"output":148,"totalTokens":9270,"cost":{"input":0.0036488000000000002,"output":0.0002368,"total":0.0038856000000000003}}}}`
			source := writePrimeNativeFixture(t, root, primeSourceID, cwd, "", row)
			request := ports.ImportedSessionRelocation{NativeSessionID: primeSourceID, ConfigDir: root, TranscriptPath: source, SourceCWD: cwd, LaunchCWD: workspace, DataDir: t.TempDir()}
			plugin := primeTestPlugin()
			const forkID = "01a1f34e-2789-7342-8012-f8817604abb0"
			plugin.runCommand = func(context.Context, string, string, map[string]string, ...string) ([]byte, error) {
				target, err := sessionDir(request.DataDir)
				if err != nil {
					t.Fatal(err)
				}
				forkRow := strings.ReplaceAll(strings.ReplaceAll(row, "0.0036488000000000002", "0.0036488"), "0.0038856000000000003", "0.0038856")
				if changedCost {
					forkRow = strings.ReplaceAll(forkRow, "0.0036488", "0.004")
				}
				writePrimeNativeFixture(t, target, forkID, workspace, source, forkRow)
				return nil, nil
			}
			ref, err := plugin.PrepareImportedSession(context.Background(), request)
			if changedCost {
				if err == nil || ref.NativeSessionID != "" {
					t.Fatalf("changed native cost accepted: %#v, %v", ref, err)
				}
			} else if err != nil || ref.NativeSessionID != forkID {
				t.Fatalf("provider decimal round trip rejected: %#v, %v", ref, err)
			}
		})
	}
}

func TestOpenAIImportedPrimeRestorePinsEndpointAfterLoginShell(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "https://stray.invalid/v1")
	root, workspace := t.TempDir(), t.TempDir()
	path := writePrimeNativeFixture(t, root, primeForkID, workspace, "", `{"type":"model_change","id":"model","parentId":null,"provider":"openai","modelId":"gpt-4.1-mini"}`, primeForkFixtureRows()[0])
	cfg := primeTestRestoreConfig(t, root, workspace, primeForkID)
	argv, ok, err := primeTestPlugin().GetRestoreCommand(context.Background(), cfg)
	if err != nil || !ok {
		t.Fatalf("restore: %v, %v", ok, err)
	}
	if !slices.Equal(argv[:3], []string{"env", "OPENAI_BASE_URL=https://api.openai.com/v1", "/synthetic/prime-agent"}) || !slices.Contains(argv, path) {
		t.Fatalf("OpenAI native restore inherits a login-shell endpoint: %v", argv)
	}
}
