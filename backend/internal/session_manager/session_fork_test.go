package sessionmanager

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/forkseed"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/scratch"
	"github.com/aoagents/agent-orchestrator/backend/internal/artifactversions"
	"github.com/aoagents/agent-orchestrator/backend/internal/attachmentstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// forkSourceWorktree makes a source session worktree with an unpushed commit,
// an edit, and a new file.
func forkSourceWorktree(t *testing.T, repo string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "source")
	runManagerGit(t, repo, "worktree", "add", "-q", "-b", "ao/source", src, "main")
	runManagerGit(t, src, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "unpushed")
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

func captureForkSeed(t *testing.T, src string) (head, ref string) {
	t.Helper()
	ref = forkseed.RefPrefix + "test"
	head, err := forkseed.Capture(context.Background(), src, ref)
	if err != nil {
		t.Fatal(err)
	}
	return head, ref
}

func TestForkCopiesUncommittedAndUntracked(t *testing.T) {
	m, st, _, repo := newGitTaskPreparationManager(t)
	src := forkSourceWorktree(t, repo)
	status := runManagerGit(t, src, "status", "--porcelain")
	head, ref := captureForkSeed(t, src)
	cfg := ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Fork: &ports.SpawnFork{SourceSessionID: "mer-0", StartPoint: head, SeedRef: ref}}
	ws, _, err := m.createSessionWorkspace(context.Background(), st.projects["mer"], cfg, "mer-1", "ao/mer-1/root", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(runManagerGit(t, ws.Path, "rev-parse", "HEAD")); got != head {
		t.Fatalf("fork HEAD = %s, want source HEAD %s", got, head)
	}
	for name, want := range map[string]string{"README.md": "edited\n", "new.txt": "new\n"} {
		b, err := os.ReadFile(filepath.Join(ws.Path, name))
		if err != nil || string(b) != want {
			t.Fatalf("%s = %q, %v; want %q", name, b, err, want)
		}
	}
	if got := runManagerGit(t, src, "status", "--porcelain"); got != status {
		t.Fatalf("source status changed: %q, want %q", got, status)
	}
	if refs := runManagerGit(t, repo, "for-each-ref", forkseed.RefPrefix); strings.TrimSpace(refs) != "" {
		t.Fatalf("seed ref left behind: %q", refs)
	}
}

type failingForkSeeds struct{ deleted []string }

func (f *failingForkSeeds) Apply(context.Context, string, string) error { return errors.New("boom") }

func (*failingForkSeeds) RecordBase(context.Context, string, string) error { return nil }
func (*failingForkSeeds) UnchangedSince(context.Context, string, string) (bool, error) {
	return false, nil
}
func (*failingForkSeeds) DiscardChanges(context.Context, string) error { return nil }
func (*failingForkSeeds) RestoreBase(context.Context, string, string) (bool, error) {
	return false, nil
}
func (f *failingForkSeeds) Delete(_ context.Context, _ string, ref string) error {
	f.deleted = append(f.deleted, ref)
	return nil
}

func TestForkRestoreFailureRollsBack(t *testing.T) {
	m, st, _, repo := newGitTaskPreparationManager(t)
	src := forkSourceWorktree(t, repo)
	head, ref := captureForkSeed(t, src)
	seeds := &failingForkSeeds{}
	m.forkSeeds = seeds
	cfg := ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Fork: &ports.SpawnFork{StartPoint: head, SeedRef: ref}}
	ws, _, err := m.createSessionWorkspace(context.Background(), st.projects["mer"], cfg, "mer-1", "ao/mer-1/root", nil)
	if err == nil {
		t.Fatal("want an error when the files can't be copied")
	}
	// The caller rolls back a returned worktree; it must be reported.
	if ws.Path == "" {
		t.Fatal("the created worktree must be returned for rollback")
	}
	if len(seeds.deleted) != 1 || seeds.deleted[0] != ref {
		t.Fatalf("seed ref not deleted: %v", seeds.deleted)
	}
}

