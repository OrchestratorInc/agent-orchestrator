package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const scratchRepositoryHost = "scratch.ao.local"

const (
	cloudGitAuthorName  = "AO Cloud Agent"
	cloudGitAuthorEmail = "noreply@aoagents.com"
	// WorkspaceReviewBaseRef is an immutable, AO-owned baseline stored with the
	// checkout so committed changes survive worker restarts and restores.
	WorkspaceReviewBaseRef = "refs/ao/diff-base"
)

type GitRunner interface {
	Run(context.Context, string, map[string]string, ...string) (string, error)
}

type ExecGitRunner struct{}

func (ExecGitRunner) Run(ctx context.Context, dir string, env map[string]string, args ...string) (string, error) {
	// Pin git wire protocol v0. git 2.39 defaults to protocol v2 over HTTP/2,
	// which is mangled through some sandbox egress paths: the ref-listing
	// handshake fails with "expected flush after ref listing" plus a spurious
	// "could not read Username" prompt, crash-looping checkout for public repos.
	// v0 is universally compatible; the only cost is slightly larger fetches.
	command := exec.CommandContext(ctx, "git", append([]string{"-c", "protocol.version=0"}, args...)...)
	command.Dir = dir
	command.Env = replaceEnvironment(os.Environ(), env)
	// Abort a stalled HTTP transfer instead of hanging until the whole sandbox is
	// torn down. If throughput stays under 1 KB/s for 60s, git fails the
	// clone/fetch/push. A stalled clone otherwise blocks worker startup - and thus
	// the agent terminal, which is only created after checkout - indefinitely,
	// which is exactly the multi-minute "terminal never connects" stall we saw.
	// This targets stalls, not slow-but-progressing transfers, so a large repo
	// still clones; and it is a no-op for local git ops, which do no HTTP.
	command.Env = append(command.Env,
		"GIT_HTTP_LOW_SPEED_LIMIT=1000",
		"GIT_HTTP_LOW_SPEED_TIME=60",
	)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(output.String())
		for _, secret := range env {
			if secret != "" {
				message = strings.ReplaceAll(message, secret, "[REDACTED]")
			}
		}
		if message == "" {
			return "", fmt.Errorf("git command failed: %w", err)
		}
		return "", fmt.Errorf("git command failed: %s", message)
	}
	return output.String(), nil
}

// CheckoutOptions tunes how PrepareCheckout clones a fresh workspace.
type CheckoutOptions struct {
	// PartialClone clones history without the contents of older file versions
	// (--filter=blob:none). Only Freestyle sets it (AO_WORKER_PARTIAL_CLONE=1),
	// matching the partial clone its project snapshots bake. Every other
	// provider (Coder, BYO Coder, Docker) keeps a full clone, so offline history
	// operations never need a lazy blob fetch.
	PartialClone bool
}

// PartialCloneEnv is the worker environment variable a provider sets to "1"
// to opt its sessions into partial clones (see CheckoutOptions.PartialClone).
const PartialCloneEnv = "AO_WORKER_PARTIAL_CLONE"

// PrepareCheckout clones once, then validates and fetches the persistent
// workspace. The token exists only in the network command's askpass
// environment. A fresh clone is a full clone; see PrepareCheckoutWithOptions.
func PrepareCheckout(ctx context.Context, runner GitRunner, workspace string, grant CheckoutGrantResponse) error {
	return PrepareCheckoutWithOptions(ctx, runner, workspace, grant, CheckoutOptions{})
}

// PrepareCheckoutWithOptions is PrepareCheckout with explicit clone options.
func PrepareCheckoutWithOptions(
	ctx context.Context,
	runner GitRunner,
	workspace string,
	grant CheckoutGrantResponse,
	options CheckoutOptions,
) error {
	if runner == nil {
		return errors.New("git runner is required")
	}
	workspace = filepath.Clean(strings.TrimSpace(workspace))
	if !filepath.IsAbs(workspace) || workspace == string(filepath.Separator) {
		return errors.New("workspace path must be an absolute non-root directory")
	}
	expected, err := githubRepositoryIdentity(grant.CloneURL)
	if err != nil {
		return fmt.Errorf("validate checkout grant: %w", err)
	}
	if grant.Token != "" && !grant.ExpiresAt.After(time.Now().Add(30*time.Second)) {
		return errors.New("checkout grant is expired")
	}
	info, statErr := os.Stat(workspace)
	if statErr == nil {
		if !info.IsDir() {
			return errors.New("workspace path is not a directory")
		}
		entries, err := os.ReadDir(workspace)
		if err != nil {
			return fmt.Errorf("inspect workspace contents: %w", err)
		}
		if len(entries) == 0 {
			statErr = os.ErrNotExist
		} else if _, gitErr := os.Stat(filepath.Join(workspace, ".git")); gitErr == nil {
			if err := validateOrigin(ctx, runner, workspace, expected); err != nil {
				return err
			}
			return withGitCredential(grant.Token, func(env map[string]string) error {
				if _, err := runner.Run(ctx, workspace, env, "fetch", "--prune", "--", "origin"); err != nil {
					return err
				}
				return adoptProjectSnapshot(ctx, runner, workspace, env)
			})
		} else {
			// The workspace is non-empty but not a Git checkout. This happens
			// when the coding agent starts first and writes files (for example
			// .claude) into the workspace before the checkout runs. Clone into a
			// staging directory and merge the result in, so a bare git clone into
			// a non-empty directory does not fail and checkout no longer depends
			// on agent-vs-checkout startup ordering.
			return cloneIntoNonEmptyWorkspace(ctx, runner, workspace, grant, expected, options)
		}
	}
	if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect workspace: %w", statErr)
	}
	if err := os.MkdirAll(filepath.Dir(workspace), 0o700); err != nil {
		return fmt.Errorf("create workspace parent: %w", err)
	}
	if err := withGitCredential(grant.Token, func(env map[string]string) error {
		_, err := runner.Run(ctx, filepath.Dir(workspace), env,
			cloneArgs(options, grant.CloneURL, workspace)...)
		return err
	}); err != nil {
		return err
	}
	return validateOrigin(ctx, runner, workspace, expected)
}

