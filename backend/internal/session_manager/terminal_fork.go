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

// ErrForkSourceMovedOn means a terminal fork's original took another turn
// while the fork was being set up. Its agent copies the conversation as it
// launches, which would no longer match the files it was given.
var ErrForkSourceMovedOn = errors.New("the original session took another turn while this fork was being set up; fork it again")

type terminalTurnLister interface {
	ListTerminalTurns(ctx context.Context, id domain.SessionID) ([]domain.HandoffTurn, error)
}

// ClaudeSessionForker copies a whole Claude session so it opens from the
// fork's worktree, returning the new session id.
type ClaudeSessionForker func(ctx context.Context, sourceID, sourceWorkspace string, target ports.ForkTarget) (string, error)

// ClaudeSessionCleaner removes a Claude session copied into a fork's worktree.
type ClaudeSessionCleaner func(ctx context.Context, sessionID, workspace string, env map[string]string) error

// SetClaudeSessionForker wires the Claude session copy used by forks, and the
// cleanup that removes it when the fork fails to start or is deleted.
func (m *Manager) SetClaudeSessionForker(fork ClaudeSessionForker, cleanup ClaudeSessionCleaner) {
	m.claudeSessionFork = fork
	m.claudeSessionCleanup = cleanup
}

// TerminalForkSource reports how a terminal session can be forked, and builds
// the fork's launch command: the agent's own fork command, with the same
// flags and permissions a restore uses, run in the fork's worktree.
func (m *Manager) TerminalForkSource(ctx context.Context, rec domain.SessionRecord, turnID string) (sessionfork.Source, error) {
	if turnID != "" {
		// A terminal has no AO turns to cut at; its fork copies everything.
		return sessionfork.Source{Refusal: sessionfork.CodeOlderTurn}, nil
	}
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok || !terminalForkSupported(rec.Harness) {
		return sessionfork.Source{Refusal: sessionfork.CodeAgentUnsupported}, nil
	}
	nativeID := strings.TrimSpace(rec.Metadata.AgentSessionID)
	if nativeID == "" && rec.Harness == domain.HarnessClaudeCode {
		// Claude terminals pin a session id derived from the AO id at launch.
		nativeID = agentruntime.ClaudeSessionID(string(rec.ID))
	}
	if nativeID == "" {
		return sessionfork.Source{Refusal: sessionfork.CodeNativeMissing}, nil
	}
	source := rec
	// The fork's files are captured when it is planned, but the agent copies
	// its conversation only when it launches, after setup. Freeze notes the
	// latest turn then, and the launch is refused if the original went on.
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
			// Copy the session into the fork's folder first, then resume the
			// copy: Claude finds sessions by the folder it runs in.
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
		// The agent picks the child's id; its hooks report it once it starts.
		return ports.ForkTerminalLaunch{Argv: argv}, nil
	}
	return sessionfork.Source{ContextMode: domain.ForkContextTerminalNative, PrepareTerminal: prepare, Freeze: freeze}, nil
}

// latestTerminalTurn is when the newest turn the prompt hooks recorded for id
// was asked, or zero.
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

// requireNoTurnSince returns ErrForkSourceMovedOn if id is busy, or a turn
// newer than lastTurn was asked in it.
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

// terminalForkArgv turns an agent's restore command for nativeID into its
// fork command. ok is false when the restore command has an unexpected shape.
func terminalForkArgv(harness domain.AgentHarness, restore []string, nativeID string) ([]string, bool) {
	i := slices.Index(restore, nativeID)
	if i < 1 {
		return nil, false
	}
	argv := slices.Clone(restore)
	flag := argv[i-1]
	switch harness {
	case domain.HarnessCodex:
		// codex resume [flags] <id>  →  codex fork [flags] <id>
		if len(argv) < 2 || argv[1] != "resume" {
			return nil, false
		}
		argv[1] = "fork"
	case domain.HarnessContinue:
		// Continue's restore already forks: cn --fork <id>.
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

// forkTerminalOutputLines is how much of the original's terminal a
// text-handoff fork reads.
const forkTerminalOutputLines = 400

// ForkTerminalOutput is the end of a terminal session's screen and
// scrollback, as plain text, or "" when it can't be read.
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
