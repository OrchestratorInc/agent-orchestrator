package main

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/agentconnect"
	"github.com/aoagents/agent-orchestrator/cloud/internal/config"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox/docker"
	"github.com/google/uuid"
)

// dockerAgentLoginRunner is available only for an explicitly selected local
// Docker backend. Its provider has a separate namespace from user workers.
type dockerAgentLoginRunner struct {
	provider  dockerAgentLoginProvider
	publicURL string
}

type dockerAgentLoginProvider interface {
	sandbox.Provider
	DeleteWorkspaceBySession(context.Context, string) (bool, error)
}

var dockerLoginNamespacePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)
var dockerLoginTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

var _ agentconnect.Runner = (*dockerAgentLoginRunner)(nil)
var _ agentconnect.CleanupRunner = (*dockerAgentLoginRunner)(nil)

func newDockerAgentLoginRunner(cfg config.Config) (agentconnect.Runner, error) {
	if cfg.Hosted() || cfg.SandboxProvider != sandbox.ProviderDocker {
		return nil, nil
	}
	namespace, err := dockerAgentLoginNamespace(cfg.DockerNamespace)
	if err != nil {
		return nil, err
	}
	if cfg.NativeAgentLoginEnabled && !validDockerAgentLoginURL(cfg.PublicURL) {
		return nil, errors.New("local native login requires an HTTPS public control-plane URL")
	}
	provider, err := docker.New(docker.Config{Host: cfg.DockerHost, WorkerImage: cfg.DockerWorkerImage, Network: cfg.DockerNetwork, Namespace: namespace})
	if err != nil {
		return nil, errors.New("could not initialize local native login compute")
	}
	return &dockerAgentLoginRunner{provider: provider, publicURL: strings.TrimRight(cfg.PublicURL, "/")}, nil
}

func dockerAgentLoginNamespace(base string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		return "", errors.New("local native login requires a Docker namespace")
	}
	namespace := base + "-auth"
	// Never truncate or replace characters: either can collide with a normal
	// worker namespace or another local development backend.
	if !dockerLoginNamespacePattern.MatchString(namespace) {
		return "", errors.New("local native login Docker namespace is invalid or too long")
	}
	return namespace, nil
}

func validDockerAgentLoginURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Path == "" || u.Path == "/")
}

func (r *dockerAgentLoginRunner) Start(ctx context.Context, launch agentconnect.Launch) (string, error) {
	if uuid.Validate(launch.ID) != nil || (launch.Harness != "codex" && launch.Harness != "claude-code") || !dockerLoginTokenPattern.MatchString(launch.Token) || !launch.ExpiresAt.After(time.Now()) || !validDockerAgentLoginURL(r.publicURL) || r.provider == nil {
		return "", errors.New("invalid local native login launch")
	}
	ctx, cancel := context.WithDeadline(ctx, launch.ExpiresAt)
	defer cancel()
	if ctx.Err() != nil {
		return launch.ID, errors.New("local native login startup was cancelled")
	}
	_, err := r.provider.Create(ctx, sandbox.Spec{
		Name: "ao-login-" + launch.ID, SessionID: launch.ID, OrgID: launch.OrgID,
		Environment: map[string]string{
			"AO_AGENT_LOGIN_ID": launch.ID, "AO_AGENT_LOGIN_TOKEN": launch.Token,
			"AO_CLOUD_PUBLIC_URL": strings.TrimRight(r.publicURL, "/"), "AO_DATA_DIR": "/workspace/.ao/agent-login",
			"PATH": "/usr/local/bin:/usr/bin:/bin",
		},
		Labels: map[string]string{"ao.purpose": "agent-login", "ao.agent_login_id": launch.ID},
	})
	if err != nil {
		return launch.ID, errors.New("could not provision private local native login compute")
	}
	return launch.ID, nil
}

func (r *dockerAgentLoginRunner) Stop(ctx context.Context, handle string) error {
	_, err := r.Cleanup(ctx, handle)
	return err
}

func (r *dockerAgentLoginRunner) Cleanup(ctx context.Context, handle string) (bool, error) {
	if uuid.Validate(handle) != nil || r.provider == nil {
		return false, errors.New("invalid local native login cleanup handle")
	}
	env, found, err := r.provider.FindBySession(ctx, handle)
	if errors.Is(err, sandbox.ErrNotFound) {
		found, err = false, nil
	}
	if err != nil || (found && env.ID == "") {
		return false, errors.New("could not locate private local native login compute")
	}
	if found {
		if err := r.provider.Delete(sandbox.WithoutCleanup(ctx), env.ID); err != nil && !errors.Is(err, sandbox.ErrNotFound) {
			return false, errors.New("could not clean up private local native login compute")
		}
		remaining, err := r.provider.Get(ctx, env.ID)
		if !errors.Is(err, sandbox.ErrNotFound) && !(err == nil && remaining.State == sandbox.StateDeleted) {
			return false, errors.New("local native login compute deletion is not yet confirmed")
		}
	}
	// Container lookup is insufficient: Create can leave only a volume, and
	// Delete can remove compute before a failed volume removal. Recover by the
	// same private UUID even when the container is already gone.
	_, err = r.provider.DeleteWorkspaceBySession(ctx, handle)
	if err != nil {
		return false, errors.New("could not clean up private local native login workspace")
	}
	// A volume-only partial create may still gain a late container. Removing
	// its private state does not confirm that an unfinished launch is quiescent.
	return found, nil
}
