package sessionfork

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// gitSeedsUnused fails any test that reaches for git on a plain folder.
type gitSeedsUnused struct{ t *testing.T }

func (g gitSeedsUnused) Capture(context.Context, string, string) (string, error) {
	g.t.Error("git seed Capture called for a plain folder")
	return "", ErrNotRepository
}
func (g gitSeedsUnused) Delete(context.Context, string, string) error {
	g.t.Error("git seed Delete called for a plain folder")
	return nil
}
func (g gitSeedsUnused) SnapshotHead(context.Context, string, string) (string, error) {
	g.t.Error("git SnapshotHead called for a plain folder")
	return "", ErrSnapshotMissing
}

type fakeDirSeeds struct {
	unmanaged bool
	captured  *[]string
	deleted   *[]string
	// onCapture runs as the folder is copied.
	onCapture func()
	err       error
}

func (f fakeDirSeeds) Manages(string) bool { return !f.unmanaged }
func (f fakeDirSeeds) Capture(_ context.Context, workspace string) (string, error) {
	if f.onCapture != nil {
		f.onCapture()
	}
	if f.err != nil {
		return "", f.err
	}
	seed := "/data/fork-seeds/seed-" + workspace
	if f.captured != nil {
		*f.captured = append(*f.captured, seed)
	}
	return seed, nil
}
func (f fakeDirSeeds) Delete(_ context.Context, seed string) error {
	if f.deleted != nil {
		*f.deleted = append(*f.deleted, seed)
	}
	return nil
}

func standaloneSource() domain.SessionRecord {
	rec := source()
	rec.ProjectID = ""
	rec.Harness = domain.HarnessClaudeCode
	rec.Metadata.WorkspacePath = "/data/worktrees/standalone/sessions/standalone-1"
	return rec
}

func newStandaloneService(t *testing.T, rec domain.SessionRecord, dirs fakeDirSeeds, opts ...func(*Service)) (*Service, *fakeSpawner, *fakeLineage) {
	t.Helper()
	all := append([]func(*Service){func(s *Service) {
		s.deps.Seeds = gitSeedsUnused{t: t}
		s.deps.DirSeeds = dirs
		s.deps.Sources = fakeSources{latestTurn: "turn-4", anyTurn: true, olderFiles: "refs/ao/snap"}
	}}, opts...)
	return newService(rec, all...)
}

func TestStandaloneOptionsAllowedWithoutGit(t *testing.T) {
	s, _, _ := newStandaloneService(t, standaloneSource(), fakeDirSeeds{})
	opts, err := s.Options(context.Background(), "p-1")
	if err != nil {
		t.Fatal(err)
	}
	if !opts.Allowed || opts.Refusal != nil {
		t.Fatalf("options = %+v, want allowed", opts)
	}
	if opts.AnyTurn {
		t.Fatal("a standalone agent offered forks of older turns")
	}
	if opts.SuggestedName != "Fork of Retry failed webhook deliveries" {
		t.Fatalf("suggested name = %q", opts.SuggestedName)
	}
}

func TestStandaloneForkCapturesDirectorySeed(t *testing.T) {
	var captured []string
	s, sp, ln := newStandaloneService(t, standaloneSource(), fakeDirSeeds{captured: &captured})
	if _, err := s.Fork(context.Background(), "p-1", "req-1"); err != nil {
		t.Fatal(err)
	}
	if len(sp.got) != 1 {
		t.Fatalf("spawned %d forks, want 1", len(sp.got))
	}
	cfg := sp.got[0]
	if cfg.ProjectID != "" {
		t.Fatalf("fork project = %q, want standalone", cfg.ProjectID)
	}
	if cfg.Fork == nil || len(captured) != 1 || cfg.Fork.SeedDir != captured[0] {
		t.Fatalf("fork = %+v, captured %v", cfg.Fork, captured)
	}
	if cfg.Fork.SeedRef != "" || cfg.Fork.StartPoint != "" || cfg.Fork.KeepSeedRef {
		t.Fatalf("plain-folder fork carries git fields: %+v", cfg.Fork)
	}
	if cfg.Fork.SourceWorkspace != standaloneSource().Metadata.WorkspacePath {
		t.Fatalf("source workspace = %q", cfg.Fork.SourceWorkspace)
	}
	if len(ln.rows) != 1 || ln.rows[0].SourceTurnID != "turn-4" || ln.rows[0].ContextMode != domain.ForkContextNativeCurrent {
		t.Fatalf("lineage = %+v", ln.rows)
	}
}

func TestStandaloneForkOfLatestTurnByIDIsAllowed(t *testing.T) {
	var captured []string
	s, sp, _ := newStandaloneService(t, standaloneSource(), fakeDirSeeds{captured: &captured})
	if _, err := s.ForkTurn(context.Background(), "p-1", "req-1", "turn-4"); err != nil {
		t.Fatal(err)
	}
	if len(sp.got) != 1 || len(captured) != 1 {
		t.Fatalf("spawned %d, captured %v", len(sp.got), captured)
	}
}

func TestStandaloneForkRefusesOlderTurn(t *testing.T) {
	var captured []string
	s, sp, _ := newStandaloneService(t, standaloneSource(), fakeDirSeeds{captured: &captured})
	_, err := s.ForkTurn(context.Background(), "p-1", "req-1", "turn-2")
	if code := refusalCode(t, err); code != CodeStandaloneOlderTurn {
		t.Fatalf("refusal = %q, want %q", code, CodeStandaloneOlderTurn)
	}
	if len(sp.got) != 0 || len(captured) != 0 {
		t.Fatalf("spawned %d, captured %v; want neither", len(sp.got), captured)
	}
}

