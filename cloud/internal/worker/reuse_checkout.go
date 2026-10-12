package worker

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ReuseExistingCheckoutEnv is set to "1" by a Coder connection that runs the
// worker as the workspace user and wants the session to work in the checkout
// the template already cloned (for example /home/ubuntu/work/<repo>, where the
// template's own docker stack runs) instead of a fresh clone into the AO
// workspace directory.
const ReuseExistingCheckoutEnv = "AO_WORKER_REUSE_EXISTING_CHECKOUT"

// checkoutRootFile, in the worker data directory, records the worktree root a
// session settled on, so a restarted worker keeps working in the same place
// even if the reuse decision would now come out differently.
const checkoutRootFile = "checkout-root"

// errCheckoutNotReusable marks an existing checkout AO declined to adopt; the
// caller falls back to its own clone.
var errCheckoutNotReusable = errors.New("existing checkout cannot be reused")

// NormalizeRemoteURL reduces a git remote URL to a comparable host/path form:
// scheme, credentials, port, a trailing ".git" and "/" are dropped and the
// result is lower-cased, so https://github.com/Acme/App.git,
// git@github.com:acme/app and ssh://git@github.com/acme/app all compare equal.
// It returns "" for a value that is not a recognizable remote.
func NormalizeRemoteURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var host, remotePath string
	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Hostname() == "" {
			return ""
		}
		host, remotePath = parsed.Hostname(), parsed.Path
	} else if at := strings.Index(raw, "@"); at >= 0 && strings.Contains(raw[at:], ":") {
		// scp-like syntax: [user@]host:path
		rest := raw[at+1:]
		colon := strings.Index(rest, ":")
		host, remotePath = rest[:colon], rest[colon+1:]
	} else if colon := strings.Index(raw, ":"); colon > 0 && !strings.Contains(raw[:colon], "/") {
		host, remotePath = raw[:colon], raw[colon+1:]
	} else {
		return ""
	}
	remotePath = strings.Trim(remotePath, "/")
	remotePath = strings.TrimSuffix(remotePath, ".git")
	remotePath = strings.Trim(remotePath, "/")
	if host == "" || remotePath == "" {
		return ""
	}
	return strings.ToLower(host + "/" + remotePath)
}

