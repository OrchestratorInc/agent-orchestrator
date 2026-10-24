package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/agentconnect"
	"github.com/aoagents/agent-orchestrator/cloud/internal/config"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox/freestyle"
	"github.com/aoagents/agent-orchestrator/cloud/internal/secrets"
	"github.com/google/uuid"
)

type agentLoginVMs interface {
	sandbox.Provider
	ExecRoot(context.Context, sandbox.ID, string, map[string]string, time.Duration) (string, error)
}

// This launcher never touches a normal session. Each native login gets a fresh
// base VM in a separate namespace, no repository checkout, no user bearer, and
// no ordinary worker credential. It only executes the login-only worker mode.
type agentLoginSandboxRunner struct {
	vms             agentLoginVMs
	publicURL       string
	workerHash      string
	defaultSnapshot string
	snapshots       map[string]string
}

func newNativeAgentLogins(cfg config.Config, store *postgres.Store, cipher *secrets.Cipher, binary []byte) (*agentconnect.Service, error) {
	localDocker := !cfg.Hosted() && cfg.SandboxProvider == sandbox.ProviderDocker
	freestyleConfigured := slices.Contains(cfg.AvailableSandboxProviders, sandbox.ProviderFreestyle) && cfg.FreestyleAPIKey != "" && cfg.FreestyleDefaultSnapshot != ""
	configured := localDocker || freestyleConfigured
	if !cfg.NativeAgentLoginEnabled && (store == nil || !configured) {
		return nil, nil
	}
	if store == nil || (cfg.NativeAgentLoginEnabled && (cipher == nil || (!localDocker && len(binary) == 0))) {
		return nil, errors.New("native agent login requires encrypted storage and the current worker build")
	}
	if !configured {
		return nil, errors.New("native agent login requires the managed Freestyle base snapshots")
	}
	if cfg.NativeAgentLoginEnabled && !validLoginControlPlaneURL(cfg.PublicURL) {
		return nil, errors.New("native agent login requires an HTTPS public control-plane URL")
	}
	if localDocker {
		if _, err := dockerAgentLoginNamespace(cfg.DockerNamespace); err != nil && !cfg.NativeAgentLoginEnabled {
			// Such a namespace cannot have created native jobs, but remains
			// valid for ordinary Docker workers. The default-off feature must
			// not impose its longer private namespace on existing deployments.
			return nil, nil
		}
		runner, err := newDockerAgentLoginRunner(cfg)
		if err != nil {
			return nil, err
		}
		return agentconnect.New(agentconnect.Config{Store: store, Cipher: cipher, Runner: runner, MaxSandboxes: cfg.MaxSandboxesPerOrg, DisableNewLogins: !cfg.NativeAgentLoginEnabled}), nil
	}
	hash := sha256.Sum256(binary)
	runner := &agentLoginSandboxRunner{
		vms:       freestyle.New(freestyle.Config{BaseURL: cfg.FreestyleBaseURL, APIKey: cfg.FreestyleAPIKey, DefaultSnapshot: cfg.FreestyleDefaultSnapshot}),
		publicURL: strings.TrimRight(cfg.PublicURL, "/"), workerHash: hex.EncodeToString(hash[:]),
		defaultSnapshot: cfg.FreestyleDefaultSnapshot, snapshots: cfg.FreestyleSnapshotByHarness,
	}
	return agentconnect.New(agentconnect.Config{Store: store, Cipher: cipher, Runner: runner, MaxSandboxes: cfg.MaxSandboxesPerOrg, DisableNewLogins: !cfg.NativeAgentLoginEnabled}), nil
}

func validLoginControlPlaneURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Path == "" || u.Path == "/")
}

