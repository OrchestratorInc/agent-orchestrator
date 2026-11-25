//go:build !windows

package sessionmanager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/agent/codewhale"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/runfile"
)

func TestCodeWhaleAdoptedImportArchiveRestoreAfterHistoryGrowth(t *testing.T) {
	ctx := context.Background()
	const sourceID = "c819deee-22a0-452b-b03e-7cb499b5f891"
	root, sourceCWD, workspace, bin := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	workspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEWHALE_HOME", root)
	t.Setenv("CODEWHALE_MANAGED_CONFIG_PATH", "")
	t.Setenv("CODEWHALE_RUNTIME_DIR", "")
	t.Setenv("DEEPSEEK_MANAGED_CONFIG_PATH", "")
	// Provider subprocesses answer capability/config probes only. Preparation,
	// first-open proof, hooks, restore argv and locator use the real adapter.
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then printf '0.10.2\\n'; exit 0; fi\nif [ \"$1\" = config ] && [ \"$2\" = get ] && [ \"$3\" = managed_config_path ]; then exit 0; fi\nexit 91\n"
	if err := os.WriteFile(filepath.Join(bin, "codewhale"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	message := func(role, text string) any {
		return map[string]any{"role": role, "content": []any{map[string]any{"type": "text", "text": text}}}
	}
	sourceDoc := map[string]any{"schema_version": 1, "metadata": map[string]any{"id": sourceID, "workspace": sourceCWD, "model": "synthetic-model", "updated_at": "2026-11-03T00:00:00Z"}, "messages": []any{message("user", "COPIED_HISTORY_MARKER")}}
	write := func(path string, doc map[string]any) []byte {
		t.Helper()
		body, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		return body
	}
	source := filepath.Join(root, "sessions", sourceID+".json")
	original := write(source, sourceDoc)
	agent, rt := codewhale.New(), &fakeRuntime{}
	m, st, ws := newExitedResumeManager(t, rt, agent)
	m.store = &primeImportIdentityStore{fakeStore: st}
	m.dataDir, err = filepath.EvalSymlinks(m.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	ws.path = workspace
	m.runFilePath = filepath.Join(m.dataDir, "running.json")
	if err := runfile.Write(m.runFilePath, runfile.Info{PID: 42, Port: 43123, StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	generation := 0
	m.newLaunchID = func() string { generation++; return fmt.Sprintf("codewhale-generation-%d", generation) }
	rec := st.sessions["mer-1"]
	rec.Harness, rec.Mode, rec.Activity.State = domain.HarnessCodewhale, domain.SessionModeTUI, domain.ActivityIdle
	rec.Metadata = domain.SessionMetadata{WorkspacePath: workspace, Branch: "ao/codewhale-import", AgentSessionID: sourceID, ProviderConversationID: sourceID, NativeTranscriptPath: source, ImportSource: &domain.SessionImportSource{NativeID: sourceID, ConfigDir: root, CWD: sourceCWD, Transferred: true, Prepared: true}}
	st.sessions[rec.ID] = rec
	result, err := m.ResumeAgentWithMode(ctx, rec.ID)
	if err != nil || result.Mode != RestoreModeNative || result.Session.NeedsImportResume() {
		t.Fatalf("first native adoption=%+v err=%v", result, err)
	}
	prepared := result.Session.Metadata.ImportSource
	copiedID := prepared.ResumeNativeID
	if !prepared.Adopted || copiedID == "" || copiedID == sourceID || result.Session.Metadata.AgentSessionID != copiedID {
		t.Fatalf("wrong adopted identity: %+v", result.Session.Metadata)
	}
	if after, err := os.ReadFile(source); err != nil || string(after) != string(original) {
		t.Fatalf("preparation changed source: %v", err)
	}
	runtimeDir := rt.lastCfg.Env["CODEWHALE_RUNTIME_DIR"]
	if runtimeDir == "" {
		t.Fatal("first launch omitted native runtime")
	}
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "owner.json"), []byte(`{"owner_id":"growth-owner"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Emulate native save after adoption: first-open marker disappears, while
	// the same copied ID gains a runtime binding and more than 16 MiB of turns.
	copied, err := os.ReadFile(prepared.ResumeTranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(copied, &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc, "ao_import")
	sum := sha256.Sum256([]byte("growth-owner\x00" + filepath.Join(runtimeDir, "events.lock")))
	doc["metadata"].(map[string]any)["runtime_store"] = map[string]any{"data_dir": runtimeDir, "execution_scope": hex.EncodeToString(sum[:])}
	messages := doc["messages"].([]any)
	for range 257 {
		messages = append(messages, message("assistant", strings.Repeat("n", 64<<10)))
	}
	messages = append(messages, message("assistant", "NATIVE_ONLY_LATER"))
	doc["messages"] = messages
	grown := write(prepared.ResumeTranscriptPath, doc)
	if len(grown) <= 16<<20 {
		t.Fatal("fixture did not exceed source admission budget")
	}
	sourceDoc["messages"] = append(sourceDoc["messages"].([]any), message("user", "SOURCE_ONLY_LATER"))
	changedSource := write(source, sourceDoc)
	for _, archive := range []bool{true, false} {
		later := st.sessions[rec.ID]
		later.IsTerminated, later.Activity.State = archive, domain.ActivityExited
		later.Metadata.RuntimeHandleID, later.Metadata.RuntimeLaunchID, later.Metadata.AgentSessionIDLaunchID = "", "", ""
		st.sessions[rec.ID] = later
		if archive {
			result, err = m.RestoreWithMode(ctx, rec.ID)
		} else {
			result, err = m.ResumeAgentWithMode(ctx, rec.ID)
		}
		if err != nil || result.Mode != RestoreModeNative {
			t.Fatalf("archive=%v native reopen=%+v err=%v", archive, result, err)
		}
		meta := result.Session.Metadata
		if meta.AgentSessionID != copiedID || meta.ProviderConversationID != copiedID || meta.ImportSource.NativeID != sourceID || meta.ImportSource.ResumeNativeID != copiedID || !meta.ImportSource.Adopted {
			t.Fatalf("reopen changed provenance/identity: %+v", meta)
		}
		if rt.lastCfg.Env["CODEWHALE_RUNTIME_DIR"] != runtimeDir || !slices.Contains(rt.lastCfg.Argv, copiedID) {
			t.Fatalf("reopen lost copied binding: %+v", rt.lastCfg)
		}
		id, ok, err := agent.ResolveNativeSessionID(ctx, ports.NativeSessionResolveConfig{DataDir: m.dataDir, SessionID: rec.ID, LaunchID: meta.RuntimeLaunchID})
		if err != nil || !ok || id != copiedID {
			t.Fatalf("reopen locator=%q,%v,%v", id, ok, err)
		}
		after, err := os.ReadFile(prepared.ResumeTranscriptPath)
		if err != nil || string(after) != string(grown) || strings.Contains(string(after), "SOURCE_ONLY_LATER") || !strings.Contains(string(after), "NATIVE_ONLY_LATER") {
			t.Fatalf("reopen replaced prepared history: %v", err)
		}
	}
	if after, err := os.ReadFile(source); err != nil || string(after) != string(changedSource) {
		t.Fatalf("reopen modified original: %v", err)
	}
}
