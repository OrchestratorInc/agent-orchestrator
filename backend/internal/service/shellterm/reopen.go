package shellterm

import (
	"context"
	"fmt"
	"maps"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func agentResumeCommand(harness domain.AgentHarness, conversationID string) string {
	var picker string
	switch harness {
	case domain.HarnessClaudeCode:
		picker = "claude --resume"
	case domain.HarnessCodex:
		picker = "codex resume -c check_for_update_on_startup=false"
	default:
		return ""
	}
	if _, err := uuid.Parse(conversationID); err != nil {
		return picker
	}
	return picker + " " + conversationID
}

func resumeHarness(rec ShellTerminalRecord) domain.AgentHarness {
	if rec.EndedAt == nil || agentResumeCommand(rec.LastAgentHarness, "") == "" {
		return ""
	}
	return rec.LastAgentHarness
}

var agentResumeDeliveryTimeout = 30 * time.Second

// ReopenShellTerminal restores an ended shell as a fresh login shell in the
// same session, directory (when it still exists) and tab title and position,
// showing the output saved when the app quit above its first prompt. It never
// replays anything the old shell ran. With ResumeAgent, the agent that was
// running in the shell is resumed once the new shell reaches its prompt (see
// agentResumeCommand); if that cannot be delivered the shell is still open
// for manual use.
func (s *Service) ReopenShellTerminal(ctx context.Context, in ReopenShellTerminalInput) (ShellTerminal, error) {
	if in.HandleID == "" {
		return ShellTerminal{}, apierr.Invalid("SHELL_TERMINAL_ID_REQUIRED", "A shell terminal id is required", nil)
	}
	rec, err := s.endedShellTerminal(ctx, in.HandleID)
	if err != nil {
		return ShellTerminal{}, err
	}
	if s.sessions == nil {
		return ShellTerminal{}, apierr.Internal("SHELL_TERMINAL_NO_SESSION_LOOKUP", "Session lookup is unavailable")
	}
	if _, _, err := s.sessions.SessionWorkspace(ctx, rec.SessionID); err != nil {
		return ShellTerminal{}, fmt.Errorf("reopen shell terminal: resolve session %s: %w", rec.SessionID, err)
	}
	release, err := s.acquireSessionGate(ctx, rec.SessionID)
	if err != nil {
		return ShellTerminal{}, err
	}
	defer release()
	if rec, err = s.endedShellTerminal(ctx, in.HandleID); err != nil {
		return ShellTerminal{}, err
	}
	resumeCommand := ""
	var resumedHarness domain.AgentHarness
	if in.ResumeAgent {
		resumedHarness = resumeHarness(rec)
		if resumedHarness == "" {
			return ShellTerminal{}, apierr.Invalid("SHELL_TERMINAL_AGENT_NOT_RESUMABLE",
				"No agent that Orchestrator can resume was seen in this terminal", nil)
		}
	}

	workingDir, projectID, conversationID := rec.WorkingDir, rec.ProjectID, rec.AgentConversationID
	if info, statErr := os.Stat(workingDir); statErr != nil || !info.IsDir() {
		workingDir, projectID, err = s.resolveShellTerminalWorkingDir(ctx, rec.ProjectID, rec.SessionID)
		if err != nil {
			return ShellTerminal{}, err
		}
		conversationID = ""
	}
	if resumedHarness != "" {
		resumeCommand = agentResumeCommand(resumedHarness, conversationID)
	}
	var projectEnv map[string]string
	if projectID != "" {
		projectEnv, err = s.projects.ProjectEnv(ctx, projectID)
		if err != nil {
			return ShellTerminal{}, fmt.Errorf("reopen shell terminal: resolve project environment: %w", err)
		}
	}
	launch, err := s.prepareUserShell(rec.SessionID, in.Shell, projectEnv)
	if err != nil {
		return ShellTerminal{}, err
	}
	defer launch.done()

	cfg := openTerminalConfig{
		argv: launch.argv, env: launch.env, projectEnv: projectEnv, projectID: projectID, sessionID: rec.SessionID,
		workingDir: workingDir, title: rec.Title, titleExplicit: rec.TitleExplicit,
		previewVerifier:  launch.verifier,
		cols:             in.Cols,
		rows:             in.Rows,
		replaces:         rec.HandleID,
		createdAt:        rec.CreatedAt,
		lastAgentHarness: resumedHarness,
	}
	if saved := s.savedOutputPath(rec.HandleID); fileExists(saved) {
		cfg.replayFile = saved
	}
	if resumeCommand == "" {
		cfg.startOnAttach = true
		terminal, err := s.openTerminal(ctx, cfg)
		if err == nil {
			s.removeSavedOutput(rec.HandleID)
		}
		return terminal, err
	}
	readiness, err := prepareCueShellReadiness(s.dataDir, launch.argv, launch.env)
	if err != nil {
		return ShellTerminal{}, err
	}
	if readiness.file == "" {
		readiness.cleanup()
		return ShellTerminal{}, apierr.Invalid("SHELL_TERMINAL_AGENT_RESUME_UNSUPPORTED",
			"Reopening with an agent needs bash, zsh, sh or fish. Reopen the terminal and start the agent yourself.", nil)
	}
	cfg.argv = readiness.argv
	cfg.env = maps.Clone(launch.env)
	maps.Copy(cfg.env, readiness.env)
	terminal, err := s.openTerminal(ctx, cfg)
	if err != nil {
		readiness.cleanup()
		return ShellTerminal{}, err
	}
	s.removeSavedOutput(rec.HandleID)
	go s.deliverAgentResume(context.WithoutCancel(ctx), terminal.HandleID, launch.argv, readiness, resumeCommand)
	return terminal, nil
}

func (s *Service) endedShellTerminal(ctx context.Context, handleID string) (ShellTerminalRecord, error) {
	rec, found, err := s.store.SelectShellTerminalByHandleID(ctx, handleID)
	if err != nil {
		return ShellTerminalRecord{}, fmt.Errorf("reopen shell terminal %s: %w", handleID, err)
	}
	if !found {
		return ShellTerminalRecord{}, apierr.NotFound("SHELL_TERMINAL_NOT_FOUND", "No such shell terminal: "+handleID)
	}
	if rec.EndedAt == nil || !keepsEndedShell(rec) {
		return ShellTerminalRecord{}, apierr.Conflict("SHELL_TERMINAL_NOT_ENDED", "This terminal is still running", nil)
	}
	return rec, nil
}

func (s *Service) deliverAgentResume(ctx context.Context, handleID string, shellArgv []string, readiness cueShellReadiness, command string) {
	defer readiness.cleanup()
	ctx, cancel := context.WithTimeout(ctx, agentResumeDeliveryTimeout)
	defer cancel()
	handle := ports.RuntimeHandle{ID: handleID}
	if err := s.waitForCueShellReady(ctx, handle, readiness.file); err != nil {
		s.log.Warn("reopened shell terminal: agent resume not sent", "handleId", handleID, "error", err)
		return
	}
	delivery, err := prepareCueCommand(s.dataDir, shellArgv, command)
	if err != nil {
		s.log.Warn("reopened shell terminal: agent resume not prepared", "handleId", handleID, "error", err)
		return
	}
	defer delivery.cleanup()
	if err := s.runtime.SendMessage(ctx, handle, delivery.input); err != nil {
		s.log.Warn("reopened shell terminal: agent resume not sent", "handleId", handleID, "error", err)
		return
	}
	if err := waitForCueCommandConsumed(ctx, delivery.file); err != nil {
		s.log.Warn("reopened shell terminal: agent resume not consumed", "handleId", handleID, "error", err)
	}
}

// RecordShellAgent remembers the coding agent in front of a shell ("" once it
// leaves), so a shell that ends without a quit (a power loss) can still offer
// to resume it. Best-effort: a failed write only loses the offer.
//
// The write outlives the caller's cancellation: a watch is cancelled when its
// last viewer leaves, and the reading it already counted is not reported again.
func (s *Service) RecordShellAgent(ctx context.Context, handleID string, harness domain.AgentHarness) {
	if handleID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.store.UpdateShellTerminalAgentHarness(ctx, handleID, harness); err != nil {
		s.log.Warn("shell terminal: record agent failed", "handleId", handleID, "error", err)
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
