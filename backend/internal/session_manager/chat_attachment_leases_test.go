package sessionmanager

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type leaseFixture struct {
	store     *sqlite.Store
	dataDir   string
	workspace string
	id        domain.SessionID
	conv      string
	now       time.Time
}

func newLeaseFixture(t *testing.T) *leaseFixture {
	t.Helper()
	ctx := context.Background()
	f := &leaseFixture{dataDir: t.TempDir(), workspace: t.TempDir(), now: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)}
	f.store = sqlitetest.MustOpenAt(t, f.dataDir)
	if err := f.store.UpsertProject(ctx, domain.ProjectRecord{ID: "lease", Path: f.dataDir, RegisteredAt: f.now}); err != nil {
		t.Fatal(err)
	}
	rec, err := f.store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "lease",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Mode:      domain.SessionModeChat,
		Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: f.now},
		Metadata:  domain.SessionMetadata{Branch: "feat/lease", WorkspacePath: f.workspace},
		CreatedAt: f.now,
		UpdatedAt: f.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.id = rec.ID
	conv, err := f.store.CreateConversation(ctx, "conv-lease", domain.ConversationScopeSession, "lease", rec.ID, f.now)
	if err != nil {
		t.Fatal(err)
	}
	f.conv = conv.ID
	if err := f.store.ClaimChatControllerGeneration(ctx, rec.ID, "gen-1"); err != nil {
		t.Fatal(err)
	}
	return f
}

// manager is a fresh Manager over the same database and data directory, as a
// daemon restart would build.
func (f *leaseFixture) manager() *Manager {
	return New(Deps{
		Store: f.store, Workspace: &fakeWorkspace{}, DataDir: f.dataDir,
		Clock: func() time.Time { return f.now }, Logger: slog.New(slog.DiscardHandler),
	})
}

// present reports whether the canonical and worktree copies of ref exist.
func (f *leaseFixture) present(t *testing.T, ref string) (canonical, worktree bool) {
	t.Helper()
	_, canonicalErr := os.Stat(filepath.Join(f.dataDir, "attachments", string(f.id), filepath.Base(ref)))
	_, worktreeErr := os.Stat(filepath.Join(f.workspace, filepath.FromSlash(ref)))
	return canonicalErr == nil, worktreeErr == nil
}

func (f *leaseFixture) requirePresent(t *testing.T, ref string, want bool) {
	t.Helper()
	canonical, worktree := f.present(t, ref)
	if canonical != want || worktree != want {
		t.Fatalf("%s: canonical=%v worktree=%v, want both %v", ref, canonical, worktree, want)
	}
}

func (f *leaseFixture) accept(t *testing.T, id, text string) {
	t.Helper()
	msg := domain.ConversationMessage{ID: id, Text: text, Origin: domain.MessageOriginHuman}
	if _, err := f.store.AppendUserMessage(context.Background(), f.conv, f.id, "gen-1", msg, "turn-"+id, f.now); err != nil {
		t.Fatal(err)
	}
}

func draftFiles(n int) []ports.SpawnAttachment {
	files := make([]ports.SpawnAttachment, n)
	for i := range files {
		files[i] = ports.SpawnAttachment{Ext: ".png", Data: []byte{byte('a' + i)}}
	}
	return files
}

// Releasing a draft deletes both copies of only the files no accepted message
// names; a release that arrives after the send (a late chip removal or a
// discard racing acceptance) leaves the sent file readable.
func TestDraftAttachmentReleaseKeepsAcceptedHistory(t *testing.T) {
	ctx := context.Background()
	f := newLeaseFixture(t)
	m := f.manager()
	refs, err := m.StageDraftAttachments(ctx, f.id, draftFiles(3))
	if err != nil {
		t.Fatal(err)
	}
	sent, removed, kept := refs[0], refs[1], refs[2]
	f.accept(t, "m1", "What is in "+sent+"?")

	missing, err := m.UpdateAttachmentLeases(ctx, f.id, []string{sent, removed}, []string{kept})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want none", missing)
	}
	f.requirePresent(t, sent, true)
	f.requirePresent(t, removed, false)
	f.requirePresent(t, kept, true)

	// Releasing again, or naming a path that was never a staged file, is a no-op.
	if _, err := m.UpdateAttachmentLeases(ctx, f.id, []string{removed, sent, "../../etc/passwd", "cloud:ref"}, nil); err != nil {
		t.Fatal(err)
	}
	f.requirePresent(t, sent, true)
}

