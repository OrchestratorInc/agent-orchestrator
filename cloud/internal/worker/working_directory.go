package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

// CapabilityChatStart, in a worker's capabilities, means it starts a session
// created in Chat straight into the Chat controller, and itself moves the
// session to the TUI (POST /worker/interface/chat-start-fallback) when it
// cannot run that controller. For a worker that does not report it, the
// control plane moves a never-started Claude Chat session to the TUI when the
// worker bootstraps.
const CapabilityChatStart = "agent.chat-start"

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

// StandaloneWorkingDirectory resolves a working directory template for a
// session with no repository. A {repo} template names one checkout among
// several, so such a session starts in the folder that holds them:
// ~/work/{repo} becomes ~/work. A template with no {repo} resolves as
// ExpandWorkingDirectory does, and one whose {repo} sits directly under home
// or the root has no such folder and is an error.
func StandaloneWorkingDirectory(template, home string) (string, error) {
	template = strings.TrimSpace(template)
	index := strings.Index(template, "{repo}")
	if index < 0 {
		return ExpandWorkingDirectory(template, home, "")
	}
	parent := template[:max(strings.LastIndex(template[:index], "/"), 0)]
	if parent == "" || parent == "~" || parent == "$HOME" {
		return "", fmt.Errorf("working directory %q has no folder above {repo} for a session with no repository", template)
	}
	dir, err := ExpandWorkingDirectory(parent, home, "")
	if err != nil {
		return "", err
	}
	// The folder above {repo} must hold the repositories, not be a home or the
	// root: the session writes its agent settings and runs Setup there.
	clean := filepath.Clean(dir)
	if clean == string(filepath.Separator) {
		return "", fmt.Errorf("working directory %q puts a session with no repository at the filesystem root", template)
	}
	for _, other := range []string{home, os.Getenv(UserHomeEnv)} {
		if other = strings.TrimSpace(other); other != "" && filepath.Clean(other) == clean {
			return "", fmt.Errorf("working directory %q puts a session with no repository in the home directory", template)
		}
	}
	if _, err := os.Stat(filepath.Join(clean, ".ao")); err == nil {
		return "", fmt.Errorf("working directory %q puts a session with no repository in AO's worker folder", template)
	}
	return dir, nil
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

// Worker environment for a working-folder checkout, set by the Coder provider
// when the worker runs as the dedicated ao-worker user (mirrored by the coder
// provider's constants of the same names).
const (
	// CheckoutDirectoryEnv is the absolute folder AO checks the session's main
	// repository out in (the project's working folder, for example
	// /home/ubuntu/work/alice), instead of the durable root's repository
	// folder. AO_WORKSPACE_DIR names the same folder.
	CheckoutDirectoryEnv = "AO_WORKER_CHECKOUT_DIRECTORY"
	// UserHomeEnv is the workspace login user's home. The agent never starts
	// in it, and messages show folders below it with "~".
	UserHomeEnv = "AO_WORKER_USER_HOME"
)

// ErrWorkingDirectoryOccupied reports a working-folder checkout that already
// exists and is not AO's: something else (typically the Coder template, from
// one of its own options) created it. AO, running as its own user, never
// takes over or changes it, and never falls back to another folder.
var ErrWorkingDirectoryOccupied = errors.New("the working folder already exists")

// ErrCheckoutDirectoryUnusable reports a working-folder checkout AO could not
// prepare, for example a missing folder it is not allowed to create.
var ErrCheckoutDirectoryUnusable = errors.New("the working folder cannot be used")

// ResolveOwnedCheckoutDirectory checks the folder a dedicated-user worker
// checks the main repository out in. The provider's bootstrap creates it owned
// by the worker user, so a folder the worker owns is AO's own checkout (a fresh
// one, or this session's from an earlier start, whose origin the checkout then
// verifies) and is used as is. A missing folder is created. Anything else (a
// folder owned by another user, a symbolic link or a file) is
// ErrWorkingDirectoryOccupied; home is the workspace login user's home, used
// to name the folder with "~".
func ResolveOwnedCheckoutDirectory(dir, home string) error {
	return resolveOwnedCheckoutDirectory(dir, home, os.Getuid())
}

func resolveOwnedCheckoutDirectory(dir, home string, uid int) error {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if !filepath.IsAbs(dir) || dir == string(filepath.Separator) {
		return fmt.Errorf("%w: %q is not an absolute folder", ErrCheckoutDirectoryUnusable, dir)
	}
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		if mkdirErr := os.MkdirAll(dir, 0o755); mkdirErr != nil {
			return fmt.Errorf("%w: %s does not exist and AO could not create it: %v",
				ErrCheckoutDirectoryUnusable, DisplayPath(dir, home), mkdirErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrCheckoutDirectoryUnusable, DisplayPath(dir, home), err)
	}
	if foreignFolder(info, uid) {
		return fmt.Errorf("%w: %s", ErrWorkingDirectoryOccupied, DisplayPath(dir, home))
	}
	return nil
}

// foreignFolder reports whether info is not a folder of uid's own: a symbolic
// link, not a folder, or a folder another user owns.
func foreignFolder(info os.FileInfo, uid int) bool {
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return true
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) != uid
}

