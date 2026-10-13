package dockersandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/container/dockerreap"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	probeTimeout  = 30 * time.Second
	createTimeout = 10 * time.Minute // `docker run` may pull the image first
)

// dockerFallbacks covers GUI launches whose PATH lacks the Docker CLI.
var dockerFallbacks = []string{
	"/usr/local/bin/docker",
	"/opt/homebrew/bin/docker",
	"/Applications/Docker.app/Contents/Resources/bin/docker",
}

type commandRunner interface {
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := aoprocess.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil && stderr.Len() > 0 {
		return out, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, err
}

// Config locates AO state. Both paths must be under the AO data dir.
type Config struct {
	DataDir     string
	ManagedRoot string // workspace adapter's managed worktree root
}

// Sandbox implements ports.Sandbox with the docker CLI.
type Sandbox struct {
	cfg    Config
	run    commandRunner
	docker string // resolved lazily unless injected by tests

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

var _ ports.Sandbox = (*Sandbox)(nil)

// New returns a docker-CLI sandbox.
func New(cfg Config) *Sandbox { return newWithRunner(cfg, execRunner{}, "") }

func newWithRunner(cfg Config, r commandRunner, docker string) *Sandbox {
	return &Sandbox{cfg: cfg, run: r, docker: docker, locks: map[string]*sync.Mutex{}}
}

func (s *Sandbox) lock(name string) func() {
	s.mu.Lock()
	l, ok := s.locks[name]
	if !ok {
		l = &sync.Mutex{}
		s.locks[name] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func (s *Sandbox) dockerPath() (string, error) {
	if s.docker != "" {
		return s.docker, nil
	}
	if p, err := exec.LookPath("docker"); err == nil {
		return p, nil
	}
	for _, p := range dockerFallbacks {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("%w: the docker CLI was not found; install Docker Desktop or Docker Engine", ports.ErrSandboxUnavailable)
}

func (s *Sandbox) targetFor(container string, project domain.ProjectID, session domain.SessionID) ports.SandboxTarget {
	projectDir := filepath.Join(s.cfg.DataDir, "sandbox", "projects", string(project))
	sessionDir := filepath.Join(projectDir, "sessions", string(session))
	return ports.SandboxTarget{
		Container:  container,
		Docker:     s.docker,
		BinDir:     filepath.Join(s.cfg.DataDir, "sandbox", "bin"),
		ProjectDir: projectDir,
		SessionDir: sessionDir,
		RelayDir:   filepath.Join(sessionDir, "relay"),
	}
}

func (s *Sandbox) mounts(req ports.SandboxRequest, gitDir string) ([]mount, error) {
	work := req.WorkspacePath
	if req.Config.Mode == domain.SandboxModeShared {
		work = filepath.Join(s.cfg.ManagedRoot, string(req.ProjectID))
		rel, err := filepath.Rel(work, req.WorkspacePath)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			return nil, fmt.Errorf("%w: shared sandbox needs an AO-managed worktree under %s, got %s",
				ports.ErrSandboxMisconfigured, work, req.WorkspacePath)
		}
	}
	projectDir := filepath.Join(s.cfg.DataDir, "sandbox", "projects", string(req.ProjectID))
	state := []mount{{Path: projectDir}}
	if req.Config.Mode != domain.SandboxModeShared {
		// Per-agent containers see only their own session dir, not siblings'.
		state = []mount{
			{Path: filepath.Join(projectDir, "home")},
			{Path: filepath.Join(projectDir, "claude")},
			{Path: filepath.Join(projectDir, "sessions", string(req.SessionID))},
		}
	}
	m := []mount{
		{Path: work},
		{Path: gitDir},
		{Path: filepath.Join(s.cfg.DataDir, "prompts"), ReadOnly: true},
	}
	m = append(m, state...)
	return append(m, mount{Path: filepath.Join(s.cfg.DataDir, "sandbox", "bin"), ReadOnly: true}), nil
}

func (s *Sandbox) gitCommonDir(ctx context.Context, workspace string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := s.run.Output(ctx, "git", "-C", workspace, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("%w: resolve git dir for %s: %v", ports.ErrSandboxMisconfigured, workspace, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (s *Sandbox) hashFor(req ports.SandboxRequest, gitDir string) string {
	m, _ := s.mounts(req, gitDir)
	return configHash(req.Config, m)
}

// Ensure returns a running container for req, creating or starting it when
// needed. It is idempotent so restore after a Docker restart works.
func (s *Sandbox) Ensure(ctx context.Context, req ports.SandboxRequest) (ports.SandboxTarget, error) {
	if !req.Config.Enabled() {
		return ports.SandboxTarget{}, fmt.Errorf("%w: sandbox mode is off", ports.ErrSandboxMisconfigured)
	}
	docker, err := s.dockerPath()
	if err != nil {
		return ports.SandboxTarget{}, err
	}
	gitDir, err := s.gitCommonDir(ctx, req.WorkspacePath)
	if err != nil {
		return ports.SandboxTarget{}, err
	}
	mounts, err := s.mounts(req, gitDir)
	if err != nil {
		return ports.SandboxTarget{}, err
	}
	hash := configHash(req.Config, mounts)
	name := containerName(req, hash)
	if req.Config.Mode == domain.SandboxModeShared {
		defer s.lock("project:" + string(req.ProjectID))()
	}
	defer s.lock(name)()

	target := s.targetFor(name, req.ProjectID, req.SessionID)
	target.Docker = docker
	target.Mode = req.Config.Mode
	for _, dir := range []string{filepath.Join(target.ProjectDir, "home"), filepath.Join(target.ProjectDir, "claude"), filepath.Join(target.RelayDir, "req"),
		filepath.Join(target.RelayDir, "work"), filepath.Join(target.RelayDir, "resp"), filepath.Join(s.cfg.DataDir, "prompts")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return ports.SandboxTarget{}, fmt.Errorf("sandbox state dir: %w", err)
		}
	}
	if err := InstallBin(target.BinDir); err != nil {
		return ports.SandboxTarget{}, err
	}

	status, existingHash, err := s.inspect(ctx, docker, name)
	if err != nil {
		return ports.SandboxTarget{}, err
	}
	switch {
	case status != "" && status != "running" && existingHash != hash:
		// A stopped container with stale config is recreated; the worktree
		// lives on the host. A running one may hold a live agent: reuse it.
		if _, err := s.output(ctx, probeTimeout, docker, "rm", "-f", name); err != nil {
			return ports.SandboxTarget{}, err
		}
		fallthrough
	case status == "":
		labels := map[string]string{labelSandbox: string(req.Config.Mode), labelProject: string(req.ProjectID), labelHash: hash, labelGitDir: gitDir}
		if req.Config.Mode == domain.SandboxModePerAgent {
			labels[dockerreap.SessionLabel] = string(req.SessionID)
		}
		if _, err := s.output(ctx, createTimeout, docker, runArgs(name, req.Config, labels, mounts, hostUser())...); err != nil {
			if errors.Is(err, ports.ErrSandboxUnavailable) {
				return ports.SandboxTarget{}, err
			}
			return ports.SandboxTarget{}, fmt.Errorf("%w: start sandbox from image %s (the image must exist or be pullable and provide /bin/sh): %v",
				ports.ErrSandboxMisconfigured, req.Config.Image, err)
		}
	case status != "running":
		if _, err := s.output(ctx, probeTimeout, docker, "start", name); err != nil {
			return ports.SandboxTarget{}, err
		}
	}
	for _, bin := range req.RequireBinaries {
		if _, err := s.output(ctx, probeTimeout, docker, "exec", name, "/bin/sh", "-c", `command -v "$1"`, "sh", bin); err != nil {
			if errors.Is(err, ports.ErrSandboxUnavailable) {
				return ports.SandboxTarget{}, err
			}
			return ports.SandboxTarget{}, fmt.Errorf("%w: image %s has no %q on PATH (and needs /bin/sh): %v",
				ports.ErrSandboxMisconfigured, req.Config.Image, bin, err)
		}
	}
	return target, nil
}

// inspect returns ("", "", nil) when the container does not exist.
func (s *Sandbox) inspect(ctx context.Context, docker, name string) (string, string, error) {
	out, err := s.output(ctx, probeTimeout, docker, "inspect", "--type", "container",
		"--format", `{{.State.Status}}	{{index .Config.Labels "`+labelHash+`"}}`, name)
	if err != nil {
		if msg := strings.ToLower(err.Error()); strings.Contains(msg, "no such object") || strings.Contains(msg, "no such container") {
			return "", "", nil
		}
		return "", "", err
	}
	status, hash, _ := strings.Cut(strings.TrimSpace(string(out)), "\t")
	return status, hash, nil
}

func (s *Sandbox) output(ctx context.Context, timeout time.Duration, docker string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := s.run.Output(ctx, docker, args...)
	if err != nil && (dockerreap.IsDockerUnavailable(err) || dockerSocketDenied(err)) {
		return out, fmt.Errorf("%w: Docker is not running or not reachable: %v", ports.ErrSandboxUnavailable, err)
	}
	return out, err
}

// Exec writes the session env file (0600) and returns the docker exec argv.
func (s *Sandbox) Exec(target ports.SandboxTarget, workspacePath string, env map[string]string, argv []string) ([]string, error) {
	merged := make(map[string]string, len(env)+2)
	for k, v := range env {
		merged[k] = v
	}
	data, err := renderEnvFile(merged)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ports.ErrSandboxMisconfigured, err)
	}
	// Set after rendering so hostOnlyEnv cannot drop them.
	data = append(data, []byte("HOME='"+filepath.Join(target.ProjectDir, "home")+"'\nAO_SANDBOX='1'\nAO_SANDBOX_RELAY='"+target.RelayDir+"'\n")...)
	path := filepath.Join(target.SessionDir, "env")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return nil, fmt.Errorf("write sandbox env: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, fmt.Errorf("install sandbox env: %w", err)
	}
	docker := target.Docker
	if docker == "" {
		if docker, err = s.dockerPath(); err != nil {
			return nil, err
		}
	}
	return execArgv(docker, target, workspacePath, argv), nil
}

// Discard removes a per-agent container after a failed launch. Shared
// containers belong to Release; an unreachable Docker is not an error.
func (s *Sandbox) Discard(ctx context.Context, target ports.SandboxTarget) error {
	if target.Mode != domain.SandboxModePerAgent || target.Container == "" {
		return nil
	}
	defer s.lock(target.Container)()
	docker := target.Docker
	if docker == "" {
		var err error
		if docker, err = s.dockerPath(); err != nil {
			return nil
		}
	}
	if _, err := s.output(ctx, probeTimeout, docker, "rm", "-f", target.Container); err != nil && !errors.Is(err, ports.ErrSandboxUnavailable) {
		return err
	}
	return nil
}

// dockerSocketDenied reports a Docker socket permission error, which means
// the daemon is unusable for this user.
func dockerSocketDenied(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "permission denied") && strings.Contains(msg, "docker")
}

// Release removes the project's shared containers once nothing uses them.
// inUse runs under the project lock, so it cannot race a shared Ensure; an
// error or true keeps the containers. An unreachable Docker is not an error.
func (s *Sandbox) Release(ctx context.Context, project domain.ProjectID, inUse func(context.Context) (bool, error)) error {
	defer s.lock("project:" + string(project))()
	used, err := inUse(ctx)
	if err != nil || used {
		return nil
	}
	docker, err := s.dockerPath()
	if err != nil {
		return nil
	}
	out, err := s.output(ctx, probeTimeout, docker, "ps", "-a", "--filter", "label="+labelProject+"="+string(project),
		"--filter", "label="+labelSandbox+"=shared", "--format", "{{.ID}}")
	if err != nil {
		if errors.Is(err, ports.ErrSandboxUnavailable) {
			return nil
		}
		return err
	}
	var errs []error
	for _, id := range strings.Fields(string(out)) {
		if _, err := s.output(ctx, probeTimeout, docker, "rm", "-f", id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// hostUser runs the container as the host user so files written to the
// worktree keep the user's ownership on Linux.
func hostUser() string {
	return strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())
}