// EnsureWorkspaceReviewBase records the checkout's comparison baseline once.
// Existing refs are deliberately left untouched even when a later fetch moves
// origin/<defaultBranch>.
func EnsureWorkspaceReviewBase(ctx context.Context, runner GitRunner, workspace, defaultBranch string) error {
	if runner == nil {
		return errors.New("git runner is required")
	}
	// A scratch / freshly-initialized workspace has an unborn HEAD (git init with
	// no commit), so there is no history to anchor a review base to. Treat it as a
	// no-op instead of failing worker startup on the later rev-list HEAD, which
	// aborts every no-repo session across all providers.
	if _, err := runner.Run(ctx, workspace, nil, "rev-parse", "--verify", "HEAD"); err != nil {
		return nil
	}
	existingOutput, existingErr := runner.Run(ctx, workspace, nil, "rev-parse", "--verify", WorkspaceReviewBaseRef)
	configuredCandidate := ""
	if branch := strings.TrimSpace(defaultBranch); branch != "" {
		if output, err := runner.Run(ctx, workspace, nil, "merge-base", "origin/"+branch, "HEAD"); err == nil {
			configuredCandidate = strings.TrimSpace(output)
		}
	}
	remoteHeadCandidate := ""
	if configuredCandidate == "" {
		if output, err := runner.Run(ctx, workspace, nil, "merge-base", "refs/remotes/origin/HEAD", "HEAD"); err == nil {
			remoteHeadCandidate = strings.TrimSpace(output)
		}
	}
	if existingErr == nil {
		// Older workers fell straight back to the repository root when a project
		// called its default branch "main" but the remote used "master" (or vice
		// versa). Repair only that recognizable legacy fallback; every non-root
		// baseline remains immutable across fetches and restores.
		if configuredCandidate != "" || remoteHeadCandidate == "" {
			return nil
		}
		rootOutput, err := runner.Run(ctx, workspace, nil, "rev-list", "--max-parents=0", "--reverse", "HEAD")
		if err != nil {
			return nil
		}
		root := strings.TrimSpace(strings.SplitN(rootOutput, "\n", 2)[0])
		if strings.TrimSpace(existingOutput) != root || remoteHeadCandidate == root {
			return nil
		}
		if _, err := runner.Run(ctx, workspace, nil, "update-ref", WorkspaceReviewBaseRef, remoteHeadCandidate); err != nil {
			return fmt.Errorf("repair workspace review base: %w", err)
		}
		return nil
	}

	candidate := configuredCandidate
	if candidate == "" {
		candidate = remoteHeadCandidate
	}
	if candidate == "" {
		output, err := runner.Run(ctx, workspace, nil, "rev-list", "--max-parents=0", "--reverse", "HEAD")
		if err != nil {
			return fmt.Errorf("resolve workspace review base: %w", err)
		}
		candidate = strings.TrimSpace(strings.SplitN(output, "\n", 2)[0])
	}
	if candidate == "" {
		return errors.New("resolve workspace review base: repository has no commits")
	}
	if _, err := runner.Run(ctx, workspace, nil, "update-ref", WorkspaceReviewBaseRef, candidate); err != nil {
		return fmt.Errorf("record workspace review base: %w", err)
	}
	return nil
}

// cloneIntoNonEmptyWorkspace clones the authorized repository into a staging
// directory and merges the result into a workspace that already contains files
// the coding agent wrote (for example .claude) before the checkout ran. Files
// the clone did not produce are preserved; the clone's .git directory and
// tracked files are moved in. This removes the ordering dependency between
// agent startup and repository checkout.
func cloneIntoNonEmptyWorkspace(
	ctx context.Context,
	runner GitRunner,
	workspace string,
	grant CheckoutGrantResponse,
	expected string,
	options CheckoutOptions,
) error {
	// Stage inside the workspace itself, not its parent. The parent is the
	// provider's durable root (e.g. Coder's /home/coder), which is owned by the
	// provider's own user and is not writable by the AO worker user, so a
	// staging dir there fails with "permission denied". The workspace, by
	// contrast, is always writable by the worker (the agent just wrote into it,
	// which is why this non-empty path runs) and is on the same filesystem, so
	// the entry moves below stay a same-filesystem rename. The hidden staging
	// dir is removed before the checkout returns.
	staging, err := os.MkdirTemp(workspace, ".ao-checkout-")
	if err != nil {
		return fmt.Errorf("create checkout staging directory: %w", err)
	}
	defer os.RemoveAll(staging)
	clone := filepath.Join(staging, "repository")
	if err := withGitCredential(grant.Token, func(env map[string]string) error {
		_, err := runner.Run(ctx, staging, env, cloneArgs(options, grant.CloneURL, clone)...)
		return err
	}); err != nil {
		return err
	}
	if err := validateOrigin(ctx, runner, clone, expected); err != nil {
		return err
	}
	entries, err := os.ReadDir(clone)
	if err != nil {
		return fmt.Errorf("read cloned repository: %w", err)
	}
	for _, entry := range entries {
		destination := filepath.Join(workspace, entry.Name())
		if _, statErr := os.Stat(destination); statErr == nil {
			// A pre-existing file the agent wrote (for example .claude) stays.
			continue
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("inspect workspace entry %s: %w", entry.Name(), statErr)
		}
		if err := os.Rename(filepath.Join(clone, entry.Name()), destination); err != nil {
			return fmt.Errorf("move %s into workspace: %w", entry.Name(), err)
		}
	}
	return nil
}

