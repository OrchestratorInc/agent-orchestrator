package devimport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	engine "github.com/aoagents/agent-orchestrator/backend/internal/devimport"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

type Store interface {
	engine.Store
}

type SourceStore interface {
	engine.Store
	Close() error
}

type SourceOpener func(ctx context.Context, dataDir string) (SourceStore, error)

type RunInput struct {
	SourceDataDir string
	DryRun        bool
}

type Service interface {
	RunProjects(ctx context.Context, in RunInput) (engine.Report, error)
}

type Deps struct {
	Store         Store
	TargetDataDir string
	OpenSource    SourceOpener
}

type Manager struct {
	store         Store
	targetDataDir string
	openSource    SourceOpener
}

var _ Service = (*Manager)(nil)

func New(deps Deps) *Manager {
	return &Manager{store: deps.Store, targetDataDir: deps.TargetDataDir, openSource: deps.OpenSource}
}

func (m *Manager) RunProjects(ctx context.Context, in RunInput) (engine.Report, error) {
	sourceDataDir, err := resolveDataDir(in.SourceDataDir)
	if err != nil {
		return engine.Report{}, err
	}
	targetDataDir, err := resolveDataDir(m.targetDataDir)
	if err != nil {
		return engine.Report{}, fmt.Errorf("resolve target data dir: %w", err)
	}
	same, err := sameDataDir(sourceDataDir, targetDataDir)
	if err != nil {
		return engine.Report{}, err
	}
	if same {
		return engine.Report{}, apierr.Invalid("DEV_IMPORT_SOURCE_TARGET_SAME",
			"sourceDataDir must be different from the target Orchestrator data dir", map[string]any{"path": sourceDataDir})
	}
	if m.openSource == nil {
		return engine.Report{}, fmt.Errorf("open source store: dependency is nil")
	}

	source, err := m.openSource(ctx, sourceDataDir)
	if err != nil {
		return engine.Report{}, fmt.Errorf("open source store: %w", err)
	}
	defer func() { _ = source.Close() }()

	return engine.Run(ctx, source, m.store, engine.Options{
		SourceDataDir: sourceDataDir,
		TargetDataDir: targetDataDir,
		DryRun:        in.DryRun,
	})
}

func resolveDataDir(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", apierr.Invalid("SOURCE_DATA_DIR_REQUIRED", "sourceDataDir is required", nil)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", apierr.Invalid("INVALID_DATA_DIR", "data dir path is invalid", map[string]any{"path": path})
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return filepath.Clean(resolved), nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return filepath.Clean(abs), nil
	}
	return "", apierr.Invalid("INVALID_DATA_DIR", "data dir path could not be resolved", map[string]any{"path": path})
}

func sameDataDir(a, b string) (bool, error) {
	ai, aErr := os.Stat(a)
	bi, bErr := os.Stat(b)
	if aErr == nil && bErr == nil {
		return os.SameFile(ai, bi), nil
	}
	if aErr != nil && !errors.Is(aErr, os.ErrNotExist) {
		return false, apierr.Invalid("INVALID_DATA_DIR", "sourceDataDir could not be read", map[string]any{"path": a})
	}
	if bErr != nil && !errors.Is(bErr, os.ErrNotExist) {
		return false, apierr.Invalid("INVALID_DATA_DIR", "target data dir could not be read", map[string]any{"path": b})
	}
	return filepath.Clean(a) == filepath.Clean(b), nil
}