// Files staged without a draft lease (spawn briefs, terminal sends, browser
// annotations) are never reclaimed, even if a caller names them.
func TestUnleasedAttachmentsAreNeverReleased(t *testing.T) {
	ctx := context.Background()
	f := newLeaseFixture(t)
	m := f.manager()
	refs, err := m.StageAttachments(ctx, f.id, draftFiles(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateAttachmentLeases(ctx, f.id, refs, nil); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(2 * ChatAttachmentLeaseTTL)
	if err := f.manager().ReclaimChatAttachments(ctx); err != nil {
		t.Fatal(err)
	}
	f.requirePresent(t, refs[0], true)
}

// A daemon that stopped between releasing a lease and deleting its files
// finishes on the next start, and a draft nobody reopened within the TTL is
// reclaimed then too. Renewed and sent files survive; a reclaimed file is
// reported gone to the draft that still shows it. Reclaiming twice is safe.
func TestDraftAttachmentReclaimRecoversAcrossRestartAndExpiry(t *testing.T) {
	ctx := context.Background()
	f := newLeaseFixture(t)
	refs, err := f.manager().StageDraftAttachments(ctx, f.id, draftFiles(4))
	if err != nil {
		t.Fatal(err)
	}
	sent, interrupted, renewed, abandoned := refs[0], refs[1], refs[2], refs[3]
	f.accept(t, "m1", sent)
	// The release committed, then the daemon died before deleting anything.
	if err := f.store.ReleaseChatAttachmentLeases(ctx, f.id, []string{filepath.Base(interrupted)}, f.now); err != nil {
		t.Fatal(err)
	}
	f.requirePresent(t, interrupted, true)

	f.now = f.now.Add(ChatAttachmentLeaseTTL / 2)
	if missing, err := f.manager().UpdateAttachmentLeases(ctx, f.id, nil, []string{renewed}); err != nil || len(missing) != 0 {
		t.Fatalf("renew = %v, %v", missing, err)
	}
	f.now = f.now.Add(ChatAttachmentLeaseTTL/2 + time.Hour)

	restarted := f.manager()
	if err := restarted.ReclaimChatAttachments(ctx); err != nil {
		t.Fatal(err)
	}
	f.requirePresent(t, sent, true)
	f.requirePresent(t, interrupted, false)
	f.requirePresent(t, renewed, true)
	f.requirePresent(t, abandoned, false)
	if err := restarted.ReclaimChatAttachments(ctx); err != nil {
		t.Fatalf("second reclaim: %v", err)
	}

	missing, err := restarted.UpdateAttachmentLeases(ctx, f.id, nil, []string{sent, renewed, abandoned})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{abandoned}; !reflect.DeepEqual(missing, want) {
		t.Fatalf("missing = %v, want %v", missing, want)
	}
}

// Deleting the session still removes every attachment it has, leased or not.
func TestRemoveSessionAttachmentsStillRemovesDraftFiles(t *testing.T) {
	ctx := context.Background()
	f := newLeaseFixture(t)
	m := f.manager()
	refs, err := m.StageDraftAttachments(ctx, f.id, draftFiles(1))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.attachments.RemoveSession(ctx, f.id); err != nil {
		t.Fatal(err)
	}
	if canonical, _ := f.present(t, refs[0]); canonical {
		t.Fatal("canonical copy survived session deletion")
	}
	if err := m.ReclaimChatAttachments(ctx); err != nil {
		t.Fatalf("reclaim after session data removal: %v", err)
	}
}