func TestChatForkStartsFromPreparedConversation(t *testing.T) {
	launcher := &recordingLauncher{}
	m, _, _ := newChatManager(launcher)
	var target ports.ForkTarget
	rec, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		RequestedMode: domain.SessionModeChat,
		Fork: &ports.SpawnFork{SourceSessionID: "mer-0", StartPoint: "abc", PrepareChat: func(_ context.Context, got ports.ForkTarget) (string, error) {
			target = got
			return "forked-thread", nil
		}},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if len(launcher.started) != 1 || launcher.started[0].ProviderConversationID != "forked-thread" {
		t.Fatalf("started = %+v", launcher.started)
	}
	// So the fork's conversation starts with, and saves, the original's settings.
	if !launcher.started[0].ForkFirstStart {
		t.Fatal("the fork's first start isn't marked as one")
	}
	if target.SessionID != rec.ID || target.WorkspacePath == "" || target.WorkspacePath != launcher.started[0].WorkspacePath {
		t.Fatalf("target = %+v, started in %q", target, launcher.started[0].WorkspacePath)
	}
}

func TestFailedChatForkStartsNothing(t *testing.T) {
	launcher := &recordingLauncher{}
	m, _, _ := newChatManager(launcher)
	_, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		RequestedMode: domain.SessionModeChat,
		Fork: &ports.SpawnFork{StartPoint: "abc", PrepareChat: func(context.Context, ports.ForkTarget) (string, error) {
			return "", errors.New("no native session")
		}},
	})
	if err == nil {
		t.Fatal("want an error")
	}
	if len(launcher.started) != 0 {
		t.Fatalf("started = %+v", launcher.started)
	}
}

