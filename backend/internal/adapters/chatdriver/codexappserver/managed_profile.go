package codexappserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/processenv"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const managedCodexTokenEnv = "AO_ACCOUNTS_MANAGER_SESSION_TOKEN"

func managedLaunchConfig(ctx context.Context, binary string, cfg ports.ChatStartConfig) (persistenthost.Config, error) {
	var launch persistenthost.Config
	if err := ctx.Err(); err != nil {
		return launch, err
	}
	if cfg.Route == nil || cfg.Env[managedCodexTokenEnv] == "" || strings.ContainsAny(cfg.Env[managedCodexTokenEnv], "\x00\r\n") ||
		cfg.SessionID == "" || cfg.ControllerGeneration == "" || !filepath.IsAbs(cfg.DataDir) || !filepath.IsAbs(cfg.WorkspacePath) || binary == "" {
		return launch, errors.New("managed Codex requires an explicit route and private session profile")
	}
	args, err := codex.ManagedRouteArgs(cfg.Route)
	if err != nil {
		return launch, err
	}
	home, err := managedProfile(cfg.DataDir, string(cfg.SessionID))
	if err != nil {
		return launch, err
	}
	var env []string
	proxyExclusions := map[string]bool{"127.0.0.1": true, "localhost": true}
	for _, entry := range processenv.Merge(cfg.Env) {
		key, value, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(key)
		if upper == "NO_PROXY" {
			for host := range strings.SplitSeq(value, ",") {
				if host = strings.TrimSpace(host); host != "" {
					proxyExclusions[host] = true
				}
			}
			continue
		}
		if strings.HasPrefix(upper, "OPENAI_") || strings.HasPrefix(upper, "CODEX_") || upper == managedCodexTokenEnv {
			continue
		}
		env = append(env, entry)
	}
	exclusions := make([]string, 0, len(proxyExclusions))
	for host := range proxyExclusions {
		exclusions = append(exclusions, host)
	}
	sort.Strings(exclusions)
	// Local route capabilities must not traverse an inherited external proxy.
	env = append(env, "NO_PROXY="+strings.Join(exclusions, ","), "no_proxy="+strings.Join(exclusions, ","),
		"CODEX_HOME="+home, managedCodexTokenEnv+"="+cfg.Env[managedCodexTokenEnv])
	sort.Strings(env)
	ownerGeneration := cfg.ControllerGeneration
	if cfg.Route.BindingRevision > 0 {
		ownerGeneration = "binding-" + strconv.FormatInt(cfg.Route.BindingRevision, 10)
	}
	owner, err := json.Marshal([]string{filepath.Clean(cfg.WorkspacePath), string(cfg.SessionID), ownerGeneration, cfg.ProviderScopeID, home, cfg.Route.BaseURL, "managed-codex-v1"})
	if err != nil {
		return launch, err
	}
	fingerprint := sha256.Sum256(owner)
	return persistenthost.Config{SessionID: string(cfg.SessionID), DataDir: cfg.DataDir, Workdir: cfg.WorkspacePath,
		Protocol: persistenthost.ProtocolManagedRaw, Env: env, Argv: append(append([]string{binary}, args...), "app-server"), OwnershipFingerprint: hex.EncodeToString(fingerprint[:])}, nil
}

func managedProfile(dataDir, session string) (string, error) {
	root, err := os.OpenRoot(dataDir)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	id := sha256.Sum256([]byte(session))
	relative := filepath.Join("managed-codex", hex.EncodeToString(id[:]))
	for _, dir := range []string{"managed-codex", relative} {
		if err := root.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", err
		}
		info, err := root.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !managedHomePrivate(filepath.Join(dataDir, dir), info) {
			return "", errors.New("managed Codex profile is not private")
		}
	}
	return filepath.Join(dataDir, relative), nil
}
