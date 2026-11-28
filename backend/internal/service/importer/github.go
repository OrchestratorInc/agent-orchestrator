package importer

import (
	"context"
	"regexp"
	"strings"
	"time"
)

// GitHubOwner is an account a project import can create its GitHub repository
// under: the signed-in gh user or one of its organizations.
type GitHubOwner struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatarUrl"`
}

// GitHubOwnersResult is the body of GET /api/v1/imports/github-owners.
type GitHubOwnersResult struct {
	Owners []GitHubOwner `json:"owners"`
}

// GitHubAvailabilityQuery is the query of GET /api/v1/imports/github-availability.
type GitHubAvailabilityQuery struct {
	Owner string `query:"owner" description:"GitHub user or organization the repository would be created under."`
	Name  string `query:"name" description:"Repository name to check."`
}

// GitHubAvailabilityResult reports whether a project import may create owner/name.
type GitHubAvailabilityResult struct {
	Available bool   `json:"available"`
	Message   string `json:"message,omitempty"`
}

const (
	githubOwnersTimeout = 8 * time.Second
	githubLookupTimeout = 8 * time.Second
)

// githubOwnerSegment and githubNameSegment bound owner and name to what GitHub
// allows, so neither can reach gh as a flag or another API path. Owners start
// with a letter or digit; repository names may start with a period (.github),
// but "." and ".." are refused because they would change the API path.
var (
	githubOwnerSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
	githubNameSegment  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
)

// GitHubOwners lists the accounts gh on this machine can create repositories
// under, the user first. The same lookup the desktop app runs locally, so a
// client without gh (the phone) can offer the same choice. An account
// without the read:org scope still yields the user; no gh, or gh signed out,
// yields none.
func (m *Manager) GitHubOwners(ctx context.Context) (GitHubOwnersResult, error) {
	ctx, cancel := context.WithTimeout(ctx, githubOwnersTimeout)
	defer cancel()
	user, err := importGhOutputFunc(ctx, "", "api", "user", "--jq", "[.login, .avatar_url] | @tsv")
	if err != nil {
		return GitHubOwnersResult{Owners: []GitHubOwner{}}, nil //nolint:nilerr // signed out is an empty list, not a failure
	}
	orgs, orgErr := importGhOutputFunc(ctx, "", "api", "user/memberships/orgs", "--paginate", "--jq", ".[] | [.organization.login, .organization.avatar_url] | @tsv")
	if orgErr != nil {
		orgs = ""
	}
	owners := []GitHubOwner{}
	seen := map[string]bool{}
	for _, line := range append(strings.Split(user, "\n"), strings.Split(orgs, "\n")...) {
		login, avatar, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || login == "" || avatar == "" || seen[login] {
			continue
		}
		seen[login] = true
		owners = append(owners, GitHubOwner{Login: login, AvatarURL: avatar})
	}
	return GitHubOwnersResult{Owners: owners}, nil
}

// GitHubRepositoryAvailability reports whether owner/name is free to create,
// as the desktop app checks before creating a project's GitHub repository.
func (m *Manager) GitHubRepositoryAvailability(ctx context.Context, in GitHubAvailabilityQuery) (GitHubAvailabilityResult, error) {
	owner := strings.TrimSpace(in.Owner)
	name := strings.TrimSpace(in.Name)
	if owner == "" || name == "" {
		return GitHubAvailabilityResult{Message: "Owner and repository name are required."}, nil
	}
	if !githubOwnerSegment.MatchString(owner) || !githubNameSegment.MatchString(name) || name == "." || name == ".." {
		return GitHubAvailabilityResult{Message: "Use letters, numbers, hyphens, underscores, or periods."}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, githubLookupTimeout)
	defer cancel()
	out, err := importGhOutputFunc(ctx, "", "api", "repos/"+owner+"/"+name)
	if err == nil {
		return GitHubAvailabilityResult{Message: "Repository name is already in use for this owner."}, nil
	}
	// Only gh's own output names GitHub's answer: the process error also holds
	// the command line, and a missing gh reads "executable file not found".
	if strings.Contains(out, "HTTP 404") {
		return GitHubAvailabilityResult{Available: true}, nil
	}
	return GitHubAvailabilityResult{Message: "Could not check this repository name. Confirm GitHub CLI is signed in."}, nil
}
