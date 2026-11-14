package sessionfork

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeSessions map[domain.SessionID]domain.SessionRecord

func (f fakeSessions) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	r, ok := f[id]
	return r, ok, nil
}

type fakeProjects map[string]domain.ProjectRecord

func (f fakeProjects) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	p, ok := f[id]
	return p, ok, nil
}

type fakeLineage struct{ rows []domain.SessionFork }

func (f *fakeLineage) RecordSessionFork(_ context.Context, fork domain.SessionFork) error {
	f.rows = append(f.rows, fork)
	return nil
}
func (f *fakeLineage) GetSessionFork(_ context.Context, id domain.SessionID) (domain.SessionFork, bool, error) {
	for _, r := range f.rows {
		if r.ForkSessionID == id {
			return r, true, nil
		}
	}
	return domain.SessionFork{}, false, nil
}
func (f *fakeLineage) NextSessionForkNumber(_ context.Context, root domain.SessionID) (int, error) {
	n := 0
	for _, r := range f.rows {
		if r.RootSessionID == root && r.ForkNumber > n {
			n = r.ForkNumber
		}
	}
	return n + 1, nil
}

type fakeSpawner struct {
	got   []ports.SpawnConfig
	byReq map[string]domain.SessionRecord
	next  int
}

func (f *fakeSpawner) Spawn(_ context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, int, int, error) {
	if r, ok := f.byReq[cfg.ClientRequestID]; ok {
		return r, 0, 0, nil
	}
	f.got = append(f.got, cfg)
	f.next++
	rec := domain.SessionRecord{ID: domain.SessionID("p-9" + string(rune('0'+f.next))), DisplayName: cfg.DisplayName, ProjectID: cfg.ProjectID,
		ClientRequestHash: cfg.ClientRequestHash, ClientRequestCommitted: true}
	if f.byReq == nil {
		f.byReq = map[string]domain.SessionRecord{}
	}
	f.byReq[cfg.ClientRequestID] = rec
	return rec, 0, 0, nil
}

type fakeSources struct {
	latestTurn string
	refusal    string
	// olderFiles is the snapshot an older-turn fork copies its files from.
	olderFiles string
	anyTurn    bool
	gotTurn    *string
}

func (f fakeSources) ForkSource(_ context.Context, rec domain.SessionRecord, turnID string) (Source, error) {
	if f.gotTurn != nil {
		*f.gotTurn = turnID
	}
	src := Source{LatestTurnID: f.latestTurn, Refusal: f.refusal, ContextMode: domain.ForkContextNativeCurrent, AnyTurn: f.anyTurn}
	if turnID != "" && turnID != f.latestTurn {
		src.LatestTurnID, src.FilesSnapshotRef = turnID, f.olderFiles
	}
	return src, nil
}

type fakeSeeds struct {
	err       error
	deleted   *[]string
	snapshots map[string]string
}

func (f fakeSeeds) Capture(_ context.Context, _ string, _ string) (string, error) {
	return "abc123", f.err
}
func (f fakeSeeds) Delete(_ context.Context, _ string, ref string) error {
	if f.deleted != nil {
		*f.deleted = append(*f.deleted, ref)
	}
	return nil
}
func (f fakeSeeds) SnapshotHead(_ context.Context, _ string, ref string) (string, error) {
	head, ok := f.snapshots[ref]
	if !ok {
		return "", ErrSnapshotMissing
	}
	return head, nil
}

func source() domain.SessionRecord {
	return domain.SessionRecord{
		ID: "p-1", ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
		DisplayName: "Retry failed webhook deliveries",
		Activity:    domain.Activity{State: domain.ActivityIdle},
		Metadata:    domain.SessionMetadata{WorkspacePath: "/tmp/w1", Model: "gpt-6.1", Effort: "high", Permissions: domain.PermissionMode("ask")},
	}
}

func newService(rec domain.SessionRecord, opts ...func(*Service)) (*Service, *fakeSpawner, *fakeLineage) {
	sp, ln := &fakeSpawner{}, &fakeLineage{}
	s := New(Deps{
		Enabled:  true,
		Sessions: fakeSessions{rec.ID: rec},
		Projects: fakeProjects{"p": {ID: "p", Kind: domain.ProjectKindSingleRepo}},
		Lineage:  ln, Spawner: sp, Sources: fakeSources{latestTurn: "turn-4"}, Seeds: fakeSeeds{},
		Now: func() time.Time { return time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC) },
	})
	for _, o := range opts {
		o(s)
	}
	return s, sp, ln
}

