package project

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// ProjectBranch is one selectable base branch of a project's repository.
type ProjectBranch struct {
	// Name is what the project's defaultBranch accepts: a local branch name, or
	// "<remote>/<branch>" for a remote-tracking branch.
	Name string `json:"name"`
	// Remote is empty for a local branch, else the remote name ("origin").
	Remote string `json:"remote,omitempty"`
	// Local is true when refs/heads/<Name> exists in the root repository.
	Local bool `json:"local"`
	// Current marks the branch checked out in the root repository.
	Current bool `json:"current"`
	// Default marks the single row the project's configured base branch names.
	// A local branch and its origin/<branch> counterpart are different rows, so
	// only the spelling the setting stores is marked.
	Default bool `json:"default"`
}

// ProjectBranches is the base-branch picker's read model for one project.
type ProjectBranches struct {
	ProjectID domain.ProjectID `json:"projectId"`
	// Repository is the root repository the branches were read from. Workspace
	// projects list ONLY the root repo: a session base branch is resolved from
	// the root repository alone, so child repositories contribute nothing.
	Repository string          `json:"repository"`
	Default    string          `json:"default"`
	Branches   []ProjectBranch `json:"branches"`
	Truncated  bool            `json:"truncated"`
}

// maxProjectBranches caps the picker's list. The field is a free-text combobox,
// so a repository with thousands of branches needs no complete list.
const maxProjectBranches = 300

// Branches lists the local and origin-tracking branches a project's
// defaultBranch can name. Remote rows carry the "origin/<branch>" form so the
// value can be stored verbatim, and origin/HEAD is excluded: it is a symbolic
// alias of a branch that is already listed, not a branch of its own.
//
// Every failure reading the repository is reported as an error rather than an
// empty list, so the settings form can say why the list is missing while still
// allowing a hand-typed branch.
func (m *Service) Branches(ctx context.Context, id domain.ProjectID) (ProjectBranches, error) {
	if err := validateProjectID(id); err != nil {
		return ProjectBranches{}, err
	}
	row, ok, err := m.store.GetProject(ctx, string(id))
	if err != nil {
		return ProjectBranches{}, apierr.Internal("PROJECT_LOAD_FAILED", "Failed to load project")
	}
	if !ok || !row.ArchivedAt.IsZero() {
		return ProjectBranches{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	result := ProjectBranches{ProjectID: id, Repository: row.Path, Branches: []ProjectBranch{}}
	if row.Kind.WithDefault() == domain.ProjectKindScratch {
		return result, nil
	}
	exists, statErr := folderExists(row.Path)
	if statErr != nil {
		return ProjectBranches{}, apierr.Internal("PROJECT_FOLDER_UNREADABLE", "The project folder could not be read")
	}
	if !exists {
		return ProjectBranches{}, apierr.Invalid("PROJECT_FOLDER_MISSING", "The project folder no longer exists", map[string]any{"path": row.Path})
	}
	if err := m.readBranches(ctx, row, &result); err != nil {
		return ProjectBranches{}, err
	}
	return result, nil
}

// readBranches fills result from the root repository. Local and remote refs are
// read in one for-each-ref pass; a failure is reported, never swallowed.
func (m *Service) readBranches(ctx context.Context, row domain.ProjectRecord, result *ProjectBranches) error {
	defaultBranch := row.Config.WorktreeBaseBranch()
	if defaultBranch == "" || defaultBranch == domain.DefaultBranchAuto {
		defaultBranch = resolveDefaultBranch(ctx, row.Path)
	}
	result.Default = defaultBranch
	out, err := gitBranchOutput(ctx, row.Path,
		"for-each-ref",
		"--format=%(refname)",
		"--sort=-committerdate",
		"refs/heads/",
		"refs/remotes/",
	)
	if err != nil {
		return apierr.Invalid("PROJECT_BRANCHES_UNAVAILABLE", "The project's branches could not be read", map[string]any{"path": row.Path})
	}
	current := currentProjectBranch(ctx, row.Path)
	locals := make(map[string]bool)
	// Order is git's own (newest committer date first), kept so a truncated list
	// still offers the branches people actually work on.
	var localNames, remoteNames []string
	for _, line := range strings.Split(out, "\n") {
		ref := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(ref, "refs/heads/"):
			name := strings.TrimPrefix(ref, "refs/heads/")
			if name != "" && !locals[name] {
				locals[name] = true
				localNames = append(localNames, name)
			}
		case strings.HasPrefix(ref, "refs/remotes/"):
			remoteNames = append(remoteNames, strings.TrimPrefix(ref, "refs/remotes/"))
		}
	}
	appendBranch := func(branch ProjectBranch) bool {
		if len(result.Branches) >= maxProjectBranches {
			result.Truncated = true
			return false
		}
		branch.Default = branch.Name == defaultBranch
		result.Branches = append(result.Branches, branch)
		return true
	}
	for _, name := range localNames {
		if !appendBranch(ProjectBranch{Name: name, Local: true, Current: name == current}) {
			break
		}
	}
	for _, ref := range remoteNames {
		remote, branch, ok := strings.Cut(ref, "/")
		if !ok || remote == "" || branch == "" {
			continue
		}
		// origin/HEAD is a symbolic alias of refs/remotes/origin/<branch>, not a
		// branch, and its name would never resolve as a base branch.
		if branch == "HEAD" || strings.HasSuffix(branch, "/HEAD") {
			continue
		}
		if !appendBranch(ProjectBranch{Name: ref, Remote: remote, Local: locals[branch]}) {
			break
		}
	}
	return nil
}

// currentProjectBranch returns the branch checked out in the root repository, or
// "" for a detached HEAD or an unreadable repository.
func currentProjectBranch(ctx context.Context, path string) string {
	out, err := gitBranchOutput(ctx, path, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// gitBranchOutput runs one read-only git command against path. Stderr stays out
// of the error: it can embed credentials from remote URLs, and callers map every
// failure to a fixed public message.
func gitBranchOutput(ctx context.Context, path string, args ...string) (string, error) {
	cmd := aoprocess.CommandContext(ctx, "git", append([]string{"-C", path}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}
