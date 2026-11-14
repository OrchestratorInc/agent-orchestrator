// Package sessionmove moves a session's code between this machine and AO
// Cloud through origin: it captures uncommitted work as one commit on top of
// the branch and pushes it, and places a branch Cloud pushed.
package sessionmove

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

type gitError struct {
	args   []string
	stderr string
	err    error
}

func (e *gitError) Error() string {
	return fmt.Sprintf("git %s: %v: %s", strings.Join(e.args, " "), e.err, e.stderr)
}

func (e *gitError) Unwrap() error { return e.err }

// runGit runs git in dir with hooks, prompts and optional index writes turned
// off, so a move never runs project code or rewrites the real index.
func runGit(ctx context.Context, dir string, env []string, stdin io.Reader, args ...string) (string, error) {
	full := append([]string{
		"--no-pager", "--no-optional-locks",
		"-c", "core.hooksPath=" + os.DevNull,
		"-c", "core.fsmonitor=false",
		"-C", dir,
	}, args...)
	cmd := aoprocess.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never", "LC_ALL=C")
	cmd.Env = append(cmd.Env, env...)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), &gitError{args: args, stderr: strings.TrimSpace(stderr.String()), err: err}
	}
	return stdout.String(), nil
}

func gitLine(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := runGit(ctx, dir, nil, nil, args...)
	return strings.TrimSpace(out), err
}

// Blob is a file as it travels: its path and size in bytes.
type Blob struct {
	Path string
	Size int64
}

// unpushedBlobs lists, among the files in commits reachable from tip that
// origin does not have, those over limit, largest first, and the Git LFS
// pointers, whose objects a push without the LFS hook would leave behind.
func unpushedBlobs(ctx context.Context, dir, tip string, limit int64) ([]Blob, []string, error) {
	out, err := runGit(ctx, dir, nil, nil, "rev-list", "--objects", tip, "--not", "--remotes=origin")
	if err != nil {
		return nil, nil, err
	}
	paths := map[string]string{}
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		id, path, ok := strings.Cut(line, " ")
		if !ok || path == "" {
			continue
		}
		if _, seen := paths[id]; !seen {
			paths[id] = path
			ids = append(ids, id)
		}
	}
	sizes, err := objectSizes(ctx, dir, ids)
	if err != nil {
		return nil, nil, err
	}
	var blobs []Blob
	var small []string
	for i, id := range ids {
		if sizes[i] > 0 && sizes[i] < 1024 {
			small = append(small, id)
		}
		if sizes[i] <= limit {
			continue
		}
		blobs = append(blobs, Blob{Path: paths[id], Size: sizes[i]})
	}
	slices.SortFunc(blobs, func(a, b Blob) int { return cmp.Compare(b.Size, a.Size) })
	contents, err := blobContents(ctx, dir, small)
	if err != nil {
		return nil, nil, err
	}
	var lfs []string
	for _, id := range small {
		if bytes.HasPrefix(contents[id], []byte(lfsPointerPrefix)) {
			lfs = append(lfs, paths[id])
		}
	}
	slices.Sort(lfs)
	return blobs, lfs, nil
}

// objectSizes returns each object's size, or -1 for anything that is not a blob.
func objectSizes(ctx context.Context, dir string, ids []string) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	out, err := runGit(ctx, dir, nil, strings.NewReader(strings.Join(ids, "\n")+"\n"),
		"cat-file", "--batch-check=%(objecttype) %(objectsize)")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != len(ids) {
		return nil, fmt.Errorf("cat-file returned %d lines for %d objects", len(lines), len(ids))
	}
	sizes := make([]int64, len(ids))
	for i, line := range lines {
		kind, size, _ := strings.Cut(line, " ")
		if kind != "blob" {
			sizes[i] = -1
			continue
		}
		if sizes[i], err = strconv.ParseInt(size, 10, 64); err != nil {
			return nil, fmt.Errorf("cat-file size %q: %w", line, err)
		}
	}
	return sizes, nil
}

// blobContents reads small blobs in one cat-file call.
func blobContents(ctx context.Context, dir string, ids []string) (map[string][]byte, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	out, err := runGit(ctx, dir, nil, strings.NewReader(strings.Join(ids, "\n")+"\n"), "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	r := bufio.NewReader(strings.NewReader(out))
	contents := map[string][]byte{}
	for range ids {
		header, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("cat-file header: %w", err)
		}
		fields := strings.Fields(header)
		if len(fields) != 3 {
			continue // "<id> missing"
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("cat-file header %q: %w", header, err)
		}
		body := make([]byte, size+1) // content plus the trailing newline
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, fmt.Errorf("cat-file body: %w", err)
		}
		contents[fields[0]] = body[:size]
	}
	return contents, nil
}
