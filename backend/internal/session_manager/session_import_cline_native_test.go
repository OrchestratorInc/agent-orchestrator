//go:build !windows

package sessionmanager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/cline"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestClineFirstOpenUsesExactImportedRootAndAOExecutionDirectory(t *testing.T) {
	for _, valid := range []bool{true, false} {
		t.Run(map[bool]string{true: "native import", false: "malformed history refused"}[valid], func(t *testing.T) {
			home, root, source, workspace, binDir := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CLINE_DIR", "")
			// The import root differs from the launch default, so AO must pass it explicitly.
			t.Setenv("CLINE_DATA_DIR", "")
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			// Resolution uses a synthetic executable. fakeRuntime records argv;
			// neither the real provider nor a login is needed by this CI test.
			if err := os.WriteFile(filepath.Join(binDir, "cline"), []byte("#!/bin/sh\nexit 73\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			id := "imported-cline-root"
			dir := filepath.Join(root, "sessions", id)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			manifest, err := json.Marshal(map[string]string{"session_id": id, "cwd": source})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, id+".json"), manifest, 0o600); err != nil {
				t.Fatal(err)
			}
			body := `{"sessionId":"imported-cline-root","messages":[{"role":"user","content":"make a greeting"}]}`
			if !valid {
				body = `{"sessionId":`
			}
			path := filepath.Join(dir, id+".messages.json")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			rt := &fakeRuntime{}
			m, st, _ := newExitedResumeManager(t, rt, cline.New())
			rec := st.sessions["mer-1"]
			rec.Mode, rec.Harness, rec.Activity.State = domain.SessionModeTUI, domain.HarnessCline, domain.ActivityIdle
			rec.Metadata = domain.SessionMetadata{WorkspacePath: workspace, Branch: "ao/import", AgentSessionID: id, ProviderConversationID: id, NativeTranscriptPath: path,
				ImportSource: &domain.SessionImportSource{NativeID: id, ConfigDir: root, CWD: source, Transferred: true, Prepared: true}}
			st.sessions[rec.ID] = rec
			result, err := m.ResumeAgentWithMode(context.Background(), rec.ID)
			if !valid {
				if err == nil || rt.created != 0 || !st.sessions[rec.ID].NeedsImportResume() {
					t.Fatalf("invalid import launched/adopted = %#v, %v, created=%d", result, err, rt.created)
				}
				return
			}
			if err != nil || result.Mode != RestoreModeNative || result.Session.NeedsImportResume() || rt.created != 1 {
				t.Fatalf("first open = %#v, %v, created=%d", result, err, rt.created)
			}
			argv := strings.Join(rt.lastCfg.Argv, " ")
			if !strings.Contains(argv, "--cwd "+workspace) || !strings.Contains(argv, "--id "+id) || strings.Contains(argv, source) || rt.lastCfg.WorkspacePath != workspace || rt.lastCfg.Env["CLINE_DATA_DIR"] != root {
				t.Fatalf("wrong native/workspace launch = %#v", rt.lastCfg)
			}
			if result.Session.Metadata.AgentSessionID != id || result.Session.Metadata.ProviderConversationID != id {
				t.Fatalf("selected identity changed = %+v", result.Session.Metadata)
			}
		})
	}
}
