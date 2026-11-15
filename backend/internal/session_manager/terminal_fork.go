package sessionmanager

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionfork"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentruntime"
)

var ErrForkSourceMovedOn = errors.New("the original session took another turn while this fork was being set up; fork it again")

type terminalTurnLister interface {
	ListTerminalTurns(ctx context.Context, id domain.SessionID) ([]domain.HandoffTurn, error)
}

type ClaudeSessionForker func(ctx context.Context, sourceID, sourceWorkspace string, target ports.ForkTarget) (string, error)

type ClaudeSessionCleaner func(ctx context.Context, sessionID, workspace string, env map[string]string) error

func (m *Manager) SetClaudeSessionForker(fork ClaudeSessionForker, cleanup ClaudeSessionCleaner) {
	m.claudeSessionFork = fork
	m.claudeSessionCleanup = cleanup
}

func (m *Manager) TerminalForkSource(ctx context.Context, rec domain.SessionRecord, turnID string) (sessionfork.Source, error) {
	if turnID != "" {
		return sessionfork.Source{Refusal: sessionfork.CodeOlderTurn}, nil
	}
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok || !terminalForkSupported(rec.Harness) {
		return sessionfork.Source{Refusal: sessionfork.CodeAgentUnsupported}, nil
	}
	nativeID := strings.TrimSpace(rec.Metadata.AgentSessionID)
	if nativeID == "" && rec.Harness == domain.HarnessClaudeCode {
		nativeID = agentruntime.ClaudeSessionID(string(rec.ID))
	}
	if nativeID == "" {
		return sessionfork.Source{Refusal: sessionfork.CodeNativeMissing}, nil
	}
	source := rec
	var (
		frozen   bool
		lastTurn time.Time
	)
	freeze := func(ctx context.Context) {
		frozen, lastTurn = true, m.latestTerminalTurn(ctx, source.ID)
	}
	prepare := func(ctx context.Context, target ports.ForkTarget) (ports.ForkTerminalLaunch, error) {
		if frozen {
			if err := m.requireNoTurnSince(ctx, source.ID, lastTurn); err != nil {
				return ports.ForkTerminalLaunch{}, err
			}
		}
		project, err := m.loadProject(ctx, source.ProjectID)
		if err != nil {
			return ports.ForkTerminalLaunch{}, err
		}
		agentConfig := restoredAgentConfig(source, project.Config)
		restoreID, childID := nativeID, ""
		if source.Harness == domain.HarnessClaudeCode {
			if m.claudeSessionFork == nil {
				return ports.ForkTerminalLaunch{}, errors.New("claude session fork is not available")
			}
			childID, err = m.claudeSessionFork(ctx, nativeID, source.Metadata.WorkspacePath, target)
			if err != nil {
				return ports.ForkTerminalLaunch{}, err
			}
			restoreID = childID
		}
		cmd, ok, err := agent.GetRestoreCommand(ctx, ports.RestoreConfig{
			Session: ports.SessionRef{
				ID: string(target.SessionID), WorkspacePath: target.WorkspacePath, DataDir: target.DataDir,
				Metadata: map[string]string{ports.MetadataKeyAgentSessionID: restoreID, ports.MetadataKeySessionMode: string(domain.SessionModeTUI)},
			},
			Kind: source.Kind, DataDir: target.DataDir, Config: agentConfig, Permissions: agentConfig.Permissions,
			SystemPrompt: target.SystemPrompt, SystemPromptFile: target.SystemPromptFile,
		})
		if err != nil {
			return ports.ForkTerminalLaunch{}, err
		}
		if !ok {
			return ports.ForkTerminalLaunch{}, errors.New("agent can't resume this session")
		}
		if source.Harness == domain.HarnessClaudeCode {
			return ports.ForkTerminalLaunch{Argv: cmd, AgentSessionID: childID}, nil
		}
		argv, ok := terminalForkArgv(source.Harness, cmd, nativeID)
		if !ok {
			return ports.ForkTerminalLaunch{}, errors.New("agent has no fork command")
		}
		return ports.ForkTerminalLaunch{Argv: argv}, nil
	}
	return sessionfork.Source{ContextMode: domain.ForkContextTerminalNative, PrepareTerminal: prepare, Freeze: freeze}, nil
}

func (m *Manager) latestTerminalTurn(ctx context.Context, id domain.SessionID) time.Time {
	lister, ok := m.store.(terminalTurnLister)
	if !ok {
		return time.Time{}
	}
	turns, err := lister.ListTerminalTurns(ctx, id)
	if err != nil {
		return time.Time{}
	}
	var latest time.Time
	for _, turn := range turns {
		if turn.At.After(latest) {
			latest = turn.At
		}
	}
	return latest
}

func (m *Manager) requireNoTurnSince(ctx context.Context, id domain.SessionID, lastTurn time.Time) error {
	rec, found, err := m.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if found && rec.Activity.State == domain.ActivityActive {
		return ErrForkSourceMovedOn
	}
	if m.latestTerminalTurn(ctx, id).After(lastTurn) {
		return ErrForkSourceMovedOn
	}
	return nil
}

func terminalForkSupported(harness domain.AgentHarness) bool {
	switch harness {
	case domain.HarnessClaudeCode, domain.HarnessCodex, domain.HarnessContinue, domain.HarnessOpenCode,
		domain.HarnessPi, domain.HarnessGrok, domain.HarnessDroid:
		return true
	}
	return false
}

func terminalForkArgv(harness domain.AgentHarness, restore []string, nativeID string) ([]string, bool) {
	i := slices.Index(restore, nativeID)
	if i < 1 {
		return nil, false
	}
	argv := slices.Clone(restore)
	flag := argv[i-1]
	switch harness {
	case domain.HarnessCodex:
		if len(argv) < 2 || argv[1] != "resume" {
			return nil, false
		}
		argv[1] = "fork"
	case domain.HarnessContinue:
		if flag != "--fork" {
			return nil, false
		}
	case domain.HarnessOpenCode:
		if flag != "--session" {
			return nil, false
		}
		argv = slices.Insert(argv, i+1, "--fork")
	case domain.HarnessPi:
		if flag != "--session" {
			return nil, false
		}
		argv[i-1] = "--fork"
	case domain.HarnessGrok:
		if flag != "-r" && flag != "--resume" {
			return nil, false
		}
		argv[i-1] = "--resume"
		argv = slices.Insert(argv, i+1, "--fork-session")
	case domain.HarnessDroid:
		if flag != "-r" && flag != "--resume" {
			return nil, false
		}
		argv[i-1] = "--fork"
	default:
		return nil, false
	}
	return argv, true
}

const forkTerminalOutputLines = 400

func (m *Manager) ForkTerminalOutput(ctx context.Context, rec domain.SessionRecord) string {
	handle := strings.TrimSpace(rec.Metadata.RuntimeHandleID)
	if handle == "" || m.runtime == nil {
		return ""
	}
	output, err := m.runtime.GetOutput(ctx, ports.RuntimeHandle{ID: handle}, forkTerminalOutputLines)
	if err != nil {
		return ""
	}
	return output
}
