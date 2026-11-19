package gitworktree

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeOrigin fakes a clone of origin whose remote HEAD is main. cached is
// whether Git has cached that HEAD (refs/remotes/origin/HEAD) and present
// whether refs/remotes/origin/main exists; a fetch and a HEAD update set them.
// A background lookup calls it from another goroutine, so it locks.
type fakeOrigin struct {
	mu       sync.Mutex
	cached   bool
	present  bool
	lsRemote int
	fetches  int
	// advertised is the branch the remote names as HEAD; empty means main.
	advertised string
}

func (o *fakeOrigin) counts() (lsRemote, fetches int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.lsRemote, o.fetches
}

func (o *fakeOrigin) runner(t *testing.T) commandRunner {
	t.Helper()
	exitOne := exitStatusOne(t)
	return func(_ context.Context, _ string, args ...string) ([]byte, error) {
		o.mu.Lock()
		defer o.mu.Unlock()
		joined := strings.Join(args, " ")
		switch {
		case strings.HasSuffix(joined, " remote"):
			return []byte("origin\n"), nil
		case strings.Contains(joined, "config --get checkout.defaultRemote"):
			return nil, commandError{args: args, err: exitOne}
		case strings.Contains(joined, "ls-remote --symref -- origin HEAD"):
			o.lsRemote++
			advertised := o.advertised
			if advertised == "" {
				advertised = "main"
			}
			return []byte("ref: refs/heads/" + advertised + "\tHEAD\nabc123\tHEAD\n"), nil
		case strings.Contains(joined, " fetch "):
			o.fetches++
			o.present = true
			return nil, nil
		case strings.Contains(joined, "symbolic-ref --quiet refs/remotes/origin/HEAD"):
			if !o.cached {
				return nil, commandError{args: args, err: exitOne}
			}
			return []byte("refs/remotes/origin/main\n"), nil
		case strings.Contains(joined, "symbolic-ref refs/remotes/origin/HEAD refs/remotes/origin/main"):
			o.cached = true
			return nil, nil
		case strings.Contains(joined, "rev-parse --verify --quiet refs/remotes/origin/main"):
			if !o.present {
				return nil, commandError{args: args, err: exitOne}
			}
			return []byte("abc123\n"), nil
		default:
			return nil, nil
		}
	}
}