// partialCloneFilter clones history without the contents of older file
// versions. The checked-out tree is still complete; an older version is fetched
// on demand (blame, log -p, checking out another commit) through the repo's
// credential helper, which brokers a fresh token for every network operation.
// For a mature repository this cuts the clone to a fraction of its full size.
// It is applied only when CheckoutOptions.PartialClone is set (Freestyle).
const partialCloneFilter = "--filter=blob:none"

// cloneArgs builds the primary-checkout clone command.
func cloneArgs(options CheckoutOptions, cloneURL, destination string) []string {
	args := []string{"clone", "--origin", "origin", "--no-tags"}
	if options.PartialClone {
		args = append(args, partialCloneFilter)
	}
	return append(args, "--", cloneURL, destination)
}

// ConfigureWorkerGit prepares the assigned branch and a repo-local credential
// helper that brokers a fresh scoped token for each GitHub network operation.
// The helper stores only the rotating worker-token path, never a GitHub token.
func ConfigureWorkerGit(
	ctx context.Context,
	runner GitRunner,
	workspace, dataDir, publicURL, sessionID, branch string,
) error {
	if runner == nil {
		return errors.New("git runner is required")
	}
	for label, value := range map[string]string{
		"workspace": workspace, "data directory": dataDir, "public URL": publicURL,
		"session ID": sessionID, "branch": branch,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", label)
		}
	}
	if err := writeWorkerGitTooling(dataDir, publicURL, sessionID); err != nil {
		return err
	}
	helperPath := GitCredentialHelperPath(dataDir)
	commands := [][]string{
		{"config", "--local", "--replace-all", "credential.helper", ""},
		{"config", "--local", "--add", "credential.helper", helperPath},
		{"config", "--local", "--replace-all", "credential.useHttpPath", "true"},
		{"config", "--local", "--replace-all", "user.name", cloudGitAuthorName},
		{"config", "--local", "--replace-all", "user.email", cloudGitAuthorEmail},
	}
	for _, command := range commands {
		if _, err := runner.Run(ctx, workspace, nil, command...); err != nil {
			return fmt.Errorf("configure worker Git repository: %w", err)
		}
	}
	// A restarted worker already has the local branch: it switches to it as it
	// is, so the agent's commits on it survive even when HEAD was left on
	// another branch. A branch that already exists on the remote is continued
	// from its commits; when local files block that switch the session stops
	// with ErrSessionBranchSwitch instead of quietly starting the branch from
	// the default branch, which would hide the remote work and fail every push.
	// Only a new branch starts at HEAD.
	if _, err := runner.Run(ctx, workspace, nil, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		if _, err := runner.Run(ctx, workspace, nil, "checkout", branch); err == nil {
			return nil
		} else if stashErr := stashBeforeSessionBranch(ctx, runner, workspace, sessionID, branch); stashErr != nil {
			return fmt.Errorf("%w: switch to %s: %v", ErrSessionBranchSwitch, branch, err)
		}
		// The agent left uncommitted changes on another branch that block the
		// switch back: they are kept in a session-named stash.
		if _, err := runner.Run(ctx, workspace, nil, "checkout", branch); err != nil {
			return fmt.Errorf("%w: switch to %s: %v", ErrSessionBranchSwitch, branch, err)
		}
		return fmt.Errorf("%w: uncommitted changes that blocked switching back to %s were stashed", ErrSessionBranchStashed, branch)
	}
	if remote := remoteBranchRef(ctx, runner, workspace, branch); remote != "" {
		if _, err := runner.Run(ctx, workspace, nil, "checkout", "--no-track", "-B", branch, remote); err != nil {
			if !GeneratedSessionBranch(sessionID, branch) {
				return fmt.Errorf("%w: %s exists on the repository but could not be checked out from origin/%s: %v",
					ErrSessionBranchSwitch, branch, branch, err)
			}
			// The session's own generated branch, pushed before its sandbox
			// was re-created: start it at HEAD as sessions always did, and
			// tell the worker, rather than stop a session nobody asked to
			// continue that branch.
			if _, fallbackErr := runner.Run(ctx, workspace, nil, "checkout", "-B", branch); fallbackErr != nil {
				return fmt.Errorf("configure worker Git repository: %w", fallbackErr)
			}
			return fmt.Errorf("%w: %s could not continue from origin/%s and starts from the default branch: %v",
				ErrSessionBranchFellBack, branch, branch, err)
		}
		return nil
	}
	if _, err := runner.Run(ctx, workspace, nil, "checkout", "-b", branch); err != nil {
		return fmt.Errorf("configure worker Git repository: %w", err)
	}
	return nil
}

// ErrSessionBranchSwitch means the checkout could not be put on the session's
// branch: its existing local branch, or the commits already pushed to it, so
// the session must not start on a branch cut from somewhere else.
var ErrSessionBranchSwitch = errors.New("the session branch could not be checked out")

