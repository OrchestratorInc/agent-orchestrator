package session

import (
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// addSpawnLaunchProps adds what a spawn outcome event says about the launch:
// the steps that ran (model_check_ms ... agent_start_ms), mode (chat or
// terminal), async and the model. mode falls back to the caller's when the
// trace has none (the start failed before choosing). model comes only from
// the trace's catalog check (sessionmanager.NoteSpawnModel): a listed ID, or
// "custom" for a typed one, never the raw saved or requested ID.
func addSpawnLaunchProps(payload map[string]any, trace sessionmanager.SpawnTraceFacts, mode domain.SessionMode) {
	for key, ms := range trace.StepMs {
		payload[key] = ms
	}
	if trace.Mode != "" {
		mode = trace.Mode
	}
	switch domain.NormalizeSessionMode(mode) {
	case domain.SessionModeChat:
		payload["mode"] = "chat"
	case domain.SessionModeTUI:
		payload["mode"] = "terminal"
	}
	payload["async"] = trace.Async
	if trace.Model != "" {
		payload["model"] = trace.Model
	}
}

// withSpawnClassification carries err's failed_step and cause on the API
// error the spawn answers with, so the request's Sentry capture tags them.
func withSpawnClassification(mapped, err error) error {
	var classified *sessionmanager.ClassifiedSpawnError
	var apiErr *apierr.Error
	if !errors.As(err, &classified) || !errors.As(mapped, &apiErr) {
		return mapped
	}
	clone := *apiErr
	clone.Cause = &sessionmanager.ClassifiedSpawnError{Err: apiErr.Cause, Step: classified.Step, Cause: classified.Cause}
	return &clone
}