func refusalCode(t *testing.T, err error) string {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) {
		t.Fatalf("want a refusal, got %v", err)
	}
	return r.Code
}

func TestForkDisabledWithoutFlag(t *testing.T) {
	s, _, _ := newService(source())
	s.deps.Enabled = false
	if _, err := s.Fork(context.Background(), "p-1", "req-1"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("err=%v", err)
	}
}

func TestOptionsRefusals(t *testing.T) {
	cases := map[string]func(*domain.SessionRecord, *Service){
		CodeOrchestrator:     func(r *domain.SessionRecord, _ *Service) { r.Kind = domain.KindOrchestrator },
		CodeSourceTerminated: func(r *domain.SessionRecord, _ *Service) { r.IsTerminated = true },
		CodeSourceBusy:       func(r *domain.SessionRecord, _ *Service) { r.Activity.State = domain.ActivityActive },
		CodeWorkspaceProject: func(_ *domain.SessionRecord, s *Service) {
			s.deps.Projects = fakeProjects{"p": {ID: "p", Kind: domain.ProjectKindWorkspace}}
		},
		CodeNotGit:           func(_ *domain.SessionRecord, s *Service) { s.deps.Seeds = fakeSeeds{err: ErrNotRepository} },
		CodeAgentUnsupported: func(_ *domain.SessionRecord, s *Service) { s.deps.Sources = fakeSources{refusal: CodeAgentUnsupported} },
		CodeNativeMissing:    func(_ *domain.SessionRecord, s *Service) { s.deps.Sources = fakeSources{refusal: CodeNativeMissing} },
	}
	for code, mutate := range cases {
		t.Run(code, func(t *testing.T) {
			rec := source()
			s, _, _ := newService(rec)
			mutate(&rec, s)
			s.deps.Sessions = fakeSessions{rec.ID: rec}
			_, err := s.Fork(context.Background(), "p-1", "req-"+code)
			if got := refusalCode(t, err); got != code {
				t.Fatalf("code=%s want %s", got, code)
			}
			opts, err := s.Options(context.Background(), "p-1")
			if err != nil || opts.Allowed || opts.Refusal == nil || opts.Refusal.Code != code {
				t.Fatalf("options=%+v err=%v", opts, err)
			}
		})
	}
}

func TestForkDisplayName(t *testing.T) {
	if got := ForkDisplayName(1, "X"); got != "Fork of X" {
		t.Fatal(got)
	}
	if got := ForkDisplayName(2, "X"); got != "Fork 2 of X" {
		t.Fatal(got)
	}
	for in, want := range map[string]string{"Fork of X": "X", "Fork 12 of X": "X", "Forked deploy": "Forked deploy", "X": "X"} {
		if got := RootTitle(in); got != want {
			t.Fatalf("RootTitle(%q)=%q want %q", in, got, want)
		}
	}
}

func TestForkCopiesAgentSettings(t *testing.T) {
	s, sp, ln := newService(source())
	rec, err := s.Fork(context.Background(), "p-1", "req-1")
	if err != nil {
		t.Fatal(err)
	}
	cfg := sp.got[0]
	if cfg.Harness != domain.HarnessCodex || cfg.RequestedMode != domain.SessionModeChat || cfg.Kind != domain.KindWorker || cfg.ProjectID != "p" {
		t.Fatalf("cfg=%+v", cfg)
	}
	if cfg.AgentConfig.Model != "gpt-6.1" || cfg.AgentConfig.Effort != "high" || cfg.AgentConfig.Permissions != "ask" {
		t.Fatalf("agent config=%+v", cfg.AgentConfig)
	}
	if cfg.DisplayName != "Fork of Retry failed webhook deliveries" || !cfg.KeepDisplayName || cfg.NameFromPrompt {
		t.Fatalf("naming=%q keep=%v fromPrompt=%v", cfg.DisplayName, cfg.KeepDisplayName, cfg.NameFromPrompt)
	}
	if cfg.Fork == nil || cfg.Fork.SourceSessionID != "p-1" || cfg.Fork.StartPoint != "abc123" || cfg.Fork.SeedRef == "" {
		t.Fatalf("fork descriptor=%+v", cfg.Fork)
	}
	if len(ln.rows) != 1 || ln.rows[0].ForkSessionID != rec.ID || ln.rows[0].SourceTurnID != "turn-4" || ln.rows[0].ForkNumber != 1 {
		t.Fatalf("lineage=%+v", ln.rows)
	}
}

