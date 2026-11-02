//go:build !windows

package sessionmanager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/aider"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestAiderFirstOpenCopiesNativeHistoryAndRefusesExistingHistory(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "native copy", true: "existing history refused"}[existing], func(t *testing.T) {
			source, workspace, binDir := t.TempDir(), t.TempDir(), t.TempDir()
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			if err := os.WriteFile(filepath.Join(binDir, "aider"), []byte("#!/bin/sh\nexit 73\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			body := "#### make a greeting\n\nHello\n"
			path := filepath.Join(source, ".aider.chat.history.md")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(workspace, ".aider.chat.history.md")
			if existing {
				if err := os.WriteFile(target, []byte("#### unrelated worktree chat\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			id := aider.NativeHistoryID(source)
			rt := &fakeRuntime{}
			m, st, _ := newExitedResumeManager(t, rt, aider.New())
			rec := st.sessions["mer-1"]
			rec.Mode, rec.Harness, rec.Activity.State = domain.SessionModeTUI, domain.HarnessAider, domain.ActivityIdle
			rec.Metadata = domain.SessionMetadata{WorkspacePath: workspace, Branch: "ao/import", AgentSessionID: id, ProviderConversationID: id, NativeTranscriptPath: path,
				ImportSource: &domain.SessionImportSource{NativeID: id, ConfigDir: source, CWD: source, Transferred: true, Prepared: true}}
			st.sessions[rec.ID] = rec
			result, err := m.ResumeAgentWithMode(context.Background(), rec.ID)
			if existing {
				if err == nil || rt.created != 0 || !st.sessions[rec.ID].NeedsImportResume() {
					t.Fatalf("existing history was accepted = %#v, %v, created=%d", result, err, rt.created)
				}
				data, err := os.ReadFile(target)
				if err != nil || string(data) != "#### unrelated worktree chat\n" {
					t.Fatalf("existing history changed = %q, %v", data, err)
				}
				return
			}
			if err != nil || result.Mode != RestoreModeNative || result.Session.NeedsImportResume() || rt.created != 1 {
				t.Fatalf("native Aider first open = %#v, %v, created=%d", result, err, rt.created)
			}
			argv := strings.Join(rt.lastCfg.Argv, " ")
			if !strings.Contains(argv, "--restore-chat-history") || !strings.Contains(argv, "--chat-history-file "+target) || rt.lastCfg.WorkspacePath != workspace {
				t.Fatalf("wrong native/worktree launch = %#v", rt.lastCfg)
			}
			provenance := result.Session.Metadata.ImportSource
			if provenance.ConfigDir != source || provenance.ResumeConfigDir != workspace || provenance.ResumeTranscriptPath != target || provenance.SourceTranscriptPath != path || result.Session.Metadata.AgentSessionID != id {
				t.Fatalf("native copy lost provenance or identity = %+v", result.Session.Metadata)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != body {
				t.Fatalf("source history changed = %q, %v", data, err)
			}
		})
	}
}
