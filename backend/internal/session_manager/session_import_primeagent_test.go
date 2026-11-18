//go:build !windows

package sessionmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/primeagent"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type primeImportIdentityStore struct{ *fakeStore }

func (s *primeImportIdentityStore) SetPreparedImportIdentity(ctx context.Context, id domain.SessionID, expected, next *domain.SessionImportSource, workspace domain.SessionMetadata, now time.Time) (bool, error) {
	updated, err := s.fakeStore.SetPreparedImportIdentity(ctx, id, expected, next, workspace, now)
	if updated && err == nil {
		// Match the real store's atomic prepared-id/session-handle transaction.
		rec := s.sessions[id]
		rec.Metadata.AgentSessionID, rec.Metadata.ProviderConversationID = next.ResumeNativeID, next.ResumeNativeID
		rec.Metadata.AgentSessionIDLaunchID = ""
		s.sessions[id] = rec
	}
	return updated, err
}

type primeManagedImportAgent struct {
	*primeagent.Plugin
	restores []ports.RestoreConfig
}

func (a *primeManagedImportAgent) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	a.restores = append(a.restores, cfg)
	return a.Plugin.GetRestoreCommand(ctx, cfg)
}

func TestPrimeImportPrelaunchAdoptionAndReopenMakeNoMessengerWrites(t *testing.T) {
	const sourceID = "f395a38e-7815-4849-ae63-74e445dfc830"
	const forkID = "7e30cc6c-ab67-42bf-abfb-9f68ef5499b1"
	ctx := context.Background()
	sourceRoot, sourceCWD, workspace, bin := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	workspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(sourceRoot, sourceID+".jsonl")
	header := func(id, cwd, parent string) string {
		t.Helper()
		data, err := json.Marshal(map[string]any{"type": "session", "version": 3, "id": id, "cwd": cwd, "parentSession": parent})
		if err != nil {
			t.Fatal(err)
		}
		return string(data) + "\n"
	}
	rows := `{"type":"message","id":"u1","parentId":null,"message":{"role":"user","content":"remember synthetic prime history"}}
{"type":"message","id":"a1","parentId":"u1","message":{"role":"assistant","content":"synthetic history retained"}}
`
	original := header(sourceID, sourceCWD, "") + rows
	if err := os.WriteFile(source, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	agent := &primeManagedImportAgent{Plugin: primeagent.New()}
	rt := &fakeRuntime{}
	m, st, _ := newExitedResumeManager(t, rt, agent)
	m.store = &primeImportIdentityStore{fakeStore: st}
	messenger := &fakeMessenger{}
	m.messenger = messenger
	generation := 0
	m.newLaunchID = func() string {
		generation++
		return fmt.Sprintf("prime-generation-%d", generation)
	}
	targetRoot := filepath.Join(m.dataDir, "agent-runtime", "prime-agent", "sessions")
	target := filepath.Join(targetRoot, forkID+".jsonl")
	trace := filepath.Join(t.TempDir(), "native-forks")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	// Only the provider subprocess is synthetic. The real Prime preparer,
	// prober and restore argv run through the actual manager lifecycle.
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = --help ]; then printf 'prime-agent fixture\\n'; exit 0; fi\n" +
		"[ \"$1\" = --mode ] && [ \"$2\" = rpc ] && [ \"$4\" = --session-dir ] || exit 21\n" +
		"[ \"$5\" = " + quote(targetRoot) + " ] && [ \"$7\" = " + quote(workspace) + " ] && [ \"$9\" = " + quote(source) + " ] || exit 22\n" +
		"cat > " + quote(target) + " <<'PRIME_SYNTHETIC_NATIVE'\n" +
		header(forkID, workspace, source) + rows + "PRIME_SYNTHETIC_NATIVE\n" +
		"printf 'fork\\n' >> " + quote(trace) + "\n"
	if err := os.WriteFile(filepath.Join(bin, "prime-agent"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	rec := st.sessions["mer-1"]
	rec.Harness, rec.Mode, rec.Activity.State = domain.HarnessPrimeAgent, domain.SessionModeTUI, domain.ActivityIdle
	rec.Metadata = domain.SessionMetadata{
		WorkspacePath: workspace, Branch: "ao/prime-import", AgentSessionID: sourceID, ProviderConversationID: sourceID, NativeTranscriptPath: source,
		ImportSource: &domain.SessionImportSource{NativeID: sourceID, ConfigDir: sourceRoot, CWD: sourceCWD, Transferred: true, Prepared: true},
	}
	st.sessions[rec.ID] = rec
	result, err := m.ResumeAgentWithMode(ctx, rec.ID)
	if err != nil || result.Mode != RestoreModeNative || result.Session.NeedsImportResume() {
		t.Fatalf("Prime first open = %#v, %v", result, err)
	}
	if result.Session.Metadata.AgentSessionID != forkID || result.Session.Metadata.ProviderConversationID != forkID || result.Session.Metadata.ImportSource.NativeID != sourceID || result.Session.Metadata.ImportSource.ResumeNativeID != forkID {
		t.Fatalf("manager did not adopt the prepared native id: %#v", result.Session.Metadata)
	}
	if len(agent.restores) != 1 || agent.restores[0].Session.Metadata[ports.MetadataKeyAgentSessionID] != forkID || agent.restores[0].Session.WorkspacePath != workspace {
		t.Fatalf("first native argv used the wrong identity/workspace: %#v", agent.restores)
	}
	later := st.sessions[rec.ID]
	later.Activity.State = domain.ActivityExited
	later.Metadata.RuntimeHandleID, later.Metadata.RuntimeLaunchID, later.Metadata.AgentSessionIDLaunchID = "", "", ""
	st.sessions[rec.ID] = later
	result, err = m.ResumeAgentWithMode(ctx, rec.ID)
	if err != nil || result.Session.Metadata.AgentSessionID != forkID || len(agent.restores) != 2 {
		t.Fatalf("Prime adopted reopen = %#v, %v", result, err)
	}
	if len(messenger.msgs) != 0 || len(rt.inputs) != 0 {
		t.Fatalf("taskless initialization sent input: messages=%v inputs=%v", messenger.msgs, rt.inputs)
	}
	forks, err := os.ReadFile(trace)
	if err != nil || string(forks) != "fork\n" {
		t.Fatalf("reopen forked again: %q, %v", forks, err)
	}
	for _, cfg := range agent.restores {
		argv, native, err := agent.Plugin.GetRestoreCommand(ctx, cfg)
		if err != nil || !native || slices.Contains(argv, "--fork") || !slices.Contains(argv, target) || !slices.Contains(argv, workspace) {
			t.Fatalf("unsafe native reopen argv: %v, %v, %v", argv, native, err)
		}
	}
	after, err := os.ReadFile(source)
	if err != nil || string(after) != original {
		t.Fatalf("original native history changed: %v", err)
	}
}
