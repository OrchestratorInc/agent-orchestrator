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
