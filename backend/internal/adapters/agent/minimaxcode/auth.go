package minimaxcode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/OrchestratorInc/agent-orchestrator/backend/internal/process"
)

var authProbeTimeout = 20 * time.Second

// probeStopGrace lets a timed-out mcode run its exit handlers. The CLI holds
// a proper-lockfile lock on the whole profile (<profile>.lock) while it
// migrates; a SIGKILL orphans that lock and every mcode command, the user's
// own included, then waits out its 30-minute staleness window.
const probeStopGrace = 3 * time.Second

var runAuthProbe = func(ctx context.Context, binary string) ([]byte, error) {
	cmd := aoprocess.CommandContext(ctx, binary, "provider", "list", "--json")
	aoprocess.InterruptOnCancel(cmd, probeStopGrace)
	return cmd.Output()
}

// AuthStatus reports configured credentials only. A saved connectivity status
// may be stale; it does not authorize a later provider call.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	dataDir, err := sourceProfile(nil)
	if err != nil {
		dataDir = ""
	}
	// A signed-in account is decided from its state file alone, so a busy or
	// locked CLI cannot hide it behind a probe timeout.
	if oauthAuthStatus(dataDir) == ports.AgentAuthStatusConfigured {
		return ports.AgentAuthStatusConfigured, nil
	}
	// Windows cannot deliver os.Interrupt to mcode.cmd's child CLI. Starting
	// provider list could therefore orphan its profile lock on timeout. Keep
	// ambiguous credentials unknown instead of taking that lock to inspect them.
	if runtime.GOOS == "windows" {
		return localAuthStatus(dataDir), nil
	}
	probe, cancel := context.WithTimeout(ctx, authProbeTimeout)
	defer cancel()
	out, err := runAuthProbe(probe, binary)
	if ctx.Err() != nil {
		return ports.AgentAuthStatusUnknown, ctx.Err()
	}
	if err != nil || probe.Err() != nil {
		return ports.AgentAuthStatusUnknown, nil
	}
	return providerAuthStatus(out, dataDir), nil
}

// LogoutArgs completes `mcode logout` with the account region the user's
// profile last selected. Without --region, mcode logout signs out of
// MAVIS_REGION's account (China by default), not a saved Global login.
func LogoutArgs() []string {
	dataDir, err := sourceProfile(nil)
	if err != nil {
		return nil
	}
	switch savedRegion(dataDir) {
	case "en":
		return []string{"--region", "global"}
	case "cn":
		return []string{"--region", "cn"}
	default:
		return nil
	}
}

// savedRegion reads MiniMax's region preference ({"version":1,"regions":
// {"<buildEnv>":"cn"|"en"}}) for released builds, which use buildEnv prod.
func savedRegion(dataDir string) string {
	path := filepath.Join(dataDir, filepath.FromSlash(nativeRegionPreference))
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var preference struct {
		Version int               `json:"version"`
		Regions map[string]string `json:"regions"`
	}
	if json.Unmarshal(data, &preference) != nil || preference.Version != 1 {
		return ""
	}
	return preference.Regions["prod"]
}

var authCredentialEnvNames = [...]string{
	"MINIMAX_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY",
	"MAVIS_ACCESS_TOKEN", "__MAVIS_PARENT_ACCESS_TOKEN",
}

// localAuthStatus can confirm a credential-free OAuth logout without starting
// MiniMax. BYOK config, alternate credentials, and unreadable state stay unknown.
func localAuthStatus(dataDir string) ports.AgentAuthStatus {
	status := oauthAuthStatus(dataDir)
	if status != ports.AgentAuthStatusUnauthorized {
		return status
	}
	for _, name := range authCredentialEnvNames {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return ports.AgentAuthStatusUnknown
		}
	}
	for _, name := range []string{"codex-auth.json", "local-runtime.auth.json"} {
		if _, err := os.Lstat(filepath.Join(dataDir, name)); !os.IsNotExist(err) {
			return ports.AgentAuthStatusUnknown
		}
	}
	data, err := readPrivateConfig(filepath.Join(dataDir, "config.yaml"))
	if err != nil {
		return ports.AgentAuthStatusUnknown
	}
	var config map[string]any
	if yaml.Unmarshal(data, &config) != nil {
		return ports.AgentAuthStatusUnknown
	}
	for _, key := range []string{"minimax_api", "custom_provider", "provider"} {
		if config[key] != nil {
			return ports.AgentAuthStatusUnknown
		}
	}
	if source, ok := config["minimaxModelSource"]; ok && source != "token_plan" {
		return ports.AgentAuthStatusUnknown
	}
	if model, ok := config["defaultModel"]; ok {
		value, valid := model.(string)
		if !valid || (value != "" && !strings.HasPrefix(value, "minimax/")) {
			return ports.AgentAuthStatusUnknown
		}
	}
	return ports.AgentAuthStatusUnauthorized
}

