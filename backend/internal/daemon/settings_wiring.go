package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/gateway"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	settingssvc "github.com/aoagents/agent-orchestrator/backend/internal/service/settings"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

// settingsStore adapts the SQLite store to the settings service's Store.
//
// The two define their own snapshot types so neither depends on the other's; this
// is the one place that knows both, keeping the translation in the wiring.
type settingsStore struct{ store *sqlite.Store }

var _ settingssvc.Store = settingsStore{}

func (s settingsStore) GetAppSettings(ctx context.Context) (settingssvc.Snapshot, error) {
	row, err := s.store.GetAppSettings(ctx)
	if err != nil {
		return settingssvc.Snapshot{}, err
	}
	return settingssvc.Snapshot{
		DefaultSessionMode: row.DefaultSessionMode,
		HarnessDefaults:    row.HarnessDefaults,
		CloudOffering:      row.CloudOffering,
		VersionRefresh:     row.VersionRefresh,
		ScmProxyURL:        row.ScmProxyURL,
		UpdatedAt:          row.UpdatedAt,
	}, nil
}

func (s settingsStore) SetDefaultSessionMode(
	ctx context.Context,
	mode domain.SessionMode,
	now time.Time,
) error {
	return s.store.SetDefaultSessionMode(ctx, mode, now)
}

func (s settingsStore) SetCloudOffering(ctx context.Context, enabled bool, now time.Time) error {
	return s.store.SetCloudOffering(ctx, enabled, now)
}

func (s settingsStore) SetVersionRefresh(ctx context.Context, enabled bool, now time.Time) error {
	return s.store.SetVersionRefresh(ctx, enabled, now)
}

func (s settingsStore) SetScmProxyURL(ctx context.Context, url string, now time.Time) error {
	return s.store.SetScmProxyURL(ctx, url, now)
}

func (s settingsStore) SetHarnessDefault(ctx context.Context, agent domain.AgentHarness, value domain.HarnessDefault, now time.Time) error {
	return s.store.SetHarnessDefault(ctx, agent, value, now)
}

// gatewayConfigDir resolves the app-scope Claude settings directory exactly
// the way the resolution chain reads it: CLAUDE_CONFIG_DIR, then the home.
func gatewayConfigDir() (string, error) {
	return agentcreds.ClaudeConfigDir(agentcreds.ResolveOptions{Env: os.Getenv})
}

// newGatewayService builds the write-through gateway service: entries persist
// in the same Claude settings files the resolution chain reads, so the UI,
// the files, and the launch path can never disagree.
func newGatewayService(mgr projectsvc.Manager) *gateway.Service {
	return gateway.New(gatewayConfigDir, projectPathLookup(mgr))
}

// projectPathLookup adapts the project manager to the gateway service's
// project-ID-to-path lookup, keeping neither package importing the other.
func projectPathLookup(mgr projectsvc.Manager) gateway.ProjectLookup {
	return func(ctx context.Context, projectID string) (string, error) {
		result, err := mgr.Get(ctx, domain.ProjectID(projectID))
		if err != nil {
			return "", err
		}
		if result.Project == nil {
			return "", fmt.Errorf("gateway: project %s not found", projectID)
		}
		return result.Project.Path, nil
	}
}

// providerEntryLookup builds the gateway-entry list every launch, relaunch,
// reviewer, and model-discovery path resolves its provider pin — or default
// entry — against. Entries are read from the Claude settings files themselves:
// the app scope from the config directory, each project from its workspace's
// .claude/settings.json. Project rows come from the store because the launch
// wiring needs this lookup before the project service exists (the session
// service and the project service depend on each other).
func providerEntryLookup(store *sqlite.Store) func(ctx context.Context) []agentcreds.GatewayEntry {
	return func(ctx context.Context) []agentcreds.GatewayEntry {
		var out []agentcreds.GatewayEntry
		if dir, err := gatewayConfigDir(); err == nil {
			if baseURL, token, model, ok := gateway.EntryFromSettings(filepath.Join(dir, "settings.json")); ok {
				out = append(out, agentcreds.GatewayEntry{Scope: string(gateway.ScopeApp), BaseURL: baseURL, Token: token, Model: model})
			}
		}
		projects, err := store.ListProjects(ctx)
		if err != nil {
			return out
		}
		for _, project := range projects {
			if project.Path == "" {
				continue
			}
			if baseURL, token, model, ok := gateway.EntryFromSettings(filepath.Join(project.Path, ".claude", "settings.json")); ok {
				out = append(out, agentcreds.GatewayEntry{Scope: string(gateway.ScopeProject), ProjectID: project.ID, BaseURL: baseURL, Token: token, Model: model})
			}
		}
		return out
	}
}
