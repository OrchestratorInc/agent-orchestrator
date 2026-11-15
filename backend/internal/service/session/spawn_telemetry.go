package session

import (
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

func addSpawnLaunchProps(payload map[string]any, trace sessionmanager.SpawnTraceFacts, mode domain.SessionMode) {
	for key, ms := range trace.StepMs {
		payload[key] = ms
	}
	if trace.Mode != "" {
		mode = trace.Mode
	}
	switch {
	case mode == "":
	case domain.NormalizeSessionMode(mode) == domain.SessionModeChat:
		payload["mode"] = "chat"
	case domain.NormalizeSessionMode(mode) == domain.SessionModeTUI:
		payload["mode"] = "terminal"
	}
	payload["async"] = trace.Async
	if trace.Model != "" {
		payload["model"] = trace.Model
	}
}

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
