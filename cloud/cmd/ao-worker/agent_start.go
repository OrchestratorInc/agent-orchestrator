package main

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/aoagents/agent-orchestrator/cloud/internal/workerexec"
)

// agentStartFile, in the worker data directory, records where the session's
// coding agent started on its first launch.
const agentStartFile = "agent-starts-in"

// pinAgentStartsIn returns where the session's coding agent starts, fixed at
// its first launch. Claude Code keeps a conversation under its working
// directory, so a session that changed folders when the project's "agent
// starts in" setting changed could not resume. The first launch records the
// folder it used (the project's setting, unless workerexec refused the parent
// folder); every later boot of the same workspace reuses the record. A fresh
// sandbox (a deleted and restored session) has no record and follows the
// project's current setting; its transcript is restored to match.
func pinAgentStartsIn(logger *slog.Logger, dataDir string, launch worker.LaunchContext, workspace string) string {
	path := filepath.Join(dataDir, agentStartFile)
	if data, err := os.ReadFile(path); err == nil {
		switch recorded := strings.TrimSpace(string(data)); recorded {
		case domain.ProjectAgentStartsInRepository, domain.ProjectAgentStartsInParent:
			if recorded != normalizedAgentStartsIn(launch.AgentStartsIn) {
				logger.Info("agent start folder: keeping the folder this session started in; the project's setting applies to new sessions",
					"recorded", recorded, "project", normalizedAgentStartsIn(launch.AgentStartsIn))
			}
			return recorded
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		logger.Warn("agent start folder: read the recorded folder", "error", err)
	}
	effective := domain.ProjectAgentStartsInRepository
	if workerexec.AgentDirectory(launch, workspace) != workspace {
		effective = domain.ProjectAgentStartsInParent
	} else if normalizedAgentStartsIn(launch.AgentStartsIn) == domain.ProjectAgentStartsInParent &&
		launch.Kind != "reviewer" && !worker.IsScratchRepositoryURL(launch.RepositoryURL) {
		logger.Warn("agent start folder: the folder holding the repositories cannot be used; starting in the main checkout",
			"folder", filepath.Dir(workspace), "reason", workerexec.ParentAgentDirectoryRefusal(workspace))
		// Keep the project's request so the prompt explains the fallback; the
		// launcher applies the same refusal on every boot.
		effective = domain.ProjectAgentStartsInParent
	}
	if err := os.MkdirAll(dataDir, 0o700); err == nil {
		if err := os.WriteFile(path, []byte(effective+"\n"), 0o600); err != nil {
			logger.Warn("agent start folder: record the folder", "error", err)
		}
	}
	return effective
}

func normalizedAgentStartsIn(value string) string {
	if strings.TrimSpace(value) == domain.ProjectAgentStartsInParent {
		return domain.ProjectAgentStartsInParent
	}
	return domain.ProjectAgentStartsInRepository
}
