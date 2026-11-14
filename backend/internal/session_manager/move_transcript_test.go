package sessionmanager

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/nativetransfer"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentruntime"
)

// Another install's session of the same name leaves a transcript under the id
// derived from that name; a move must not take it for this session's.
func TestCloudTranscriptUsesOnlyTheRecordedConversation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	projects := filepath.Join(home, ".claude", "projects")
	writeTranscript := func(dir, id, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(projects, dir), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(projects, dir, id+".jsonl"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeTranscript("-elsewhere-app-1", agentruntime.ClaudeSessionID("app-1"), "{\"cwd\":\"/elsewhere\"}\n")

	cwd := t.TempDir()
	m := &Manager{agents: providerHomeAgents{}, dataDir: t.TempDir(), executable: os.Executable, logger: slog.New(slog.DiscardHandler)}
	rec := domain.SessionRecord{ID: "app-1", Harness: domain.HarnessClaudeCode, Metadata: domain.SessionMetadata{WorkspacePath: cwd}}
	if _, err := m.CloudTranscriptSize(context.Background(), rec, domain.ProjectRecord{}); !errors.Is(err, ErrNoRecordedConversation) {
		t.Fatalf("unrecorded conversation: err = %v, want ErrNoRecordedConversation", err)
	}

	const recorded = "7bc36b37-4693-40b3-ace9-6efddc586895"
	dir, err := nativetransfer.ClaudeProjectDirName(cwd)
	if err != nil {
		t.Fatal(err)
	}
	own := "{\"cwd\":\"" + cwd + "\"}\n"
	writeTranscript(dir, recorded, own)
	rec.Metadata.AgentSessionID = recorded
	size, err := m.CloudTranscriptSize(context.Background(), rec, domain.ProjectRecord{})
	if err != nil || size != int64(len(own)) {
		t.Fatalf("recorded conversation: size %d, err %v; want %d", size, err, len(own))
	}

	rec.Harness = domain.HarnessCodex
	if _, err := m.CloudTranscriptSize(context.Background(), rec, domain.ProjectRecord{}); !errors.Is(err, ErrCloudTranscriptUnsupported) {
		t.Fatalf("codex: err = %v, want ErrCloudTranscriptUnsupported", err)
	}
}

type nativeConfigAgent struct {
	*recordingAgent
	dir string
}

func (a nativeConfigAgent) NativeSessionConfigDir(context.Context, map[string]string) (string, error) {
	return a.dir, nil
}

// A moved conversation is resumed when it can be placed; otherwise the agent
// starts a new conversation from the fallback note.
func TestSpawnTransferResumesOrFallsBackToTheNote(t *testing.T) {
	const id = "0b5a6c1e-3f2d-4c8a-9e7b-1d2c3b4a5f60"
	inbox := filepath.Join(t.TempDir(), id+".jsonl")
	if err := os.WriteFile(inbox, []byte(`{"type":"user","cwd":"/workspace/repository","sessionId":"`+id+`","message":{"role":"user","content":"hi"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "conversation.tgz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nativetransfer.WriteClaudeArchiveFromCloud(context.Background(), f, inbox, "/home/agent/.claude/projects/-workspace-repository"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	for _, tc := range []struct {
		name       string
		archive    string
		wantResume bool
	}{
		{"placed", archive, true},
		{"archive missing", filepath.Join(t.TempDir(), "missing.tgz"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
			agent := &recordingAgent{}
			m := New(Deps{
				Runtime:   &fakeRuntime{},
				Agents:    singleAgent{agent: nativeConfigAgent{recordingAgent: agent, dir: t.TempDir()}},
				Workspace: &fakeWorkspace{path: t.TempDir()},
				Store:     st,
				Messenger: &fakeMessenger{},
				Lifecycle: &fakeLCM{store: st},
				LookPath:  func(string) (string, error) { return "/bin/true", nil },
			})
			rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{
				ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
				Transfer: &ports.SpawnTransfer{NativeArchive: tc.archive, FallbackPrompt: "NOTE"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantResume {
				if agent.restoreCalls != 1 || agent.launchCalls != 0 || agent.lastRestore.Session.Metadata[ports.MetadataKeyAgentSessionID] != id {
					t.Fatalf("restore calls %d, launch calls %d, resumed %q; want a resume of %s",
						agent.restoreCalls, agent.launchCalls, agent.lastRestore.Session.Metadata[ports.MetadataKeyAgentSessionID], id)
				}
				if rec.Metadata.AgentSessionID != id {
					t.Fatalf("recorded conversation = %q, want %s", rec.Metadata.AgentSessionID, id)
				}
				return
			}
			if agent.launchCalls != 1 || agent.restoreCalls != 0 || agent.lastLaunch.Prompt != "NOTE" {
				t.Fatalf("launch calls %d, restore calls %d, prompt %q; want a fresh launch with the note",
					agent.launchCalls, agent.restoreCalls, agent.lastLaunch.Prompt)
			}
			if rec.Metadata.AgentSessionID != "" {
				t.Fatalf("recorded conversation = %q, want none", rec.Metadata.AgentSessionID)
			}
		})
	}
}
