package sessionmanager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/autohand"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestAutohandImportRefusesMalformedNativeFilesBeforeLaunch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		adopted bool
		state   bool
	}{
		{name: "first open torn history"},
		{name: "first open torn state", state: true},
		{name: "adopted torn history", adopted: true},
		{name: "adopted torn state", adopted: true, state: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := "11111111-2222-4333-8444-555555555555-1790000000000"
			root, source, workspace, binDir := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
			t.Setenv("AUTOHAND_HOME", root)
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			binary, script := "autohand", "#!/bin/sh\nexit 73\n"
			if runtime.GOOS == "windows" {
				binary, script = "autohand.cmd", "@exit /b 73\r\n"
			}
			if err := os.WriteFile(filepath.Join(binDir, binary), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(root, "sessions", id)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			metadata, err := json.Marshal(map[string]string{"sessionId": id, "projectPath": source})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "metadata.json"), metadata, 0o600); err != nil {
				t.Fatal(err)
			}
			body := "{\"role\":\"user\",\"content\":\"synthetic prompt\"}\n{\"role\":"
			if tc.state {
				body = `{"role":"user","content":"synthetic prompt"}`
				if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"readFileState":`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(dir, "conversation.jsonl")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			rt := &fakeRuntime{}
			m, st, _ := newExitedResumeManager(t, rt, autohand.New())
			rec := st.sessions["mer-1"]
			rec.Mode, rec.Harness, rec.Activity.State = domain.SessionModeTUI, domain.HarnessAutohand, domain.ActivityIdle
			if tc.adopted {
				rec.Activity.State = domain.ActivityExited
			}
			rec.Metadata = domain.SessionMetadata{WorkspacePath: workspace, Branch: "ao/import", AgentSessionID: id, ProviderConversationID: id, NativeTranscriptPath: path,
				ImportSource: &domain.SessionImportSource{NativeID: id, ConfigDir: root, CWD: source, Transferred: true, Prepared: true, Adopted: tc.adopted}}
			st.sessions[rec.ID] = rec
			if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err == nil {
				t.Fatal("malformed native files reached launch/adoption")
			}
			if rt.created != 0 || st.sessions[rec.ID].NeedsImportResume() != !tc.adopted {
				t.Fatalf("malformed history changed session: created=%d metadata=%+v", rt.created, st.sessions[rec.ID].Metadata)
			}
		})
	}
}