// stashBeforeSessionBranch stashes tracked and untracked changes under a
// session-named message so the checkout can switch back to the session branch.
func stashBeforeSessionBranch(ctx context.Context, runner GitRunner, dir, sessionID, branch string) error {
	message := "ao-session " + sessionID + ": changes found before switching back to " + branch +
		" at " + time.Now().UTC().Format(time.RFC3339)
	_, err := runner.Run(ctx, dir, nil,
		"-c", "user.name="+cloudGitAuthorName, "-c", "user.email="+cloudGitAuthorEmail,
		"stash", "push", "--include-untracked", "--message", message)
	return err
}

// ErrSessionBranchStashed is not a failure: changes that blocked switching back
// to the session branch were stashed under a session-named message.
var ErrSessionBranchStashed = errors.New("changes were stashed to switch back to the session branch")

// ErrSessionBranchFellBack is not a failure: the session's own generated
// branch could not continue its pushed commits and started from the default
// branch instead. The worker logs it and carries on.
var ErrSessionBranchFellBack = errors.New("the session branch started from the default branch")

// GeneratedSessionBranch reports whether branch is the one the control plane
// generates for this session, <prefix>/<first 8 characters of its id>, as
// opposed to a branch someone asked the session to continue.
func GeneratedSessionBranch(sessionID, branch string) bool {
	id := strings.TrimSpace(sessionID)
	if len(id) < 8 {
		return false
	}
	prefix, short, ok := strings.Cut(strings.TrimSpace(branch), "/")
	return ok && prefix != "" && !strings.Contains(short, "/") && short == id[:8]
}

// ErrSessionBranchIsBase means an additional repository's base branch has the
// session branch's name, so the session works on that repository at its base
// without checking out or pushing the session branch there.
var ErrSessionBranchIsBase = errors.New("the session branch is this repository's base branch")

// remoteBranchRef returns refs/remotes/origin/<branch> when the checkout has
// that remote-tracking branch, and "" otherwise.
func remoteBranchRef(ctx context.Context, runner GitRunner, dir, branch string) string {
	if branch = strings.TrimSpace(branch); branch == "" {
		return ""
	}
	ref := "refs/remotes/origin/" + branch
	if _, err := runner.Run(ctx, dir, nil, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
		return ""
	}
	return ref
}

// writeWorkerGitTooling writes the session's git credential helper and gh
// wrapper into the worker data directory.
func writeWorkerGitTooling(dataDir, publicURL, sessionID string) error {
	for label, value := range map[string]string{
		"data directory": dataDir, "public URL": publicURL, "session ID": sessionID,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", label)
		}
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("create worker Git credential directory: %w", err)
	}
	binDir := filepath.Join(dataDir, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		return fmt.Errorf("create worker tooling directory: %w", err)
	}
	helperPath := GitCredentialHelperPath(dataDir)
	helper := fmt.Sprintf(`#!/bin/sh
set -eu
[ "${1:-}" = "get" ] || exit 0
# git supplies the request as protocol/host/path lines on stdin (path is
# included because credential.useHttpPath is true). Read them so the token can
# be scoped to the exact repository being fetched or pushed — the primary
# checkout or a declared extra dev-kit repository. Any parse miss leaves
# repo_query empty and the control plane issues the broad multi-repository
# grant, so this never narrows a request it cannot classify.
req_host=""
req_path=""
while IFS='=' read -r key value; do
  [ -n "$key" ] || break
  case "$key" in
    host) req_host="$value" ;;
    path) req_path="$value" ;;
  esac
done
# Only ever hand the GitHub installation token to github.com. This helper is
# registered unscoped and credential.useHttpPath makes git invoke it for EVERY
# host, so without this gate a sandbox git operation against a non-github remote
# (which the coding agent can add) would be handed the installation token. exit 0
# returns no credential, so git falls through rather than leaking it.
[ "$req_host" = "github.com" ] || exit 0
repo_query=""
if [ -n "$req_path" ]; then
  repo="${req_path%%.git}"
  repo="${repo#/}"
  # Only a real owner/repo (exactly two segments) is forwarded; a single-segment
  # or multi-segment path leaves repo_query empty → the broad multi-repository
  # grant, never a bogus scoped request.
  case "$repo" in
    */*)
      owner="${repo%%%%/*}"
      name="${repo#*/}"
      case "$name" in */*) name="" ;; esac
      if [ -n "$owner" ] && [ -n "$name" ]; then
        repo_query="?repo=${owner}/${name}"
      fi
      ;;
  esac
fi
worker_token="$(tr -d '\r\n' < %s)"
token_url=%s
response="$(curl -fsS --connect-timeout 10 --max-time 30 -X POST \
  -H "Authorization: Worker ${worker_token}" \
  -H "X-AO-Session-ID: %s" \
  "${token_url}${repo_query}")"
# jq is not on every workspace image (BYO templates), so parse the token with
# sed when it is missing. GitHub tokens are [A-Za-z0-9_], so the pattern is exact.
ao_extract_token() {
  if command -v jq >/dev/null 2>&1; then
    printf '%%s' "$1" | jq -er '.token | select(type == "string" and length > 0)'
    return
  fi
  token="$(printf '%%s' "$1" | tr -d '\r\n' | sed -n 's/.*"token"[[:space:]]*:[[:space:]]*"\([A-Za-z0-9_.-]*\)".*/\1/p')"
  [ -n "$token" ] || { echo "AO could not read the GitHub token response" >&2; return 1; }
  printf '%%s\n' "$token"
}
github_token="$(ao_extract_token "$response")"
printf 'username=x-access-token\npassword=%%s\n' "$github_token"
`, shellQuote(filepath.Join(dataDir, "worker-token")),
		shellQuote(strings.TrimRight(publicURL, "/")+"/api/cloud/v1/worker/github-token"),
		sessionID)
	if err := os.WriteFile(helperPath, []byte(helper), 0o700); err != nil {
		return fmt.Errorf("write worker Git credential helper: %w", err)
	}
	githubWrapper := fmt.Sprintf(`#!/bin/sh
set -eu
# jq is not on every workspace image (BYO templates), so parse the token with
# sed when it is missing. GitHub tokens are [A-Za-z0-9_], so the pattern is exact.
ao_extract_token() {
  if command -v jq >/dev/null 2>&1; then
    printf '%%s' "$1" | jq -er '.token | select(type == "string" and length > 0)'
    return
  fi
  token="$(printf '%%s' "$1" | tr -d '\r\n' | sed -n 's/.*"token"[[:space:]]*:[[:space:]]*"\([A-Za-z0-9_.-]*\)".*/\1/p')"
  [ -n "$token" ] || { echo "AO could not read the GitHub token response" >&2; return 1; }
  printf '%%s\n' "$token"
}
real_gh="${AO_GH_REAL_BINARY:-}"
if [ -z "$real_gh" ]; then
  for candidate in /usr/local/bin/gh /usr/bin/gh; do
    if [ -x "$candidate" ]; then
      real_gh="$candidate"
      break
    fi
  done
fi
if [ -z "$real_gh" ] || [ ! -x "$real_gh" ]; then
  echo "AO GitHub CLI is unavailable; expected /usr/local/bin/gh or /usr/bin/gh" >&2
  exit 127
fi
if [ -n "${GH_TOKEN:-}" ]; then
  github_token="$GH_TOKEN"
elif [ -n "${GITHUB_TOKEN:-}" ]; then
  github_token="$GITHUB_TOKEN"
else
  worker_token="$(tr -d '\r\n' < %s)"
  response="$(curl -fsS --connect-timeout 10 --max-time 30 -X POST \
    -H "Authorization: Worker ${worker_token}" \
    -H "X-AO-Session-ID: %s" \
    %s)"
  github_token="$(ao_extract_token "$response")"
fi
if [ "${1:-}" = "pr" ] && [ "${2:-}" = "create" ]; then
  set +e
  output="$(GH_TOKEN="$github_token" "$real_gh" "$@" 2>&1)"
  status=$?
  set -e
  printf '%%s\n' "$output"
  if [ "$status" -ne 0 ]; then
    exit "$status"
  fi
  pr_url="$(printf '%%s\n' "$output" | sed -n 's#.*\(https://github.com/[^[:space:]]*/pull/[0-9][0-9]*\).*#\1#p' | tail -n 1)"
  if [ -z "$pr_url" ]; then
    echo "AO could not observe the pull request URL; run ao claim-pr <number-or-url>." >&2
    exit 1
  fi
  exec ao claim-pr "$pr_url"
fi
GH_TOKEN="$github_token" exec "$real_gh" "$@"
`, shellQuote(filepath.Join(dataDir, "worker-token")), sessionID,
		shellQuote(strings.TrimRight(publicURL, "/")+"/api/cloud/v1/worker/github-token"))
	if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte(githubWrapper), 0o700); err != nil {
		return fmt.Errorf("write worker GitHub CLI wrapper: %w", err)
	}
	return nil
}

