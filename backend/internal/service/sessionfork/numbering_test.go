package sessionfork

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// lockedLineage is fakeLineage safe for concurrent forks.
type lockedLineage struct {
	mu sync.Mutex
	fakeLineage
}

func (l *lockedLineage) RecordSessionFork(ctx context.Context, fork domain.SessionFork) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fakeLineage.RecordSessionFork(ctx, fork)
}
func (l *lockedLineage) GetSessionFork(ctx context.Context, id domain.SessionID) (domain.SessionFork, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fakeLineage.GetSessionFork(ctx, id)
}
func (l *lockedLineage) NextSessionForkNumber(ctx context.Context, root domain.SessionID) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fakeLineage.NextSessionForkNumber(ctx, root)
}
func (l *lockedLineage) ListSessionForks(ctx context.Context, id domain.SessionID) ([]domain.SessionFork, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fakeLineage.ListSessionForks(ctx, id)
}

// gatedSpawner holds every spawn until all of them have started, the way two
// forks overlap while each one's worktree is being set up.
type gatedSpawner struct {
	mu      sync.Mutex
	names   []string
	started sync.WaitGroup
}

func (g *gatedSpawner) Spawn(_ context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, int, int, error) {
	g.mu.Lock()
	g.names = append(g.names, cfg.DisplayName)
	n := len(g.names)
	g.mu.Unlock()
	g.started.Done()
	g.started.Wait()
	return domain.SessionRecord{ID: domain.SessionID(fmt.Sprintf("p-f%d", n)), DisplayName: cfg.DisplayName}, 0, 0, nil
}

// Forks of one session started at the same time each get their own number.
func TestForksStartedTogetherGetTheirOwnNumbers(t *testing.T) {
	spawner := &gatedSpawner{}
	spawner.started.Add(2)
	lineage := &lockedLineage{}
	s := New(Deps{
		Enabled:  true,
		Sessions: fakeSessions{source().ID: source()},
		Projects: fakeProjects{"p": {ID: "p", Kind: domain.ProjectKindSingleRepo}},
		Lineage:  lineage, Spawner: spawner, Sources: fakeSources{latestTurn: "turn-4"}, Seeds: fakeSeeds{},
	})
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Fork(context.Background(), "p-1", fmt.Sprintf("req-%d", i)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(spawner.names) != 2 || spawner.names[0] == spawner.names[1] {
		t.Fatalf("names = %q, want two different names", spawner.names)
	}
	numbers := map[int]bool{}
	for _, row := range lineage.rows {
		numbers[row.ForkNumber] = true
	}
	if len(numbers) != 2 {
		t.Fatalf("recorded numbers = %v, want two different ones", lineage.rows)
	}
	// The next fork follows both.
	opts, err := s.Options(context.Background(), "p-1")
	if err != nil || opts.SuggestedName != "Fork 3 of Retry failed webhook deliveries" {
		t.Fatalf("next name = %q, %v", opts.SuggestedName, err)
	}
}

type cancellingSpawner struct {
	fakeSpawner
	cancel context.CancelFunc
}

func (c *cancellingSpawner) Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, int, int, error) {
	rec, a, b, err := c.fakeSpawner.Spawn(ctx, cfg)
	c.cancel() // the caller goes away just as the session is created
	return rec, a, b, err
}

type ctxCheckingLineage struct{ fakeLineage }

func (l *ctxCheckingLineage) RecordSessionFork(ctx context.Context, fork domain.SessionFork) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return l.fakeLineage.RecordSessionFork(ctx, fork)
}

// A fork whose request is cancelled once its session exists still records
// where it came from, so retry and cleanup can find its original.
func TestForkLineageIsRecordedEvenIfTheRequestIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	spawner := &cancellingSpawner{cancel: cancel}
	lineage := &ctxCheckingLineage{}
	s := New(Deps{
		Enabled:  true,
		Sessions: fakeSessions{source().ID: source()},
		Projects: fakeProjects{"p": {ID: "p", Kind: domain.ProjectKindSingleRepo}},
		Lineage:  lineage, Spawner: spawner, Sources: fakeSources{latestTurn: "turn-4"}, Seeds: fakeSeeds{},
	})
	if _, err := s.Fork(ctx, "p-1", "req-1"); err != nil {
		t.Fatal(err)
	}
	if len(lineage.rows) != 1 {
		t.Fatalf("lineage rows = %v, want the fork recorded", lineage.rows)
	}
}

