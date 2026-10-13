package kilocode

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"

	_ "modernc.org/sqlite" // register sqlite driver for KiloCode auth database probes
)

var _ ports.AgentAuthChecker = (*Plugin)(nil)

// AuthStatus returns the plugin's local authentication status. Missing
// credentials remain unknown because Kilo can still run eligible public free
// models without a provider login.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	// Kilo's own stored credentials decide first, then its `auth list`, and
	// only then a key in the environment.
	status, ok, storedErr := kilocodeStoredAuthStatus(ctx)
	if storedErr == nil && ok {
		return status, nil
	}
	if storedErr == nil {
		listStatus, listOK, err := kilocodeAuthListAuthStatus(ctx, binary)
		if err != nil {
			return ports.AgentAuthStatusUnknown, err
		}
		if listOK && listStatus == ports.AgentAuthStatusAuthorized {
			return listStatus, nil
		}
		if envStatus, envOK := kilocodeEnvAuthStatus(ctx); envOK {
			return envStatus, nil
		}
		if listOK {
			return listStatus, nil
		}
		return ports.AgentAuthStatusUnknown, nil
	}
	if envStatus, envOK := kilocodeEnvAuthStatus(ctx); envOK {
		return envStatus, nil
	}
	return ports.AgentAuthStatusUnknown, storedErr
}

func kilocodeAuthListAuthStatus(ctx context.Context, binary string) (ports.AgentAuthStatus, bool, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, _ := aoprocess.CommandContext(probeCtx, binary, "auth", "list").CombinedOutput()
	if probeCtx.Err() != nil {
		if probeCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
			return ports.AgentAuthStatusUnknown, false, nil
		}
		return ports.AgentAuthStatusUnknown, false, probeCtx.Err()
	}
	status, ok := kilocodeAuthListStatus(string(out))
	if ok && status == ports.AgentAuthStatusAuthorized {
		ports.RecordAuthEvidence(ctx, domain.CommandSource(binary, "auth", "list"))
	}
	return status, ok, nil
}

var kilocodeAPIKeyEnvVars = []string{
	"KILO_API_KEY",
	"KILOCODE_API_KEY",
	"OPENAI_API_KEY",
	"ANTHROPIC_API_KEY",
	"GEMINI_API_KEY",
	"GOOGLE_API_KEY",
	"OPENROUTER_API_KEY",
	"DEEPSEEK_API_KEY",
	"GROQ_API_KEY",
	"XAI_API_KEY",
	"MISTRAL_API_KEY",
	"COHERE_API_KEY",
}

// kilocodeLocalAuthStatus checks Kilo's stored credentials, then a key in the
// environment.
func kilocodeLocalAuthStatus(ctx context.Context) (ports.AgentAuthStatus, bool, error) {
	status, ok, err := kilocodeStoredAuthStatus(ctx)
	if err == nil && ok {
		return status, true, nil
	}
	if envStatus, envOK := kilocodeEnvAuthStatus(ctx); envOK {
		return envStatus, true, nil
	}
	return ports.AgentAuthStatusUnknown, false, err
}

func kilocodeEnvAuthStatus(ctx context.Context) (ports.AgentAuthStatus, bool) {
	for _, name := range kilocodeAPIKeyEnvVars {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			ports.RecordAuthEvidence(ctx, domain.EnvironmentSource(name).WithMethod(domain.AgentAccountMethodAPIKey))
			return ports.AgentAuthStatusAuthorized, true
		}
	}
	return ports.AgentAuthStatusUnknown, false
}

// kilocodeStoredAuthStatus reads Kilo's auth.json, then its database.
func kilocodeStoredAuthStatus(ctx context.Context) (ports.AgentAuthStatus, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	dataDir, ok := kilocodeDataDir()
	if !ok {
		return ports.AgentAuthStatusUnknown, false, nil
	}
	authPath := filepath.Join(dataDir, "auth.json")
	authorized, known, err := kilocodeAuthJSONStatus(authPath)
	if err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	if known && authorized {
		evidence := domain.FileSource(authPath).WithMethod(authprobe.JSONProviderMethod(authPath))
		evidence.Provider = authprobe.JSONProviderNames(authPath)
		ports.RecordAuthEvidence(ctx, evidence)
		return ports.AgentAuthStatusAuthorized, true, nil
	}
	dbPath := filepath.Join(dataDir, "kilo.db")
	authorized, known, err = kilocodeDBAuthStatus(ctx, dbPath)
	if err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	if known && authorized {
		ports.RecordAuthEvidence(ctx, domain.FileSource(dbPath).WithMethod(domain.AgentAccountMethodAccount))
		return ports.AgentAuthStatusAuthorized, true, nil
	}
	return ports.AgentAuthStatusUnknown, false, nil
}

func kilocodeDataDir() (string, bool) {
	if dataDir := strings.TrimSpace(os.Getenv("KILO_DATA_DIR")); dataDir != "" {
		return dataDir, true
	}
	if dataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dataHome != "" {
		return filepath.Join(dataHome, "kilo"), true
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return filepath.Join(home, ".local", "share", "kilo"), true
}

func kilocodeAuthJSONStatus(path string) (authorized, known bool, err error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return false, false, nil
	}
	var providers map[string]map[string]any
	if err := json.Unmarshal(data, &providers); err != nil {
		return false, false, err
	}
	for _, provider := range providers {
		if len(provider) == 0 {
			continue
		}
		known = true
		for _, key := range []string{"key", "apiKey", "api_key", "access_token", "token"} {
			if strings.TrimSpace(asString(provider[key])) != "" {
				return true, true, nil
			}
		}
	}
	return false, known, nil
}

func asString(value any) string {
	s, _ := value.(string)
	return s
}

func kilocodeDBAuthStatus(ctx context.Context, path string) (authorized, known bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, false, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, false, nil
	} else if err != nil {
		return false, false, err
	}

	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&_pragma=busy_timeout(1000)")
	if err != nil {
		return false, false, err
	}
	defer func() {
		_ = db.Close()
	}()
	return kilocodeDBHasAuthorizedAccount(ctx, db)
}

func kilocodeDBHasAuthorizedAccount(ctx context.Context, db *sql.DB) (authorized, known bool, err error) {
	for _, query := range []string{
		`SELECT COUNT(*) FROM account_state WHERE active_account_id IS NOT NULL AND trim(active_account_id) != ''`,
		`SELECT COUNT(*) FROM account WHERE trim(access_token) != ''`,
		`SELECT COUNT(*) FROM control_account WHERE active = 1 AND trim(access_token) != ''`,
	} {
		var count int
		if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "no such table") {
				continue
			}
			return false, false, err
		}
		known = true
		if count > 0 {
			return true, true, nil
		}
	}
	return false, known, nil
}

var kilocodeAuthListCountRE = regexp.MustCompile(`(?m)\b([1-9][0-9]*)\s+(credentials?|environment variables?)\b`)

func kilocodeAuthListStatus(output string) (ports.AgentAuthStatus, bool) {
	text := strings.ToLower(output)
	if kilocodeAuthListCountRE.MatchString(text) {
		return ports.AgentAuthStatusAuthorized, true
	}
	if strings.Contains(text, "0 credentials") && strings.Contains(text, "0 environment variable") {
		return ports.AgentAuthStatusUnknown, true
	}
	return ports.AgentAuthStatusUnknown, false
}