func ToolingBinDir(dataDir string) string {
	return filepath.Join(dataDir, "bin")
}

// GitCredentialHelperPath is where ConfigureWorkerGit writes the repo-local
// credential helper that brokers fresh scoped GitHub tokens. Exported so extra
// dev-kit repositories can reuse the same session helper.
func GitCredentialHelperPath(dataDir string) string {
	return filepath.Join(dataDir, "git-credential-ao")
}

// CloneExtraRepo clones an additional dev-kit repository beside the primary
// checkout using the SAME askpass mechanism as the primary (the token lives only
// in the clone command's environment - never in the URL, the process argv, or
// the repository's .git/config, so the coding agent cannot read it back). It
// then points the repo at the session credential helper (already written by
// ConfigureWorkerGit for the primary checkout) so the agent's own git
// fetch/push in the extra repo keeps working after the short-lived clone token
// expires. cloneURL must be an uncredentialed GitHub URL.
func CloneExtraRepo(
	ctx context.Context,
	runner GitRunner,
	parentDir, dest, cloneURL, branch, token, dataDir string,
) error {
	if runner == nil {
		return errors.New("git runner is required")
	}
	// Extra repos are always full clones, whatever the provider.
	args := []string{"clone", "--origin", "origin", "--no-tags"}
	if strings.TrimSpace(branch) != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, "--", cloneURL, dest)
	if err := withGitCredential(token, func(env map[string]string) error {
		_, err := runner.Run(ctx, parentDir, env, args...)
		return err
	}); err != nil {
		return err
	}
	return configureExtraRepoGit(ctx, runner, dest, dataDir)
}

// configureExtraRepoGit points an additional repository at the session's
// credential helper and commit identity, as ConfigureWorkerGit does for the
// main checkout. Without a session helper (no main checkout was configured)
// the repository is left as it is: its origin is uncredentialed, so the
// agent's network operations fail rather than leak a token.
func configureExtraRepoGit(ctx context.Context, runner GitRunner, dest, dataDir string) error {
	helperPath := GitCredentialHelperPath(dataDir)
	if _, statErr := os.Stat(helperPath); statErr != nil {
		return nil
	}
	for _, command := range [][]string{
		{"config", "--local", "--replace-all", "credential.helper", ""},
		{"config", "--local", "--add", "credential.helper", helperPath},
		{"config", "--local", "--replace-all", "credential.useHttpPath", "true"},
		{"config", "--local", "--replace-all", "user.name", cloudGitAuthorName},
		{"config", "--local", "--replace-all", "user.email", cloudGitAuthorEmail},
	} {
		if _, err := runner.Run(ctx, dest, nil, command...); err != nil {
			return fmt.Errorf("configure extra repo git: %w", err)
		}
	}
	return nil
}

