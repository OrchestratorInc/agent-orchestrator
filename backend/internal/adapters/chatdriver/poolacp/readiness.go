package poolacp

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func preflight(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return validatePlatform(runtime.GOOS)
}

func validatePlatform(goos string) error {
	if goos != "darwin" && goos != "linux" {
		return fmt.Errorf("pool Chat requires macOS or Linux for its private instruction hooks; this AO integration does not support %s", goos)
	}
	return nil
}

// validateConfiguration checks only explicit, locally decidable overrides.
// Saved login and remote authorization remain native Pool's responsibility.
func validateConfiguration(env map[string]string) error {
	value := func(key string) string {
		if override, present := env[key]; present {
			return strings.TrimSpace(override)
		}
		return strings.TrimSpace(os.Getenv(key))
	}
	if endpoint := value("POOLSIDE_STANDALONE_BASE_URL"); endpoint != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			return fmt.Errorf("pool POOLSIDE_STANDALONE_BASE_URL must be an absolute HTTP or HTTPS provider base URL; check Pool's installation guide")
		}
	}
	if raw := value("POOLSIDE_STANDALONE_CONTEXT_LENGTH"); raw != "" {
		length, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || length == 0 {
			return fmt.Errorf("pool POOLSIDE_STANDALONE_CONTEXT_LENGTH must be a positive integer within the model server's context window")
		}
	}
	return nil
}

func discoveryLaunch(ctx context.Context, request ports.AgentModelDiscoveryRequest) (acpdriver.Launch, error) {
	if err := preflight(ctx); err != nil {
		return acpdriver.Launch{}, err
	}
	if err := validateConfiguration(request.Env); err != nil {
		return acpdriver.Launch{}, err
	}
	return acpdriver.Launch{Command: request.Binary, Args: []string{"acp"}, Env: request.Env}, nil
}

// DiscoverConfigOptions reads Pool's native model/configuration catalog without
// sending a prompt or installing the per-session standing-instruction overlay.
func DiscoverConfigOptions(ctx context.Context, request ports.AgentModelDiscoveryRequest, log *slog.Logger) ([]ports.ChatConfigOption, error) {
	launch, err := discoveryLaunch(ctx, request)
	if err != nil {
		return nil, err
	}
	return acpdriver.DiscoverConfigOptions(ctx, launch, request.WorkingDir, log)
}