func TestStandaloneForkRefusesUnmanagedFolder(t *testing.T) {
	var captured []string
	s, sp, _ := newStandaloneService(t, standaloneSource(), fakeDirSeeds{unmanaged: true, captured: &captured})
	_, err := s.Fork(context.Background(), "p-1", "req-1")
	if code := refusalCode(t, err); code != CodeWorkspaceUnmanaged {
		t.Fatalf("refusal = %q, want %q", code, CodeWorkspaceUnmanaged)
	}
	if len(sp.got) != 0 || len(captured) != 0 {
		t.Fatalf("spawned %d, captured %v; want neither", len(sp.got), captured)
	}
	opts, err := s.Options(context.Background(), "p-1")
	if err != nil || opts.Allowed || opts.Refusal == nil || opts.Refusal.Code != CodeWorkspaceUnmanaged {
		t.Fatalf("options = %+v, err = %v", opts, err)
	}
}

// Without the folder copier wired, a plain folder is refused as before.
func TestStandaloneWithoutDirSeedsStillRefused(t *testing.T) {
	s, _, _ := newService(standaloneSource(), func(s *Service) { s.deps.Seeds = fakeSeeds{err: ErrNotRepository} })
	_, err := s.Fork(context.Background(), "p-1", "req-1")
	if code := refusalCode(t, err); code != CodeNotGit {
		t.Fatalf("refusal = %q, want %q", code, CodeNotGit)
	}
}

type failingSpawner struct{}

func (failingSpawner) Spawn(context.Context, ports.SpawnConfig) (domain.SessionRecord, int, int, error) {
	return domain.SessionRecord{}, 0, 0, errors.New("boom")
}

func TestStandaloneSeedDroppedWhenSpawnFails(t *testing.T) {
	var captured, deleted []string
	s, _, _ := newStandaloneService(t, standaloneSource(), fakeDirSeeds{captured: &captured, deleted: &deleted},
		func(s *Service) { s.deps.Spawner = failingSpawner{} })
	if _, err := s.Fork(context.Background(), "p-1", "req-1"); err == nil {
		t.Fatal("fork succeeded with a failing spawn")
	}
	if len(deleted) != 1 || deleted[0] != captured[0] {
		t.Fatalf("deleted %v, want the captured seed %v", deleted, captured)
	}
}

func TestStandaloneCaptureFailureIsAnError(t *testing.T) {
	s, sp, _ := newStandaloneService(t, standaloneSource(), fakeDirSeeds{err: errors.New("disk full")})
	_, err := s.Fork(context.Background(), "p-1", "req-1")
	var refusal *Refusal
	if err == nil || errors.As(err, &refusal) {
		t.Fatalf("err = %v, want a plain error", err)
	}
	if len(sp.got) != 0 {
		t.Fatalf("spawned %d forks, want none", len(sp.got))
	}
}

func TestStandaloneForkRefusedWhenSourceActedDuringCapture(t *testing.T) {
	froze := 0
	var deleted []string
	rec := standaloneSource()
	sessions := fakeSessions{rec.ID: rec}
	dirs := fakeDirSeeds{deleted: &deleted, onCapture: func() {
		now := sessions[rec.ID]
		now.Activity.LastActivityAt = now.Activity.LastActivityAt.Add(1)
		sessions[rec.ID] = now
	}}
	s, sp, _ := newStandaloneService(t, rec, dirs, func(s *Service) {
		s.deps.Sessions = sessions
		s.deps.Sources = freezingSources{froze: &froze}
	})
	_, err := s.Fork(context.Background(), "p-1", "req-1")
	if code := refusalCode(t, err); code != CodeSourceBusy {
		t.Fatalf("refusal = %q, want %q", code, CodeSourceBusy)
	}
	if froze != 1 {
		t.Fatalf("froze %d times, want 1", froze)
	}
	if len(sp.got) != 0 || len(deleted) != 1 {
		t.Fatalf("spawned %d, deleted %v; want no fork and the seed deleted", len(sp.got), deleted)
	}
}

// A legacy Scratch project's session is a plain folder too.
func TestScratchProjectForkUsesDirectorySeed(t *testing.T) {
	var captured []string
	rec := source()
	s, sp, _ := newStandaloneService(t, rec, fakeDirSeeds{captured: &captured}, func(s *Service) {
		s.deps.Projects = fakeProjects{"p": {ID: "p", Kind: domain.ProjectKindScratch}}
	})
	if _, err := s.Fork(context.Background(), "p-1", "req-1"); err != nil {
		t.Fatal(err)
	}
	if len(sp.got) != 1 || sp.got[0].Fork.SeedDir == "" || sp.got[0].ProjectID != "p" {
		t.Fatalf("spawned %+v", sp.got)
	}
}

func TestProjectForkStillUsesGitSeeds(t *testing.T) {
	var captured []string
	s, sp, _ := newService(source(), func(s *Service) { s.deps.DirSeeds = fakeDirSeeds{captured: &captured} })
	if _, err := s.Fork(context.Background(), "p-1", "req-1"); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 0 {
		t.Fatalf("a git project's fork copied its folder: %v", captured)
	}
	if fork := sp.got[0].Fork; fork.SeedRef == "" || fork.StartPoint != "abc123" || fork.SeedDir != "" {
		t.Fatalf("fork = %+v, want a git seed", fork)
	}
}
