package forkseed

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/pkg/gitbundle"
)

// ErrPrerequisiteMissing means a seed bundle needs commits the receiving
// repository doesn't hold, such as history a shallow clone left out. Fetch
// them first; gitbundle.Prerequisites lists every one.
var ErrPrerequisiteMissing = errors.New("forkseed: bundle needs commits the repository lacks")

// Bundle writes the seed under ref, with every commit it needs that no remote
// holds, to the bundle file out, so another machine's clone of the same
// remote can take it with Unbundle. A repository with no remote, such as a
// standalone agent's, bundles its whole history. It returns the commits the
// receiver must already hold: the remote history the seed builds on.
func Bundle(ctx context.Context, workspace, ref, out string) ([]string, error) {
	if !strings.HasPrefix(ref, RefPrefix) {
		return nil, fmt.Errorf("forkseed: ref %q is outside %s", ref, RefPrefix)
	}
	// git runs in workspace, so a relative path is made absolute against the
	// caller's folder first, for git and for reading the header alike.
	out, err := filepath.Abs(out)
	if err != nil {
		return nil, fmt.Errorf("forkseed: resolve bundle path: %w", err)
	}
	if _, err := run(ctx, workspace, nil, "bundle", "create", "--quiet", out, ref, "--not", "--remotes"); err != nil {
		return nil, fmt.Errorf("forkseed: create bundle: %w", err)
	}
	return gitbundle.Prerequisites(out)
}

// Unbundle fetches the seed under ref from the bundle file into the
// repository at worktree, under the same ref, for Apply. Every commit the
// bundle needs must already be there; ErrPrerequisiteMissing names those that
// aren't, and nothing is fetched.
func Unbundle(ctx context.Context, worktree, bundle, ref string) error {
	if !strings.HasPrefix(ref, RefPrefix) {
		return fmt.Errorf("forkseed: ref %q is outside %s", ref, RefPrefix)
	}
	bundle, err := filepath.Abs(bundle)
	if err != nil {
		return fmt.Errorf("forkseed: resolve bundle path: %w", err)
	}
	prerequisites, err := gitbundle.Prerequisites(bundle)
	if err != nil {
		return fmt.Errorf("forkseed: read bundle: %w", err)
	}
	missing, err := missingCommits(ctx, worktree, prerequisites)
	if err != nil {
		return failure(ctx, fmt.Errorf("forkseed: check required commits: %w", err))
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s", ErrPrerequisiteMissing, strings.Join(missing, ", "))
	}
	if _, err := run(ctx, worktree, nil, "bundle", "verify", "--quiet", bundle); err != nil {
		return failure(ctx, fmt.Errorf("forkseed: verify bundle: %w", err))
	}
	if _, err := run(ctx, worktree, nil, "fetch", "--quiet", "--no-write-fetch-head", "--no-prune", bundle, ref+":"+ref); err != nil {
		return failure(ctx, fmt.Errorf("forkseed: fetch bundle: %w", err))
	}
	return nil
}

// missingCommits are the commits the repository at worktree lacks. git
// cat-file --batch-check names each one missing and fails only when it can't
// read the repository, so a missing commit is never confused with another
// failure: plain cat-file -e exits the same way for both.
func missingCommits(ctx context.Context, worktree string, commits []string) ([]string, error) {
	if len(commits) == 0 {
		return nil, nil
	}
	var input strings.Builder
	for _, commit := range commits {
		input.WriteString(commit + "^{commit}\n")
	}
	cmd := aoprocess.CommandContext(ctx, "git", "-C", worktree, "cat-file", "--batch-check=%(objecttype)")
	cmd.Env = processEnv()
	cmd.Stdin = strings.NewReader(input.String())
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git cat-file: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	types := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(types) != len(commits) {
		return nil, fmt.Errorf("git cat-file --batch-check answered %d lines for %d commits", len(types), len(commits))
	}
	var missing []string
	for i, kind := range types {
		if kind != "commit" {
			missing = append(missing, commits[i])
		}
	}
	return missing, nil
}

// failure is err, or the context's own error when it was cancelled, which is
// why git failed.
func failure(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}
