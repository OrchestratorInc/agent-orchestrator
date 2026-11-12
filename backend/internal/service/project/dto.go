package project

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// GetResult is the discriminated result returned by Service.Get.
type GetResult struct {
	Status   string
	Project  *Project
	Degraded *Degraded
}

// AddInput is the body shape for POST /api/v1/projects.
type AddInput struct {
	Path               string                `json:"path"`
	ProjectID          *string               `json:"projectId,omitempty"`
	Name               *string               `json:"name,omitempty"`
	Config             *domain.ProjectConfig `json:"config,omitempty"`
	AsWorkspace        bool                  `json:"asWorkspace,omitempty"`
	ClonePreparationID string                `json:"clonePreparationId,omitempty"`
}

// CloneInput is the body shape for POST /api/v1/projects/clone. The daemon
// derives the checkout directory name from RemoteURL and creates it directly
// beneath DestinationParent before registering the resulting project.
type CloneInput struct {
	RemoteURL         string                `json:"remoteUrl" minLength:"1"`
	DestinationParent string                `json:"destinationParent" minLength:"1"`
	ProjectID         *string               `json:"projectId,omitempty"`
	Name              *string               `json:"name,omitempty"`
	Config            *domain.ProjectConfig `json:"config,omitempty"`
}

// ClonePreparationResult is the checkout returned before project registration.
type ClonePreparationResult struct {
	Path          string `json:"path"`
	RemoteURL     string `json:"remoteUrl"`
	PreparationID string `json:"preparationId"`
}

// CloneDestination reports local destination readiness without contacting the remote.
type CloneDestination struct {
	RemoteURL      string `json:"remoteUrl"`
	Path           string `json:"path"`
	Exists         bool   `json:"exists"`
	CanUseExisting bool   `json:"canUseExisting"`
}

// ClonePreparationCleanupInput identifies a checkout created by prepare-clone
// that the user abandoned before project registration.
type ClonePreparationCleanupInput struct {
	Path          string `json:"path" minLength:"1"`
	PreparationID string `json:"preparationId" minLength:"1"`
	KeepCheckout  bool   `json:"keepCheckout,omitempty"`
}

// InitializeRepositoryInput is the body shape for POST /api/v1/projects/initialize.
type InitializeRepositoryInput struct {
	Path string `json:"path"`
}

// InitializeRepositoryResult reports the repository path initialized for onboarding.
type InitializeRepositoryResult struct {
	Path string `json:"path"`
}

// UpdateSettingsInput is the body shape for PUT /api/v1/projects/{id}. It
// atomically replaces the user-facing display name and per-project config.
type UpdateSettingsInput struct {
	DisplayName string               `json:"displayName" minLength:"1" maxLength:"100"`
	Config      domain.ProjectConfig `json:"config"`
	// WorktreeRootPresent reports whether the request body's config object
	// carried the "worktreeRoot" KEY. Presence is the whole rule: JSON cannot
	// otherwise tell "leave the saved root alone" from "clear it", because an
	// absent key and an explicit "" both decode to the empty string.
	//
	//   key absent  -> the saved root is preserved (this is what any client built
	//                  before the field existed sends, and what a field-flag edit
	//                  that never mentions the root sends)
	//   key present -> its value is used verbatim, so "worktreeRoot": "" clears
	//                  the saved root
	//
	// The CLI keeps three states distinct: `ao project set-config` with field
	// flags omits the key; `--worktree-root ""` sends the key with an empty
	// string; and the full replacements `--clear` / `--config-json` always send
	// the key, explicitly empty when the replacement names no root, because they
	// replace the whole config rather than merging into it.
	WorktreeRootPresent bool `json:"-"`
}

// SetConfigInput is the body shape for PUT /api/v1/projects/{id}/config. Config
// replaces the project's stored config wholesale; a zero-value config clears it.
type SetConfigInput struct {
	Config domain.ProjectConfig `json:"config"`
	// WorktreeRootPresent mirrors UpdateSettingsInput: the key's presence in the
	// request's config object decides. Absent preserves the stored root; present
	// (including "") replaces it. See UpdateSettingsInput for the CLI's three
	// states.
	WorktreeRootPresent bool `json:"-"`
}

// RemoveResult reports what DELETE /api/v1/projects/{id} actually did.
type RemoveResult struct {
	ProjectID         domain.ProjectID `json:"projectId"`
	RemovedStorageDir bool             `json:"removedStorageDir"`
}

// SetPermissionsInput remembers a project-wide policy for future sessions.
type SetPermissionsInput struct {
	SourceHarness domain.AgentHarness   `json:"sourceHarness,omitempty"`
	Permissions   domain.PermissionMode `json:"permissions" enum:"default,accept-edits,auto,bypass-permissions"`
}