// ownedRepoFoldersFile, in the worker data directory, marks a session whose
// checkouts must all be AO's own folders: a dedicated-user worker's
// working-folder checkout (CheckoutDirectoryEnv) and the additional
// repositories beside it. An additional repository's folder that already
// exists there and is not the worker's (the Coder template's own clone, from
// one of its options) is then never used, as the main folder is not: AO does
// not check the repository out in it, and its Setup and Cleanup steps are
// skipped (ForeignRepoFolder). The file lists the folders refused when the
// worker started, for diagnosis.
const ownedRepoFoldersFile = "owned-repo-folders"

// RecordOwnedRepoFolders marks dataDir's session as one whose checkouts must
// be AO's own (owned), or clears the mark, and returns the additional
// repositories' checkouts (extras) whose folders are refused now.
func RecordOwnedRepoFolders(dataDir string, owned bool, extras []RepoCheckout) ([]RepoCheckout, error) {
	path := filepath.Join(dataDir, ownedRepoFoldersFile)
	if !owned {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, nil
	}
	var refused []RepoCheckout
	var listed strings.Builder
	for _, checkout := range extras {
		if ForeignRepoFolder(checkout.Path) {
			refused = append(refused, checkout)
			listed.WriteString(checkout.Path + "\n")
		}
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return refused, err
	}
	return refused, writeFileAtomic(path, []byte(listed.String()), 0o600)
}

// OwnedRepoFoldersOnly reports whether dataDir marks its session as one whose
// checkouts must be AO's own (RecordOwnedRepoFolders).
func OwnedRepoFoldersOnly(dataDir string) bool {
	_, err := os.Stat(filepath.Join(dataDir, ownedRepoFoldersFile))
	return err == nil
}

// UsedSessionRepoCheckouts lists the session's checkouts AO uses
// (SessionRepoCheckouts), the main repository's first. In a session whose
// checkouts must be AO's own (OwnedRepoFoldersOnly in dataDir), an additional
// repository whose folder is not (ForeignRepoFolder: the Coder template's own
// clone) is left out, as a repository skipped for its folder name is: AO did
// not check it out there, and never shows, changes or pushes from it. A
// missing folder is kept: AO checks the repository out there.
func UsedSessionRepoCheckouts(workspace, dataDir, mainURL string, extras []RepoRef) []RepoCheckout {
	checkouts := SessionRepoCheckouts(workspace, mainURL, extras)
	if strings.TrimSpace(dataDir) == "" || !OwnedRepoFoldersOnly(dataDir) {
		return checkouts
	}
	used := []RepoCheckout{checkouts[0]}
	for _, checkout := range checkouts[1:] {
		if !ForeignRepoFolder(checkout.Path) {
			used = append(used, checkout)
		}
	}
	return used
}

// ForeignRepoFolder reports whether path exists and is not a folder of the
// worker's own (foreignFolder): one AO never checks a repository out in or
// runs a Setup or Cleanup step in when its checkouts must be its own.
func ForeignRepoFolder(path string) bool {
	return foreignRepoFolder(path, os.Getuid())
}

func foreignRepoFolder(path string, uid int) bool {
	info, err := os.Lstat(path)
	return err == nil && foreignFolder(info, uid)
}

// ForeignRepoFolderHint says why a Setup or Cleanup step of an additional
// repository did not run: its folder (as shown) already exists and is not
// AO's.
func ForeignRepoFolderHint(folder string) string {
	return folder + " already exists (created by your Coder template), so AO did not run this step in it. " +
		"Untick it in the project's Template options, or turn on 'Run the agent as the workspace user' to reuse it. " +
		"Start a new session after changing it."
}

// DisplayPath names dir for a person: below home it starts with "~/".
func DisplayPath(dir, home string) string {
	home = filepath.Clean(strings.TrimSpace(home))
	if home != "" && filepath.IsAbs(home) && home != string(filepath.Separator) {
		if rest, ok := strings.CutPrefix(dir, home+string(filepath.Separator)); ok {
			return "~/" + rest
		}
	}
	return dir
}
