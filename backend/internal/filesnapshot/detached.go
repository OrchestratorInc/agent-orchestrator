package filesnapshot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// DetachedStores names the git directory that keeps a plain folder's
// snapshots, such as a standalone agent's folder. ok is false for any other
// folder. The store lives outside the folder, so nothing is ever written into
// it but the files a restore puts back. It takes the folder's snapshots while
// the folder has no repository of its own; once the agent makes it one, new
// snapshots go to that repository, as any repository's do, and the store
// keeps the ones taken before.
type DetachedStores func(workspace string) (gitDir string, ok bool)

var detachedStores atomic.Pointer[DetachedStores]

// SetDetachedStores routes plain folders' snapshots to the stores f names.
// Nil turns it off: a plain folder is then not a repository.
func SetDetachedStores(f DetachedStores) {
	if f == nil {
		detachedStores.Store(nil)
		return
	}
	detachedStores.Store(&f)
}

// detachedExcludes are left out of a plain folder's snapshots, as a git
// project ignores them: dependency folders, large and reinstallable.
const detachedExcludes = "node_modules/\n.venv/\n"

// detachedPathspec, for a plain folder, keeps its dependency folders out of
// the files a snapshot lists and stages, at any depth. The store's excludes
// alone don't: a .gitignore in the folder outranks them, so a rule such as
// `!node_modules/` would put them back. Other files keep ordinary ignore
// rules. Nil for any other folder.
func detachedPathspec(ctx context.Context, dir string) []string {
	if _, ok := storeInUse(ctx, dir); !ok {
		return nil
	}
	return []string{"--", ".", ":(exclude,glob)**/node_modules/**", ":(exclude,glob)**/.venv/**"}
}

// detachedStore is the store named for workspace's snapshots, if any.
func detachedStore(workspace string) (string, bool) {
	f := detachedStores.Load()
	if f == nil {
		return "", false
	}
	return (*f)(filepath.Clean(workspace))
}

// activeStore is the store taking workspace's snapshots now: its named store,
// while the folder has no repository of its own.
func activeStore(workspace string) (string, bool) {
	store, ok := detachedStore(workspace)
	if !ok {
		return "", false
	}
	if _, err := os.Lstat(filepath.Join(workspace, ".git")); !errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	return store, true
}

type storeKey struct{}

// withStore runs every git command under ctx against store, for a snapshot
// the store kept from before the folder became a repository.
func withStore(ctx context.Context, store string) context.Context {
	return context.WithValue(ctx, storeKey{}, store)
}

// storeInUse is the store git commands for dir run against: the one ctx
// names, or dir's active store.
func storeInUse(ctx context.Context, dir string) (string, bool) {
	if store, ok := ctx.Value(storeKey{}).(string); ok {
		return store, true
	}
	return activeStore(dir)
}

// detachedEnv points git at the store in use for dir, with dir as its
// worktree.
func detachedEnv(ctx context.Context, dir string) []string {
	store, ok := storeInUse(ctx, dir)
	if !ok {
		return nil
	}
	return []string{"GIT_DIR=" + store, "GIT_WORK_TREE=" + filepath.Clean(dir)}
}

// earlierSnapshot finds ref in the store a folder used before it became a
// repository, and returns ctx pointed at that store. ok is false when the
// folder's store is the one in use anyway, or doesn't hold ref.
func earlierSnapshot(ctx context.Context, workspace, ref string) (context.Context, string, bool) {
	if _, active := activeStore(workspace); active {
		return ctx, "", false
	}
	if _, forced := ctx.Value(storeKey{}).(string); forced {
		return ctx, "", false
	}
	store, ok := detachedStore(workspace)
	if !ok || !storeReady(store) {
		return ctx, "", false
	}
	stored := withStore(ctx, store)
	if _, err := git(stored, filepath.Clean(workspace), nil, nil, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
		return ctx, "", false
	}
	return stored, store, true
}