// ExtraRepoOptions configures PrepareExtraRepo.
type ExtraRepoOptions struct {
	SessionID string
	// SessionBranch is the main repository's session branch, checked out in
	// every additional repository too.
	SessionBranch string
	Token         string
	DataDir       string
	// Adopt means the session works in the user's own workspace (a reused
	// template checkout or a configured working directory), so an existing
	// checkout of the repository belongs to the user: it is adopted like the
	// main reused checkout, without writing AO's git configuration into it
	// (the worker's process-level ReusedCheckoutGitEnvironment supplies the
	// session credential helper and identity) and with any uncommitted
	// changes stashed before it switches to the session branch.
	Adopt bool
}

// PrepareExtraRepo makes one of the project's additional repositories ready
// beside the main checkout at dest. It clones the repository into a missing
// or empty folder. A checkout of the same repository already there is either
// AO's own (a worker restart or a stopped and started workspace), which is
// fetched, or, with opts.Adopt, the user's, which is fetched and adopted
// without touching its git configuration. It then puts the checkout on the
// session branch: an existing local branch is kept (the agent's commits on it
// survive), a branch already pushed to origin is checked out from there (a
// restored sandbox, or a session continuing a branch), and otherwise the
// branch starts from the repository's branch. A session branch named like the
// repository's base branch is never checked out there: the checkout stays at
// its base and ErrSessionBranchIsBase is returned. A folder holding anything
// else is left untouched and reported.
// adopted reports that an existing user checkout was adopted, so the caller
// must apply ReusedCheckoutGitEnvironment to the worker's process.
func PrepareExtraRepo(
	ctx context.Context,
	runner GitRunner,
	dest string,
	repo RepoRef,
	opts ExtraRepoOptions,
) (adopted bool, err error) {
	if runner == nil {
		return false, errors.New("git runner is required")
	}
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return false, fmt.Errorf("create the folder for additional repositories: %w", err)
	}
	existing, err := extraRepoCheckoutState(ctx, runner, dest, repo.URL)
	if err != nil {
		return false, err
	}
	branch := strings.TrimSpace(opts.SessionBranch)
	if existing && opts.Adopt {
		// Fetch from the repository URL rather than the named remote (the user
		// may have cloned over SSH) and clear any configured helper for this
		// one command, so only the grant is used and nothing is persisted.
		if err := withGitCredential(opts.Token, func(env map[string]string) error {
			_, fetchErr := runner.Run(ctx, dest, env, "-c", "credential.helper=",
				"fetch", "--prune", "--no-tags", "--", repo.URL, "+refs/heads/*:refs/remotes/origin/*")
			return fetchErr
		}); err != nil {
			return false, fmt.Errorf("fetch the existing checkout: %w", err)
		}
		base := extraRepoBaseBranch(ctx, runner, dest, repo.Branch)
		if sessionBranchIsBase(branch, base) {
			return true, sessionBranchIsBaseError(ctx, runner, dest, branch, base)
		}
		if branch != "" {
			// Adoption continues origin/<branch> when it was pushed and
			// otherwise starts the branch from base; this repository was
			// fetched above, so the created branch needs no later move.
			if _, _, err := adoptExistingCheckout(ctx, runner, dest, opts.SessionID, branch, base); err != nil {
				return false, err
			}
		}
		return true, EnsureWorkspaceReviewBase(ctx, runner, dest, base)
	}
	if existing {
		if err := withGitCredential(opts.Token, func(env map[string]string) error {
			_, fetchErr := runner.Run(ctx, dest, env, "fetch", "--no-tags", "--quiet", "origin")
			return fetchErr
		}); err != nil {
			return false, fmt.Errorf("fetch the existing checkout: %w", err)
		}
		if err := configureExtraRepoGit(ctx, runner, dest, opts.DataDir); err != nil {
			return false, err
		}
	} else if err := CloneExtraRepo(ctx, runner, parent, dest, repo.URL, repo.Branch, opts.Token, opts.DataDir); err != nil {
		return false, err
	}
	if base := extraRepoBaseBranch(ctx, runner, dest, repo.Branch); sessionBranchIsBase(branch, base) {
		return false, sessionBranchIsBaseError(ctx, runner, dest, branch, base)
	}
	if branch != "" {
		if _, err := runner.Run(ctx, dest, nil, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
			args := []string{"checkout", "-B", branch}
			if remoteBranchExists(ctx, runner, dest, branch) {
				args = append(args, "--no-track", "refs/remotes/origin/"+branch)
			}
			if _, err := runner.Run(ctx, dest, nil, args...); err != nil {
				return false, fmt.Errorf("check out the session branch: %w", err)
			}
		}
	}
	return false, EnsureWorkspaceReviewBase(ctx, runner, dest, extraRepoBaseBranch(ctx, runner, dest, repo.Branch))
}

// sessionBranchIsBase reports whether the session branch has the name of a
// repository's base branch, where a session must never push.
func sessionBranchIsBase(branch, base string) bool {
	branch, base = strings.TrimSpace(branch), strings.TrimSpace(base)
	return branch != "" && base != "" && strings.EqualFold(branch, base)
}

