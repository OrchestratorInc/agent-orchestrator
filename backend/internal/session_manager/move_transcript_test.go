package sessionmanager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

	rec.Harness = domain.HarnessAider
	if _, err := m.CloudTranscriptSize(context.Background(), rec, domain.ProjectRecord{}); !errors.Is(err, ErrCloudTranscriptUnsupported) {
		t.Fatalf("aider: err = %v, want ErrCloudTranscriptUnsupported", err)
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
	const codexID = "01a1f2eb-1c10-7462-9afc-a0787b4e965c"
	rollout := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(rollout, []byte(`{"type":"session_meta","payload":{"id":"`+codexID+`","timestamp":"2026-11-19T08:27:36.895Z","cwd":"/workspace/repository"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name       string
		harness    domain.AgentHarness
		archive    string
		wantResume string
	}{
		{"placed", domain.HarnessClaudeCode, archive, id},
		{"codex placed", domain.HarnessCodex, rollout, codexID},
		{"archive missing", domain.HarnessClaudeCode, filepath.Join(t.TempDir(), "missing.tgz"), ""},
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
				ProjectID: "mer", Kind: domain.KindWorker, Harness: tc.harness,
				Transfer: &ports.SpawnTransfer{NativeArchive: tc.archive, FallbackPrompt: "NOTE", SystemNote: "MOVE-WIP-NOTE"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if want := tc.wantResume; want != "" {
				if agent.restoreCalls != 1 || agent.launchCalls != 0 || agent.lastRestore.Session.Metadata[ports.MetadataKeyAgentSessionID] != want {
					t.Fatalf("restore calls %d, launch calls %d, resumed %q; want a resume of %s",
						agent.restoreCalls, agent.launchCalls, agent.lastRestore.Session.Metadata[ports.MetadataKeyAgentSessionID], want)
				}
				if rec.Metadata.AgentSessionID != want {
					t.Fatalf("recorded conversation = %q, want %s", rec.Metadata.AgentSessionID, want)
				}
				// The move's note reaches the resumed launch's system prompt, not a turn.
				system := agent.lastRestore.SystemPrompt
				if file := agent.lastRestore.SystemPromptFile; file != "" {
					data, _ := os.ReadFile(file)
					system += string(data)
				}
				if !strings.Contains(system, "MOVE-WIP-NOTE") || agent.lastRestore.Prompt != "" {
					t.Fatalf("restore system prompt %q, prompt %q; want the note in the system prompt and no turn", system, agent.lastRestore.Prompt)
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

type openCodeTransferAgent struct {
	*recordingAgent
	imported, cwd, exported string
}

func (a *openCodeTransferAgent) ExportConversation(_ context.Context, id string, _ map[string]string) ([]byte, error) {
	a.exported = id
	return []byte(`{"info":{"id":"` + id + `","location":{"directory":"/w/app"}},"messages":[]}`), nil
}

// A Chat session records OpenCode's id in its harness envelope; the export
// names the raw session id and replaces the directory with the placeholder.
func TestCloudTranscriptExportsTheOpenCodeSession(t *testing.T) {
	agent := &openCodeTransferAgent{recordingAgent: &recordingAgent{}}
	m := &Manager{agents: singleAgent{agent: agent}, dataDir: t.TempDir(), executable: os.Executable, logger: slog.New(slog.DiscardHandler)}
	rec := domain.SessionRecord{ID: "app-1", Harness: domain.HarnessOpenCodeV2, Metadata: domain.SessionMetadata{ProviderConversationID: "ao-harness:opencode-v2:ses_1"}}
	var out strings.Builder
	id, err := m.WriteCloudTranscript(context.Background(), rec, domain.ProjectRecord{}, &out, 1<<20)
	if err != nil || id != "ses_1" || agent.exported != "ses_1" || !strings.Contains(out.String(), `"directory":"/ao-moved/cwd"`) {
		t.Fatalf("WriteCloudTranscript = %q, %v, exported %q:\n%s", id, err, agent.exported, out.String())
	}
	rec.Metadata.ProviderConversationID = "ao-harness:opencode:ses_1"
	if _, err := m.WriteCloudTranscript(context.Background(), rec, domain.ProjectRecord{}, &out, 1<<20); err == nil {
		t.Fatal("another harness's conversation was exported")
	}
}

func (a *openCodeTransferAgent) ImportConversation(_ context.Context, data []byte, cwd string, _ map[string]string) (string, error) {
	a.imported, a.cwd = string(data), cwd
	return "ses_1", nil
}

// An OpenCode 2 conversation is imported in the new worktree, with the
// sandbox's directory replaced, and resumed.
func TestSpawnTransferImportsAnOpenCodeConversation(t *testing.T) {
	doc := filepath.Join(t.TempDir(), "ses_1.jsonl")
	if err := os.WriteFile(doc, []byte(`{"info":{"id":"ses_1","location":{"directory":"/workspace/repository"}},"messages":[{"text":"/workspace/repository/a.go"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	agent := &openCodeTransferAgent{recordingAgent: &recordingAgent{}}
	workspace := t.TempDir()
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{path: workspace},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessOpenCodeV2,
		Transfer: &ports.SpawnTransfer{NativeArchive: doc, FallbackPrompt: "NOTE"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if agent.cwd != workspace || strings.Contains(agent.imported, "/workspace/repository") || !strings.Contains(agent.imported, workspace+"/a.go") {
		t.Fatalf("imported %s in %s", agent.imported, agent.cwd)
	}
	if agent.restoreCalls != 1 || rec.Metadata.AgentSessionID != "ses_1" {
		t.Fatalf("restore calls %d, recorded %q", agent.restoreCalls, rec.Metadata.AgentSessionID)
	}
}

// A move to AO Cloud uploads a copy of the conversation and archives the
// session here; the conversation itself stays. Its transcript and sidecar
// folder keep every byte, and the archived record still points at them, which
// is what restoring, forking and reading its history go by.
func TestMoveToCloudLeavesTheConversationHere(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	cwd, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const id = "7bc36b37-4693-40b3-ace9-6efddc586895"
	dirName, err := nativetransfer.ClaudeProjectDirName(cwd)
	if err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(home, ".claude", "projects", dirName)
	files := map[string]string{
		id + ".jsonl":                   `{"type":"user","cwd":"` + cwd + `","sessionId":"` + id + `","message":{"role":"user","content":"remember PELICAN-42"}}` + "\n",
		id + "/subagents/agent-1.jsonl": `{"type":"user","cwd":"` + cwd + `","message":{"role":"user","content":"sub"}}` + "\n",
		id + "/tool-results/out.txt":    "large tool output\n",
	}
	sums := map[string][32]byte{}
	for rel, content := range files {
		path := filepath.Join(project, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		sums[rel] = sha256.Sum256([]byte(content))
	}
	transcript := filepath.Join(project, id+".jsonl")
	meta := domain.SessionMetadata{WorkspacePath: cwd, AgentSessionID: id, NativeTranscriptPath: transcript, RuntimeHandleID: "h1"}

	// move/transcript reads the conversation for the upload.
	reader := &Manager{agents: providerHomeAgents{}, dataDir: t.TempDir(), executable: os.Executable, logger: slog.New(slog.DiscardHandler)}
	rec := domain.SessionRecord{ID: "mer-1", Harness: domain.HarnessClaudeCode, Metadata: meta}
	var upload, sidecar bytes.Buffer
	if _, err := reader.WriteCloudTranscript(context.Background(), rec, domain.ProjectRecord{}, &upload, 1<<20); err != nil {
		t.Fatal(err)
	}
	if ok, err := reader.WriteCloudSidecar(context.Background(), rec, domain.ProjectRecord{}, &sidecar, 1<<20); err != nil || !ok {
		t.Fatalf("sidecar = %v, %v", ok, err)
	}

	// move/complete archives the session.
	m, st, _, _ := newManager()
	live := mkLive("mer-1")
	live.Harness, live.Metadata = domain.HarnessClaudeCode, meta
	live.Activity.State = domain.ActivityExited
	live.MovedTo = &domain.SessionMove{MoveID: "m-1", OrgID: "org-1", SessionID: "cloud-7"}
	st.sessions["mer-1"] = live
	if _, err := m.RequestKill(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}

	for rel, want := range sums {
		data, err := os.ReadFile(filepath.Join(project, filepath.FromSlash(rel)))
		if err != nil || sha256.Sum256(data) != want {
			t.Fatalf("%s after the move: %v, changed=%v", rel, err, err == nil)
		}
	}
	archived := st.sessions["mer-1"]
	if !archived.IsTerminated || archived.Metadata.AgentSessionID != id || archived.Metadata.NativeTranscriptPath != transcript {
		t.Fatalf("archived record = terminated %v, conversation %q at %q; want it archived and still pointing at %s",
			archived.IsTerminated, archived.Metadata.AgentSessionID, archived.Metadata.NativeTranscriptPath, transcript)
	}
}