func (r *agentLoginSandboxRunner) Start(ctx context.Context, launch agentconnect.Launch) (string, error) {
	if _, err := uuid.Parse(launch.ID); err != nil {
		return "", errors.New("invalid login job identifier")
	}
	if launch.Harness != "codex" && launch.Harness != "claude-code" {
		return "", errors.New("unsupported native login agent")
	}
	if !validLoginControlPlaneURL(r.publicURL) || len(r.workerHash) != 64 || launch.Token == "" || !launch.ExpiresAt.After(time.Now()) {
		return "", errors.New("native login runner configuration is invalid")
	}
	if _, err := hex.DecodeString(r.workerHash); err != nil {
		return "", errors.New("native login worker hash is invalid")
	}
	ctx, cancel := context.WithDeadline(ctx, launch.ExpiresAt)
	defer cancel()
	snapshot := strings.TrimSpace(r.snapshots[launch.Harness])
	if snapshot == "" {
		snapshot = r.defaultSnapshot
	}
	if snapshot == "" {
		return "", errors.New("native login base snapshot is unavailable")
	}
	env, err := r.vms.Create(ctx, sandbox.Spec{
		Name: "ao-login-" + launch.ID, SessionID: "login-" + launch.ID, OrgID: launch.OrgID,
		RootFS: snapshot, AutoDeleteMinutes: 10, AutoPauseSeconds: 600,
		Labels: map[string]string{"ao.agent_login_id": launch.ID, "ao.org_id": launch.OrgID, "ao.purpose": "agent-login"},
	})
	if err != nil {
		return launch.ID, errors.New("could not provision private native login compute")
	}
	for env.State != sandbox.StateRunning {
		if env.State == sandbox.StateFailed || env.State == sandbox.StateDeleted || env.State == sandbox.StateDeleting {
			return launch.ID, errors.New("native login compute could not start")
		}
		select {
		case <-ctx.Done():
			return launch.ID, errors.New("native login compute startup expired")
		case <-time.After(500 * time.Millisecond):
		}
		env, err = r.vms.Get(ctx, env.ID)
		if err != nil {
			return launch.ID, errors.New("could not inspect private native login compute")
		}
	}
	// The native job is installed by verified content hash instead of trusting
	// whatever worker an older snapshot baked. No token is interpolated into
	// the shell command or download URL; only the provider's private env body
	// carries the narrowly-scoped attempt token.
	binaryURL := strings.TrimRight(r.publicURL, "/") + "/api/cloud/v1/worker/binary/" + r.workerHash
	script := "set -eu; umask 077; " +
		"id -u ao-worker >/dev/null 2>&1 || useradd --create-home --home-dir /workspace/.ao/home --shell /bin/bash ao-worker; " +
		"mkdir -p /workspace/.ao/agent-login/bin; chmod 700 /workspace/.ao/agent-login /workspace/.ao/agent-login/bin; " +
		"curl --proto '=https' --fail --silent --show-error --max-time 30 " + loginShellQuote(binaryURL) + " -o /workspace/.ao/agent-login/bin/ao-worker; " +
		"printf '%s  %s\\n' " + loginShellQuote(r.workerHash) + " /workspace/.ao/agent-login/bin/ao-worker | sha256sum -c - >/dev/null; " +
		"chmod 700 /workspace/.ao/agent-login/bin/ao-worker; chown -R ao-worker:ao-worker /workspace/.ao/agent-login; " +
		"setsid nohup runuser --preserve-environment --user ao-worker -- /workspace/.ao/agent-login/bin/ao-worker >/dev/null 2>&1 </dev/null & " +
		"echo AO_AGENT_LOGIN_STARTED"
	out, err := r.vms.ExecRoot(ctx, env.ID, script, map[string]string{
		"AO_CLOUD_PUBLIC_URL": r.publicURL, "AO_AGENT_LOGIN_ID": launch.ID,
		"AO_AGENT_LOGIN_TOKEN": launch.Token, "AO_DATA_DIR": "/workspace/.ao/agent-login",
	}, 40*time.Second)
	if err != nil || !strings.Contains(out, "AO_AGENT_LOGIN_STARTED") {
		return launch.ID, errors.New("could not launch native agent login")
	}
	return launch.ID, nil
}

func (r *agentLoginSandboxRunner) Stop(ctx context.Context, handle string) error {
	_, err := r.Cleanup(ctx, handle)
	return err
}

// Cleanup distinguishes a confirmed deletion from an eventually-consistent
// lookup miss. The service retains unknown provisioning leases on a miss so a
// delayed VM can never lose its durable cleanup record.
func (r *agentLoginSandboxRunner) Cleanup(ctx context.Context, handle string) (bool, error) {
	if _, err := uuid.Parse(handle); err != nil {
		return false, errors.New("invalid native login cleanup handle")
	}
	env, found, err := r.vms.FindBySession(ctx, "login-"+handle)
	if errors.Is(err, sandbox.ErrNotFound) || (err == nil && !found) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("locate native login compute: %w", err)
	}
	// These are disposable authentication VMs, not project workspaces. Normal
	// Delete may return nil while a project's Cleanup hook is still pending.
	if err := r.vms.Delete(sandbox.WithoutCleanup(ctx), env.ID); err != nil {
		if errors.Is(err, sandbox.ErrNotFound) {
			return true, nil
		}
		return false, errors.New("could not clean up native login compute")
	}
	remaining, err := r.vms.Get(ctx, env.ID)
	if errors.Is(err, sandbox.ErrNotFound) || (err == nil && remaining.State == sandbox.StateDeleted) {
		return true, nil
	}
	return false, errors.New("native login compute deletion is not yet confirmed")
}

func loginShellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