// sessionBranchIsBaseError keeps an additional repository at its base branch,
// with its review base recorded, and reports ErrSessionBranchIsBase so the
// caller says why it is not on the session branch.
func sessionBranchIsBaseError(ctx context.Context, runner GitRunner, dest, branch, base string) error {
	if err := EnsureWorkspaceReviewBase(ctx, runner, dest, base); err != nil {
		return err
	}
	return fmt.Errorf("%w: %s; it stays on %s and the session does not push to it", ErrSessionBranchIsBase, branch, base)
}

// SessionBranchIsBase reports whether branch is the base branch of the
// checkout at dir (configured is the project's branch for it, if any), so
// nothing pushes the session's work straight onto that base.
func SessionBranchIsBase(ctx context.Context, runner GitRunner, dir, configured, branch string) bool {
	return sessionBranchIsBase(branch, extraRepoBaseBranch(ctx, runner, dir, configured))
}

// remoteBranchExists reports whether origin has branch as a fetched
// remote-tracking ref.
func remoteBranchExists(ctx context.Context, runner GitRunner, dir, branch string) bool {
	_, err := runner.Run(ctx, dir, nil, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch+"^{commit}")
	return err == nil
}

// extraRepoBaseBranch is the branch an additional repository's work is based
// on: the project's configured branch for it, else origin's default branch.
func extraRepoBaseBranch(ctx context.Context, runner GitRunner, dir, configured string) string {
	if configured = strings.TrimSpace(configured); configured != "" {
		return configured
	}
	output, err := runner.Run(ctx, dir, nil, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(output), "origin/")
}

// extraRepoCheckoutState reports whether dest already holds a checkout of
// repoURL. A missing or empty folder is not a checkout (the clone creates or
// fills it); a folder holding anything else is an error, so nothing the
// workspace template or the user put there is overwritten.
func extraRepoCheckoutState(ctx context.Context, runner GitRunner, dest, repoURL string) (bool, error) {
	entries, err := os.ReadDir(dest)
	if errors.Is(err, os.ErrNotExist) || (err == nil && len(entries) == 0) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", dest, err)
	}
	if info, statErr := os.Stat(filepath.Join(dest, ".git")); statErr != nil || !info.IsDir() {
		return false, fmt.Errorf("%s already exists and is not a Git checkout", dest)
	}
	output, err := runner.Run(ctx, dest, nil, "remote", "get-url", "origin")
	if err != nil {
		return false, fmt.Errorf("%s has no origin remote: %w", dest, err)
	}
	if !sameRepository(strings.TrimSpace(output), repoURL) {
		return false, fmt.Errorf("%s is a checkout of a different repository", dest)
	}
	return true, nil
}

// sameRepository compares two repository URLs: by GitHub owner/name when both
// are GitHub URLs, otherwise as written without a trailing .git or slash.
func sameRepository(left, right string) bool {
	leftIdentity, leftErr := githubRepositoryIdentity(left)
	rightIdentity, rightErr := githubRepositoryIdentity(right)
	if leftErr == nil && rightErr == nil {
		return leftIdentity == rightIdentity
	}
	normalize := func(value string) string {
		return strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(value), "/"), ".git")
	}
	return normalize(left) == normalize(right)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

// PushBranch pushes the current HEAD to a remote branch using a fresh
// write-scoped grant. It never force-pushes: git push refuses non-fast-
// forward updates by default, so a branch that already has commits this
// workspace doesn't have fails loudly instead of silently discarding them.
func PushBranch(
	ctx context.Context,
	runner GitRunner,
	workspace, branch string,
	grant CheckoutGrantResponse,
) error {
	if runner == nil {
		return errors.New("git runner is required")
	}
	workspace = filepath.Clean(strings.TrimSpace(workspace))
	if !filepath.IsAbs(workspace) || workspace == string(filepath.Separator) {
		return errors.New("workspace path must be an absolute non-root directory")
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return errors.New("branch name is required")
	}
	if grant.Token != "" && !grant.ExpiresAt.After(time.Now().Add(30*time.Second)) {
		return errors.New("push grant is expired")
	}
	return withGitCredential(grant.Token, func(env map[string]string) error {
		_, err := runner.Run(ctx, workspace, env,
			"push", "--", "origin", "HEAD:refs/heads/"+branch)
		return err
	})
}

// IsScratchRepositoryURL identifies AO's non-network repository sentinel.
func IsScratchRepositoryURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil &&
		parsed.Scheme == "https" &&
		strings.EqualFold(parsed.Hostname(), scratchRepositoryHost) &&
		parsed.Port() == "" &&
		parsed.User == nil &&
		parsed.RawQuery == "" &&
		parsed.Fragment == "" &&
		strings.Trim(parsed.Path, "/") != ""
}

// PrepareScratchWorkspace initializes an empty persistent workspace as a Git
// repository. Existing scratch repositories survive worker replacements.
func PrepareScratchWorkspace(
	ctx context.Context,
	runner GitRunner,
	workspace string,
) error {
	if runner == nil {
		return errors.New("git runner is required")
	}
	workspace = filepath.Clean(strings.TrimSpace(workspace))
	if !filepath.IsAbs(workspace) || workspace == string(filepath.Separator) {
		return errors.New("workspace path must be an absolute non-root directory")
	}
	info, err := os.Stat(workspace)
	if err == nil {
		if !info.IsDir() {
			return errors.New("workspace path is not a directory")
		}
		entries, readErr := os.ReadDir(workspace)
		if readErr != nil {
			return fmt.Errorf("inspect workspace contents: %w", readErr)
		}
		if len(entries) > 0 {
			if gitInfo, statErr := os.Stat(filepath.Join(workspace, ".git")); statErr != nil ||
				!gitInfo.IsDir() {
				return errors.New("existing scratch workspace is not a Git repository")
			}
			if _, runErr := runner.Run(
				ctx, workspace, nil, "rev-parse", "--is-inside-work-tree",
			); runErr != nil {
				return fmt.Errorf("validate scratch workspace: %w", runErr)
			}
			return nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect workspace: %w", err)
	}
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		return fmt.Errorf("create scratch workspace: %w", err)
	}
	if _, err := runner.Run(
		ctx, workspace, nil, "init", "--initial-branch", "main",
	); err != nil {
		return fmt.Errorf("initialize scratch workspace: %w", err)
	}
	return nil
}

