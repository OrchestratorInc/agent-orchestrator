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
// snapshots, for a folder with no repository of its own such as a standalone
// agent's. ok is false for any other folder. The store lives outside the
// folder, so nothing is ever written into it but the files a restore puts
// back.
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

// detachedStore is the store keeping workspace's snapshots, if it has one.
func detachedStore(workspace string) (string, bool) {
	f := detachedStores.Load()
	if f == nil {
		return "", false
	}
	return (*f)(filepath.Clean(workspace))
}

// detachedEnv points git at dir's store, with dir as its worktree.
func detachedEnv(dir string) []string {
	store, ok := detachedStore(dir)
	if !ok {
		return nil
	}
	return []string{"GIT_DIR=" + store, "GIT_WORK_TREE=" + filepath.Clean(dir)}
}

var storeInit sync.Mutex

// ensureStore creates a plain folder's store the first time it is used: a bare
// repository whose HEAD is an empty commit, with an empty index, so a snapshot
// is the folder's files on top of nothing. It is built beside its final path
// and renamed into place, so a store is either whole or absent.
func ensureStore(ctx context.Context, store string) error {
	if storeReady(store) {
		return nil
	}
	storeInit.Lock()
	defer storeInit.Unlock()
	if storeReady(store) {
		return nil
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
