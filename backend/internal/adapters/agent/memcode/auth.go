package memcode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

var memcodeAuthEnvNames = []string{
	"MEMCODE_CREDENTIALS", "MEMCODE_CREDENTIAL_SOURCE", "MEMCODE_API_TOKEN",
	"MEMCODE_ENDPOINT_URL", "ANTHROPIC_API_KEY", "OPENAI_API_KEY",
}

// AuthStatus asks the native CLI about the selected local auth route. Memcode's
// auth status command does not load its global .env, although interactive runs
// do, so supply only the documented auth settings missing from our environment.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	binary, err := p.ResolveBinary(ctx)
	if errors.Is(err, ports.ErrAgentBinaryNotFound) {
		return ports.AgentAuthStatusUnknown, nil
	}
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	settings, err := readMemcodeAuthEnv(memcodeAuthEnvPath())
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	env := os.Environ()
	// Catalog refresh is read-only; the native CLI otherwise checks for a
	// background self-update when invoked.
	env = append(env, "MEMCODE_AUTO_UPDATE=off")
	for _, name := range memcodeAuthEnvNames {
		if _, present := os.LookupEnv(name); !present && settings[name] != "" {
			env = append(env, name+"="+settings[name])
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := process.CommandContext(probeCtx, binary, "auth", "status")
	cmd.Env = env
	out, runErr := cmd.CombinedOutput()
	if status := memcodeAuthStatusFromText(string(out)); status != ports.AgentAuthStatusUnknown {
		if status == ports.AgentAuthStatusUnauthorized || runErr == nil {
			return status, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if os.Getenv("MEMCODE_ENDPOINT_URL") != "" || settings["MEMCODE_ENDPOINT_URL"] != "" ||
		os.Getenv("MEMCODE_API_TOKEN") != "" || settings["MEMCODE_API_TOKEN"] != "" {
		return ports.AgentAuthStatusConfigured, nil
	}
	return ports.AgentAuthStatusUnknown, nil
}

func memcodeAuthEnvPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "memcode", ".env")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "memcode", ".env")
}

func readMemcodeAuthEnv(path string) (map[string]string, error) {
	settings := make(map[string]string)
	if path == "" {
		return settings, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		name, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		for _, allowed := range memcodeAuthEnvNames {
			if name == allowed {
				settings[name] = strings.Trim(strings.TrimSpace(value), `"'`)
				break
			}
		}
	}
	return settings, nil
}

func memcodeAuthStatusFromText(out string) ports.AgentAuthStatus {
	text := strings.ToLower(out)
	if strings.Contains(text, "signed in to a memcode account") ||
		strings.Contains(text, "(active)") ||
		strings.Contains(text, "using a custom endpoint:") ||
		strings.Contains(text, "using your exported anthropic_api_key") ||
		strings.Contains(text, "using your exported openai_api_key") {
		return ports.AgentAuthStatusConfigured
	}
	if strings.Contains(text, "not connected yet") || strings.Contains(text, "attached, but not signed in") {
		return ports.AgentAuthStatusUnauthorized
	}
	return ports.AgentAuthStatusUnknown
}
