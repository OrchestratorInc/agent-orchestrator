package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Worker environment a bring-your-own Coder connection sets through the
// worker bootstrap (mirrored by the coder provider).
const (
	// WorkingDirectoryEnv is the connection's working directory template, for
	// example ~/work/{repo}. It describes the template's own layout, so it
	// only ever reaches a worker that runs as the workspace's login user.
	WorkingDirectoryEnv = "AO_WORKER_WORKING_DIRECTORY"
	// ProtectSecretFilesEnv is "1" when the coding agent must not read or
	// edit secret environment files (.env and .env.*).
	ProtectSecretFilesEnv = "AO_WORKER_PROTECT_SECRET_FILES"
)

// WorkspacePathPlaceholder stands for the session's working directory in the
// control-plane-authored system prompt. The control plane cannot know where a
// Coder template's checkout lives (it depends on the workspace user's home and
// the connection's working directory), so the worker substitutes its resolved
// workspace before launching the agent.
const WorkspacePathPlaceholder = "{{AO_WORKSPACE_DIR}}"

// CapabilityWorkspacePathPlaceholder is the worker capability that it
// substitutes WorkspacePathPlaceholder. A control plane sends the placeholder
// only to a worker reporting it; an older worker gets the literal default
// path it always showed, never the raw placeholder.
const CapabilityWorkspacePathPlaceholder = "workspace.path-placeholder"

// CapabilityMultiRepo, in a worker's bootstrap capabilities, means it checks
// out a project's additional repositories on the session branch, honours
// LaunchContext.AgentStartsIn and opens pull requests in any project
// repository through the pull request socket's "repository" field. The control
// plane only describes those to the agent for a worker that advertises it.
const CapabilityMultiRepo = "workspace.multi-repo"

// CapabilitiesHeader carries a worker's capabilities, comma separated, on its
// requests (the reconnect GET has no body to report them in).
const CapabilitiesHeader = "AO-Worker-Capabilities"

// LegacyWorkspacePath is the working directory older workers' instructions
// named before the worker resolved it.
const LegacyWorkspacePath = "/workspace/repository"

// workingDirectoryFile, in the worker data directory, records how a session
// settled on its configured working directory ("adopted <path>" or "cloned
// <path>"), so a restarted worker treats the directory the same way.
const workingDirectoryFile = "working-directory"

// ErrWorkingDirectoryUnusable reports a configured working directory that
// exists but is not a checkout of the session repository. AO never falls back
// to another directory for it: the template asked for this one.
var ErrWorkingDirectoryUnusable = errors.New("configured working directory cannot be used")

// RepositoryName is the last path segment of a repository URL without a
// trailing ".git", for example "alice" for https://github.com/11x-ai/alice.git.
// It returns "" when the URL has no usable name.
func RepositoryName(repositoryURL string) string {
	if NormalizeRemoteURL(repositoryURL) == "" {
		return ""
	}
	raw := strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(repositoryURL), "/"), ".git")
	name := raw[strings.LastIndexAny(raw, "/:")+1:]
	if name == "" || name == "." || name == ".." {
		return ""
	}
	return name
}

// ExpandWorkingDirectory resolves a working directory template against the
// workspace user's home and the session repository: a leading "~/" or
// "$HOME/" becomes home and {repo} becomes RepositoryName. The result is a
// clean absolute path.
func ExpandWorkingDirectory(template, home, repositoryURL string) (string, error) {
	template = strings.TrimSpace(template)
	if template == "" {
		return "", errors.New("working directory is empty")
	}
	if strings.Contains(template, "{repo}") {
		name := RepositoryName(repositoryURL)
		if name == "" {
			return "", fmt.Errorf("working directory %q names {repo}, but the session has no repository", template)
		}
		template = strings.ReplaceAll(template, "{repo}", name)
	}
	for _, prefix := range []string{"~/", "$HOME/"} {
		if rest, ok := strings.CutPrefix(template, prefix); ok {
			home = filepath.Clean(strings.TrimSpace(home))
			if !filepath.IsAbs(home) || home == string(filepath.Separator) {
				return "", errors.New("working directory is under home, but the worker has no home directory")
			}
			template = filepath.Join(home, rest)
			break
		}
	}
	if !filepath.IsAbs(template) {
		return "", fmt.Errorf("working directory %q is not an absolute path", template)
	}
	for _, segment := range strings.Split(template, string(filepath.Separator)) {
		if segment == ".." {
			return "", fmt.Errorf("working directory %q must not contain ..", template)
		}
	}
	resolved := filepath.Clean(template)
	if resolved == string(filepath.Separator) {
		return "", errors.New("working directory must not be the filesystem root")
	}
	return resolved, nil
}

