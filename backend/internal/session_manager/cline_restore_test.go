package sessionmanager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/cline"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
)

func TestClineRestoreProbesRecoveredConversation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	for _, operation := range []string{"restore", "resume"} {
		for _, savedHistory := range []bool{true, false} {
			name := operation + "/missing-history"
			if savedHistory {
				name = operation + "/saved-history"
			}
			t.Run(name, func(t *testing.T) {
				home, workspace, binDir := t.TempDir(), t.TempDir(), t.TempDir()
				const recoveredID = "1793339903050_zcuop"
				writeJSON := func(path string, value any) {
					t.Helper()
					data, err := json.Marshal(value)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, data, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if savedHistory {
					dir := filepath.Join(home, ".cline", "data", "sessions", recoveredID)
					if err := os.MkdirAll(dir, 0o700); err != nil {
						t.Fatal(err)
					}
					writeJSON(filepath.Join(dir, recoveredID+".json"), map[string]string{
						"session_id": recoveredID, "cwd": workspace,
					})
					writeJSON(filepath.Join(dir, recoveredID+".messages.json"), map[string]any{
						"sessionId": recoveredID,
						"messages":  []map[string]string{{"role": "user", "content": "keep this follow-up"}},
					})
				}
				history, err := json.Marshal([]map[string]string{{"sessionId": recoveredID, "cwd": workspace}})
				if err != nil {
					t.Fatal(err)
				}
				script := "#!/bin/sh\n[ \"$1\" = history ] || exit 1\nprintf '%s' '" + string(history) + "'\n"
				binary := filepath.Join(binDir, "cline")
				if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", binDir)
				t.Setenv("HOME", home)
				t.Setenv("USERPROFILE", home)

				rt := &fakeRuntime{}
				m, st, ws := newExitedResumeManager(t, rt, cline.New())
				ws.path = workspace
				rec := st.sessions["mer-1"]
				rec.Harness = domain.HarnessCline
				rec.IsTerminated = operation == "restore"
				rec.Metadata.WorkspacePath = workspace
				rec.Metadata.AgentSessionID = "conv_1793289582097_j8ibi1w"
				st.sessions[rec.ID] = rec
				msg := &fakeMessenger{}
				m.messenger = sessionguard.New(st, msg, m.logger)
				m.messenger.SetInputLease(m)
				var result RestoreResult
				if operation == "restore" {
					result, err = m.RestoreWithMode(ctx, rec.ID)
				} else {
					result, err = m.ResumeAgentWithMode(ctx, rec.ID)
				}
				if err != nil {
					t.Fatal(err)
				}
				wantMode := RestoreModeSavedPrompt
				if savedHistory {
					wantMode = RestoreModeNative
				}
				if result.Mode != wantMode {
					t.Fatalf("mode = %q, want %q; argv = %q", result.Mode, wantMode, rt.lastCfg.Argv)
				}
				if savedHistory {
					idFlag := slices.Index(rt.lastCfg.Argv, "--id")
					if idFlag < 0 || idFlag+1 >= len(rt.lastCfg.Argv) || rt.lastCfg.Argv[idFlag+1] != recoveredID {
						t.Fatalf("runtime did not resume recovered ID: %q", rt.lastCfg.Argv)
					}
					if len(msg.msgs) != 0 {
						t.Fatalf("native restore replayed the saved prompt: %q", msg.msgs)
					}
				}
			})
		}
	}
}