// InDetachedStore reports whether workspace's store holds the snapshot under
// ref: one taken while the folder had no repository of its own.
func InDetachedStore(ctx context.Context, workspace, ref string) bool {
	if store, ok := activeStore(workspace); ok {
		if !storeReady(store) {
			return false
		}
		_, err := git(ctx, filepath.Clean(workspace), nil, nil, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
		return err == nil
	}
	_, _, ok := earlierSnapshot(ctx, workspace, ref)
	return ok
}

// storeLocks holds one lock per store, so making one store never waits on
// another's.
var storeLocks sync.Map

// ensureStore creates a plain folder's store the first time it is used: a bare
// repository whose HEAD is an empty commit, with an empty index, so a snapshot
// is the folder's files on top of nothing. It is built beside its final path
// and renamed into place. A store that lost its index gets an empty one again,
// keeping its snapshots, and anything else at its path that isn't a
// repository is replaced, so one bad leftover can't fail every later turn.
func ensureStore(ctx context.Context, store string) error {
	if storeReady(store) {
		return nil
	}
	lock, _ := storeLocks.LoadOrStore(store, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	if storeReady(store) {
		return nil
	}
	if _, err := os.Stat(filepath.Join(store, "HEAD")); err == nil {
		env := []string{"GIT_DIR=" + store, "GIT_INDEX_FILE=" + filepath.Join(store, "index")}
		_, err := git(ctx, filepath.Dir(store), env, nil, "read-tree", "--empty")
		return err
	}
	if err := os.RemoveAll(store); err != nil {
		return fmt.Errorf("remove incomplete snapshot store: %w", err)
	}
	parent := filepath.Dir(store)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create snapshot store folder: %w", err)
	}
	tmp, err := os.MkdirTemp(parent, ".new-store-*")
	if err != nil {
		return fmt.Errorf("create snapshot store: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if _, err := git(ctx, parent, nil, nil, "init", "--quiet", "--bare", tmp); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "info", "exclude"), []byte(detachedExcludes), 0o600); err != nil {
		return fmt.Errorf("write snapshot store excludes: %w", err)
	}
	gitDir, index := "GIT_DIR="+tmp, "GIT_INDEX_FILE="+filepath.Join(tmp, "index")
	env := []string{gitDir, index}
	if _, err := git(ctx, parent, env, nil, "read-tree", "--empty"); err != nil {
		return err
	}
	empty, err := git(ctx, parent, env, nil, "hash-object", "-t", "tree", "-w", os.DevNull)
	if err != nil {
		return err
	}
	identity := []string{
		gitDir, index,
		"GIT_AUTHOR_NAME=Agent Orchestrator", "GIT_AUTHOR_EMAIL=agent-orchestrator@localhost",
		"GIT_COMMITTER_NAME=Agent Orchestrator", "GIT_COMMITTER_EMAIL=agent-orchestrator@localhost",
	}
	base, err := git(ctx, parent, identity, nil, "commit-tree", empty, "-m", "ao folder snapshots")
	if err != nil {
		return err
	}
	if _, err := git(ctx, parent, env, nil, "update-ref", "HEAD", base); err != nil {
		return err
	}
	if err := os.Rename(tmp, store); err != nil {
		return fmt.Errorf("place snapshot store: %w", err)
	}
	return nil
}

func storeReady(store string) bool {
	for _, name := range []string{"HEAD", "index"} {
		if _, err := os.Stat(filepath.Join(store, name)); err != nil {
			return false
		}
	}
	return true
}

// Export writes the files of the snapshot under ref into dst, a folder that
// must not exist yet, the way they were when it was taken. Ignored files and
// dependency folders were never in the snapshot and are not written. A
// missing snapshot is ErrNotFound, and one holding another repository, whose
// files it never recorded, is ErrNestedRepository; on any failure dst is
// removed.
func Export(ctx context.Context, workspace, ref, dst string) (err error) {
	if stored, _, ok := earlierSnapshot(ctx, workspace, ref); ok {
		ctx = stored
	}
	top, err := toplevel(ctx, workspace)
	if err != nil {
		return err
	}
	snapshot, err := git(ctx, top, nil, nil, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		var exit *commandError
		if errors.As(err, &exit) && exit.code == 1 {
			return fmt.Errorf("%w: %s", ErrNotFound, ref)
		}
		return err
	}
	// A repository inside the folder is in the snapshot only as the commit it
	// had checked out, never its files: exported, it would be an empty folder.
	nested, err := gitlink(ctx, top, snapshot+"^{tree}")
	if err != nil {
		return err
	}
	if nested != "" {
		return fmt.Errorf("%w: %s", ErrNestedRepository, nested)
	}
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("export snapshot: %s already exists", dst)
	}
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return fmt.Errorf("export snapshot: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dst)
		}
	}()
	dir, err := os.MkdirTemp("", "ao-file-snapshot-*")
	if err != nil {
		return fmt.Errorf("create temporary index directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(dir, "index")}
	if _, err := git(ctx, top, env, nil, "read-tree", snapshot+"^{tree}"); err != nil {
		return err
	}
	// The prefix is a path, so checkout-index writes under dst and never into
	// the worktree.
	_, err = git(ctx, top, env, nil, "checkout-index", "--all", "--force", "--prefix="+filepath.Clean(dst)+string(filepath.Separator))
	return err
}
