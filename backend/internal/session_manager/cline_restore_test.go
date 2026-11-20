package sessionmanager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
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
				// The hook must carry the new runtime's identity, not the shared
				// Cline hub's environment or the generation from a previous launch.
				hook, err := os.ReadFile(filepath.Join(workspace, ".clinerules", "hooks", "TaskStart"))
				if err != nil {
					t.Fatal(err)
				}
				launchID := rt.lastCfg.Env["AO_RUNTIME_LAUNCH_ID"]
				if launchID == "" || !strings.Contains(string(hook), "AO_RUNTIME_LAUNCH_ID='"+launchID+"'") ||
					!strings.Contains(string(hook), "AO_SESSION_ID='"+string(rec.ID)+"'") {
					t.Fatalf("%s hook is not bound to resumed runtime: %s", operation, hook)
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

func TestClineRestoreWithoutIDFallsBackWhenHistoryFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	for _, operation := range []string{"restore", "resume"} {
		t.Run(operation, func(t *testing.T) {
			home, workspace, binDir := t.TempDir(), t.TempDir(), t.TempDir()
			binary := filepath.Join(binDir, "cline")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", binDir)
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)

			rt := &fakeRuntime{outputs: []string{"Type a message"}}
			m, st, ws := newExitedResumeManager(t, rt, cline.New())
			ws.path = workspace
			rec := st.sessions["mer-1"]
			rec.Harness = domain.HarnessCline
			rec.IsTerminated = operation == "restore"
			rec.Metadata.WorkspacePath = workspace
			rec.Metadata.AgentSessionID = ""
			const savedPrompt = "continue the saved task"
			rec.Metadata.Prompt = savedPrompt
			st.sessions[rec.ID] = rec
			msg := &fakeMessenger{}
			m.messenger = sessionguard.New(st, msg, m.logger)
			m.messenger.SetInputLease(m)

			var result RestoreResult
			var err error
			if operation == "restore" {
				result, err = m.RestoreWithMode(ctx, rec.ID)
			} else {
				result, err = m.ResumeAgentWithMode(ctx, rec.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.Mode != RestoreModeSavedPrompt || rt.created != 1 {
				t.Fatalf("mode = %q, runtimes created = %d; want saved_prompt and one fresh runtime", result.Mode, rt.created)
			}
			if slices.Contains(rt.lastCfg.Argv, "--id") {
				t.Fatalf("fresh launch attempted native restore: %q", rt.lastCfg.Argv)
			}
			if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], savedPrompt) {
				t.Fatalf("fresh launch did not deliver the saved prompt: %q", msg.msgs)
			}
		})
	}
}
