package sessionmanager

import (
	"errors"
	"fmt"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// projectNotARepoFix is the recovery guidance shown when a registered project
// folder is no longer a Git repository (#8905).
const projectNotARepoFix = "Restore the folder's .git directory (or run `git init` and create an initial commit there), or remove and re-add the project in Orchestrator, then try again."

// ProjectNotARepoGuidance explains a failure caused by a registered project
// repository folder that is no longer a Git repository, replacing git's raw
// "not a git repository" exit 128 with the folder and how to recover. It
// returns ok=false for any other error. The same text backs both the API error
// (PROJECT_NOT_A_REPO) and an asynchronous chat start's failure message, so the
// two surfaces cannot drift.
func ProjectNotARepoGuidance(err error) (message, path, suggestedFix string, ok bool) {
	if !errors.Is(err, ports.ErrWorkspaceNotARepo) {
		return "", "", "", false
	}
	var notRepo *ports.WorkspaceNotARepoError
	if errors.As(err, &notRepo) {
		path = notRepo.Path
	}
	if path == "" {
		return "The project folder is no longer a Git repository. " + projectNotARepoFix, "", projectNotARepoFix, true
	}
	return fmt.Sprintf("The project folder %q is no longer a Git repository. %s", path, projectNotARepoFix), path, projectNotARepoFix, true
}