type freezingSources struct{ froze *int }

func (f freezingSources) ForkSource(context.Context, domain.SessionRecord, string) (Source, error) {
	return Source{LatestTurnID: "turn-4", ContextMode: domain.ForkContextTextHandoff, Freeze: func(context.Context) { *f.froze++ }}, nil
}

// A source fixes its conversation where the fork's files are captured, so
// the two match; checking whether it can fork doesn't capture anything.
func TestForkFreezesTheSourceOnlyWhenForking(t *testing.T) {
	froze := 0
	s, _, _ := newService(source(), func(s *Service) { s.deps.Sources = freezingSources{froze: &froze} })
	if _, err := s.Options(context.Background(), "p-1"); err != nil {
		t.Fatal(err)
	}
	if froze != 0 {
		t.Fatalf("options froze the source %d times", froze)
	}
	if _, err := s.Fork(context.Background(), "p-1", "req-1"); err != nil {
		t.Fatal(err)
	}
	if froze != 1 {
		t.Fatalf("fork froze the source %d times, want once", froze)
	}
}

// activeDuringCapture is a seed store whose capture runs while the source
// takes a new command.
type activeDuringCapture struct {
	fakeSeeds
	sessions fakeSessions
}

func (a activeDuringCapture) Capture(ctx context.Context, workspace, ref string) (string, error) {
	rec := a.sessions["p-1"]
	rec.Activity.LastActivityAt = rec.Activity.LastActivityAt.Add(time.Second)
	a.sessions["p-1"] = rec
	return a.fakeSeeds.Capture(ctx, workspace, ref)
}

// A source that acts between its freeze and the file capture could give the
// fork files its copied conversation doesn't explain, so the fork is refused.
func TestForkRefusesASourceThatActsWhileItsFilesAreCaptured(t *testing.T) {
	froze := 0
	var deleted []string
	sessions := fakeSessions{"p-1": source()}
	s, sp, _ := newService(source(), func(s *Service) {
		s.deps.Sources = freezingSources{froze: &froze}
		s.deps.Sessions = sessions
		s.deps.Seeds = activeDuringCapture{fakeSeeds: fakeSeeds{deleted: &deleted}, sessions: sessions}
	})
	_, err := s.Fork(context.Background(), "p-1", "req-1")
	if code := refusalCode(t, err); code != CodeSourceBusy {
		t.Fatalf("refusal = %q, want %q", code, CodeSourceBusy)
	}
	if len(sp.got) != 0 {
		t.Fatalf("spawned %d forks, want none", len(sp.got))
	}
	if len(deleted) != 1 {
		t.Fatalf("deleted snapshots = %v, want the captured one", deleted)
	}
}

// spawnerRequests looks requests up in what the fake spawner made.
type spawnerRequests struct{ sp *fakeSpawner }

func (r spawnerRequests) GetSessionByClientRequestID(_ context.Context, id string) (domain.SessionRecord, bool, error) {
	rec, ok := r.sp.byReq[id]
	return rec, ok, nil
}

// A retry of a fork whose response was lost returns the fork it made, even
// once the original has moved on: busy, archived, or deleted.
func TestRetriedForkReturnsItsSessionAfterTheOriginalMovesOn(t *testing.T) {
	sessions := fakeSessions{"p-1": source()}
	s, sp, _ := newService(source(), func(s *Service) { s.deps.Sessions = sessions })
	s.deps.Requests = spawnerRequests{sp}
	first, err := s.Fork(context.Background(), "p-1", "req-1")
	if err != nil {
		t.Fatal(err)
	}
	busy, archived := source(), source()
	busy.Activity.State = domain.ActivityActive
	archived.IsTerminated = true
	for name, rec := range map[string]*domain.SessionRecord{"busy": &busy, "archived": &archived, "deleted": nil} {
		if rec == nil {
			delete(sessions, "p-1")
		} else {
			sessions["p-1"] = *rec
		}
		again, err := s.Fork(context.Background(), "p-1", "req-1")
		if err != nil {
			t.Fatalf("%s original: retry = %v, want the first fork", name, err)
		}
		if again.ID != first.ID {
			t.Fatalf("%s original: retry = %s, want %s", name, again.ID, first.ID)
		}
	}
	if len(sp.got) != 1 {
		t.Fatalf("spawned %d forks, want 1", len(sp.got))
	}
}