func TestForkCopiesArtifacts(t *testing.T) {
	launcher := &recordingLauncher{}
	m, _, _ := newChatManager(launcher)
	m.dataDir = t.TempDir()
	srcDir := m.artifactDir("mer-0")
	if err := os.MkdirAll(filepath.Join(srcDir, "page"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "page", "index.html"), []byte("<h1>hi</h1>"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		RequestedMode: domain.SessionModeChat,
		Fork:          &ports.SpawnFork{SourceSessionID: "mer-0", StartPoint: "abc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(m.artifactDir(rec.ID), "page", "index.html"))
	if err != nil || string(got) != "<h1>hi</h1>" {
		t.Fatalf("artifact = %q, %v", got, err)
	}
	// The copy is independent: removing the source's artifacts keeps the fork's.
	if err := os.RemoveAll(srcDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(m.artifactDir(rec.ID), "page", "index.html")); err != nil {
		t.Fatal(err)
	}
}

type lineageStore struct {
	*fakeStore
	forks map[domain.SessionID]domain.SessionFork
}

func (s lineageStore) GetSessionFork(_ context.Context, id domain.SessionID) (domain.SessionFork, bool, error) {
	f, ok := s.forks[id]
	return f, ok, nil
}

func TestForkPromptStatesNewIdentity(t *testing.T) {
	launcher := &recordingLauncher{}
	m, _, _ := newChatManager(launcher)
	_, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		RequestedMode: domain.SessionModeChat,
		Fork:          &ports.SpawnFork{SourceSessionID: "mer-0", SourceTitle: "Fix login", StartPoint: "abc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	prompt := launcher.started[0].SystemPrompt
	for _, want := range []string{"fork of Orchestrator session mer-0", `"Fix login"`, "pull requests belong to the original", "own worktree"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("system prompt lacks %q", want)
		}
	}
}

func TestForkPromptSurvivesRestore(t *testing.T) {
	m, st, _, _ := newManager()
	m.store = lineageStore{fakeStore: st, forks: map[domain.SessionID]domain.SessionFork{
		"mer-5": {ForkSessionID: "mer-5", SourceSessionID: "mer-0", SourceTitle: "Fix login"},
	}}
	prompt, err := m.buildSystemPrompt(context.Background(), domain.KindWorker, "mer", "mer-5", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "fork of Orchestrator session mer-0") {
		t.Fatalf("restored fork lost its fork instructions:\n%s", prompt)
	}
	plain, err := m.buildSystemPrompt(context.Background(), domain.KindWorker, "mer", "mer-6", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "Session Fork") {
		t.Fatal("a normal session got fork instructions")
	}
}

// transcriptAgent keeps each native session as <dir>/<id>.jsonl.
type transcriptAgent struct {
	fakeAgent
	dir string
}

func (a transcriptAgent) NativeSessionConfigDir(context.Context, map[string]string) (string, error) {
	return a.dir, nil
}

func (a transcriptAgent) LocateTranscript(_ context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	path := filepath.Join(ref.ConfigDir, ref.NativeSessionID+".jsonl")
	if _, err := os.Stat(path); err != nil {
		return "", false, nil //nolint:nilerr // a missing transcript is not found
	}
	return path, true, nil
}

func forkDeleteManager(t *testing.T, harness domain.AgentHarness) (*Manager, string) {
	t.Helper()
	m, st, _, _ := newManager()
	m.dataDir = t.TempDir()
	native := t.TempDir()
	m.agents = singleAgent{agent: transcriptAgent{dir: native}}
	m.store = lineageStore{fakeStore: st, forks: map[domain.SessionID]domain.SessionFork{
		"mer-5": {ForkSessionID: "mer-5", SourceSessionID: "mer-0"},
	}}
	for _, id := range []domain.SessionID{"mer-0", "mer-5"} {
		st.sessions[id] = domain.SessionRecord{ID: id, ProjectID: "mer", Kind: domain.KindWorker, Harness: harness, IsTerminated: true,
			Mode: domain.SessionModeChat, Metadata: domain.SessionMetadata{ProviderConversationID: "native-" + string(id), WorkspacePath: "/ws/" + string(id)}}
		if st.cleanupFacts == nil {
			st.cleanupFacts = map[domain.SessionID]domain.SessionCleanupRecord{}
		}
		st.cleanupFacts[id] = domain.SessionCleanupRecord{SessionID: id, WorkspaceDisposition: domain.DispositionRemoved}
		if err := os.MkdirAll(m.artifactDir(id), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(native, "native-"+string(id)+".jsonl"), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return m, native
}

func TestDeletingForkLeavesNoLeftovers(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex} {
		m, native := forkDeleteManager(t, harness)
		if err := m.DeleteSession(context.Background(), "mer-5", false); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(m.artifactDir("mer-5")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s: fork artifacts left: %v", harness, err)
		}
		if _, err := os.Stat(filepath.Join(native, "native-mer-5.jsonl")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s: fork's native session left: %v", harness, err)
		}
		if _, err := os.Stat(filepath.Join(native, "native-mer-0.jsonl")); err != nil {
			t.Fatalf("%s: source's native session removed: %v", harness, err)
		}
	}
}

func TestDeletingForkKeepsSourceProviderSession(t *testing.T) {
	m, native := forkDeleteManager(t, domain.HarnessCodex)
	if err := m.DeleteSession(context.Background(), "mer-0", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(native, "native-mer-0.jsonl")); err != nil {
		t.Fatalf("deleting a normal session removed its provider session: %v", err)
	}
	if _, err := os.Stat(m.artifactDir("mer-5")); err != nil {
		t.Fatalf("fork artifacts removed with the source: %v", err)
	}
}

func TestRetriedForkReforks(t *testing.T) {
	launcher := &recordingLauncher{}
	m, st, _ := newChatManager(launcher)
	m.dataDir = t.TempDir()
	deferred := deferredBackground(m)
	ws := m.workspace.(*fakeWorkspace)
	ws.createErr = errors.New("temporary git failure")
	cfg := asyncChatSpawnConfig("")
	cfg.Fork = &ports.SpawnFork{SourceSessionID: "mer-0", StartPoint: "abc", PrepareChat: func(context.Context, ports.ForkTarget) (string, error) {
		return "first-fork", nil
	}}
	rec, _, _, err := m.Spawn(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	(*deferred)[0]()
	if st.sessions[rec.ID].ProvisionState != domain.SessionProvisionFailed {
		t.Fatalf("state = %q", st.sessions[rec.ID].ProvisionState)
	}
	m.store = lineageStore{fakeStore: st, forks: map[domain.SessionID]domain.SessionFork{rec.ID: {ForkSessionID: rec.ID, SourceSessionID: "mer-0"}}}
	var retriedFor domain.SessionID
	m.SetForkRetrier(func(_ context.Context, fork domain.SessionRecord) (*ports.SpawnFork, error) {
		retriedFor = fork.ID
		return &ports.SpawnFork{SourceSessionID: "mer-0", StartPoint: "abc", PrepareChat: func(context.Context, ports.ForkTarget) (string, error) {
			return "second-fork", nil
		}}, nil
	})
	ws.createErr = nil
	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	(*deferred)[1]()
	if retriedFor != rec.ID || len(launcher.started) != 1 || launcher.started[0].ProviderConversationID != "second-fork" {
		t.Fatalf("retried for %q, started %+v", retriedFor, launcher.started)
	}
}

type linkRewriteStore struct {
	*fakeStore
	from, to, conversation string
	copiedFrom, copiedTo   string
}

func (s *linkRewriteStore) CopyForkMessageContent(_ context.Context, source, fork string) (int, error) {
	s.copiedFrom, s.copiedTo = source, fork
	return 1, nil
}

func (s *linkRewriteStore) ConversationForSession(_ context.Context, id domain.SessionID) (domain.ConversationRecord, error) {
	return domain.ConversationRecord{ID: "conv-" + string(id)}, nil
}

func (s *linkRewriteStore) RewriteConversationText(_ context.Context, conversation, from, to string) (int, error) {
	s.conversation, s.from, s.to = conversation, from, to
	return 1, nil
}

func TestForkCopiesAttachmentsAndPointsLinksAtItself(t *testing.T) {
	launcher := &recordingLauncher{}
	m, st, _ := newChatManager(launcher)
	m.dataDir = t.TempDir()
	m.attachments = attachmentstore.New(m.dataDir)
	store := &linkRewriteStore{fakeStore: st}
	m.store = store
	src := filepath.Join(m.dataDir, "attachments", "mer-0")
	if err := os.MkdirAll(src, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "attachment-1.png"), []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		RequestedMode: domain.SessionModeChat,
		Fork: &ports.SpawnFork{SourceSessionID: "mer-0", StartPoint: "abc", PrepareChat: func(context.Context, ports.ForkTarget) (string, error) {
			return "forked", nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(m.dataDir, "attachments", string(rec.ID), "attachment-1.png")); err != nil || string(b) != "png" {
		t.Fatalf("attachment not copied: %q, %v", b, err)
	}
	wantFrom := "ao://sessions/" + string(chatTestProject) + "/mer-0/artifacts/"
	wantTo := "ao://sessions/" + string(chatTestProject) + "/" + string(rec.ID) + "/artifacts/"
	if store.conversation != "conv-"+string(rec.ID) || store.from != wantFrom || store.to != wantTo {
		t.Fatalf("rewrite = %q %q -> %q", store.conversation, store.from, store.to)
	}
	if store.copiedFrom != "conv-mer-0" || store.copiedTo != "conv-"+string(rec.ID) {
		t.Fatalf("images copied from %q to %q", store.copiedFrom, store.copiedTo)
	}
}

type recordingForkSeeds struct{ applied, deleted, restoredBase []string }

func (r *recordingForkSeeds) RestoreBase(_ context.Context, worktree, ref string) (bool, error) {
	r.restoredBase = append(r.restoredBase, worktree+"|"+ref)
	return true, nil
}

func (r *recordingForkSeeds) Apply(_ context.Context, _ string, ref string) error {
	r.applied = append(r.applied, ref)
	return nil
}

func (*recordingForkSeeds) RecordBase(context.Context, string, string) error { return nil }
func (*recordingForkSeeds) UnchangedSince(context.Context, string, string) (bool, error) {
	return false, nil
}
func (*recordingForkSeeds) DiscardChanges(context.Context, string) error { return nil }
func (r *recordingForkSeeds) Delete(_ context.Context, _ string, ref string) error {
	r.deleted = append(r.deleted, ref)
	return nil
}

func TestForkKeepsABorrowedSnapshot(t *testing.T) {
	m, st, _, repo := newGitTaskPreparationManager(t)
	src := forkSourceWorktree(t, repo)
	head, _ := captureForkSeed(t, src)
	seeds := &recordingForkSeeds{}
	m.forkSeeds = seeds
	cfg := ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Fork: &ports.SpawnFork{StartPoint: head, SeedRef: "refs/ao/snapshots/mer-0/turn-3", KeepSeedRef: true}}
	if _, _, err := m.createSessionWorkspace(context.Background(), st.projects["mer"], cfg, "mer-1", "ao/mer-1/root", nil); err != nil {
		t.Fatal(err)
	}
	if len(seeds.applied) != 1 || len(seeds.deleted) != 0 {
		t.Fatalf("applied %v, deleted %v", seeds.applied, seeds.deleted)
	}
}

type artifactVersionStore struct {
	*fakeStore
	versions map[string][]domain.SessionArtifactVersion
}

func (s artifactVersionStore) ListSessionArtifactVersions(_ context.Context, _ domain.SessionID, path string) ([]domain.SessionArtifactVersion, error) {
	return s.versions[path], nil
}

func (s artifactVersionStore) ListLatestSessionArtifactVersions(_ context.Context, _ domain.SessionID) ([]domain.SessionArtifactVersion, error) {
	var latest []domain.SessionArtifactVersion
	for _, versions := range s.versions {
		latest = append(latest, versions[len(versions)-1])
	}
	return latest, nil
}

func writeArtifactBlob(t *testing.T, dir, content string) string {
	t.Helper()
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	path := filepath.Join(dir, ".versions", "blobs", sum[:2], sum)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return sum
}

func TestOlderTurnForkGetsThatTurnsArtifacts(t *testing.T) {
	m, st, _, _ := newManager()
	m.dataDir = t.TempDir()
	src := m.artifactDir("mer-0")
	cutoff := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	before, after := cutoff.Add(-time.Minute), cutoff.Add(time.Minute)
	writeTestFile(t, filepath.Join(src, "report.md"), "report v2")
	writeTestFile(t, filepath.Join(src, "notes", "todo.md"), "created later")
	writeTestFile(t, filepath.Join(src, "plain.txt"), "never versioned")
	// Written before the turn and deleted after it: only its saved version is left.
	gone := writeArtifactBlob(t, src, "deleted later")
	if err := os.Chtimes(filepath.Join(src, "plain.txt"), before, before); err != nil {
		t.Fatal(err)
	}
	v1 := writeArtifactBlob(t, src, "report v1")
	v2 := writeArtifactBlob(t, src, "report v2")
	todo := writeArtifactBlob(t, src, "created later")
	m.store = artifactVersionStore{fakeStore: st, versions: map[string][]domain.SessionArtifactVersion{
		"report.md":     {{Path: "report.md", Version: 1, SHA256: v1, ModTime: before}, {Path: "report.md", Version: 2, SHA256: v2, ModTime: after}},
		"notes/todo.md": {{Path: "notes/todo.md", Version: 1, SHA256: todo, ModTime: after}},
		"old/gone.md":   {{Path: "old/gone.md", Version: 1, SHA256: gone, ModTime: before}},
	}}
	dst := t.TempDir()
	if err := m.copyForkArtifactsAsOf(context.Background(), "mer-0", dst, cutoff, ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "old", "gone.md")); string(b) != "deleted later" {
		t.Fatalf("old/gone.md = %q, want the artifact as it was at that turn, though deleted since", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "report.md")); string(b) != "report v1" {
		t.Fatalf("report.md = %q, want the version from that turn", b)
	}
	if _, err := os.Stat(filepath.Join(dst, "notes", "todo.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a file created after the turn must not be copied")
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "plain.txt")); string(b) != "never versioned" {
		t.Fatalf("plain.txt = %q", b)
	}
	if _, err := os.Stat(filepath.Join(dst, ".versions")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the version store must not be copied")
	}
}

func TestLatestTurnForkSkipsTheVersionStore(t *testing.T) {
	m, _, _, _ := newManager()
	m.dataDir = t.TempDir()
	src := m.artifactDir("mer-0")
	writeTestFile(t, filepath.Join(src, "report.md"), "current")
	writeArtifactBlob(t, src, "current")
	dst := t.TempDir()
	if err := m.copyForkArtifacts(context.Background(), "mer-0", dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "report.md")); string(b) != "current" {
		t.Fatalf("report.md = %q", b)
	}
	if _, err := os.Stat(filepath.Join(dst, ".versions")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the version store must not be copied")
	}
}

func inheritedForkFixture(t *testing.T, sourceTerminated bool) (*Manager, domain.SessionRecord, ports.WorkspaceInfo) {
	t.Helper()
	m, st, _, repo := newGitTaskPreparationManager(t)
	src := forkSourceWorktree(t, repo)
	head, ref := captureForkSeed(t, src)
	cfg := ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Fork: &ports.SpawnFork{SourceSessionID: "mer-0", StartPoint: head, SeedRef: ref}}
	ws, _, err := m.createSessionWorkspace(context.Background(), st.projects["mer"], cfg, "mer-1", "ao/mer-1/root", nil)
	if err != nil {
		t.Fatal(err)
	}
	st.sessions["mer-0"] = domain.SessionRecord{ID: "mer-0", ProjectID: "mer", IsTerminated: sourceTerminated, Metadata: domain.SessionMetadata{WorkspacePath: src}}
	fork := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", IsTerminated: true, Metadata: domain.SessionMetadata{WorkspacePath: ws.Path}}
	st.sessions["mer-1"] = fork
	m.store = lineageStore{fakeStore: st, forks: map[domain.SessionID]domain.SessionFork{"mer-1": {ForkSessionID: "mer-1", SourceSessionID: "mer-0"}}}
	return m, fork, ws
}

func TestUnchangedForkDropsInheritedChanges(t *testing.T) {
	m, fork, ws := inheritedForkFixture(t, false)
	if status := runManagerGit(t, ws.Path, "status", "--porcelain"); strings.TrimSpace(status) == "" {
		t.Fatal("the fork should start with the original's uncommitted files")
	}
	if !m.dropInheritedForkChanges(context.Background(), fork, ws) {
		t.Fatal("an unchanged fork of a live original should drop its inherited changes")
	}
	if status := runManagerGit(t, ws.Path, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Fatalf("worktree still dirty: %q", status)
	}
}

func TestChangedForkKeepsItsWork(t *testing.T) {
	m, fork, ws := inheritedForkFixture(t, false)
	if err := os.WriteFile(filepath.Join(ws.Path, "fork-work.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if m.dropInheritedForkChanges(context.Background(), fork, ws) {
		t.Fatal("a fork with its own changes must be kept")
	}
	if _, err := os.Stat(filepath.Join(ws.Path, "fork-work.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestForkOfAnArchivedOriginalKeepsItsFiles(t *testing.T) {
	m, fork, ws := inheritedForkFixture(t, true)
	if m.dropInheritedForkChanges(context.Background(), fork, ws) {
		t.Fatal("with the original archived, the fork may hold the only copy of those changes")
	}
}

func TestCleanupRemovesAnUnchangedForksWorktree(t *testing.T) {
	m, fork, ws := inheritedForkFixture(t, false)
	if _, refusal := m.cleanupOne(context.Background(), fork, ws); refusal.refused() {
		t.Fatalf("cleanup refused: %+v", refusal)
	}
	if _, err := os.Stat(ws.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree still on disk: %v", err)
	}
}

func TestCleanupKeepsAChangedForksWorktree(t *testing.T) {
	m, fork, ws := inheritedForkFixture(t, false)
	if err := os.WriteFile(filepath.Join(ws.Path, "fork-work.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, refusal := m.cleanupOne(context.Background(), fork, ws); !refusal.refused() {
		t.Fatal("a fork with its own uncommitted work must be kept")
	}
	if _, err := os.Stat(filepath.Join(ws.Path, "fork-work.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestArchivingAnUnchangedForkRemovesItsWorktree(t *testing.T) {
	m, fork, ws := inheritedForkFixture(t, false)
	st := m.store.(lineageStore).fakeStore
	fork.IsTerminated = false
	st.sessions[fork.ID] = fork
	if _, err := m.Kill(context.Background(), fork.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if _, err := os.Stat(ws.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree still on disk after archiving: %v", err)
	}
}

func TestForkFallsBackToTheDefaultModelWhenTheOriginalsIsRejected(t *testing.T) {
	m, _, _, _ := newManager()
	// The original's model now needs a newer Claude Code than this machine has.
	m.modelCatalog = tuningCatalog{catalog: ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
		{ID: "claude-opus-5-5", IsDefault: true},
		{ID: "claude-opus-6", RequiresVersion: "9.0.0", UnavailableReason: "needs a newer Claude Code"},
	}}}
	cfg := ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, AgentConfig: ports.AgentConfig{Model: "claude-opus-6"}}
	if _, err := m.resolveAgentConfig(context.Background(), cfg, domain.ProjectConfig{}); !errors.Is(err, ErrUnsupportedModel) {
		t.Fatalf("a normal session keeps the strict check: %v", err)
	}
	cfg.Fork = &ports.SpawnFork{SourceSessionID: "mer-0"}
	resolved, err := m.resolveAgentConfig(context.Background(), cfg, domain.ProjectConfig{})
	if err != nil {
		t.Fatalf("a fork should fall back to the default model: %v", err)
	}
	if resolved.Model != "" && resolved.Model != "claude-opus-5-5" {
		t.Fatalf("model = %q", resolved.Model)
	}
}

func TestChatTextHandoffForkStartsWithTheNote(t *testing.T) {
	launcher := &recordingLauncher{}
	m, _, _ := newChatManager(launcher)
	rec, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		RequestedMode: domain.SessionModeChat,
		Fork: &ports.SpawnFork{SourceSessionID: "mer-0", StartPoint: "abc", Handoff: func(_ context.Context, got ports.ForkTarget) (string, error) {
			if got.WorkspacePath == "" {
				t.Errorf("target = %+v, want the fork's worktree", got)
			}
			return "COPIED-CONVERSATION", nil
		}},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if len(launcher.started) != 1 {
		t.Fatalf("started = %+v", launcher.started)
	}
	started := launcher.started[0]
	if started.ForkHandoffNote != "COPIED-CONVERSATION" || started.ProviderConversationID != "" {
		t.Fatalf("started = %+v, want a fresh agent with the copied conversation", started)
	}
	if rec.Metadata.Prompt != "" {
		t.Fatalf("task = %q, the note must not be saved as the task", rec.Metadata.Prompt)
	}
}

// Restore recreates an archived fork's worktree at its start commit; its
// starting files are written back, so archiving an untouched fork loses
// nothing. An ordinary session's recreated worktree is left as it is.
func TestRestoreRecreatedForkGetsItsStartingFilesBack(t *testing.T) {
	m, st, _, _ := newManager()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(root, "workspaces")
	adapter, err := scratch.New(scratch.Options{ManagedRoot: managed})
	if err != nil {
		t.Fatal(err)
	}
	m.workspace = adapter
	seeds := &recordingForkSeeds{}
	m.forkSeeds = seeds
	project := domain.ProjectRecord{ID: "scratch", Kind: domain.ProjectKindScratch, Path: t.TempDir()}
	fork := domain.SessionRecord{ID: "scratch-1", ProjectID: "scratch", Kind: domain.KindWorker,
		Metadata: domain.SessionMetadata{WorkspacePath: filepath.Join(managed, "scratch", "workers", "scratch-1")}}
	plain := domain.SessionRecord{ID: "scratch-2", ProjectID: "scratch", Kind: domain.KindWorker,
		Metadata: domain.SessionMetadata{WorkspacePath: filepath.Join(managed, "scratch", "workers", "scratch-2")}}
	m.store = lineageStore{fakeStore: st, forks: map[domain.SessionID]domain.SessionFork{fork.ID: {ForkSessionID: fork.ID, SourceSessionID: "scratch-0"}}}

	for _, rec := range []domain.SessionRecord{fork, plain} {
		if _, err := m.restoreSessionWorkspace(context.Background(), project, rec); err != nil {
			t.Fatalf("restore %s: %v", rec.ID, err)
		}
	}
	want := fork.Metadata.WorkspacePath + "|" + forkseed.BaseRef(string(fork.ID))
	if len(seeds.restoredBase) != 1 || seeds.restoredBase[0] != want {
		t.Fatalf("starting files restored for %v, want only %q", seeds.restoredBase, want)
	}
	// A worktree that wasn't recreated keeps whatever it has.
	if _, err := m.restoreSessionWorkspace(context.Background(), project, fork); err != nil {
		t.Fatal(err)
	}
	if len(seeds.restoredBase) != 1 {
		t.Fatalf("starting files written again into an existing worktree: %v", seeds.restoredBase)
	}
}

// A fork of an older turn copies only the attachments its conversation had
// by then; a fork of the latest turn copies them all.
func TestForkCopiesOnlyTheAttachmentsItsTurnHad(t *testing.T) {
	m, _, _, _ := newManager()
	m.dataDir = t.TempDir()
	src := filepath.Join(m.dataDir, "attachments", "mer-0")
	for _, name := range []string{"image-early.png", "image-later.png"} {
		if err := os.MkdirAll(src, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.copyForkAttachments(context.Background(), &ports.SpawnFork{SourceSessionID: "mer-0", LimitAttachments: true, AttachmentNames: []string{"image-early.png", "../escape"}}, "mer-1"); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(m.dataDir, "attachments", "mer-1")
	if _, err := os.Stat(filepath.Join(dst, "image-early.png")); err != nil {
		t.Fatalf("the turn's attachment wasn't copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "image-later.png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an attachment from after the turn was copied: %v", err)
	}

	if err := m.copyForkAttachments(context.Background(), &ports.SpawnFork{SourceSessionID: "mer-0"}, "mer-2"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"image-early.png", "image-later.png"} {
		if _, err := os.Stat(filepath.Join(m.dataDir, "attachments", "mer-2", name)); err != nil {
			t.Fatalf("a latest-turn fork is missing %s: %v", name, err)
		}
	}
}

// A fork whose artifacts can't all be copied must not start with some of
// them missing: the spawn fails and leaves nothing behind.
func TestForkFailsWhenItsArtifactsCannotBeCopied(t *testing.T) {
	launcher := &recordingLauncher{}
	m, st, _ := newChatManager(launcher)
	m.dataDir = t.TempDir()
	// An older turn's saved version of report.md whose stored copy is gone,
	// so copying it fails the same way on every system.
	cutoff := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	writeTestFile(t, filepath.Join(m.artifactDir("mer-0"), "report.md"), "report now")
	m.store = artifactVersionStore{fakeStore: st, versions: map[string][]domain.SessionArtifactVersion{
		"report.md": {{Path: "report.md", Version: 1, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("missing blob"))), ModTime: cutoff.Add(-time.Minute)}},
	}}
	_, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		RequestedMode: domain.SessionModeChat,
		Fork:          &ports.SpawnFork{SourceSessionID: "mer-0", StartPoint: "abc", ArtifactsAsOf: cutoff},
	})
	if !errors.Is(err, ErrSpawnArtifactDir) {
		t.Fatalf("spawn = %v, want ErrSpawnArtifactDir", err)
	}
	if len(launcher.started) != 0 {
		t.Fatalf("started = %+v", launcher.started)
	}
	for id := range st.sessions {
		if id != "mer-0" && !st.sessions[id].IsTerminated {
			t.Fatalf("the failed fork %s was left behind", id)
		}
	}
}

// A fork of an older turn gets the artifacts that turn left, under the paths
// they had then, even after a later turn renamed one. The real version store
// moves a renamed file's history to its new path, so the fork reads the list
// kept as the next turn started.
func TestOlderTurnForkKeepsAnArtifactRenamedLater(t *testing.T) {
	m, _, _, _ := newManager()
	m.dataDir = t.TempDir()
	ctx := context.Background()
	store := sqlitetest.MustOpenAt(t, filepath.Join(m.dataDir, "db"))
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "ao", Path: m.dataDir, RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	source, err := store.CreateSession(ctx, domain.SessionRecord{ProjectID: "ao", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	src := m.artifactDir(source.ID)
	writeTestFile(t, filepath.Join(src, "page.html"), "<p>turn one</p>")
	capturer := artifactversions.New(store, m.dataDir, nil)
	track := func() {
		t.Helper()
		rows, err := store.ListSessionArtifacts(ctx, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := capturer.Track(ctx, source, rows); err != nil {
			t.Fatal(err)
		}
	}
	track()
	// Turn two starts, then renames the page.
	if err := capturer.SnapshotTurn(ctx, source, []string{"turn-2"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(src, "page.html"), filepath.Join(src, "final.html")); err != nil {
		t.Fatal(err)
	}
	track()
	m.store = store

	dst := t.TempDir()
	if err := m.copyForkArtifactsAsOf(ctx, source.ID, dst, now.Add(time.Minute), "turn-2"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "page.html")); err != nil || string(b) != "<p>turn one</p>" {
		t.Fatalf("page.html = %q, %v; want the page as turn one left it", b, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "final.html")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("final.html, named after the forked turn, was copied: %v", err)
	}
}

// The copied conversation names the original's worktree; the fork's agent is
// told which folder that is, so it works on its own copy of those files. A
// restored fork is told too.
func TestForkPromptNamesTheOriginalsWorktree(t *testing.T) {
	launcher := &recordingLauncher{}
	m, st, _ := newChatManager(launcher)
	_, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		RequestedMode: domain.SessionModeChat,
		Fork:          &ports.SpawnFork{SourceSessionID: "mer-0", SourceWorkspace: "/worktrees/mer-0", StartPoint: "abc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prompt := launcher.started[0].SystemPrompt; !strings.Contains(prompt, "/worktrees/mer-0") {
		t.Fatalf("system prompt doesn't name the original's worktree:\n%s", prompt)
	}

	st.sessions["mer-0"] = domain.SessionRecord{ID: "mer-0", ProjectID: "mer", Metadata: domain.SessionMetadata{WorkspacePath: "/worktrees/mer-0"}}
	m.store = lineageStore{fakeStore: st, forks: map[domain.SessionID]domain.SessionFork{"mer-5": {ForkSessionID: "mer-5", SourceSessionID: "mer-0"}}}
	restored, err := m.buildSystemPrompt(context.Background(), domain.KindWorker, "mer", "mer-5", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(restored, "/worktrees/mer-0") {
		t.Fatalf("restored fork's prompt doesn't name the original's worktree:\n%s", restored)
	}
}