func newAutoDefaultWorkspace(t *testing.T, origin *fakeOrigin) (*Workspace, string) {
	t.Helper()
	repo := t.TempDir()
	ws, err := New(Options{ManagedRoot: t.TempDir(), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ws.run = origin.runner(t)
	t.Cleanup(ws.waitForHeadRefreshes)
	return ws, repo
}

func TestResolveDefaultBranchAnswersFromCacheAndLooksUpInBackground(t *testing.T) {
	origin := &fakeOrigin{cached: true, present: true}
	ws, repo := newAutoDefaultWorkspace(t, origin)
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	ws.now = func() time.Time { return now }

	first, err := ws.ResolveDefaultBranch(context.Background(), repo, "")
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if first.BaseRef != "refs/remotes/origin/main" || first.Fetched {
		t.Fatalf("first resolve = %#v, want cached origin/main left for the caller to fetch", first)
	}
	ws.waitForHeadRefreshes()
	if lsRemote, fetches := origin.counts(); lsRemote != 1 || fetches != 0 {
		t.Fatalf("background lookup ran ls-remote %d and fetch %d times, want 1 and 0 for a branch already present", lsRemote, fetches)
	}

	now = now.Add(automaticDefaultRecheck - time.Minute)
	if _, err := ws.ResolveDefaultBranch(context.Background(), repo, ""); err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	ws.waitForHeadRefreshes()
	if lsRemote, _ := origin.counts(); lsRemote != 1 {
		t.Fatalf("resolve inside the window ran ls-remote %d times, want no new lookup", lsRemote)
	}

	now = now.Add(2 * time.Minute)
	third, err := ws.ResolveDefaultBranch(context.Background(), repo, "")
	if err != nil {
		t.Fatalf("third resolve: %v", err)
	}
	if third.BaseRef != "refs/remotes/origin/main" || third.Fetched {
		t.Fatalf("resolve after the window = %#v, want the cached answer at once", third)
	}
	ws.waitForHeadRefreshes()
	if lsRemote, fetches := origin.counts(); lsRemote != 2 || fetches != 0 {
		t.Fatalf("after the window ran ls-remote %d and fetch %d times, want a second background lookup only", lsRemote, fetches)
	}
}

func TestResolveDefaultBranchWaitsForLiveLookupWithoutCache(t *testing.T) {
	origin := &fakeOrigin{}
	ws, repo := newAutoDefaultWorkspace(t, origin)

	got, err := ws.ResolveDefaultBranch(context.Background(), repo, "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.BaseRef != "refs/remotes/origin/main" || !got.Fetched {
		t.Fatalf("resolve = %#v, want live origin/main that was fetched", got)
	}
	if lsRemote, fetches := origin.counts(); lsRemote != 1 || fetches != 1 {
		t.Fatalf("resolve ran ls-remote %d and fetch %d times, want 1 and 1", lsRemote, fetches)
	}
}

func TestWorktreeRefsReuseTheRefreshLookup(t *testing.T) {
	origin := &fakeOrigin{cached: true, present: true}
	ws, repo := newAutoDefaultWorkspace(t, origin)

	if _, err := ws.ResolveDefaultBranch(context.Background(), repo, ""); err != nil {
		t.Fatalf("refresh resolve: %v", err)
	}
	refs, err := ws.resolveWorktreeRefsWithBudget(context.Background(), repo, "ao/work", "")
	if err != nil {
		t.Fatalf("worktree refs: %v", err)
	}
	if refs.baseRef != "refs/remotes/origin/main" {
		t.Fatalf("worktree base ref = %q, want origin/main", refs.baseRef)
	}
	ws.waitForHeadRefreshes()
	if lsRemote, fetches := origin.counts(); lsRemote != 1 || fetches != 0 {
		t.Fatalf("refresh then worktree creation ran ls-remote %d and fetch %d times, want one background lookup", lsRemote, fetches)
	}
}

func TestBackgroundLookupFetchesARenamedDefault(t *testing.T) {
	// The remote now names main, which this clone has never fetched.
	origin := &fakeOrigin{}
	ws, repo := newAutoDefaultWorkspace(t, origin)

	ws.refreshRemoteHeadInBackground(repo)
	ws.waitForHeadRefreshes()
	if lsRemote, fetches := origin.counts(); lsRemote != 1 || fetches != 1 {
		t.Fatalf("lookup ran ls-remote %d and fetch %d times, want the new branch fetched once", lsRemote, fetches)
	}
	got, err := ws.ResolveDefaultBranch(context.Background(), repo, "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.BaseRef != "refs/remotes/origin/main" || got.Fetched {
		t.Fatalf("resolve = %#v, want the cached new default", got)
	}
}

func TestConcurrentStartsLaunchOneBackgroundLookup(t *testing.T) {
	origin := &fakeOrigin{cached: true, present: true}
	ws, repo := newAutoDefaultWorkspace(t, origin)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ws.refreshRemoteHeadInBackground(repo)
		}()
	}
	wg.Wait()
	ws.waitForHeadRefreshes()
	if lsRemote, _ := origin.counts(); lsRemote != 1 {
		t.Fatalf("eight concurrent starts ran ls-remote %d times, want one lookup per window", lsRemote)
	}
}

func TestBackgroundLookupFetchesARenamedDefaultWithAnOldRef(t *testing.T) {
	// The cached HEAD still names main; the remote now names develop, and an
	// origin/develop ref from long ago is still present locally.
	origin := &fakeOrigin{cached: true, present: true, advertised: "develop"}
	ws, repo := newAutoDefaultWorkspace(t, origin)

	ws.refreshRemoteHeadInBackground(repo)
	ws.waitForHeadRefreshes()
	if lsRemote, fetches := origin.counts(); lsRemote != 1 || fetches != 1 {
		t.Fatalf("lookup ran ls-remote %d and fetch %d times, want the renamed default fetched before it is cached", lsRemote, fetches)
	}
}
