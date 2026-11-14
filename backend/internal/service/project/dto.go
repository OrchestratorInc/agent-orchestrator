package project

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

type GetResult struct {
	Status   string
	Project  *Project
	Degraded *Degraded
}

type AddInput struct {
	Path               string                `json:"path"`
	ProjectID          *string               `json:"projectId,omitempty"`
	Name               *string               `json:"name,omitempty"`
	Config             *domain.ProjectConfig `json:"config,omitempty"`
	AsWorkspace        bool                  `json:"asWorkspace,omitempty"`
	ClonePreparationID string                `json:"clonePreparationId,omitempty"`
}

type CloneInput struct {
	RemoteURL         string                `json:"remoteUrl" minLength:"1"`
	DestinationParent string                `json:"destinationParent" minLength:"1"`
	ProjectID         *string               `json:"projectId,omitempty"`
	Name              *string               `json:"name,omitempty"`
	Config            *domain.ProjectConfig `json:"config,omitempty"`
}

type ClonePreparationResult struct {
	Path          string `json:"path"`
	RemoteURL     string `json:"remoteUrl"`
	PreparationID string `json:"preparationId"`
}

type CloneProgressPhase string

const (
	ClonePhaseEnumeratingObjects CloneProgressPhase = "enumerating_objects"
	ClonePhaseCountingObjects    CloneProgressPhase = "counting_objects"
	ClonePhaseCompressingObjects CloneProgressPhase = "compressing_objects"
	ClonePhaseReceivingObjects   CloneProgressPhase = "receiving_objects"
	ClonePhaseResolvingDeltas    CloneProgressPhase = "resolving_deltas"
	ClonePhaseUpdatingFiles      CloneProgressPhase = "updating_files"
	ClonePhaseFilteringContent   CloneProgressPhase = "filtering_content"
	ClonePhaseRetrying           CloneProgressPhase = "retrying"
)

type CloneProgress struct {
	Phase   CloneProgressPhase `json:"phase" enum:"enumerating_objects,counting_objects,compressing_objects,receiving_objects,resolving_deltas,updating_files,filtering_content,retrying"`
	Attempt int                `json:"attempt" minimum:"1"`
	Percent *int               `json:"percent,omitempty" minimum:"0" maximum:"100"`
}

type CloneDestination struct {
	RemoteURL      string `json:"remoteUrl"`
	Path           string `json:"path"`
	Exists         bool   `json:"exists"`
	CanUseExisting bool   `json:"canUseExisting"`
}

type ClonePreparationCleanupInput struct {
	Path          string `json:"path" minLength:"1"`
	PreparationID string `json:"preparationId" minLength:"1"`
	KeepCheckout  bool   `json:"keepCheckout,omitempty"`
}

type InitializeRepositoryInput struct {
	Path     string `json:"path"`
	Create   bool   `json:"create,omitempty"`
	CreateID string `json:"createId,omitempty" maxLength:"64"`
}

type InitializeRepositoryResult struct {
	Path string `json:"path"`
}

type UpdateSettingsInput struct {
	DisplayName string               `json:"displayName" minLength:"1" maxLength:"100"`
	Config      domain.ProjectConfig `json:"config"`
}

type SetConfigInput struct {
	Config domain.ProjectConfig `json:"config"`
}

type RemoveResult struct {
	ProjectID         domain.ProjectID `json:"projectId"`
	RemovedStorageDir bool             `json:"removedStorageDir"`
}

type SetPermissionsInput struct {
	SourceHarness domain.AgentHarness   `json:"sourceHarness,omitempty"`
	Permissions   domain.PermissionMode `json:"permissions" enum:"default,accept-edits,auto,bypass-permissions"`
}
