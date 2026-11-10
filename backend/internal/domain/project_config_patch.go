package domain

import (
	"errors"
	"strings"
)

// ProjectConfigPatch contains only the config fields explicitly supplied by a
// focused project update. Nil leaves a field unchanged; an empty string clears it.
type ProjectConfigPatch struct {
	CanonicalRepoURL *string
	DefaultBranch    *string
}

// ProjectConfigChange describes a value changed by a focused project update.
type ProjectConfigChange struct {
	Path string
	From string
	To   string
}

// ProjectConfigPatchResult holds the transaction's before and after states.
// Applied is false for dry runs and no-op patches.
type ProjectConfigPatchResult struct {
	Before  ProjectRecord
	After   ProjectRecord
	Changes []ProjectConfigChange
	Found   bool
	Applied bool
}

// ProjectConfigPatchError marks a rejected patch without involving API error
// vocabulary in the domain or storage layer.
type ProjectConfigPatchError struct{ Cause error }

func (e *ProjectConfigPatchError) Error() string { return e.Cause.Error() }
func (e *ProjectConfigPatchError) Unwrap() error { return e.Cause }

// Apply merges and validates a patch against one current project row. It does
// not mutate the row or perform I/O, so preview and commit share one decision.
func (p ProjectConfigPatch) Apply(current ProjectRecord) (ProjectConfig, []ProjectConfigChange, error) {
	updated := current.Config
	changes := make([]ProjectConfigChange, 0, 2)
	if p.CanonicalRepoURL != nil {
		from := updated.CanonicalRepoURL
		updated.CanonicalRepoURL = strings.TrimSpace(*p.CanonicalRepoURL)
		if from != updated.CanonicalRepoURL {
			changes = append(changes, ProjectConfigChange{Path: "config.canonicalRepoURL", From: from, To: updated.CanonicalRepoURL})
		}
	}
	if p.DefaultBranch != nil {
		from := updated.DefaultBranch
		updated.DefaultBranch = strings.TrimSpace(*p.DefaultBranch)
		if from != updated.DefaultBranch {
			changes = append(changes, ProjectConfigChange{Path: "config.defaultBranch", From: from, To: updated.DefaultBranch})
		}
	}
	if err := updated.Validate(); err != nil {
		return ProjectConfig{}, nil, &ProjectConfigPatchError{Cause: err}
	}
	if current.Kind.WithDefault() == ProjectKindScratch {
		if err := ValidateScratchProjectConfig(updated); err != nil {
			return ProjectConfig{}, nil, &ProjectConfigPatchError{Cause: err}
		}
	}
	if err := updated.ValidateCanonicalRepository(current.RepoOriginURL); err != nil {
		return ProjectConfig{}, nil, &ProjectConfigPatchError{Cause: err}
	}
	return updated, changes, nil
}

// ValidateScratchProjectConfig rejects settings that require a Git repository.
func ValidateScratchProjectConfig(cfg ProjectConfig) error {
	if cfg.CanonicalRepoURL != "" {
		return errors.New("scratch projects do not support canonicalRepoURL")
	}
	if strings.TrimSpace(cfg.DefaultBranch) != "" {
		return errors.New("scratch projects do not support defaultBranch")
	}
	if cfg.TrackerIntake.Enabled {
		return errors.New("scratch projects do not support tracker intake")
	}
	if len(cfg.Reviewers) > 0 {
		return errors.New("scratch projects do not support reviewers")
	}
	return nil
}
