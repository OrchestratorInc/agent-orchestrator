package primeagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type primeRestorePlan struct {
	argv       []string
	root       string
	source     string
	workspace  string
	sourceID   string
	targetID   string
	targetPath string
	expected   primeNativeHistory
	before     map[string]bool
	fork       bool
}

// PrepareNativeRestore uses a provider-native fork once, then exact native
// resume. Explicit --cwd keeps the CLI, daemon config and tools in AO's worktree.
func (p *Plugin) PrepareNativeRestore(ctx context.Context, cfg ports.RestoreConfig) (ports.NativeRestoreInitialization, error) {
	id := cfg.Session.Metadata[ports.MetadataKeyAgentSessionID]
	if id == "" && cfg.Session.Metadata[ports.MetadataKeyImportSourceNativeID] == "" {
		return nil, ports.ErrNativeHistoryUnavailable
	}
	root := cfg.Session.Metadata[ports.MetadataKeyImportConfigDir]
	if root == "" {
		var err error
		root, err = sessionDir(cfg.DataDir)
		if err != nil {
			return nil, err
		}
	}
	path, found, err := p.LocateTranscript(ctx, ports.NativeSessionRef{NativeSessionID: id, ConfigDir: root})
	if err != nil {
		return nil, err
	}
	if !found {
		if cfg.Session.Metadata[ports.MetadataKeyImportSourceNativeID] != "" {
			return nil, errors.New("imported prime native history is unavailable")
		}
		return nil, ports.ErrNativeHistoryUnavailable
	}
	workspace, err := filepath.EvalSymlinks(cfg.Session.WorkspacePath)
	if err != nil {
		return nil, err
	}
	argv, err := p.baseCommand(ctx, cfg.DataDir, cfg.SystemPrompt, cfg.SystemPromptFile, cfg.Config)
	if err != nil {
		return nil, err
	}
	// baseCommand's fixed --session-dir is the first option after the binary.
	argv[2] = root
	plan := &primeRestorePlan{root: root, source: path, workspace: workspace, sourceID: id, targetID: id, targetPath: path}
	plan.fork = cfg.Session.Metadata[ports.MetadataKeyImportSourceNativeID] != "" && cfg.Session.Metadata[ports.MetadataKeyImportSourceNativeID] == id
	argv = append(argv, "--cwd", workspace)
	plan.expected, err = readPrimeNativeHistory(ctx, root, path, 0)
	if err != nil {
		return nil, err
	}
	if plan.fork {
		plan.before, err = primeSessionFiles(ctx, root)
		if err != nil {
			return nil, err
		}
		plan.targetID, plan.targetPath = "", ""
		argv = append(argv, "--fork", path)
	} else {
		argv = append(argv, "--resume", path)
	}
	plan.argv = argv
	return plan, nil
}

// Argv returns only the exact native fork/resume invocation.
func (p *primeRestorePlan) Argv() []string { return append([]string(nil), p.argv...) }

// LaunchEnv preserves the pinned native session root for this launch.
func (p *primeRestorePlan) LaunchEnv() map[string]string {
	return map[string]string{"PRIME_AGENT_SESSION_DIR": p.root}
}

// ResumeInput sends no fabricated task or answer.
func (*primeRestorePlan) ResumeInput() string { return "" }

// TargetID returns the exact observed fork id, or the requested resume id.
func (p *primeRestorePlan) TargetID() string { return p.targetID }

// SourceNativeSessionID permits strict S2 takeover only from this source id.
func (p *primeRestorePlan) SourceNativeSessionID() string { return p.sourceID }

// Ready requires native file evidence; the manager also fences the live launch
// and reads its current terminal screen. This taskless plan never sends input.
func (p *primeRestorePlan) Ready(ctx context.Context, _ string, _ string) (bool, error) {
	return p.verify(ctx)
}

// Restored rechecks the exact native identity, history and worktree before adoption.
func (p *primeRestorePlan) Restored(ctx context.Context, _ string, _ string) (bool, error) {
	return p.verify(ctx)
}

func (p *primeRestorePlan) verify(ctx context.Context) (bool, error) {
	if !p.fork {
		history, err := readPrimeNativeHistory(ctx, p.root, p.targetPath, p.expected.count)
		if err != nil {
			return false, err
		}
		if history.id != p.targetID || !primeSamePath(history.cwd, p.workspace) || history.digest != p.expected.digest {
			return false, errors.New("prime native resume did not retain its identity and AO workspace")
		}
		return history.opened, nil
	}
	files, err := primeSessionFiles(ctx, p.root)
	if err != nil {
		return false, err
	}
	var target, targetPath string
	for path := range files {
		if p.before[path] {
			continue
		}
		history, err := readPrimeNativeHistory(ctx, p.root, path, p.expected.count)
		if err != nil || !history.opened || !primeSamePath(history.parent, p.source) || !primeSamePath(history.cwd, p.workspace) || history.count != p.expected.count || history.digest != p.expected.digest {
			continue
		}
		id, err := uuid.Parse(history.id)
		if err != nil || len(history.id) != 36 || id.Version() != 4 || history.id == p.sourceID {
			continue
		}
		if target != "" {
			return false, errors.New("prime native fork identity is ambiguous")
		}
		target, targetPath = history.id, path
	}
	if target == "" {
		return false, nil
	}
	if p.targetID != "" && p.targetID != target {
		return false, errors.New("prime native fork identity changed during initialization")
	}
	p.targetID, p.targetPath = target, targetPath
	return true, nil
}

func primeSamePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	left, err := os.Stat(a)
	if err != nil {
		return false
	}
	right, err := os.Stat(b)
	return err == nil && os.SameFile(left, right)
}
