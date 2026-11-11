package chat_test

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// The artifacts a turn changed are read from the artifact directory, not from the
// provider's diff: an agent that writes a file through a shell command and
// registers it with `ao artifact add` reports no diff at all.

func artifactDirFor(t *testing.T, h *harness) string {
	t.Helper()
	record, found, err := h.st.GetSession(context.Background(), testSession)
	if err != nil || !found {
		t.Fatalf("read session: found=%v err=%v", found, err)
	}
	dir := cmp.Or(record.Metadata.ArtifactDir, filepath.Join(h.rendersDir, "artifacts", string(testSession)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeTurnArtifact(t *testing.T, dir, rel, content string, mtime time.Time) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestTurnRecordsArtifactsWrittenWithoutAProviderDiff(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	dir := artifactDirFor(t, h)
	earlier := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	writeTurnArtifact(t, dir, "notes.md", "# Notes\n", earlier)
	writeTurnArtifact(t, dir, "untouched.md", "same\n", earlier)

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "generate some artifacts", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// What a Bash tool call would leave behind: a new file, an edited one, a new
	// file in a subdirectory. The provider reports none of it.
	later := earlier.Add(time.Hour)
	writeTurnArtifact(t, dir, "chart.svg", "<svg/>", later)
	writeTurnArtifact(t, dir, "notes.md", "# Notes\n\nMore.\n", later)
	writeTurnArtifact(t, dir, "data/rows.csv", "a,b\n", later)

	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"},
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted},
	)

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].Artifacts != nil
	})
	turn := snapshot.Turns[0]
	if turn.Diff != nil {
		t.Errorf("turn diff = %+v, want none: the provider reported no diff", turn.Diff)
	}
	want := []domain.ConversationTurnArtifact{
		{Path: "chart.svg", Status: domain.TurnArtifactAdded},
		{Path: "data/rows.csv", Status: domain.TurnArtifactAdded},
		{Path: "notes.md", Status: domain.TurnArtifactModified},
	}
	got := turn.Artifacts.Files
	if len(got) != len(want) {
		t.Fatalf("turn artifacts = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("artifact %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestTurnThatChangesNoArtifactRecordsNone(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	dir := artifactDirFor(t, h)
	writeTurnArtifact(t, dir, "notes.md", "# Notes\n", time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC))

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "just answer", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Deleting an artifact is not something to show a card for.
	if err := os.Remove(filepath.Join(dir, "notes.md")); err != nil {
		t.Fatal(err)
	}
	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"},
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted},
	)
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateCompleted
	})
	if snapshot.Turns[0].Artifacts != nil {
		t.Fatalf("turn artifacts = %+v, want none", snapshot.Turns[0].Artifacts)
	}
}