// ExistingCheckoutCandidates lists the directories AO searches for a checkout
// the template already cloned: every directory directly under home/work, then
// every directory directly under home. Hidden directories and the AO-owned
// paths in exclude are skipped; each group is sorted so the search is
// deterministic.
func ExistingCheckoutCandidates(home string, exclude ...string) []string {
	home = filepath.Clean(strings.TrimSpace(home))
	if !filepath.IsAbs(home) || home == string(filepath.Separator) {
		return nil
	}
	skip := map[string]bool{}
	for _, path := range exclude {
		if path = strings.TrimSpace(path); path != "" {
			skip[filepath.Clean(path)] = true
		}
	}
	var candidates []string
	for _, root := range []string{filepath.Join(home, "work"), home} {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		var names []string
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			names = append(names, entry.Name())
		}
		sort.Strings(names)
		for _, name := range names {
			candidate := filepath.Join(root, name)
			if skip[candidate] {
				continue
			}
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

// FindExistingCheckout returns the first candidate directory (see
// ExistingCheckoutCandidates) that is a git checkout whose origin remote
// matches repositoryURL after NormalizeRemoteURL.
func FindExistingCheckout(ctx context.Context, runner GitRunner, home, repositoryURL string, exclude ...string) (string, bool) {
	want := NormalizeRemoteURL(repositoryURL)
	if want == "" || runner == nil {
		return "", false
	}
	for _, candidate := range ExistingCheckoutCandidates(home, exclude...) {
		if _, err := os.Stat(filepath.Join(candidate, ".git")); err != nil {
			continue
		}
		output, err := runner.Run(ctx, candidate, nil, "remote", "get-url", "origin")
		if err != nil {
			continue
		}
		if NormalizeRemoteURL(strings.TrimSpace(output)) == want {
			return candidate, true
		}
	}
	return "", false
}

// ReuseDecision is the outcome of ResolveReusedCheckout.
type ReuseDecision struct {
	// Workspace is the worktree root the session uses: the adopted checkout,
	// or the default workspace when nothing was adopted.
	Workspace string
	// Reused is true when Workspace is an existing checkout the template made.
	Reused bool
	// AdoptedNow is true when this call switched the checkout onto the
	// session branch (first adoption, as opposed to a worker restart).
	AdoptedNow bool
	// Stash names the stash entry holding changes the checkout had before
	// AO switched it to the session branch, when there were any.
	Stash string
	// Reason explains, for the log, why the default workspace was kept.
	Reason string
}

// ResolveReusedCheckout decides where a session works when the connection
// asked to reuse the template's checkout. The decision is recorded in dataDir
// and honored on every later worker start. It only does local git work: the
// adopted checkout is switched to the session branch here (from the local
// origin/<defaultBranch>, refreshed later by AdvanceAdoptedCheckout), so a
// checkout that cannot be adopted falls back to the default workspace before
// anything else starts using the path.
//
// Uncommitted changes to tracked files are never discarded: they are saved in
// a stash named after the session, and when that is not possible the checkout
// is left untouched and the session clones into the default workspace.
func ResolveReusedCheckout(
	ctx context.Context,
	runner GitRunner,
	home, defaultWorkspace, dataDir, repositoryURL, sessionID, branch, defaultBranch string,
) ReuseDecision {
	keep := ReuseDecision{Workspace: defaultWorkspace}
	recorded := filepath.Join(dataDir, checkoutRootFile)
	if raw, err := os.ReadFile(recorded); err == nil {
		root := filepath.Clean(strings.TrimSpace(string(raw)))
		if filepath.IsAbs(root) && root != filepath.Clean(defaultWorkspace) {
			if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
				return ReuseDecision{Workspace: root, Reused: true}
			}
			keep.Reason = "the recorded checkout " + root + " is no longer a git checkout"
			return keep
		}
		keep.Reason = "an earlier worker start chose the default workspace"
		return keep
	}
	record := func(decision ReuseDecision) ReuseDecision {
		if err := os.MkdirAll(dataDir, 0o700); err == nil {
			_ = os.WriteFile(recorded, []byte(decision.Workspace+"\n"), 0o600)
		}
		return decision
	}
	candidate, found := FindExistingCheckout(ctx, runner, home, repositoryURL,
		defaultWorkspace, filepath.Dir(filepath.Clean(dataDir)))
	if !found {
		keep.Reason = "no existing checkout of the repository under " + filepath.Join(home, "work") + " or " + home
		return record(keep)
	}
	stash, err := adoptExistingCheckout(ctx, runner, candidate, sessionID, branch, defaultBranch)
	if err != nil {
		keep.Reason = fmt.Sprintf("existing checkout %s was not adopted: %v", candidate, err)
		return record(keep)
	}
	return record(ReuseDecision{Workspace: candidate, Reused: true, AdoptedNow: true, Stash: stash})
}

// adoptExistingCheckout puts an existing checkout on the session branch. A
// checkout already on that branch is left as it is. Otherwise tracked
// changes are stashed under a session-named message, and the branch is
// created from origin/<defaultBranch> (or switched to, if it exists). Any
// failure restores the stash and reports errCheckoutNotReusable.
func adoptExistingCheckout(ctx context.Context, runner GitRunner, dir, sessionID, branch, defaultBranch string) (string, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return "", fmt.Errorf("%w: the session has no branch", errCheckoutNotReusable)
	}
	if current, err := runner.Run(ctx, dir, nil, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil &&
		strings.TrimSpace(current) == branch {
		return "", nil
	}
	if _, err := runner.Run(ctx, dir, nil, "rev-parse", "--verify", "--quiet", "HEAD"); err != nil {
		return "", fmt.Errorf("%w: it has no commits", errCheckoutNotReusable)
	}
	status, err := runner.Run(ctx, dir, nil, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return "", fmt.Errorf("%w: read status: %v", errCheckoutNotReusable, err)
	}
	stash := ""
	if strings.TrimSpace(status) != "" {
		stash = "ao-session " + sessionID + ": changes found before switching to " + branch +
			" at " + time.Now().UTC().Format(time.RFC3339)
		if _, err := runner.Run(ctx, dir, nil,
			"-c", "user.name="+cloudGitAuthorName, "-c", "user.email="+cloudGitAuthorEmail,
			"stash", "push", "--message", stash); err != nil {
			return "", fmt.Errorf("%w: its uncommitted changes could not be stashed: %v", errCheckoutNotReusable, err)
		}
	}
	restore := func(cause error) (string, error) {
		if stash != "" {
			if _, err := runner.Run(ctx, dir, nil, "stash", "pop", "--index"); err != nil {
				cause = fmt.Errorf("%v (its changes remain in the stash %q: %v)", cause, stash, err)
			}
		}
		return "", fmt.Errorf("%w: %v", errCheckoutNotReusable, cause)
	}
	if _, err := runner.Run(ctx, dir, nil, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		if _, err := runner.Run(ctx, dir, nil, "checkout", branch); err != nil {
			return restore(fmt.Errorf("switch to %s: %v", branch, err))
		}
		return stash, nil
	}
	base := "HEAD"
	if defaultBranch = strings.TrimSpace(defaultBranch); defaultBranch != "" {
		if _, err := runner.Run(ctx, dir, nil, "rev-parse", "--verify", "--quiet",
			"refs/remotes/origin/"+defaultBranch+"^{commit}"); err == nil {
			base = "refs/remotes/origin/" + defaultBranch
		}
	}
	if _, err := runner.Run(ctx, dir, nil, "checkout", "--no-track", "-b", branch, base); err != nil {
		return restore(fmt.Errorf("create %s: %v", branch, err))
	}
	return stash, nil
}

// PrepareReusedCheckout refreshes an adopted checkout with the session's
// checkout grant: it checks the origin is the authorized repository and
// fetches it. The fetch clears any credential helper the checkout or the
// user configured (git -c credential.helper=) so only the grant is used;
// nothing is written to the user's git configuration.
func PrepareReusedCheckout(ctx context.Context, runner GitRunner, workspace string, grant CheckoutGrantResponse) error {
	if runner == nil {
		return errors.New("git runner is required")
	}
	expected, err := githubRepositoryIdentity(grant.CloneURL)
	if err != nil {
		return fmt.Errorf("validate checkout grant: %w", err)
	}
	if grant.Token != "" && !grant.ExpiresAt.After(time.Now().Add(30*time.Second)) {
		return errors.New("checkout grant is expired")
	}
	output, err := runner.Run(ctx, workspace, nil, "remote", "get-url", "origin")
	if err != nil {
		return fmt.Errorf("read workspace origin: %w", err)
	}
	if NormalizeRemoteURL(output) != NormalizeRemoteURL("https://github.com/"+expected) {
		return errors.New("workspace origin does not match the authorized repository")
	}
	// Fetch from the authorized URL rather than the named remote: a template
	// may have cloned over SSH, where the grant cannot authenticate. The
	// refspec keeps origin's remote-tracking refs current either way.
	return withGitCredential(grant.Token, func(env map[string]string) error {
		_, err := runner.Run(ctx, workspace, env, "-c", "credential.helper=",
			"fetch", "--prune", "--", grant.CloneURL, "+refs/heads/*:refs/remotes/origin/*")
		return err
	})
}

// AdvanceAdoptedCheckout fast-forwards a just-adopted session branch to the
// freshly fetched origin/<defaultBranch>, so the session starts from the
// latest base rather than the template's possibly stale clone. It never
// merges or rewrites: a branch that is not strictly behind is left alone.
func AdvanceAdoptedCheckout(ctx context.Context, runner GitRunner, workspace, defaultBranch string) error {
	defaultBranch = strings.TrimSpace(defaultBranch)
	if defaultBranch == "" {
		return nil
	}
	upstream := "refs/remotes/origin/" + defaultBranch
	if _, err := runner.Run(ctx, workspace, nil, "rev-parse", "--verify", "--quiet", upstream+"^{commit}"); err != nil {
		return nil
	}
	_, err := runner.Run(ctx, workspace, nil, "merge", "--ff-only", "--quiet", upstream)
	return err
}

// ReusedCheckoutGitEnvironment scopes AO's git setup to the worker's process
// tree through git's environment configuration (the equivalent of git -c)
// instead of writing it into the reused checkout or the user's global config:
// the session credential helper (after clearing any other helper), per-path
// credential scoping, and the AO commit identity. Every git command the worker
// and its agent run inherits it; the user's own shells outside AO do not.
func ReusedCheckoutGitEnvironment(dataDir string) map[string]string {
	entries := [][2]string{
		{"credential.helper", ""},
		{"credential.helper", GitCredentialHelperPath(dataDir)},
		{"credential.useHttpPath", "true"},
		{"user.name", cloudGitAuthorName},
		{"user.email", cloudGitAuthorEmail},
	}
	environment := map[string]string{"GIT_CONFIG_COUNT": strconv.Itoa(len(entries))}
	for index, entry := range entries {
		environment["GIT_CONFIG_KEY_"+strconv.Itoa(index)] = entry[0]
		environment["GIT_CONFIG_VALUE_"+strconv.Itoa(index)] = entry[1]
	}
	return environment
}

// ConfigureReusedCheckoutGit writes the session's git tooling (the credential
// helper and gh wrapper) for a reused checkout without touching that
// checkout's configuration or switching its branch: ResolveReusedCheckout
// already put it on the session branch, and ReusedCheckoutGitEnvironment
// points git at the helper.
func ConfigureReusedCheckoutGit(dataDir, publicURL, sessionID string) error {
	return writeWorkerGitTooling(dataDir, publicURL, sessionID)
}
