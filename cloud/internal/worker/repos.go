package worker

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// ExtraRepoDirName derives a safe local directory name from a repo URL, e.g.
// "https://github.com/owner/ChartSnip" -> "ChartSnip" (domain.RepoDirName, which
// the control plane also uses to validate a project's repositories).
func ExtraRepoDirName(repoURL string) string {
	return domain.RepoDirName(repoURL)
}

// ExtraRepoPath returns where an additional dev-kit repo is checked out: a
// sibling of the primary workspace checkout. Cloning beside the primary repo
// (rather than in a worker-internal data dir) lets the agent, whose working
// directory is the primary repo, reach it at ../<name>. The launcher and the
// worker both compute the path here so the on-disk location and the path shown
// to the agent always agree.
func ExtraRepoPath(workspace, repoURL string) string {
	return filepath.Join(filepath.Dir(workspace), ExtraRepoDirName(repoURL))
}

// RepoCheckout is one repository checkout of a session: the main repository's
// or one of the project's additional repositories beside it.
type RepoCheckout struct {
	// Name is the checkout's folder name.
	Name string
	// Path is the checkout's absolute path.
	Path string
	// URL is the repository's URL as the project lists it.
	URL string
	// FullName is the repository's lower-case GitHub owner/name, or empty when
	// the URL is not a GitHub repository URL.
	FullName string
	// Branch is the branch an additional repository was cloned at; empty for
	// its default branch and for the main repository.
	Branch string
	// Main marks the main repository's checkout.
	Main bool
}

// SessionRepoCheckouts lists a session's checkouts, the main repository's
// first. A standalone session (no repository) lists only its workspace.
func SessionRepoCheckouts(workspace, mainURL string, extras []RepoRef) []RepoCheckout {
	main := RepoCheckout{Name: filepath.Base(workspace), Path: workspace, URL: mainURL, Main: true}
	if identity, err := githubRepositoryIdentity(mainURL); err == nil {
		main.FullName = identity
	}
	checkouts := []RepoCheckout{main}
	if IsScratchRepositoryURL(mainURL) {
		return checkouts
	}
	seen := map[string]bool{filepath.Clean(workspace): true}
	for _, repo := range extras {
		if strings.TrimSpace(repo.URL) == "" || !UsableExtraRepoDir(ExtraRepoDirName(repo.URL)) {
			continue
		}
		// Two repositories, or an additional repository and a main checkout in a
		// configured working directory, can map onto one folder. Only the first
		// claims it, so a selector never resolves a repository to another's
		// checkout.
		if path := filepath.Clean(ExtraRepoPath(workspace, repo.URL)); seen[path] {
			continue
		} else {
			seen[path] = true
		}
		checkout := RepoCheckout{
			Name: ExtraRepoDirName(repo.URL), Path: ExtraRepoPath(workspace, repo.URL),
			URL: repo.URL, Branch: strings.TrimSpace(repo.Branch),
		}
		if identity, err := githubRepositoryIdentity(repo.URL); err == nil {
			checkout.FullName = identity
		}
		checkouts = append(checkouts, checkout)
	}
	return checkouts
}

// SessionRepositoriesEnv names the agent environment variable that lists the
// session's GitHub repositories as lower-case owner/name, comma-separated, so
// `ao claim-pr <number>` only turns a number into a URL on one of them.
const SessionRepositoriesEnv = "AO_SESSION_REPOSITORIES"

// UsableExtraRepoDir reports whether an additional repository may be checked
// out into the folder name beside the main checkout. A name starting with a
// dot is refused: with a durable root of $HOME the checkout would land in a
// dotfile folder such as ~/.aws or ~/.ssh, and .ao is AO's own state.
func UsableExtraRepoDir(name string) bool {
	return name != "" && !strings.HasPrefix(name, ".")
}

// SkippedExtraRepos lists the extras SessionRepoCheckouts leaves out (a dot
// folder, or a folder another checkout already uses), for the worker's log.
func SkippedExtraRepos(workspace, mainURL string, extras []RepoRef) []RepoRef {
	kept := map[string]bool{}
	for _, checkout := range SessionRepoCheckouts(workspace, mainURL, extras)[1:] {
		kept[checkout.URL] = true
	}
	var skipped []RepoRef
	if IsScratchRepositoryURL(mainURL) {
		return nil
	}
	for _, repo := range extras {
		if strings.TrimSpace(repo.URL) != "" && !kept[repo.URL] {
			skipped = append(skipped, repo)
		}
	}
	return skipped
}

// ErrUnknownRepository means a repository selector names none of the session's
// repositories.
var ErrUnknownRepository = errors.New("repository is not one of this session's repositories")

// ResolveRepoCheckout picks the checkout a selector names: an empty selector
// is the main repository; otherwise the selector is a repository's GitHub
// owner/name, URL or folder name, compared without case.
func ResolveRepoCheckout(checkouts []RepoCheckout, selector string) (RepoCheckout, error) {
	selector = strings.TrimSpace(selector)
	if len(checkouts) == 0 {
		return RepoCheckout{}, ErrUnknownRepository
	}
	if selector == "" {
		return checkouts[0], nil
	}
	fullName := strings.ToLower(strings.TrimSuffix(strings.Trim(selector, "/"), ".git"))
	if identity, err := githubRepositoryIdentity(selector); err == nil {
		fullName = identity
	}
	for _, checkout := range checkouts {
		if (checkout.FullName != "" && checkout.FullName == fullName) || strings.EqualFold(checkout.Name, selector) {
			return checkout, nil
		}
	}
	return RepoCheckout{}, ErrUnknownRepository
}

// ExtraRepoFolderBlocked says why the worker cannot create an additional
// repository's checkout folder: it does not exist yet and the folder beside
// the main checkout does not let the worker's user add one (for example a
// ~/work the Coder template created, which the session's bootstrap prepared
// only the repositories' folders of the project at session creation in). It
// returns "" when the folder exists or can be created.
func ExtraRepoFolderBlocked(path string) string {
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return ""
	}
	parent := filepath.Dir(path)
	const writeAndSearch = 0x2 | 0x1 // W_OK | X_OK
	if err := syscall.Access(parent, writeAndSearch); err != nil {
		return parent + " does not let AO's worker user create " + filepath.Base(path) + " (" + err.Error() + ")"
	}
	return ""
}