// ResolveWorkingDirectory decides how a session uses the connection's
// configured working directory:
//   - a git checkout whose origin is the session repository is adopted
//     exactly like ResolveReusedCheckout adopts one (session branch, stash);
//   - a missing or empty directory gets AO's own fresh clone (Reused false,
//     Workspace the directory);
//   - anything else is ErrWorkingDirectoryUnusable.
//
// The decision is recorded in dataDir and honored on every later worker start.
func ResolveWorkingDirectory(
	ctx context.Context,
	runner GitRunner,
	template, home, dataDir, repositoryURL, sessionID, branch, defaultBranch string,
) (ReuseDecision, error) {
	dir, err := ExpandWorkingDirectory(template, home, repositoryURL)
	if err != nil {
		return ReuseDecision{}, fmt.Errorf("%w: %v", ErrWorkingDirectoryUnusable, err)
	}
	recorded := filepath.Join(dataDir, workingDirectoryFile)
	if raw, readErr := os.ReadFile(recorded); readErr == nil {
		mode, path, _ := strings.Cut(strings.TrimSpace(string(raw)), " ")
		if filepath.Clean(path) == dir {
			switch mode {
			case "adopted":
				if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
					return ReuseDecision{Workspace: dir, Reused: true}, nil
				}
				return ReuseDecision{}, fmt.Errorf("%w: %s is no longer a git checkout", ErrWorkingDirectoryUnusable, dir)
			case "cloned":
				return ReuseDecision{Workspace: dir}, nil
			}
		}
	}
	record := func(mode string, decision ReuseDecision) (ReuseDecision, error) {
		if err := os.MkdirAll(dataDir, 0o700); err == nil {
			_ = os.WriteFile(recorded, []byte(mode+" "+dir+"\n"), 0o600)
		}
		return decision, nil
	}
	info, statErr := os.Stat(dir)
	switch {
	case errors.Is(statErr, os.ErrNotExist):
		return record("cloned", ReuseDecision{Workspace: dir})
	case statErr != nil:
		return ReuseDecision{}, fmt.Errorf("%w: %s: %v", ErrWorkingDirectoryUnusable, dir, statErr)
	case !info.IsDir():
		return ReuseDecision{}, fmt.Errorf("%w: %s is not a directory", ErrWorkingDirectoryUnusable, dir)
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
		return record("cloned", ReuseDecision{Workspace: dir})
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return ReuseDecision{}, fmt.Errorf("%w: %s exists but is not a git checkout", ErrWorkingDirectoryUnusable, dir)
	}
	want := NormalizeRemoteURL(repositoryURL)
	origin, err := runner.Run(ctx, dir, nil, "remote", "get-url", "origin")
	if err != nil || want == "" || NormalizeRemoteURL(strings.TrimSpace(origin)) != want {
		return ReuseDecision{}, fmt.Errorf("%w: %s is a checkout of a different repository", ErrWorkingDirectoryUnusable, dir)
	}
	stash, created, err := adoptExistingCheckout(ctx, runner, dir, sessionID, branch, defaultBranch)
	if err != nil {
		return ReuseDecision{}, fmt.Errorf("%w: %s: %v", ErrWorkingDirectoryUnusable, dir, err)
	}
	return record("adopted", ReuseDecision{
		Workspace: dir, Reused: true, AdoptedNow: true, Stash: stash, BranchCreated: created,
		AdoptedAt: adoptedCommit(ctx, runner, dir, branch, created),
	})
}