// providerAuthStatus combines `mcode provider list --json` with MiniMax's
// OAuth state. The list always reports the MiniMax OAuth row without a key,
// and its MiniMax rows are "active" by credential source even when the
// default model is a custom provider, so no single row decides the result.
// Unauthorized is reported only when no credential of any kind is present.
func providerAuthStatus(data []byte, dataDir string) ports.AgentAuthStatus {
	var snapshot struct {
		Providers []struct {
			Kind      string `json:"kind"`
			Active    bool   `json:"active"`
			Enabled   bool   `json:"enabled"`
			HasAPIKey bool   `json:"hasApiKey"`
		} `json:"providers"`
	}
	if json.Unmarshal(data, &snapshot) != nil {
		return ports.AgentAuthStatusUnknown
	}
	minimaxListed, customListed, inactiveKey := false, false, false
	for _, p := range snapshot.Providers {
		if !p.Enabled {
			continue
		}
		if p.Active && p.HasAPIKey {
			return ports.AgentAuthStatusConfigured
		}
		// A saved key on an inactive provider is a credential, not its absence.
		inactiveKey = inactiveKey || p.HasAPIKey
		switch p.Kind {
		case "minimax-oauth", "minimax-api-key":
			minimaxListed = true
		default:
			customListed = true
		}
	}
	oauth := oauthAuthStatus(dataDir)
	switch {
	case oauth == ports.AgentAuthStatusConfigured:
		return ports.AgentAuthStatusConfigured
	case customListed || !minimaxListed || inactiveKey:
		// Custom providers may read their key from an environment variable
		// the list does not report, and an inactive provider's saved key may
		// be selected later, so neither is reported as signed out.
		return ports.AgentAuthStatusUnknown
	default:
		return oauth
	}
}

// oauthAuthStatus reads MiniMax's non-secret OAuth state files:
// <data>/auth/<buildEnv>/<region>/mcode-public/auth-state.json, plus the
// legacy <data>/auth/auth-state.json. Credentials themselves are never read.
func oauthAuthStatus(dataDir string) ports.AgentAuthStatus {
	if dataDir == "" {
		return ports.AgentAuthStatusUnknown
	}
	authHome := filepath.Join(dataDir, "auth")
	paths, err := scopedAuthStatePaths(authHome)
	if err != nil {
		return ports.AgentAuthStatusUnknown
	}
	paths = append(paths, filepath.Join(authHome, "auth-state.json"))
	status := ports.AgentAuthStatusUnauthorized
	for _, path := range paths {
		switch readOAuthState(path) {
		case "authenticated", "refreshing", "scope_upgrade_required":
			return ports.AgentAuthStatusConfigured
		case "", "anonymous", "expired", "logging_out", "logout_pending":
		default:
			// authorizing or error: a login may be in flight or need repair.
			status = ports.AgentAuthStatusUnknown
		}
	}
	return status
}

// scopedAuthStatePaths lists <authHome>/<buildEnv>/<region>/mcode-public/
// auth-state.json files that exist. It reads directories rather than globbing,
// so a profile path containing pattern characters such as [1] stays literal.
// Missing and non-directory entries are skipped; any other inspection error is
// returned, since an unreadable directory may hold a signed-in account.
func scopedAuthStatePaths(authHome string) ([]string, error) {
	buildEnvs, err := readDirIfPresent(authHome)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, buildEnv := range buildEnvs {
		buildEnvDir := filepath.Join(authHome, buildEnv.Name())
		regions, err := readDirIfPresent(buildEnvDir)
		if err != nil {
			return nil, err
		}
		for _, region := range regions {
			regionDir := filepath.Join(buildEnvDir, region.Name())
			if info, err := os.Stat(regionDir); errors.Is(err, os.ErrNotExist) || err == nil && !info.IsDir() {
				continue
			} else if err != nil {
				return nil, err
			}
			path := filepath.Join(regionDir, "mcode-public", "auth-state.json")
			if _, err := os.Lstat(path); err == nil {
				paths = append(paths, path)
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		}
	}
	return paths, nil
}

// readDirIfPresent reads dir, treating a missing path or a non-directory as
// empty, as a glob would.
func readDirIfPresent(dir string) ([]os.DirEntry, error) {
	info, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) || err == nil && !info.IsDir() {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return os.ReadDir(dir)
}

// readOAuthState returns the state file's status, "" when it is absent, and
// "unreadable" when it exists but cannot be read as bounded JSON.
func readOAuthState(path string) string {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return "unreadable"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "unreadable"
	}
	var state struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(data, &state) != nil || state.Status == "" {
		return "unreadable"
	}
	return state.Status
}
