package filesnapshot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/pkg/gitbundle"
)

// ErrChainBroken means a snapshot bundle needs commits the store doesn't
// hold: an earlier link of its chain is missing, or was fetched out of order.
var ErrChainBroken = errors.New("snapshot bundle chain is broken")

// BundleChain writes the snapshot under ref, from the git directory gitDir
// (a repository's, or a plain folder's store), to the bundle file out, so it
// can be kept away from the folder and fetched back into any store. With an
// empty parentRef the bundle is a base holding everything ref needs; with
// parentRef, the previous snapshot of the same chain, it holds only what that
// one lacks. It returns the commits the receiving store must already hold.
func BundleChain(ctx context.Context, gitDir, ref, parentRef, out string) ([]string, error) {
	if !strings.HasPrefix(ref, snapshotsPrefix) {
		return nil, fmt.Errorf("snapshot ref %q is outside %s", ref, snapshotsPrefix)
	}
	args := []string{"bundle", "create", "--quiet", out, ref}
	if parentRef != "" {
		if !strings.HasPrefix(parentRef, snapshotsPrefix) {
			return nil, fmt.Errorf("snapshot ref %q is outside %s", parentRef, snapshotsPrefix)
		}
		args = append(args, "--not", parentRef)
	}
	if err := inGitDir(ctx, gitDir, args...); err != nil {
		return nil, err
	}
	return gitbundle.Prerequisites(out)
}

// FetchChain fetches every snapshot in bundles, oldest first, into the git
// directory gitDir under the refs they were bundled from. It stops at the
// first bundle whose required commits the store lacks, with ErrChainBroken,
// having fetched nothing from that bundle.
func FetchChain(ctx context.Context, gitDir string, bundles []string) error {
	for _, bundle := range bundles {
		prerequisites, err := gitbundle.Prerequisites(bundle)
		if err != nil {
			return fmt.Errorf("read snapshot bundle: %w", err)
		}
		for _, commit := range prerequisites {
			if err := inGitDir(ctx, gitDir, "cat-file", "-e", commit+"^{commit}"); err != nil {
				return fmt.Errorf("%w: %s needs %s", ErrChainBroken, bundle, commit)
			}
		}
		if err := inGitDir(ctx, gitDir, "bundle", "verify", "--quiet", bundle); err != nil {
			return err
		}
		if err := inGitDir(ctx, gitDir, "fetch", "--quiet", "--no-write-fetch-head", "--no-prune", bundle, snapshotsPrefix+"*:"+snapshotsPrefix+"*"); err != nil {
			return err
		}
	}
	return nil
}

// inGitDir runs git against the git directory gitDir from an empty folder.
// Run from inside a git directory, a ref's name is also the path of its loose
// ref file there, so git can't tell the revision from the file.
func inGitDir(ctx context.Context, gitDir string, args ...string) error {
	cwd, err := os.MkdirTemp("", "ao-file-snapshot-*")
	if err != nil {
		return fmt.Errorf("create a folder to run git in: %w", err)
	}
	defer func() { _ = os.RemoveAll(cwd) }()
	_, err = git(ctx, cwd, []string{"GIT_DIR=" + gitDir}, nil, args...)
	return err
}
