package userconfig

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// SetUserConfigInput is the body shape for PUT /api/v1/user-config. AgentConfig
// replaces the stored user-scope config wholesale; a zero-value config clears it
// (stores SQL NULL). See ADR 0002: the write is wholesale, resolution is
// field-by-field.
type SetUserConfigInput struct {
	AgentConfig domain.AgentConfig `json:"agentConfig"`
}

// DefaultPrompts is the assembled hardcoded system-prompt baseline for both
// roles, served alongside the stored override so the UI can prefill its edit
// boxes with the real baseline text. Worker/Orchestrator hold the static
// "skeleton" prompts (role blocks + multi-PR/guard) without any per-session
// dynamic data.
type DefaultPrompts struct {
	Worker       string `json:"defaultWorkerPrompt"`
	Orchestrator string `json:"defaultOrchestratorPrompt"`
}
