package omp

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // register sqlite driver for OMP auth database probes

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentAuthChecker = (*Plugin)(nil)

// AuthStatus returns OMP's local authentication status from its credential
// stores. OMP has no non-interactive `auth status` command: unknown positional
// arguments launch its interactive agent and must never be used as probes.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	_, err := p.ResolveBinary(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if status, ok, err := ompLocalAuthStatus(ctx); err != nil {
		return ports.AgentAuthStatusUnknown, err
	} else if ok {
		return status, nil
	}
	return ports.AgentAuthStatusUnknown, nil
}

func ompLocalAuthStatus(ctx context.Context) (ports.AgentAuthStatus, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	configDir, ok := ompConfigDir()
	if !ok {
		return ports.AgentAuthStatusUnknown, false, nil
	}
	dbPath := filepath.Join(configDir, "agent.db")
	if status, ok, err := ompAgentDBAuthStatus(ctx, dbPath); err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	} else if ok {
		if status == ports.AgentAuthStatusAuthorized {
			ports.RecordAuthEvidence(ctx, authprobe.FileSource(dbPath))
		}
		return status, true, nil
	}
	authPath := filepath.Join(configDir, "auth.json")
	status, ok, err := ompAuthJSONStatus(ctx, authPath)
	if status == ports.AgentAuthStatusAuthorized {
		ports.RecordAuthEvidence(ctx, authprobe.FileSource(authPath))
	}
	return status, ok, err
}

func ompConfigDir() (string, bool) {
	if configDir := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); configDir != "" {
		return configDir, true
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return filepath.Join(home, ".omp", "agent"), true
}

type ompAuthEntry struct {
	Type string `json:"type"`
	Key  string `json:"key"`
}

func ompAuthJSONStatus(ctx context.Context, path string) (ports.AgentAuthStatus, bool, error) {
	data, err := authprobe.ReadCredentialFile(ctx, path)
	if os.IsNotExist(err) {
		return ports.AgentAuthStatusUnknown, false, nil
	}
	if err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return ports.AgentAuthStatusUnknown, false, nil
	}

	var entries map[string]ompAuthEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		authprobe.RecordFileDiagnostic(ctx, path, "credential_file_invalid")
		return ports.AgentAuthStatusUnknown, false, err
	}
	if len(entries) == 0 {
		return ports.AgentAuthStatusUnknown, false, nil
	}
	for provider, entry := range entries {
		if strings.TrimSpace(provider) == "" {
			continue
		}
		if strings.TrimSpace(entry.Key) != "" {
			return ports.AgentAuthStatusAuthorized, true, nil
		}
	}
	return ports.AgentAuthStatusUnknown, false, nil
}

func ompAgentDBAuthStatus(ctx context.Context, path string) (status ports.AgentAuthStatus, ok bool, err error) {
	defer func() {
		if err != nil && ctx.Err() == nil {
			authprobe.RecordFileDiagnostic(ctx, path, "credential_store_unreadable")
		}
	}()
	if strings.TrimSpace(path) == "" {
		return ports.AgentAuthStatusUnknown, false, nil
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		authprobe.RecordFileDiagnostic(ctx, path, "credential_file_missing")
		return ports.AgentAuthStatusUnknown, false, nil
	} else if err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}

	db, err := sql.Open("sqlite", path+"?mode=ro")
	if err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	defer func() {
		_ = db.Close()
	}()

	var total int
	var enabled int
	err = db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COUNT(CASE WHEN trim(coalesce(data, '')) <> '' AND disabled_cause IS NULL THEN 1 END)
		FROM auth_credentials
	`).Scan(&total, &enabled)
	if err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	if enabled > 0 {
		return ports.AgentAuthStatusAuthorized, true, nil
	}
	if total > 0 {
		authprobe.RecordFileDiagnostic(ctx, path, "credentials_disabled")
		return ports.AgentAuthStatusUnknown, false, nil
	}
	return ports.AgentAuthStatusUnknown, false, nil
}