// PrepareStandaloneWorkspace prepares a standalone session's folder. An empty
// or missing folder becomes a fresh Git repository, as PrepareScratchWorkspace
// makes it. With adoptExisting, a folder that already holds files (a working
// directory the workspace template filled, such as ~/work with its own
// checkouts) is used exactly as it is: nothing is initialized or changed.
func PrepareStandaloneWorkspace(ctx context.Context, runner GitRunner, workspace string, adoptExisting bool) error {
	if adoptExisting {
		if entries, err := os.ReadDir(workspace); err == nil && len(entries) > 0 {
			return nil
		}
	}
	return PrepareScratchWorkspace(ctx, runner, workspace)
}

// ProjectSnapshotMarker, inside .git, marks a pristine checkout baked into a
// project snapshot. Its commit is as old as the snapshot, so the first
// checkout moves it to the latest origin/HEAD. The marker is then removed, so a
// worker restarting on a session's own workspace never resets its work.
const ProjectSnapshotMarker = "ao-project-snapshot"

func adoptProjectSnapshot(ctx context.Context, runner GitRunner, workspace string, env map[string]string) error {
	marker := filepath.Join(workspace, ".git", ProjectSnapshotMarker)
	if _, err := os.Stat(marker); err != nil {
		return nil
	}
	// An empty repository (or a remote that reports no default branch) has no
	// origin/HEAD: there is nothing newer to move to, so keep the checkout as
	// baked rather than fail the session.
	if _, err := runner.Run(ctx, workspace, env, "rev-parse", "--verify", "--quiet", "origin/HEAD^{commit}"); err == nil {
		// A partial clone downloads the new tree's file contents here, so this
		// runs with the same credential as the fetch.
		if _, err := runner.Run(ctx, workspace, env, "reset", "--hard", "--quiet", "origin/HEAD"); err != nil {
			return fmt.Errorf("move project snapshot checkout to origin/HEAD: %w", err)
		}
	}
	if err := os.Remove(marker); err != nil {
		return fmt.Errorf("clear project snapshot marker: %w", err)
	}
	return nil
}

func validateOrigin(ctx context.Context, runner GitRunner, workspace, expected string) error {
	if info, err := os.Stat(filepath.Join(workspace, ".git")); err != nil || !info.IsDir() {
		return errors.New("existing workspace is not a Git repository")
	}
	output, err := runner.Run(ctx, workspace, nil, "remote", "get-url", "origin")
	if err != nil {
		return fmt.Errorf("read workspace origin: %w", err)
	}
	actual, err := githubRepositoryIdentity(strings.TrimSpace(output))
	if err != nil || actual != expected {
		return errors.New("workspace origin does not match the authorized repository")
	}
	return nil
}

func withGitCredential(token string, operation func(map[string]string) error) error {
	if token == "" {
		return operation(map[string]string{"GIT_TERMINAL_PROMPT": "0"})
	}
	return withAskpass(token, operation)
}

func withAskpass(token string, operation func(map[string]string) error) error {
	dir, err := os.MkdirTemp("", "ao-git-askpass-")
	if err != nil {
		return fmt.Errorf("create askpass directory: %w", err)
	}
	defer os.RemoveAll(dir)
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure askpass directory: %w", err)
	}
	path := filepath.Join(dir, "askpass")
	script := "#!/bin/sh\ncase \"$1\" in\n*Username*) printf '%s\\n' x-access-token;;\n*Password*) printf '%s\\n' \"$AO_GIT_TOKEN\";;\n*) exit 1;;\nesac\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		return fmt.Errorf("write askpass helper: %w", err)
	}
	return operation(map[string]string{
		"GIT_ASKPASS": path, "GIT_ASKPASS_REQUIRE": "force",
		"GIT_TERMINAL_PROMPT": "0", "AO_GIT_TOKEN": token,
	})
}

// GitHubRepositoryIdentity returns the lower-case owner/name a GitHub URL
// names: the identity the checkout's origin is validated against.
func GitHubRepositoryIdentity(raw string) (string, error) {
	return githubRepositoryIdentity(raw)
}

func githubRepositoryIdentity(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	var path string
	if strings.HasPrefix(raw, "git@github.com:") {
		path = strings.TrimPrefix(raw, "git@github.com:")
	} else {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" ||
			!strings.EqualFold(parsed.Hostname(), "github.com") ||
			parsed.Port() != "" || parsed.User != nil ||
			parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", errors.New("repository URL is not an uncredentialed GitHub URL")
		}
		path = strings.TrimPrefix(parsed.Path, "/")
	}
	parts := strings.Split(strings.TrimSuffix(path, ".git"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" ||
		parts[0] == "." || parts[0] == ".." || parts[1] == "." || parts[1] == ".." {
		return "", errors.New("repository URL does not identify one GitHub repository")
	}
	return strings.ToLower(parts[0] + "/" + parts[1]), nil
}

func replaceEnvironment(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return base
	}
	result := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[key]; !replaced {
			result = append(result, entry)
		}
	}
	for key, value := range overrides {
		result = append(result, key+"="+value)
	}
	return result
}