func TestForkHasNoIssueOrAutomationLink(t *testing.T) {
	src := source()
	src.IssueID = "42"
	run := domain.AutomationRunID("run-1")
	src.AutomationRunID = &run
	s, sp, _ := newService(src)
	if _, err := s.Fork(context.Background(), "p-1", "req-1"); err != nil {
		t.Fatal(err)
	}
	if sp.got[0].IssueID != "" || sp.got[0].AutomationRunID != nil {
		t.Fatalf("issue=%q automation=%v", sp.got[0].IssueID, sp.got[0].AutomationRunID)
	}
}

func TestForkSameClientRequestReturnsSameSession(t *testing.T) {
	s, sp, ln := newService(source())
	a, err := s.Fork(context.Background(), "p-1", "req-1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Fork(context.Background(), "p-1", "req-1")
	if err != nil || a.ID != b.ID || len(sp.got) != 1 || len(ln.rows) != 1 {
		t.Fatalf("a=%s b=%s spawns=%d lineage=%d err=%v", a.ID, b.ID, len(sp.got), len(ln.rows), err)
	}
}

func TestForkOfForkCountsAgainstRoot(t *testing.T) {
	fork := source()
	fork.ID, fork.DisplayName = "p-2", "Fork of Retry failed webhook deliveries"
	s, sp, ln := newService(fork)
	ln.rows = []domain.SessionFork{{ForkSessionID: "p-2", SourceSessionID: "p-1", RootSessionID: "p-1", ForkNumber: 1}}
	if _, err := s.Fork(context.Background(), "p-2", "req-1"); err != nil {
		t.Fatal(err)
	}
	if sp.got[0].DisplayName != "Fork 2 of Retry failed webhook deliveries" {
		t.Fatal(sp.got[0].DisplayName)
	}
	last := ln.rows[len(ln.rows)-1]
	if last.SourceSessionID != "p-2" || last.RootSessionID != "p-1" {
		t.Fatalf("lineage=%+v", last)
	}
}

func TestOptionsReportsWhereAForkCameFrom(t *testing.T) {
	fork := source()
	fork.ID, fork.DisplayName = "p-9", "Fork of Retry failed webhook deliveries"
	s, _, ln := newService(fork)
	ln.rows = append(ln.rows, domain.SessionFork{ForkSessionID: "p-9", SourceSessionID: "p-1", RootSessionID: "p-1", SourceTitle: "Retry failed webhook deliveries", SourceTurnID: "turn-4"})
	opts, err := s.Options(context.Background(), "p-9")
	if err != nil {
		t.Fatal(err)
	}
	if opts.ForkedFrom == nil || opts.ForkedFrom.SessionID != "p-1" || opts.ForkedFrom.Title != "Retry failed webhook deliveries" || opts.ForkedFrom.SourceExists {
		t.Fatalf("forkedFrom = %+v", opts.ForkedFrom)
	}
	s.deps.Sessions = fakeSessions{"p-9": fork, "p-1": source()}
	opts, _ = s.Options(context.Background(), "p-9")
	if !opts.ForkedFrom.SourceExists {
		t.Fatal("the original exists")
	}
	if plain, _ := s.Options(context.Background(), "p-1"); plain.ForkedFrom != nil {
		t.Fatalf("a normal session has no origin: %+v", plain.ForkedFrom)
	}
}

func TestOptionsMarksAForkGivenItsConversationAsText(t *testing.T) {
	fork := source()
	fork.ID = "p-9"
	s, _, ln := newService(fork)
	ln.rows = append(ln.rows, domain.SessionFork{ForkSessionID: "p-9", SourceSessionID: "p-1", RootSessionID: "p-1", ContextMode: domain.ForkContextTextHandoff})
	opts, err := s.Options(context.Background(), "p-9")
	if err != nil {
		t.Fatal(err)
	}
	if opts.ForkedFrom == nil || !opts.ForkedFrom.TextHandoff {
		t.Fatalf("forkedFrom = %+v, want it marked as a text copy", opts.ForkedFrom)
	}
	ln.rows[len(ln.rows)-1].ContextMode = domain.ForkContextNativeCurrent
	if opts, _ := s.Options(context.Background(), "p-9"); opts.ForkedFrom.TextHandoff {
		t.Fatal("a native fork must not be marked as a text copy")
	}
}

func TestForkSendsAStableRequestHash(t *testing.T) {
	s, sp, _ := newService(source())
	if _, err := s.Fork(context.Background(), "p-1", "req-1"); err != nil {
		t.Fatal(err)
	}
	if len(sp.got) != 1 || sp.got[0].ClientRequestHash == "" {
		t.Fatalf("a client request id needs a request hash: %+v", sp.got)
	}
	if forkRequestHash("p-1") != sp.got[0].ClientRequestHash || forkRequestHash("p-2") == sp.got[0].ClientRequestHash {
		t.Fatal("the hash must identify the source")
	}
}

func TestRetriedForkDeletesItsUnusedSnapshot(t *testing.T) {
	s, sp, _ := newService(source())
	var deleted []string
	s.deps.Seeds = fakeSeeds{deleted: &deleted}
	if _, err := s.Fork(context.Background(), "p-1", "req-1"); err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 0 {
		t.Fatalf("the first fork's snapshot belongs to its spawn: %v", deleted)
	}
	if _, err := s.Fork(context.Background(), "p-1", "req-1"); err != nil {
		t.Fatal(err)
	}
	if len(sp.got) != 1 || len(deleted) != 1 {
		t.Fatalf("spawns = %d, deleted = %v", len(sp.got), deleted)
	}
}

type fakeAccounts map[domain.SessionID]string

func (f fakeAccounts) SessionAccountID(_ context.Context, id domain.SessionID, _ domain.AgentHarness) (string, error) {
	return f[id], nil
}

func TestForkKeepsTheSourcesAccount(t *testing.T) {
	// The agent's own copy of the conversation lives in the account's store,
	// so the fork must run under the same account to find it.
	s, sp, _ := newService(source())
	s.deps.Accounts = fakeAccounts{"p-1": "work"}
	if _, err := s.Fork(context.Background(), "p-1", "req-1"); err != nil {
		t.Fatal(err)
	}
	if sp.got[0].AgentAccountID != "work" {
		t.Fatalf("account = %q", sp.got[0].AgentAccountID)
	}
}

func TestSessionStillSettingUpIsRefused(t *testing.T) {
	// A fresh fork has no worktree until its setup finishes; say so rather
	// than reporting that it isn't a git repository.
	rec := source()
	rec.ProvisionState = domain.SessionProvisionState("provisioning")
	rec.Metadata.WorkspacePath = ""
	s, _, _ := newService(rec)
	opts, err := s.Options(context.Background(), "p-1")
	if err != nil {
		t.Fatal(err)
	}
	if opts.Refusal == nil || opts.Refusal.Code != CodeSourcePreparing {
		t.Fatalf("refusal = %+v", opts.Refusal)
	}
}

func TestRetryReforksFromTheOriginal(t *testing.T) {
	fork := source()
	fork.ID, fork.DisplayName = "p-9", "Fork of Retry failed webhook deliveries"
	s, _, ln := newService(source())
	s.deps.Sessions = fakeSessions{"p-1": source(), "p-9": fork}
	ln.rows = append(ln.rows, domain.SessionFork{ForkSessionID: "p-9", SourceSessionID: "p-1", RootSessionID: "p-1", SourceTitle: "Retry failed webhook deliveries"})
	spawnFork, err := s.RetrySpawnFork(context.Background(), fork)
	if err != nil {
		t.Fatal(err)
	}
	if spawnFork.SourceSessionID != "p-1" || spawnFork.StartPoint != "abc123" || spawnFork.SeedRef == "" || spawnFork.SourceTitle != "Retry failed webhook deliveries" {
		t.Fatalf("fork = %+v", spawnFork)
	}
	s.deps.Sessions = fakeSessions{"p-9": fork}
	if _, err := s.RetrySpawnFork(context.Background(), fork); err == nil {
		t.Fatal("a fork whose original is gone can't be forked again")
	}
}

func (f *fakeLineage) ListSessionForks(_ context.Context, source domain.SessionID) ([]domain.SessionFork, error) {
	var out []domain.SessionFork
	for _, r := range f.rows {
		if r.SourceSessionID == source {
			out = append(out, r)
		}
	}
	return out, nil
}

type fakePRs map[domain.SessionID][]domain.PRFacts

func (f fakePRs) ListPRFactsForSession(_ context.Context, id domain.SessionID) ([]domain.PRFacts, error) {
	return f[id], nil
}

func TestOptionsLinkForksAndOriginals(t *testing.T) {
	fork := source()
	fork.ID, fork.DisplayName = "p-9", "Fork of Retry failed webhook deliveries"
	s, _, ln := newService(source())
	s.deps.Sessions = fakeSessions{"p-1": source(), "p-9": fork}
	s.deps.PRs = fakePRs{"p-1": {{URL: "https://github.com/a/b/pull/7", Number: 7}}}
	ln.rows = append(ln.rows, domain.SessionFork{ForkSessionID: "p-9", SourceSessionID: "p-1", RootSessionID: "p-1", SourceTitle: "Retry failed webhook deliveries", ForkNumber: 1})

	original, err := s.Options(context.Background(), "p-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(original.Forks) != 1 || original.Forks[0].SessionID != "p-9" || original.Forks[0].Title != "Fork of Retry failed webhook deliveries" || !original.Forks[0].Exists {
		t.Fatalf("forks = %+v", original.Forks)
	}
	if len(original.SourcePRs) != 0 {
		t.Fatal("only a fork lists its original's PRs")
	}
	forked, err := s.Options(context.Background(), "p-9")
	if err != nil {
		t.Fatal(err)
	}
	if len(forked.SourcePRs) != 1 || forked.SourcePRs[0].Number != 7 || forked.SourcePRs[0].URL == "" {
		t.Fatalf("source PRs = %+v", forked.SourcePRs)
	}
}

func TestForkAtAnOlderTurnUsesThatTurnsFiles(t *testing.T) {
	s, sp, ln := newService(source())
	var gotTurn string
	s.deps.Sources = fakeSources{latestTurn: "turn-4", olderFiles: "refs/ao/snapshots/p-1/turn-3", anyTurn: true, gotTurn: &gotTurn}
	var deleted []string
	s.deps.Seeds = fakeSeeds{deleted: &deleted, snapshots: map[string]string{"refs/ao/snapshots/p-1/turn-3": "older-head"}}
	if _, err := s.ForkTurn(context.Background(), "p-1", "req-1", "turn-2"); err != nil {
		t.Fatal(err)
	}
	fork := sp.got[0].Fork
	if gotTurn != "turn-2" || fork.StartPoint != "older-head" || fork.SeedRef != "refs/ao/snapshots/p-1/turn-3" || !fork.KeepSeedRef {
		t.Fatalf("turn=%q fork=%+v", gotTurn, fork)
	}
	if ln.rows[0].SourceTurnID != "turn-2" {
		t.Fatalf("lineage turn = %q", ln.rows[0].SourceTurnID)
	}
	if len(deleted) != 0 {
		t.Fatalf("a rollback snapshot must never be deleted: %v", deleted)
	}
}

func TestOlderTurnWithoutSavedFilesIsRefused(t *testing.T) {
	s, _, _ := newService(source())
	s.deps.Sources = fakeSources{latestTurn: "turn-4", olderFiles: "refs/ao/snapshots/p-1/turn-3", anyTurn: true}
	s.deps.Seeds = fakeSeeds{}
	_, err := s.ForkTurn(context.Background(), "p-1", "req-1", "turn-2")
	if refusalCode(t, err) != CodeTurnFilesMissing {
		t.Fatalf("err = %v", err)
	}
}

func TestOptionsSayWhetherOlderTurnsFork(t *testing.T) {
	s, _, _ := newService(source())
	s.deps.Sources = fakeSources{latestTurn: "turn-4", anyTurn: true}
	opts, err := s.Options(context.Background(), "p-1")
	if err != nil || !opts.AnyTurn {
		t.Fatalf("opts = %+v, %v", opts, err)
	}
}

func TestForkedFromShowsTheOriginalsCurrentName(t *testing.T) {
	fork := source()
	fork.ID, fork.DisplayName = "p-9", "Fork of Retry failed webhook deliveries"
	renamed := source()
	renamed.DisplayName = "Webhook retries"
	s, _, ln := newService(fork)
	s.deps.Sessions = fakeSessions{"p-9": fork, "p-1": renamed}
	ln.rows = append(ln.rows, domain.SessionFork{ForkSessionID: "p-9", SourceSessionID: "p-1", RootSessionID: "p-1", SourceTitle: "Retry failed webhook deliveries"})
	opts, err := s.Options(context.Background(), "p-9")
	if err != nil {
		t.Fatal(err)
	}
	if opts.ForkedFrom.Title != "Webhook retries" {
		t.Fatalf("title = %q, want the original's current name", opts.ForkedFrom.Title)
	}
	// Once the original is deleted, the name it had when forked is kept.
	s.deps.Sessions = fakeSessions{"p-9": fork}
	opts, _ = s.Options(context.Background(), "p-9")
	if opts.ForkedFrom.Title != "Retry failed webhook deliveries" {
		t.Fatalf("title = %q, want the stored name", opts.ForkedFrom.Title)
	}
}
